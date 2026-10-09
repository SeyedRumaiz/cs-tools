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

	"github.com/wso2-open-operations/cs-tools/apps/chat-routing-service/sdk-go/routingclient"
	"github.com/wso2-open-operations/cs-tools/apps/live-chat-sdk/sdk-go/pushevents"
)

// EngineerIdlePolicy says when the customer of a quiet engineer is told,
// and when the chat is ended for them. Idle time counts from the
// engineer's last message, or from the accept before their first one.
//
// An engineer is never set offline for closing their tab, since that can
// be accidental; a closed tab only shortens how long the customer waits.
type EngineerIdlePolicy struct {
	// AwayWarnAfter: tab closed and idle this long, so tell the customer
	// the engineer seems to have stepped away.
	AwayWarnAfter time.Duration
	// AwayEndAfter: tab closed and idle this long, so end the chat.
	AwayEndAfter time.Duration
	// BusyNoticeAfter: tab open and idle this long, so tell the customer
	// the engineer is still looking into it. The chat is not ended: the
	// engineer may be reading logs.
	BusyNoticeAfter time.Duration
	// TabClosedGrace: how long a tab must stay closed before it counts,
	// so reloading the page is not treated as leaving.
	TabClosedGrace time.Duration
}

// DefaultEngineerIdlePolicy returns the limits used unless configured.
func DefaultEngineerIdlePolicy() EngineerIdlePolicy {
	return EngineerIdlePolicy{
		AwayWarnAfter:   2 * time.Minute,
		AwayEndAfter:    5 * time.Minute,
		BusyNoticeAfter: 5 * time.Minute,
		TabClosedGrace:  30 * time.Second,
	}
}

// WithEngineerIdlePolicy replaces the default idle limits.
func (h *ChatHandler) WithEngineerIdlePolicy(p EngineerIdlePolicy) *ChatHandler {
	h.idlePolicy = p
	return h
}

// idleTracker is the idle sweep's memory between ticks. Only the sweeper
// goroutine uses it.
type idleTracker struct {
	// tabClosedSince is the first sweep that found an engineer with no
	// open portal tab, by engineer user ID.
	tabClosedSince map[string]time.Time
	// notices is the last status the customer was told, by case ID.
	notices map[string]idleNotice
}

type idleNotice struct {
	// activeAt is the engineer activity the notice was about; a newer
	// engineer message starts over.
	activeAt time.Time
	status   pushevents.EngineerStatus
}

func newIdleTracker() idleTracker {
	return idleTracker{tabClosedSince: map[string]time.Time{}, notices: map[string]idleNotice{}}
}

// sweepEngineerIdleOnce checks every accepted chat against the idle
// policy. Best-effort: a failed lookup just skips this tick.
func (h *ChatHandler) sweepEngineerIdleOnce(ctx context.Context, now time.Time) {
	chats, err := h.routing.ActiveChats(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "chat: engineer idle sweep failed", "err", err)
		return
	}

	p := h.idlePolicy
	engineers := map[string]bool{}
	cases := map[string]bool{}
	for _, chat := range chats {
		engineers[chat.AssigneeID] = true
		cases[chat.CaseID] = true

		away := h.tabClosedFor(chat.AssigneeID, now) >= p.TabClosedGrace
		idle := now.Sub(chat.EngineerActiveAt)
		notice := h.idle.notices[chat.CaseID]
		if !notice.activeAt.Equal(chat.EngineerActiveAt) {
			notice = idleNotice{activeAt: chat.EngineerActiveAt}
		}

		switch {
		case away && idle >= p.AwayEndAfter:
			h.endInactiveChat(ctx, chat)
			delete(h.idle.notices, chat.CaseID)
			continue
		case away && idle >= p.AwayWarnAfter:
			notice.status = h.tellCustomer(ctx, chat, notice.status, pushevents.EngineerAway)
		case !away && notice.status == pushevents.EngineerAway:
			h.tellCustomer(ctx, chat, notice.status, pushevents.EngineerBack)
			notice.status = ""
			if idle >= p.BusyNoticeAfter {
				// "Back" already answers the question; skip the busy notice
				// for this same quiet spell.
				notice.status = pushevents.EngineerBusy
			}
		case !away && idle >= p.BusyNoticeAfter:
			notice.status = h.tellCustomer(ctx, chat, notice.status, pushevents.EngineerBusy)
		}
		h.idle.notices[chat.CaseID] = notice
	}

	for caseID := range h.idle.notices {
		if !cases[caseID] {
			delete(h.idle.notices, caseID)
		}
	}
	for userID := range h.idle.tabClosedSince {
		if !engineers[userID] {
			delete(h.idle.tabClosedSince, userID)
		}
	}
}

// tabClosedFor reports how long userID has had no csm-portal tab open, as
// seen by this sweep; zero while one is open.
func (h *ChatHandler) tabClosedFor(userID string, now time.Time) time.Duration {
	if h.hub.Subscribers(engineerHubKey(userID)) > 0 {
		delete(h.idle.tabClosedSince, userID)
		return 0
	}
	since, ok := h.idle.tabClosedSince[userID]
	if !ok {
		h.idle.tabClosedSince[userID] = now
		return 0
	}
	return now.Sub(since)
}

// tellCustomer sends status to the chat's customer unless they were
// already told it, and returns the status they now have.
func (h *ChatHandler) tellCustomer(ctx context.Context, chat routingclient.ActiveChat, told, status pushevents.EngineerStatus) pushevents.EngineerStatus {
	if told == status {
		return told
	}
	slog.InfoContext(ctx, "chat: telling the customer about the engineer's availability",
		"caseId", chat.CaseID, "assigneeId", chat.AssigneeID, "status", status)
	h.notifyOrigin(ctx, h.sourceForCase(ctx, chat.CaseID), chatEvent{
		Type:           string(pushevents.TypeEngineerStatus),
		CaseID:         chat.CaseID,
		ConversationID: chat.ConversationID,
		EngineerEmail:  h.engineers.email(chat.AssigneeID),
		Status:         string(status),
		Timestamp:      time.Now().UTC().Format(time.RFC3339),
	})
	return status
}

// endInactiveChat ends a chat whose engineer left, telling both sides the
// same way HandleCompleteSession does, with the reason for the customer.
func (h *ChatHandler) endInactiveChat(ctx context.Context, chat routingclient.ActiveChat) {
	result, err := h.routing.Completed(ctx, chat.AssigneeID, chat.CaseID)
	if err != nil {
		slog.ErrorContext(ctx, "chat: ending an inactive chat failed", "caseId", chat.CaseID, "err", err)
		return
	}
	slog.InfoContext(ctx, "chat: ended a chat whose engineer was away too long",
		"caseId", chat.CaseID, "assigneeId", chat.AssigneeID)

	email := h.engineers.email(chat.AssigneeID)
	now := time.Now().UTC().Format(time.RFC3339)
	h.publishToEngineers(chatEvent{
		Type:           "session_closed",
		CaseID:         chat.CaseID,
		ConversationID: chat.ConversationID,
		EngineerEmail:  email,
		Timestamp:      now,
	})
	h.notifyOrigin(ctx, h.sourceForCase(ctx, chat.CaseID), chatEvent{
		Type:           string(pushevents.TypeEngineerDisconnected),
		CaseID:         chat.CaseID,
		ConversationID: chat.ConversationID,
		EngineerEmail:  email,
		Reason:         pushevents.ReasonInactive,
		Timestamp:      now,
	})
	if result.AssignedCase != nil {
		h.publishAssignment(ctx, chat.AssigneeID, *result.AssignedCase, liveChatFromQueue)
	}
}
