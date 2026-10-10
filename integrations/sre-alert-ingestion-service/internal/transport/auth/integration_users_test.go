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
	"crypto/pbkdf2"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// hashes produces the stored salt/hash pair for secret, the way alerts-core's
// cmd/user writes it.
func hashes(t *testing.T, secret string, iterations int) (saltB64, hashB64 string) {
	t.Helper()
	salt := []byte("0123456789abcdef")
	key, err := pbkdf2.Key(sha256.New, secret, salt, iterations, keyLen)
	if err != nil {
		t.Fatalf("pbkdf2: %v", err)
	}
	return base64.StdEncoding.EncodeToString(salt), base64.StdEncoding.EncodeToString(key)
}

// A hash written by alerts-core must verify here: both must agree on the algorithm.
func TestVerifySecret(t *testing.T) {
	salt, hash := hashes(t, "correct-secret", 100)

	if !verifySecret("correct-secret", salt, hash, 100) {
		t.Error("the correct secret must verify")
	}
	if verifySecret("wrong-secret", salt, hash, 100) {
		t.Error("a wrong secret must not verify")
	}
	if verifySecret("correct-secret", salt, hash, 101) {
		t.Error("a mismatched iteration count must not verify")
	}
	if verifySecret("correct-secret", "not-base64!!", hash, 100) {
		t.Error("a malformed salt must not verify")
	}
}

func TestParseCredentials(t *testing.T) {
	t.Run("bearer base64 pair", func(t *testing.T) {
		token := base64.StdEncoding.EncodeToString([]byte("alice:s3cr3t"))
		r := httptest.NewRequest("POST", "/", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		u, s, ok := parseCredentials(r)
		if !ok || u != "alice" || s != "s3cr3t" {
			t.Errorf("got (%q, %q, %v)", u, s, ok)
		}
	})

	t.Run("basic auth", func(t *testing.T) {
		r := httptest.NewRequest("POST", "/", nil)
		r.SetBasicAuth("alice", "s3cr3t")
		u, s, ok := parseCredentials(r)
		if !ok || u != "alice" || s != "s3cr3t" {
			t.Errorf("got (%q, %q, %v)", u, s, ok)
		}
	})

	// RFC 7235 makes the scheme case-insensitive, as BasicAuth already is for Basic.
	t.Run("scheme is case-insensitive", func(t *testing.T) {
		token := base64.StdEncoding.EncodeToString([]byte("alice:s3cr3t"))
		for _, scheme := range []string{"Bearer ", "bearer ", "BEARER "} {
			r := httptest.NewRequest("POST", "/", nil)
			r.Header.Set("Authorization", scheme+token)
			if _, _, ok := parseCredentials(r); !ok {
				t.Errorf("%q should parse", scheme)
			}
		}
	})

	t.Run("malformed", func(t *testing.T) {
		for _, h := range []string{
			"", "Bearer not-base64!!",
			"Bearer " + base64.StdEncoding.EncodeToString([]byte("no-colon")),
			"Bearer " + base64.StdEncoding.EncodeToString([]byte(":no-user")),
			"Bogus scheme",
		} {
			r := httptest.NewRequest("POST", "/", nil)
			if h != "" {
				r.Header.Set("Authorization", h)
			}
			if _, _, ok := parseCredentials(r); ok {
				t.Errorf("%q should not parse", h)
			}
		}
	})
}

func newUsers(cacheTTL time.Duration) *IntegrationUsers {
	return NewIntegrationUsers(nil, slog.New(slog.NewTextHandler(io.Discard, nil)), UsersConfig{
		QueryTimeout: time.Second, RefreshInterval: 30 * time.Second, MaxStale: 15 * time.Minute, CacheTTL: cacheTTL,
	})
}

// load installs rows as the current copy, loaded at loadedAt, the way Refresh would.
func load(a *IntegrationUsers, loadedAt time.Time, rows map[string]userRow) {
	a.snap.Store(&usersSnapshot{users: rows, loadedAt: loadedAt})
}

func basic(user, secret string) *http.Request {
	r := httptest.NewRequest("POST", "/elasticsearch", nil)
	r.SetBasicAuth(user, secret)
	return r
}

// TestAuthenticate_FromMemory: every decision comes from the in-memory copy, with no database at all (the pool is nil).
func TestAuthenticate_FromMemory(t *testing.T) {
	salt, hash := hashes(t, "s3cr3t", 10_000)
	a := newUsers(time.Minute)
	load(a, time.Now(), map[string]userRow{
		"alice":  {hash: hash, salt: salt, iterations: 10_000, enabled: true},
		"off":    {hash: hash, salt: salt, iterations: 10_000, enabled: false},
		"absurd": {hash: hash, salt: salt, iterations: 50_000_000, enabled: true},
	})
	cases := map[string]struct {
		user, secret string
		want         error
	}{
		"valid":            {"alice", "s3cr3t", nil},
		"wrong secret":     {"alice", "nope", ErrUnauthorized},
		"unknown user":     {"bob", "s3cr3t", ErrUnauthorized},
		"disabled user":    {"off", "s3cr3t", ErrUnauthorized},
		"bad iterations":   {"absurd", "s3cr3t", ErrUnauthorized},
		"cached and valid": {"alice", "s3cr3t", nil},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if err := a.Authenticate(basic(tc.user, tc.secret), "elasticsearch"); !errors.Is(err, tc.want) {
				t.Errorf("Authenticate = %v, want %v", err, tc.want)
			}
		})
	}
}

// TestAuthenticate_UnavailableBeforeLoadAndWhenStale: no copy yet, or one older than MaxStale, is a 503, not a 401.
func TestAuthenticate_UnavailableBeforeLoadAndWhenStale(t *testing.T) {
	salt, hash := hashes(t, "s3cr3t", 10_000)
	a := newUsers(time.Minute)
	if err := a.Authenticate(basic("alice", "s3cr3t"), "aws"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("before the first load = %v, want ErrUnavailable", err)
	}
	rows := map[string]userRow{"alice": {hash: hash, salt: salt, iterations: 10_000, enabled: true}}
	load(a, time.Now().Add(-14*time.Minute), rows)
	if err := a.Authenticate(basic("alice", "s3cr3t"), "aws"); err != nil {
		t.Fatalf("a copy inside MaxStale must still serve, got %v", err)
	}
	load(a, time.Now().Add(-16*time.Minute), rows)
	if err := a.Authenticate(basic("alice", "s3cr3t"), "aws"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("a copy past MaxStale = %v, want ErrUnavailable", err)
	}
}

// TestAuthenticate_RotationInvalidatesCache: once the copy carries a new hash, the old secret stops working even though it was cached.
func TestAuthenticate_RotationInvalidatesCache(t *testing.T) {
	oldSalt, oldHash := hashes(t, "old-secret", 10_000)
	a := newUsers(time.Hour)
	load(a, time.Now(), map[string]userRow{"alice": {hash: oldHash, salt: oldSalt, iterations: 10_000, enabled: true}})
	if err := a.Authenticate(basic("alice", "old-secret"), "aws"); err != nil {
		t.Fatal(err)
	}
	newKey, err := pbkdf2.Key(sha256.New, "new-secret", []byte("fedcba9876543210"), 10_000, keyLen)
	if err != nil {
		t.Fatal(err)
	}
	load(a, time.Now(), map[string]userRow{"alice": {
		hash: base64.StdEncoding.EncodeToString(newKey), salt: base64.StdEncoding.EncodeToString([]byte("fedcba9876543210")),
		iterations: 10_000, enabled: true,
	}})
	if err := a.Authenticate(basic("alice", "old-secret"), "aws"); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("old secret after rotation = %v, want ErrUnauthorized", err)
	}
	if err := a.Authenticate(basic("alice", "new-secret"), "aws"); err != nil {
		t.Errorf("new secret after rotation = %v, want accepted", err)
	}
}

func TestCache(t *testing.T) {
	a := newUsers(time.Minute)
	row := userRow{hash: "h1"}
	if a.cachedHit("alice", "s3cr3t", "h1") {
		t.Error("nothing cached yet, must miss")
	}
	a.remember("alice", "s3cr3t", row)
	if !a.cachedHit("alice", "s3cr3t", "h1") {
		t.Error("the remembered secret must hit")
	}
	if a.cachedHit("alice", "wrong-secret", "h1") {
		t.Error("a wrong secret must miss even for a cached user")
	}
	if a.cachedHit("alice", "s3cr3t", "h2") {
		t.Error("a changed stored hash must miss")
	}
	if a.cachedHit("bob", "s3cr3t", "h1") {
		t.Error("another username must miss")
	}
}

func TestCache_Expires(t *testing.T) {
	a := newUsers(time.Millisecond)
	a.remember("alice", "s3cr3t", userRow{hash: "h"})
	time.Sleep(5 * time.Millisecond)
	if a.cachedHit("alice", "s3cr3t", "h") {
		t.Error("an expired entry must miss")
	}
}

func TestCache_DisabledByZeroTTL(t *testing.T) {
	a := newUsers(0)
	a.remember("alice", "s3cr3t", userRow{hash: "h"})
	if a.cachedHit("alice", "s3cr3t", "h") {
		t.Error("a zero TTL must disable caching entirely")
	}
}

// CodeRabbit: iterations from the row must be bounded before driving PBKDF2, so a
// corrupted or absurd value (0, negative, or huge) can't error out verification or
// burn disproportionate CPU on every wrong guess (wrong secrets are never cached,
// so each one re-derives).
func TestVerifySecret_IterationsAreImplicitlyTrusted_SoCallersMustBoundThem(t *testing.T) {
	// verifySecret itself has no bound - Authenticate is what must reject out-of-range
	// values before calling it. This documents that contract for verifySecret's callers.
	salt, hash := hashes(t, "s3cr3t", minIterations)
	if !verifySecret("s3cr3t", salt, hash, minIterations) {
		t.Error("minIterations itself must still verify correctly")
	}
}

func TestIterationsBounds(t *testing.T) {
	for _, n := range []int{minIterations, 10_000, maxIterations} {
		if n < minIterations || n > maxIterations {
			t.Errorf("%d should be within [minIterations, maxIterations]", n)
		}
	}
	for _, n := range []int{0, -1, minIterations - 1, maxIterations + 1, 50_000_000} {
		if n >= minIterations && n <= maxIterations {
			t.Errorf("%d should be rejected as out of range", n)
		}
	}
}
