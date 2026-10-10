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

package repository_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/repository"
)

// Proves announcementRequestRepo.Search's state filters against a real
// Postgres: `states` returns one merged, correctly ordered and paginated
// list (Total describes the merged list, not one state), an empty/nil
// `states` is "no state filter" rather than "match nothing", and the older
// single `state` filter and the scheduled-publish cron query still behave as
// before. Rows are isolated from anything else in the database by a unique
// created_by, which every query below also filters on.
//
//	ANNOUNCEMENT_VISIBILITY_TEST_DSN=postgres://... go test ./internal/repository/ -run AnnouncementRequestSearchStates
func TestAnnouncementRequestSearchStatesIntegration(t *testing.T) {
	pool := announcementVisibilityPool(t)
	ctx := context.Background()
	repo := repository.NewAnnouncementRequestRepository(pool)

	creator := fmt.Sprintf("states-test-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM announcement_requests WHERE created_by = $1`, creator)
	})

	// Newest first, so the expected order for any subset is just this list
	// filtered. scheduled_on is set on exactly one approved row, already in
	// the past, so the cron query has one row to find.
	base := time.Now().UTC().Truncate(time.Second)
	seed := []struct {
		name        string
		state       string
		ageMinutes  int
		scheduledOn *time.Time
	}{
		{"draft-newest", "draft", 1, nil},
		{"pending", "pending_approval", 2, nil},
		{"approved-scheduled", "approved", 3, ptrTime(base.Add(-time.Hour))},
		{"published", "published", 4, nil},
		{"draft-older", "draft", 5, nil},
		{"approved-plain", "approved", 6, nil},
	}
	for _, s := range seed {
		created := base.Add(-time.Duration(s.ageMinutes) * time.Minute)
		if err := pool.QueryRow(ctx, `
			INSERT INTO announcement_requests (kind, state, subject, created_by, created_on, updated_on, scheduled_on)
			VALUES ('customer', $1, $2, $3, $4, $4, $5) RETURNING id::text`,
			s.state, s.name, creator, created, s.scheduledOn).Scan(new(string)); err != nil {
			t.Fatalf("seed %s: %v", s.name, err)
		}
	}

	search := func(t *testing.T, req domain.SearchAnnouncementRequestsRequest) ([]string, int) {
		t.Helper()
		req.CreatedBy = &creator
		if req.Pagination.Limit == 0 {
			req.Pagination.Limit = 50
		}
		rows, total, err := repo.Search(ctx, req)
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		subjects := make([]string, len(rows))
		for i, r := range rows {
			subjects[i] = r.Subject
		}
		return subjects, total
	}
	assertSubjects := func(t *testing.T, got []string, want ...string) {
		t.Helper()
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("subjects = %v, want %v", got, want)
		}
	}

	t.Run("states merges several states into one newest-first list", func(t *testing.T) {
		got, total := search(t, domain.SearchAnnouncementRequestsRequest{
			States: []domain.AnnouncementRequestState{domain.AnnouncementRequestStateDraft, domain.AnnouncementRequestStateApproved},
		})
		assertSubjects(t, got, "draft-newest", "approved-scheduled", "draft-older", "approved-plain")
		if total != 4 {
			t.Fatalf("total = %d, want 4", total)
		}
	})

	t.Run("states covering every state matches every row", func(t *testing.T) {
		got, total := search(t, domain.SearchAnnouncementRequestsRequest{
			States: []domain.AnnouncementRequestState{
				domain.AnnouncementRequestStateDraft,
				domain.AnnouncementRequestStatePendingApproval,
				domain.AnnouncementRequestStateApproved,
				domain.AnnouncementRequestStatePublished,
			},
		})
		if len(got) != len(seed) || total != len(seed) {
			t.Fatalf("got %d rows / total %d, want %d", len(got), total, len(seed))
		}
	})

	t.Run("a one-element states list equals the single state filter", func(t *testing.T) {
		pending := domain.AnnouncementRequestStatePendingApproval
		viaState, stateTotal := search(t, domain.SearchAnnouncementRequestsRequest{State: &pending})
		viaStates, statesTotal := search(t, domain.SearchAnnouncementRequestsRequest{
			States: []domain.AnnouncementRequestState{pending},
		})
		assertSubjects(t, viaStates, viaState...)
		if stateTotal != statesTotal || statesTotal != 1 {
			t.Fatalf("totals state=%d states=%d, want both 1", stateTotal, statesTotal)
		}
	})

	t.Run("duplicate entries in states do not duplicate rows", func(t *testing.T) {
		got, total := search(t, domain.SearchAnnouncementRequestsRequest{
			States: []domain.AnnouncementRequestState{domain.AnnouncementRequestStatePublished, domain.AnnouncementRequestStatePublished},
		})
		assertSubjects(t, got, "published")
		if total != 1 {
			t.Fatalf("total = %d, want 1", total)
		}
	})

	t.Run("nil and empty states mean no state filter, not match-nothing", func(t *testing.T) {
		for name, states := range map[string][]domain.AnnouncementRequestState{
			"nil":   nil,
			"empty": {},
		} {
			got, total := search(t, domain.SearchAnnouncementRequestsRequest{States: states})
			if len(got) != len(seed) || total != len(seed) {
				t.Fatalf("%s states: got %d rows / total %d, want %d", name, len(got), total, len(seed))
			}
		}
	})

	t.Run("pagination slices the merged list and total stays the merged total", func(t *testing.T) {
		states := []domain.AnnouncementRequestState{domain.AnnouncementRequestStateDraft, domain.AnnouncementRequestStateApproved}
		first, total := search(t, domain.SearchAnnouncementRequestsRequest{
			States: states, Pagination: domain.Pagination{Limit: 3, Offset: 0},
		})
		assertSubjects(t, first, "draft-newest", "approved-scheduled", "draft-older")
		if total != 4 {
			t.Fatalf("first page total = %d, want 4", total)
		}
		second, total := search(t, domain.SearchAnnouncementRequestsRequest{
			States: states, Pagination: domain.Pagination{Limit: 3, Offset: 3},
		})
		assertSubjects(t, second, "approved-plain")
		if total != 4 {
			t.Fatalf("second page total = %d, want 4", total)
		}
	})

	t.Run("states with no matching row returns an empty list", func(t *testing.T) {
		if _, err := pool.Exec(ctx, `DELETE FROM announcement_requests WHERE created_by = $1 AND state = 'published'`, creator); err != nil {
			t.Fatalf("delete: %v", err)
		}
		got, total := search(t, domain.SearchAnnouncementRequestsRequest{
			States: []domain.AnnouncementRequestState{domain.AnnouncementRequestStatePublished},
		})
		if len(got) != 0 || total != 0 {
			t.Fatalf("got %v / total %d, want none", got, total)
		}
	})

	t.Run("the single state filter still works unchanged", func(t *testing.T) {
		draft := domain.AnnouncementRequestStateDraft
		got, total := search(t, domain.SearchAnnouncementRequestsRequest{State: &draft})
		assertSubjects(t, got, "draft-newest", "draft-older")
		if total != 2 {
			t.Fatalf("total = %d, want 2", total)
		}
	})

	t.Run("readyForScheduledPublish still finds only the due approved row", func(t *testing.T) {
		got, total := search(t, domain.SearchAnnouncementRequestsRequest{ReadyForScheduledPublish: true})
		assertSubjects(t, got, "approved-scheduled")
		if total != 1 {
			t.Fatalf("total = %d, want 1", total)
		}
	})
}

func ptrTime(t time.Time) *time.Time { return &t }
