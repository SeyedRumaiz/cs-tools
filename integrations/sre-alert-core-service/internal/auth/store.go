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
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrUserNotFound marks a lookup for a username with no row in integration_users.
var ErrUserNotFound = errors.New("internal user not found")

// userColumns excludes "id": new rows get one from the column default (gen_random_uuid()),
// existing rows keep theirs untouched since Upsert's ON CONFLICT clause never sets it.
var userColumns = []string{
	"username", "secret_hash", "salt", "iterations", "enabled",
	"created_at", "created_by", "updated_at", "secret_rotated_at", "last_used_at",
}

var allUserColumns = append([]string{"id"}, userColumns...)

// UserRepo owns the integration_users table.
type UserRepo struct {
	pool *pgxpool.Pool
}

// NewUserRepo wraps pool for internal-user reads and provisioning.
func NewUserRepo(pool *pgxpool.Pool) *UserRepo {
	return &UserRepo{pool: pool}
}

// Get reads a user by username; returns ErrUserNotFound if no row exists.
func (r *UserRepo) Get(ctx context.Context, username string) (User, error) {
	query := fmt.Sprintf(`SELECT %s FROM integration_users WHERE username = $1`, columnList(allUserColumns))
	rows, err := r.pool.Query(ctx, query, username)
	if err != nil {
		return User{}, fmt.Errorf("read internal user %s: %w", username, err)
	}
	u, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[User])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, fmt.Errorf("read internal user %s: %w", username, ErrUserNotFound)
		}
		return User{}, fmt.Errorf("read internal user %s: %w", username, err)
	}
	return u, nil
}

// Upsert inserts a new user (id defaults via gen_random_uuid()) or, for an existing username,
// replaces every column except id/created_at; used by the createuser CLI tool.
func (r *UserRepo) Upsert(ctx context.Context, u User) error {
	query := fmt.Sprintf(`INSERT INTO integration_users (%s) VALUES (%s)
		ON CONFLICT (username) DO UPDATE SET
			secret_hash = EXCLUDED.secret_hash, salt = EXCLUDED.salt, iterations = EXCLUDED.iterations,
			enabled = EXCLUDED.enabled, created_by = EXCLUDED.created_by, updated_at = EXCLUDED.updated_at,
			secret_rotated_at = EXCLUDED.secret_rotated_at, last_used_at = EXCLUDED.last_used_at`,
		columnList(userColumns), placeholders(len(userColumns)))
	args := []any{
		u.Username, u.SecretHash, u.Salt, u.Iterations, u.Enabled,
		u.CreatedAt, u.CreatedBy, u.UpdatedAt, u.SecretRotatedAt, u.LastUsedAt,
	}
	if _, err := r.pool.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("upsert internal user %s: %w", u.Username, err)
	}
	return nil
}

// List reads every row in integration_users; the table is small (service accounts only), so a full scan is fine.
func (r *UserRepo) List(ctx context.Context) ([]User, error) {
	query := fmt.Sprintf(`SELECT %s FROM integration_users`, columnList(allUserColumns))
	rows, err := r.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list internal users: %w", err)
	}
	users, err := pgx.CollectRows(rows, pgx.RowToStructByName[User])
	if err != nil {
		return nil, fmt.Errorf("list internal users: %w", err)
	}
	return users, nil
}

// SetEnabled flips a user's enabled flag and bumps updated_at; used to disable/re-enable an account without deleting its row.
func (r *UserRepo) SetEnabled(ctx context.Context, username string, enabled bool) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE integration_users SET enabled = $1, updated_at = $2 WHERE username = $3`,
		enabled, time.Now().UTC(), username,
	)
	if err != nil {
		return fmt.Errorf("set enabled=%t for internal user %s: %w", enabled, username, err)
	}
	return nil
}

func columnList(cols []string) string {
	s := ""
	for i, c := range cols {
		if i > 0 {
			s += ", "
		}
		s += c
	}
	return s
}

func placeholders(n int) string {
	s := ""
	for i := 1; i <= n; i++ {
		if i > 1 {
			s += ","
		}
		s += fmt.Sprintf("$%d", i)
	}
	return s
}
