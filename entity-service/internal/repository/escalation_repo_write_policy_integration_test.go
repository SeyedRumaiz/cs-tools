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

// Regression test for migration 0150: case_escalation/
// case_escalation_notification_list's INSERT policies were internal-only
// from migration 0141 onward, written back when EscalationService.
// CreateEscalation had no real implementation at all. That assumption no
// longer holds -- escalation_service.go's CreateEscalation is a genuine,
// project-membership-authorized customer write, and an external caller who
// legitimately owns the case being escalated would otherwise have their
// escalation rejected by RLS with a raw "row violates row-level security
// policy" error, despite the Go-layer authorization already having approved
// them. Runs against a real Postgres with 0150 applied. Skipped without
// CASE_STATS_TEST_DSN, so an ordinary `go test ./...` stays hermetic.
//
//	CASE_STATS_TEST_DSN=postgres://... go test ./internal/repository/ -run EscalationWritePolicy

package repository_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/repository"
)

const (
	escWritePolicyAccountID = "50000000-0000-0000-0000-000000000001"
	escWritePolicyContactID = "50000000-0000-0000-0000-000000000002"
	escWritePolicyProjectID = "51111111-0000-0000-0000-000000000001"
	escWritePolicyCaseID    = "52222222-0000-0000-0000-000000000001"
	escWritePolicyMember    = "esc-write-policy-member@test.local"
	escWritePolicyStranger  = "esc-write-policy-stranger@test.local"
)

// seedEscalationWritePolicyFixture creates one project with a single
// REGISTERED project_contact and one open case in it -- escWritePolicyMember
// is that contact's email (is_project_member(escWritePolicyProjectID) is true
// for them); escWritePolicyStranger is never registered on any project, so
// is_project_member is false for them regardless of which project they name.
func seedEscalationWritePolicyFixture(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	// WithSystemIdentity: work_item/"case" both carry RLS now (migration
	// 0147), including this fixture's own writes and its cleanup's DELETEs.
	ctx := repository.WithSystemIdentity(context.Background())
	scoped := repository.NewScoped(pool)

	cleanup := func() {
		_, _ = scoped.Exec(ctx, `DELETE FROM case_escalation_notification_list WHERE case_escalation_id IN (SELECT id FROM case_escalation WHERE work_item_id = $1)`, escWritePolicyCaseID)
		_, _ = scoped.Exec(ctx, `DELETE FROM case_escalation WHERE work_item_id = $1`, escWritePolicyCaseID)
		_, _ = scoped.Exec(ctx, `DELETE FROM work_item WHERE id = $1`, escWritePolicyCaseID)
		_, _ = pool.Exec(ctx, `DELETE FROM project_contact WHERE project_id = $1`, escWritePolicyProjectID)
		_, _ = pool.Exec(ctx, `DELETE FROM project WHERE id = $1`, escWritePolicyProjectID)
		_, _ = pool.Exec(ctx, `DELETE FROM account_contact WHERE id = $1`, escWritePolicyContactID)
		_, _ = pool.Exec(ctx, `DELETE FROM account WHERE id = $1`, escWritePolicyAccountID)
	}
	cleanup()
	t.Cleanup(cleanup)

	mustExec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("seed (%.80s): %v", sql, err)
		}
	}
	mustExecScoped := func(sql string, args ...any) {
		t.Helper()
		if _, err := scoped.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("seed scoped (%.80s): %v", sql, err)
		}
	}
	now := time.Now().UTC()

	mustExec(`INSERT INTO account (id, created_on, updated_on, created_by, updated_by, name, number, sf_id)
		VALUES ($1, $2, $2, 'test', 'test', 'ESC Write Policy Account', 'ESCWP-ACC-1', 'ESCWP-SF-ACC-1')`, escWritePolicyAccountID, now)
	mustExec(`INSERT INTO account_contact (id, created_on, updated_on, created_by, updated_by, user_name, account_id)
		VALUES ($1, $2, $2, 'test', 'test', 'ESC Write Policy Contact', $3)`, escWritePolicyContactID, now, escWritePolicyAccountID)
	mustExec(`INSERT INTO project (id, created_on, updated_on, created_by, updated_by, key, sf_id, account_id)
		VALUES ($1, $2, $2, 'test', 'test', 'ESCWRITEPOL', 'sf-escwritepol', $3)`, escWritePolicyProjectID, now, escWritePolicyAccountID)
	// state = 'REGISTERED': is_project_member requires it, same reasoning as
	// every other fixture in this package.
	mustExec(`INSERT INTO project_contact (id, created_on, updated_on, created_by, updated_by, email, account_contact_id, project_id, state)
		VALUES (gen_random_uuid(), $1, $1, 'test', 'test', $2, $3, $4, 'REGISTERED')`,
		now, escWritePolicyMember, escWritePolicyContactID, escWritePolicyProjectID)

	mustExecScoped(`INSERT INTO work_item (id, created_on, updated_on, created_by, updated_by, number, wso2_id, subject, type, project_id)
		VALUES ($1, $2, $2, 'test', 'test', 'ESCWRITEPOL-1', 'ESCWRITEPOL-WSO2-1', 'esc write policy test case', 'CASE', $3)`,
		escWritePolicyCaseID, now, escWritePolicyProjectID)
	mustExecScoped(`INSERT INTO "case" (id, state, severity) VALUES ($1, 'OPEN', 'S2')`, escWritePolicyCaseID)
}

// TestEscalationWritePolicyIntegration_ProjectMemberCanCreateEscalation is the
// end-to-end regression test: exercises the real EscalationRepository.
// CreateEscalation Go code (not just the SQL policy in isolation) as an
// external, non-internal caller who is genuinely registered on the case's
// project -- before migration 0150, this INSERT was rejected by RLS
// regardless of Go-layer authorization, surfacing as a 500 for every real
// customer escalation.
func TestEscalationWritePolicyIntegration_ProjectMemberCanCreateEscalation(t *testing.T) {
	pool := caseStatsPool(t)
	seedEscalationWritePolicyFixture(t, pool)

	repo := repository.NewEscalationRepository(repository.NewScoped(pool), repository.EscalationNotificationConfig{})
	ctx := repository.WithCallerIdentity(context.Background(), repository.SearchScope{Unrestricted: false, ViewerEmail: escWritePolicyMember})

	reason := "customer-initiated escalation"
	created, err := repo.CreateEscalation(ctx, escWritePolicyCaseID, domain.EscalationActionEscalate, &reason, escWritePolicyMember)
	if err != nil {
		t.Fatalf("CreateEscalation() as a registered project member: unexpected error = %v, want success now that migration 0150 widens the write policy", err)
	}
	if created.CurrentLevel.ID != "1" {
		t.Errorf("CurrentLevel.ID = %q, want \"1\" (EL0 -> EL1 on first escalation)", created.CurrentLevel.ID)
	}
	if created.CreatedBy != escWritePolicyMember {
		t.Errorf("CreatedBy = %q, want %q", created.CreatedBy, escWritePolicyMember)
	}
}

// TestEscalationWritePolicyIntegration_StrangerCannotInsertEscalation isolates
// the case_escalation INSERT policy itself (migration 0150) from the
// separate case-visibility gate CreateEscalation's own case lookup applies
// first (migration 0147): escWritePolicyStranger is registered on no
// project at all, so is_project_member is false for them under any
// project_id, and the raw INSERT below -- issued directly, bypassing
// EscalationRepository/CreateEscalation's case lookup entirely -- must still
// be rejected by RLS on its own.
func TestEscalationWritePolicyIntegration_StrangerCannotInsertEscalation(t *testing.T) {
	pool := caseStatsPool(t)
	seedEscalationWritePolicyFixture(t, pool)

	scoped := repository.NewScoped(pool)
	ctx := repository.WithCallerIdentity(context.Background(), repository.SearchScope{Unrestricted: false, ViewerEmail: escWritePolicyStranger})

	_, err := scoped.Exec(ctx, `
		INSERT INTO case_escalation (id, created_on, updated_on, created_by, updated_by, work_item_id, current_level, previous_level, reason)
		VALUES (gen_random_uuid(), NOW(), NOW(), $1, $1, $2, 'EL1'::case_escalation_level_enum, 'EL0'::case_escalation_level_enum, 'should be rejected')`,
		escWritePolicyStranger, escWritePolicyCaseID)
	if !repository.IsRLSPolicyViolation(err) {
		t.Fatalf("INSERT INTO case_escalation as a non-member, non-internal caller: want an RLS violation (42501), got %v", err)
	}
}
