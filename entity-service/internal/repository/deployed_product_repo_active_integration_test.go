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
// PostgreSQL instance, regression-testing a gap where deactivating a
// deployed product (PATCH .../products/{id} {active: false}) wrote the
// column correctly but this query never filtered on it at all, so a
// "deleted" product kept showing up in every search exactly as before --
// the delete confirmation and the write both succeeded, but nothing ever
// disappeared from the UI. Same DSN and skip-when-unset pattern as
// deployed_product_repo_search_integration_test.go:
//
//	CASE_STATS_TEST_DSN=postgres://... go test ./internal/repository/ -run TestSearchDeployedProductsExcludesDeactivated

package repository_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/repository"
)

const (
	dpActiveProjectID    = "38111111-1111-1111-1111-111111111111"
	dpActiveDeploymentID = "38222222-0000-0000-0000-000000000001"
	dpActiveProductID    = "38333333-0000-0000-0000-000000000001"
	// dpActiveTrueID is an ordinary, active deployed product.
	dpActiveTrueID = "38444444-0000-0000-0000-000000000001"
	// dpActiveFalseID has been deactivated ("deleted" from the caller's
	// point of view) and must never appear in search results.
	dpActiveFalseID = "38555555-0000-0000-0000-000000000001"
	// dpActiveNullID leaves `active` unset -- NULL counts as active, the
	// same convention AccessService.ResolveScope already uses for
	// "user".is_active.
	dpActiveNullID = "38666666-0000-0000-0000-000000000001"
)

func seedDeployedProductActiveFixture(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := repository.WithSystemIdentity(context.Background())
	scoped := repository.NewScoped(pool)

	cleanup := func() {
		_, _ = scoped.Exec(ctx, `DELETE FROM deployed_product WHERE id IN ($1, $2, $3)`,
			dpActiveTrueID, dpActiveFalseID, dpActiveNullID)
		_, _ = scoped.Exec(ctx, `DELETE FROM deployment WHERE id = $1`, dpActiveDeploymentID)
		_, _ = pool.Exec(ctx, `DELETE FROM product WHERE id = $1`, dpActiveProductID)
		_, _ = pool.Exec(ctx, `DELETE FROM project WHERE id = $1`, dpActiveProjectID)
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
	          VALUES ($1, now(), now(), 'dp-active-test', 'dp-active-test', 'DPACTIVETEST', 'sf-dpactivetest', (now() + INTERVAL '30 days')::date)`,
		dpActiveProjectID)

	mustExec(`INSERT INTO deployment (id, created_on, updated_on, created_by, updated_by, number, name, is_active, project_id)
	          VALUES ($1, now(), now(), 'dp-active-test', 'dp-active-test', 'DPACTIVEDEP01', 'dp active dep', true, $2)`,
		dpActiveDeploymentID, dpActiveProjectID)

	if _, err := pool.Exec(ctx, `INSERT INTO product (id, created_on, updated_on, created_by, updated_by, manufacturer, category, name, code)
	          VALUES ($1, now(), now(), 'dp-active-test', 'dp-active-test', 'WSO2', 'SOFTWARE', 'DP Active Test Product', 'dpactivetest')`,
		dpActiveProductID); err != nil {
		t.Fatalf("seed product: %v", err)
	}

	mustExec(`INSERT INTO deployed_product (id, created_on, updated_on, created_by, updated_by, number, active, project_id, deployment_id, product_id)
	          VALUES ($1, now(), now(), 'dp-active-test', 'dp-active-test', 'DPACTIVETRUE01', true, $2, $3, $4)`,
		dpActiveTrueID, dpActiveProjectID, dpActiveDeploymentID, dpActiveProductID)

	mustExec(`INSERT INTO deployed_product (id, created_on, updated_on, created_by, updated_by, number, active, project_id, deployment_id, product_id)
	          VALUES ($1, now(), now(), 'dp-active-test', 'dp-active-test', 'DPACTIVEFALSE01', false, $2, $3, $4)`,
		dpActiveFalseID, dpActiveProjectID, dpActiveDeploymentID, dpActiveProductID)

	mustExec(`INSERT INTO deployed_product (id, created_on, updated_on, created_by, updated_by, number, active, project_id, deployment_id, product_id)
	          VALUES ($1, now(), now(), 'dp-active-test', 'dp-active-test', 'DPACTIVENULL01', NULL, $2, $3, $4)`,
		dpActiveNullID, dpActiveProjectID, dpActiveDeploymentID, dpActiveProductID)
}

func TestSearchDeployedProductsExcludesDeactivated(t *testing.T) {
	pool := caseStatsPool(t)
	seedDeployedProductActiveFixture(t, pool)

	ctx := repository.WithSystemIdentity(context.Background())
	repo := repository.NewDeployedProductRepository(repository.NewScoped(pool))

	views, total, err := repo.SearchDeployedProducts(ctx, domain.SearchDeployedProductsRequest{
		Pagination:    domain.Pagination{Limit: 50, Offset: 0},
		DeploymentIDs: []string{dpActiveDeploymentID},
	})
	if err != nil {
		t.Fatalf("SearchDeployedProducts: %v", err)
	}
	if total != 2 {
		t.Fatalf("total = %d, want 2 (the deactivated row excluded)", total)
	}

	seen := make(map[string]bool, len(views))
	for _, v := range views {
		seen[v.ID] = true
	}
	if !seen[dpActiveTrueID] {
		t.Errorf("active=true row %s missing from results", dpActiveTrueID)
	}
	if !seen[dpActiveNullID] {
		t.Errorf("active=NULL row %s missing from results (NULL must count as active)", dpActiveNullID)
	}
	if seen[dpActiveFalseID] {
		t.Errorf("active=false row %s present in results, want it excluded (this is the delete bug)", dpActiveFalseID)
	}
}
