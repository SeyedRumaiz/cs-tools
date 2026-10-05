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
// engineers who were assigned a case (PENDING) and never accepted it
// within that service's configured timeout, delivering any resulting
// reassignment the same way a fresh escalation would arrive and clearing
// the stale alert on the original engineer's screen. It runs until ctx is
// cancelled.
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

// sweepTimeoutsOnce runs one sweep. Best-effort: a failed sweep just means
// this tick found nothing to do; the next tick tries again.
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
		// alert is gone.
		h.publishToEngineer(timeout.UserID, chatEvent{
			Type:      "case_timed_out",
			CaseID:    timeout.CaseID,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})

		if timeout.ReassignedTo != "" && timeout.AssignedCase != nil {
			h.publishAssignment(ctx, timeout.ReassignedTo, *timeout.AssignedCase, liveChatReassigned)
		}
	}

	// Queue-abandonment results: a case never assigned to any engineer,
	// abandoned after sitting too long. No engineer-facing event to clear
	// here; best-effort notify the case's origin so an open customer chat
	// can show a "no engineer was available" message.
	for _, abandoned := range result.Abandoned {
		slog.InfoContext(ctx, "chat: abandoned a case that waited too long with no engineer free",
			"caseId", abandoned.CaseID, "conversationId", abandoned.ConversationID)
		h.notifyOrigin(ctx, h.sourceForCase(ctx, abandoned.CaseID), chatEvent{
			Type:           "chat_abandoned",
			CaseID:         abandoned.CaseID,
			ConversationID: abandoned.ConversationID,
			Message:        "No engineer was available to take this chat. Please try again.",
			Timestamp:      time.Now().UTC().Format(time.RFC3339),
		})
	}

	// Stale accepted sessions: an engineer held one of these and never
	// completed it (browser closed, crashed, forgotten). Notify both sides
	// the same way HandleCompleteSession does for an explicit complete.
	for _, stale := range result.Stale {
		slog.InfoContext(ctx, "chat: force-ended an accepted session that went idle too long",
			"caseId", stale.CaseID, "conversationId", stale.ConversationID, "assigneeId", stale.AssigneeID)
		now := time.Now().UTC().Format(time.RFC3339)
		h.publishToEngineers(chatEvent{
			Type:           "session_closed",
			CaseID:         stale.CaseID,
			ConversationID: stale.ConversationID,
			Timestamp:      now,
		})
		h.notifyOrigin(ctx, h.sourceForCase(ctx, stale.CaseID), chatEvent{
			Type:           "engineer_disconnected",
			CaseID:         stale.CaseID,
			ConversationID: stale.ConversationID,
			Timestamp:      now,
		})
		if stale.AssignedCase != nil {
			h.publishAssignment(ctx, stale.AssigneeID, *stale.AssignedCase, liveChatFromQueue)
		}
	}
}
