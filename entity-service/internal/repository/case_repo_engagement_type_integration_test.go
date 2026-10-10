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

// Regression test for a real, reported bug: GetCaseByID never selected
// engagement.type on this data source, even though SearchCases already did
// -- so a Postgres-sourced engagement's detail response always omitted
// engagementType entirely (customer-portal's backend-v2 maps a nil
// CaseView.EngagementType to no field at all via omitempty), while the same
// case through the ServiceNow data source carried a real
// {"id":"1","label":"Migration"}. This silently hid the webapp's Migration
// reminder/engagement-type display for every Postgres-sourced engagement.
// Runs against a real Postgres. Skipped without CASE_STATS_TEST_DSN.
//
//	CASE_STATS_TEST_DSN=postgres://... go test ./internal/repository/ -run EngagementTypeIntegration

package repository_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/repository"
)

const (
	etEngagementID = "90000000-0000-0000-0000-000000000005"
	etCaseID       = "90000000-0000-0000-0000-000000000006"
)

func seedEngagementTypeFixture(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := repository.WithSystemIdentity(context.Background())
	scoped := repository.NewScoped(pool)

	cleanup := func() {
		_, _ = scoped.Exec(ctx, `DELETE FROM work_item WHERE id IN ($1, $2)`, etEngagementID, etCaseID)
	}
	cleanup()
	t.Cleanup(cleanup)

	now := time.Now().UTC()
	mustExecScoped := func(sql string, args ...any) {
		t.Helper()
		if _, err := scoped.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("seed scoped (%.80s): %v", sql, err)
		}
	}
	mustExec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("seed (%.80s): %v", sql, err)
		}
	}

	mustExecScoped(`INSERT INTO work_item (id, created_on, updated_on, created_by, updated_by, number, wso2_id, subject, type)
		VALUES ($1, $2, $2, 'test', 'test', 'ET-TEST-0001', 'ET-TEST-WSO2-0001', 'engagement-type integration test fixture', 'ENGAGEMENT')`,
		etEngagementID, now)
	mustExec(`INSERT INTO engagement (id, state, type) VALUES ($1, 'OPEN', 'MIGRATION')`, etEngagementID)

	mustExecScoped(`INSERT INTO work_item (id, created_on, updated_on, created_by, updated_by, number, wso2_id, subject, type)
		VALUES ($1, $2, $2, 'test', 'test', 'ET-TEST-0002', 'ET-TEST-WSO2-0002', 'engagement-type integration test fixture (case)', 'CASE')`,
		etCaseID, now)
	mustExec(`INSERT INTO "case" (id, state) VALUES ($1, 'OPEN')`, etCaseID)
}

// TestEngagementTypeIntegration_GetCaseByIDReturnsItLowerCased proves
// GetCaseByID now carries engagement.type back as CaseView.EngagementType,
// lower-cased ("migration") the same way SearchCases already does --
// customer-portal's backend-v2 maps that through its own
// caseEngagementTypeRef into a human "Migration"-style {id, label}, not the
// raw enum spelling.
func TestEngagementTypeIntegration_GetCaseByIDReturnsItLowerCased(t *testing.T) {
	pool := caseStatsPool(t)
	seedEngagementTypeFixture(t, pool)
	repo := repository.NewCaseRepository(repository.NewScoped(pool))
	ctx := repository.WithSystemIdentity(context.Background())

	cv, err := repo.GetCaseByID(ctx, etEngagementID, repository.SearchScope{Unrestricted: true})
	if err != nil {
		t.Fatalf("GetCaseByID: %v", err)
	}
	if cv.EngagementType == nil {
		t.Fatal("EngagementType is nil, want \"migration\"")
	}
	if *cv.EngagementType != "migration" {
		t.Errorf("EngagementType = %q, want %q", *cv.EngagementType, "migration")
	}
}

// TestEngagementTypeIntegration_PlainCaseLeavesItNil proves the eng LEFT JOIN
// genuinely never matches for a non-engagement case-like type, rather than
// e.g. accidentally reading some other table's column.
func TestEngagementTypeIntegration_PlainCaseLeavesItNil(t *testing.T) {
	pool := caseStatsPool(t)
	seedEngagementTypeFixture(t, pool)
	repo := repository.NewCaseRepository(repository.NewScoped(pool))
	ctx := repository.WithSystemIdentity(context.Background())

	cv, err := repo.GetCaseByID(ctx, etCaseID, repository.SearchScope{Unrestricted: true})
	if err != nil {
		t.Fatalf("GetCaseByID: %v", err)
	}
	if cv.EngagementType != nil {
		t.Errorf("EngagementType = %q on a plain case, want nil", *cv.EngagementType)
	}
}
