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

func TestPatchCase(t *testing.T) {
	const caseID = "11111111-1111-1111-1111-111111111111"

	t.Run("rejects empty case ID", func(t *testing.T) {
		h := NewCaseHandler(&mockEntityCaseClient{}, "")
		r := httptest.NewRequest(http.MethodPatch, "/cases/", strings.NewReader(`{"state":"closed"}`))
		w := httptest.NewRecorder()
		h.PatchCase(w, r)
		assertStatus(t, w, http.StatusBadRequest)
		assertErrorMessage(t, w, ErrMsgInvalidUUID)
		assertContentType(t, w, "application/json")
	})

	t.Run("rejects non-UUID case ID", func(t *testing.T) {
		h := NewCaseHandler(&mockEntityCaseClient{}, "")
		r := httptest.NewRequest(http.MethodPatch, "/cases/case-42", strings.NewReader(`{"state":"closed"}`))
		r.SetPathValue("id", "case-42")
		w := httptest.NewRecorder()
		h.PatchCase(w, r)
		assertStatus(t, w, http.StatusBadRequest)
		assertErrorMessage(t, w, ErrMsgInvalidUUID)
		assertContentType(t, w, "application/json")
	})

	t.Run("rejects body exceeding 1 MiB", func(t *testing.T) {
		h := NewCaseHandler(&mockEntityCaseClient{}, "")
		r := httptest.NewRequest(http.MethodPatch, "/cases/"+caseID, strings.NewReader(strings.Repeat("x", maxRequestBodyBytes+1)))
		r.SetPathValue("id", caseID)
		w := httptest.NewRecorder()
		h.PatchCase(w, r)
		assertStatus(t, w, http.StatusRequestEntityTooLarge)
		assertErrorMessage(t, w, ErrMsgTooLarge)
		assertContentType(t, w, "application/json")
	})

	t.Run("rejects invalid JSON body", func(t *testing.T) {
		h := NewCaseHandler(&mockEntityCaseClient{}, "")
		r := httptest.NewRequest(http.MethodPatch, "/cases/"+caseID, strings.NewReader(`not-json`))
		r.SetPathValue("id", caseID)
		w := httptest.NewRecorder()
		h.PatchCase(w, r)
		assertStatus(t, w, http.StatusBadRequest)
		assertErrorMessage(t, w, ErrMsgBadRequest)
		assertContentType(t, w, "application/json")
	})

	t.Run("rejects empty body", func(t *testing.T) {
		h := NewCaseHandler(&mockEntityCaseClient{}, "")
		r := httptest.NewRequest(http.MethodPatch, "/cases/"+caseID, nil)
		r.SetPathValue("id", caseID)
		w := httptest.NewRecorder()
		h.PatchCase(w, r)
		assertStatus(t, w, http.StatusBadRequest)
		assertErrorMessage(t, w, ErrMsgBadRequest)
		assertContentType(t, w, "application/json")
	})

	t.Run("forwards body verbatim and returns upstream response", func(t *testing.T) {
		var capturedCaseID string
		var capturedBody []byte
		reqBody := `{"state":"closed"}`
		client := &mockEntityCaseClient{
			patchCaseFn: func(_ context.Context, id string, body []byte) ([]byte, error) {
				capturedCaseID = id
				capturedBody = body
				return []byte(`{"message":"Case updated successfully","case":{"id":"` + caseID + `","state":"closed"}}`), nil
			},
		}
		h := NewCaseHandler(client, "")
		r := httptest.NewRequest(http.MethodPatch, "/cases/"+caseID, strings.NewReader(reqBody))
		r.SetPathValue("id", caseID)
		w := httptest.NewRecorder()
		h.PatchCase(w, r)

		assertStatus(t, w, http.StatusOK)
		assertContentType(t, w, "application/json")

		if capturedCaseID != caseID {
			t.Errorf("caseID = %q, want %q", capturedCaseID, caseID)
		}
		if string(capturedBody) != reqBody {
			t.Errorf("upstream body = %q, want verbatim %q", string(capturedBody), reqBody)
		}

		resp := decodeJSON[map[string]any](t, w)
		if resp["message"] != "Case updated successfully" {
			t.Errorf("message = %v, want %v", resp["message"], "Case updated successfully")
		}
	})

	t.Run("upstream errors are mapped correctly", func(t *testing.T) {
		for _, tc := range upstreamErrors("Failed to update case.") {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				client := &mockEntityCaseClient{
					patchCaseFn: func(_ context.Context, _ string, _ []byte) ([]byte, error) {
						return nil, tc.err
					},
				}
				h := NewCaseHandler(client, "")
				r := httptest.NewRequest(http.MethodPatch, "/cases/"+caseID, strings.NewReader(`{"state":"closed"}`))
				r.SetPathValue("id", caseID)
				w := httptest.NewRecorder()
				h.PatchCase(w, r)
				assertStatus(t, w, tc.wantCode)
				assertErrorMessage(t, w, tc.wantMsg)
				assertContentType(t, w, "application/json")
			})
		}
	})
}

func TestCreateCaseComment(t *testing.T) {
	const caseID = "11111111-1111-1111-1111-111111111111"

	t.Run("rejects empty case ID", func(t *testing.T) {
		h := NewCaseHandler(&mockEntityCaseClient{}, "")
		r := httptest.NewRequest(http.MethodPost, "/cases//comments", strings.NewReader(`{"type":"comment","content":"hi"}`))
		w := httptest.NewRecorder()
		h.CreateCaseComment(w, r)
		assertStatus(t, w, http.StatusBadRequest)
		assertErrorMessage(t, w, ErrMsgInvalidUUID)
		assertContentType(t, w, "application/json")
	})

	t.Run("rejects non-UUID case ID", func(t *testing.T) {
		h := NewCaseHandler(&mockEntityCaseClient{}, "")
		r := httptest.NewRequest(http.MethodPost, "/cases/case-42/comments", strings.NewReader(`{"type":"comment","content":"hi"}`))
		r.SetPathValue("id", "case-42")
		w := httptest.NewRecorder()
		h.CreateCaseComment(w, r)
		assertStatus(t, w, http.StatusBadRequest)
		assertErrorMessage(t, w, ErrMsgInvalidUUID)
		assertContentType(t, w, "application/json")
	})

	t.Run("rejects body exceeding 1 MiB", func(t *testing.T) {
		h := NewCaseHandler(&mockEntityCaseClient{}, "")
		r := httptest.NewRequest(http.MethodPost, "/cases/"+caseID+"/comments", strings.NewReader(strings.Repeat("x", maxRequestBodyBytes+1)))
		r.SetPathValue("id", caseID)
		w := httptest.NewRecorder()
		h.CreateCaseComment(w, r)
		assertStatus(t, w, http.StatusRequestEntityTooLarge)
		assertErrorMessage(t, w, ErrMsgTooLarge)
		assertContentType(t, w, "application/json")
	})

	t.Run("rejects invalid JSON body", func(t *testing.T) {
		h := NewCaseHandler(&mockEntityCaseClient{}, "")
		r := httptest.NewRequest(http.MethodPost, "/cases/"+caseID+"/comments", strings.NewReader(`not-json`))
		r.SetPathValue("id", caseID)
		w := httptest.NewRecorder()
		h.CreateCaseComment(w, r)
		assertStatus(t, w, http.StatusBadRequest)
		assertErrorMessage(t, w, ErrMsgBadRequest)
		assertContentType(t, w, "application/json")
	})

	t.Run("rejects empty body", func(t *testing.T) {
		h := NewCaseHandler(&mockEntityCaseClient{}, "")
		r := httptest.NewRequest(http.MethodPost, "/cases/"+caseID+"/comments", nil)
		r.SetPathValue("id", caseID)
		w := httptest.NewRecorder()
		h.CreateCaseComment(w, r)
		assertStatus(t, w, http.StatusBadRequest)
		assertErrorMessage(t, w, ErrMsgBadRequest)
		assertContentType(t, w, "application/json")
	})

	t.Run("rejects missing content", func(t *testing.T) {
		h := NewCaseHandler(&mockEntityCaseClient{}, "svc@example.com")
		r := httptest.NewRequest(http.MethodPost, "/cases/"+caseID+"/comments", strings.NewReader(`{"type":"comment"}`))
		r.SetPathValue("id", caseID)
		w := httptest.NewRecorder()
		h.CreateCaseComment(w, r)
		assertStatus(t, w, http.StatusBadRequest)
		assertErrorMessage(t, w, ErrMsgContentRequired)
		assertContentType(t, w, "application/json")
	})

	t.Run("rejects blank content", func(t *testing.T) {
		h := NewCaseHandler(&mockEntityCaseClient{}, "svc@example.com")
		r := httptest.NewRequest(http.MethodPost, "/cases/"+caseID+"/comments", strings.NewReader(`{"type":"comment","content":"   "}`))
		r.SetPathValue("id", caseID)
		w := httptest.NewRecorder()
		h.CreateCaseComment(w, r)
		assertStatus(t, w, http.StatusBadRequest)
		assertErrorMessage(t, w, ErrMsgContentRequired)
		assertContentType(t, w, "application/json")
	})

	t.Run("injects the configured actorEmail and ignores a caller-supplied one", func(t *testing.T) {
		var capturedCaseID string
		var capturedBody []byte
		reqBody := `{"type":"comment","content":"Investigating now.","actorEmail":"attacker@example.com"}`
		client := &mockEntityCaseClient{
			createCaseCommentFn: func(_ context.Context, id string, body []byte) ([]byte, error) {
				capturedCaseID = id
				capturedBody = body
				return []byte(`{"message":"Comment created successfully","comment":{"id":"c-1","createdBy":"svc@example.com"}}`), nil
			},
		}
		h := NewCaseHandler(client, "svc@example.com")
		r := httptest.NewRequest(http.MethodPost, "/cases/"+caseID+"/comments", strings.NewReader(reqBody))
		r.SetPathValue("id", caseID)
		w := httptest.NewRecorder()
		h.CreateCaseComment(w, r)

		assertStatus(t, w, http.StatusCreated)
		assertContentType(t, w, "application/json")

		if capturedCaseID != caseID {
			t.Errorf("caseID = %q, want %q", capturedCaseID, caseID)
		}

		var sent struct {
			Type       string `json:"type"`
			Content    string `json:"content"`
			ActorEmail string `json:"actorEmail"`
		}
		if err := json.Unmarshal(capturedBody, &sent); err != nil {
			t.Fatalf("decode captured body: %v; raw: %s", err, capturedBody)
		}
		if sent.Type != "comment" {
			t.Errorf("type = %q, want %q", sent.Type, "comment")
		}
		if sent.Content != "Investigating now." {
			t.Errorf("content = %q, want %q", sent.Content, "Investigating now.")
		}
		if sent.ActorEmail != "svc@example.com" {
			t.Errorf("actorEmail = %q, want the configured service actor email, never the caller-supplied one", sent.ActorEmail)
		}

		resp := decodeJSON[map[string]any](t, w)
		if resp["message"] != "Comment created successfully" {
			t.Errorf("message = %v, want %v", resp["message"], "Comment created successfully")
		}
	})

	t.Run("upstream errors are mapped correctly", func(t *testing.T) {
		for _, tc := range upstreamErrors("Failed to create case comment.") {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				client := &mockEntityCaseClient{
					createCaseCommentFn: func(_ context.Context, _ string, _ []byte) ([]byte, error) {
						return nil, tc.err
					},
				}
				h := NewCaseHandler(client, "")
				r := httptest.NewRequest(http.MethodPost, "/cases/"+caseID+"/comments", strings.NewReader(`{"type":"comment","content":"hi"}`))
				r.SetPathValue("id", caseID)
				w := httptest.NewRecorder()
				h.CreateCaseComment(w, r)
				assertStatus(t, w, tc.wantCode)
				assertErrorMessage(t, w, tc.wantMsg)
				assertContentType(t, w, "application/json")
			})
		}
	})
}

func TestSearchCases(t *testing.T) {
	t.Run("rejects body exceeding 1 MiB", func(t *testing.T) {
		h := NewCaseHandler(&mockEntityCaseClient{}, "")
		r := httptest.NewRequest(http.MethodPost, "/cases/search", strings.NewReader(strings.Repeat("x", maxRequestBodyBytes+1)))
		w := httptest.NewRecorder()
		h.SearchCases(w, r)
		assertStatus(t, w, http.StatusRequestEntityTooLarge)
		assertErrorMessage(t, w, ErrMsgTooLarge)
		assertContentType(t, w, "application/json")
	})

	t.Run("rejects invalid JSON body", func(t *testing.T) {
		h := NewCaseHandler(&mockEntityCaseClient{}, "")
		r := httptest.NewRequest(http.MethodPost, "/cases/search", strings.NewReader(`not-json`))
		w := httptest.NewRecorder()
		h.SearchCases(w, r)
		assertStatus(t, w, http.StatusBadRequest)
		assertErrorMessage(t, w, ErrMsgBadRequest)
		assertContentType(t, w, "application/json")
	})

	t.Run("forwards body verbatim and returns upstream response", func(t *testing.T) {
		var capturedBody []byte
		reqBody := `{"filters":{"filters":[{"field":"number","op":"eq","values":["CS0012345"]}]}}`
		client := &mockEntityCaseClient{
			searchCasesFn: func(_ context.Context, body []byte) ([]byte, error) {
				capturedBody = body
				return []byte(`{"cases":[{"id":"11111111-1111-1111-1111-111111111111","number":"CS0012345"}],"total":1,"limit":0,"offset":0}`), nil
			},
		}
		h := NewCaseHandler(client, "")
		r := httptest.NewRequest(http.MethodPost, "/cases/search", strings.NewReader(reqBody))
		w := httptest.NewRecorder()
		h.SearchCases(w, r)

		assertStatus(t, w, http.StatusOK)
		assertContentType(t, w, "application/json")

		if string(capturedBody) != reqBody {
			t.Errorf("upstream body = %q, want verbatim %q", string(capturedBody), reqBody)
		}

		resp := decodeJSON[map[string]any](t, w)
		cases, _ := resp["cases"].([]any)
		if len(cases) != 1 {
			t.Errorf("cases = %v, want 1 entry", resp["cases"])
		}
	})

	t.Run("upstream errors are mapped correctly", func(t *testing.T) {
		for _, tc := range upstreamErrors("Failed to search cases.") {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				client := &mockEntityCaseClient{
					searchCasesFn: func(_ context.Context, _ []byte) ([]byte, error) {
						return nil, tc.err
					},
				}
				h := NewCaseHandler(client, "")
				r := httptest.NewRequest(http.MethodPost, "/cases/search", strings.NewReader(`{"filters":{"filters":[]}}`))
				w := httptest.NewRecorder()
				h.SearchCases(w, r)
				assertStatus(t, w, tc.wantCode)
				assertErrorMessage(t, w, tc.wantMsg)
				assertContentType(t, w, "application/json")
			})
		}
	})
}

func TestAddCaseTag(t *testing.T) {
	const caseID = "11111111-1111-1111-1111-111111111111"

	t.Run("rejects empty case ID", func(t *testing.T) {
		h := NewCaseHandler(&mockEntityCaseClient{}, "svc@example.com")
		r := httptest.NewRequest(http.MethodPost, "/cases//tags", strings.NewReader(`{"label":"urgent"}`))
		w := httptest.NewRecorder()
		h.AddCaseTag(w, r)
		assertStatus(t, w, http.StatusBadRequest)
		assertErrorMessage(t, w, ErrMsgInvalidUUID)
		assertContentType(t, w, "application/json")
	})

	t.Run("rejects non-UUID case ID", func(t *testing.T) {
		h := NewCaseHandler(&mockEntityCaseClient{}, "svc@example.com")
		r := httptest.NewRequest(http.MethodPost, "/cases/case-42/tags", strings.NewReader(`{"label":"urgent"}`))
		r.SetPathValue("id", "case-42")
		w := httptest.NewRecorder()
		h.AddCaseTag(w, r)
		assertStatus(t, w, http.StatusBadRequest)
		assertErrorMessage(t, w, ErrMsgInvalidUUID)
		assertContentType(t, w, "application/json")
	})

	t.Run("rejects body exceeding 1 MiB", func(t *testing.T) {
		h := NewCaseHandler(&mockEntityCaseClient{}, "svc@example.com")
		r := httptest.NewRequest(http.MethodPost, "/cases/"+caseID+"/tags", strings.NewReader(strings.Repeat("x", maxRequestBodyBytes+1)))
		r.SetPathValue("id", caseID)
		w := httptest.NewRecorder()
		h.AddCaseTag(w, r)
		assertStatus(t, w, http.StatusRequestEntityTooLarge)
		assertErrorMessage(t, w, ErrMsgTooLarge)
		assertContentType(t, w, "application/json")
	})

	t.Run("rejects invalid JSON body", func(t *testing.T) {
		h := NewCaseHandler(&mockEntityCaseClient{}, "svc@example.com")
		r := httptest.NewRequest(http.MethodPost, "/cases/"+caseID+"/tags", strings.NewReader(`not-json`))
		r.SetPathValue("id", caseID)
		w := httptest.NewRecorder()
		h.AddCaseTag(w, r)
		assertStatus(t, w, http.StatusBadRequest)
		assertErrorMessage(t, w, ErrMsgBadRequest)
		assertContentType(t, w, "application/json")
	})

	t.Run("rejects empty body", func(t *testing.T) {
		h := NewCaseHandler(&mockEntityCaseClient{}, "svc@example.com")
		r := httptest.NewRequest(http.MethodPost, "/cases/"+caseID+"/tags", nil)
		r.SetPathValue("id", caseID)
		w := httptest.NewRecorder()
		h.AddCaseTag(w, r)
		assertStatus(t, w, http.StatusBadRequest)
		assertErrorMessage(t, w, ErrMsgBadRequest)
		assertContentType(t, w, "application/json")
	})

	t.Run("rejects missing label", func(t *testing.T) {
		h := NewCaseHandler(&mockEntityCaseClient{}, "svc@example.com")
		r := httptest.NewRequest(http.MethodPost, "/cases/"+caseID+"/tags", strings.NewReader(`{}`))
		r.SetPathValue("id", caseID)
		w := httptest.NewRecorder()
		h.AddCaseTag(w, r)
		assertStatus(t, w, http.StatusBadRequest)
		assertErrorMessage(t, w, ErrMsgLabelRequired)
		assertContentType(t, w, "application/json")
	})

	t.Run("rejects blank label", func(t *testing.T) {
		h := NewCaseHandler(&mockEntityCaseClient{}, "svc@example.com")
		r := httptest.NewRequest(http.MethodPost, "/cases/"+caseID+"/tags", strings.NewReader(`{"label":"   "}`))
		r.SetPathValue("id", caseID)
		w := httptest.NewRecorder()
		h.AddCaseTag(w, r)
		assertStatus(t, w, http.StatusBadRequest)
		assertErrorMessage(t, w, ErrMsgLabelRequired)
		assertContentType(t, w, "application/json")
	})

	t.Run("injects the configured actorEmail and ignores a caller-supplied one", func(t *testing.T) {
		var capturedCaseID string
		var capturedBody []byte
		client := &mockEntityCaseClient{
			addCaseTagFn: func(_ context.Context, id string, body []byte) ([]byte, error) {
				capturedCaseID = id
				capturedBody = body
				return []byte(`{"id":"tag-1","label":"urgent"}`), nil
			},
		}
		h := NewCaseHandler(client, "svc@example.com")
		reqBody := `{"label":"urgent","actorEmail":"attacker@example.com"}`
		r := httptest.NewRequest(http.MethodPost, "/cases/"+caseID+"/tags", strings.NewReader(reqBody))
		r.SetPathValue("id", caseID)
		w := httptest.NewRecorder()
		h.AddCaseTag(w, r)

		assertStatus(t, w, http.StatusCreated)
		assertContentType(t, w, "application/json")

		if capturedCaseID != caseID {
			t.Errorf("caseID = %q, want %q", capturedCaseID, caseID)
		}

		var sent struct {
			Label      string `json:"label"`
			ActorEmail string `json:"actorEmail"`
		}
		if err := json.Unmarshal(capturedBody, &sent); err != nil {
			t.Fatalf("decode captured body: %v; raw: %s", err, capturedBody)
		}
		if sent.Label != "urgent" {
			t.Errorf("label = %q, want %q", sent.Label, "urgent")
		}
		if sent.ActorEmail != "svc@example.com" {
			t.Errorf("actorEmail = %q, want the configured service actor email, never the caller-supplied one", sent.ActorEmail)
		}

		resp := decodeJSON[map[string]any](t, w)
		if resp["label"] != "urgent" {
			t.Errorf("label = %v, want %v", resp["label"], "urgent")
		}
	})

	t.Run("upstream errors are mapped correctly", func(t *testing.T) {
		for _, tc := range upstreamErrors("Failed to add case tag.") {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				client := &mockEntityCaseClient{
					addCaseTagFn: func(_ context.Context, _ string, _ []byte) ([]byte, error) {
						return nil, tc.err
					},
				}
				h := NewCaseHandler(client, "svc@example.com")
				r := httptest.NewRequest(http.MethodPost, "/cases/"+caseID+"/tags", strings.NewReader(`{"label":"urgent"}`))
				r.SetPathValue("id", caseID)
				w := httptest.NewRecorder()
				h.AddCaseTag(w, r)
				assertStatus(t, w, tc.wantCode)
				assertErrorMessage(t, w, tc.wantMsg)
				assertContentType(t, w, "application/json")
			})
		}
	})
}
