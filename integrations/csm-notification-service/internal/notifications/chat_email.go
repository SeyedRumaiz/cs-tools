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

//go:embed templates/chat_engineer_away_warning.html
var chatEngineerAwayWarningTemplateRaw string

var chatEngineerAwayWarningTemplate = bakeLogo(chatEngineerAwayWarningTemplateRaw)

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
	var rows chatDetailRows
	rows.add("Customer", d.CustomerName)
	rows.add("Product", d.Product)
	rows.add("Subject", d.Subject)
	rows.add("Assigned At", chatEmailTime(d.AssignedOn))
	return renderChatEmail(chatAssignedWhileAwayTemplate, &rows, d.ChatLink, d.IntendedFor)
}

// ChatEngineerAwayWarningSubject is the away-warning email's subject.
const ChatEngineerAwayWarningSubject = "[WSO2 Support] Your live chat will end soon"

// ChatEngineerAwayWarningEmailData is what
// RenderChatEngineerAwayWarningEmail substitutes. Times are RFC3339, shown
// like AssignedOn above.
type ChatEngineerAwayWarningEmailData struct {
	CustomerName string
	Product      string
	Subject      string
	LastActiveAt string
	EndsAt       string
	ChatLink     string
	IntendedFor  string
}

// RenderChatEngineerAwayWarningEmail fills in the email to an engineer
// whose customer on an accepted chat was told they seem to have stepped
// away.
func RenderChatEngineerAwayWarningEmail(d ChatEngineerAwayWarningEmailData) string {
	var rows chatDetailRows
	rows.add("Customer", d.CustomerName)
	rows.add("Product", d.Product)
	rows.add("Subject", d.Subject)
	rows.add("Your Last Reply", chatEmailTime(d.LastActiveAt))
	rows.add("Ends At", chatEmailTime(d.EndsAt))
	return renderChatEmail(chatEngineerAwayWarningTemplate, &rows, d.ChatLink, d.IntendedFor)
}

// chatDetailRows builds the label/value table both chat emails show. A row
// with no value is left out.
type chatDetailRows struct{ b strings.Builder }

func (r *chatDetailRows) add(label, value string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	r.b.WriteString(`<tr><td style="color:#71717a; width:150px; padding:2px 0; vertical-align:top;">` + escapeHTML(label) +
		`</td><td style="padding:2px 0;">` + escapeHTML(value) + `</td></tr>`)
}

// chatEmailTime shows an RFC3339 time in Asia/Colombo like the other emails
// here ("2026-10-12 10:13:05"); anything unparseable is shown as given.
func chatEmailTime(rfc3339 string) string {
	if t, err := time.Parse(time.RFC3339, rfc3339); err == nil {
		return t.In(escalationTimeZone).Format("2006-01-02 15:04:05")
	}
	return rfc3339
}

func renderChatEmail(tmpl string, rows *chatDetailRows, chatLink, intendedFor string) string {
	tmpl = applyOptionalBlock(tmpl, "INTENDED_FOR", intendedFor)
	return strings.NewReplacer(
		"<!-- [INTENDED_FOR] -->", escapeHTML(intendedFor),
		"<!-- [DETAIL_ROWS] -->", rows.b.String(),
		"<!-- [CHAT_LINK] -->", escapeHTML(chatLink),
	).Replace(tmpl)
}
