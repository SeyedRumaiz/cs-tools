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

package handler

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
)

// mockEntityReportsClient implements entityReportsClient. searchCasesPages
// and searchTimeCardsPages let a test return a different response for each
// successive page (keyed by call order), so pagination itself can be
// exercised rather than just a single-page happy path.
type mockEntityReportsClient struct {
	searchProjectsFn func(ctx context.Context, body []byte) ([]byte, error)
	getProjectFn     func(ctx context.Context, id string) ([]byte, error)

	searchCasesPages     [][]byte
	searchCasesCallCount int

	searchTimeCardsPagesByCase map[string][][]byte
	searchTimeCardsCallCount   map[string]int
}

func (m *mockEntityReportsClient) SearchProjects(ctx context.Context, body []byte) ([]byte, error) {
	return m.searchProjectsFn(ctx, body)
}

func (m *mockEntityReportsClient) GetProject(ctx context.Context, id string) ([]byte, error) {
	return m.getProjectFn(ctx, id)
}

func (m *mockEntityReportsClient) SearchCases(ctx context.Context, body []byte) ([]byte, error) {
	page := m.searchCasesPages[m.searchCasesCallCount]
	m.searchCasesCallCount++
	return page, nil
}

func (m *mockEntityReportsClient) SearchTimeCards(ctx context.Context, body []byte) ([]byte, error) {
	var req entitySearchTimeCardsRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}
	if m.searchTimeCardsCallCount == nil {
		m.searchTimeCardsCallCount = map[string]int{}
	}
	call := m.searchTimeCardsCallCount[req.Filters.CaseID]
	m.searchTimeCardsCallCount[req.Filters.CaseID] = call + 1
	return m.searchTimeCardsPagesByCase[req.Filters.CaseID][call], nil
}

func jsonBytesReports(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// TestGetTimeLogBreakdown_PagesThroughAllCasesAndTimeCards verifies the
// fix for the finding that this report silently stopped at 50 cases and 50
// time cards per case: a project with more than entitySearchPageLimit cases,
// and a case with more than entitySearchPageLimit time cards, must both be
// fully paged through rather than truncated at the first page.
func TestGetTimeLogBreakdown_PagesThroughAllCasesAndTimeCards(t *testing.T) {
	entity := &mockEntityReportsClient{
		searchProjectsFn: func(context.Context, []byte) ([]byte, error) {
			return jsonBytesReports(t, entitySearchProjectsResponse{
				Total:    1,
				Projects: []entityProjectView{{ID: "proj-uuid-1", Key: "PRJ001"}},
			}), nil
		},
		getProjectFn: func(context.Context, string) ([]byte, error) {
			return jsonBytesReports(t, entityProjectDetailsView{
				ID: "proj-uuid-1", Name: "Acme Project", Key: "PRJ001", SubscriptionType: "Standard",
			}), nil
		},
		// entitySearchPageLimit (50) + 1 = 51 cases total, split across two pages.
		searchCasesPages: [][]byte{
			jsonBytesReports(t, entitySearchCasesResponse{
				Total: 51,
				Cases: makeCaseViews(0, 50, "case"),
			}),
			jsonBytesReports(t, entitySearchCasesResponse{
				Total: 51,
				Cases: makeCaseViews(50, 1, "case"),
			}),
		},
		searchTimeCardsPagesByCase: map[string][][]byte{},
	}
	// Every case's own time-card search: also split across two pages
	// (60 total) to exercise the same pagination path per case.
	for i := 0; i < 51; i++ {
		caseID := caseIDFor(i, "case")
		entity.searchTimeCardsPagesByCase[caseID] = [][]byte{
			jsonBytesReports(t, entitySearchTimeCardsResponse{
				Total:     60,
				TimeCards: makeTimeCardViews(50, 1.0),
			}),
			jsonBytesReports(t, entitySearchTimeCardsResponse{
				Total:     60,
				TimeCards: makeTimeCardViews(10, 1.0),
			}),
		}
	}

	c := NewPostgresReportsClient(entity, nil)
	got, err := c.GetTimeLogBreakdown(context.Background(), "PRJ001")
	if err != nil {
		t.Fatalf("GetTimeLogBreakdown: %v", err)
	}
	if len(got.Cases) != 51 {
		t.Fatalf("len(Cases) = %d, want 51 (all cases across both pages, not truncated at 50)", len(got.Cases))
	}
	for _, cr := range got.Cases {
		if len(cr.TimeCards) != 60 {
			t.Errorf("case %s: len(TimeCards) = %d, want 60 (all time cards across both pages)", cr.CaseNumber, len(cr.TimeCards))
		}
	}
}

func caseIDFor(i int, prefix string) string {
	return prefix + "-uuid-" + strconv.Itoa(i)
}

// makeCaseViews builds n views with globally-unique ids/numbers starting at
// start, so a second page picks up right where a first page of size start left off.
func makeCaseViews(start, n int, prefix string) []entitySearchCaseView {
	views := make([]entitySearchCaseView, n)
	for i := 0; i < n; i++ {
		idx := start + i
		views[i] = entitySearchCaseView{ID: caseIDFor(idx, prefix), Number: prefix + strconv.Itoa(idx), InternalID: "WSO2-" + strconv.Itoa(idx)}
	}
	return views
}

func makeTimeCardViews(n int, hours float64) []entityTimeCardView {
	views := make([]entityTimeCardView, n)
	for i := 0; i < n; i++ {
		views[i] = entityTimeCardView{ID: "tc-" + strconv.Itoa(i), TotalTime: hours, WorkDate: "2026-01-01"}
	}
	return views
}
