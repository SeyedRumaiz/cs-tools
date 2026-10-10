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

package service

import (
	"testing"
	"time"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
)

func at(s string) *time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return &t
}

func outage(phase domain.OutageNotificationPhase, end *time.Time) domain.OutageForNotification {
	o := domain.OutageForNotification{OutageID: "o1", Number: "OUT0001234", EndOn: end}
	o.State = &domain.OutageNotificationState{OutageID: "o1", Phase: phase}
	return o
}

// The three arms, in the original's order.
func TestDecideOutageNotification_TheThreeBranches(t *testing.T) {
	tests := []struct {
		name   string
		phase  domain.OutageNotificationPhase
		end    *time.Time
		want   domain.OutageNotificationKind
		reason string
	}{
		{"never notified, still running", domain.OutageNotificationPhaseNone, nil,
			domain.OutageNotificationDeclared, "Declaration (The First Email)"},
		{"declared and still running", domain.OutageNotificationPhaseDeclared, nil,
			domain.OutageNotificationUpdate, "Update (Everything In-Between)"},
		{"declared and now ended", domain.OutageNotificationPhaseDeclared, at("2026-09-25T10:00:00Z"),
			domain.OutageNotificationResolved, "Resolution (The Final Email)"},
		{"already resolved and ended", domain.OutageNotificationPhaseResolved, at("2026-09-25T10:00:00Z"),
			domain.OutageNotificationNone, "no branch matched"},
		// Resolved but End cleared: arm 4 needs End, arm 7 needs Declared.
		// Neither matches, so nothing is sent — same as the original.
		{"resolved with end cleared", domain.OutageNotificationPhaseResolved, nil,
			domain.OutageNotificationNone, "no branch matched"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := decideOutageNotification(outage(tc.phase, tc.end))
			if got.Kind != tc.want {
				t.Fatalf("Kind = %q, want %q", got.Kind, tc.want)
			}
			if got.Reason != tc.reason {
				t.Errorf("Reason = %q, want %q", got.Reason, tc.reason)
			}
		})
	}
}

// Arm 1 wins over arm 4 when both would match. An outage that is ALREADY OVER
// when notification is switched on still gets "declared" first — the ordering
// consequence, reproduced rather than tidied away.
func TestDecideOutageNotification_AnAlreadyEndedOutageIsDeclaredFirst(t *testing.T) {
	d := decideOutageNotification(outage(domain.OutageNotificationPhaseNone, at("2026-09-25T10:00:00Z")))
	if d.Kind != domain.OutageNotificationDeclared {
		t.Fatalf("Kind = %q, want DECLARED — arm 1 must win over arm 4", d.Kind)
	}
	// And the next evaluation resolves it. In ServiceNow this needs a second
	// record update that may never come; the sweep guarantees it.
	next := decideOutageNotification(outage(domain.OutageNotificationPhaseDeclared, at("2026-09-25T10:00:00Z")))
	if next.Kind != domain.OutageNotificationResolved {
		t.Fatalf("second evaluation Kind = %q, want RESOLVED", next.Kind)
	}
}

// The guard on arm 4. Without `phase is not Resolved`, every change after the
// outage ends would re-send the resolution email.
func TestDecideOutageNotification_ResolutionIsNotResent(t *testing.T) {
	for i := 0; i < 3; i++ {
		d := decideOutageNotification(outage(domain.OutageNotificationPhaseResolved, at("2026-09-25T10:00:00Z")))
		if d.Kind != domain.OutageNotificationNone {
			t.Fatalf("evaluation %d sent %q; the resolution must go out once", i, d.Kind)
		}
	}
}

// The guard on arm 7. Without `End is empty`, update emails would continue
// after the outage is over.
func TestDecideOutageNotification_NoUpdatesAfterItEnds(t *testing.T) {
	d := decideOutageNotification(outage(domain.OutageNotificationPhaseDeclared, at("2026-09-25T10:00:00Z")))
	if d.Kind == domain.OutageNotificationUpdate {
		t.Fatal("an ended outage must not emit update emails")
	}
}

// With no local state, the phase is seeded from ServiceNow's mirrored value.
// Without this, a cutover re-declares every outage ServiceNow already
// announced.
func TestDecideOutageNotification_SeedsFromTheSyncedPhase(t *testing.T) {
	o := domain.OutageForNotification{
		OutageID: "o1", Number: "OUT0001234",
		SyncedPhase: domain.OutageNotificationPhaseResolved,
		EndOn:       at("2026-09-25T10:00:00Z"),
	}
	if d := decideOutageNotification(o); d.Kind != domain.OutageNotificationNone {
		t.Fatalf("Kind = %q, want nothing — ServiceNow already resolved this one", d.Kind)
	}

	o.SyncedPhase = domain.OutageNotificationPhaseDeclared
	o.EndOn = nil
	if d := decideOutageNotification(o); d.Kind != domain.OutageNotificationUpdate {
		t.Fatalf("Kind = %q, want UPDATE — ServiceNow already declared this one", d.Kind)
	}
}

// Our own state beats the mirrored value: once we have sent something, the
// sync column is stale by definition.
func TestDecideOutageNotification_LocalStateWinsOverTheSyncedPhase(t *testing.T) {
	o := outage(domain.OutageNotificationPhaseResolved, at("2026-09-25T10:00:00Z"))
	o.SyncedPhase = domain.OutageNotificationPhaseNone // stale
	if d := decideOutageNotification(o); d.Kind != domain.OutageNotificationNone {
		t.Fatalf("Kind = %q, want nothing — we have already resolved this", d.Kind)
	}
}

// The wording is the ServiceNow original's, exactly. Three fixed sentences
// carrying no outage detail: faithful to a stub on purpose.
func TestRenderOutageNotification_MatchesTheOriginalWording(t *testing.T) {
	o := domain.OutageForNotification{Number: "OUT0001234"}
	for _, tc := range []struct {
		kind    domain.OutageNotificationKind
		subject string
		body    string
	}{
		{domain.OutageNotificationDeclared, "[Outage] OUT0001234 - Declared", "Outage OUT0001234 declared."},
		{domain.OutageNotificationResolved, "[Outage] OUT0001234 - Resolved", "Outage OUT0001234 resolved."},
		{domain.OutageNotificationUpdate, "[Outage] OUT0001234 - Update", "Outage OUT0001234 update."},
	} {
		s, b := renderOutageNotification(tc.kind, o)
		if s != tc.subject {
			t.Errorf("%s subject = %q, want %q", tc.kind, s, tc.subject)
		}
		if b != tc.body {
			t.Errorf("%s body = %q, want %q", tc.kind, b, tc.body)
		}
	}
}

// The update arm writes no phase — that is what lets one outage emit many
// update emails, and it is the original's behaviour.
func TestPhaseAfter_EveryArmHasAWellDefinedResultingPhase(t *testing.T) {
	if p := phaseAfter(domain.OutageNotificationDeclared); p != domain.OutageNotificationPhaseDeclared {
		t.Errorf("declared -> %q", p)
	}
	if p := phaseAfter(domain.OutageNotificationResolved); p != domain.OutageNotificationPhaseResolved {
		t.Errorf("resolved -> %q", p)
	}
	// The update arm leaves the phase at Declared rather than reporting "no
	// change" — writing "unchanged" into a first row stored NONE and
	// re-declared the outage on the next sweep.
	if p := phaseAfter(domain.OutageNotificationUpdate); p != domain.OutageNotificationPhaseDeclared {
		t.Errorf("update -> %q, want DECLARED (unchanged, but known)", p)
	}
}

// *** THE UPDATE ARM FIRES ONCE PER CHANGE, NOT ONCE PER SWEEP. ***
//
// ServiceNow ran the flow "For each unique change" on the outage record. A
// sweep inherits no such trigger: it re-reads every declared, unended outage
// on every tick. Without a guard, one open outage mails the whole internal
// list twelve times an hour at the default */5 schedule.
//
// Each case below pins one half of the rule, and the first two are the ones
// that regress silently — a notifier that repeats is noisy, but a notifier
// that goes quiet is nearly invisible.
func TestDecideOutageNotification_UpdateOnlyAfterTheOutageChanges(t *testing.T) {
	declared := at("2026-09-30T08:00:00Z")

	withTimes := func(updatedOn, declaredOn, lastUpdateOn *time.Time) domain.OutageForNotification {
		o := outage(domain.OutageNotificationPhaseDeclared, nil)
		o.UpdatedOn = updatedOn
		o.State.DeclaredOn = declaredOn
		o.State.LastUpdateOn = lastUpdateOn
		return o
	}

	tests := []struct {
		name     string
		outage   domain.OutageForNotification
		wantKind domain.OutageNotificationKind
	}{
		{
			// The whole bug: a sweep five minutes later, nothing edited.
			name:     "unchanged since the declaration sends nothing",
			outage:   withTimes(at("2026-09-30T07:59:00Z"), declared, nil),
			wantKind: domain.OutageNotificationNone,
		},
		{
			name:     "edited after the declaration earns one update",
			outage:   withTimes(at("2026-09-30T08:30:00Z"), declared, nil),
			wantKind: domain.OutageNotificationUpdate,
		},
		{
			// Compared against what we last SAID, not when we last looked.
			name:     "unchanged since the last update sends nothing",
			outage:   withTimes(at("2026-09-30T08:30:00Z"), declared, at("2026-09-30T09:00:00Z")),
			wantKind: domain.OutageNotificationNone,
		},
		{
			name:     "edited again after the last update earns another",
			outage:   withTimes(at("2026-09-30T09:30:00Z"), declared, at("2026-09-30T09:00:00Z")),
			wantKind: domain.OutageNotificationUpdate,
		},
		{
			// An edit landing exactly on the recorded instant is NOT a change.
			// After() is strict, and equal timestamps are what a same-second
			// write produces.
			name:     "an edit at the same instant is not a change",
			outage:   withTimes(declared, declared, nil),
			wantKind: domain.OutageNotificationNone,
		},
		{
			// Fail open: the mirror leaves updated_on nullable, and silence
			// would be permanent for that outage.
			name:     "a missing updated_on still sends",
			outage:   withTimes(nil, declared, nil),
			wantKind: domain.OutageNotificationUpdate,
		},
		{
			// Fail open: a phase seeded from ServiceNow carries no timestamps
			// of ours.
			name:     "a state row with no timestamps still sends",
			outage:   withTimes(at("2026-09-30T08:30:00Z"), nil, nil),
			wantKind: domain.OutageNotificationUpdate,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := decideOutageNotification(tc.outage)
			if got.Kind != tc.wantKind {
				t.Fatalf("Kind = %q, want %q (reason %q)", got.Kind, tc.wantKind, got.Reason)
			}
		})
	}
}
