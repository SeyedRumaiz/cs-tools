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
	"strings"
	"testing"

	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/middleware"
)

const testCommentID = "11111111-1111-1111-1111-111111111111"

func TestUpdateComment(t *testing.T) {
	t.Run("rejects unauthenticated requests", func(t *testing.T) {
		h := NewCommentHandler(&mockEntityCommentClient{})
		r := httptest.NewRequest(http.MethodPatch, "/comments/"+testCommentID, strings.NewReader(`{"content":"edited"}`))
		r.SetPathValue("id", testCommentID)
		w := httptest.NewRecorder()
		h.UpdateComment(w, r)
		assertStatus(t, w, http.StatusUnauthorized)
		assertErrorMessage(t, w, ErrMsgUnauthorized)
		assertContentType(t, w, "application/json")
	})

	t.Run("rejects malformed UUID", func(t *testing.T) {
		h := NewCommentHandler(&mockEntityCommentClient{})
		r := withUser(httptest.NewRequest(http.MethodPatch, "/comments/not-a-uuid", strings.NewReader(`{"content":"edited"}`)))
		r.SetPathValue("id", "not-a-uuid")
		w := httptest.NewRecorder()
		h.UpdateComment(w, r)
		assertStatus(t, w, http.StatusBadRequest)
		assertErrorMessage(t, w, ErrMsgInvalidUUID)
		assertContentType(t, w, "application/json")
	})

	t.Run("rejects malformed JSON body", func(t *testing.T) {
		h := NewCommentHandler(&mockEntityCommentClient{})
		r := withUser(httptest.NewRequest(http.MethodPatch, "/comments/"+testCommentID, strings.NewReader(`{bad`)))
		r.SetPathValue("id", testCommentID)
		w := httptest.NewRecorder()
		h.UpdateComment(w, r)
		assertStatus(t, w, http.StatusBadRequest)
		assertErrorMessage(t, w, ErrMsgBadRequest)
		assertContentType(t, w, "application/json")
	})

	t.Run("forwards id and body, returns 200 with the updated comment", func(t *testing.T) {
		var capturedID string
		var capturedBody []byte
		client := &mockEntityCommentClient{
			updateCommentFn: func(_ context.Context, id string, body []byte) ([]byte, error) {
				capturedID, capturedBody = id, body
				return []byte(`{"id":"` + id + `","content":"edited"}`), nil
			},
		}
		h := NewCommentHandler(client)
		r := withUser(httptest.NewRequest(http.MethodPatch, "/comments/"+testCommentID, strings.NewReader(`{"content":"edited"}`)))
		r.SetPathValue("id", testCommentID)
		w := httptest.NewRecorder()
		h.UpdateComment(w, r)

		assertStatus(t, w, http.StatusOK)
		assertContentType(t, w, "application/json")
		if capturedID != testCommentID {
			t.Errorf("upstream received id %q, want %q", capturedID, testCommentID)
		}
		if string(capturedBody) != `{"content":"edited"}` {
			t.Errorf("upstream received body %q", capturedBody)
		}
		resp := decodeJSON[map[string]any](t, w)
		if resp["content"] != "edited" {
			t.Errorf("content = %v, want %q", resp["content"], "edited")
		}
	})

	t.Run("upstream errors are mapped correctly", func(t *testing.T) {
		for _, tc := range upstreamErrors("Failed to update comment.") {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				client := &mockEntityCommentClient{
					updateCommentFn: func(_ context.Context, _ string, _ []byte) ([]byte, error) {
						return nil, tc.err
					},
				}
				h := NewCommentHandler(client)
				r := withUser(httptest.NewRequest(http.MethodPatch, "/comments/"+testCommentID, strings.NewReader(`{"content":"edited"}`)))
				r.SetPathValue("id", testCommentID)
				w := httptest.NewRecorder()
				h.UpdateComment(w, r)
				assertStatus(t, w, tc.wantCode)
				assertErrorMessage(t, w, tc.wantMsg)
			})
		}
	})
}

func TestDeleteComment(t *testing.T) {
	t.Run("rejects unauthenticated requests", func(t *testing.T) {
		h := NewCommentHandler(&mockEntityCommentClient{})
		r := httptest.NewRequest(http.MethodDelete, "/comments/"+testCommentID, nil)
		r.SetPathValue("id", testCommentID)
		w := httptest.NewRecorder()
		h.DeleteComment(w, r)
		assertStatus(t, w, http.StatusUnauthorized)
		assertErrorMessage(t, w, ErrMsgUnauthorized)
		assertContentType(t, w, "application/json")
	})

	t.Run("rejects malformed UUID", func(t *testing.T) {
		h := NewCommentHandler(&mockEntityCommentClient{})
		r := withUser(httptest.NewRequest(http.MethodDelete, "/comments/not-a-uuid", nil))
		r.SetPathValue("id", "not-a-uuid")
		w := httptest.NewRecorder()
		h.DeleteComment(w, r)
		assertStatus(t, w, http.StatusBadRequest)
		assertErrorMessage(t, w, ErrMsgInvalidUUID)
		assertContentType(t, w, "application/json")
	})

	t.Run("forwards id to upstream, returns 204 with no body", func(t *testing.T) {
		var capturedID string
		client := &mockEntityCommentClient{
			deleteCommentFn: func(_ context.Context, id string) ([]byte, error) {
				capturedID = id
				return nil, nil
			},
		}
		h := NewCommentHandler(client)
		r := withUser(httptest.NewRequest(http.MethodDelete, "/comments/"+testCommentID, nil))
		r.SetPathValue("id", testCommentID)
		w := httptest.NewRecorder()
		h.DeleteComment(w, r)

		assertStatus(t, w, http.StatusNoContent)
		if capturedID != testCommentID {
			t.Errorf("upstream received id %q, want %q", capturedID, testCommentID)
		}
		if w.Body.Len() != 0 {
			t.Errorf("body = %q, want empty", w.Body.String())
		}
	})

	t.Run("upstream errors are mapped correctly", func(t *testing.T) {
		for _, tc := range upstreamErrors("Failed to delete comment.") {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				client := &mockEntityCommentClient{
					deleteCommentFn: func(_ context.Context, _ string) ([]byte, error) {
						return nil, tc.err
					},
				}
				h := NewCommentHandler(client)
				r := withUser(httptest.NewRequest(http.MethodDelete, "/comments/"+testCommentID, nil))
				r.SetPathValue("id", testCommentID)
				w := httptest.NewRecorder()
				h.DeleteComment(w, r)
				assertStatus(t, w, tc.wantCode)
				assertErrorMessage(t, w, tc.wantMsg)
			})
		}
	})
}

// requestWithUser returns r with a *middleware.UserInfo (caller-chosen email
// and roles) stored in its context -- the same shape withUser uses for
// testUser, just configurable, for exercising authorizeCommentActor's
// author-vs-PermUpdateDeleteAnyComment logic.
func requestWithUser(r *http.Request, email string, roles []string) *http.Request {
	user := &middleware.UserInfo{Email: email, UserID: "u-test", Roles: roles}
	return r.WithContext(middleware.WithUserInfo(r.Context(), user))
}

// commentAuthoredBy is a mockEntityCommentClient.getCommentFn returning a
// comment whose createdBy.email is authorEmail -- for setting up
// authorizeCommentActor's "is this caller the author" check.
func commentAuthoredBy(authorEmail string) func(context.Context, string) ([]byte, error) {
	return func(_ context.Context, id string) ([]byte, error) {
		return []byte(`{"id":"` + id + `","createdBy":{"email":"` + authorEmail + `"}}`), nil
	}
}

// TestAuthorizeCommentActor_EmptyAuthorEmailDoesNotMatchEmptyCallerEmail
// covers a real bug: strings.EqualFold("", "") returns true, so without an
// explicit non-empty guard, a comment with no resolved author email (e.g. an
// integration account with only an id or name, see domain.NewUserReference)
// would match a caller whose own email failed to resolve to anything,
// treating a complete stranger as "the author". Neither has any role to fall
// back on either, so this must be Forbidden, not an accidental match.
func TestAuthorizeCommentActor_EmptyAuthorEmailDoesNotMatchEmptyCallerEmail(t *testing.T) {
	client := &mockEntityCommentClient{
		getCommentFn: commentAuthoredBy(""),
	}
	h := NewCommentHandler(client)
	r := requestWithUser(httptest.NewRequest(http.MethodPatch, "/comments/"+testCommentID, strings.NewReader(`{"content":"edited"}`)), "", nil)
	r.SetPathValue("id", testCommentID)
	w := httptest.NewRecorder()
	h.UpdateComment(w, r)

	assertStatus(t, w, http.StatusForbidden)
	assertErrorMessage(t, w, ErrMsgForbidden)
}

// TestUpdateComment_AuthorizeCommentActor covers CommentHandler's own
// authorization decision (entity-service performs none of its own any more --
// see comments.go's authorizeCommentActor and PermUpdateDeleteAnyComment's own
// doc comment for why cs_engineer alone is excluded from the "any comment"
// population even though it reaches the handler at all via
// PermUpdateDeleteComment).
func TestUpdateComment_AuthorizeCommentActor(t *testing.T) {
	const author = "author@example.com"
	tests := []struct {
		name            string
		callerEmail     string
		roles           []string
		wireAccessGuard bool
		wantStatus      int
	}{
		{name: "the comment's own author succeeds, regardless of roles", callerEmail: author, roles: nil, wireAccessGuard: true, wantStatus: http.StatusOK},
		{name: "comment_updater succeeds on someone else's comment", callerEmail: "other@example.com", roles: []string{"test-comment-updater"}, wireAccessGuard: true, wantStatus: http.StatusOK},
		{name: "admin succeeds on someone else's comment", callerEmail: "other@example.com", roles: []string{"test-admin"}, wireAccessGuard: true, wantStatus: http.StatusOK},
		{name: "cs_engineer alone is forbidden from someone else's comment", callerEmail: "other@example.com", roles: []string{"test-cs-engineer"}, wireAccessGuard: true, wantStatus: http.StatusForbidden},
		{name: "no access guard wired is forbidden from someone else's comment, even for comment_updater", callerEmail: "other@example.com", roles: []string{"test-comment-updater"}, wireAccessGuard: false, wantStatus: http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &mockEntityCommentClient{
				getCommentFn: commentAuthoredBy(author),
				updateCommentFn: func(_ context.Context, id string, _ []byte) ([]byte, error) {
					return []byte(`{"id":"` + id + `"}`), nil
				},
			}
			h := NewCommentHandler(client)
			if tt.wireAccessGuard {
				h = h.WithAccessGuard(viewerAccessGuard)
			}
			r := requestWithUser(httptest.NewRequest(http.MethodPatch, "/comments/"+testCommentID, strings.NewReader(`{"content":"edited"}`)), tt.callerEmail, tt.roles)
			r.SetPathValue("id", testCommentID)
			w := httptest.NewRecorder()
			h.UpdateComment(w, r)

			assertStatus(t, w, tt.wantStatus)
			if tt.wantStatus == http.StatusForbidden {
				assertErrorMessage(t, w, ErrMsgForbidden)
			}
		})
	}
}

// TestDeleteComment_AuthorizeCommentActor is
// TestUpdateComment_AuthorizeCommentActor's DELETE counterpart -- same
// population, same reasoning.
func TestDeleteComment_AuthorizeCommentActor(t *testing.T) {
	const author = "author@example.com"
	tests := []struct {
		name            string
		callerEmail     string
		roles           []string
		wireAccessGuard bool
		wantStatus      int
	}{
		{name: "the comment's own author succeeds, regardless of roles", callerEmail: author, roles: nil, wireAccessGuard: true, wantStatus: http.StatusNoContent},
		{name: "comment_updater succeeds on someone else's comment", callerEmail: "other@example.com", roles: []string{"test-comment-updater"}, wireAccessGuard: true, wantStatus: http.StatusNoContent},
		{name: "admin succeeds on someone else's comment", callerEmail: "other@example.com", roles: []string{"test-admin"}, wireAccessGuard: true, wantStatus: http.StatusNoContent},
		{name: "cs_engineer alone is forbidden from someone else's comment", callerEmail: "other@example.com", roles: []string{"test-cs-engineer"}, wireAccessGuard: true, wantStatus: http.StatusForbidden},
		{name: "no access guard wired is forbidden from someone else's comment, even for comment_updater", callerEmail: "other@example.com", roles: []string{"test-comment-updater"}, wireAccessGuard: false, wantStatus: http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &mockEntityCommentClient{
				getCommentFn: commentAuthoredBy(author),
				deleteCommentFn: func(_ context.Context, _ string) ([]byte, error) {
					return nil, nil
				},
			}
			h := NewCommentHandler(client)
			if tt.wireAccessGuard {
				h = h.WithAccessGuard(viewerAccessGuard)
			}
			r := requestWithUser(httptest.NewRequest(http.MethodDelete, "/comments/"+testCommentID, nil), tt.callerEmail, tt.roles)
			r.SetPathValue("id", testCommentID)
			w := httptest.NewRecorder()
			h.DeleteComment(w, r)

			assertStatus(t, w, tt.wantStatus)
			if tt.wantStatus == http.StatusForbidden {
				assertErrorMessage(t, w, ErrMsgForbidden)
			}
		})
	}
}

// TestUpdateComment_GetCommentFailurePropagates confirms the author-lookup
// call's own errors (e.g. the comment doesn't exist) are mapped and
// surfaced, not swallowed or misreported as 403.
func TestUpdateComment_GetCommentFailurePropagates(t *testing.T) {
	for _, tc := range upstreamErrors("Failed to load comment.") {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			client := &mockEntityCommentClient{
				getCommentFn: func(context.Context, string) ([]byte, error) {
					return nil, tc.err
				},
			}
			h := NewCommentHandler(client)
			r := withUser(httptest.NewRequest(http.MethodPatch, "/comments/"+testCommentID, strings.NewReader(`{"content":"edited"}`)))
			r.SetPathValue("id", testCommentID)
			w := httptest.NewRecorder()
			h.UpdateComment(w, r)
			assertStatus(t, w, tc.wantCode)
			assertErrorMessage(t, w, tc.wantMsg)
		})
	}
}
