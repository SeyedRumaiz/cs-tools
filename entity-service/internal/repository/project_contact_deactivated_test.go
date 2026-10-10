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
// KIND, either express or implied. See the License for the
// specific language governing permissions and limitations
// under the License.

package repository

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// The Salesforce ingest keeps a removed contact as a DEACTIVATED
// project_contact row where ServiceNow hard-deleted it, so readers that
// never filtered on state must now do so. These pin the two that the
// Contact PR's reader audit flagged.

const deactivatedPredicate = "(pc.state IS NULL OR pc.state <> 'DEACTIVATED'::project_contact_state_enum)"

func TestProjectContactEmailsQuery_ExcludesDeactivated(t *testing.T) {
	if !strings.Contains(projectContactEmailsQuery, deactivatedPredicate) {
		t.Errorf("change-request notice recipients must leave out DEACTIVATED memberships:\n%s", projectContactEmailsQuery)
	}
}

func TestHasCustomerAdminContactExists_ExcludesDeactivated(t *testing.T) {
	if !strings.Contains(hasCustomerAdminContactExists, deactivatedPredicate) {
		t.Errorf("a DEACTIVATED membership must not count as the project's admin contact:\n%s", hasCustomerAdminContactExists)
	}
}

// Fixture ids for the live-database test, distinct from every other
// integration file's.
const (
	dciAccountID   = "dc1a0000-0000-4000-8000-000000000001"
	dciProjectID   = "dc1a0000-0000-4000-8000-000000000002"
	dciLiveUserID  = "dc1a0000-0000-4000-8000-000000000003"
	dciGoneUserID  = "dc1a0000-0000-4000-8000-000000000004"
	dciLiveACID    = "dc1a0000-0000-4000-8000-000000000005"
	dciGoneACID    = "dc1a0000-0000-4000-8000-000000000006"
	dciLivePCID    = "dc1a0000-0000-4000-8000-000000000007"
	dciGonePCID    = "dc1a0000-0000-4000-8000-000000000008"
	dciNullPCID    = "dc1a0000-0000-4000-8000-000000000009"
	dciNullACID    = "dc1a0000-0000-4000-8000-00000000000a"
	dciRoleID      = "dc1a0000-0000-4000-8000-00000000000b"
	dciUserRoleID  = "dc1a0000-0000-4000-8000-00000000000c"
	dciLiveEmail   = "dci.live@example.test"
	dciGoneEmail   = "dci.gone@example.test"
	dciNullEmail   = "dci.nullstate@example.test"
	dciCreatedByID = "deactivated-integration-test"
)

// TestDeactivatedMembershipReadersIntegration runs both queries against a
// live PostgreSQL instance. Skipped unless ENTITY_TEST_DATABASE_URL is set
// (see account_repo_salesforce_integration_test.go for the setup).
func TestDeactivatedMembershipReadersIntegration(t *testing.T) {
	dsn := os.Getenv("ENTITY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("ENTITY_TEST_DATABASE_URL is not set; skipping the live-database tests")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)

	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("exec (%.70s): %v", sql, err)
		}
	}
	clean := func() {
		for _, stmt := range []string{
			`DELETE FROM user_role WHERE id = '` + dciUserRoleID + `'`,
			`DELETE FROM role WHERE id = '` + dciRoleID + `'`,
			`DELETE FROM project_contact WHERE project_id = '` + dciProjectID + `'`,
			`DELETE FROM account_contact WHERE account_id = '` + dciAccountID + `'`,
			`DELETE FROM "user" WHERE id IN ('` + dciLiveUserID + `', '` + dciGoneUserID + `')`,
			`DELETE FROM project WHERE id = '` + dciProjectID + `'`,
			`DELETE FROM account WHERE id = '` + dciAccountID + `'`,
		} {
			if _, err := pool.Exec(ctx, stmt); err != nil {
				t.Fatalf("clean: %v", err)
			}
		}
	}
	clean()
	t.Cleanup(clean)

	exec(`INSERT INTO account (id, created_on, updated_on, created_by, updated_by, name, number)
	      VALUES ($1, now(), now(), $2, $2, 'DCI account', 'ACC-DCI-1')`, dciAccountID, dciCreatedByID)
	exec(`INSERT INTO project (id, created_on, updated_on, created_by, updated_by, key, account_id)
	      VALUES ($1, now(), now(), $2, $2, 'DCITEST', $3)`, dciProjectID, dciCreatedByID, dciAccountID)
	for _, u := range [][2]string{{dciLiveUserID, dciLiveEmail}, {dciGoneUserID, dciGoneEmail}} {
		exec(`INSERT INTO "user" (id, created_on, updated_on, user_name, email, is_active) VALUES ($1, now(), now(), $2, $2, true)`, u[0], u[1])
	}
	for _, ac := range [][2]string{{dciLiveACID, dciLiveEmail}, {dciGoneACID, dciGoneEmail}, {dciNullACID, dciNullEmail}} {
		exec(`INSERT INTO account_contact (id, created_on, updated_on, created_by, updated_by, user_name, account_id)
		      VALUES ($1, now(), now(), $3, $3, $2, $4)`, ac[0], ac[1], dciCreatedByID, dciAccountID)
	}
	exec(`INSERT INTO project_contact (id, created_on, updated_on, created_by, updated_by, email, account_contact_id, project_id, state)
	      VALUES ($1, now(), now(), $4, $4, $2, $3, $5, 'REGISTERED')`, dciLivePCID, dciLiveEmail, dciLiveACID, dciCreatedByID, dciProjectID)
	exec(`INSERT INTO project_contact (id, created_on, updated_on, created_by, updated_by, email, account_contact_id, project_id, state)
	      VALUES ($1, now(), now(), $4, $4, $2, $3, $5, 'DEACTIVATED')`, dciGonePCID, dciGoneEmail, dciGoneACID, dciCreatedByID, dciProjectID)
	// A ServiceNow-era row with no state at all is live.
	exec(`INSERT INTO project_contact (id, created_on, updated_on, created_by, updated_by, email, account_contact_id, project_id, state)
	      VALUES ($1, now(), now(), $4, $4, $2, $3, $5, NULL)`, dciNullPCID, dciNullEmail, dciNullACID, dciCreatedByID, dciProjectID)

	// The only customer_admin is the deactivated contact. role.name is
	// unique, so the seeded customer_admin row is reused when there is one;
	// otherwise a fixture row (removed by clean) stands in for it.
	roleID := dciRoleID
	if err := pool.QueryRow(ctx, `SELECT id::text FROM role WHERE name = 'customer_admin'`).Scan(&roleID); err != nil {
		roleID = dciRoleID
		exec(`INSERT INTO role (id, created_on, updated_on, name) VALUES ($1, now(), now(), 'customer_admin')`, dciRoleID)
	}
	exec(`INSERT INTO user_role (id, created_on, updated_on, user_id, role_id) VALUES ($1, now(), now(), $2, $3)`, dciUserRoleID, dciGoneUserID, roleID)

	// The repositories under test run every statement under a caller identity
	// (Scoped); these are background-style reads, so stamp a system identity.
	sysCtx := WithSystemIdentity(ctx)
	emails, err := (&crNoticeRepository{db: NewScoped(pool)}).ProjectContactEmails(sysCtx, dciProjectID)
	if err != nil {
		t.Fatalf("ProjectContactEmails: %v", err)
	}
	if want := []string{dciLiveEmail, dciNullEmail}; !reflect.DeepEqual(emails, want) {
		t.Errorf("recipients = %v, want %v (the DEACTIVATED contact left out)", emails, want)
	}

	stats := &projectStatsRepo{db: NewScoped(pool)}
	in, err := stats.SLAStatusInputs(sysCtx, dciProjectID)
	if err != nil {
		t.Fatalf("SLAStatusInputs: %v", err)
	}
	if in.HasCustomerAdminContact {
		t.Error("a DEACTIVATED membership must not count as the project's customer admin contact")
	}

	// The same person re-registered on the project counts again.
	exec(`UPDATE project_contact SET state = 'REGISTERED' WHERE id = $1`, dciGonePCID)
	if in, err = stats.SLAStatusInputs(sysCtx, dciProjectID); err != nil {
		t.Fatalf("SLAStatusInputs: %v", err)
	}
	if !in.HasCustomerAdminContact {
		t.Error("a live customer_admin contact must count")
	}
}
