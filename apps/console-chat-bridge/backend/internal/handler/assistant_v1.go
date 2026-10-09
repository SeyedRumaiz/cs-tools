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
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/wso2-open-operations/cs-tools/apps/console-chat-bridge/backend/internal/assistant"
	"github.com/wso2-open-operations/cs-tools/apps/console-chat-bridge/backend/internal/stream"
	"github.com/wso2-open-operations/cs-tools/apps/console-chat-bridge/backend/internal/tenant"
)

// assistantAnswerTimeout bounds one answer, tools included.
const assistantAnswerTimeout = 5 * time.Minute

// assistantStreamBuffer is how many answer events a slow browser
// connection may fall behind by before it misses some. Answers arrive in
// many small pieces; the final "done" event repeats the whole answer.
const assistantStreamBuffer = 512

// assistantSubscriberWait is how long an answer waits for the browser's
// events stream to connect: the widget opens it just before asking, and
// answers are not replayed.
const assistantSubscriberWait = 3 * time.Second

// assistantConversationRe matches the conversation IDs the widget mints,
// within what the model service accepts for a session key.
var assistantConversationRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)

// AssistantHandler serves the AI assistant routes:
//
//	GET  /v1/{tenant}/assistant                               whether one is available
//	GET  /v1/{tenant}/assistant/{conversationId}/events      the answer stream (SSE)
//	POST /v1/{tenant}/assistant/{conversationId}/messages    ask a question
//
// A question is answered in the background and streamed to the events
// route, the same split the engineer chat uses, so products with a custom
// transport need nothing new. Streams are keyed by the caller's subject,
// so one user can never read another's conversation.
type AssistantHandler struct {
	// providerFor returns a tenant's assistant, or nil when it has none.
	providerFor func(tenantSlug string) assistant.Provider
	hub         *stream.Hub

	mu   sync.Mutex
	busy map[string]bool
}

// NewAssistantHandler builds an AssistantHandler.
func NewAssistantHandler(providerFor func(tenantSlug string) assistant.Provider, hub *stream.Hub) *AssistantHandler {
	return &AssistantHandler{providerFor: providerFor, hub: hub, busy: map[string]bool{}}
}

// assistantContext resolves the tenant, caller and the tenant's assistant,
// writing an error and returning ok=false when it cannot serve them.
func (h *AssistantHandler) assistantContext(w http.ResponseWriter, r *http.Request) (t *tenant.Tenant, subject string, provider assistant.Provider, ok bool) {
	t, ok = tenant.FromContext(r.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "Internal error.")
		return nil, "", nil, false
	}
	subject = canonicalOwner(r)
	if subject == "" {
		writeError(w, http.StatusUnauthorized, "A user session is required for this action.")
		return nil, "", nil, false
	}
	provider = h.providerFor(t.Slug)
	if provider == nil {
		writeError(w, http.StatusNotFound, "The assistant is not available for this product.")
		return nil, "", nil, false
	}
	return t, subject, provider, true
}

// assistantStreamKey is the hub key for one user's conversation.
func assistantStreamKey(tenantSlug, subject, conversationID string) string {
	return "assistant:" + assistant.AccountID(tenantSlug, subject) + ":" + conversationID
}

// HandleStatusV1 handles GET /v1/{tenant}/assistant.
func (h *AssistantHandler) HandleStatusV1(w http.ResponseWriter, r *http.Request) {
	if _, _, _, ok := h.assistantContext(w, r); !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": true})
}

// HandleEventsV1 handles GET /v1/{tenant}/assistant/{conversationId}/events.
func (h *AssistantHandler) HandleEventsV1(w http.ResponseWriter, r *http.Request) {
	t, subject, _, ok := h.assistantContext(w, r)
	if !ok {
		return
	}
	conversationID := r.PathValue("conversationId")
	if !assistantConversationRe.MatchString(conversationID) {
		writeError(w, http.StatusBadRequest, "Invalid conversation ID.")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "Streaming unsupported.")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ch, unsubscribe := h.hub.SubscribeSize(assistantStreamKey(t.Slug, subject, conversationID), assistantStreamBuffer)
	defer unsubscribe()
	heartbeat := time.NewTicker(sseHeartbeatInterval)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case payload := <-ch:
			fmt.Fprintf(w, "data: %s\n\n", payload)
			flusher.Flush()
		case <-heartbeat.C:
			fmt.Fprint(w, ": heartbeat\n\n")
			flusher.Flush()
		}
	}
}

// assistantMessageRequest is the body of POST .../messages.
type assistantMessageRequest struct {
	Message string `json:"message"`
}

// HandleMessageV1 handles POST /v1/{tenant}/assistant/{conversationId}/messages.
// It answers 202 at once; the answer arrives on the events route. A
// conversation answers one question at a time (409 while busy).
func (h *AssistantHandler) HandleMessageV1(w http.ResponseWriter, r *http.Request) {
	t, subject, provider, ok := h.assistantContext(w, r)
	if !ok {
		return
	}
	conversationID := r.PathValue("conversationId")
	if !assistantConversationRe.MatchString(conversationID) {
		writeError(w, http.StatusBadRequest, "Invalid conversation ID.")
		return
	}
	body, ok := readBody(w, r)
	if !ok {
		return
	}
	var req assistantMessageRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body.")
		return
	}
	req.Message = strings.TrimSpace(req.Message)
	if req.Message == "" || len(req.Message) > maxMessageContentBytes {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("message must be 1 to %d bytes.", maxMessageContentBytes))
		return
	}

	key := assistantStreamKey(t.Slug, subject, conversationID)
	h.mu.Lock()
	if h.busy[key] {
		h.mu.Unlock()
		writeError(w, http.StatusConflict, "The assistant is still answering the previous question.")
		return
	}
	h.busy[key] = true
	h.mu.Unlock()

	turn := assistant.Turn{
		TenantSlug:     t.Slug,
		AccountID:      assistant.AccountID(t.Slug, subject),
		ConversationID: conversationID,
		Message:        req.Message,
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), assistantAnswerTimeout)
	go func() {
		defer cancel()
		defer func() {
			h.mu.Lock()
			delete(h.busy, key)
			h.mu.Unlock()
		}()
		h.waitForSubscriber(ctx, key)
		provider.Answer(ctx, turn, func(evt assistant.Event) {
			payload, err := json.Marshal(evt)
			if err != nil {
				slog.Error("assistant: encode event", "err", err)
				return
			}
			h.hub.PublishLive(key, string(payload))
		})
	}()

	writeJSON(w, http.StatusAccepted, map[string]string{"conversationId": conversationID})
}

// waitForSubscriber returns once key has a subscriber, or after
// assistantSubscriberWait.
func (h *AssistantHandler) waitForSubscriber(ctx context.Context, key string) {
	deadline := time.Now().Add(assistantSubscriberWait)
	for h.hub.Subscribers(key) == 0 && time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return
		case <-time.After(25 * time.Millisecond):
		}
	}
}
