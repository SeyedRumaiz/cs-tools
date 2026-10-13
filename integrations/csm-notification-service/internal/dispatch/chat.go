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

package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/wso2-open-operations/cs-tools/integrations/csm-notification-service/internal/eventbus"
	"github.com/wso2-open-operations/cs-tools/integrations/csm-notification-service/internal/events"
	"github.com/wso2-open-operations/cs-tools/integrations/csm-notification-service/internal/notifications"
)

// handleChatAssignedWhileAway emails the engineer a live chat was assigned
// to while their CSM Portal tab was closed, so they come back and accept
// it. A private email to that engineer alone: no CC, no team address.
//
// The send is the only side effect and the last step, so a retry after a
// failed send cannot duplicate anything.
func (d *Dispatcher) handleChatAssignedWhileAway(ctx context.Context, _ eventbus.Record, raw json.RawMessage) error {
	var p events.ChatAssignedWhileAwayPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return fmt.Errorf("dispatch: decode chat.assigned_while_away payload: %w", err)
	}

	recipients, intendedFor, ok := d.chatEngineerRecipients(ctx, p.EngineerEmail, p.CaseID, "away-engineer chat email")
	if !ok {
		return nil
	}

	body := notifications.RenderChatAssignedWhileAwayEmail(notifications.ChatAssignedWhileAwayEmailData{
		CustomerName: p.CustomerName,
		Product:      p.Product,
		Subject:      p.Subject,
		AssignedOn:   p.AssignedOn,
		ChatLink:     p.ChatLink,
		IntendedFor:  intendedFor,
	})
	if err := d.email.SendEmail(ctx, recipients, nil, nil, nil, notifications.ChatAssignedWhileAwaySubject, body, nil); err != nil {
		return fmt.Errorf("dispatch: send away-engineer chat email for %s: %w", p.CaseID, err)
	}
	slog.InfoContext(ctx, "dispatch: away-engineer chat email sent", "caseId", p.CaseID)
	return nil
}

// handleChatEngineerAwayWarning emails the engineer on an accepted live
// chat whose customer was just told they seem to have stepped away, so they
// can reply before the chat is ended. Private to that engineer, like
// handleChatAssignedWhileAway, and the send is again the last step.
func (d *Dispatcher) handleChatEngineerAwayWarning(ctx context.Context, _ eventbus.Record, raw json.RawMessage) error {
	var p events.ChatEngineerAwayWarningPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return fmt.Errorf("dispatch: decode chat.engineer_away_warning payload: %w", err)
	}
	recipients, intendedFor, ok := d.chatEngineerRecipients(ctx, p.EngineerEmail, p.CaseID, "chat away-warning email")
	if !ok {
		return nil
	}

	body := notifications.RenderChatEngineerAwayWarningEmail(notifications.ChatEngineerAwayWarningEmailData{
		CustomerName: p.CustomerName,
		Product:      p.Product,
		Subject:      p.Subject,
		LastActiveAt: p.LastActiveAt,
		EndsAt:       p.EndsAt,
		ChatLink:     p.ChatLink,
		IntendedFor:  intendedFor,
	})
	if err := d.email.SendEmail(ctx, recipients, nil, nil, nil, notifications.ChatEngineerAwayWarningSubject, body, nil); err != nil {
		return fmt.Errorf("dispatch: send chat away-warning email for %s: %w", p.CaseID, err)
	}
	slog.InfoContext(ctx, "dispatch: chat away-warning email sent", "caseId", p.CaseID)
	return nil
}

// chatEngineerRecipients is who a live-chat email to engineerEmail goes to:
// that engineer, or the debug recipients in debug mode, with intendedFor
// set to the engineer then. ok is false when nothing should be sent; the
// reason is logged with what, the email's name.
func (d *Dispatcher) chatEngineerRecipients(ctx context.Context, engineerEmail, caseID, what string) (recipients []string, intendedFor string, ok bool) {
	if !d.emailSendingEnabled {
		slog.InfoContext(ctx, "dispatch: email sending disabled, skipping "+what, "caseId", caseID)
		return nil, "", false
	}
	if !d.emailDebugMode {
		return []string{engineerEmail}, "", true
	}
	if len(d.emailDebugRecipients) == 0 {
		slog.WarnContext(ctx, "dispatch: email debug mode on with no debug recipients, skipping "+what, "caseId", caseID)
		return nil, "", false
	}
	return d.emailDebugRecipients, engineerEmail, true
}
