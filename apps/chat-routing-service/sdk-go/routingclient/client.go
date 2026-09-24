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

// Package routingclient is the client SDK for chat-routing-service
// (apps/chat-routing-service/backend) — the engineer availability/queue
// state machine that decides which engineer (if any) an escalation is
// routed to. See that service's internal/handler/routes.go for the exact
// routes and response shapes this client mirrors.
//
// # Server-to-server only
//
// Every call here is authenticated with a shared bearer secret
// (X-Routing-Service-Token) rather than a user JWT — chat-routing-service
// and its callers do not share a JWT audience/issuer for a "service
// identity", so this is a static, pre-shared secret instead. That makes
// this client safe to use only from a backend process that can hold a
// secret: never construct a Client in code that ships to a browser or any
// other untrusted runtime, and never proxy this token through to one. A
// frontend that needs routing decisions should keep calling its own
// backend's proxy endpoints (see apps/csm-portal/backend/internal/handler/
// chat.go's HandleEscalate/HandleSetPresence/etc.), which hold this
// Client server-side and never expose InternalToken to the browser.
//
// # Versioning
//
// This module is nested inside the wso2-open-operations/cs-tools monorepo
// at apps/chat-routing-service/sdk-go and is versioned independently of
// both chat-routing-service/backend and any of its callers, via git tags
// scoped to this directory (e.g. apps/chat-routing-service/sdk-go/v0.1.0).
// A breaking change to chat-routing-service's HTTP API should land here as
// a new type/method (or a major version bump) rather than a silent
// behavior change to an existing one, since callers pin a version like any
// other Go dependency.
package routingclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ErrConversationEnded is returned by AddComment when chat-routing-service
// reports (via 410 Gone) that the case's session already ended -- mirrors
// router.ErrConversationEnded on the server side. 410 is reserved across
// this whole client for exactly this meaning; do's own status-to-error
// mapping below is what makes errors.Is(err, ErrConversationEnded) work
// regardless of which endpoint returned it.
var ErrConversationEnded = errors.New("chat session has already ended for this case")

// ErrCaseAlreadyEnded is returned by Escalate when chat-routing-service
// reports (via 409 Conflict) that this caseId's chat_conversation has
// already ended -- mirrors router.ErrCaseAlreadyEnded on the server side.
// See that error's own doc comment for why Escalate rejects this instead
// of creating a zombie queue row.
var ErrCaseAlreadyEnded = errors.New("this case has already ended and cannot be escalated again")

// ErrDuplicateOpenChat is returned by CreateWorkItem when chat-routing-
// service reports (via 409 Conflict) that this customerEmail + projectId
// already has another, non-ended live chat in progress -- mirrors
// router.ErrDuplicateOpenChat on the server side.
var ErrDuplicateOpenChat = errors.New("this customer already has an open live chat for this project")

// internalTokenHeader must match chat-routing-service/backend's own
// internal/middleware.InternalTokenHeader.
const internalTokenHeader = "X-Routing-Service-Token"

// maxResponseBodyBytes bounds how much of an error response this client will
// read into memory/log.
const maxResponseBodyBytes = 64 << 10 // 64 KiB

// Config holds the configuration for the chat-routing-service client.
type Config struct {
	// BaseURL is the chat-routing-service's listener base URL, e.g.
	// "http://localhost:9096" (ROUTING_SERVICE_BASE_URL).
	BaseURL string
	// InternalToken is the shared secret sent as X-Routing-Service-Token.
	// Must equal that service's own ROUTING_SERVICE_TOKEN. Load this from
	// your own process's environment/secret store — never hardcode it,
	// and never let it reach client-side code (see the package doc above).
	InternalToken string
}

// Client calls the chat-routing-service. Safe for concurrent use by
// multiple goroutines (holds no mutable state beyond its *http.Client).
type Client struct {
	http    *http.Client
	baseURL string
	token   string
}

// NewClient constructs a Client. Does not validate connectivity — the first
// call surfaces a dial failure; callers decide per-endpoint whether that is
// fatal to the caller-facing request (chat-routing-service's callers
// typically fall back to a degraded default for user-facing calls like
// Escalate, and log-and-continue for best-effort side-channel calls like
// Completed/Decline — see apps/csm-portal/backend/internal/handler/chat.go
// for a worked example of both).
func NewClient(cfg Config) *Client {
	return &Client{
		http:    &http.Client{Timeout: 10 * time.Second},
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
		token:   cfg.InternalToken,
	}
}

// Status mirrors chat-routing-service/backend/internal/router.Status — a
// plain three-way manual toggle, independent of how many cases an engineer
// is actually holding. Duplicated rather than shared via a common package:
// chat-routing-service and this SDK are versioned and deployed
// independently, and this HTTP API is their only coupling point.
//
// Never PENDING: "pending" (assigned, not yet accepted) is a per-case fact
// now, not a top-level engineer status — see CaseStatus.Pending. Removed
// alongside the 2026-09-10 concurrent-chat-capacity change (see the
// project's db-schema-review-2026-09-07-outcomes.md); a caller still
// sending "PENDING" to SetPresence gets a 400 from that endpoint, same as
// it always has for any other invalid value.
type Status string

const (
	StatusAvailable Status = "AVAILABLE"
	// StatusBusy is a manual do-not-disturb toggle: takes no new
	// assignments, but does not affect cases already held.
	StatusBusy    Status = "BUSY"
	StatusOffline Status = "OFFLINE"
)

// CaseInfo mirrors router.CaseInfo — the case fields carried through an
// escalation, queueing, or reassignment. Field names and JSON tags match
// that service's escalateRequest/CaseInfo wire shape exactly, so a CaseInfo
// value can be marshaled directly as the POST /route/escalate or
// POST /route/workitem body.
type CaseInfo struct {
	CaseID         string `json:"caseId"`
	ConversationID string `json:"conversationId"`
	ProjectID      string `json:"projectId,omitempty"`
	// Source/Channel identify which product/surface raised this case (e.g.
	// "customer-portal"/"" for the existing Novera "Chat with an Engineer"
	// flow, "asgardeo"/"ask-ai" for an escalation raised from identity-apps'
	// Ask AI panel via console-chat-bridge). Persisted as-is in
	// chat_conversation.case_info (existing jsonb column, no migration) and
	// echoed back by GetCaseInfo so csm-portal/backend can route its
	// downstream push to the right service. Optional -- an empty Source
	// means "customer-portal", the only source that existed before this
	// field did.
	Source  string `json:"source,omitempty"`
	Channel string `json:"channel,omitempty"`
	Subject string `json:"subject,omitempty"`
	CustomerEmail  string `json:"customerEmail,omitempty"`
	CustomerName   string `json:"customerName,omitempty"`
	Message        string `json:"message,omitempty"`
	// PriorMessages mirrors router.CaseInfo.PriorMessages -- the customer's
	// AI-chatbot (Novera) conversation history, snapshotted at the moment
	// of escalation. See PriorMessage below.
	PriorMessages []PriorMessage `json:"priorMessages,omitempty"`
}

// PriorMessageRole mirrors router.PriorMessageRole -- who sent one
// PriorMessage, the customer or the Novera AI assistant. Never an engineer:
// a pre-escalation transcript predates any engineer being involved.
type PriorMessageRole string

const (
	PriorMessageRoleCustomer  PriorMessageRole = "customer"
	PriorMessageRoleAssistant PriorMessageRole = "assistant"
)

// PriorMessage mirrors router.PriorMessage -- one message from the
// customer's AI-chatbot (Novera) conversation that happened before an
// escalation, supplied by the caller (customer-portal/backend-v2, from the
// frontend's own visible conversation) and persisted into
// chat_routing.comment by CreateWorkItem. Duplicated rather than shared,
// same as every other type in this file (see this file's own package doc
// comment). CreatedAt is RFC 3339, optional.
type PriorMessage struct {
	Role      PriorMessageRole `json:"role"`
	Content   string           `json:"content"`
	CreatedAt string           `json:"createdAt,omitempty"`
}

// CaseStatus mirrors router.CaseStatus — one case an engineer currently
// holds, as reported by GetPresence. Replaces the old single
// PresenceDetail.CurrentCase/PendingSince pair now that an engineer can
// hold more than one case at once.
type CaseStatus struct {
	CaseInfo
	// Pending is true until Accept confirms this specific case.
	Pending bool `json:"pending"`
	// AssignedAt is when this case was assigned to this engineer (not when
	// created) — the countdown to the timeout sweep runs from here.
	AssignedAt string `json:"assignedAt"`
}

// EscalateResult mirrors router.EscalateResult.
type EscalateResult struct {
	// EngineerUserID is the IdP "userid" claim of the engineer this case was
	// assigned to (see chat-routing-service's migrations/
	// 000014_rename_engineer_status_table for why this is a user ID rather
	// than an email) -- empty when Queued.
	EngineerUserID string `json:"engineerId,omitempty"`
	Queued         bool   `json:"queued,omitempty"`
	Position       int    `json:"position,omitempty"`
}

// PresenceResult mirrors router.PresenceResult.
type PresenceResult struct {
	Applied bool `json:"applied"`
	// AssignedCases is set when this presence change immediately drained
	// the queue -- transitioning to AVAILABLE claims cases off the queue
	// until either it's empty or the engineer's own capacity is full, so
	// (unlike Completed/Decline/a timeout, which each free at most one
	// slot) more than one case can land here at once.
	AssignedCases []CaseInfo `json:"assignedCases,omitempty"`
}

// CompletedResult mirrors router.CompletedResult.
type CompletedResult struct {
	// Ended is true when the given case was actually an open (not
	// already-ended) conversation assigned to the caller -- false is a
	// no-op, guarding against a duplicate call for a session that already
	// ended.
	Ended bool `json:"ended,omitempty"`
	// AssignedCase is set when ending this conversation freed a slot that
	// was immediately backfilled from the waiting queue.
	AssignedCase *CaseInfo `json:"assignedCase,omitempty"`
}

// DeclineResult mirrors router.DeclineResult.
type DeclineResult struct {
	// ReassignedTo is the user ID of the engineer the case was handed to
	// instead.
	ReassignedTo string `json:"reassignedTo,omitempty"`
	Requeued     bool   `json:"requeued,omitempty"`
	// AssignedCase is set alongside ReassignedTo — the declined case, now
	// handed to that other engineer, in the same shape Escalate would
	// have delivered it in originally.
	AssignedCase *CaseInfo `json:"assignedCase,omitempty"`
}

// AcceptResult mirrors router.AcceptResult.
type AcceptResult struct {
	Applied bool `json:"applied"`
}

// TimeoutResult mirrors router.TimeoutResult -- one conversation's outcome
// from a call to SweepTimeouts: it had been assigned to an engineer and
// never accepted within chat-routing-service's own configured
// PENDING_TIMEOUT_SECONDS, so that service reassigned or requeued it.
// Unlike before the 2026-09-10 concurrent-chat-capacity change, the
// unresponsive engineer's chat_status is left untouched -- they may well
// be mid-conversation on a different concurrent case at the same time (see
// router.Router.SweepExpiredPending's own doc comment).
type TimeoutResult struct {
	UserID       string    `json:"userId"`
	CaseID       string    `json:"caseId"`
	ReassignedTo string    `json:"reassignedTo,omitempty"`
	Requeued     bool      `json:"requeued,omitempty"`
	AssignedCase *CaseInfo `json:"assignedCase,omitempty"`
}

// do is the shared request/response plumbing for every method below: encode
// reqBody (if any) as the JSON request body, attach the shared-secret
// header, and on a 2xx response decode into out (if any). Any non-2xx
// response or transport failure is returned as an error; the routing
// service never uses redirects, so the 2xx check does not follow any.
//
// conflictErr, when non-nil, is what a 409 response from this specific
// call is translated to (wrapped, so errors.Is(err, conflictErr) works) --
// different endpoints mean different things by 409 (Escalate: the case
// already ended; CreateWorkItem: a duplicate open chat), so this is a
// per-call choice rather than a single client-wide mapping the way 410 is.
// Pass nil for any call that has no 409 meaning of its own.
func (c *Client) do(ctx context.Context, method, path string, reqBody, out any, conflictErr error) error {
	var bodyReader io.Reader
	if reqBody != nil {
		b, err := json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("routingclient: encode request: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return fmt.Errorf("routingclient: build request: %w", err)
	}
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set(internalTokenHeader, c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("routingclient: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes))
		switch {
		case resp.StatusCode == http.StatusGone:
			return fmt.Errorf("routingclient: %s %s: %w: %s", method, path, ErrConversationEnded, string(body))
		case resp.StatusCode == http.StatusConflict && conflictErr != nil:
			return fmt.Errorf("routingclient: %s %s: %w: %s", method, path, conflictErr, string(body))
		}
		return fmt.Errorf("routingclient: %s %s: upstream returned %d: %s", method, path, resp.StatusCode, string(body))
	}

	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("routingclient: %s %s: decode response: %w", method, path, err)
	}
	return nil
}

// Escalate calls POST /route/escalate, asking the routing service to assign
// ci to an available engineer or queue it.
func (c *Client) Escalate(ctx context.Context, ci CaseInfo) (EscalateResult, error) {
	var out EscalateResult
	err := c.do(ctx, http.MethodPost, "/route/escalate", ci, &out, ErrCaseAlreadyEnded)
	return out, err
}

// SetPresence calls POST /route/presence, applying an engineer's requested
// status change (see router.Router.SetPresence's doc comment for the full
// state machine this triggers). userID is the caller's stable per-account
// identifier (e.g. an IdP "userid" claim) -- the routing service's
// cs_engineer_status table is keyed by it directly, so this is the only
// identifier this call needs, on both a first-contact and a later call for
// the same engineer.
func (c *Client) SetPresence(ctx context.Context, userID string, status Status) (PresenceResult, error) {
	var out PresenceResult
	body := struct {
		UserID string `json:"userId"`
		Status Status `json:"status"`
	}{UserID: userID, Status: status}
	err := c.do(ctx, http.MethodPost, "/route/presence", body, &out, nil)
	return out, err
}

// Completed calls POST /route/completed, reporting that userID just ended
// their session on caseID -- one of possibly several concurrent cases they
// hold. A no-op (Ended: false) if caseID isn't currently an open
// conversation assigned to userID.
func (c *Client) Completed(ctx context.Context, userID, caseID string) (CompletedResult, error) {
	var out CompletedResult
	body := struct {
		UserID string `json:"userId"`
		CaseID string `json:"caseId"`
	}{UserID: userID, CaseID: caseID}
	err := c.do(ctx, http.MethodPost, "/route/completed", body, &out, nil)
	return out, err
}

// Decline calls POST /route/decline, reporting that userID is declining the
// case identified by caseID before accepting it.
func (c *Client) Decline(ctx context.Context, userID, caseID string) (DeclineResult, error) {
	var out DeclineResult
	body := struct {
		UserID string `json:"userId"`
		CaseID string `json:"caseId"`
	}{UserID: userID, CaseID: caseID}
	err := c.do(ctx, http.MethodPost, "/route/decline", body, &out, nil)
	return out, err
}

// Accept calls POST /route/accept, confirming userID is accepting the case
// (caseID) they were assigned -- moves that one conversation from OPEN to
// ACTIVE server-side; any other concurrent case userID holds is untouched
// either way. See router.Router.Accept's own doc comment for when Applied
// comes back false (a stale accept) rather than an error.
func (c *Client) Accept(ctx context.Context, userID, caseID string) (AcceptResult, error) {
	var out AcceptResult
	body := struct {
		UserID string `json:"userId"`
		CaseID string `json:"caseId"`
	}{UserID: userID, CaseID: caseID}
	err := c.do(ctx, http.MethodPost, "/route/accept", body, &out, nil)
	return out, err
}

// CreateWorkItem calls POST /route/workitem -- LOCAL STAND-IN persistence,
// see chat-routing-service's internal/router/workitem.go package doc
// comment. Creates the work_item + chat_conversation pair (plus the first
// comment, if ci.Message is non-empty) for a brand-new escalation, and
// durably stores ci itself as the case's display blob (see that method's
// own doc comment on why this now outlives Accept).
//
// Must be called BEFORE Escalate for the same case -- Escalate's own
// assignment now writes chat_conversation.assignee_id directly, so this
// row must already exist by the time Escalate runs (see router.Router.
// CreateWorkItem's doc comment). ci.CustomerEmail attributes the work item
// to the CUSTOMER who escalated, not an engineer.
func (c *Client) CreateWorkItem(ctx context.Context, ci CaseInfo) error {
	return c.do(ctx, http.MethodPost, "/route/workitem", ci, nil, ErrDuplicateOpenChat)
}

// AddComment calls POST /route/comment -- LOCAL STAND-IN persistence, same
// caveat as CreateWorkItem above. Used for both directions of a live chat
// message (customer and engineer) so the whole transcript lands in one
// place. authorEmail is a free-text attribution column (comment.
// created_by), not an identity join -- also unaffected by the engineer
// user-ID switch.
func (c *Client) AddComment(ctx context.Context, caseID, authorEmail, content string) error {
	body := struct {
		CaseID      string `json:"caseId"`
		AuthorEmail string `json:"authorEmail"`
		Content     string `json:"content"`
	}{CaseID: caseID, AuthorEmail: authorEmail, Content: content}
	return c.do(ctx, http.MethodPost, "/route/comment", body, nil, nil)
}

// PresenceDetail is GetPresence's result -- the engineer's manual
// chat_status, their concurrent-chat capacity and current load, and every
// case they're currently holding (pending or accepted alike) so a caller
// whose own UI state was lost can rehydrate all of it, instead of leaving
// the engineer stuck with nothing to act on. Replaces the old single
// Status/CurrentCase/PendingSince shape now that an engineer can hold more
// than one case at once (see the 2026-09-10 concurrent-chat-capacity
// change).
type PresenceDetail struct {
	ChatStatus         Status       `json:"chatStatus"`
	ActiveChats        int          `json:"activeChats"`
	MaxConcurrentChats int          `json:"maxConcurrentChats"`
	AtCapacity         bool         `json:"atCapacity"`
	Cases              []CaseStatus `json:"cases,omitempty"`
	// PendingTimeoutSeconds is chat-routing-service's own configured
	// PENDING_TIMEOUT_SECONDS -- always present, a constant rather than
	// per-engineer state.
	PendingTimeoutSeconds int `json:"pendingTimeoutSeconds"`
}

// GetPresence calls GET /route/presence/{userId}, returning StatusOffline
// (and no cases) for an engineer the routing service has never seen a
// presence update from (see router.Router.GetPresence).
func (c *Client) GetPresence(ctx context.Context, userID string) (PresenceDetail, error) {
	var out PresenceDetail
	err := c.do(ctx, http.MethodGet, "/route/presence/"+url.PathEscape(userID), nil, &out, nil)
	if err != nil {
		return PresenceDetail{}, err
	}
	if out.ChatStatus == "" {
		out.ChatStatus = StatusOffline
	}
	return out, nil
}

// AbandonedResult mirrors router.AbandonedResult -- one case SweepTimeouts
// gave up on because it sat WAITING_FOR_ENGINEER (never assigned to anyone
// at all) past chat-routing-service's own configured QUEUE_ABANDON_SECONDS.
// Distinct from TimeoutResult, which covers a case that WAS assigned to a
// specific engineer and never confirmed -- see router.Router.
// SweepAbandonedQueue's own doc comment for why this second, longer-fused
// sweep exists: without it, an old queued escalation nobody was ever free
// to take could sit forever and later be silently claimed by whichever
// engineer next went AVAILABLE.
type AbandonedResult struct {
	CaseID         string `json:"caseId"`
	ConversationID string `json:"conversationId"`
}

// SweepResult is SweepTimeouts's result: every case reassigned/requeued
// for a PENDING-accept timeout, plus every case abandoned for having
// waited too long with nobody ever free to take it.
type SweepResult struct {
	Timeouts  []TimeoutResult   `json:"results"`
	Abandoned []AbandonedResult `json:"abandoned"`
}

// SweepTimeouts calls POST /route/sweep-timeouts, asking the routing
// service to (a) reassign or requeue any case whose assigned engineer has
// been PENDING (assigned, not yet accepted) past that service's own
// configured PENDING_TIMEOUT_SECONDS, and (b) give up on any case that's
// been waiting in the queue with nobody ever free to take it past
// QUEUE_ABANDON_SECONDS (see AbandonedResult). Meant to be polled
// periodically by whichever caller owns delivering the result to the
// newly-assigned engineer (see apps/csm-portal/backend/internal/handler's
// ChatHandler.StartTimeoutSweeper) -- this service never pushes anything
// itself, see this package's own "Synchronous HTTP only" design note
// above.
func (c *Client) SweepTimeouts(ctx context.Context) (SweepResult, error) {
	var out SweepResult
	err := c.do(ctx, http.MethodPost, "/route/sweep-timeouts", nil, &out, nil)
	return out, err
}

// SetCapacityResult mirrors router.SetCapacityResult.
type SetCapacityResult struct {
	// AssignedCases is set when raising the limit immediately drained the
	// waiting queue into this engineer's newly-opened capacity -- same
	// queue-drain semantics as PresenceResult.AssignedCases (more than one
	// case can land here at once). Deliver each one to the engineer the
	// same way a fresh escalation would be.
	AssignedCases []CaseInfo `json:"assignedCases,omitempty"`
}

// SetMaxConcurrentChats calls PATCH /route/capacity, setting userID's
// configurable concurrent-chat capacity (see router.Router.
// SetMaxConcurrentChats). max must be between 1 and 10 inclusive -- an
// out-of-range value gets a 400 from that endpoint, surfaced here as a
// plain error. Raising the limit while this engineer is AVAILABLE can
// immediately drain the waiting queue into the newly-opened capacity (see
// SetCapacityResult.AssignedCases) -- callers must deliver each one to the
// engineer, the same way HandleSetPresence already does for its own
// queue-drain (see apps/csm-portal/backend/internal/handler/chat.go's
// HandleSetMaxConcurrentChats).
func (c *Client) SetMaxConcurrentChats(ctx context.Context, userID string, max int) (SetCapacityResult, error) {
	var out SetCapacityResult
	body := struct {
		UserID             string `json:"userId"`
		MaxConcurrentChats int    `json:"maxConcurrentChats"`
	}{UserID: userID, MaxConcurrentChats: max}
	err := c.do(ctx, http.MethodPatch, "/route/capacity", body, &out, nil)
	return out, err
}

// GetCaseInfo calls POST /route/workitem/{caseId}/info, returning caseID's
// originally-submitted CaseInfo as CreateWorkItem stored it.
func (c *Client) GetCaseInfo(ctx context.Context, caseID string) (CaseInfo, error) {
	var out CaseInfo
	err := c.do(ctx, http.MethodPost, "/route/workitem/"+url.PathEscape(caseID)+"/info", nil, &out, nil)
	return out, err
}

// ConvertToCaseResult mirrors router.ConvertToCaseResult.
type ConvertToCaseResult struct {
	// AssignedCase is set when converting freed a slot that was immediately
	// backfilled from the waiting queue.
	AssignedCase *CaseInfo `json:"assignedCase,omitempty"`
}

// ConvertToCase calls POST /route/convert-to-case, ending caseID's chat
// session and recording entityCaseID against it. userID must be the
// engineer currently holding caseID in an accepted (ACTIVE) session, or
// the call returns a 409.
func (c *Client) ConvertToCase(ctx context.Context, userID, caseID, entityCaseID string) (ConvertToCaseResult, error) {
	var out ConvertToCaseResult
	body := struct {
		UserID       string `json:"userId"`
		CaseID       string `json:"caseId"`
		EntityCaseID string `json:"entityCaseId"`
	}{UserID: userID, CaseID: caseID, EntityCaseID: entityCaseID}
	err := c.do(ctx, http.MethodPost, "/route/convert-to-case", body, &out, nil)
	return out, err
}
