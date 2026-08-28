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

// Package routingclient is the outbound HTTP client this backend uses to
// call the standalone chat-routing-service (apps/chat-routing-service/
// backend) — the in-memory engineer availability/queue state machine that
// decides which engineer (if any) an escalation is routed to. See that
// service's internal/handler/routes.go for the exact routes and response
// shapes this client mirrors.
//
// Like internal/chatnotify, this is a server-to-server call authenticated
// with a shared bearer secret (X-Routing-Service-Token) rather than a user
// JWT, since the two processes do not share a JWT audience/issuer for a
// "service identity". Unlike chatnotify's fire-and-forget PushEvent, every
// call here has a typed response the caller acts on synchronously (e.g. an
// escalation needs to know which engineer, if any, to publish the alert
// to) — so this client encodes/decodes JSON rather than passing raw bytes.
package routingclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

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
	// Must equal that service's own ROUTING_SERVICE_TOKEN.
	InternalToken string
}

// Client calls the chat-routing-service.
type Client struct {
	http    *http.Client
	baseURL string
	token   string
}

// NewClient constructs a Client. Does not validate connectivity — the first
// call surfaces a dial failure; callers decide per-endpoint whether that is
// fatal to the caller-facing request (HandleEscalate falls back to
// broadcasting; presence/completed/decline calls are best-effort, matching
// this feature's existing "a failed side-channel push never blocks the
// request that already succeeded" philosophy — see chatnotify.NewClient).
func NewClient(cfg Config) *Client {
	return &Client{
		http:    &http.Client{Timeout: 10 * time.Second},
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
		token:   cfg.InternalToken,
	}
}

// Status mirrors chat-routing-service/backend/internal/router.Status.
// Duplicated rather than shared via a common module: these are two
// independently deployable services, and this HTTP API is their only
// coupling point.
type Status string

const (
	StatusAvailable Status = "AVAILABLE"
	StatusBusy      Status = "BUSY"
	StatusOffline   Status = "OFFLINE"
)

// CaseInfo mirrors router.CaseInfo — the case fields carried through an
// escalation, queueing, or reassignment. Field names and JSON tags match
// that service's escalateRequest/CaseInfo wire shape exactly, so a CaseInfo
// value can be marshaled directly as the POST /route/escalate body.
type CaseInfo struct {
	CaseID         string `json:"caseId"`
	ConversationID string `json:"conversationId"`
	ProjectID      string `json:"projectId,omitempty"`
	Subject        string `json:"subject,omitempty"`
	CustomerEmail  string `json:"customerEmail,omitempty"`
	CustomerName   string `json:"customerName,omitempty"`
	Message        string `json:"message,omitempty"`
}

// EscalateResult mirrors router.EscalateResult.
type EscalateResult struct {
	EngineerEmail string `json:"engineerEmail,omitempty"`
	Queued        bool   `json:"queued,omitempty"`
	Position      int    `json:"position,omitempty"`
}

// PresenceResult mirrors router.PresenceResult.
type PresenceResult struct {
	Applied        bool      `json:"applied"`
	PendingOffline bool      `json:"pendingOffline,omitempty"`
	AssignedCase   *CaseInfo `json:"assignedCase,omitempty"`
}

// CompletedResult mirrors router.CompletedResult.
type CompletedResult struct {
	Removed      bool      `json:"removed,omitempty"`
	Rejoined     bool      `json:"rejoined,omitempty"`
	AssignedCase *CaseInfo `json:"assignedCase,omitempty"`
}

// DeclineResult mirrors router.DeclineResult.
type DeclineResult struct {
	ReassignedTo string `json:"reassignedTo,omitempty"`
	Requeued     bool   `json:"requeued,omitempty"`
	// AssignedCase is set alongside ReassignedTo — the declined case, now
	// handed to that other engineer, in the same shape HandleEscalate would
	// have delivered it in originally.
	AssignedCase *CaseInfo `json:"assignedCase,omitempty"`
}

// do is the shared request/response plumbing for every method below: encode
// reqBody (if any) as the JSON request body, attach the shared-secret
// header, and on a 2xx response decode into out (if any). Any non-2xx
// response or transport failure is returned as an error; the routing
// service never uses redirects, so the 2xx check does not follow any.
func (c *Client) do(ctx context.Context, method, path string, reqBody, out any) error {
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
	err := c.do(ctx, http.MethodPost, "/route/escalate", ci, &out)
	return out, err
}

// SetPresence calls POST /route/presence, applying an engineer's requested
// status change (see router.Router.SetPresence's doc comment for the full
// state machine this triggers).
func (c *Client) SetPresence(ctx context.Context, email string, status Status) (PresenceResult, error) {
	var out PresenceResult
	body := struct {
		Email  string `json:"email"`
		Status Status `json:"status"`
	}{Email: email, Status: status}
	err := c.do(ctx, http.MethodPost, "/route/presence", body, &out)
	return out, err
}

// Completed calls POST /route/completed, reporting that email just ended
// their current session.
func (c *Client) Completed(ctx context.Context, email string) (CompletedResult, error) {
	var out CompletedResult
	body := struct {
		Email string `json:"email"`
	}{Email: email}
	err := c.do(ctx, http.MethodPost, "/route/completed", body, &out)
	return out, err
}

// Decline calls POST /route/decline, reporting that email is declining the
// case identified by caseID before accepting it.
func (c *Client) Decline(ctx context.Context, email, caseID string) (DeclineResult, error) {
	var out DeclineResult
	body := struct {
		Email  string `json:"email"`
		CaseID string `json:"caseId"`
	}{Email: email, CaseID: caseID}
	err := c.do(ctx, http.MethodPost, "/route/decline", body, &out)
	return out, err
}

// GetPresence calls GET /route/presence/{email}, returning StatusOffline
// for an engineer the routing service has never seen a presence update
// from (see router.Router.GetPresence).
func (c *Client) GetPresence(ctx context.Context, email string) (Status, error) {
	var out struct {
		Status Status `json:"status"`
	}
	err := c.do(ctx, http.MethodGet, "/route/presence/"+url.PathEscape(email), nil, &out)
	return out.Status, err
}
