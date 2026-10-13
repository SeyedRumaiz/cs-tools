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

	if !d.emailSendingEnabled {
		slog.InfoContext(ctx, "dispatch: email sending disabled, skipping away-engineer chat email", "caseId", p.CaseID)
		return nil
	}
	recipients := []string{p.EngineerEmail}
	var intendedFor string
	if d.emailDebugMode {
		if len(d.emailDebugRecipients) == 0 {
			slog.WarnContext(ctx, "dispatch: email debug mode on with no debug recipients, skipping away-engineer chat email",
				"caseId", p.CaseID)
			return nil
		}
		intendedFor = p.EngineerEmail
		recipients = d.emailDebugRecipients
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
