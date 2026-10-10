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

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestOutageSweepLockLive: a second sweep cannot take a held lock, the two
// flows' locks are independent, and release frees it.
func TestOutageSweepLockLive(t *testing.T) {
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
	notif := NewOutageNotificationRepository(pool)
	comm := NewOutageCommunicationRepository(pool)

	release, ok, err := notif.TryLockSweep(ctx)
	if err != nil || !ok {
		t.Fatalf("first lock: ok=%v err=%v", ok, err)
	}
	if _, ok2, err := notif.TryLockSweep(ctx); err != nil || ok2 {
		t.Fatalf("second notification sweep took a held lock: ok=%v err=%v", ok2, err)
	}
	commRelease, ok3, err := comm.TryLockSweep(ctx)
	if err != nil || !ok3 {
		t.Fatalf("the communication sweep must not wait on the notification one: ok=%v err=%v", ok3, err)
	}
	commRelease()
	release()
	again, ok4, err := notif.TryLockSweep(ctx)
	if err != nil || !ok4 {
		t.Fatalf("lock not freed by release: ok=%v err=%v", ok4, err)
	}
	again()
}
