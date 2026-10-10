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

// Package postgres handles Postgres connection setup and schema migration; internal/store and internal/pglock build on the pools it opens.
package postgres

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Config holds PostgreSQL connection settings read from PG* env vars at startup.
type Config struct {
	Host     string `env:"PGHOST,notEmpty"`
	Port     int    `env:"PGPORT" envDefault:"5432"`
	Database string `env:"PGDATABASE,notEmpty"`
	User     string `env:"PGUSER,notEmpty"`
	Password string `env:"PGPASSWORD,notEmpty"`
	// SSLMode defaults to "require", matching Azure Flexible Server's minimum.
	SSLMode string `env:"PGSSLMODE" envDefault:"require"`
	// PoolMaxConns caps this replica's main pool; 0 lets SizePool derive it from poll.concurrency.
	PoolMaxConns int32 `env:"PGPOOLMAXCONNS" envDefault:"0"`
}

// PoolHeadroom is the main pool's room beyond the fold workers, for the claimer, ack flushes, delivery reads, retention and health checks.
const PoolHeadroom = 16

// SizePool derives an unset PGPOOLMAXCONNS from foldWorkers plus PoolHeadroom, and refuses an explicit one below that, since fold workers holding every connection would stall claims, acks and health checks.
func SizePool(cfg Config, foldWorkers int) (Config, error) {
	need := int32(foldWorkers + PoolHeadroom)
	switch {
	case cfg.PoolMaxConns == 0:
		cfg.PoolMaxConns = need
	case cfg.PoolMaxConns < need:
		return Config{}, fmt.Errorf("PGPOOLMAXCONNS=%d is below poll.concurrency (%d) + %d; unset it or set at least %d", cfg.PoolMaxConns, foldWorkers, PoolHeadroom, need)
	}
	return cfg, nil
}

// ConfigFromEnv reads Config from the environment.
func ConfigFromEnv() (Config, error) {
	var cfg Config
	if err := env.Parse(&cfg); err != nil {
		return Config{}, fmt.Errorf("postgres config: %w", err)
	}
	return cfg, nil
}

// connString builds the DSN through url.URL so each part gets its own escaping, keeping passwords with spaces or reserved characters intact.
func connString(cfg Config) string {
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(cfg.User, cfg.Password),
		Host:     net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)),
		Path:     "/" + cfg.Database,
		RawQuery: url.Values{"sslmode": {cfg.SSLMode}}.Encode(),
	}
	return u.String()
}

// Connect opens a pooled connection, bounding connect time and the default per-query timeout; asyncCommit turns off synchronous_commit, which is safe only for writes that are replayed after a crash.
func Connect(cfg Config, connectTimeout, queryTimeout time.Duration, asyncCommit bool) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(connString(cfg))
	if err != nil {
		return nil, fmt.Errorf("parse postgres config: %w", err)
	}
	poolCfg.ConnConfig.ConnectTimeout = connectTimeout
	if asyncCommit {
		poolCfg.ConnConfig.RuntimeParams["synchronous_commit"] = "off"
	}
	if cfg.PoolMaxConns > 0 {
		poolCfg.MaxConns = cfg.PoolMaxConns
	}

	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	pingCtx, pingCancel := context.WithTimeout(context.Background(), queryTimeout)
	defer pingCancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return pool, nil
}

// migrationLockKey serialises concurrent replica startups so parallel CREATE INDEX IF NOT EXISTS calls can't collide.
const migrationLockKey = 7426100331

// Migrate applies schemaSQL in one transaction under an advisory lock; safe to run on every startup.
func Migrate(ctx context.Context, pool *pgxpool.Pool, schemaSQL string) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("migrate: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", migrationLockKey); err != nil {
		return fmt.Errorf("migrate: lock: %w", err)
	}
	if _, err := tx.Exec(ctx, schemaSQL); err != nil {
		return fmt.Errorf("migrate: apply schema: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("migrate: commit: %w", err)
	}
	return nil
}
