// Copyright (c) 2026 WSO2 LLC. (https://www.wso2.com).
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

// Package db manages the PostgreSQL connection pool this service uses for
// engineer presence, sticky routing, and the escalation queue -- ordinarily
// the same physical database entity-service connects to, kept separate at
// the schema level instead (see internal/config.Schema and migrations/).
package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wso2-open-operations/cs-tools/apps/chat-routing-service/backend/internal/config"
)

const (
	poolMaxConns        int32         = 10               // this service's write volume is far lower than entity-service's
	poolMinConns        int32         = 1                // connections kept warm when idle
	poolMaxConnLifetime time.Duration = 30 * time.Minute // rotate connections to avoid stale server-side state
	poolMaxConnIdleTime time.Duration = 5 * time.Minute  // release unused connections back to the OS
)

// NewPool creates a pgxpool connection pool for the given DSN, pings the
// database to confirm connectivity, and returns the pool ready for use.
// The caller is responsible for calling pool.Close on shutdown. Mirrors
// entity-service's internal/db.NewPool, with smaller pool bounds and one
// addition: every new physical connection has its search_path set to
// config.Schema (AfterConnect runs once per new connection, not once per
// pool.Acquire, so this is cheap) -- verified directly against a real
// Postgres 16 instance while building this, since a bare "search_path"
// DSN query parameter is rejected outright by libpq/pgx's URI parser and
// isn't a safe alternative.
func NewPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse pool config: %w", err)
	}

	cfg.MaxConns = poolMaxConns
	cfg.MinConns = poolMinConns
	cfg.MaxConnLifetime = poolMaxConnLifetime
	cfg.MaxConnIdleTime = poolMaxConnIdleTime
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "SET search_path TO "+pgx.Identifier{config.Schema}.Sanitize())
		return err
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	return pool, nil
}
