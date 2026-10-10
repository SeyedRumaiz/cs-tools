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

package repository_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/repository"
)

// Proves the announcement registry's one-shot read returns exactly what the
// registry used to collect by paging /cases/search 50 at a time: same rows,
// same order, same values for every field the registry reads, across several
// filters. Runs against a real Postgres (see announcementVisibilityPool).
//
//	ANNOUNCEMENT_VISIBILITY_TEST_DSN=postgres://... go test ./internal/repository/ -run AnnouncementRegistry
func TestAnnouncementRegistryRepoMatchesPagedSearchIntegration(t *testing.T) {
	pool := announcementVisibilityPool(t)
	seedAnnouncementVisibilityFixtures(t, pool)
	ctx := repository.WithSystemIdentity(context.Background())
	scoped := repository.NewScoped(pool)

	// 130 extra announcements: three pages of 50, groups of three sharing one
	// updated_on (so the id tie-break decides their order), every 7th closed.
	base := time.Now().UTC().Truncate(time.Second)
	for i := 0; i < 130; i++ {
		id := fmt.Sprintf("34%06d-0000-4000-8000-%012d", i, i)
		updated := base.Add(-time.Duration(i/3) * time.Minute)
		if _, err := scoped.Exec(ctx, `INSERT INTO work_item (id, created_on, updated_on, created_by, updated_by, number, wso2_id, subject, type, project_id)
			VALUES ($1, $2, $2, 'registry-test@test.local', 'test', $3, $4, $5, 'ANNOUNCEMENT', $6)`,
			id, updated, fmt.Sprintf("AV-EQ-%03d", i), fmt.Sprintf("AV-EQ-WSO2-%03d", i), fmt.Sprintf("registry equiv announcement %03d", i), avProjectID); err != nil {
			t.Fatalf("seed work_item %d: %v", i, err)
		}
		state := "OPEN"
		if i%7 == 0 {
			state = "CLOSE"
		}
		if _, err := scoped.Exec(ctx, `INSERT INTO announcement (id, announcement_type, state) VALUES ($1, 'GENERAL', $2)`, id, state); err != nil {
			t.Fatalf("seed announcement %d: %v", i, err)
		}
	}

	caseRepo := repository.NewCaseRepository(scoped)
	regRepo := repository.NewAnnouncementRegistryRepository(scoped)
	scope := repository.SearchScope{Unrestricted: true}
	announcements := []string{"announcement"}

	requests := map[string]domain.SearchCasesRequest{
		"all announcements":     {Parsed: domain.ParsedCaseFilters{Types: announcements}},
		"one project":           {Parsed: domain.ParsedCaseFilters{Types: announcements, ProjectIDs: []string{avProjectID}}},
		"closed only":           {Parsed: domain.ParsedCaseFilters{Types: announcements, States: []domain.CaseState{domain.CaseStateClosed}}},
		"open only":             {Parsed: domain.ParsedCaseFilters{Types: announcements, States: []domain.CaseState{domain.CaseStateOpen}}},
		"free text":             {Filters: domain.SearchCasesFilters{SearchQuery: "equiv announcement 01"}, Parsed: domain.ParsedCaseFilters{Types: announcements}},
		"free text, no matches": {Filters: domain.SearchCasesFilters{SearchQuery: "no-such-registry-text"}, Parsed: domain.ParsedCaseFilters{Types: announcements}},
		"project and free text": {Filters: domain.SearchCasesFilters{SearchQuery: "equiv"}, Parsed: domain.ParsedCaseFilters{Types: announcements, ProjectIDs: []string{avProjectID}}},
	}

	for name, req := range requests {
		t.Run(name, func(t *testing.T) {
			// What the registry did before: page /cases/search, newest-updated first.
			var paged []domain.SearchCaseView
			for offset := 0; ; offset += 50 {
				pageReq := req
				pageReq.SortBy = domain.CaseSort{Field: domain.CaseSortFieldUpdatedOn, Order: domain.CaseSortOrderDesc}
				pageReq.Pagination = domain.Pagination{Limit: 50, Offset: offset}
				page, total, err := caseRepo.SearchCases(ctx, pageReq, scope)
				if err != nil {
					t.Fatalf("paged SearchCases offset %d: %v", offset, err)
				}
				paged = append(paged, page...)
				if len(page) == 0 || len(paged) >= total {
					break
				}
			}

			got, err := regRepo.SearchAnnouncementCases(ctx, req, scope, 10000)
			if err != nil {
				t.Fatalf("SearchAnnouncementCases: %v", err)
			}
			if len(got) != len(paged) {
				t.Fatalf("row count: one-shot=%d, paged=%d", len(got), len(paged))
			}
			for i := range got {
				if d := diffRegistryRow(got[i], paged[i]); d != "" {
					t.Fatalf("row %d (%s) differs from the paged result: %s", i, paged[i].ID, d)
				}
			}
			t.Logf("%d rows identical, in the same order", len(got))
		})
	}

	t.Run("the 130 seeded announcements are all returned", func(t *testing.T) {
		got, err := regRepo.SearchAnnouncementCases(ctx, requests["one project"], scope, 10000)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) < 130 {
			t.Fatalf("got %d rows for the seeded project, want at least 130", len(got))
		}
	})

	t.Run("exactly the bound is allowed, one over is an error", func(t *testing.T) {
		all, err := regRepo.SearchAnnouncementCases(ctx, requests["one project"], scope, 10000)
		if err != nil {
			t.Fatal(err)
		}
		n := len(all)
		if got, err := regRepo.SearchAnnouncementCases(ctx, requests["one project"], scope, n); err != nil || len(got) != n {
			t.Fatalf("maxRows == %d matches: got %d rows, err %v; want all %d rows and no error", n, len(got), err, n)
		}
		if _, err := regRepo.SearchAnnouncementCases(ctx, requests["one project"], scope, n-1); !errors.Is(err, repository.ErrTooManyRegistryRows) {
			t.Fatalf("maxRows == %d with %d matches: want ErrTooManyRegistryRows, got %v", n-1, n, err)
		}
	})

	t.Run("more rows than the bound is an error, not a truncated list", func(t *testing.T) {
		_, err := regRepo.SearchAnnouncementCases(ctx, requests["one project"], scope, 5)
		if !errors.Is(err, repository.ErrTooManyRegistryRows) {
			t.Fatalf("want ErrTooManyRegistryRows, got %v", err)
		}
	})
}

// diffRegistryRow compares every field the registry reads (id, number, internal
// id, subject, state, dates, creator, project) and describes the first mismatch.
func diffRegistryRow(a, b domain.SearchCaseView) string {
	str := func(p *string) string {
		if p == nil {
			return "<nil>"
		}
		return *p
	}
	switch {
	case a.ID != b.ID:
		return fmt.Sprintf("id %q vs %q", a.ID, b.ID)
	case a.Number != b.Number:
		return fmt.Sprintf("number %q vs %q", a.Number, b.Number)
	case a.InternalID != b.InternalID:
		return fmt.Sprintf("internalId %q vs %q", a.InternalID, b.InternalID)
	case str(a.Subject) != str(b.Subject):
		return fmt.Sprintf("subject %q vs %q", str(a.Subject), str(b.Subject))
	case str(a.State) != str(b.State):
		return fmt.Sprintf("state %q vs %q", str(a.State), str(b.State))
	case a.CreatedOn != b.CreatedOn:
		return fmt.Sprintf("createdOn %q vs %q", a.CreatedOn, b.CreatedOn)
	case a.UpdatedOn != b.UpdatedOn:
		return fmt.Sprintf("updatedOn %q vs %q", a.UpdatedOn, b.UpdatedOn)
	case a.Type != b.Type:
		return fmt.Sprintf("type %q vs %q", a.Type, b.Type)
	}
	if (a.CreatedBy == nil) != (b.CreatedBy == nil) || (a.CreatedBy != nil && (a.CreatedBy.Email != b.CreatedBy.Email || a.CreatedBy.Name != b.CreatedBy.Name)) {
		return fmt.Sprintf("createdBy %+v vs %+v", a.CreatedBy, b.CreatedBy)
	}
	if (a.Project == nil) != (b.Project == nil) || (a.Project != nil && *a.Project != *b.Project) {
		return fmt.Sprintf("project %+v vs %+v", a.Project, b.Project)
	}
	return ""
}
