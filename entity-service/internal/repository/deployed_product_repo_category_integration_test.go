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
// PostgreSQL instance, regression-testing a gap where a deployed product
// with no product_category set (the real-world majority -- e.g. 251 of 501
// "WSO2 API Manager" rows in staging) was silently excluded from every
// ProductCategories-filtered search, even though it's a perfectly valid,
// active product -- just never categorized. A project type that restricts
// SR categories (ProjectFeatures.SrProductCategories) then showed "Product
// Version: Not available" in the SR creation form despite the deployment
// having active products. Same DSN and skip-when-unset pattern as the
// other deployed_product_repo integration tests:
//
//	CASE_STATS_TEST_DSN=postgres://... go test ./internal/repository/ -run TestSearchDeployedProductsCategoryFilter

package repository_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/repository"
)

const (
	dpCategoryProjectID    = "39111111-1111-1111-1111-111111111111"
	dpCategoryDeploymentID = "39222222-0000-0000-0000-000000000001"
	dpCategoryProductID    = "39333333-0000-0000-0000-000000000001"
	// dpCategoryNullID has no product_category set -- the common,
	// real-world case this fix is about.
	dpCategoryNullID = "39444444-0000-0000-0000-000000000001"
	// dpCategoryMatchID is explicitly categorized MS, which the filter
	// below also requests -- must still match.
	dpCategoryMatchID = "39555555-0000-0000-0000-000000000001"
	// dpCategoryMismatchID is explicitly categorized PS, which the filter
	// below does NOT request -- must stay excluded, proving the fix didn't
	// turn the filter into a no-op.
	dpCategoryMismatchID = "39666666-0000-0000-0000-000000000001"
)

func seedDeployedProductCategoryFixture(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := repository.WithSystemIdentity(context.Background())
	scoped := repository.NewScoped(pool)

	cleanup := func() {
		_, _ = scoped.Exec(ctx, `DELETE FROM deployed_product WHERE id IN ($1, $2, $3)`,
			dpCategoryNullID, dpCategoryMatchID, dpCategoryMismatchID)
		_, _ = scoped.Exec(ctx, `DELETE FROM deployment WHERE id = $1`, dpCategoryDeploymentID)
		_, _ = pool.Exec(ctx, `DELETE FROM product WHERE id = $1`, dpCategoryProductID)
		_, _ = pool.Exec(ctx, `DELETE FROM project WHERE id = $1`, dpCategoryProjectID)
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
	          VALUES ($1, now(), now(), 'dp-category-test', 'dp-category-test', 'DPCATTEST', 'sf-dpcattest', (now() + INTERVAL '30 days')::date)`,
		dpCategoryProjectID)

	mustExec(`INSERT INTO deployment (id, created_on, updated_on, created_by, updated_by, number, name, is_active, project_id)
	          VALUES ($1, now(), now(), 'dp-category-test', 'dp-category-test', 'DPCATDEP01', 'dp category dep', true, $2)`,
		dpCategoryDeploymentID, dpCategoryProjectID)

	if _, err := pool.Exec(ctx, `INSERT INTO product (id, created_on, updated_on, created_by, updated_by, manufacturer, category, name, code)
	          VALUES ($1, now(), now(), 'dp-category-test', 'dp-category-test', 'WSO2', 'SOFTWARE', 'DP Category Test Product', 'dpcattest')`,
		dpCategoryProductID); err != nil {
		t.Fatalf("seed product: %v", err)
	}

	mustExec(`INSERT INTO deployed_product (id, created_on, updated_on, created_by, updated_by, number, active, project_id, deployment_id, product_id, product_category)
	          VALUES ($1, now(), now(), 'dp-category-test', 'dp-category-test', 'DPCATNULL01', true, $2, $3, $4, NULL)`,
		dpCategoryNullID, dpCategoryProjectID, dpCategoryDeploymentID, dpCategoryProductID)

	mustExec(`INSERT INTO deployed_product (id, created_on, updated_on, created_by, updated_by, number, active, project_id, deployment_id, product_id, product_category)
	          VALUES ($1, now(), now(), 'dp-category-test', 'dp-category-test', 'DPCATMATCH01', true, $2, $3, $4, 'MS')`,
		dpCategoryMatchID, dpCategoryProjectID, dpCategoryDeploymentID, dpCategoryProductID)

	mustExec(`INSERT INTO deployed_product (id, created_on, updated_on, created_by, updated_by, number, active, project_id, deployment_id, product_id, product_category)
	          VALUES ($1, now(), now(), 'dp-category-test', 'dp-category-test', 'DPCATMISMATCH01', true, $2, $3, $4, 'PS')`,
		dpCategoryMismatchID, dpCategoryProjectID, dpCategoryDeploymentID, dpCategoryProductID)
}

func TestSearchDeployedProductsCategoryFilter(t *testing.T) {
	pool := caseStatsPool(t)
	seedDeployedProductCategoryFixture(t, pool)

	ctx := repository.WithSystemIdentity(context.Background())
	repo := repository.NewDeployedProductRepository(repository.NewScoped(pool))

	views, total, err := repo.SearchDeployedProducts(ctx, domain.SearchDeployedProductsRequest{
		Pagination:        domain.Pagination{Limit: 50, Offset: 0},
		DeploymentIDs:     []string{dpCategoryDeploymentID},
		ProductCategories: []string{"ms", "pc"},
	})
	if err != nil {
		t.Fatalf("SearchDeployedProducts: %v", err)
	}
	if total != 2 {
		t.Fatalf("total = %d, want 2 (NULL-category wildcard match + the explicit MS match, PS excluded)", total)
	}

	seen := make(map[string]bool, len(views))
	for _, v := range views {
		seen[v.ID] = true
	}
	if !seen[dpCategoryNullID] {
		t.Errorf("NULL-category row %s missing from results -- the wildcard fix isn't working", dpCategoryNullID)
	}
	if !seen[dpCategoryMatchID] {
		t.Errorf("explicitly matching MS row %s missing from results", dpCategoryMatchID)
	}
	if seen[dpCategoryMismatchID] {
		t.Errorf("explicitly mismatching PS row %s present in results, want it excluded (filter must not have become a no-op)", dpCategoryMismatchID)
	}
}

// TestUpdateDeployedProductFields_WritesCategory regression-tests the write
// side of the same gap: before this fix, there was no way for any caller --
// CSM Portal included -- to ever set product_category at all, since neither
// CreateDeployedProductRequest nor UpdateDeployedProductRequest had a field
// for it. Seeds a deployed product with NULL category, patches it to "ms"
// (lower-case, the wire vocabulary), and confirms the real column -- not
// just the echoed response -- actually changed.
func TestUpdateDeployedProductFields_WritesCategory(t *testing.T) {
	pool := caseStatsPool(t)
	seedDeployedProductCategoryFixture(t, pool)

	ctx := repository.WithSystemIdentity(context.Background())
	repo := repository.NewDeployedProductRepository(repository.NewScoped(pool))

	category := "ms"
	_, err := repo.UpdateDeployedProductFields(ctx, domain.UpdateDeployedProductRequest{
		ID:       dpCategoryNullID,
		Category: &category,
	}, "dp-category-test")
	if err != nil {
		t.Fatalf("UpdateDeployedProductFields: %v", err)
	}

	var stored *string
	scoped := repository.NewScoped(pool)
	err = scoped.QueryRow(ctx, `SELECT product_category::TEXT FROM deployed_product WHERE id = $1`, dpCategoryNullID).Scan(&stored)
	if err != nil {
		t.Fatalf("read back product_category: %v", err)
	}
	if stored == nil || *stored != "MS" {
		t.Fatalf("product_category = %v, want \"MS\"", stored)
	}
}
