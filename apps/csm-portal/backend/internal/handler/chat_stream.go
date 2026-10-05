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
// backend delivers to the signed-in engineer — a customer escalation
// routed to them specifically, another engineer accepting/completing a
// session, or a customer message during an accepted session. It runs on
// the main API listener behind the normal Auth/CORS chain, clearing its
// own write deadline via http.NewResponseController so the listener's
// WriteTimeout doesn't cut the connection off.
//
// It registers under two stream.BroadcastHub keys: this engineer's own
// (engineerHubKey(user.UserID)), where targeted deliveries land, and the
// shared broadcastHubKey, used only for the escalate fallback when the
// routing service is unreachable and for the customer-message relay (see
// broadcastHubKey's own doc comment). Events carry no more than an
// engineer needs to pick up a case (case/conversation id, subject,
// customer name, opening message) — never anything from a session
// assigned to a different engineer.
func (h *ChatHandler) StreamEngineerAlerts(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserInfoFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, ErrMsgUnauthorized)
		return
	}
	h.rememberEngineer(user)

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, ErrMsgInternal)
		return
	}

	// This connection must stay open indefinitely, unlike every other route
	// on the shared main listener, so clear the write deadline for this
	// response only (zero time.Time means "no deadline") rather than
	// raising the listener's timeout for every route.
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

	// emit writes one SSE event for payload (compact, single-line JSON),
	// returning false if the write failed and the connection should close.
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
