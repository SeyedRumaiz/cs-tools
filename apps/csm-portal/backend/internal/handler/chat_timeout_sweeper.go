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
	"log/slog"
	"time"
)

// StartTimeoutSweeper polls chat-routing-service periodically for
// engineers who were assigned a case (PENDING) and never accepted it past
// that service's own configured PENDING_TIMEOUT_SECONDS, delivering any
// resulting reassignment the same way a fresh escalation would arrive, and
// clearing the stale alert on the original (unresponsive) engineer's
// screen.
//
// Deliberately a poll from this side rather than a push from
// chat-routing-service: this handler already owns the only thing that can
// act on the result (the engineer SSE hub), and chat-routing-service is
// intentionally synchronous/caller-driven only (see routingclient's
// package doc comment on why it never calls back) -- adding a reverse
// callback direction just for this one feature would be new
// infrastructure for a small win. Runs until ctx is cancelled -- see
// cmd/server/main.go, which starts this alongside the HTTP server and
// passes the same shutdown context.
func (h *ChatHandler) StartTimeoutSweeper(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.sweepTimeoutsOnce(ctx)
		}
	}
}

// sweepTimeoutsOnce runs one sweep. Best-effort by construction, matching
// this file's other background/side-channel calls: a failed sweep just
// means this tick found nothing to do from this handler's point of view —
// chat-routing-service itself is unaffected, and the next tick tries again.
func (h *ChatHandler) sweepTimeoutsOnce(ctx context.Context) {
	result, err := h.routing.SweepTimeouts(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "chat: timeout sweep failed", "err", err)
		return
	}
	for _, timeout := range result.Timeouts {
		slog.InfoContext(ctx, "chat: engineer timed out on pending case",
			"userID", timeout.UserID, "caseId", timeout.CaseID,
			"reassignedTo", timeout.ReassignedTo, "requeued", timeout.Requeued)

		// Tell the unresponsive engineer's own browser their stale pending
		// alert is gone. EngineerAlertNotification.tsx now handles this
		// event type (clears the pending card) -- see that component's
		// handleAlert "case_timed_out" case.
		h.publishToEngineer(timeout.UserID, chatEvent{
			Type:      "case_timed_out",
			CaseID:    timeout.CaseID,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})

		if timeout.ReassignedTo != "" && timeout.AssignedCase != nil {
			h.publishToEngineer(timeout.ReassignedTo, assignedCaseEvent(*timeout.AssignedCase))
		}
	}

	// Queue-abandonment results (2026-09-10 fix): a case that sat
	// WAITING_FOR_ENGINEER -- never assigned to anyone at all -- past
	// chat-routing-service's own QUEUE_ABANDON_SECONDS. Nobody on the
	// engineer side ever saw this case (it was never delivered), so there
	// is no engineer-facing event to clear here -- only the customer might
	// still have a tab open waiting on it. Best-effort notify backend-v2 so
	// a still-open customer chat can show a "no engineer was available"
	// message instead of waiting forever; a customer who already left sees
	// nothing, which is no worse than today.
	for _, abandoned := range result.Abandoned {
		slog.InfoContext(ctx, "chat: abandoned a case that waited too long with no engineer free",
			"caseId", abandoned.CaseID, "conversationId", abandoned.ConversationID)
		h.notifyBackendV2(ctx, chatEvent{
			Type:           "chat_abandoned",
			CaseID:         abandoned.CaseID,
			ConversationID: abandoned.ConversationID,
			Message:        "No engineer was available to take this chat. Please try again.",
			Timestamp:      time.Now().UTC().Format(time.RFC3339),
		})
	}
}
