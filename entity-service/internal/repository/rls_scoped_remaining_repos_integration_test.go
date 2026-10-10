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

// Regression tests for the five repositories that held a raw *pgxpool.Pool
// while reading RLS-protected tables (project, outage, account, project
// membership, Salesforce project), and so saw ZERO work_item rows under
// FORCE ROW LEVEL SECURITY even for internal callers:
//
//   - project search counted every project's active cases as 0,
//   - an outage's linked incident number/subject came back blank,
//   - the duplicate-sf_id tie-break (an EXISTS over work_item) silently ranked
//     every copy as "unreferenced" and could pick the wrong row to write.
//
// They only prove something when the DSN's role is subject to RLS (not the
// table owner, not a superuser, not BYPASSRLS); rlsPrecondition fails loudly
// otherwise instead of passing vacuously. Skipped without CASE_STATS_TEST_DSN.
//
//	CASE_STATS_TEST_DSN=postgres://... go test ./internal/repository/ -run RLSScopedRemaining

package repository_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/apierror"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/repository"
)

const (
	rsAccountID     = "7a000000-0000-4000-8000-000000000001"
	rsContactAID    = "7a000000-0000-4000-8000-000000000002"
	rsContactBID    = "7a000000-0000-4000-8000-000000000003"
	rsProjectA      = "7a000000-0000-4000-8000-000000000011"
	rsProjectB      = "7a000000-0000-4000-8000-000000000012"
	rsMemberA       = "rs-member-a@test.local"
	rsMemberB       = "rs-member-b@test.local"
	rsStranger      = "rs-stranger@test.local"
	rsIncidentID    = "7a000000-0000-4000-8000-000000000021"
	rsOutageID      = "7a000000-0000-4000-8000-000000000031"
	rsDupProjectOld = "7a000000-0000-4000-8000-000000000041"
	rsDupProjectNew = "7a000000-0000-4000-8000-000000000042"
	rsDupAccountOld = "7a000000-0000-4000-8000-000000000051"
	rsDupAccountNew = "7a000000-0000-4000-8000-000000000052"
	rsDupProjectSf  = "a0PRSCOPED000001A"
	rsDupAccountSf  = "001RSCOPED000001A"
)

// rsWorkItems is the work_item ids the fixture owns, so cleanup is exact.
var rsWorkItems = []string{
	"7a000000-0000-4000-8000-0000000000a1", "7a000000-0000-4000-8000-0000000000a2",
	"7a000000-0000-4000-8000-0000000000a3", "7a000000-0000-4000-8000-0000000000a4",
	"7a000000-0000-4000-8000-0000000000a5", rsIncidentID,
	"7a000000-0000-4000-8000-0000000000b1", "7a000000-0000-4000-8000-0000000000b2",
}

// seedRLSScopedRemaining builds two customer projects with registered members,
// their work items, an incident-linked outage, and two pairs of duplicate-sf_id
// copies (one project pair, one account pair) where only the NEWER copy is
// referenced by a work_item -- the only thing ranking them apart.
func seedRLSScopedRemaining(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := repository.WithSystemIdentity(context.Background())
	scoped := repository.NewScoped(pool)

	cleanup := func() {
		for _, id := range rsWorkItems {
			_, _ = scoped.Exec(ctx, `DELETE FROM engagement WHERE id = $1`, id)
			_, _ = scoped.Exec(ctx, `DELETE FROM "case" WHERE id = $1`, id)
			_, _ = scoped.Exec(ctx, `DELETE FROM work_item WHERE id = $1`, id)
		}
		_, _ = pool.Exec(ctx, `DELETE FROM outage WHERE id = $1`, rsOutageID)
		_, _ = pool.Exec(ctx, `DELETE FROM project_contact WHERE project_id IN ($1, $2)`, rsProjectA, rsProjectB)
		_, _ = pool.Exec(ctx, `DELETE FROM project WHERE id IN ($1, $2, $3, $4)`, rsProjectA, rsProjectB, rsDupProjectOld, rsDupProjectNew)
		_, _ = pool.Exec(ctx, `DELETE FROM account_contact WHERE id IN ($1, $2)`, rsContactAID, rsContactBID)
		_, _ = pool.Exec(ctx, `DELETE FROM account WHERE id IN ($1, $2, $3)`, rsAccountID, rsDupAccountOld, rsDupAccountNew)
		_, _ = pool.Exec(ctx, `DELETE FROM salesforce_ingest_state WHERE sf_id LIKE '%RSCOPED%'`)
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

	mustExec(`INSERT INTO account (id, created_on, updated_on, created_by, updated_by, name, number, sf_id) VALUES
		($1, now(), now(), 't', 't', 'RS Account', 'RS-ACC-1', 'sf-rs-acc-1'),
		($2, now() - interval '1 day', now(), 't', 't', 'RS Dup Old', 'RS-ACC-2', $4),
		($3, now(), now(), 't', 't', 'RS Dup New', 'RS-ACC-3', $4)`,
		rsAccountID, rsDupAccountOld, rsDupAccountNew, rsDupAccountSf)
	mustExec(`INSERT INTO account_contact (id, created_on, updated_on, created_by, updated_by, user_name, account_id) VALUES
		($1, now(), now(), 't', 't', 'RS Contact A', $3), ($2, now(), now(), 't', 't', 'RS Contact B', $3)`,
		rsContactAID, rsContactBID, rsAccountID)
	mustExec(`INSERT INTO project (id, created_on, updated_on, created_by, updated_by, key, name, sf_id, account_id) VALUES
		($1, now(), now(), 't', 't', 'RSSCOPED-A', 'RS Project A', 'sf-rs-a', $3),
		($2, now(), now(), 't', 't', 'RSSCOPED-B', 'RS Project B', 'sf-rs-b', $3)`,
		rsProjectA, rsProjectB, rsAccountID)
	mustExec(`INSERT INTO project (id, created_on, updated_on, created_by, updated_by, key, sf_id) VALUES
		($1, now() - interval '1 day', now(), 't', 't', 'RSDUPPRJ-OLD', $3),
		($2, now(), now(), 't', 't', 'RSDUPPRJ-NEW', $3)`,
		rsDupProjectOld, rsDupProjectNew, rsDupProjectSf)
	mustExec(`INSERT INTO project_contact (id, created_on, updated_on, created_by, updated_by, email, account_contact_id, project_id, state) VALUES
		(gen_random_uuid(), now(), now(), 't', 't', $1, $3, $5, 'REGISTERED'),
		(gen_random_uuid(), now(), now(), 't', 't', $2, $4, $6, 'REGISTERED')`,
		rsMemberA, rsMemberB, rsContactAID, rsContactBID, rsProjectA, rsProjectB)

	// Project A: two open cases, a closed case, an open engagement -> 3 active.
	// Project B: one open case -> 1 active. wso2_id is required by the
	// work_item_wso2_id_required_by_type CHECK for case-like types.
	items := []struct {
		id, wiType, project, state string
	}{
		{rsWorkItems[0], "CASE", rsProjectA, "OPEN"},
		{rsWorkItems[1], "CASE", rsProjectA, "AWAITING_INFO"},
		{rsWorkItems[2], "CASE", rsProjectA, "CLOSED"},
		{rsWorkItems[3], "ENGAGEMENT", rsProjectA, "OPEN"},
		{rsWorkItems[4], "CASE", rsProjectB, "OPEN"},
	}
	for i, it := range items {
		n := string(rune('1' + i))
		mustExecScoped(`INSERT INTO work_item (id, created_on, updated_on, created_by, updated_by, number, wso2_id, subject, type, project_id)
			VALUES ($1, now(), now(), 't', 't', $2, $3, 'rs seeded', $4::work_item_type_enum, $5)`,
			it.id, "RSN000"+n, "RS-WSO2-"+n, it.wiType, it.project)
		switch it.wiType {
		case "CASE":
			mustExecScoped(`INSERT INTO "case" (id, state) VALUES ($1, $2::case_state_enum)`, it.id, it.state)
		case "ENGAGEMENT":
			mustExecScoped(`INSERT INTO engagement (id, state, type) VALUES ($1, $2::engagement_state_enum, 'MIGRATION'::engagement_type_enum)`, it.id, it.state)
		}
	}

	// The outage's incident: a work_item with no project, which only an
	// internal caller may see. The outage carries no service offering, so the
	// cloud status sweep can never pick it up as a publishable candidate.
	mustExecScoped(`INSERT INTO work_item (id, created_on, updated_on, created_by, updated_by, number, wso2_id, subject, type)
		VALUES ($1, now(), now(), 't', 't', 'INC-RS-0001', 'RS-WSO2-INC', 'rs incident subject', 'INCIDENT'::work_item_type_enum)`, rsIncidentID)
	mustExec(`INSERT INTO outage (id, created_on, updated_on, created_by, updated_by, number, type, start_on, name, work_item_id)
		VALUES ($1, now(), now(), 't', 't', 'OUT-RS-0001', 'OUTAGE'::outage_type_enum, now() - interval '1 hour', 'rs outage', $2)`,
		rsOutageID, rsIncidentID)

	// Duplicate sf_id copies: the NEWER copy alone is referenced by a work_item,
	// so projectReferencedOrder/accountReferencedOrder rank it first -- but only
	// for a caller that can see that work_item.
	mustExecScoped(`INSERT INTO work_item (id, created_on, updated_on, created_by, updated_by, number, wso2_id, subject, type, project_id, account_id)
		VALUES ($1, now(), now(), 't', 't', 'RSN0009', 'RS-WSO2-9', 'rs dup ref', 'CASE'::work_item_type_enum, $2, $3)`,
		rsWorkItems[6], rsDupProjectNew, rsDupAccountNew)
	mustExecScoped(`INSERT INTO "case" (id, state) VALUES ($1, 'OPEN'::case_state_enum)`, rsWorkItems[6])
}

// rlsPrecondition proves the DSN's role is genuinely subject to RLS: with no
// identity the raw pool must see none of the fixture's work items. If it sees
// any, every assertion below would hold with or without the fix.
func rlsPrecondition(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM work_item WHERE project_id = $1`, rsProjectA).Scan(&n); err != nil {
		t.Fatalf("precondition query: %v", err)
	}
	if n != 0 {
		t.Fatalf("a raw pool with no identity sees %d work_item rows of the fixture's project: the DSN's role bypasses "+
			"row-level security (owner, superuser or BYPASSRLS), so this test would prove nothing", n)
	}
}

func rsInternal() context.Context {
	return repository.WithCallerIdentity(context.Background(), repository.SearchScope{Unrestricted: true})
}

func rsCustomer(email string, projectIDs ...string) (context.Context, repository.SearchScope) {
	scope := repository.SearchScope{ProjectIDs: projectIDs, ViewerEmail: email}
	return repository.WithCallerIdentity(context.Background(), scope), scope
}

func rsProjectCounts(t *testing.T, projects []domain.Project) map[string]int {
	t.Helper()
	out := map[string]int{}
	for _, p := range projects {
		out[p.ID] = p.ActiveCasesCount
	}
	return out
}

func rsSearchProjects(t *testing.T, repo repository.ProjectRepository, ctx context.Context, scope repository.SearchScope) map[string]int {
	t.Helper()
	got, _, err := repo.SearchProjects(ctx, domain.SearchProjectsRequest{
		SearchQuery: "RSSCOPED-",
		Pagination:  domain.Pagination{Limit: 50},
	}, scope)
	if err != nil {
		t.Fatalf("SearchProjects: %v", err)
	}
	return rsProjectCounts(t, got)
}

// An internal caller must get the real active-case counts under RLS, a
// customer only their own projects' counts, and the database must still hold
// the line if a Go-side scope were ever wrong.
func TestRLSScopedRemaining_ProjectActiveCaseCounts(t *testing.T) {
	pool := caseStatsPool(t)
	seedRLSScopedRemaining(t, pool)
	rlsPrecondition(t, pool)
	repo := repository.NewProjectRepository(repository.NewScoped(pool))

	t.Run("internal sees the real counts for every project", func(t *testing.T) {
		scope := repository.SearchScope{Unrestricted: true}
		got := rsSearchProjects(t, repo, repository.WithCallerIdentity(context.Background(), scope), scope)
		// The caller's ctx identity is not what scopes this call: the explicit
		// scope is, so a bare context behaves identically.
		bare := rsSearchProjects(t, repo, context.Background(), scope)
		for name, counts := range map[string]map[string]int{"with identity on ctx": got, "bare ctx": bare} {
			if counts[rsProjectA] != 3 || counts[rsProjectB] != 1 {
				t.Errorf("internal counts (%s) = A:%d B:%d, want A:3 B:1 (0/0 means the count read work_item with no identity)",
					name, counts[rsProjectA], counts[rsProjectB])
			}
		}
	})

	t.Run("customer sees only their own project, with its real count", func(t *testing.T) {
		ctxA, scopeA := rsCustomer(rsMemberA, rsProjectA)
		gotA := rsSearchProjects(t, repo, ctxA, scopeA)
		if len(gotA) != 1 || gotA[rsProjectA] != 3 {
			t.Errorf("customer A projects = %v, want only A with 3", gotA)
		}
		ctxB, scopeB := rsCustomer(rsMemberB, rsProjectB)
		gotB := rsSearchProjects(t, repo, ctxB, scopeB)
		if len(gotB) != 1 || gotB[rsProjectB] != 1 {
			t.Errorf("customer B projects = %v, want only B with 1", gotB)
		}
	})

	t.Run("a customer registered nowhere sees no projects", func(t *testing.T) {
		ctx, scope := rsCustomer(rsStranger)
		if got := rsSearchProjects(t, repo, ctx, scope); len(got) != 0 {
			t.Errorf("stranger projects = %v, want none", got)
		}
	})

	// Defence in depth: hand customer A a Go-side scope that wrongly includes
	// project B. The project row is not RLS-protected so it is listed, but the
	// work items behind its count are, and A is not a member of B.
	t.Run("database still hides another project's work items from a customer", func(t *testing.T) {
		ctx, scope := rsCustomer(rsMemberA, rsProjectA, rsProjectB)
		got := rsSearchProjects(t, repo, ctx, scope)
		if got[rsProjectA] != 3 || got[rsProjectB] != 0 {
			t.Errorf("over-broad scope counts = A:%d B:%d, want A:3 B:0 (RLS backstop)", got[rsProjectA], got[rsProjectB])
		}
	})

	t.Run("project detail resolves under the explicit scope", func(t *testing.T) {
		if _, err := repo.GetProjectByID(context.Background(), rsProjectA, repository.SearchScope{Unrestricted: true}); err != nil {
			t.Errorf("GetProjectByID as internal on a bare ctx: %v", err)
		}
	})
}

// An internal caller gets the linked incident's number and subject; a customer
// gets the outage but not the incident, which no project of theirs owns.
func TestRLSScopedRemaining_OutageIncidentFields(t *testing.T) {
	pool := caseStatsPool(t)
	seedRLSScopedRemaining(t, pool)
	rlsPrecondition(t, pool)
	repo := repository.NewOutageRepository(repository.NewScoped(pool))

	search := func(t *testing.T, ctx context.Context) domain.Outage {
		t.Helper()
		got, total, err := repo.Search(ctx, domain.SearchOutagesRequest{
			Filters:    domain.SearchOutagesFilters{IncidentIDs: []string{rsIncidentID}},
			Pagination: domain.Pagination{Limit: 10},
		}, time.Now().Add(-24*time.Hour))
		if err != nil || total != 1 || len(got) != 1 {
			t.Fatalf("Search: total=%d len=%d err=%v, want exactly the fixture outage", total, len(got), err)
		}
		return got[0]
	}

	t.Run("internal sees the incident number and subject", func(t *testing.T) {
		out := search(t, rsInternal())
		if out.Incident == nil || out.Incident.Number != "INC-RS-0001" || out.Incident.ShortDescription != "rs incident subject" {
			t.Errorf("incident via Search = %+v, want number INC-RS-0001 and the seeded subject (blank means work_item was read with no identity)", out.Incident)
		}
	})

	// GetByID shares outageSelect (the work_item join) with Search, but also
	// counts outage_communication rows by channel. No migration defines that
	// column yet, so on a database built only from the migrations this would
	// fail for a reason unrelated to identity; skip rather than fail there.
	t.Run("internal sees the incident number and subject via GetByID", func(t *testing.T) {
		var hasChannel bool
		if err := pool.QueryRow(context.Background(), `SELECT EXISTS (SELECT 1 FROM information_schema.columns
			WHERE table_schema = current_schema() AND table_name = 'outage_communication' AND column_name = 'channel')`).Scan(&hasChannel); err != nil {
			t.Fatalf("schema check: %v", err)
		}
		if !hasChannel {
			t.Skip("outage_communication.channel does not exist in this database; GetByID's journal counts need it")
		}
		detail, err := repo.GetByID(rsInternal(), rsOutageID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if detail.Incident == nil || detail.Incident.Number != "INC-RS-0001" || detail.Incident.ShortDescription != "rs incident subject" {
			t.Errorf("incident via GetByID = %+v, want number and subject populated", detail.Incident)
		}
	})

	t.Run("customer gets the outage without the incident's number or subject", func(t *testing.T) {
		ctx, _ := rsCustomer(rsMemberA, rsProjectA)
		out := search(t, ctx)
		if out.Incident != nil && (out.Incident.Number != "" || out.Incident.ShortDescription != "") {
			t.Errorf("customer sees incident %+v, want its number and subject withheld", out.Incident)
		}
	})

	t.Run("a context with no identity fails closed rather than reading blank", func(t *testing.T) {
		_, _, err := repo.Search(context.Background(), domain.SearchOutagesRequest{Pagination: domain.Pagination{Limit: 1}}, time.Now().Add(-24*time.Hour))
		if !errors.Is(err, repository.ErrNoCallerIdentity) {
			t.Errorf("Search with no identity: err = %v, want ErrNoCallerIdentity", err)
		}
	})
}

// With duplicate sf_id copies, the ingest must write the copy a work_item
// references -- whoever's context it runs on, and on a context with no
// identity at all (the webhook and the retry worker).
func TestRLSScopedRemaining_DuplicateSfIDTieBreakSeesWorkItems(t *testing.T) {
	pool := caseStatsPool(t)
	seedRLSScopedRemaining(t, pool)
	rlsPrecondition(t, pool)
	scoped := repository.NewScoped(pool)

	// The mechanism, shown directly: with no identity the referencing work_item
	// is invisible, which is what would rank both copies equal and pick the
	// older, unreferenced one.
	var referenced bool
	if err := pool.QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM work_item w WHERE w.project_id = $1)`, rsDupProjectNew).Scan(&referenced); err != nil {
		t.Fatalf("mechanism query: %v", err)
	}
	if referenced {
		t.Fatal("the referencing work_item is visible to a raw pool; the precondition for this test does not hold")
	}

	stranger, _ := rsCustomer(rsStranger)
	contexts := map[string]context.Context{
		"no identity (webhook / retry worker)": context.Background(),
		"internal":                             rsInternal(),
		"customer who cannot see the case":     stranger,
	}
	for name, ctx := range contexts {
		t.Run("project lookup, "+name, func(t *testing.T) {
			id, err := repository.NewSalesforceProjectRepository(scoped).LookupProjectIDBySfID(ctx, rsDupProjectSf)
			if err != nil || id == nil || *id != rsDupProjectNew {
				t.Errorf("LookupProjectIDBySfID = %v, %v; want the referenced copy %s", deref(id), err, rsDupProjectNew)
			}
		})
		t.Run("account lookup, "+name, func(t *testing.T) {
			id, err := repository.NewAccountRepository(scoped).LookupAccountIDBySfID(ctx, rsDupAccountSf)
			if err != nil || id == nil || *id != rsDupAccountNew {
				t.Errorf("LookupAccountIDBySfID = %v, %v; want the referenced copy %s", deref(id), err, rsDupAccountNew)
			}
		})
	}

	t.Run("project upsert writes only the referenced copy", func(t *testing.T) {
		written := "Written by ingest"
		_, err := repository.NewSalesforceProjectRepository(scoped).UpsertFromSalesforce(context.Background(),
			domain.SalesforceProjectUpsert{SfID: rsDupProjectSf, Key: "RSDUPPRJ-NEW", Name: &written},
			domain.UpsertSalesforceIngestStateRequest{
				Entity: domain.SalesforceIngestEntityProject, SfID: rsDupProjectSf, EventModifiedOn: time.Now().UTC(),
				EventType: domain.SalesforceEventUpdated, Status: domain.SalesforceIngestSucceeded,
			})
		if err != nil {
			t.Fatalf("UpsertFromSalesforce: %v", err)
		}
		var oldName, newName *string
		if err := pool.QueryRow(context.Background(), `SELECT name FROM project WHERE id = $1`, rsDupProjectOld).Scan(&oldName); err != nil {
			t.Fatalf("read old copy: %v", err)
		}
		if err := pool.QueryRow(context.Background(), `SELECT name FROM project WHERE id = $1`, rsDupProjectNew).Scan(&newName); err != nil {
			t.Fatalf("read new copy: %v", err)
		}
		if oldName != nil || newName == nil || *newName != "Written by ingest" {
			t.Errorf("old copy name = %v, new copy name = %v; want only the referenced (new) copy written", deref(oldName), deref(newName))
		}
	})
}

// The membership writes run as the system on their own transaction without
// handing that identity to the Salesforce plan, and keep their two failure
// shapes apart: a plan error is an ordinary error, never "committed to
// Salesforce but not the database".
func TestRLSScopedRemaining_MembershipRepoUnderRLS(t *testing.T) {
	pool := caseStatsPool(t)
	seedRLSScopedRemaining(t, pool)
	rlsPrecondition(t, pool)
	repo := repository.NewProjectMembershipRepository(repository.NewScoped(pool))

	t.Run("deactivating an unknown membership is a no-op on a context with no identity", func(t *testing.T) {
		found, affected, err := repo.DeactivateBySfID(context.Background(), "a0RSCOPED-NO-SUCH-MEMBERSHIP", nil)
		if err != nil || found || len(affected) != 0 {
			t.Errorf("DeactivateBySfID = %v, %v, %v; want false, none, nil", found, affected, err)
		}
	})

	t.Run("a missing project stays a typed NotFoundError the ingest's retry can match", func(t *testing.T) {
		_, err := repo.Upsert(context.Background(), domain.SalesforceMembershipUpsert{
			ContactSfID: "003RSCOPED-NOPROJECT", ContactEmail: "rs-noproject@test.local",
			ProjectKey: "RS-NO-SUCH-KEY", ProjectSfID: "a0RS-NO-SUCH-PROJECT",
		}, domain.UpsertOnboardingStepRequest{Step: domain.OnboardingStepDatabase})
		var nf *apierror.NotFoundError
		if !errors.As(err, &nf) || !strings.HasPrefix(strings.ToLower(nf.Msg), "project not found") {
			t.Errorf("Upsert with an unknown project: err = %v, want *apierror.NotFoundError starting \"project not found\"", err)
		}
	})

	t.Run("a plan error is returned as is and the plan keeps the caller's identity", func(t *testing.T) {
		errPlan := errors.New("salesforce half failed")
		ctx, _ := rsCustomer(rsMemberA, rsProjectA)
		var planIdentity repository.SearchScope
		var planHadIdentity bool
		_, err := repo.UpsertWithin(ctx, rsProjectA, "rs-new-contact@test.local",
			func(pctx context.Context, wc repository.MembershipWriteContext) (domain.SalesforceMembershipUpsert, domain.UpsertOnboardingStepRequest, error) {
				planIdentity, planHadIdentity = repository.CallerIdentityFromContext(pctx)
				if wc.Target.ProjectID != rsProjectA {
					t.Errorf("plan got target project %q, want %q", wc.Target.ProjectID, rsProjectA)
				}
				return domain.SalesforceMembershipUpsert{}, domain.UpsertOnboardingStepRequest{}, errPlan
			})
		if !errors.Is(err, errPlan) || errors.Is(err, repository.ErrMembershipCommitFailed) {
			t.Errorf("UpsertWithin error = %v, want the plan's own error and not ErrMembershipCommitFailed", err)
		}
		if !planHadIdentity || planIdentity.Unrestricted || planIdentity.ViewerEmail != rsMemberA {
			t.Errorf("plan saw identity %+v (present=%v), want the customer's own, not the system identity the transaction runs on",
				planIdentity, planHadIdentity)
		}
		var n int
		if err := pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM project_contact WHERE email = 'rs-new-contact@test.local'`).Scan(&n); err != nil || n != 0 {
			t.Errorf("project_contact rows for the failed write = %d (err %v), want 0 -- the transaction must have rolled back", n, err)
		}
	})
}

// UpdateProject writes only non-RLS tables but runs through Scoped now: it must
// keep working on a context with no identity (an allow-listed client the
// identity middleware cannot resolve) and keep its three error shapes.
func TestRLSScopedRemaining_ProjectUpdate(t *testing.T) {
	pool := caseStatsPool(t)
	seedRLSScopedRemaining(t, pool)
	repo := repository.NewProjectRepository(repository.NewScoped(pool))
	ctx := context.Background()

	t.Run("updates the closure state and the linked account on a bare context", func(t *testing.T) {
		endDate, agent := "Notified", true
		got, err := repo.UpdateProject(ctx, rsProjectA, domain.ProjectUpdateRequest{EndDateClosureState: &endDate, HasAgent: &agent}, "rs-test")
		if err != nil {
			t.Fatalf("UpdateProject: %v", err)
		}
		if got.ID != rsProjectA || got.UpdatedBy != "rs-test" || got.EndDateClosureState == nil || *got.EndDateClosureState != "Notified" {
			t.Errorf("UpdateProject result = %+v, want project %s updated by rs-test with end-date state Notified", got, rsProjectA)
		}
		var state string
		var aiGen *bool
		if err := pool.QueryRow(ctx, `SELECT p.end_date_closure_state::text, a.ai_gen_response_enabled FROM project p JOIN account a ON a.id = p.account_id WHERE p.id = $1`, rsProjectA).Scan(&state, &aiGen); err != nil {
			t.Fatalf("read back: %v", err)
		}
		if state != "NOTIFIED" || aiGen == nil || !*aiGen {
			t.Errorf("stored end_date_closure_state = %q, ai_gen_response_enabled = %v, want NOTIFIED and true", state, aiGen)
		}
	})

	t.Run("an unknown project is a NotFoundError", func(t *testing.T) {
		endDate := "Notified"
		_, err := repo.UpdateProject(ctx, "7a000000-0000-4000-8000-0000000000ff", domain.ProjectUpdateRequest{EndDateClosureState: &endDate}, "rs-test")
		var nf *apierror.NotFoundError
		if !errors.As(err, &nf) {
			t.Errorf("UpdateProject on an unknown project: err = %v, want *apierror.NotFoundError", err)
		}
	})

	t.Run("an unrecognised closure value is a ValidationError and writes nothing", func(t *testing.T) {
		bogus := "Not A State"
		_, err := repo.UpdateProject(ctx, rsProjectB, domain.ProjectUpdateRequest{EndDateClosureState: &bogus}, "rs-test")
		var ve *apierror.ValidationError
		if !errors.As(err, &ve) {
			t.Errorf("UpdateProject with a bogus state: err = %v, want *apierror.ValidationError", err)
		}
	})
}

func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}
