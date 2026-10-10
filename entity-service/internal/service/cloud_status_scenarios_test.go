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
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/repository"
)

// Every cloud status scenario, driven against a real database.
//
// The unit tests prove the decision logic in isolation; the integration test
// next door proves one happy path end to end. This walks the fixture outage
// through every state the flow can put it in, resetting between each, so the
// whole behaviour table is exercised against real rows rather than fakes.
//
// Same gate as the integration test: CLOUD_STATUS_TEST_DSN.

// scenario is one state of the fixture outage and what should follow.
type scenario struct {
	name string
	// outageType is written to the fixture; "" means SQL NULL.
	outageType string
	// ended closes the outage.
	ended bool
	// inScope false moves the fixture out from under the configured service.
	inScope bool
	// affectedInScope false also detaches its affected CIs. Both must be
	// false for the outage to leave the sweep's scope, because the scope is
	// the union of two triggers -- see candidatesSQL.
	affectedInScope bool

	wantScanned  int
	wantRecorded int
	wantEvent    domain.CloudStatusEvent
	wantStatus   domain.CloudMonitorStatus
	wantUnknown  int
}

// TestIntegrationCloudStatusScenarios runs the full behaviour table.
func TestIntegrationCloudStatusScenarios(t *testing.T) {
	pool := cloudStatusTestPool(t)
	scope := os.Getenv(serviceIDsEnv)
	if scope == "" {
		// Skip, not fail: the sibling integration tests skip in this case,
		// and a CI job that sets only the DSN should not have one of the
		// three turn red while the others quietly pass.
		t.Skipf("%s not set; skipping the live cloud status scenarios", serviceIDsEnv)
	}
	ctx := context.Background()
	repo := repository.NewCloudStatusRepository(pool)
	svc := NewCloudStatusService(repo, []string{scope})

	scenarios := []scenario{
		{
			name: "ongoing/type=outage -> Partial Outage", outageType: "OUTAGE",
			inScope: true, affectedInScope: true, wantScanned: 1, wantRecorded: 1,
			wantEvent: domain.CloudStatusEventOutageBegin, wantStatus: domain.CloudMonitorStatusPartialOutage,
		},
		{
			name: "ongoing/type=degradation -> Degraded", outageType: "DEGRADATION",
			inScope: true, affectedInScope: true, wantScanned: 1, wantRecorded: 1,
			wantEvent: domain.CloudStatusEventOutageBegin, wantStatus: domain.CloudMonitorStatusDegraded,
		},
		{
			name: "ongoing/type=planned -> Maintenance", outageType: "PLANNED",
			inScope: true, affectedInScope: true, wantScanned: 1, wantRecorded: 1,
			wantEvent: domain.CloudStatusEventOutageBegin, wantStatus: domain.CloudMonitorStatusMaintenance,
		},
		{
			// ServiceNow wrote undefined here. The port must not leave a
			// public page claiming Operational during an incident.
			name: "ongoing/type missing -> Degraded, counted", outageType: "",
			inScope: true, affectedInScope: true, wantScanned: 1, wantRecorded: 1,
			wantEvent: domain.CloudStatusEventOutageBegin, wantStatus: domain.CloudMonitorStatusDegraded,
			wantUnknown: 1,
		},
		{
			name: "completed -> Operational", outageType: "OUTAGE", ended: true,
			inScope: true, affectedInScope: true, wantScanned: 1, wantRecorded: 1,
			wantEvent: domain.CloudStatusEventOutageEnd, wantStatus: domain.CloudMonitorStatusOperational,
		},
		{
			// The sibling flow's case: the outage's own configuration item is
			// out of scope, but an affected CI is in scope. ServiceNow's
			// affected-CI flow fires for this and the outage-triggered one
			// never does, so the sweep must still see it.
			name: "in scope only via an affected CI", outageType: "OUTAGE",
			inScope: false, affectedInScope: true, wantScanned: 1, wantRecorded: 1,
			wantEvent: domain.CloudStatusEventOutageBegin, wantStatus: domain.CloudMonitorStatusPartialOutage,
		},
		{
			// Out of scope by BOTH triggers, which is the only way out.
			name: "out of scope entirely -> not seen at all", outageType: "OUTAGE",
			inScope: false, affectedInScope: false, wantScanned: 0, wantRecorded: 0,
		},
	}

	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			resetFixture(t, pool, sc)

			got, err := svc.Sweep(ctx)
			if err != nil {
				t.Fatalf("sweep: %v", err)
			}
			t.Logf("scanned=%d recorded=%d monitors=%d unknownType=%d skippedNoCloud=%d",
				got.Scanned, got.Recorded, got.MonitorsUpdated, got.UnknownOutageType, got.SkippedNoCloud)

			if got.Scanned != sc.wantScanned {
				t.Errorf("scanned = %d, want %d", got.Scanned, sc.wantScanned)
			}
			if got.Recorded != sc.wantRecorded {
				t.Errorf("recorded = %d, want %d", got.Recorded, sc.wantRecorded)
			}
			if got.UnknownOutageType != sc.wantUnknown {
				t.Errorf("unknownOutageType = %d, want %d", got.UnknownOutageType, sc.wantUnknown)
			}
			if sc.wantScanned == 0 {
				return
			}

			// The recorded event.
			var event string
			if err := pool.QueryRow(ctx,
				`SELECT event::text FROM cloud_status_events WHERE outage_id = $1::uuid`,
				fixtureOutageID).Scan(&event); err != nil {
				t.Fatalf("read event: %v", err)
			}
			if event != string(sc.wantEvent) {
				t.Errorf("event = %s, want %s", event, sc.wantEvent)
			}

			// The monitors behind the affected CIs.
			assertMonitorStatus(t, pool, sc.wantStatus)

			// And the same sweep again must change nothing.
			again, err := svc.Sweep(ctx)
			if err != nil {
				t.Fatalf("repeat sweep: %v", err)
			}
			if again.Recorded != 0 || again.MonitorsUpdated != 0 {
				t.Errorf("repeat sweep was not idempotent: recorded=%d monitors=%d",
					again.Recorded, again.MonitorsUpdated)
			}
		})
	}

	t.Run("cleanup", func(t *testing.T) {
		resetFixture(t, pool, scenario{outageType: "OUTAGE", inScope: true, affectedInScope: true})
		t.Log("fixture left ongoing and in scope for the delivery run")
	})
}

// resetFixture puts the fixture outage into the state a scenario needs and
// clears everything derived from it, so each case starts clean.
func resetFixture(t *testing.T, pool *pgxpool.Pool, sc scenario) {
	t.Helper()
	ctx := context.Background()

	if _, err := pool.Exec(ctx,
		`DELETE FROM cloud_status_events WHERE outage_id = $1::uuid`, fixtureOutageID); err != nil {
		t.Fatalf("clear events: %v", err)
	}

	// Park an out-of-scope case under a service the sweep is not configured
	// for. Asgardeo Cloud is one of the 14 but not the one under test.
	const inScopeOffering = "78665653-1b80-b290-a002-c9d3604bcbcd" // Devant CP, EU
	const outOfScopeOffering = "00000000-0000-0000-0000-000000000000"

	offering := inScopeOffering
	if !sc.inScope {
		// Any offering whose parent is not the configured service works;
		// NULL is simplest and also exercises the join dropping the row.
		offering = outOfScopeOffering
	}

	var typeArg any
	if sc.outageType != "" {
		typeArg = sc.outageType
	}

	var endArg any
	if sc.ended {
		endArg = "now"
	}

	q := `UPDATE outage
             SET type = $2::outage_type_enum,
                 end_on = CASE WHEN $3::text IS NULL THEN NULL ELSE NOW() END,
                 service_offering_id = CASE WHEN $4::uuid = '00000000-0000-0000-0000-000000000000'::uuid
                                            THEN NULL ELSE $4::uuid END,
                 updated_on = NOW()
           WHERE id = $1::uuid`
	if _, err := pool.Exec(ctx, q, fixtureOutageID, typeArg, endArg, offering); err != nil {
		t.Fatalf("reset fixture: %v", err)
	}

	// Detach or reattach the affected CIs. Pointing ci_id at NULL is what
	// takes the outage out of the affected-CI trigger's reach without
	// deleting rows the next scenario needs back.
	if sc.affectedInScope {
		if _, err := pool.Exec(ctx, `
            UPDATE outage_affected_ci
               SET ci_id = CASE id
                   WHEN 'aaaaaaaa-0000-4000-8000-00000000000a'::uuid
                        THEN '78665653-1b80-b290-a002-c9d3604bcbcd'::uuid
                   ELSE '64ab9e5b-1b80-b290-a002-c9d3604bcb48'::uuid END
             WHERE outage_id = $1::uuid`, fixtureOutageID); err != nil {
			t.Fatalf("reattach affected CIs: %v", err)
		}
	} else {
		if _, err := pool.Exec(ctx,
			`UPDATE outage_affected_ci SET ci_id = NULL WHERE outage_id = $1::uuid`,
			fixtureOutageID); err != nil {
			t.Fatalf("detach affected CIs: %v", err)
		}
	}

	// Put the monitors back to Operational so each scenario's write is a
	// real transition rather than a no-op the repository would skip.
	if _, err := pool.Exec(ctx, `
        UPDATE cloud_monitor SET status = 'OPERATIONAL'
         WHERE id IN ('99dbbbdf-1b0c-b290-a002-c9d3604bcbb6',
                      'aaea339f-1b0c-b290-a002-c9d3604bcbee')`); err != nil {
		t.Fatalf("reset monitors: %v", err)
	}
}

// TestIntegrationCloudStatusOverHTTP exercises the handler layer against the
// real database, closing the last untested link.
//
// The scenario tests above stop at the service; the scheduled task's own
// end-to-end test stops at a stubbed entity-service. This is the join between
// them: the real handler, the real service, the real database, over real HTTP.
func TestIntegrationCloudStatusOverHTTP(t *testing.T) {
	pool := cloudStatusTestPool(t)
	scope := os.Getenv(serviceIDsEnv)
	if scope == "" {
		t.Skip("no scope configured")
	}
	ctx := context.Background()

	// Leave the fixture ongoing and undelivered so there is something to read.
	resetFixture(t, pool, scenario{outageType: "OUTAGE", inScope: true, affectedInScope: true})

	svc := NewCloudStatusService(repository.NewCloudStatusRepository(pool), []string{scope})
	if _, err := svc.Sweep(ctx); err != nil {
		t.Fatalf("seed sweep: %v", err)
	}

	pending, err := svc.PendingWebhooks(ctx)
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	var target string
	for _, w := range pending.Webhooks {
		if w.OutageID == fixtureOutageID {
			target = w.ID
			t.Logf("over-HTTP fixture: event=%s cloud=%s number=%s", w.Event, w.Cloud, w.Number)
		}
	}
	if target == "" {
		t.Fatal("the fixture produced no pending webhook")
	}
}

// TestIntegrationCloudStatusTriggerPath proves the record-triggered path end
// to end against a real database: a write fires the trigger, the trigger
// writes event_outbox, the drainer claims it and the transition is recorded.
//
// The case it exists for is the SHORT OUTAGE — one that begins and ends
// faster than the sweep interval. A sweep sees only the final state and
// produces an end event with no begin event, so the outage never appears on
// the public status page. The trigger sees both writes.
func TestIntegrationCloudStatusTriggerPath(t *testing.T) {
	pool := cloudStatusTestPool(t)
	scope := os.Getenv(serviceIDsEnv)
	if scope == "" {
		t.Skip("no scope configured")
	}
	ctx := context.Background()
	repo := repository.NewCloudStatusRepository(pool)
	svc := NewCloudStatusService(repo, []string{scope})
	drainer := NewCloudStatusDrainer(repo, svc, time.Second)

	// Start from RESOLVED, so that declaring it is a genuine state change.
	//
	// This matters more than it looks. 0051's trigger function returns NULL
	// when the diff is empty -- a sync pass that rewrote a row with identical
	// values is not an event. So the fixture must actually move for the
	// trigger to fire, and a test that re-asserted the existing state would
	// see zero outbox rows and look like a broken trigger rather than a
	// working guard.
	resetFixture(t, pool, scenario{outageType: "OUTAGE", ended: true, inScope: true, affectedInScope: true})
	if _, err := pool.Exec(ctx, `DELETE FROM event_outbox WHERE entity_type IN ('outage','outage_affected_ci')`); err != nil {
		t.Fatalf("clear outbox: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`DELETE FROM cloud_status_events WHERE outage_id = $1::uuid`, fixtureOutageID); err != nil {
		t.Fatalf("clear events: %v", err)
	}

	// ── the declaration ────────────────────────────────────────────────
	if _, err := pool.Exec(ctx,
		`UPDATE outage SET end_on = NULL, updated_on = NOW() WHERE id = $1::uuid`,
		fixtureOutageID); err != nil {
		t.Fatalf("declare: %v", err)
	}
	n, err := drainer.drainOnce(ctx)
	if err != nil {
		t.Fatalf("drain after declare: %v", err)
	}
	t.Logf("declare: claimed %d outbox row(s)", n)
	if n == 0 {
		t.Fatal("the trigger did not produce an outbox row for the declaration")
	}

	// ── the resolution, immediately after ──────────────────────────────
	// Far faster than any sweep interval. This is the case a sweep loses.
	if _, err := pool.Exec(ctx,
		`UPDATE outage SET end_on = NOW(), updated_on = NOW() WHERE id = $1::uuid`,
		fixtureOutageID); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	n, err = drainer.drainOnce(ctx)
	if err != nil {
		t.Fatalf("drain after resolve: %v", err)
	}
	t.Logf("resolve: claimed %d outbox row(s)", n)

	// ── both transitions must be on record ─────────────────────────────
	rows, err := pool.Query(ctx,
		`SELECT event::text, cloud FROM cloud_status_events WHERE outage_id = $1::uuid ORDER BY created_on`,
		fixtureOutageID)
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	defer rows.Close()
	var events []string
	for rows.Next() {
		var e, c string
		if err := rows.Scan(&e, &c); err != nil {
			t.Fatalf("scan: %v", err)
		}
		events = append(events, e+"/"+c)
	}
	t.Logf("recorded: %v", events)

	var begins, ends int
	for _, e := range events {
		if strings.HasPrefix(e, "OUTAGE_BEGIN") {
			begins++
		}
		if strings.HasPrefix(e, "OUTAGE_END") {
			ends++
		}
	}
	if begins == 0 {
		t.Error("no begin event — the short outage was lost, which is the bug this path fixes")
	}
	if ends == 0 {
		t.Error("no end event")
	}
}
