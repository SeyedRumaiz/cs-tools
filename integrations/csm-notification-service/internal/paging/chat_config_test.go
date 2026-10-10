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

package paging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wso2-open-operations/cs-tools/integrations/csm-notification-service/internal/notifications"
)

// chat.webhookUrlEnv names where the ladder's room is; the URL never sits in
// the committed file.
func TestLadderChat_WebhookFromNamedVariable(t *testing.T) {
	shared := notifications.NewGoogleChatClient(notifications.GoogleChatConfig{
		AudienceSpaces: []notifications.GoogleChatAudienceSpace{{Audience: "Incident Monitor", WebhookURL: "https://shared.invalid/x"}},
	})
	env := map[string]string{"CRE_CHAT_WEBHOOK_URL": "https://ladder.invalid/y"}
	getenv := func(k string) string { return env[k] }

	// Named and set: the ladder's own client, holding just that room.
	c, room := ladderChat(Chat{WebhookURLEnv: "CRE_CHAT_WEBHOOK_URL", Audience: "CRE Escalations"}, shared, "", getenv)
	if c == shared {
		t.Fatal("a named webhook must replace the shared GOOGLE_CHAT_SPACES client")
	}
	if room != "CRE Escalations" || !c.HasAudienceSpace(room) {
		t.Errorf("room %q, has space %v; want the ladder's own room", room, c.HasAudienceSpace(room))
	}

	// Named but empty: no fallback to another room -- every rung NO_CHAT_SPACE.
	c, room = ladderChat(Chat{WebhookURLEnv: "UNSET_VARIABLE"}, shared, "", getenv)
	if c == shared || c.HasAudienceSpace(room) {
		t.Error("an unset webhook variable must not fall back to GOOGLE_CHAT_SPACES")
	}

	// Not named: the shared client, routed by audience, as before.
	c, room = ladderChat(Chat{}, shared, "", getenv)
	if c != shared || room != "Incident Monitor" {
		t.Errorf("no webhookUrlEnv: got room %q and a different client; want the shared one and Incident Monitor", room)
	}
}

// A URL pasted into webhookUrlEnv is refused, and never repeated back.
//
// The file is committed to a public repository and the URL carries the space's
// key and token. Failing the load stops the mistake before it is committed;
// the error must not echo the value, or the secret lands in a log instead.
func TestConfig_WebhookURLEnvRefusesAURL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "paging-alert.yaml")
	const secret = "https://chat.googleapis.com/v1/spaces/SPACE/messages?key=KEY&token=TOKEN"
	if err := os.WriteFile(path, []byte("enabled: true\ncre:\n  chat:\n    webhookUrlEnv: \""+secret+"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("a URL in chat.webhookUrlEnv was accepted")
	}
	for _, part := range []string{"KEY", "TOKEN", "chat.googleapis.com"} {
		if strings.Contains(err.Error(), part) {
			t.Errorf("the error repeats part of the URL (%q): %v", part, err)
		}
	}

	if err := os.WriteFile(path, []byte("enabled: true\ncre:\n  chat:\n    webhookUrlEnv: CRE_CHAT_WEBHOOK_URL\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err != nil {
		t.Errorf("a variable name was refused: %v", err)
	}
}
