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
	"strings"
	"testing"
	"time"
)

// smeTeamChatsEngine is smeEngine with sme.teamChats set: each listed SME
// team posts to its own fake space.
func smeTeamChatsEngine(shared *fakeChat, own map[string]*fakeChat, notes *fakeNotes) *Engine {
	sme := smeOn
	sme.TeamChats = map[string]Chat{}
	for team := range own {
		sme.TeamChats[team] = Chat{WebhookURLEnv: "SME_CHAT_WEBHOOK_URL_" + strings.ToUpper(team)}
	}
	e := smeEngine(rotaReader(true), newMemStore(), notes, &fakeChat{}, shared, sme)
	e.smeTeamNotifiers = map[string][]notifier{}
	for team, c := range own {
		e.smeTeamNotifiers[team] = []notifier{chatNotifier{chat: c, links: fakeLinks{}, audience: team}}
	}
	e.smeNoChatNotifiers = withoutChat(e.smeNotifiers)
	return e
}

// Each SME team's cards go to its own space, and none to the shared one.
func TestSMETeamChats_EachTeamPostsToItsOwnSpace(t *testing.T) {
	shared, asgardeo, choreo := &fakeChat{}, &fakeChat{}, &fakeChat{}
	e := smeTeamChatsEngine(shared, map[string]*fakeChat{"asgardeo": asgardeo, "choreo-sme": choreo}, &fakeNotes{})

	raise(t, e, handoff("Apollo", "asgardeo", "asgardeo", testClock))
	raise(t, e, handoff("Apollo", "", "choreo", testClock.Add(time.Minute)))

	if len(asgardeo.posted) != 1 || asgardeo.posted[0].RecipientName != "s-a" {
		t.Errorf("Asgardeo space cards = %+v; want one, for s-a", asgardeo.posted)
	}
	if len(choreo.posted) != 1 || choreo.posted[0].RecipientName != "c-1" {
		t.Errorf("Choreo SME space cards = %+v; want one, for c-1", choreo.posted)
	}
	if len(shared.posted) != 0 {
		t.Errorf("%d cards in the shared SME space; with teamChats set it must get none", len(shared.posted))
	}
}

// With teamChats set, a team it does not list gets no card, and the
// incident's work note says why.
func TestSMETeamChats_ATeamNotListedGetsNoCard(t *testing.T) {
	shared, choreo, notes := &fakeChat{}, &fakeChat{}, &fakeNotes{}
	e := smeTeamChatsEngine(shared, map[string]*fakeChat{"choreo-sme": choreo}, notes)

	raise(t, e, handoff("Apollo", "asgardeo", "asgardeo", testClock))

	if len(shared.posted) != 0 || len(choreo.posted) != 0 {
		t.Errorf("cards posted for an unlisted team: shared %d, choreo %d; want none", len(shared.posted), len(choreo.posted))
	}
	got := notesWith(notes, "NO_CHAT_SPACE")
	if len(got) != 1 || !strings.Contains(got[0], "asgardeo") {
		t.Errorf("work notes = %v; want one NO_CHAT_SPACE note naming asgardeo", notes.notes)
	}
}

// Without teamChats every team still posts to the shared SME space.
func TestSMETeamChats_UnsetKeepsTheSharedSpace(t *testing.T) {
	shared := &fakeChat{}
	e := smeEngine(rotaReader(true), newMemStore(), &fakeNotes{}, &fakeChat{}, shared, smeOn)

	raise(t, e, handoff("Apollo", "asgardeo", "asgardeo", testClock))
	raise(t, e, handoff("Apollo", "", "choreo", testClock.Add(time.Minute)))

	if len(shared.posted) != 2 {
		t.Errorf("shared SME space cards = %d; want 2, one per team", len(shared.posted))
	}
}

// A team not listed keeps the SME page's calls; only its chat is left out.
func TestSMETeamChats_WithoutChatKeepsCalls(t *testing.T) {
	got := withoutChat([]notifier{voiceNotifier{}, chatNotifier{}, logNotifier{}})
	if len(got) != 2 {
		t.Fatalf("withoutChat = %#v; want the call and log notifiers", got)
	}
	for _, n := range got {
		if _, isChat := n.(chatNotifier); isChat {
			t.Error("withoutChat kept a chat notifier")
		}
	}
}

func TestSMETeamChats_Validation(t *testing.T) {
	for name, c := range map[string]struct {
		chats   map[string]Chat
		wantErr string
	}{
		"env names are fine": {map[string]Chat{"asgardeo": {WebhookURLEnv: "SME_CHAT_WEBHOOK_URL_ASGARDEO"}, "b-central": {Audience: "B-Central SME"}}, ""},
		"a URL is refused":   {map[string]Chat{"u2": {WebhookURLEnv: "https://chat.googleapis.com/v1/spaces/x/messages"}}, "NAME of an environment variable"},
		"a blank key":        {map[string]Chat{" ": {WebhookURLEnv: "X"}}, "no SME team key"},
		"a key listed twice": {map[string]Chat{"Asgardeo": {}, "asgardeo": {}}, "twice"},
	} {
		t.Run(name, func(t *testing.T) {
			s := SMEConfig{Enabled: true, Channel: ChannelChat, TeamChats: c.chats}
			err := s.validate()
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("validate: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("validate = %v; want an error containing %q", err, c.wantErr)
			}
		})
	}
}
