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
	"net/http"
	"net/http/httptest"
	"testing"
)

type mockEntityTeamsClient struct {
	fn func(ctx context.Context, teamID string) ([]byte, error)
}

func (m *mockEntityTeamsClient) GetTeamMembers(ctx context.Context, teamID string) ([]byte, error) {
	return m.fn(ctx, teamID)
}

const testTeamUUID = "11111111-1111-1111-1111-111111111111"

func TestGetTeamMembers(t *testing.T) {
	t.Run("requires authenticated user", func(t *testing.T) {
		h := NewTeamHandler(&mockEntityTeamsClient{})
		r := httptest.NewRequest(http.MethodGet, "/teams/"+testTeamUUID+"/members", nil)
		r.SetPathValue("id", testTeamUUID)
		w := httptest.NewRecorder()
		h.GetTeamMembers(w, r)
		assertStatus(t, w, http.StatusUnauthorized)
	})

	t.Run("rejects a non-UUID id", func(t *testing.T) {
		h := NewTeamHandler(&mockEntityTeamsClient{})
		r := withUser(httptest.NewRequest(http.MethodGet, "/teams/not-a-uuid/members", nil))
		r.SetPathValue("id", "not-a-uuid")
		w := httptest.NewRecorder()
		h.GetTeamMembers(w, r)
		assertStatus(t, w, http.StatusBadRequest)
	})

	t.Run("returns 404 when no members found", func(t *testing.T) {
		entity := &mockEntityTeamsClient{
			fn: func(ctx context.Context, teamID string) ([]byte, error) {
				return []byte(`{"members":[]}`), nil
			},
		}
		h := NewTeamHandler(entity)
		r := withUser(httptest.NewRequest(http.MethodGet, "/teams/"+testTeamUUID+"/members", nil))
		r.SetPathValue("id", testTeamUUID)
		w := httptest.NewRecorder()
		h.GetTeamMembers(w, r)
		assertStatus(t, w, http.StatusNotFound)
	})

	t.Run("returns member name, email, and role from entity-service", func(t *testing.T) {
		entity := &mockEntityTeamsClient{
			fn: func(ctx context.Context, teamID string) ([]byte, error) {
				if teamID != testTeamUUID {
					t.Errorf("teamID = %q, want %q", teamID, testTeamUUID)
				}
				return []byte(`{"members":[{"id":"m1","name":"Jane Doe","email":"jane@example.com","role":"lead"}]}`), nil
			},
		}
		h := NewTeamHandler(entity)
		r := withUser(httptest.NewRequest(http.MethodGet, "/teams/"+testTeamUUID+"/members", nil))
		r.SetPathValue("id", testTeamUUID)
		w := httptest.NewRecorder()
		h.GetTeamMembers(w, r)
		assertStatus(t, w, http.StatusOK)
		views := decodeJSON[[]TeamMemberView](t, w)
		if len(views) != 1 {
			t.Fatalf("got %d members, want 1", len(views))
		}
		v := views[0]
		if v.Name != "Jane Doe" || v.Email != "jane@example.com" || v.Role != "lead" {
			t.Errorf("view = %+v, unexpected", v)
		}
	})

	t.Run("a member with no role still comes back with a blank role rather than an error", func(t *testing.T) {
		entity := &mockEntityTeamsClient{
			fn: func(ctx context.Context, teamID string) ([]byte, error) {
				return []byte(`{"members":[{"id":"m1","name":"Jane Doe","email":"jane@example.com"}]}`), nil
			},
		}
		h := NewTeamHandler(entity)
		r := withUser(httptest.NewRequest(http.MethodGet, "/teams/"+testTeamUUID+"/members", nil))
		r.SetPathValue("id", testTeamUUID)
		w := httptest.NewRecorder()
		h.GetTeamMembers(w, r)
		assertStatus(t, w, http.StatusOK)
		views := decodeJSON[[]TeamMemberView](t, w)
		if len(views) != 1 || views[0].Role != "" {
			t.Errorf("view = %+v, want a blank role rather than an error", views)
		}
	})
}
