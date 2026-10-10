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
// deployed_product_repo.go's SearchDeployedProducts against a live
// PostgreSQL instance, regression-testing a read-side gap where
// description/update_level_info were written correctly by
// UpdateDeployedProductFields but never selected back by this query --
// a fake DeployedProductRepository can't reproduce a column simply missing
// from a SELECT list. Same DSN and skip-when-unset pattern as
// project_stats_repo_integration_test.go/time_card_repo_test.go (package
// repository_test, reuses caseStatsPool):
//
//	CASE_STATS_TEST_DSN=postgres://... go test ./internal/repository/ -run TestSearchDeployedProductsDescriptionAndUpdates

package repository_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/repository"
)

const (
	dpSearchProjectID    = "37111111-1111-1111-1111-111111111111"
	dpSearchDeploymentID = "37222222-0000-0000-0000-000000000001"
	dpSearchProductID    = "37333333-0000-0000-0000-000000000001"
	// dpSearchFullID has both description and update_level_info populated.
	dpSearchFullID = "37444444-0000-0000-0000-000000000001"
	// dpSearchBareID leaves both NULL, the pre-existing (and still valid)
	// "nothing recorded" shape.
	dpSearchBareID = "37555555-0000-0000-0000-000000000001"
)

// seedDeployedProductSearchFixture creates one project/deployment/product
// and two deployed_product rows: one with a description and a two-entry
// update_level_info history, one with both left NULL.
func seedDeployedProductSearchFixture(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	// WithSystemIdentity: deployed_product/deployment are RLS-protected
	// (migrations 0176/0177); an internal identity is what makes this
	// seed's writes succeed regardless of which project this fixture uses,
	// same as seedProjectStats/seedCaseStats in this package.
	ctx := repository.WithSystemIdentity(context.Background())
	scoped := repository.NewScoped(pool)

	cleanup := func() {
		_, _ = scoped.Exec(ctx, `DELETE FROM deployed_product WHERE id IN ($1, $2)`, dpSearchFullID, dpSearchBareID)
		_, _ = scoped.Exec(ctx, `DELETE FROM deployment WHERE id = $1`, dpSearchDeploymentID)
		_, _ = pool.Exec(ctx, `DELETE FROM product WHERE id = $1`, dpSearchProductID)
		_, _ = pool.Exec(ctx, `DELETE FROM project WHERE id = $1`, dpSearchProjectID)
	}
	cleanup()
	t.Cleanup(cleanup)

	mustExec := func(sql string, args ...any) {
		t.Helper()
		if _, err := scoped.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("seed (%.60s): %v", sql, err)
		}
	}

	mustExec(`INSERT INTO project (id, created_on, updated_on, created_by, updated_by, key, sf_id, end_date)
	          VALUES ($1, now(), now(), 'dp-search-test', 'dp-search-test', 'DPSEARCHTEST', 'sf-dpsearchtest', (now() + INTERVAL '30 days')::date)`, dpSearchProjectID)

	mustExec(`INSERT INTO deployment (id, created_on, updated_on, created_by, updated_by, number, name, is_active, project_id)
	          VALUES ($1, now(), now(), 'dp-search-test', 'dp-search-test', 'DPSEARCHDEP01', 'dp search dep', true, $2)`,
		dpSearchDeploymentID, dpSearchProjectID)

	if _, err := pool.Exec(ctx, `INSERT INTO product (id, created_on, updated_on, created_by, updated_by, manufacturer, category, name, code)
	          VALUES ($1, now(), now(), 'dp-search-test', 'dp-search-test', 'WSO2', 'SOFTWARE', 'DP Search Test Product', 'dpsearchtest')`,
		dpSearchProductID); err != nil {
		t.Fatalf("seed product: %v", err)
	}

	mustExec(`INSERT INTO deployed_product (id, created_on, updated_on, created_by, updated_by, number, active, project_id, deployment_id, product_id, description, update_level_info)
	          VALUES ($1, now(), now(), 'dp-search-test', 'dp-search-test', 'DPSEARCHFULL01', true, $2, $3, $4, $5, $6::jsonb)`,
		dpSearchFullID, dpSearchProjectID, dpSearchDeploymentID, dpSearchProductID,
		"Production instance, customer-provided note",
		`[{"updateLevel":5,"date":"2026-01-10","details":"Quarterly patch"},{"updateLevel":4,"date":"2025-10-02","details":null}]`,
	)

	mustExec(`INSERT INTO deployed_product (id, created_on, updated_on, created_by, updated_by, number, active, project_id, deployment_id, product_id)
	          VALUES ($1, now(), now(), 'dp-search-test', 'dp-search-test', 'DPSEARCHBARE01', true, $2, $3, $4)`,
		dpSearchBareID, dpSearchProjectID, dpSearchDeploymentID, dpSearchProductID)
}

// TestSearchDeployedProductsDescriptionAndUpdates regression-tests the gap
// where UpdateDeployedProductFields wrote description/update_level_info
// correctly but SearchDeployedProducts -- the only read endpoint for
// deployed products -- never selected either column back, so an edited
// deployed product's description and update history silently reverted to
// null/empty on the very next search.
func TestSearchDeployedProductsDescriptionAndUpdates(t *testing.T) {
	pool := caseStatsPool(t)
	seedDeployedProductSearchFixture(t, pool)

	ctx := repository.WithSystemIdentity(context.Background())
	repo := repository.NewDeployedProductRepository(repository.NewScoped(pool))

	views, total, err := repo.SearchDeployedProducts(ctx, domain.SearchDeployedProductsRequest{
		Pagination:    domain.Pagination{Limit: 50, Offset: 0},
		DeploymentIDs: []string{dpSearchDeploymentID},
	})
	if err != nil {
		t.Fatalf("SearchDeployedProducts: %v", err)
	}
	if total < 2 {
		t.Fatalf("total = %d, want at least 2", total)
	}

	byID := make(map[string]domain.DeployedProductView, len(views))
	for _, v := range views {
		byID[v.ID] = v
	}

	full, ok := byID[dpSearchFullID]
	if !ok {
		t.Fatalf("full fixture row %s not found in search results", dpSearchFullID)
	}
	if full.Description == nil || *full.Description != "Production instance, customer-provided note" {
		t.Errorf("full.Description = %v, want %q", full.Description, "Production instance, customer-provided note")
	}
	if len(full.Updates) != 2 {
		t.Fatalf("full.Updates has %d entries, want 2: %+v", len(full.Updates), full.Updates)
	}
	if full.Updates[0].UpdateLevel != 5 || full.Updates[0].Date != "2026-01-10" {
		t.Errorf("full.Updates[0] = %+v, want {UpdateLevel:5 Date:2026-01-10 ...}", full.Updates[0])
	}
	if full.Updates[0].Details == nil || *full.Updates[0].Details != "Quarterly patch" {
		t.Errorf("full.Updates[0].Details = %v, want %q", full.Updates[0].Details, "Quarterly patch")
	}
	if full.Updates[1].UpdateLevel != 4 || full.Updates[1].Details != nil {
		t.Errorf("full.Updates[1] = %+v, want {UpdateLevel:4 Details:nil ...}", full.Updates[1])
	}

	bare, ok := byID[dpSearchBareID]
	if !ok {
		t.Fatalf("bare fixture row %s not found in search results", dpSearchBareID)
	}
	if bare.Description != nil {
		t.Errorf("bare.Description = %v, want nil", bare.Description)
	}
	if len(bare.Updates) != 0 {
		t.Errorf("bare.Updates = %+v, want nil/empty", bare.Updates)
	}
}
