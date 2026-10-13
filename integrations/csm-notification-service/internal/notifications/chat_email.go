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

package notifications

import (
	_ "embed"
	"strings"
	"time"
)

//go:embed templates/chat_assigned_while_away.html
var chatAssignedWhileAwayTemplateRaw string

var chatAssignedWhileAwayTemplate = bakeLogo(chatAssignedWhileAwayTemplateRaw)

// ChatAssignedWhileAwaySubject is the email's subject.
const ChatAssignedWhileAwaySubject = "[WSO2 Support] A live chat is waiting for you"

// ChatAssignedWhileAwayEmailData is what RenderChatAssignedWhileAwayEmail
// substitutes.
type ChatAssignedWhileAwayEmailData struct {
	CustomerName string
	Product      string
	Subject      string
	// AssignedOn is RFC3339; shown in Asia/Colombo like the other emails
	// here ("2026-10-12 10:13:05"). Unparseable is shown as given.
	AssignedOn string
	ChatLink   string
	// IntendedFor, when non-empty, shows an "Expected sent to" row — see
	// RenderCommentAddedEmail's own doc comment for the convention.
	IntendedFor string
}

// RenderChatAssignedWhileAwayEmail fills in the email to an engineer who
// was assigned a live chat while their CSM Portal tab was closed. A row
// with no value is left out.
func RenderChatAssignedWhileAwayEmail(d ChatAssignedWhileAwayEmailData) string {
	var rows strings.Builder
	row := func(label, value string) {
		if strings.TrimSpace(value) == "" {
			return
		}
		rows.WriteString(`<tr><td style="color:#71717a; width:150px; padding:2px 0; vertical-align:top;">` + escapeHTML(label) +
			`</td><td style="padding:2px 0;">` + escapeHTML(value) + `</td></tr>`)
	}
	assignedOn := d.AssignedOn
	if t, err := time.Parse(time.RFC3339, d.AssignedOn); err == nil {
		assignedOn = t.In(escalationTimeZone).Format("2006-01-02 15:04:05")
	}
	row("Customer", d.CustomerName)
	row("Product", d.Product)
	row("Subject", d.Subject)
	row("Assigned At", assignedOn)

	tmpl := applyOptionalBlock(chatAssignedWhileAwayTemplate, "INTENDED_FOR", d.IntendedFor)
	return strings.NewReplacer(
		"<!-- [INTENDED_FOR] -->", escapeHTML(d.IntendedFor),
		"<!-- [DETAIL_ROWS] -->", rows.String(),
		"<!-- [CHAT_LINK] -->", escapeHTML(d.ChatLink),
	).Replace(tmpl)
}
