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

// Package pglock gives cross-replica mutual exclusion via Postgres session-level advisory locks, so no leader election is needed.
package pglock

import (
	"context"
	"database/sql"
	"fmt"
)

// Locker hands out cross-replica mutual exclusion over its own dedicated *sql.DB, kept separate from the main data pool.
type Locker struct {
	db *sql.DB
}

// New wraps db, bridged from a dedicated pgxpool via stdlib.OpenDBFromPool and sized to the locks this replica holds at once.
func New(db *sql.DB) *Locker {
	return &Locker{db: db}
}

// TryLock takes the session advisory lock for key across every replica without waiting; ok=false means another session holds it, and callers must call unlock exactly once.
func (l *Locker) TryLock(ctx context.Context, key string) (unlock func(), ok bool, err error) {
	conn, err := l.db.Conn(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("acquire lock connection for %q: %w", key, err)
	}
	if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1, 0))`, key).Scan(&ok); err != nil {
		conn.Close()
		return nil, false, fmt.Errorf("try advisory lock for %q: %w", key, err)
	}
	if !ok {
		conn.Close()
		return nil, false, nil
	}
	return releaser(conn, key), true, nil
}

func releaser(conn *sql.Conn, key string) func() {
	return func() {
		// Detached from ctx so a cancelled caller still releases the lock.
		_, _ = conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, key)
		conn.Close()
	}
}
