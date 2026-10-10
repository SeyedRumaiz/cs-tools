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
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/middleware"
)

// entityCommentClient abstracts the entity service's generic comment
// operations — edit and soft-delete apply to a comment by id regardless of
// which aggregate (case, change request, incident, ...) it was created
// under, so there is one client interface and one handler for both, rather
// than one per aggregate. GET /comments/{id}/history also exists on the
// entity service but deliberately has no BFF pass-through here — see
// CommentHandler's doc comment.
//
// entity-service performs no author/role check of its own on any of these
// three calls (see its own comment_service.go, GetComment's doc comment, for
// why) -- this handler is the only place that decision is made. GetComment is
// called first, purely to learn the comment's author, before UpdateComment/
// DeleteComment ever runs.
type entityCommentClient interface {
	GetComment(ctx context.Context, id string) ([]byte, error)
	UpdateComment(ctx context.Context, id string, body []byte) ([]byte, error)
	DeleteComment(ctx context.Context, id string) ([]byte, error)
}

// commentAuthorView is the sliver of entity-service's Comment schema
// UpdateComment/DeleteComment need from a GetComment call: just enough to
// learn who authored it. Deliberately not the full comment shape (content,
// timestamps, ...) this handler has no other use for.
type commentAuthorView struct {
	CreatedBy *struct {
		Email string `json:"email"`
	} `json:"createdBy"`
}

// CommentHandler handles HTTP requests for the generic comment resource:
// PATCH /comments/{id} and DELETE /comments/{id}. Comments are created and
// searched through per-aggregate routes (e.g. POST /cases/{id}/comments),
// but once a comment exists its id is enough to edit or delete it, so those
// two operations are exposed here once instead of duplicated per aggregate.
//
// GET /comments/{id}/history exists on the entity service (same lack of its
// own authorization, same reasoning) but is intentionally not exposed here:
// there is no UI consumer yet (a dedicated history-viewer UI was deferred),
// and adding an unscoped pass-through for it would need the same
// author-or-PermUpdateDeleteAnyComment gate UpdateComment/DeleteComment
// apply, which has no reason to be built before there is a caller for it.
type CommentHandler struct {
	entity entityCommentClient
	// access backs the in-handler author-or-PermUpdateDeleteAnyComment check
	// in authorizeCommentActor -- see WithAccessGuard. nil fails that check
	// closed (an author-only caller still succeeds on their own comment; a
	// non-author never does), the same "nil denies, never grants" posture
	// CaseHandler.access documents.
	access *AccessGuard
}

// NewCommentHandler creates a CommentHandler backed by the given entity client.
func NewCommentHandler(entity entityCommentClient) *CommentHandler {
	return &CommentHandler{entity: entity}
}

// WithAccessGuard wires the same guard that authorises every route into this
// handler, so UpdateComment/DeleteComment can decide whether to tell
// entity-service the caller may act on any comment, not just one of their
// own — see PermUpdateDeleteAnyComment's own doc comment. Returns h for
// chaining at the construction site.
func (h *CommentHandler) WithAccessGuard(g *AccessGuard) *CommentHandler {
	h.access = g
	return h
}

// authorizeCommentActor is UpdateComment/DeleteComment's shared gate: fetches
// the comment (purely to learn its author -- entity-service itself no longer
// checks this) and allows the caller through when they either authored it or
// hold PermUpdateDeleteAnyComment (admin/comment_updater). Returns ok=false
// after already writing the right error response (the GetComment error
// mapped through mapUpstreamError, or a 403) -- the caller just returns.
func (h *CommentHandler) authorizeCommentActor(w http.ResponseWriter, r *http.Request, user *middleware.UserInfo, id string) (ok bool) {
	raw, err := h.entity.GetComment(r.Context(), id)
	if err != nil {
		slog.ErrorContext(r.Context(), "entity GetComment failed", "userID", user.UserID, "id", id, "err", err)
		mapUpstreamError(w, err, "Failed to load comment.")
		return false
	}

	var comment commentAuthorView
	if err := json.Unmarshal(raw, &comment); err != nil {
		slog.ErrorContext(r.Context(), "entity GetComment returned unparsable body", "userID", user.UserID, "id", id, "err", err)
		writeError(w, http.StatusInternalServerError, ErrMsgInternal)
		return false
	}

	isAuthor := comment.CreatedBy != nil && user.Email != "" && strings.EqualFold(comment.CreatedBy.Email, user.Email)
	canActOnAny := h.access != nil && h.access.Permits(PermUpdateDeleteAnyComment, user.Roles)
	if !isAuthor && !canActOnAny {
		writeError(w, http.StatusForbidden, ErrMsgForbidden)
		return false
	}
	return true
}

// UpdateComment handles PATCH /comments/{id} with body {"content": "..."}.
//
// Uses mapUpstreamError, not mapUpstreamErrorGeneric, for the entity
// UpdateComment call itself: the upstream 400 ("a deleted comment cannot be
// edited") is a rejection of the edit the caller just attempted, the same
// reasoning that puts the ten existing PATCH/update handlers on
// mapUpstreamError (see backend CLAUDE.md's Handler conventions) — this is
// simply an eleventh.
func (h *CommentHandler) UpdateComment(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserInfoFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, ErrMsgUnauthorized)
		return
	}

	id := r.PathValue("id")
	if id == "" || !uuidRe.MatchString(id) {
		writeError(w, http.StatusBadRequest, ErrMsgInvalidUUID)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxCommentBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeError(w, http.StatusRequestEntityTooLarge, ErrMsgTooLarge)
			return
		}
		writeError(w, http.StatusBadRequest, errMsgReadBody)
		return
	}

	if !json.Valid(body) {
		writeError(w, http.StatusBadRequest, ErrMsgBadRequest)
		return
	}

	if !h.authorizeCommentActor(w, r, user, id) {
		return
	}

	result, err := h.entity.UpdateComment(r.Context(), id, body)
	if err != nil {
		slog.ErrorContext(r.Context(), "entity UpdateComment failed", "userID", user.UserID, "id", id, "err", err)
		mapUpstreamError(w, err, "Failed to update comment.")
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// DeleteComment handles DELETE /comments/{id}. Soft delete only — content is
// never destroyed. Carries no body, but still uses mapUpstreamError rather
// than mapUpstreamErrorGeneric: the upstream 409 ("comment is already
// deleted") is a rejection of the delete action the caller just took on this
// specific comment, not an unvalidated payload — the same reasoning as
// UpdateComment above, just triggered by path state instead of body content.
// The entity service returns 204 No Content on success, forwarded as-is with
// no body (same pattern as CaseHandler.RemoveCaseTag).
func (h *CommentHandler) DeleteComment(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserInfoFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, ErrMsgUnauthorized)
		return
	}

	id := r.PathValue("id")
	if id == "" || !uuidRe.MatchString(id) {
		writeError(w, http.StatusBadRequest, ErrMsgInvalidUUID)
		return
	}

	if !h.authorizeCommentActor(w, r, user, id) {
		return
	}

	if _, err := h.entity.DeleteComment(r.Context(), id); err != nil {
		slog.ErrorContext(r.Context(), "entity DeleteComment failed", "userID", user.UserID, "id", id, "err", err)
		mapUpstreamError(w, err, "Failed to delete comment.")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
