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
	"context"
	"strings"
	"testing"
	"time"
)

// handoffEngine is an SRE engine placing the SME page, with
// sre.smeHandoffChat on: Apollo has a space of its own, everyone else the
// shared SRE space.
func handoffEngine(shared, apollo *fakeChat, notes *fakeNotes) *Engine {
	e := smeEngine(rotaReader(true), newMemStore(), notes, &fakeChat{}, &fakeChat{}, smeOn)
	e.cfg.Ladder.SMEHandoffChat = true
	e.sharedRoom = &chatRoom{chat: shared, room: "SRE"}
	e.teamRooms = map[string]chatRoom{"apollo": {chat: apollo, room: "Apollo SRE"}}
	return e
}

// The press tells the incident's SRE team space, once, that the SMEs are
// being paged and SRE paging stopped.
func TestSMEHandoffChat_TellsTheSRETeamSpaceOnce(t *testing.T) {
	shared, apollo := &fakeChat{}, &fakeChat{}
	e := handoffEngine(shared, apollo, &fakeNotes{})

	p := handoff("Apollo", "asgardeo", "asgardeo", testClock)
	p.Priority = "P1"
	raise(t, e, p)
	raise(t, e, handoff("Apollo", "asgardeo", "asgardeo", testClock.Add(time.Minute))) // same team again: ignored

	if len(apollo.handoffs) != 1 || len(shared.handoffs) != 0 {
		t.Fatalf("handoff messages: apollo %d, shared %d; want one, in Apollo's space", len(apollo.handoffs), len(shared.handoffs))
	}
	h := apollo.handoffs[0]
	if h.Audience != "Apollo SRE" || !h.Paging || h.By != "lead@example.com" || h.IncidentRef != "INC0099001" {
		t.Errorf("handoff = %+v", h)
	}
	if h.Priority != "P1" {
		t.Errorf("priority = %q; want the incident's, P1", h.Priority)
	}
	if h.SMETeam != "Asgardeo" {
		t.Errorf("SME team = %q; want the SME team's name, Asgardeo", h.SMETeam)
	}
	if h.ThreadKey != "incident-escalation-"+testIncidentID {
		t.Errorf("thread = %q; want the incident's ladder thread", h.ThreadKey)
	}
}

// Another SRE team uses the shared space; nothing is posted when the setting
// is off, or when the press pages no SME (not a SaaS SRE incident).
func TestSMEHandoffChat_WhereAndWhen(t *testing.T) {
	t.Run("artemis uses the shared space", func(t *testing.T) {
		shared, apollo := &fakeChat{}, &fakeChat{}
		e := handoffEngine(shared, apollo, &fakeNotes{})
		raise(t, e, handoff("Artemis", "asgardeo", "asgardeo", testClock))
		if len(shared.handoffs) != 1 || len(apollo.handoffs) != 0 {
			t.Errorf("handoff messages: shared %d, apollo %d; want one in the shared space", len(shared.handoffs), len(apollo.handoffs))
		}
	})
	t.Run("off", func(t *testing.T) {
		shared, apollo := &fakeChat{}, &fakeChat{}
		e := handoffEngine(shared, apollo, &fakeNotes{})
		e.cfg.Ladder.SMEHandoffChat = false
		raise(t, e, handoff("Apollo", "asgardeo", "asgardeo", testClock))
		if len(apollo.handoffs)+len(shared.handoffs) != 0 {
			t.Error("a handoff message was posted with sre.smeHandoffChat off")
		}
	})
	t.Run("not a SaaS SRE incident", func(t *testing.T) {
		shared, apollo := &fakeChat{}, &fakeChat{}
		e := handoffEngine(shared, apollo, &fakeNotes{})
		raise(t, e, handoff("SRE IaaS", "asgardeo", "asgardeo", testClock))
		if len(apollo.handoffs)+len(shared.handoffs) != 0 {
			t.Error("a handoff message was posted for an escalation that pages no SME")
		}
	})
}

// An SME ladder that reaches its end unanswered posts the closing message to
// the SME team's own space.
func TestSMEUnansweredChat_PostsToTheSMETeamsSpace(t *testing.T) {
	asgardeo, notes := &fakeChat{}, &fakeNotes{}
	e := smeEngine(rotaReader(true), newMemStore(), notes, &fakeChat{}, &fakeChat{}, smeOn)
	e.cfg.SME.UnansweredChat = true
	e.smeRooms = map[string]chatRoom{"asgardeo": {chat: asgardeo, room: "Asgardeo SME"}}

	raise(t, e, handoff("Apollo", "asgardeo", "asgardeo", testClock))
	for _, m := range []time.Duration{5, 10} {
		if err := e.Tick(context.Background(), testClock.Add(m*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}

	if len(asgardeo.unanswered) != 1 {
		t.Fatalf("closing messages in the Asgardeo SME space = %d; want 1", len(asgardeo.unanswered))
	}
	u := asgardeo.unanswered[0]
	if u.Chain != "Asgardeo SME" || !strings.HasPrefix(strings.Join(u.Called, ","), "L1 s-a") {
		t.Errorf("closing message = %+v", u)
	}

	// Off: nothing.
	other := &fakeChat{}
	e2 := smeEngine(rotaReader(true), newMemStore(), &fakeNotes{}, &fakeChat{}, &fakeChat{}, smeOn)
	e2.smeRooms = map[string]chatRoom{"asgardeo": {chat: other, room: "Asgardeo SME"}}
	raise(t, e2, handoff("Apollo", "asgardeo", "asgardeo", testClock))
	if err := e2.Tick(context.Background(), testClock.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if len(other.unanswered) != 0 {
		t.Error("an SME closing message was posted with sme.unansweredChat off")
	}
}

func TestSMETeamLabel(t *testing.T) {
	for key, want := range map[string]string{"asgardeo": "Asgardeo", "choreo-runtime": "Choreo Runtime", "b-central": "B-Central", "u2": "U2", "cloud-core": "Cloud Core"} {
		if got := smeTeamLabel(key); got != want {
			t.Errorf("smeTeamLabel(%q) = %q; want %q", key, got, want)
		}
	}
}
