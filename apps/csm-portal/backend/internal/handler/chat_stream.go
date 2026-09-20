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
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/middleware"
)

// engineerAlertStreamHeartbeat mirrors caseActivityStreamHeartbeat's purpose
// (see case_stream.go) — keeps this connection alive through intermediate
// proxies that would otherwise time out an idle response.
const engineerAlertStreamHeartbeat = 15 * time.Second

// StreamEngineerAlerts handles GET /chat/alerts/stream: a long-lived
// Server-Sent Events connection that emits every live-chat event this
// backend delivers to this engineer — a customer escalation the routing
// service assigned to them specifically (see chat.go's engineerHubKey),
// another engineer accepting/completing a session, or a customer message
// arriving during an accepted session. It is registered on this backend's
// main API listener alongside every other route, behind the same
// authMiddleware/CORS chain (see cmd/server/main.go) — unlike
// StreamCaseActivities, which still uses its own dedicated listener. Since
// the main listener's WriteTimeout would otherwise cut this connection off
// after that many seconds, the handler clears its own write deadline via
// http.NewResponseController on entry (see the call below) rather than
// requiring a second always-on listener just for this one endpoint. It is
// unconditional: it does not depend on Event Hub being configured, since
// live engineer chat has no Kafka-backed fallback path to degrade to.
//
// Registers under TWO stream.BroadcastHub keys at once: this engineer's own
// (engineerHubKey(user.UserID) -- the IdP "userid" claim, see
// chat-routing-service's migrations/000014_rename_engineer_status_table for
// why this is a user ID rather than an email), where the routing service's
// targeted deliveries land, and the shared broadcastHubKey, which now serves only as
// the escalate fallback when the routing service is unreachable and as the
// (still-broadcast) customer-message relay — see chat.go's package doc
// comment and broadcastHubKey's own doc comment for why those two cases
// still go to everyone. Any signed-in user of this backend may subscribe;
// the events themselves carry no more than what an engineer picking up a
// case needs (case/conversation id, subject, customer display name, the
// opening message) — never anything from inside a session assigned to a
// *different* engineer, so the broadcast key's continued existence is an
// accepted, deliberate tradeoff for this prototype phase, not an oversight.
func (h *ChatHandler) StreamEngineerAlerts(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserInfoFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, ErrMsgUnauthorized)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, ErrMsgInternal)
		return
	}

	// This connection is meant to stay open indefinitely, unlike every other
	// route on the shared main listener (see cmd/server/main.go's
	// srv.WriteTimeout) — clear the write deadline for this response only,
	// rather than raising the shared listener's timeout for every route.
	// A zero time.Time means "no deadline". Requires
	// internal/middleware.Logger's responseWriter to forward
	// SetWriteDeadline to the underlying connection (it does); an error
	// here would mean that wrapper regressed, which is why it's still
	// worth capturing for the log even though there is nothing else to do
	// about it at this point in the request.
	if err := http.NewResponseController(w).SetWriteDeadline(time.Time{}); err != nil {
		slog.WarnContext(r.Context(), "engineer chat alert stream: failed to clear write deadline", "err", err)
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	// Nginx/Choreo-gateway hint to disable response buffering for this
	// endpoint; harmless (ignored) on stacks that don't recognise it.
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ownKey := engineerHubKey(user.UserID)
	ownCh := h.hub.Register(ownKey)
	defer h.hub.Unregister(ownKey, ownCh)

	broadcastCh := h.hub.Register(broadcastHubKey)
	defer h.hub.Unregister(broadcastHubKey, broadcastCh)

	ctx := r.Context()
	ticker := time.NewTicker(engineerAlertStreamHeartbeat)
	defer ticker.Stop()

	slog.InfoContext(ctx, "engineer chat alert stream connected", "userID", user.UserID)

	// emit writes one SSE event for payload (always compact, single-line
	// JSON built by chatEvent/json.Marshal in chat.go — safe to write as
	// one `data:` line), returning false if the write failed and the
	// connection should be torn down.
	emit := func(payload string) bool {
		if _, err := fmt.Fprintf(w, "event: chat_alert\ndata: %s\n\n", payload); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	for {
		select {
		case <-ctx.Done():
			slog.InfoContext(ctx, "engineer chat alert stream disconnected", "userID", user.UserID)
			return
		case <-ticker.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case payload, ok := <-ownCh:
			if !ok {
				return
			}
			if !emit(payload) {
				return
			}
		case payload, ok := <-broadcastCh:
			if !ok {
				return
			}
			if !emit(payload) {
				return
			}
		}
	}
}
