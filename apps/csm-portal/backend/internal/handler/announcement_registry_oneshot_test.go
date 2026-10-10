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

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// oneShotRegistryClient is the registry client plus the optional one-call
// read. The plain mock in announcement_registry_test.go does not implement it,
// so every older test keeps exercising the paged path unchanged.
type oneShotRegistryClient struct {
	mockEntityAnnouncementRegistryClient
	oneShotFn    func(ctx context.Context, body []byte) ([]byte, error)
	oneShotCalls atomic.Int32
	pagedCalls   atomic.Int32
}

func (m *oneShotRegistryClient) SearchAnnouncementRegistryCases(ctx context.Context, body []byte) ([]byte, error) {
	m.oneShotCalls.Add(1)
	return m.oneShotFn(ctx, body)
}

func (m *oneShotRegistryClient) SearchCases(ctx context.Context, body []byte) ([]byte, error) {
	m.pagedCalls.Add(1)
	return m.mockEntityAnnouncementRegistryClient.SearchCases(ctx, body)
}

// registryTestCases returns n cases newest-updated first, as entity-service does.
func registryTestCases(n int) []map[string]any {
	base := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	out := make([]map[string]any, n)
	for i := range out {
		out[i] = map[string]any{
			"id": fmt.Sprintf("case-%03d", i), "number": fmt.Sprintf("CS%03d", i), "internalId": fmt.Sprintf("INT%03d", i),
			"subject": fmt.Sprintf("Announcement %03d", i), "state": "open",
			"createdOn": base.Add(-48 * time.Hour).Format(time.RFC3339),
			"updatedOn": base.Add(-time.Duration(i) * time.Minute).Format(time.RFC3339),
			"createdBy": map[string]any{"name": "", "email": "staff@test.local"},
			"project":   map[string]any{"id": "proj-1", "name": "Project One"},
		}
	}
	return out
}

// pagedFromAll serves all as entity-service's /cases/search would: honouring
// pagination.offset/limit and reporting the real total.
func pagedFromAll(all []map[string]any) func(context.Context, []byte) ([]byte, error) {
	return func(_ context.Context, body []byte) ([]byte, error) {
		var req struct {
			Pagination struct{ Offset, Limit int } `json:"pagination"`
		}
		_ = json.Unmarshal(body, &req)
		lo := req.Pagination.Offset
		if lo > len(all) {
			lo = len(all)
		}
		hi := lo + req.Pagination.Limit
		if hi > len(all) {
			hi = len(all)
		}
		return json.Marshal(map[string]any{"cases": all[lo:hi], "total": len(all), "offset": lo, "limit": req.Pagination.Limit})
	}
}

func oneShotFromAll(all []map[string]any) func(context.Context, []byte) ([]byte, error) {
	return func(context.Context, []byte) ([]byte, error) {
		return json.Marshal(map[string]any{"cases": all, "total": len(all), "offset": 0, "limit": len(all)})
	}
}

func runRegistry(t *testing.T, h *AnnouncementRegistryHandler, body string) (int, []byte) {
	t.Helper()
	r := withUser(httptest.NewRequest(http.MethodPost, "/announcements/registry/search", strings.NewReader(body)))
	w := httptest.NewRecorder()
	h.SearchAnnouncementRegistry(w, r)
	return w.Code, w.Body.Bytes()
}

// With the one-shot read available, the registry is built from it and the
// paged /cases/search loop is never touched.
func TestSearchAnnouncementRegistry_OneShotReadSkipsThePagedLoop(t *testing.T) {
	all := registryTestCases(120)
	client := &oneShotRegistryClient{oneShotFn: oneShotFromAll(all)}
	client.searchCasesFn = func(context.Context, []byte) ([]byte, error) {
		t.Error("paged /cases/search was called although the one-shot read succeeded")
		return nil, errors.New("unexpected")
	}
	h := NewAnnouncementRegistryHandler(client)

	code, body := runRegistry(t, h, `{"pagination":{"offset":0,"limit":20}}`)
	if code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", code, body)
	}
	var got registrySearchResponse
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 20 || got.Rows[0].CaseID != "case-000" || got.Total != 120 || !got.HasMore {
		t.Fatalf("unexpected page: rows=%d first=%q total=%d hasMore=%v", len(got.Rows), got.Rows[0].CaseID, got.Total, got.HasMore)
	}
	if n := client.oneShotCalls.Load(); n != 1 {
		t.Fatalf("one-shot read called %d times, want 1", n)
	}
}

// The one-shot request carries the same filters the paged loop sent (type,
// states, projects, free text) and no pagination.
func TestSearchAnnouncementRegistry_OneShotRequestCarriesTheFilters(t *testing.T) {
	var sent []byte
	client := &oneShotRegistryClient{oneShotFn: func(_ context.Context, body []byte) ([]byte, error) {
		sent = body
		return []byte(`{"cases":[],"total":0}`), nil
	}}
	h := NewAnnouncementRegistryHandler(client)
	code, body := runRegistry(t, h, `{"states":["open"],"projectIds":["proj-1"],"search":"eol","pagination":{"offset":0,"limit":20}}`)
	if code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", code, body)
	}
	var req struct {
		Pagination *json.RawMessage `json:"pagination"`
		Filters    struct {
			SearchQuery string `json:"searchQuery"`
			Filters     []struct {
				Field  string   `json:"field"`
				Values []string `json:"values"`
			} `json:"filters"`
		} `json:"filters"`
	}
	if err := json.Unmarshal(sent, &req); err != nil {
		t.Fatalf("decode sent request: %v", err)
	}
	if req.Pagination != nil {
		t.Fatalf("one-shot request must not carry pagination, got %s", *req.Pagination)
	}
	if req.Filters.SearchQuery != "eol" {
		t.Fatalf("searchQuery = %q, want eol", req.Filters.SearchQuery)
	}
	byField := map[string][]string{}
	for _, f := range req.Filters.Filters {
		byField[f.Field] = f.Values
	}
	if len(byField["type"]) != 1 || byField["type"][0] != "announcement" ||
		len(byField["state"]) != 1 || byField["state"][0] != "open" ||
		len(byField["projectId"]) != 1 || byField["projectId"][0] != "proj-1" {
		t.Fatalf("filters not carried over: %+v", byField)
	}
}

// The registry the user sees is byte-for-byte the same whether it was built
// from the one-shot read or from the paged loop.
func TestSearchAnnouncementRegistry_OneShotAndPagedProduceTheSameRegistry(t *testing.T) {
	all := registryTestCases(120) // three pages of 50 on the paged path
	bodies := []string{
		`{"pagination":{"offset":0,"limit":20}}`,
		`{"pagination":{"offset":100,"limit":50}}`,
		`{"pagination":{"offset":119,"limit":20}}`,
		`{"pagination":{"offset":500,"limit":20}}`,
		`{"search":"x","pagination":{"offset":10,"limit":5}}`,
	}
	for _, reqBody := range bodies {
		oneShot := &oneShotRegistryClient{oneShotFn: oneShotFromAll(all)}
		paged := &mockEntityAnnouncementRegistryClient{searchCasesFn: pagedFromAll(all)}

		codeA, a := runRegistry(t, NewAnnouncementRegistryHandler(oneShot), reqBody)
		codeB, b := runRegistry(t, NewAnnouncementRegistryHandler(paged), reqBody)
		if codeA != http.StatusOK || codeB != http.StatusOK {
			t.Fatalf("%s: statuses %d / %d", reqBody, codeA, codeB)
		}
		if string(a) != string(b) {
			t.Fatalf("%s: one-shot and paged registries differ:\none-shot: %s\npaged:    %s", reqBody, a, b)
		}
	}
}

// Whatever goes wrong with the one-shot read, the registry still loads, from
// the paged loop, exactly as before the fast path existed.
func TestSearchAnnouncementRegistry_FallsBackToPagedSearch(t *testing.T) {
	all := registryTestCases(60)
	for name, oneShot := range map[string]func(context.Context, []byte) ([]byte, error){
		"entity service predates the route (404)": func(context.Context, []byte) ([]byte, error) {
			return nil, errors.New("upstream returned 404: not found")
		},
		"upstream error": func(context.Context, []byte) ([]byte, error) {
			return nil, errors.New("upstream returned 500")
		},
		"undecodable body": func(context.Context, []byte) ([]byte, error) { return []byte(`not json`), nil },
		"inconsistent body": func(context.Context, []byte) ([]byte, error) {
			return []byte(`{"cases":[{"id":"x"}],"total":9}`), nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			client := &oneShotRegistryClient{oneShotFn: oneShot}
			client.searchCasesFn = pagedFromAll(all)
			code, body := runRegistry(t, NewAnnouncementRegistryHandler(client), `{"pagination":{"offset":0,"limit":20}}`)
			if code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", code, body)
			}
			var got registrySearchResponse
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatal(err)
			}
			if got.Total != 60 || len(got.Rows) != 20 || got.Rows[0].CaseID != "case-000" {
				t.Fatalf("fallback result wrong: total=%d rows=%d", got.Total, len(got.Rows))
			}
			if client.oneShotCalls.Load() != 1 || client.pagedCalls.Load() == 0 {
				t.Fatalf("expected one one-shot attempt then paged calls; one-shot=%d paged=%d", client.oneShotCalls.Load(), client.pagedCalls.Load())
			}
		})
	}
}

// A cancelled request is not retried through the slow path.
func TestSearchAnnouncementRegistry_CancelledRequestIsNotRetried(t *testing.T) {
	client := &oneShotRegistryClient{oneShotFn: func(ctx context.Context, _ []byte) ([]byte, error) { return nil, ctx.Err() }}
	client.searchCasesFn = func(context.Context, []byte) ([]byte, error) {
		t.Error("paged search ran for a cancelled request")
		return nil, errors.New("unexpected")
	}
	r := withUser(httptest.NewRequest(http.MethodPost, "/announcements/registry/search", strings.NewReader(`{"pagination":{"offset":0,"limit":20}}`)))
	ctx, cancel := context.WithCancel(r.Context())
	cancel()
	w := httptest.NewRecorder()
	NewAnnouncementRegistryHandler(client).SearchAnnouncementRegistry(w, r.WithContext(ctx))
	if w.Code == http.StatusOK {
		t.Fatalf("a cancelled request returned 200")
	}
	if client.pagedCalls.Load() != 0 {
		t.Fatalf("paged search was called %d times for a cancelled request", client.pagedCalls.Load())
	}
}
