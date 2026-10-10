// Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com).
//
// WSO2 LLC. licenses this file to you under the Apache License,
// Version 2.0 (the "License"); you may not use this file except
// in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

package auth

import (
	"context"
	"crypto/pbkdf2"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/singleflight"
)

// keyLen must match alerts-core's internal/auth.KeyLen: both read the same integration_users rows.
const keyLen = 32

// minIterations/maxIterations bound a row's PBKDF2 iterations, so a bad row can't burn CPU per guess.
const (
	minIterations = 1_000
	maxIterations = 200_000
)

// UsersConfig tunes the in-memory copy of integration_users.
type UsersConfig struct {
	// QueryTimeout bounds one refresh query.
	QueryTimeout time.Duration
	// RefreshInterval is how often the copy is reloaded; a new, disabled or rotated user takes up to this long to apply.
	RefreshInterval time.Duration
	// MaxStale is how long the last good copy keeps serving while refreshes fail; past it every request is ErrUnavailable.
	MaxStale time.Duration
	// CacheTTL is how long a verified secret skips PBKDF2; zero disables it.
	CacheTTL time.Duration
}

// userRow is one integration_users row as the in-memory copy keeps it; users never expire.
type userRow struct {
	hash, salt string
	iterations int
	enabled    bool
}

type usersSnapshot struct {
	users    map[string]userRow
	loadedAt time.Time
}

// IntegrationUsers checks webhooks against an in-memory copy of integration_users, so no request waits on Postgres.
type IntegrationUsers struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
	cfg    UsersConfig

	snap atomic.Pointer[usersSnapshot]

	mu    sync.RWMutex
	cache map[string]cacheEntry
	// verify collapses concurrent checks of one credential into a single PBKDF2 run.
	verify singleflight.Group
}

// cacheEntry holds a verified secret's SHA-256 (never the secret) and the stored hash it matched, so a rotation invalidates it.
type cacheEntry struct {
	digest  [32]byte
	hash    string
	expires time.Time
}

// NewIntegrationUsers returns an empty copy; call Refresh once at startup and Run in a goroutine.
func NewIntegrationUsers(pool *pgxpool.Pool, logger *slog.Logger, cfg UsersConfig) *IntegrationUsers {
	return &IntegrationUsers{pool: pool, logger: logger, cfg: cfg, cache: make(map[string]cacheEntry)}
}

// Refresh reloads every row in one query; a row with a NULL or unusable field is skipped and logged, so it can't block the others.
func (a *IntegrationUsers) Refresh(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, a.cfg.QueryTimeout)
	defer cancel()
	rows, err := a.pool.Query(ctx, `SELECT username, secret_hash, salt, iterations, enabled FROM integration_users`)
	if err != nil {
		return fmt.Errorf("load integration_users: %w", err)
	}
	defer rows.Close()

	users := make(map[string]userRow)
	var skipped []string
	for rows.Next() {
		var (
			username, hash, salt *string
			iterations           *int32
			enabled              *bool
		)
		if err := rows.Scan(&username, &hash, &salt, &iterations, &enabled); err != nil {
			return fmt.Errorf("load integration_users: %w", err)
		}
		if username == nil || hash == nil || salt == nil || iterations == nil || enabled == nil {
			name := "<null>"
			if username != nil {
				name = *username
			}
			skipped = append(skipped, name)
			continue
		}
		users[*username] = userRow{hash: *hash, salt: *salt, iterations: int(*iterations), enabled: *enabled}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("load integration_users: %w", err)
	}
	if len(skipped) > 0 {
		a.logger.Warn("integration_users rows with NULL fields skipped; those users get 401", "usernames", skipped)
	}
	a.snap.Store(&usersSnapshot{users: users, loadedAt: time.Now()})
	return nil
}

// Run refreshes every RefreshInterval until ctx ends; a failed refresh keeps the last good copy.
func (a *IntegrationUsers) Run(ctx context.Context) {
	ticker := time.NewTicker(a.cfg.RefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if err := a.Refresh(ctx); err != nil && ctx.Err() == nil {
			a.logger.Warn("integration_users refresh failed; keeping the last good copy", "error", err, "copy_age", a.age().Round(time.Second).String())
		}
	}
}

// age is how old the current copy is, or -1 before the first load.
func (a *IntegrationUsers) age() time.Duration {
	s := a.snap.Load()
	if s == nil {
		return -1
	}
	return time.Since(s.loadedAt)
}

// Authenticate accepts an enabled user with a matching secret; one credential suits every source.
func (a *IntegrationUsers) Authenticate(r *http.Request, _ string) error {
	username, secret, ok := parseCredentials(r)
	if !ok {
		return ErrUnauthorized
	}
	snap := a.snap.Load()
	if snap == nil {
		return fmt.Errorf("%w: integration_users not loaded yet", ErrUnavailable)
	}
	if age := time.Since(snap.loadedAt); age > a.cfg.MaxStale {
		return fmt.Errorf("%w: integration_users copy is %v old", ErrUnavailable, age.Round(time.Second))
	}
	row, found := snap.users[username]
	if !found || !row.enabled || row.iterations < minIterations || row.iterations > maxIterations {
		return ErrUnauthorized
	}
	if a.cachedHit(username, secret, row.hash) {
		return nil
	}
	digest := sha256.Sum256([]byte(secret))
	// Keyed by the secret's digest and stored hash too, so a wrong secret or a rotated row never shares another check's result.
	key := username + "\x00" + string(digest[:]) + "\x00" + row.hash
	res, _, _ := a.verify.Do(key, func() (any, error) {
		return verifySecret(secret, row.salt, row.hash, row.iterations), nil
	})
	if !res.(bool) {
		return ErrUnauthorized
	}
	a.remember(username, secret, row)
	return nil
}

// cachedHit reports whether username's cached digest matches secret, was verified against the current stored hash, and is still fresh.
func (a *IntegrationUsers) cachedHit(username, secret, hash string) bool {
	if a.cfg.CacheTTL <= 0 {
		return false
	}
	a.mu.RLock()
	e, ok := a.cache[username]
	a.mu.RUnlock()
	if !ok || e.hash != hash || time.Now().After(e.expires) {
		return false
	}
	got := sha256.Sum256([]byte(secret))
	return subtle.ConstantTimeCompare(got[:], e.digest[:]) == 1
}

// remember caches secret for CacheTTL.
func (a *IntegrationUsers) remember(username, secret string, row userRow) {
	if a.cfg.CacheTTL <= 0 {
		return
	}
	expires := time.Now().Add(a.cfg.CacheTTL)
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cache[username] = cacheEntry{digest: sha256.Sum256([]byte(secret)), hash: row.hash, expires: expires}
}

// verifySecret recomputes the PBKDF2-HMAC-SHA256 hash and compares in constant time.
func verifySecret(secret, saltB64, hashB64 string, iterations int) bool {
	salt, err := base64.StdEncoding.DecodeString(saltB64)
	if err != nil {
		return false
	}
	want, err := base64.StdEncoding.DecodeString(hashB64)
	if err != nil {
		return false
	}
	// Stdlib crypto/pbkdf2, same algorithm as alerts-core's x/crypto, so no new dependency.
	got, err := pbkdf2.Key(sha256.New, secret, salt, iterations, keyLen)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}

// parseCredentials reads Bearer base64("user:secret") or Basic; the scheme is case-insensitive (RFC 7235).
func parseCredentials(r *http.Request) (username, secret string, ok bool) {
	const prefix = "bearer "
	if h := r.Header.Get("Authorization"); len(h) > len(prefix) && strings.EqualFold(h[:len(prefix)], prefix) {
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(h[len(prefix):]))
		if err != nil {
			return "", "", false
		}
		username, secret, found := strings.Cut(string(decoded), ":")
		if !found || username == "" || secret == "" {
			return "", "", false
		}
		return username, secret, true
	}
	username, secret, ok = r.BasicAuth()
	if !ok || username == "" || secret == "" {
		return "", "", false
	}
	return username, secret, true
}
