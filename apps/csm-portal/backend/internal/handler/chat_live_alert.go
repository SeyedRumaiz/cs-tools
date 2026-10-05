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
	"html"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/wso2-open-operations/cs-tools/apps/chat-routing-service/sdk-go/routingclient"
	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/middleware"
	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/notifications"
)

// liveChatAlertSender is the Google Chat channel used for live-chat alerts.
type liveChatAlertSender interface {
	HasSpace(product string) bool
	SendLiveChatAlert(ctx context.Context, product string, alert notifications.LiveChatAlert) error
}

// WithLiveChatAlerts enables Google Chat alerts for live-chat escalations and
// assignments. Without it (or without a configured space) none are sent.
func (h *ChatHandler) WithLiveChatAlerts(sender liveChatAlertSender, portalBaseURL string) *ChatHandler {
	h.liveChatAlerts = sender
	h.portalBaseURL = strings.TrimRight(portalBaseURL, "/")
	return h
}

const (
	liveChatAlertSpace      = "live-chat"
	liveChatAlertTimeout    = 15 * time.Second
	liveChatAlertMaxMessage = 300
)

// liveChatAlertKind says why a Google Chat message is being sent.
type liveChatAlertKind int

const (
	liveChatNewlyAssigned liveChatAlertKind = iota // a new chat went straight to an engineer
	liveChatReassigned                             // the previous engineer did not accept
	liveChatFromQueue                              // a waiting chat was given to a freed-up engineer
	liveChatQueued                                 // no engineer free; the chat is waiting
	liveChatBroadcast                              // routing service down; shown to every engineer
	liveChatAccepted                               // the assigned engineer accepted the chat
)

// engineerDirectory remembers each engineer's email by IdP user id, filled
// from their own signed-in requests (the portal polls every few seconds), so
// an assignment, which carries only the user id, can be announced by name.
type engineerDirectory struct {
	mu     sync.RWMutex
	emails map[string]string
}

func newEngineerDirectory() *engineerDirectory {
	return &engineerDirectory{emails: make(map[string]string)}
}

func (d *engineerDirectory) remember(userID, email string) {
	if userID == "" || email == "" {
		return
	}
	d.mu.Lock()
	d.emails[userID] = email
	d.mu.Unlock()
}

func (d *engineerDirectory) email(userID string) string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.emails[userID]
}

// rememberEngineer records an authenticated engineer for later announcements.
func (h *ChatHandler) rememberEngineer(user *middleware.UserInfo) {
	if user != nil && h.engineers != nil {
		h.engineers.remember(user.UserID, user.Email)
	}
}

// publishAssignment delivers a chat to the assigned engineer's browser and
// tells the Google Chat space who has it, so teammates know whom to nudge
// when it is not accepted. Only the assigned engineer can accept it.
func (h *ChatHandler) publishAssignment(ctx context.Context, engineerID string, ci routingclient.CaseInfo, kind liveChatAlertKind) {
	h.publishToEngineer(engineerID, assignedCaseEvent(ci))
	who, mention := "an available engineer", ""
	if h.engineers != nil {
		if email := h.engineers.email(engineerID); email != "" {
			who, mention = email, email
		}
	}
	h.alertLiveChat(ctx, ci, kind, who, mention)
}

// alertLiveChat posts the Google Chat card in the background; a failure is
// logged and never affects the customer's chat. mentionEmail, when set, pings
// that engineer: only used for cards that hand them a chat to accept.
func (h *ChatHandler) alertLiveChat(ctx context.Context, ci routingclient.CaseInfo, kind liveChatAlertKind, engineer, mentionEmail string) {
	if h.liveChatAlerts == nil || h.portalBaseURL == "" || !h.liveChatAlerts.HasSpace(liveChatAlertSpace) {
		return
	}
	alert := buildLiveChatAlert(ci, kind, engineer)
	alert.PortalURL = h.portalBaseURL + "/chat"
	alert.ThreadKey = ci.CaseID
	alert.MentionEmail = mentionEmail
	sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), liveChatAlertTimeout)
	go func() {
		defer cancel()
		if err := h.liveChatAlerts.SendLiveChatAlert(sendCtx, liveChatAlertSpace, alert); err != nil {
			slog.Error("chat: google chat live-chat alert failed", "caseId", ci.CaseID, "err", err)
		}
	}()
}

// buildLiveChatAlert lays out the card for one event. Every user-supplied
// value is HTML-escaped because card rows accept Google Chat's simple HTML.
// The customer's first message is only shown for a new request; follow-ups
// about the same chat leave it out.
func buildLiveChatAlert(ci routingclient.CaseInfo, kind liveChatAlertKind, engineer string) notifications.LiveChatAlert {
	customer := "A customer"
	switch {
	case ci.CustomerName != "" && ci.CustomerEmail != "" && ci.CustomerName != ci.CustomerEmail:
		customer = ci.CustomerName + " (" + ci.CustomerEmail + ")"
	case ci.CustomerName != "":
		customer = ci.CustomerName
	case ci.CustomerEmail != "":
		customer = ci.CustomerEmail
	}
	details := []notifications.LiveChatDetail{{Label: "Customer", Value: html.EscapeString(customer)}}
	if ci.TenantSlug != "" {
		details = append(details, notifications.LiveChatDetail{Label: "Product", Value: html.EscapeString(ci.TenantSlug)})
	}
	addMessage := func() {
		runes := []rune(strings.TrimSpace(ci.Message))
		if len(runes) > liveChatAlertMaxMessage {
			runes = append(runes[:liveChatAlertMaxMessage], '…')
		}
		if len(runes) > 0 {
			details = append(details, notifications.LiveChatDetail{Label: "Message", Value: html.EscapeString(string(runes))})
		}
	}
	engineerRow := func(label string) {
		details = append(details, notifications.LiveChatDetail{Label: label, Value: "<b>" + html.EscapeString(engineer) + "</b>"})
	}

	alert := notifications.LiveChatAlert{Title: "Live chat requested", Subtitle: "A customer wants to talk to an engineer"}
	switch kind {
	case liveChatNewlyAssigned:
		addMessage()
		engineerRow("Assigned to")
		alert.Note = "<i>Only the assigned engineer can accept this chat. If they do not, it moves to the next engineer automatically.</i>"
	case liveChatReassigned:
		alert.Title, alert.Subtitle = "Live chat reassigned", "The previous engineer did not accept in time"
		engineerRow("Now assigned to")
	case liveChatFromQueue:
		alert.Title, alert.Subtitle = "Waiting chat assigned", "An engineer became free"
		engineerRow("Assigned to")
	case liveChatAccepted:
		alert.Title, alert.Subtitle = "Live chat accepted", "No one else needs to pick this up"
		engineerRow("Accepted by")
	case liveChatQueued:
		addMessage()
		details = append(details, notifications.LiveChatDetail{Label: "Status", Value: "<b>Waiting in the queue</b>: no engineer is free yet"})
	default:
		addMessage()
		details = append(details, notifications.LiveChatDetail{Label: "Status", Value: "<b>Routing service unavailable</b>: every connected engineer was alerted"})
	}
	alert.Details = details
	return alert
}

// alertLiveChatAccepted tells the Google Chat space that the assigned engineer
// accepted the chat, in the same thread as the original card. The case's
// customer details are fetched best-effort; without them the card still names
// the engineer.
func (h *ChatHandler) alertLiveChatAccepted(ctx context.Context, caseID, engineerEmail string) {
	if h.liveChatAlerts == nil || h.portalBaseURL == "" || !h.liveChatAlerts.HasSpace(liveChatAlertSpace) {
		return
	}
	detached := context.WithoutCancel(ctx)
	go func() {
		lookupCtx, cancel := context.WithTimeout(detached, liveChatAlertTimeout)
		defer cancel()
		ci, err := h.routing.GetCaseInfo(lookupCtx, caseID)
		if err != nil {
			slog.Warn("chat: could not load case details for the accepted-chat alert", "caseId", caseID, "err", err)
			ci = routingclient.CaseInfo{CaseID: caseID}
		}
		if engineerEmail == "" {
			engineerEmail = "an engineer"
		}
		h.alertLiveChat(lookupCtx, ci, liveChatAccepted, engineerEmail, "")
	}()
}
