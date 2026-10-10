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

// defaultGroupResolver is the SRE resolver with "Default" as a default group.
func defaultGroupResolver() TeamScheduleResolver {
	teams := sreTeams
	teams.DefaultGroups = []string{"Default"}
	return NewTeamScheduleResolver(morningRota(), teams, nil)
}

// An incident in the "Default" group climbs the SRE ladder, each rung
// reaching whoever holds that tier on the live SaaS window.
func TestDefaultGroup_ClimbsTheSRELadderFromWhoeverIsOnDuty(t *testing.T) {
	ctx := context.Background()
	r := defaultGroupResolver()
	rc := RoutingContext{AssignedCRETeam: "default"}

	if l, err := r.LadderFor(ctx, rc); err != nil || l != LadderSRE {
		t.Fatalf("LadderFor(Default) = %q, %v; want the SRE ladder", l, err)
	}
	if f, err := r.TeamFamily(ctx, rc); err != nil || f != TeamFamilySRE {
		t.Fatalf("TeamFamily(Default) = %q, %v; want sre", f, err)
	}
	for _, c := range []struct {
		level Level
		want  string
	}{
		{Level0, "a-l1@example.com"}, // both teams hold L1: apollo first in sre.teams.abts
		{Level1, "a-l2@example.com"},
		{Level2, "r-l3@example.com"}, // only artemis has an L3 today
	} {
		got := resolveSRE(t, r, c.level, "Default")
		if len(got) != 1 || got[0] != c.want {
			t.Errorf("%s = %v; want [%s]", c.level, got, c.want)
		}
	}

	// An alias on the group still leaves it a default group, read from
	// whoever is on duty rather than from the alias's team.
	aliased := sreTeams
	aliased.DefaultGroups = []string{"Default"}
	aliased.Aliases = map[string]string{"Default": "default-sre"}
	ar := NewTeamScheduleResolver(morningRota(), aliased, nil)
	if l, err := ar.LadderFor(ctx, rc); err != nil || l != LadderSRE {
		t.Errorf("LadderFor(aliased Default) = %q, %v; want the SRE ladder", l, err)
	}
	if f, err := ar.TeamFamily(ctx, rc); err != nil || f != TeamFamilySRE {
		t.Errorf("TeamFamily(aliased Default) = %q, %v; want sre", f, err)
	}
	if own := ar.ownSRETeam(rc); own != "" {
		t.Errorf("ownSRETeam(aliased Default) = %q; want none, so the on-duty rota answers", own)
	}
	if got := resolveSRE(t, ar, Level0, "Default"); len(got) != 1 || got[0] != "a-l1@example.com" {
		t.Errorf("L1 for aliased Default = %v; want [a-l1@example.com]", got)
	}

	// Without the setting, "Default" is nobody's team, as before.
	plain := sreResolver(morningRota())
	if l, _ := plain.LadderFor(ctx, rc); l != LadderCRE {
		t.Errorf("LadderFor(Default) without defaultGroups = %q; want the CRE fallback, as before", l)
	}
}

// sreClosingEngine is an SRE engine whose ladder posts the closing message.
func sreClosingEngine(chat *fakeChat, store *memStore, team *fakeChat) *Engine {
	e := ladderEngine(LadderSRE, chat, store)
	e.cfg.Ladder.UnansweredChat = true
	e.sharedRoom = &chatRoom{chat: chat, room: "SRE"}
	if team != nil {
		e.teamRooms = map[string]chatRoom{"apollo": {chat: team, room: "Apollo SRE"}}
	}
	return e
}

// run delivers the incident and ticks through every rung.
func runUnanswered(t *testing.T, e *Engine, team string) {
	t.Helper()
	ctx := context.Background()
	if err := e.Handle(ctx, created(t, team, "HIGH")); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := e.Tick(ctx, testClock.Add(time.Duration(i)*5*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
}

// L1, L2 and L3 unanswered: the calls stop and the team's space is told so.
func TestUnansweredChat_PostsToTheTeamsOwnSpace(t *testing.T) {
	shared, apollo, store := &fakeChat{}, &fakeChat{}, newMemStore()
	e := sreClosingEngine(shared, store, apollo)

	runUnanswered(t, e, "Apollo")

	if _, found, _ := store.Get(context.Background(), testIncidentID); found {
		t.Fatal("the ladder should be over after its last rung")
	}
	if len(apollo.unanswered) != 1 {
		t.Fatalf("Apollo space closing messages = %d; want 1", len(apollo.unanswered))
	}
	if len(shared.unanswered) != 0 {
		t.Errorf("the shared space got %d closing messages; the team's own space should", len(shared.unanswered))
	}
	u := apollo.unanswered[0]
	if u.Audience != "Apollo SRE" || u.Team != "Apollo" || u.IncidentRef != "INC0099001" {
		t.Errorf("closing message = %+v", u)
	}
	if got := strings.Join(u.Called, ", "); got != "L1 a-l1, L2 a-l2, L3 r-l3" {
		t.Errorf("called = %q; want the three rungs in order", got)
	}
	if u.ThreadKey != "incident-escalation-"+testIncidentID {
		t.Errorf("thread = %q; want the ladder's own thread", u.ThreadKey)
	}
}

// A team with no space of its own uses the ladder's shared space.
func TestUnansweredChat_FallsBackToTheSharedSpace(t *testing.T) {
	shared, apollo := &fakeChat{}, &fakeChat{}
	e := sreClosingEngine(shared, newMemStore(), apollo)

	runUnanswered(t, e, "Artemis")

	if len(shared.unanswered) != 1 || len(apollo.unanswered) != 0 {
		t.Fatalf("closing messages: shared %d, apollo %d; want 1 in the shared space", len(shared.unanswered), len(apollo.unanswered))
	}
}

// Off by default, and never after someone answered.
func TestUnansweredChat_OnlyWhenOnAndUnanswered(t *testing.T) {
	t.Run("off", func(t *testing.T) {
		chat := &fakeChat{}
		e := ladderEngine(LadderSRE, chat, newMemStore())
		e.sharedRoom = &chatRoom{chat: chat, room: "SRE"}
		runUnanswered(t, e, "Apollo")
		if len(chat.unanswered) != 0 {
			t.Errorf("%d closing messages with unansweredChat off; want none", len(chat.unanswered))
		}
	})
	t.Run("answered", func(t *testing.T) {
		ctx := context.Background()
		chat := &fakeChat{}
		e := sreClosingEngine(chat, newMemStore(), nil)
		if err := e.Handle(ctx, created(t, "Apollo", "HIGH")); err != nil {
			t.Fatal(err)
		}
		if err := e.Tick(ctx, testClock); err != nil {
			t.Fatal(err)
		}
		if err := e.Handle(ctx, assigned(t)); err != nil {
			t.Fatal(err)
		}
		if err := e.Tick(ctx, testClock.Add(10*time.Minute)); err != nil {
			t.Fatal(err)
		}
		if len(chat.unanswered) != 0 {
			t.Errorf("%d closing messages after an assignment; want none", len(chat.unanswered))
		}
	})
}

// teamChats sends a team's rung cards to its own space; other teams keep the
// ladder's own.
func TestTeamChats_RungCardsGoToTheTeamsSpace(t *testing.T) {
	shared, apollo := &fakeChat{}, &fakeChat{}
	e := ladderEngine(LadderSRE, shared, newMemStore())
	e.teamNotifiers = map[string][]notifier{"apollo": {chatNotifier{chat: apollo, links: fakeLinks{}, audience: "Apollo SRE"}}}
	e.cfg.Ladder.Teams.Aliases = sreTeams.Aliases // as cmd/server passes the sre section

	runUnanswered(t, e, "SRE - Apollo") // an alias of apollo
	if len(apollo.posted) != 3 || len(shared.posted) != 0 {
		t.Fatalf("rung cards: apollo %d, shared %d; want all 3 in Apollo's space", len(apollo.posted), len(shared.posted))
	}

	other := &fakeChat{}
	e2 := ladderEngine(LadderSRE, other, newMemStore())
	e2.teamNotifiers = map[string][]notifier{"apollo": {chatNotifier{chat: apollo, links: fakeLinks{}}}}
	runUnanswered(t, e2, "Artemis")
	if len(other.posted) != 3 {
		t.Errorf("Artemis rung cards in the shared space = %d; want 3", len(other.posted))
	}
}

func TestConfig_DefaultGroupsAndTeamChats(t *testing.T) {
	ok := `
enabled: true
sre:
  enabled: true
  channel: call
  unansweredChat: true
  chat:
    webhookUrlEnv: SRE_CHAT_WEBHOOK_URL
  teamChats:
    apollo: { webhookUrlEnv: SRE_CHAT_WEBHOOK_URL_APOLLO, audience: "Apollo SRE" }
  teams:
    abts: [apollo, artemis]
    defaultGroups: [Default]
`
	cfg, err := loadYAML(t, ok)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !cfg.SRE.UnansweredChat || len(cfg.SRE.TeamChats) != 1 || len(cfg.SRE.Teams.DefaultGroups) != 1 {
		t.Errorf("sre = %+v", cfg.SRE)
	}

	for name, c := range map[string]struct{ body, want string }{
		"a URL in teamChats":                  {strings.Replace(ok, "SRE_CHAT_WEBHOOK_URL_APOLLO", "https://chat.googleapis.com/x", 1), "NAME of an environment variable"},
		"the same team twice in teamChats":    {strings.Replace(ok, "  teams:\n", "    Apollo: { webhookUrlEnv: SRE_CHAT_WEBHOOK_URL_APOLLO2, audience: \"Apollo 2\" }\n  teams:\n", 1), "lists team \"apollo\" twice"},
		"a default group that is an SRE team": {strings.Replace(ok, "defaultGroups: [Default]", "defaultGroups: [apollo]", 1), "both teams.abts and teams.defaultGroups"},
		"teamChats on CRE": {`
enabled: true
cre:
  enabled: true
  unansweredChat: true
`, "SRE settings"},
		"defaultGroups on CRE": {`
enabled: true
cre:
  enabled: true
  teams:
    defaultGroups: [Default]
`, "SRE setting"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := loadYAML(t, c.body); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("load = %v; want an error containing %q", err, c.want)
			}
		})
	}
}
