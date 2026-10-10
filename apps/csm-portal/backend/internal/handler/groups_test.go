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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSearchGroups(t *testing.T) {
	t.Run("requires authenticated user", func(t *testing.T) {
		h := NewGroupHandler(&mockEntityGroupClient{})
		r := httptest.NewRequest(http.MethodPost, "/groups/search", strings.NewReader(`{}`))
		w := httptest.NewRecorder()
		h.SearchGroups(w, r)
		assertStatus(t, w, http.StatusUnauthorized)
		assertErrorMessage(t, w, ErrMsgUnauthorized)
		assertContentType(t, w, "application/json")
	})

	t.Run("rejects body exceeding 1 MiB", func(t *testing.T) {
		h := NewGroupHandler(&mockEntityGroupClient{})
		r := withUser(httptest.NewRequest(http.MethodPost, "/groups/search", strings.NewReader(strings.Repeat("x", maxRequestBodyBytes+1))))
		w := httptest.NewRecorder()
		h.SearchGroups(w, r)
		assertStatus(t, w, http.StatusRequestEntityTooLarge)
		assertErrorMessage(t, w, ErrMsgTooLarge)
		assertContentType(t, w, "application/json")
	})

	t.Run("rejects invalid JSON body", func(t *testing.T) {
		h := NewGroupHandler(&mockEntityGroupClient{})
		r := withUser(httptest.NewRequest(http.MethodPost, "/groups/search", strings.NewReader(`not-json`)))
		w := httptest.NewRecorder()
		h.SearchGroups(w, r)
		assertStatus(t, w, http.StatusBadRequest)
		assertErrorMessage(t, w, ErrMsgBadRequest)
		assertContentType(t, w, "application/json")
	})

	t.Run("forwards body to upstream and returns 200 with response", func(t *testing.T) {
		const reqPayload = `{"pagination":{"limit":10,"offset":0}}`
		var capturedBody []byte
		client := &mockEntityGroupClient{
			searchGroupsFn: func(_ context.Context, body []byte) ([]byte, error) {
				capturedBody = body
				return []byte(`{"groups":[{"id":"11111111-1111-1111-1111-111111111111","name":"Network","active":true}],"total":1,"limit":10,"offset":0}`), nil
			},
		}
		h := NewGroupHandler(client)
		r := withUser(httptest.NewRequest(http.MethodPost, "/groups/search", strings.NewReader(reqPayload)))
		w := httptest.NewRecorder()
		h.SearchGroups(w, r)

		assertStatus(t, w, http.StatusOK)
		assertContentType(t, w, "application/json")
		if string(capturedBody) != reqPayload {
			t.Errorf("upstream received body %q, want %q", capturedBody, reqPayload)
		}
	})

	t.Run("upstream errors are mapped correctly", func(t *testing.T) {
		for _, tc := range upstreamErrorsGeneric("Failed to search groups.") {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				client := &mockEntityGroupClient{
					searchGroupsFn: func(_ context.Context, _ []byte) ([]byte, error) {
						return nil, tc.err
					},
				}
				h := NewGroupHandler(client)
				r := withUser(httptest.NewRequest(http.MethodPost, "/groups/search", strings.NewReader(`{}`)))
				w := httptest.NewRecorder()
				h.SearchGroups(w, r)
				assertStatus(t, w, tc.wantCode)
				assertErrorMessage(t, w, tc.wantMsg)
				assertContentType(t, w, "application/json")
			})
		}
	})
}

const testGroupID = "22222222-2222-4222-8222-222222222222"

func TestGetGroup(t *testing.T) {
	newReq := func(id string) *http.Request {
		r := withUser(httptest.NewRequest(http.MethodGet, "/groups/"+id, nil))
		r.SetPathValue("id", id)
		return r
	}

	t.Run("requires authenticated user", func(t *testing.T) {
		h := NewGroupHandler(&mockEntityGroupClient{})
		r := httptest.NewRequest(http.MethodGet, "/groups/"+testGroupID, nil)
		r.SetPathValue("id", testGroupID)
		w := httptest.NewRecorder()
		h.GetGroup(w, r)
		assertStatus(t, w, http.StatusUnauthorized)
		assertErrorMessage(t, w, ErrMsgUnauthorized)
		assertContentType(t, w, "application/json")
	})

	t.Run("rejects a malformed or empty id without calling upstream", func(t *testing.T) {
		for _, id := range []string{"not-a-uuid", "", "22222222222242228222222222222222", testGroupID + "/members"} {
			client := &mockEntityGroupClient{getGroupFn: func(context.Context, string) ([]byte, error) {
				t.Errorf("upstream called for id %q", id)
				return nil, nil
			}}
			h := NewGroupHandler(client)
			w := httptest.NewRecorder()
			h.GetGroup(w, newReq(id))
			assertStatus(t, w, http.StatusBadRequest)
			assertErrorMessage(t, w, ErrMsgInvalidUUID)
			assertContentType(t, w, "application/json")
		}
	})

	t.Run("forwards the id to upstream and returns the group with its members", func(t *testing.T) {
		var gotID string
		client := &mockEntityGroupClient{
			getGroupFn: func(_ context.Context, id string) ([]byte, error) {
				gotID = id
				return []byte(`{"id":"` + testGroupID + `","name":"CAB Approval","description":"Change Advisory Board","email":"cab@example.com","manager":{"id":"33333333-3333-4333-8333-333333333333","name":"Mia Manager"},"members":[{"id":"44444444-4444-4444-8444-444444444444","name":"Alice","email":"alice@example.com","userType":"INTERNAL","role":"lead"}],"total":1}`), nil
			},
		}
		h := NewGroupHandler(client)
		w := httptest.NewRecorder()
		h.GetGroup(w, newReq(testGroupID))

		assertStatus(t, w, http.StatusOK)
		assertContentType(t, w, "application/json")
		if gotID != testGroupID {
			t.Errorf("upstream received id %q, want %q", gotID, testGroupID)
		}
		resp := decodeJSON[map[string]any](t, w)
		if resp["name"] != "CAB Approval" || resp["total"].(float64) != 1 {
			t.Errorf("response = %v", resp)
		}
		members, ok := resp["members"].([]any)
		if !ok || len(members) != 1 || members[0].(map[string]any)["role"] != "lead" {
			t.Errorf("members = %v, want one lead member", resp["members"])
		}
	})

	t.Run("a group with no members is a 200 with an empty list, not an error", func(t *testing.T) {
		h := NewGroupHandler(&mockEntityGroupClient{})
		w := httptest.NewRecorder()
		h.GetGroup(w, newReq(testGroupID))
		assertStatus(t, w, http.StatusOK)
		resp := decodeJSON[map[string]any](t, w)
		if members, ok := resp["members"].([]any); !ok || len(members) != 0 {
			t.Errorf("members = %v, want []", resp["members"])
		}
	})

	t.Run("upstream errors are mapped correctly (404 unknown group, 403 non-internal caller)", func(t *testing.T) {
		for _, tc := range upstreamErrorsGeneric("Failed to retrieve group.") {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				client := &mockEntityGroupClient{
					getGroupFn: func(context.Context, string) ([]byte, error) { return nil, tc.err },
				}
				h := NewGroupHandler(client)
				w := httptest.NewRecorder()
				h.GetGroup(w, newReq(testGroupID))
				assertStatus(t, w, tc.wantCode)
				assertErrorMessage(t, w, tc.wantMsg)
				assertContentType(t, w, "application/json")
			})
		}
	})
}

// The approvals response is passed through untouched, so the new
// `assignmentGroup` reaches the webapp: an {id, name} object for an internal
// stage and null for a customer stage (whose approvers are the project's
// registered contacts, not a group).
func TestGetChangeRequestApprovals_PassesAssignmentGroupThrough(t *testing.T) {
	const upstream = `{"approvals":[` +
		`{"stage":"Peer Approval","approverType":"STATIC_GROUP","approverName":"Example Corp ABT","status":"PENDING","assignmentGroup":{"id":"` + testGroupID + `","name":"Example Corp ABT"},"approvers":[]},` +
		`{"stage":"Customer Approval","approverType":"STATIC_GROUP","approverName":"Customer Group","status":"PENDING","assignmentGroup":null,"approvers":[]}]}`
	client := &mockEntityChangeRequestClient{
		getChangeRequestApprovalsFn: func(context.Context, string) ([]byte, error) { return []byte(upstream), nil },
	}
	h := NewChangeRequestHandler(client)
	r := withUser(httptest.NewRequest(http.MethodGet, "/change-requests/"+testCRID+"/approvals", nil))
	r.SetPathValue("id", testCRID)
	w := httptest.NewRecorder()
	h.GetChangeRequestApprovals(w, r)
	assertStatus(t, w, http.StatusOK)

	var resp struct {
		Approvals []map[string]json.RawMessage `json:"approvals"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Approvals) != 2 {
		t.Fatalf("approvals = %d, want 2", len(resp.Approvals))
	}
	if got := string(resp.Approvals[0]["assignmentGroup"]); got != `{"id":"`+testGroupID+`","name":"Example Corp ABT"}` {
		t.Errorf("internal stage assignmentGroup = %s", got)
	}
	if got := string(resp.Approvals[1]["assignmentGroup"]); got != "null" {
		t.Errorf("customer stage assignmentGroup = %s, want null", got)
	}
}
