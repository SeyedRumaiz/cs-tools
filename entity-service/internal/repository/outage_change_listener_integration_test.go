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

package repository

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestOutageChangeListenerLive needs migration 0186 applied: a committed
// outage update wakes the listener, a quiet period times out WITHOUT breaking
// the connection, and a later update still wakes it.
func TestOutageChangeListenerLive(t *testing.T) {
	dsn := os.Getenv("PG_OUTAGE_TEST_DSN")
	if dsn == "" {
		t.Skip("PG_OUTAGE_TEST_DSN not set; this test needs a real database")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	l := NewOutageChangeListener(pool)
	if err := l.Listen(ctx); err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer l.Close()

	touch := func() {
		t.Helper()
		// A no-op update still fires the statement-level trigger.
		if _, err := pool.Exec(ctx, `UPDATE outage SET name = name WHERE id = (SELECT id FROM outage LIMIT 1)`); err != nil {
			t.Fatalf("touch outage: %v", err)
		}
	}

	touch()
	if ok, err := l.Wait(ctx, 5*time.Second); err != nil || !ok {
		t.Fatalf("after an outage update: notified=%v err=%v, want notified", ok, err)
	}
	if ok, err := l.Wait(ctx, 200*time.Millisecond); err != nil || ok {
		t.Fatalf("quiet period: notified=%v err=%v, want a clean timeout", ok, err)
	}
	touch()
	if ok, err := l.Wait(ctx, 5*time.Second); err != nil || !ok {
		t.Fatalf("after a timeout the listener must still work: notified=%v err=%v", ok, err)
	}

	// A rolled-back write must not wake anyone: NOTIFY is transactional.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE outage SET name = name WHERE id = (SELECT id FROM outage LIMIT 1)`); err != nil {
		t.Fatalf("update in tx: %v", err)
	}
	_ = tx.Rollback(ctx)
	if ok, err := l.Wait(ctx, 300*time.Millisecond); err != nil || ok {
		t.Fatalf("after a rollback: notified=%v err=%v, want no notification", ok, err)
	}
}
