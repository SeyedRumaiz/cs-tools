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
// KIND, either express or implied. See the License for the
// specific language governing permissions and limitations
// under the License.

package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/service"
)

// ProjectMembershipHandler handles the portal-driven membership writes under
// /projects/{id}/contacts. They sit in the same namespace as the contacts
// search and get that are already there, and key a membership by {email} —
// the way the Customer Portal addresses a contact today, and the only
// identifier a caller has before the person exists in either system.
//
// Registered only when CSM_MIGRATION_PORTAL_WRITES_ENABLED is exactly "true"
// (see routes.go); with the flag off these paths do not exist at all.
type ProjectMembershipHandler struct {
	svc service.ProjectMembershipWriteService
}

// membershipWriteDeadline is how long a membership write may take to send
// its response. The server-wide WriteTimeout (15s) is shorter than a write
// that goes through several Salesforce calls can take when Salesforce is
// slow, and when it expires the connection is closed after the write has
// committed: the caller then sees a failure for a change that happened. The
// portals' own clients stop waiting well before this, so it only has to
// outlast them.
const membershipWriteDeadline = 60 * time.Second

// extendWriteDeadline raises this response's write deadline to
// membershipWriteDeadline, leaving every other route on the server default.
// A writer that cannot reach the connection (http.ErrNotSupported) keeps the
// default; that is logged, not fatal.
func extendWriteDeadline(w http.ResponseWriter, r *http.Request) {
	err := http.NewResponseController(w).SetWriteDeadline(time.Now().Add(membershipWriteDeadline))
	if err != nil && !errors.Is(err, http.ErrNotSupported) {
		slog.WarnContext(r.Context(), "membership write: could not extend the write deadline", "err", err)
	} else if errors.Is(err, http.ErrNotSupported) {
		slog.WarnContext(r.Context(), "membership write: response writer cannot extend the write deadline")
	}
}

// NewProjectMembershipHandler constructs a ProjectMembershipHandler.
func NewProjectMembershipHandler(svc service.ProjectMembershipWriteService) *ProjectMembershipHandler {
	return &ProjectMembershipHandler{svc: svc}
}

// InviteProjectContact handles POST /projects/{id}/contacts.
func (h *ProjectMembershipHandler) InviteProjectContact(w http.ResponseWriter, r *http.Request) {
	extendWriteDeadline(w, r)
	var req domain.CreateProjectMembershipRequest
	if !decodeRequest(w, r, &req) {
		return
	}
	resp, err := h.svc.Invite(r.Context(), r.PathValue("id"), req)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(resp)
}

// ValidateProjectContact handles POST /projects/{id}/contacts/validate, the
// invitation's dry run. It answers 200 whether or not the invitation would
// be allowed; the body's valid flag says which. Error statuses mean the check
// itself failed.
func (h *ProjectMembershipHandler) ValidateProjectContact(w http.ResponseWriter, r *http.Request) {
	// Several Salesforce reads, like the writes: the same deadline applies.
	extendWriteDeadline(w, r)
	var req domain.ValidateProjectMembershipRequest
	if !decodeRequest(w, r, &req) {
		return
	}
	resp, err := h.svc.ValidateInvitation(r.Context(), r.PathValue("id"), req)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// UpdateProjectContactRoles handles PATCH /projects/{id}/contacts/{email}.
func (h *ProjectMembershipHandler) UpdateProjectContactRoles(w http.ResponseWriter, r *http.Request) {
	extendWriteDeadline(w, r)
	var req domain.UpdateProjectMembershipRolesRequest
	if !decodeRequest(w, r, &req) {
		return
	}
	resp, err := h.svc.UpdateRoles(r.Context(), r.PathValue("id"), pathEmail(r), req)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// DeactivateProjectContact handles DELETE /projects/{id}/contacts/{email}.
// The membership is deactivated, never deleted — see the service's own doc
// comment.
func (h *ProjectMembershipHandler) DeactivateProjectContact(w http.ResponseWriter, r *http.Request) {
	extendWriteDeadline(w, r)
	if err := h.svc.Deactivate(r.Context(), r.PathValue("id"), pathEmail(r)); err != nil {
		writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ResendProjectContactInvitation handles
// POST /projects/{id}/contacts/{email}/resend-invitation.
func (h *ProjectMembershipHandler) ResendProjectContactInvitation(w http.ResponseWriter, r *http.Request) {
	extendWriteDeadline(w, r)
	if err := h.svc.ResendInvitation(r.Context(), r.PathValue("id"), pathEmail(r)); err != nil {
		writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// pathEmail reads the {email} path segment.
//
// It deliberately does NOT call url.PathUnescape: net/http's own ServeMux
// already URL-decodes path variables before handing them to the handler
// (`jane%40acme.com` arrives as `jane@acme.com`, verified), so a second
// decode would corrupt any address carrying a literal percent sign. The
// service normalizes and validates whatever comes out of here.
func pathEmail(r *http.Request) string {
	return r.PathValue("email")
}
