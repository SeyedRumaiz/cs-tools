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

// This is an integration test: it applies migration 0278 (with 0180's number series) in a private schema and runs
// WSO2CloudRequestRepository against it. Skipped without a DSN:
//
//	WSO2_CLOUD_REQUEST_TEST_DSN=postgres://... go test ./internal/repository/ -run WSO2CloudRequest

package repository_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/apierror"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/repository"
)

func wso2CloudRequestTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("WSO2_CLOUD_REQUEST_TEST_DSN")
	if dsn == "" {
		t.Skip("WSO2_CLOUD_REQUEST_TEST_DSN not set")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(admin.Close)
	schema := fmt.Sprintf("wcr_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE") })

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect to schema: %v", err)
	}
	t.Cleanup(pool.Close)

	// 0180's number_series table and next_series_number function, which 0278's number default needs.
	numbering, err := os.ReadFile("../../migrations/0180_native_number_series.sql")
	if err != nil {
		t.Fatalf("read 0180: %v", err)
	}
	for _, part := range []struct{ from, to string }{
		{"CREATE TABLE number_series (", ");"},
		{"CREATE OR REPLACE FUNCTION next_series_number(", "$$ LANGUAGE plpgsql;"},
	} {
		sql := string(numbering)
		start := strings.Index(sql, part.from)
		if start < 0 {
			t.Fatalf("0180 has no %q", part.from)
		}
		end := strings.Index(sql[start:], part.to)
		if end < 0 {
			t.Fatalf("0180 has no end for %q", part.from)
		}
		if _, err := pool.Exec(ctx, sql[start:start+end+len(part.to)]); err != nil {
			t.Fatalf("apply 0180 part %q: %v", part.from, err)
		}
	}
	migration, err := os.ReadFile("../../migrations/0278_wso2_cloud_request.sql")
	if err != nil {
		t.Fatalf("read 0278: %v", err)
	}
	// Twice: the migration must be safe to re-run.
	for i := 0; i < 2; i++ {
		if _, err := pool.Exec(ctx, string(migration)); err != nil {
			t.Fatalf("apply 0278 (run %d): %v", i+1, err)
		}
	}
	return pool
}

func TestWSO2CloudRequestIntegration_Lifecycle(t *testing.T) {
	pool := wso2CloudRequestTestPool(t)
	repo := repository.NewWSO2CloudRequestRepository(pool)
	ctx := context.Background()

	enterprise, free := domain.WSO2CloudTierEnterprise, domain.WSO2CloudTierFree
	upgrade, err := repo.Create(ctx, "engineer@wso2.com", domain.CreateWSO2CloudRequestRequest{
		Type: domain.WSO2CloudRequestUpgradeOrganization, TargetID: "org-1", TargetLabel: "acme",
		Tier: &enterprise, Limits: json.RawMessage(`{"monthlyActiveUsers": 50000}`),
		PreviousTier: &free, PreviousLimits: json.RawMessage(`{"monthlyActiveUsers": 1000}`),
	})
	if err != nil {
		t.Fatalf("create upgrade: %v", err)
	}
	if upgrade.Number != "WCR0000001" || upgrade.Status != domain.WSO2CloudRequestPending || upgrade.RequestedBy != "engineer@wso2.com" {
		t.Fatalf("created %+v", upgrade)
	}
	if upgrade.Tier == nil || *upgrade.Tier != enterprise || upgrade.PreviousTier == nil || *upgrade.PreviousTier != free {
		t.Fatalf("tiers %v / %v", upgrade.Tier, upgrade.PreviousTier)
	}
	var limits map[string]int
	if err := json.Unmarshal(upgrade.Limits, &limits); err != nil || limits["monthlyActiveUsers"] != 50000 {
		t.Fatalf("limits %s: %v", upgrade.Limits, err)
	}

	// One pending request per type and target.
	_, err = repo.Create(ctx, "other@wso2.com", domain.CreateWSO2CloudRequestRequest{
		Type: domain.WSO2CloudRequestUpgradeOrganization, TargetID: "org-1", TargetLabel: "acme", Tier: &enterprise,
	})
	var conflict *apierror.ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("second pending upgrade: got %v, want ConflictError", err)
	}

	reason := "Customer reported a compromised account"
	lock, err := repo.Create(ctx, "engineer@wso2.com", domain.CreateWSO2CloudRequestRequest{
		Type: domain.WSO2CloudRequestLockUser, TargetID: "scim-1", TargetLabel: "jane@acme.com", Reason: &reason,
	})
	if err != nil {
		t.Fatalf("create lock: %v", err)
	}
	if lock.Number <= upgrade.Number || lock.Tier != nil || lock.Limits != nil {
		t.Fatalf("lock %+v", lock)
	}

	// The database refuses a tier on a non-upgrade, even if the service were bypassed.
	if _, err := repo.Create(ctx, "engineer@wso2.com", domain.CreateWSO2CloudRequestRequest{
		Type: domain.WSO2CloudRequestDeleteUser, TargetID: "scim-2", TargetLabel: "x@acme.com", Tier: &free,
	}); err == nil {
		t.Fatalf("a delete_user request with a tier was stored")
	}

	// Resolve once; a second resolve finds nothing pending.
	remark := "No Enterprise contract yet"
	rejected, err := repo.Resolve(ctx, upgrade.ID, domain.WSO2CloudRequestRejected, "approver@wso2.com", &remark)
	if err != nil || rejected == nil {
		t.Fatalf("resolve: %v / %v", rejected, err)
	}
	if rejected.Status != domain.WSO2CloudRequestRejected || rejected.ResolvedBy == nil || *rejected.ResolvedBy != "approver@wso2.com" ||
		rejected.ResolvedOn == nil || rejected.Remark == nil || *rejected.Remark != remark {
		t.Fatalf("rejected %+v", rejected)
	}
	again, err := repo.Resolve(ctx, upgrade.ID, domain.WSO2CloudRequestApproved, "approver@wso2.com", nil)
	if err != nil || again != nil {
		t.Fatalf("second resolve: %v / %v", again, err)
	}

	// Once resolved, the same target can be asked about again.
	if _, err := repo.Create(ctx, "engineer@wso2.com", domain.CreateWSO2CloudRequestRequest{
		Type: domain.WSO2CloudRequestUpgradeOrganization, TargetID: "org-1", TargetLabel: "acme", Tier: &enterprise,
	}); err != nil {
		t.Fatalf("new upgrade after the first was resolved: %v", err)
	}

	// Search: by status, by type, by requester (any case), newest first, with a total.
	page, total, err := repo.Search(ctx, domain.SearchWSO2CloudRequestsRequest{
		Filters:    domain.SearchWSO2CloudRequestsFilters{Statuses: []domain.WSO2CloudRequestStatus{domain.WSO2CloudRequestPending}},
		Pagination: domain.Pagination{Limit: 10},
	})
	if err != nil || total != 2 || len(page) != 2 || page[0].Number <= page[1].Number {
		t.Fatalf("pending search: total %d, %d rows, err %v, first %+v", total, len(page), err, page)
	}
	requester := "ENGINEER@wso2.com"
	page, total, err = repo.Search(ctx, domain.SearchWSO2CloudRequestsRequest{
		Filters:    domain.SearchWSO2CloudRequestsFilters{RequestedBy: &requester, Types: []domain.WSO2CloudRequestType{domain.WSO2CloudRequestLockUser}},
		Pagination: domain.Pagination{Limit: 10},
	})
	if err != nil || total != 1 || page[0].ID != lock.ID {
		t.Fatalf("requester+type search: total %d, err %v", total, err)
	}
	page, total, err = repo.Search(ctx, domain.SearchWSO2CloudRequestsRequest{Pagination: domain.Pagination{Limit: 1, Offset: 1}})
	if err != nil || total != 3 || len(page) != 1 {
		t.Fatalf("paged search: total %d, %d rows, err %v", total, len(page), err)
	}

	// Cancel: only a pending request is deleted.
	if ok, err := repo.Delete(ctx, upgrade.ID); err != nil || ok {
		t.Fatalf("delete resolved: %v / %v", ok, err)
	}
	if ok, err := repo.Delete(ctx, lock.ID); err != nil || !ok {
		t.Fatalf("delete pending: %v / %v", ok, err)
	}
	if got, err := repo.Get(ctx, lock.ID); err != nil || got != nil {
		t.Fatalf("deleted request still there: %v / %v", got, err)
	}

	// A cancelled request's number is not given out again (numbers can have gaps: the refused insert above took one).
	next, err := repo.Create(ctx, "engineer@wso2.com", domain.CreateWSO2CloudRequestRequest{
		Type: domain.WSO2CloudRequestLockUser, TargetID: "scim-1", TargetLabel: "jane@acme.com", Reason: &reason,
	})
	if err != nil || next.Number <= lock.Number {
		t.Fatalf("next number %q after %q: %v", next.Number, lock.Number, err)
	}
}
