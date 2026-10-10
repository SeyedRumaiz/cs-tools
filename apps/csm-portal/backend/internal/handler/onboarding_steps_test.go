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
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/entity"
)

func TestGetProjectOnboardingSteps(t *testing.T) {
	const projectID = "11111111-1111-1111-1111-111111111111"
	now := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	str := func(s string) *string { return &s }

	newRequest := func(id string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/projects/"+id+"/onboarding-steps", nil)
		r.SetPathValue("id", id)
		return r
	}

	t.Run("requires authenticated user", func(t *testing.T) {
		h := NewOnboardingStepHandler(&mockEntityOnboardingStepClient{})
		w := httptest.NewRecorder()
		h.GetProjectOnboardingSteps(w, newRequest(projectID))
		assertStatus(t, w, http.StatusUnauthorized)
		assertErrorMessage(t, w, ErrMsgUnauthorized)
		assertContentType(t, w, "application/json")
	})

	t.Run("rejects empty project ID", func(t *testing.T) {
		h := NewOnboardingStepHandler(&mockEntityOnboardingStepClient{})
		r := withUser(httptest.NewRequest(http.MethodGet, "/projects//onboarding-steps", nil))
		w := httptest.NewRecorder()
		h.GetProjectOnboardingSteps(w, r)
		assertStatus(t, w, http.StatusBadRequest)
		assertErrorMessage(t, w, ErrMsgInvalidUUID)
		assertContentType(t, w, "application/json")
	})

	t.Run("rejects non-UUID project ID without calling upstream", func(t *testing.T) {
		called := false
		h := NewOnboardingStepHandler(&mockEntityOnboardingStepClient{
			searchOnboardingStepsFn: func(context.Context, entity.OnboardingStepSearchRequest) (entity.OnboardingStepSearchResponse, error) {
				called = true
				return entity.OnboardingStepSearchResponse{}, nil
			},
		})
		w := httptest.NewRecorder()
		h.GetProjectOnboardingSteps(w, withUser(newRequest("proj-42")))
		assertStatus(t, w, http.StatusBadRequest)
		assertErrorMessage(t, w, ErrMsgInvalidUUID)
		if called {
			t.Error("upstream must not be called for an invalid project id")
		}
	})

	t.Run("returns an empty list, not 404, for a project with no recorded steps", func(t *testing.T) {
		var captured entity.OnboardingStepSearchRequest
		h := NewOnboardingStepHandler(&mockEntityOnboardingStepClient{
			searchOnboardingStepsFn: func(_ context.Context, req entity.OnboardingStepSearchRequest) (entity.OnboardingStepSearchResponse, error) {
				captured = req
				return entity.OnboardingStepSearchResponse{Steps: []entity.OnboardingStep{}, Total: 0, Limit: 50}, nil
			},
		})
		w := httptest.NewRecorder()
		h.GetProjectOnboardingSteps(w, withUser(newRequest(projectID)))
		assertStatus(t, w, http.StatusOK)
		assertContentType(t, w, "application/json")

		if captured.Filters.ProjectID == nil || *captured.Filters.ProjectID != projectID {
			t.Errorf("filters.projectId = %v, want %q", captured.Filters.ProjectID, projectID)
		}
		if captured.Pagination.Limit != entity.OnboardingStepSearchMaxLimit || captured.Pagination.Offset != 0 {
			t.Errorf("pagination = %+v, want limit %d offset 0", captured.Pagination, entity.OnboardingStepSearchMaxLimit)
		}

		raw := w.Body.String()
		if !strings.Contains(raw, `"memberships":[]`) {
			t.Errorf("memberships must serialise as an empty array, got %s", raw)
		}
		resp := decodeJSON[ProjectOnboardingStepsResponse](t, w)
		if resp.Total != 0 || len(resp.Memberships) != 0 || resp.Truncated {
			t.Errorf("resp = %+v, want empty, total 0, not truncated", resp)
		}
	})

	t.Run("pages through the ledger and groups rows per membership in step order", func(t *testing.T) {
		// Two memberships spread across two upstream pages (newest first, as
		// the ledger returns them). The second page's rows belong to the first
		// membership too, so grouping has to merge across page boundaries.
		page0 := []entity.OnboardingStep{
			{ID: "s1", MembershipSfID: "a0X2", Email: "zed@example.com", Step: "IDENTITY", Status: "SUCCEEDED", AttemptCount: 1, EventType: "CREATED", EventModifiedOn: now, UpdatedOn: now},
			{ID: "s2", MembershipSfID: "a0X1", ContactSfID: str("003A"), Email: "amy@example.com", Step: "EMAIL", Status: "FAILED", AttemptCount: 2, LastError: str("smtp: 550 mailbox unavailable"), EventType: "CREATED", EventModifiedOn: now, UpdatedOn: now},
		}
		page1 := []entity.OnboardingStep{
			{ID: "s3", MembershipSfID: "a0X1", Email: "amy@example.com", ProjectContactID: str("pc-1"), Step: "DATABASE", Status: "SUCCEEDED", AttemptCount: 1, EventType: "CREATED", EventModifiedOn: now, UpdatedOn: now},
			{ID: "s4", MembershipSfID: "a0X1", Email: "amy@example.com", Step: "IDENTITY", Status: "SUCCEEDED", AttemptCount: 1, EventType: "CREATED", EventModifiedOn: now, UpdatedOn: now},
		}
		var offsets []int
		h := NewOnboardingStepHandler(&mockEntityOnboardingStepClient{
			searchOnboardingStepsFn: func(_ context.Context, req entity.OnboardingStepSearchRequest) (entity.OnboardingStepSearchResponse, error) {
				offsets = append(offsets, req.Pagination.Offset)
				switch req.Pagination.Offset {
				case 0:
					return entity.OnboardingStepSearchResponse{Steps: page0, Total: 4, Limit: 50, Offset: 0}, nil
				case 2:
					return entity.OnboardingStepSearchResponse{Steps: page1, Total: 4, Limit: 50, Offset: 2}, nil
				default:
					return entity.OnboardingStepSearchResponse{}, fmt.Errorf("unexpected offset %d", req.Pagination.Offset)
				}
			},
		})
		w := httptest.NewRecorder()
		h.GetProjectOnboardingSteps(w, withUser(newRequest(projectID)))
		assertStatus(t, w, http.StatusOK)

		if len(offsets) != 2 || offsets[0] != 0 || offsets[1] != 2 {
			t.Errorf("upstream offsets = %v, want [0 2]", offsets)
		}

		resp := decodeJSON[ProjectOnboardingStepsResponse](t, w)
		if resp.Total != 2 || len(resp.Memberships) != 2 || resp.Truncated {
			t.Fatalf("resp = %+v, want two memberships, not truncated", resp)
		}

		amy := resp.Memberships[0]
		if amy.Email != "amy@example.com" || amy.MembershipSfID != "a0X1" {
			t.Fatalf("memberships are not sorted by email: first = %+v", amy)
		}
		if amy.ContactSfID == nil || *amy.ContactSfID != "003A" {
			t.Errorf("contactSfId = %v, want 003A taken from the row that had it", amy.ContactSfID)
		}
		if amy.ProjectContactID == nil || *amy.ProjectContactID != "pc-1" {
			t.Errorf("projectContactId = %v, want pc-1 back-filled from the DATABASE row", amy.ProjectContactID)
		}
		gotOrder := make([]string, 0, len(amy.Steps))
		for _, s := range amy.Steps {
			gotOrder = append(gotOrder, s.Step)
		}
		if fmt.Sprint(gotOrder) != "[IDENTITY DATABASE EMAIL]" {
			t.Errorf("step order = %v, want [IDENTITY DATABASE EMAIL]", gotOrder)
		}
		email := amy.Steps[2]
		if email.Status != "FAILED" || email.AttemptCount != 2 || email.LastError == nil || *email.LastError != "smtp: 550 mailbox unavailable" {
			t.Errorf("EMAIL step = %+v, want FAILED with attemptCount 2 and the upstream lastError", email)
		}

		zed := resp.Memberships[1]
		if zed.Email != "zed@example.com" || len(zed.Steps) != 1 || zed.Steps[0].Step != "IDENTITY" {
			t.Errorf("second membership = %+v, want zed's single IDENTITY step", zed)
		}
	})

	t.Run("stops at the page cap and flags the response as truncated", func(t *testing.T) {
		calls := 0
		h := NewOnboardingStepHandler(&mockEntityOnboardingStepClient{
			searchOnboardingStepsFn: func(_ context.Context, req entity.OnboardingStepSearchRequest) (entity.OnboardingStepSearchResponse, error) {
				calls++
				// A misbehaving upstream: always one more row, total never reached.
				return entity.OnboardingStepSearchResponse{
					Steps: []entity.OnboardingStep{{ID: fmt.Sprint(calls), MembershipSfID: fmt.Sprintf("m%d", calls), Email: "x@example.com", Step: "IDENTITY", Status: "SUCCEEDED"}},
					Total: 1_000_000, Limit: 50, Offset: req.Pagination.Offset,
				}, nil
			},
		})
		w := httptest.NewRecorder()
		h.GetProjectOnboardingSteps(w, withUser(newRequest(projectID)))
		assertStatus(t, w, http.StatusOK)
		if calls != maxOnboardingStepPages {
			t.Errorf("upstream calls = %d, want exactly the page cap %d", calls, maxOnboardingStepPages)
		}
		resp := decodeJSON[ProjectOnboardingStepsResponse](t, w)
		if !resp.Truncated {
			t.Error("truncated = false, want true when the page cap is hit")
		}
		if resp.Total != maxOnboardingStepPages {
			t.Errorf("total = %d, want %d memberships", resp.Total, maxOnboardingStepPages)
		}
	})

	t.Run("drops a row a concurrent ledger write repeated across pages", func(t *testing.T) {
		// The upstream orders by updated_on DESC, so a retry between the two
		// page fetches moves that row to the front and pushes a row page 0
		// already returned into page 1. amy's IDENTITY row arrives twice.
		repeated := entity.OnboardingStep{ID: "s1", MembershipSfID: "a0X1", Email: "amy@example.com", Step: "IDENTITY", Status: "SUCCEEDED", AttemptCount: 1, EventType: "CREATED", EventModifiedOn: now, UpdatedOn: now}
		page0 := []entity.OnboardingStep{
			repeated,
			{ID: "s2", MembershipSfID: "a0X1", Email: "amy@example.com", Step: "DATABASE", Status: "SUCCEEDED", AttemptCount: 1, EventType: "CREATED", EventModifiedOn: now, UpdatedOn: now},
		}
		page1 := []entity.OnboardingStep{
			repeated,
			{ID: "s3", MembershipSfID: "a0X1", Email: "amy@example.com", Step: "EMAIL", Status: "SUCCEEDED", AttemptCount: 1, EventType: "CREATED", EventModifiedOn: now, UpdatedOn: now},
		}
		h := NewOnboardingStepHandler(&mockEntityOnboardingStepClient{
			searchOnboardingStepsFn: func(_ context.Context, req entity.OnboardingStepSearchRequest) (entity.OnboardingStepSearchResponse, error) {
				switch req.Pagination.Offset {
				case 0:
					return entity.OnboardingStepSearchResponse{Steps: page0, Total: 4, Limit: 50, Offset: 0}, nil
				case 2:
					return entity.OnboardingStepSearchResponse{Steps: page1, Total: 4, Limit: 50, Offset: 2}, nil
				default:
					return entity.OnboardingStepSearchResponse{}, fmt.Errorf("unexpected offset %d", req.Pagination.Offset)
				}
			},
		})
		w := httptest.NewRecorder()
		h.GetProjectOnboardingSteps(w, withUser(newRequest(projectID)))
		assertStatus(t, w, http.StatusOK)

		resp := decodeJSON[ProjectOnboardingStepsResponse](t, w)
		if resp.Total != 1 || len(resp.Memberships) != 1 {
			t.Fatalf("resp = %+v, want one membership", resp)
		}
		gotOrder := make([]string, 0, len(resp.Memberships[0].Steps))
		for _, s := range resp.Memberships[0].Steps {
			gotOrder = append(gotOrder, s.Step)
		}
		if fmt.Sprint(gotOrder) != "[IDENTITY DATABASE EMAIL]" {
			t.Errorf("step order = %v, want each step once in flow order", gotOrder)
		}
	})

	t.Run("lower-cases the email the response documents as lower-cased", func(t *testing.T) {
		h := NewOnboardingStepHandler(&mockEntityOnboardingStepClient{
			searchOnboardingStepsFn: func(context.Context, entity.OnboardingStepSearchRequest) (entity.OnboardingStepSearchResponse, error) {
				return entity.OnboardingStepSearchResponse{
					Steps: []entity.OnboardingStep{
						// Upstream normalises on write today, so only a row
						// written before that, or by another writer, looks
						// like this — the documented shape must hold anyway.
						{ID: "s1", MembershipSfID: "a0X2", Email: "  Zed@Example.COM ", Step: "IDENTITY", Status: "SUCCEEDED", EventType: "CREATED", EventModifiedOn: now, UpdatedOn: now},
						{ID: "s2", MembershipSfID: "a0X1", Email: "AMY@example.com", Step: "IDENTITY", Status: "SUCCEEDED", EventType: "CREATED", EventModifiedOn: now, UpdatedOn: now},
					},
					Total: 2, Limit: 50, Offset: 0,
				}, nil
			},
		})
		w := httptest.NewRecorder()
		h.GetProjectOnboardingSteps(w, withUser(newRequest(projectID)))
		assertStatus(t, w, http.StatusOK)

		resp := decodeJSON[ProjectOnboardingStepsResponse](t, w)
		if len(resp.Memberships) != 2 {
			t.Fatalf("memberships = %+v, want two", resp.Memberships)
		}
		// Sorted on the normalised value, so amy precedes zed despite the
		// raw "AMY@…" sorting before "  Zed@…" only by accident of case.
		if resp.Memberships[0].Email != "amy@example.com" {
			t.Errorf("first email = %q, want %q", resp.Memberships[0].Email, "amy@example.com")
		}
		if resp.Memberships[1].Email != "zed@example.com" {
			t.Errorf("second email = %q, want %q", resp.Memberships[1].Email, "zed@example.com")
		}
	})

	t.Run("upstream errors are mapped correctly", func(t *testing.T) {
		for _, tc := range upstreamErrorsGeneric("Failed to load onboarding status.") {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				h := NewOnboardingStepHandler(&mockEntityOnboardingStepClient{
					searchOnboardingStepsFn: func(context.Context, entity.OnboardingStepSearchRequest) (entity.OnboardingStepSearchResponse, error) {
						return entity.OnboardingStepSearchResponse{}, tc.err
					},
				})
				w := httptest.NewRecorder()
				h.GetProjectOnboardingSteps(w, withUser(newRequest(projectID)))
				assertStatus(t, w, tc.wantCode)
				assertErrorMessage(t, w, tc.wantMsg)
				assertContentType(t, w, "application/json")
			})
		}
	})
}
