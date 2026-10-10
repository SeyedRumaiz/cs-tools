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

// This is an integration test: it exercises the real SQL in
// sla_engine_repo.go's RegisterClock/ReviseClocks against a live PostgreSQL
// instance, because the two bugs it regression-tests -- a terminal
// (ACHIEVED) clock getting resurrected as a fresh running one, and a
// previously-CANCELLED clock failing to be replaced -- both depend on real
// enum/stage filtering behavior a fake repository can't reproduce. Same DSN
// and skip-when-unset pattern as project_stats_repo_integration_test.go/
// time_card_repo_test.go (package repository_test, reuses caseStatsPool):
//
//	CASE_STATS_TEST_DSN=postgres://... go test ./internal/repository/ -run SLAEngineIntegration

package repository_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/repository"
)

const slaEngineIntegrationWorkItemID = "47777777-0000-0000-0000-000000000001"

// intervalLiteral formats d the same way sla_engine_repo.go's own (unexported)
// formatIntervalLiteral does -- "N seconds", always a valid Postgres interval
// literal regardless of Go's own Duration.String() formatting -- so these
// tests can push a row's start_on back by an arbitrary, precisely-known
// amount without depending on that unexported helper across package
// boundaries (this file is package repository_test, a black-box test).
func intervalLiteral(d time.Duration) string {
	return fmt.Sprintf("%d seconds", int64(d.Seconds()))
}

// seedSLAEngineWorkItem inserts one minimal work_item row (all its FK
// columns are nullable, so account/project/deployment/user need not exist)
// for the test to register/revise "sla" rows against, and removes both it
// and any "sla" rows created for it before and after the test so the
// integration test stays re-runnable against a shared database.
func seedSLAEngineWorkItem(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	// WithSystemIdentity: work_item itself is RLS-protected now too
	// (migration 0147), not just sla -- scoped, not just pool, backs this
	// seed's own insert/cleanup.
	ctx := repository.WithSystemIdentity(context.Background())
	scoped := repository.NewScoped(pool)

	cleanup := func() {
		_, _ = scoped.Exec(ctx, `DELETE FROM sla WHERE work_item_id = $1::uuid`, slaEngineIntegrationWorkItemID)
		_, _ = scoped.Exec(ctx, `DELETE FROM work_item WHERE id = $1::uuid`, slaEngineIntegrationWorkItemID)
	}
	cleanup()
	t.Cleanup(cleanup)

	if _, err := scoped.Exec(ctx, `
		INSERT INTO work_item (id, created_on, updated_on, created_by, updated_by, number, wso2_id, subject, type)
		VALUES ($1::uuid, NOW(), NOW(), 'sla-engine-test', 'sla-engine-test', 'SLAENGINE01', 'SLAENGINE-1', 'sla engine integration test case', 'CASE')`,
		slaEngineIntegrationWorkItemID); err != nil {
		t.Fatalf("seed work_item: %v", err)
	}
}

// TestSLAEngineIntegration_ReviseClocksDoesNotResurrectTerminalClock is the
// regression test CodeRabbit asked for on PR #2051's cancel-and-restart
// design: a RESPONSE clock already reached ACHIEVED (a genuine terminal
// outcome, e.g. via CompleteClock once a reply went out) must NOT be
// resurrected as a brand-new IN_PROGRESS clock just because a later
// severity change calls ReviseClocks again with the same RESPONSE policy --
// RegisterClock's per-target blocking guard (slaEngineRevisionBlockStages)
// is what prevents this, verified here against real Postgres because the
// bug can only manifest through the real NOT EXISTS filtering on real enum
// values.
//
// Same test also confirms the sibling requirement: a clock already
// CANCELLED (not a genuine terminal outcome, just retired by an earlier
// revision) is NOT blocked -- it IS replaced with a fresh IN_PROGRESS row,
// and a still-active clock is cancelled and replaced too. All three
// outcomes are exercised in a single ReviseClocks call, matching how
// SLAEngineService.ReviseCaseClocks actually calls it (one call per
// severity change, covering every applicable clock type at once).
func TestSLAEngineIntegration_ReviseClocksDoesNotResurrectTerminalClock(t *testing.T) {
	pool := caseStatsPool(t)
	seedSLAEngineWorkItem(t, pool)
	// WithSystemIdentity: this exercises the same engine the background
	// recompute worker runs as (see NewSLAEngineRepository's own doc
	// comment) -- sla's RLS policies (migration 0142) require an
	// identity on every statement now. scoped, not just pool, backs this
	// test's own setup/verification queries below too -- a raw pool.Query
	// carries no identity at all and would see zero rows regardless of
	// what was actually written, which is not what those queries mean to
	// test.
	ctx := repository.WithSystemIdentity(context.Background())
	scoped := repository.NewScoped(pool)
	repo := repository.NewSLAEngineRepository(scoped)

	respPolicy, err := repo.FindPolicyByName(ctx, "P0 - Response (Managed Services)", "RESPONSE")
	if err != nil {
		t.Fatalf("FindPolicyByName(response): %v", err)
	}
	workaroundPolicy, err := repo.FindPolicyByName(ctx, "P0 - Workaround (Managed Services)", "WORKAROUND")
	if err != nil {
		t.Fatalf("FindPolicyByName(workaround): %v", err)
	}
	resolutionPolicy, err := repo.FindPolicyByName(ctx, "P0 - Resolution (Managed Services)", "RESOLUTION")
	if err != nil {
		t.Fatalf("FindPolicyByName(resolution): %v", err)
	}

	// Register all three, then put each into a different starting state:
	// RESPONSE -> ACHIEVED (a genuine terminal outcome, via CompleteClock),
	// WORKAROUND -> left IN_PROGRESS (still active),
	// RESOLUTION -> CANCELLED directly (simulating an earlier revision that
	// already retired it).
	for _, p := range []repository.SLAPolicyRef{respPolicy, workaroundPolicy, resolutionPolicy} {
		if _, err := repo.RegisterClock(ctx, slaEngineIntegrationWorkItemID, p); err != nil {
			t.Fatalf("RegisterClock(%s) setup: %v", p.Target, err)
		}
	}
	if _, err := repo.CompleteClock(ctx, slaEngineIntegrationWorkItemID, "RESPONSE"); err != nil {
		t.Fatalf("CompleteClock(RESPONSE) setup: %v", err)
	}
	if _, err := scoped.Exec(ctx,
		`UPDATE sla SET stage = 'CANCELLED' WHERE work_item_id = $1::uuid AND sla_policy_id = $2::uuid`,
		slaEngineIntegrationWorkItemID, resolutionPolicy.ID); err != nil {
		t.Fatalf("pre-cancel RESOLUTION setup: %v", err)
	}

	// One ReviseClocks call with all three policies -- as SLAEngineService.
	// ReviseCaseClocks actually calls it on a real severity change.
	if _, err := repo.ReviseClocks(ctx, slaEngineIntegrationWorkItemID,
		[]repository.SLAPolicyRef{respPolicy, workaroundPolicy, resolutionPolicy}); err != nil {
		t.Fatalf("ReviseClocks: %v", err)
	}

	// countRows fails the test outright on a query error, rather than
	// letting a scan failure silently read back as a misleading "0 rows"
	// assertion failure below.
	countRows := func(t *testing.T, query string) int {
		t.Helper()
		var n int
		if err := scoped.QueryRow(ctx, query, slaEngineIntegrationWorkItemID).Scan(&n); err != nil {
			t.Fatalf("count query failed: %v\nquery: %s", err, query)
		}
		return n
	}

	// RESPONSE: exactly one row, still ACHIEVED -- not resurrected.
	responseTotal := countRows(t, `SELECT COUNT(*) FROM sla s JOIN sla_policy sp ON sp.id = s.sla_policy_id
		WHERE s.work_item_id = $1::uuid AND sp.target::TEXT = 'RESPONSE'`)
	responseAchieved := countRows(t, `SELECT COUNT(*) FROM sla s JOIN sla_policy sp ON sp.id = s.sla_policy_id
		WHERE s.work_item_id = $1::uuid AND sp.target::TEXT = 'RESPONSE' AND s.stage = 'ACHIEVED'`)
	if responseTotal != 1 || responseAchieved != 1 {
		t.Errorf("RESPONSE rows = %d (achieved = %d), want exactly 1 row still ACHIEVED -- a terminal clock must never be resurrected", responseTotal, responseAchieved)
	}

	// WORKAROUND: the original active row is now CANCELLED, and a fresh
	// IN_PROGRESS row exists alongside it.
	workaroundCancelled := countRows(t, `SELECT COUNT(*) FROM sla s JOIN sla_policy sp ON sp.id = s.sla_policy_id
		WHERE s.work_item_id = $1::uuid AND sp.target::TEXT = 'WORKAROUND' AND s.stage = 'CANCELLED'`)
	workaroundActive := countRows(t, `SELECT COUNT(*) FROM sla s JOIN sla_policy sp ON sp.id = s.sla_policy_id
		WHERE s.work_item_id = $1::uuid AND sp.target::TEXT = 'WORKAROUND' AND s.stage = 'IN_PROGRESS'`)
	if workaroundCancelled != 1 || workaroundActive != 1 {
		t.Errorf("WORKAROUND cancelled = %d, active = %d, want 1/1 -- the old active clock must be cancelled and a fresh one registered", workaroundCancelled, workaroundActive)
	}

	// RESOLUTION: was already CANCELLED before this call -- must now ALSO
	// have a fresh IN_PROGRESS row (a cancelled clock does not block a
	// fresh registration).
	resolutionActive := countRows(t, `SELECT COUNT(*) FROM sla s JOIN sla_policy sp ON sp.id = s.sla_policy_id
		WHERE s.work_item_id = $1::uuid AND sp.target::TEXT = 'RESOLUTION' AND s.stage = 'IN_PROGRESS'`)
	if resolutionActive != 1 {
		t.Errorf("RESOLUTION active rows = %d, want 1 -- a previously-CANCELLED clock must still be replaced by a fresh registration", resolutionActive)
	}
}

// TestSLAEngineIntegration_ReviseClocksReplacesBreachedClock is the
// regression test for a real, reported bug: a WORKAROUND/RESOLUTION clock
// that had merely BREACHED under the OLD severity (ran out the wall clock
// without ever being satisfied by an explicit completion) was being treated
// the same as a genuine ACHIEVED outcome -- neither cancelled nor replaced
// by a later severity change, leaving the case's SLA tracking permanently
// stuck on a stale, timed-out clock from the old severity instead of
// starting over under the new one. ReviseClocks must cancel a BREACHED
// WORKAROUND/RESOLUTION clock and register a fresh IN_PROGRESS one in its
// place, exactly as it already does for a still-active IN_PROGRESS/PAUSED
// clock. RESPONSE deliberately does NOT follow this rule -- see
// TestSLAEngineIntegration_ReviseClocksPreservesBreachedResponseClock below.
func TestSLAEngineIntegration_ReviseClocksReplacesBreachedClock(t *testing.T) {
	pool := caseStatsPool(t)
	seedSLAEngineWorkItem(t, pool)
	// WithSystemIdentity + Scoped: work_item is RLS-protected (see seedSLAEngineWorkItem).
	ctx := repository.WithSystemIdentity(context.Background())
	scoped := repository.NewScoped(pool)
	repo := repository.NewSLAEngineRepository(scoped)

	workaroundPolicy, err := repo.FindPolicyByName(ctx, "P0 - Workaround (Managed Services)", "WORKAROUND")
	if err != nil {
		t.Fatalf("FindPolicyByName(workaround): %v", err)
	}

	if _, err := repo.RegisterClock(ctx, slaEngineIntegrationWorkItemID, workaroundPolicy); err != nil {
		t.Fatalf("RegisterClock(workaround) setup: %v", err)
	}
	if _, err := scoped.Exec(ctx,
		`UPDATE sla SET stage = 'BREACHED', has_breached = TRUE WHERE work_item_id = $1::uuid AND sla_policy_id = $2::uuid`,
		slaEngineIntegrationWorkItemID, workaroundPolicy.ID); err != nil {
		t.Fatalf("force WORKAROUND to BREACHED setup: %v", err)
	}

	if _, err := repo.ReviseClocks(ctx, slaEngineIntegrationWorkItemID, []repository.SLAPolicyRef{workaroundPolicy}); err != nil {
		t.Fatalf("ReviseClocks: %v", err)
	}

	var cancelled, active int
	if err := scoped.QueryRow(ctx, `SELECT COUNT(*) FROM sla s JOIN sla_policy sp ON sp.id = s.sla_policy_id
		WHERE s.work_item_id = $1::uuid AND sp.target::TEXT = 'WORKAROUND' AND s.stage = 'CANCELLED'`,
		slaEngineIntegrationWorkItemID).Scan(&cancelled); err != nil {
		t.Fatalf("count cancelled: %v", err)
	}
	if err := scoped.QueryRow(ctx, `SELECT COUNT(*) FROM sla s JOIN sla_policy sp ON sp.id = s.sla_policy_id
		WHERE s.work_item_id = $1::uuid AND sp.target::TEXT = 'WORKAROUND' AND s.stage = 'IN_PROGRESS'`,
		slaEngineIntegrationWorkItemID).Scan(&active); err != nil {
		t.Fatalf("count active: %v", err)
	}
	if cancelled != 1 || active != 1 {
		t.Errorf("WORKAROUND cancelled = %d, active = %d, want 1/1 -- a BREACHED clock must be cancelled and replaced by a severity revision, not left stuck", cancelled, active)
	}

	// Regression: ReviseClocks' cancellation used to leave is_active
	// untouched at its RegisterClock-time TRUE forever, same bug as
	// CompleteClock's (see that method's own doc comment) -- a cancelled
	// row must stop reporting as "currently active" too.
	var cancelledStillActive int
	if err := scoped.QueryRow(ctx, `SELECT COUNT(*) FROM sla s JOIN sla_policy sp ON sp.id = s.sla_policy_id
		WHERE s.work_item_id = $1::uuid AND sp.target::TEXT = 'WORKAROUND' AND s.stage = 'CANCELLED' AND s.is_active`,
		slaEngineIntegrationWorkItemID).Scan(&cancelledStillActive); err != nil {
		t.Fatalf("count cancelled-but-still-active: %v", err)
	}
	if cancelledStillActive != 0 {
		t.Errorf("cancelled rows still is_active = %d, want 0", cancelledStillActive)
	}
}

// TestSLAEngineIntegration_ReviseClocksPreservesBreachedResponseClock is the
// RESPONSE-specific counterpart to the test above: per explicit product
// direction, "did a support engineer reply at all" is a fact about the past
// that a severity change cannot undo either way, so a RESPONSE clock already
// BREACHED (the first-reply window closed unanswered) must be treated the
// same as one already ACHIEVED -- neither cancelled nor resurrected by a
// later severity change. Unlike WORKAROUND/RESOLUTION, RESPONSE's BREACHED
// stage is a permanent record, not a stale clock to restart.
func TestSLAEngineIntegration_ReviseClocksPreservesBreachedResponseClock(t *testing.T) {
	pool := caseStatsPool(t)
	seedSLAEngineWorkItem(t, pool)
	// WithSystemIdentity + Scoped: work_item is RLS-protected (see seedSLAEngineWorkItem).
	ctx := repository.WithSystemIdentity(context.Background())
	scoped := repository.NewScoped(pool)
	repo := repository.NewSLAEngineRepository(scoped)

	responsePolicy, err := repo.FindPolicyByName(ctx, "P0 - Response (Managed Services)", "RESPONSE")
	if err != nil {
		t.Fatalf("FindPolicyByName(response): %v", err)
	}

	if _, err := repo.RegisterClock(ctx, slaEngineIntegrationWorkItemID, responsePolicy); err != nil {
		t.Fatalf("RegisterClock(response) setup: %v", err)
	}
	if _, err := scoped.Exec(ctx,
		`UPDATE sla SET stage = 'BREACHED', has_breached = TRUE WHERE work_item_id = $1::uuid AND sla_policy_id = $2::uuid`,
		slaEngineIntegrationWorkItemID, responsePolicy.ID); err != nil {
		t.Fatalf("force RESPONSE to BREACHED setup: %v", err)
	}

	if _, err := repo.ReviseClocks(ctx, slaEngineIntegrationWorkItemID, []repository.SLAPolicyRef{responsePolicy}); err != nil {
		t.Fatalf("ReviseClocks: %v", err)
	}

	var total, breached int
	if err := scoped.QueryRow(ctx, `SELECT COUNT(*) FROM sla s JOIN sla_policy sp ON sp.id = s.sla_policy_id
		WHERE s.work_item_id = $1::uuid AND sp.target::TEXT = 'RESPONSE'`,
		slaEngineIntegrationWorkItemID).Scan(&total); err != nil {
		t.Fatalf("count total: %v", err)
	}
	if err := scoped.QueryRow(ctx, `SELECT COUNT(*) FROM sla s JOIN sla_policy sp ON sp.id = s.sla_policy_id
		WHERE s.work_item_id = $1::uuid AND sp.target::TEXT = 'RESPONSE' AND s.stage = 'BREACHED'`,
		slaEngineIntegrationWorkItemID).Scan(&breached); err != nil {
		t.Fatalf("count breached: %v", err)
	}
	if total != 1 || breached != 1 {
		t.Errorf("RESPONSE rows = %d (breached = %d), want exactly 1 row still BREACHED -- a severity change must not cancel or resurrect a RESPONSE clock that already ran out unanswered", total, breached)
	}
}

// TestSLAEngineIntegration_RecomputeActiveKeepsMovingAfterBreach is the
// regression test for a real, live-observed bug: once RecomputeActive first
// flipped a clock to BREACHED, its own WHERE clause (stage = 'IN_PROGRESS'
// only) excluded that row from every future call, freezing
// business_elapsed_percentage/business_duration forever at whatever value
// the breaching tick happened to compute -- e.g. a response SLA observed
// stuck at "59m" elapsed long after real time had moved well past that.
// RecomputeActive must keep recomputing a BREACHED clock exactly like an
// IN_PROGRESS one, with no 100% ceiling, until its own genuine completing
// event (CompleteClock) finally finalizes it.
func TestSLAEngineIntegration_RecomputeActiveKeepsMovingAfterBreach(t *testing.T) {
	pool := caseStatsPool(t)
	seedSLAEngineWorkItem(t, pool)
	// WithSystemIdentity + scoped for every statement, not just the
	// repository construction below -- see seedSLAEngineWorkItem's own
	// comment: sla's RLS policies (migration 0142) require an identity on
	// every statement, and a raw pool.Exec/QueryRow carries none at all.
	ctx := repository.WithSystemIdentity(context.Background())
	scoped := repository.NewScoped(pool)
	repo := repository.NewSLAEngineRepository(scoped)

	policy, err := repo.FindPolicyByName(ctx, "P0 - Response (Managed Services)", "RESPONSE")
	if err != nil {
		t.Fatalf("FindPolicyByName(response): %v", err)
	}
	if _, err := repo.RegisterClock(ctx, slaEngineIntegrationWorkItemID, policy); err != nil {
		t.Fatalf("RegisterClock setup: %v", err)
	}
	// Push start_on back far enough that the clock is already well past its
	// own duration -- simulating a response that has sat unanswered for a
	// while, not one that just crossed over this instant.
	if _, err := scoped.Exec(ctx,
		`UPDATE sla SET start_on = NOW() - $2::interval WHERE work_item_id = $1::uuid`,
		slaEngineIntegrationWorkItemID, intervalLiteral(policy.Duration+10*time.Minute)); err != nil {
		t.Fatalf("push start_on back: %v", err)
	}

	if _, err := repo.RecomputeActive(ctx); err != nil {
		t.Fatalf("RecomputeActive (first): %v", err)
	}

	var stage string
	var firstPercent, firstDurationSeconds float64
	if err := scoped.QueryRow(ctx, `SELECT stage::TEXT, business_elapsed_percentage, EXTRACT(EPOCH FROM business_duration)
		FROM sla WHERE work_item_id = $1::uuid`, slaEngineIntegrationWorkItemID).
		Scan(&stage, &firstPercent, &firstDurationSeconds); err != nil {
		t.Fatalf("scan after first RecomputeActive: %v", err)
	}
	if stage != "BREACHED" {
		t.Fatalf("stage = %q, want BREACHED", stage)
	}
	if firstPercent <= 100 {
		t.Errorf("business_elapsed_percentage = %v, want > 100 -- no longer capped, and this clock was already well overrun", firstPercent)
	}

	// More real time passes while the clock remains BREACHED and unanswered.
	if _, err := scoped.Exec(ctx,
		`UPDATE sla SET start_on = start_on - INTERVAL '10 minutes' WHERE work_item_id = $1::uuid`,
		slaEngineIntegrationWorkItemID); err != nil {
		t.Fatalf("push start_on back further: %v", err)
	}
	if _, err := repo.RecomputeActive(ctx); err != nil {
		t.Fatalf("RecomputeActive (second): %v", err)
	}

	var secondPercent, secondDurationSeconds float64
	if err := scoped.QueryRow(ctx, `SELECT business_elapsed_percentage, EXTRACT(EPOCH FROM business_duration)
		FROM sla WHERE work_item_id = $1::uuid`, slaEngineIntegrationWorkItemID).
		Scan(&secondPercent, &secondDurationSeconds); err != nil {
		t.Fatalf("scan after second RecomputeActive: %v", err)
	}
	if secondPercent <= firstPercent {
		t.Errorf("business_elapsed_percentage after second RecomputeActive = %v, want greater than first (%v) -- a BREACHED clock must keep accumulating, not freeze", secondPercent, firstPercent)
	}
	if secondDurationSeconds <= firstDurationSeconds {
		t.Errorf("business_duration after second RecomputeActive = %vs, want greater than first (%vs) -- a frozen business_duration is the exact live-observed bug this regresses", secondDurationSeconds, firstDurationSeconds)
	}
}

// TestSLAEngineIntegration_CompleteClockFinalizesBreachedClock verifies a
// BREACHED clock is still completable by its own real finishing event (a
// qualifying comment, for RESPONSE), with its TRUE, uncapped overrun
// percentage -- not silently matching zero rows the way CompleteClock's
// old slaEngineActiveStageFilter-scoped query did once a clock had already
// crossed into BREACHED.
func TestSLAEngineIntegration_CompleteClockFinalizesBreachedClock(t *testing.T) {
	pool := caseStatsPool(t)
	seedSLAEngineWorkItem(t, pool)
	ctx := repository.WithSystemIdentity(context.Background())
	scoped := repository.NewScoped(pool)
	repo := repository.NewSLAEngineRepository(scoped)

	policy, err := repo.FindPolicyByName(ctx, "P0 - Response (Managed Services)", "RESPONSE")
	if err != nil {
		t.Fatalf("FindPolicyByName(response): %v", err)
	}
	if _, err := repo.RegisterClock(ctx, slaEngineIntegrationWorkItemID, policy); err != nil {
		t.Fatalf("RegisterClock setup: %v", err)
	}
	if _, err := scoped.Exec(ctx,
		`UPDATE sla SET stage = 'BREACHED', has_breached = TRUE, start_on = NOW() - $2::interval WHERE work_item_id = $1::uuid`,
		slaEngineIntegrationWorkItemID, intervalLiteral(policy.Duration+10*time.Minute)); err != nil {
		t.Fatalf("force BREACHED setup: %v", err)
	}

	completed, err := repo.CompleteClock(ctx, slaEngineIntegrationWorkItemID, "RESPONSE")
	if err != nil {
		t.Fatalf("CompleteClock: %v", err)
	}
	if !completed {
		t.Fatal("CompleteClock reported no row updated -- a BREACHED clock must still be completable by its own real finishing event")
	}

	var stage string
	var percent float64
	var isActive bool
	if err := scoped.QueryRow(ctx, `SELECT stage::TEXT, business_elapsed_percentage, is_active FROM sla WHERE work_item_id = $1::uuid`,
		slaEngineIntegrationWorkItemID).Scan(&stage, &percent, &isActive); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if stage != "ACHIEVED" {
		t.Errorf("stage = %q, want ACHIEVED", stage)
	}
	if percent <= 100 {
		t.Errorf("business_elapsed_percentage = %v, want > 100 -- completion must show the real overrun, not an artificial 100%% cap", percent)
	}
	// Regression: CompleteClock used to leave is_active untouched at its
	// RegisterClock-time TRUE forever -- see this method's own doc comment.
	// GET /sla-status (and csm-notification-service's own Reconcile/
	// pre-alert verification, which both read it) must stop reporting a
	// genuinely completed clock as "currently active".
	if isActive {
		t.Error("is_active = true, want false once the clock has genuinely completed")
	}
}

// TestSLAEngineIntegration_SetPausedPausesBreachedClock verifies a BREACHED
// clock can still be paused -- e.g. a workaround/resolution clock that ran
// out the wall clock while the case was still open, which then moves to
// AWAITING_INFO -- so RecomputeActive actually stops moving it while the
// case waits on the customer, instead of silently continuing to climb its
// elapsed time because SetPaused's own filter excluded BREACHED rows.
func TestSLAEngineIntegration_SetPausedPausesBreachedClock(t *testing.T) {
	pool := caseStatsPool(t)
	seedSLAEngineWorkItem(t, pool)
	ctx := repository.WithSystemIdentity(context.Background())
	scoped := repository.NewScoped(pool)
	repo := repository.NewSLAEngineRepository(scoped)

	policy, err := repo.FindPolicyByName(ctx, "P0 - Workaround (Managed Services)", "WORKAROUND")
	if err != nil {
		t.Fatalf("FindPolicyByName(workaround): %v", err)
	}
	if _, err := repo.RegisterClock(ctx, slaEngineIntegrationWorkItemID, policy); err != nil {
		t.Fatalf("RegisterClock setup: %v", err)
	}
	if _, err := scoped.Exec(ctx,
		`UPDATE sla SET stage = 'BREACHED', has_breached = TRUE WHERE work_item_id = $1::uuid`,
		slaEngineIntegrationWorkItemID); err != nil {
		t.Fatalf("force BREACHED setup: %v", err)
	}

	paused, err := repo.SetPaused(ctx, slaEngineIntegrationWorkItemID, "WORKAROUND", true)
	if err != nil {
		t.Fatalf("SetPaused(true): %v", err)
	}
	if !paused {
		t.Fatal("SetPaused reported no row updated -- a BREACHED clock must still be pausable")
	}

	var stage string
	if err := scoped.QueryRow(ctx, `SELECT stage::TEXT FROM sla WHERE work_item_id = $1::uuid`,
		slaEngineIntegrationWorkItemID).Scan(&stage); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if stage != "PAUSED" {
		t.Errorf("stage = %q, want PAUSED", stage)
	}
}
