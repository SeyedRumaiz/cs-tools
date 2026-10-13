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
	"strings"
	"sync"
	"time"

	"github.com/wso2-open-operations/cs-tools/apps/chat-routing-service/sdk-go/routingclient"
	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/chatevents"
)

// An engineer whose csm-portal tab is closed never sees the in-portal alert
// for a chat assigned to them, so the chat waits out the pending timeout
// before moving on. When that happens this backend publishes
// chat.assigned_while_away, and csm-notification-service emails the
// engineer. The chat itself is routed exactly as before.
//
// The same goes for a chat the engineer already accepted: with the tab
// closed they do not see that the customer was told they stepped away.
// At that point this backend publishes chat.engineer_away_warning, and the
// engineer is emailed while there is still time to reply before the chat
// is ended.

// awayEmailTimeout bounds one publish.
const awayEmailTimeout = 10 * time.Second

// awayEmailRepeatAfter is how long one (chat, engineer) pair is remembered,
// so a chat handed to the same engineer again soon after is not emailed
// twice.
const awayEmailRepeatAfter = time.Hour

// chatEventPublisher publishes one live-chat event; *chatevents.Publisher.
type chatEventPublisher interface {
	Publish(ctx context.Context, eventType, entityID string, payload any) error
}

// tabTracker remembers when each engineer's last csm-portal tab closed.
// Safe for concurrent use: the alert stream and every assignment path read
// and write it.
type tabTracker struct {
	mu       sync.Mutex
	closedAt map[string]time.Time
}

func newTabTracker() *tabTracker {
	return &tabTracker{closedAt: make(map[string]time.Time)}
}

func (t *tabTracker) opened(userID string) {
	t.mu.Lock()
	delete(t.closedAt, userID)
	t.mu.Unlock()
}

func (t *tabTracker) closed(userID string, at time.Time) {
	t.mu.Lock()
	t.closedAt[userID] = at
	t.mu.Unlock()
}

// closedFor reports how long userID's tab has been closed. ok is false when
// this process has never seen the tab open or close since it started; the
// caller decides what that means.
func (t *tabTracker) closedFor(userID string, now time.Time) (d time.Duration, ok bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	at, ok := t.closedAt[userID]
	if !ok {
		return 0, false
	}
	return now.Sub(at), true
}

// awayEmails remembers which (chat, engineer) pairs were already emailed.
type awayEmails struct {
	mu   sync.Mutex
	sent map[string]time.Time
}

// claim reports whether key may be emailed now, and if so records it.
// Entries older than awayEmailRepeatAfter are dropped on the way.
func (a *awayEmails) claim(key string, now time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.sent == nil {
		a.sent = make(map[string]time.Time)
	}
	for k, at := range a.sent {
		if now.Sub(at) >= awayEmailRepeatAfter {
			delete(a.sent, k)
		}
	}
	if _, done := a.sent[key]; done {
		return false
	}
	a.sent[key] = now
	return true
}

func (a *awayEmails) release(key string) {
	a.mu.Lock()
	delete(a.sent, key)
	a.mu.Unlock()
}

// WithAwayEngineerEmails turns on the email to an engineer who is assigned a
// chat while their csm-portal tab is closed. portalBaseURL is csm-portal's
// web address, for the link to its Chat page.
func (h *ChatHandler) WithAwayEngineerEmails(publisher chatEventPublisher, portalBaseURL string) *ChatHandler {
	h.awayPublisher = publisher
	h.awayPortalURL = strings.TrimRight(portalBaseURL, "/")
	return h
}

// engineerTabOpened and engineerTabClosed are called by the engineer's alert
// stream as it connects and disconnects.
func (h *ChatHandler) engineerTabOpened(userID string) {
	if h.tabs != nil {
		h.tabs.opened(userID)
	}
}

func (h *ChatHandler) engineerTabClosed(userID string) {
	if h.tabs != nil && h.hub.Subscribers(engineerHubKey(userID)) == 0 {
		h.tabs.closed(userID, time.Now())
	}
}

// engineerAway reports whether engineerID has had no csm-portal tab open
// for at least the idle policy's grace period, so a page reload does not
// count. An engineer this process has never seen connect counts as away:
// after a restart, an engineer who is still working reconnects within
// seconds, while one who left never will.
func (h *ChatHandler) engineerAway(engineerID string, now time.Time) bool {
	if h.hub.Subscribers(engineerHubKey(engineerID)) > 0 {
		return false
	}
	if h.tabs == nil {
		return true
	}
	closedFor, seen := h.tabs.closedFor(engineerID, now)
	return !seen || closedFor >= h.idlePolicy.TabClosedGrace
}

// emailIfAway publishes chat.assigned_while_away when the chat just
// assigned to engineerID cannot reach them in the portal. Best-effort and in
// the background: a failure is logged and never affects the chat.
func (h *ChatHandler) emailIfAway(ctx context.Context, engineerID string, ci routingclient.CaseInfo) {
	if h.awayPublisher == nil || h.awayPortalURL == "" || engineerID == "" {
		return
	}
	now := time.Now()
	if !h.engineerAway(engineerID, now) {
		return
	}
	email := ""
	if h.engineers != nil {
		email = h.engineers.email(engineerID)
	}
	if email == "" {
		// Known only once the engineer has used the portal since this
		// process started.
		slog.WarnContext(ctx, "chat: engineer away but email unknown, not emailing", "caseId", ci.CaseID, "engineerId", engineerID)
		return
	}
	key := ci.CaseID + "|" + engineerID
	if !h.awaySent.claim(key, now) {
		return
	}

	customer := ci.CustomerName
	if customer == "" {
		customer = ci.CustomerEmail
	}
	payload := chatevents.AssignedWhileAwayPayload{
		CaseID:        ci.CaseID,
		EngineerEmail: email,
		CustomerName:  customer,
		Product:       ci.TenantSlug,
		Subject:       ci.Subject,
		AssignedOn:    now.UTC().Format(time.RFC3339),
		ChatLink:      h.awayPortalURL + "/chat",
	}
	pubCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), awayEmailTimeout)
	go func() {
		defer cancel()
		if err := h.awayPublisher.Publish(pubCtx, chatevents.TypeAssignedWhileAway, ci.CaseID, payload); err != nil {
			h.awaySent.release(key)
			slog.Error("chat: publishing away-engineer email event failed", "caseId", ci.CaseID, "engineerId", engineerID, "err", err)
			return
		}
		slog.Info("chat: engineer away, email requested", "caseId", ci.CaseID, "engineerId", engineerID)
	}()
}

// emailAwayWarning publishes chat.engineer_away_warning for an accepted
// chat whose engineer was just reported away to the customer. The idle
// sweep calls it once per quiet spell; the claim below also covers a sweep
// that forgot its notices. Best-effort and in the background, like
// emailIfAway.
func (h *ChatHandler) emailAwayWarning(ctx context.Context, chat routingclient.ActiveChat, now, endsAt time.Time) {
	if h.awayPublisher == nil || h.awayPortalURL == "" || chat.AssigneeID == "" {
		return
	}
	email := ""
	if h.engineers != nil {
		email = h.engineers.email(chat.AssigneeID)
	}
	if email == "" {
		slog.WarnContext(ctx, "chat: engineer away but email unknown, not warning them", "caseId", chat.CaseID, "engineerId", chat.AssigneeID)
		return
	}
	// A new quiet spell (the engineer wrote since) may be warned again.
	key := "warning|" + chat.CaseID + "|" + chat.AssigneeID + "|" + chat.EngineerActiveAt.UTC().Format(time.RFC3339Nano)
	if !h.awaySent.claim(key, now) {
		return
	}

	pubCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), awayEmailTimeout)
	go func() {
		defer cancel()
		payload := chatevents.EngineerAwayWarningPayload{
			CaseID:        chat.CaseID,
			EngineerEmail: email,
			LastActiveAt:  chat.EngineerActiveAt.UTC().Format(time.RFC3339),
			EndsAt:        endsAt.UTC().Format(time.RFC3339),
			ChatLink:      h.awayPortalURL + "/chat",
		}
		// The customer, product and subject only make the email clearer; a
		// failed lookup still sends it.
		if ci, err := h.routing.GetCaseInfo(pubCtx, chat.CaseID); err == nil {
			payload.CustomerName = ci.CustomerName
			if payload.CustomerName == "" {
				payload.CustomerName = ci.CustomerEmail
			}
			payload.Product = ci.TenantSlug
			payload.Subject = ci.Subject
		} else {
			slog.WarnContext(pubCtx, "chat: case lookup for the away warning failed, sending without details", "caseId", chat.CaseID, "err", err)
		}
		if err := h.awayPublisher.Publish(pubCtx, chatevents.TypeEngineerAwayWarning, chat.CaseID, payload); err != nil {
			h.awaySent.release(key)
			slog.Error("chat: publishing away-warning email event failed", "caseId", chat.CaseID, "engineerId", chat.AssigneeID, "err", err)
			return
		}
		slog.Info("chat: engineer away on an accepted chat, warning email requested", "caseId", chat.CaseID, "engineerId", chat.AssigneeID)
	}()
}
