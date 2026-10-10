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

package service

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/repository"
)

// Cloud status, against a real database.
//
// Skipped unless CLOUD_STATUS_TEST_DSN is set, so `go test ./...` stays
// hermetic. The unit tests next door cover the decision logic with fakes;
// what this covers is the part fakes cannot — that the SQL is valid, that
// the scope filter's three-table join actually resolves, and that the
// enum casts Postgres applies are the ones the code expects.
//
// The last of those is not hypothetical: an earlier port in this effort
// shipped a cast that Postgres types even in an untaken CASE branch, and it
// only surfaced on a live sweep.
//
//	CLOUD_STATUS_TEST_DSN='postgres://…' \
//	CLOUD_STATUS_TEST_SERVICE_IDS='62d30e53-…' \
//	go test ./internal/service/ -run TestIntegrationCloudStatus -v
//
// It expects the fixture from scripts/seed_cloud_status_test.sql: one
// ongoing outage of type OUTAGE with two affected CIs.
const (
	dsnEnv        = "CLOUD_STATUS_TEST_DSN"
	serviceIDsEnv = "CLOUD_STATUS_TEST_SERVICE_IDS"

	fixtureOutageID = "aaaaaaaa-0000-4000-8000-000000000001"
	fixtureNumber   = "OUT-CSTEST-1"
)

func cloudStatusTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv(dsnEnv)
	if dsn == "" {
		t.Skipf("%s not set; skipping the live cloud status sweep", dsnEnv)
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatalf("ping: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// TestIntegrationCloudStatusSweep drives Sweep, PendingWebhooks and
// RecordDelivery against a real database, in the order the scheduled task
// calls them.
func TestIntegrationCloudStatusSweep(t *testing.T) {
	pool := cloudStatusTestPool(t)
	scope := os.Getenv(serviceIDsEnv)
	if scope == "" {
		t.Fatalf("%s must name the service the fixture sits under, so the sweep "+
			"does not touch every other in-scope outage", serviceIDsEnv)
	}

	repo := repository.NewCloudStatusRepository(pool)
	svc := NewCloudStatusService(repo, []string{scope})
	ctx := context.Background()

	// ── the ongoing arm ────────────────────────────────────────────────
	first, err := svc.Sweep(ctx)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	t.Logf("sweep 1: scanned=%d recorded=%d monitorsUpdated=%d skippedNoCloud=%d unknownType=%d",
		first.Scanned, first.Recorded, first.MonitorsUpdated, first.SkippedNoCloud, first.UnknownOutageType)

	if first.Scanned == 0 {
		t.Fatal("the fixture outage was not in scope; check CLOUD_STATUS_TEST_SERVICE_IDS")
	}
	if first.Recorded < 1 {
		t.Error("the fixture's transition should have been recorded")
	}

	// The two affected CIs both carry a cloud monitor, so both should move.
	if first.MonitorsUpdated < 2 {
		t.Errorf("monitorsUpdated = %d, want at least 2 (the fixture's two affected CIs)",
			first.MonitorsUpdated)
	}

	assertMonitorStatus(t, pool, domain.CloudMonitorStatusPartialOutage)

	// ── the pending read ───────────────────────────────────────────────
	pending, err := svc.PendingWebhooks(ctx)
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	var found *domain.PendingCloudStatusWebhook
	for i := range pending.Webhooks {
		if pending.Webhooks[i].OutageID == fixtureOutageID {
			found = &pending.Webhooks[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("the fixture's webhook is not pending; got %d others", pending.Count)
	}
	t.Logf("pending: number=%s event=%s cloud=%s timestamp=%s",
		found.Number, found.Event, found.Cloud, found.Timestamp)

	if found.Event != domain.CloudStatusEventOutageBegin {
		t.Errorf("event = %s, want OUTAGE_BEGIN for an ongoing outage", found.Event)
	}
	// The slug, not the stored enum spelling -- this is what goes on the wire.
	if found.Cloud != "devant" {
		t.Errorf("cloud = %q, want the lowercase slug %q", found.Cloud, "devant")
	}
	if found.Number != fixtureNumber {
		t.Errorf("number = %q, want %q", found.Number, fixtureNumber)
	}
	if found.Timestamp == "" {
		t.Error("timestamp must carry the outage's begin instant")
	}

	// ── idempotence ────────────────────────────────────────────────────
	// The steady state, and the property ServiceNow did not have: a repeat
	// sweep records nothing new and rewrites no monitor already correct.
	second, err := svc.Sweep(ctx)
	if err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	t.Logf("sweep 2: scanned=%d recorded=%d monitorsUpdated=%d",
		second.Scanned, second.Recorded, second.MonitorsUpdated)

	if second.Recorded != 0 {
		t.Errorf("a repeat sweep recorded %d transitions, want 0", second.Recorded)
	}
	if second.MonitorsUpdated != 0 {
		t.Errorf("a repeat sweep rewrote %d monitors, want 0 -- already-correct rows must be skipped",
			second.MonitorsUpdated)
	}

	// ── the delivery report ────────────────────────────────────────────
	if err := svc.RecordDelivery(ctx, domain.RecordCloudStatusDeliveryRequest{
		ID: found.ID, Delivered: true,
	}); err != nil {
		t.Fatalf("record delivery: %v", err)
	}
	after, err := svc.PendingWebhooks(ctx)
	if err != nil {
		t.Fatalf("pending after delivery: %v", err)
	}
	for _, w := range after.Webhooks {
		if w.OutageID == fixtureOutageID {
			t.Error("a delivered webhook must stop being handed out")
		}
	}
}

// TestIntegrationCloudStatusEndArm closes the outage and sweeps again, which
// is the completed arm: a second event, and the monitors returning to
// Operational.
//
// Ordered after the sweep test by name, and it mutates the fixture, so run
// the two together or not at all.
func TestIntegrationCloudStatusEndArm(t *testing.T) {
	pool := cloudStatusTestPool(t)
	scope := os.Getenv(serviceIDsEnv)
	if scope == "" {
		t.Skip("no scope configured")
	}
	ctx := context.Background()

	// Close the fixture outage. This stands in for what ServiceNow's sync
	// would bring across once someone sets the end time.
	if _, err := pool.Exec(ctx,
		`UPDATE outage SET end_on = NOW(), updated_on = NOW() WHERE id = $1::uuid`,
		fixtureOutageID); err != nil {
		t.Fatalf("close the fixture outage: %v", err)
	}

	svc := NewCloudStatusService(repository.NewCloudStatusRepository(pool), []string{scope})

	res, err := svc.Sweep(ctx)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	t.Logf("end-arm sweep: scanned=%d recorded=%d monitorsUpdated=%d",
		res.Scanned, res.Recorded, res.MonitorsUpdated)

	if res.Recorded < 1 {
		t.Error("closing the outage should record an OUTAGE_END transition")
	}
	if res.MonitorsUpdated < 2 {
		t.Errorf("monitorsUpdated = %d, want at least 2 returning to Operational",
			res.MonitorsUpdated)
	}
	assertMonitorStatus(t, pool, domain.CloudMonitorStatusOperational)

	// Both transitions should now exist for this outage, and only two.
	var events int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM cloud_status_events WHERE outage_id = $1::uuid`,
		fixtureOutageID).Scan(&events); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if events != 2 {
		t.Errorf("got %d events for the fixture, want exactly 2 (begin then end)", events)
	}
}

// assertMonitorStatus checks every monitor behind the fixture's affected CIs.
func assertMonitorStatus(t *testing.T, pool *pgxpool.Pool, want domain.CloudMonitorStatus) {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
        SELECT cm.id::text, cm.status::text
          FROM outage_affected_ci ac
          JOIN cloud_monitor cm ON cm.service_offering_id = ac.ci_id
         WHERE ac.outage_id = $1::uuid
         ORDER BY cm.id`, fixtureOutageID)
	if err != nil {
		t.Fatalf("read monitor status: %v", err)
	}
	defer rows.Close()

	var seen int
	for rows.Next() {
		var id, status string
		if err := rows.Scan(&id, &status); err != nil {
			t.Fatalf("scan: %v", err)
		}
		seen++
		if status != string(want) {
			t.Errorf("monitor %s is %s, want %s", id, status, want)
		} else {
			t.Logf("monitor %s = %s", id, status)
		}
	}
	if seen == 0 {
		t.Error("no monitors resolved from the fixture's affected CIs")
	}
}
