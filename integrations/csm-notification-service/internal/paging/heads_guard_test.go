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
	"errors"
	"testing"
)

func call(level Level, email, phone string) PlannedCall {
	return PlannedCall{Level: level, Recipient: Recipient{Name: email, Email: email, Phone: phone}}
}

// noIssues: setup holds never reach the work note.
func noIssues(t *testing.T, p Plan) {
	t.Helper()
	if len(p.Issues) != 0 {
		t.Errorf("issues = %v; a hold must not be written to the work note", p.Issues)
	}
}

func levelsOf(calls []PlannedCall) []Level {
	out := make([]Level, 0, len(calls))
	for _, c := range calls {
		out = append(out, c.Level)
	}
	return out
}

func TestApplySafety_HeadsGuard(t *testing.T) {
	ctx := context.Background()
	heads := []PlannedCall{
		call(Level3, "cre.head@example.com", "+94770000003"),
		call(Level4, "cs.head@example.com", "+94770000004"),
	}

	t.Run("call channel: nobody below is callable, so the heads are held", func(t *testing.T) {
		// What BuildPlan leaves on a call-only ladder when every lower-tier
		// recipient was NO_NUMBER: just the heads.
		e := &Engine{cfg: EngineConfig{Channel: ChannelCall}}
		plan := Plan{Calls: append([]PlannedCall{}, heads...)}
		e.applySafety(ctx, &plan)
		if len(plan.Calls) != 0 {
			t.Errorf("calls left = %v, want none", levelsOf(plan.Calls))
		}
		noIssues(t, plan)
	})

	t.Run("call channel: one lower-tier number lets the heads ring", func(t *testing.T) {
		e := &Engine{cfg: EngineConfig{Channel: ChannelCall}}
		plan := Plan{Calls: append([]PlannedCall{call(Level1, "lead@example.com", "+94770000001")}, heads...)}
		e.applySafety(ctx, &plan)
		if len(plan.Calls) != 3 {
			t.Errorf("calls = %v, want all three", levelsOf(plan.Calls))
		}
		for _, c := range plan.Calls {
			if c.HoldCall {
				t.Errorf("%s held although a lead below is callable", c.Level)
			}
		}
	})

	t.Run("both channels: the heads' cards stay, their calls go", func(t *testing.T) {
		e := &Engine{cfg: EngineConfig{Channel: ChannelBoth}}
		plan := Plan{Calls: append([]PlannedCall{
			call(Level0, "fr@example.com", ""), call(Level2, "lead@example.com", ""),
		}, heads...)}
		e.applySafety(ctx, &plan)
		if len(plan.Calls) != 4 {
			t.Fatalf("calls = %v, want every entry kept for its chat card", levelsOf(plan.Calls))
		}
		for _, c := range plan.Calls {
			if c.Level >= Level3 && !c.HoldCall {
				t.Errorf("%s call not held; it would ring", c.Level)
			}
		}
		noIssues(t, plan)
	})

	t.Run("a lower-tier number the allowlist refuses does not count", func(t *testing.T) {
		e := &Engine{cfg: EngineConfig{Channel: ChannelCall, Ladder: LadderConfig{
			Safety: Safety{AllowedNumbers: []string{"+94770000003", "+94770000004"}}}}}
		plan := Plan{Calls: append([]PlannedCall{call(Level0, "fr@example.com", "+94770000009")}, heads...)}
		e.applySafety(ctx, &plan)
		if len(plan.Calls) != 0 {
			t.Errorf("calls = %v, want the heads held", levelsOf(plan.Calls))
		}
	})

	t.Run("the switch lets the heads ring anyway", func(t *testing.T) {
		e := &Engine{cfg: EngineConfig{Channel: ChannelCall, Ladder: LadderConfig{
			Safety: Safety{CallHeadsWithoutLowerTiers: true}}}}
		plan := Plan{Calls: append([]PlannedCall{}, heads...)}
		e.applySafety(ctx, &plan)
		if len(plan.Calls) != 2 || plan.Calls[0].HoldCall || plan.Calls[1].HoldCall {
			t.Errorf("calls = %v, want both heads ringing", levelsOf(plan.Calls))
		}
	})

	t.Run("chat and log ladders place no calls, so nothing is held", func(t *testing.T) {
		for _, ch := range []Channel{ChannelChat, ChannelLog} {
			e := &Engine{cfg: EngineConfig{Channel: ch}}
			plan := Plan{Calls: append([]PlannedCall{}, heads...)}
			e.applySafety(ctx, &plan)
			if len(plan.Calls) != 2 || plan.Calls[0].HoldCall || plan.CallsHeld {
				t.Errorf("%s: calls = %v changed", ch, levelsOf(plan.Calls))
			}
		}
	})
}

// poolResolver is a LeadPoolResolver with a fixed pool.
type poolResolver struct {
	StaticResolver
	pool []Recipient
	err  error
}

func (p poolResolver) LeadPool(context.Context) ([]Recipient, error) { return p.pool, p.err }

func leadsNotVerified(p Plan) bool { return p.CallsHeld }

func TestApplySafety_VerifiedLeadsGate(t *testing.T) {
	ctx := context.Background()
	calls := func() []PlannedCall {
		return []PlannedCall{
			call(Level0, "fr@example.com", "+94770000001"),
			call(Level1, "lead@example.com", "+94770000002"),
			call(Level3, "cre.head@example.com", "+94770000003"),
		}
	}
	full := []Recipient{
		{Name: "Lead A", Email: "a@example.com", Phone: "+94770000011"},
		{Name: "Lead B", Email: "b@example.com", Phone: "+94770000012"},
	}
	oneMissing := []Recipient{full[0], {Name: "Lead B", Email: "b@example.com"}}

	run := func(ch Channel, r Resolver, safety Safety) Plan {
		e := &Engine{resolver: r, cfg: EngineConfig{Channel: ch, Ladder: LadderConfig{Safety: safety}}}
		plan := Plan{Calls: calls()}
		e.applySafety(ctx, &plan)
		return plan
	}

	t.Run("every lead has a number: calls go ahead", func(t *testing.T) {
		plan := run(ChannelCall, poolResolver{pool: full}, Safety{})
		if len(plan.Calls) != 3 || leadsNotVerified(plan) {
			t.Errorf("calls = %v held=%v", levelsOf(plan.Calls), leadsNotVerified(plan))
		}
	})

	t.Run("one lead without a number holds every call", func(t *testing.T) {
		plan := run(ChannelCall, poolResolver{pool: oneMissing}, Safety{})
		if len(plan.Calls) != 0 || !leadsNotVerified(plan) {
			t.Errorf("calls = %v held=%v, want none and the plan held", levelsOf(plan.Calls), leadsNotVerified(plan))
		}
		noIssues(t, plan)
	})

	t.Run("a non-E.164 lead number does not count", func(t *testing.T) {
		pool := []Recipient{full[0], {Name: "Lead B", Email: "b@example.com", Phone: "0770000012"}}
		if plan := run(ChannelCall, poolResolver{pool: pool}, Safety{}); !leadsNotVerified(plan) {
			t.Error("a local-format number verified the pool")
		}
	})

	t.Run("an empty or unreadable pool is not verified", func(t *testing.T) {
		if plan := run(ChannelCall, poolResolver{}, Safety{}); len(plan.Calls) != 0 || !leadsNotVerified(plan) {
			t.Errorf("empty pool: calls = %v", levelsOf(plan.Calls))
		}
		if plan := run(ChannelCall, poolResolver{err: errors.New("entity down")}, Safety{}); len(plan.Calls) != 0 {
			t.Errorf("unreadable pool: calls = %v, want none", levelsOf(plan.Calls))
		}
	})

	t.Run("both channels keep the cards and hold every call", func(t *testing.T) {
		plan := run(ChannelBoth, poolResolver{pool: oneMissing}, Safety{})
		if len(plan.Calls) != 3 {
			t.Fatalf("calls = %v, want every entry kept for its chat card", levelsOf(plan.Calls))
		}
		for _, c := range plan.Calls {
			if !c.HoldCall {
				t.Errorf("%s call not held", c.Level)
			}
		}
		noIssues(t, plan)
	})

	t.Run("the switch places calls without a verified pool", func(t *testing.T) {
		plan := run(ChannelCall, poolResolver{pool: oneMissing}, Safety{CallWithoutVerifiedLeads: true})
		if len(plan.Calls) != 3 || leadsNotVerified(plan) {
			t.Errorf("calls = %v held=%v", levelsOf(plan.Calls), leadsNotVerified(plan))
		}
	})

	t.Run("a resolver without a lead pool is not checked", func(t *testing.T) {
		plan := run(ChannelCall, StaticResolver{}, Safety{})
		if len(plan.Calls) != 3 || leadsNotVerified(plan) {
			t.Errorf("calls = %v held=%v", levelsOf(plan.Calls), leadsNotVerified(plan))
		}
	})

	t.Run("profile numbers verify the pool", func(t *testing.T) {
		inner := poolResolver{pool: []Recipient{{Name: "Lead A", Email: "a@example.com"}}}
		r := NewProfilePhoneResolver(inner, &fakeLookup{numbers: map[string]string{"a@example.com": "+94770000011"}})
		if plan := run(ChannelCall, r, Safety{}); leadsNotVerified(plan) {
			t.Error("a pool whose numbers come from profiles was not verified")
		}
		r = NewProfilePhoneResolver(StaticResolver{}, &fakeLookup{})
		if plan := run(ChannelCall, r, Safety{}); leadsNotVerified(plan) {
			t.Error("a profile wrapper around a pool-less resolver must not hold calls")
		}
	})
}

// Through the real engine: a call-only ladder held by an unverified lead pool
// schedules nothing and writes no work note -- it is setup, not an outcome.
func TestEngine_UnverifiedLeadsHoldQuietly(t *testing.T) {
	store, caller, notes := newMemStore(), &fakeCaller{}, &fakeNotes{}
	e := testEngine(store, caller, notes, enabled())
	e.resolver = poolResolver{StaticResolver: fullResolver(),
		pool: []Recipient{{Name: "Lead", Email: "lead@example.com"}}}
	if err := e.Handle(context.Background(), createdEvent(t, "CRITICAL", testClock)); err != nil {
		t.Fatal(err)
	}
	if len(store.wakes) != 0 {
		t.Errorf("%d calls scheduled; a held ladder schedules none", len(store.wakes))
	}
	if len(notes.notes) != 0 {
		t.Errorf("work notes written = %d; a held ladder writes none", len(notes.notes))
	}
}

// A held call is skipped at delivery; nothing is dialled for it.
func TestEngine_HeldCallIsNotDialled(t *testing.T) {
	caller := &fakeCaller{}
	e := testEngine(newMemStore(), caller, &fakeNotes{}, enabled())
	c := call(Level3, "cre.head@example.com", "+94770000003")
	c.HoldCall = true
	if err := e.place(context.Background(), Plan{Calls: []PlannedCall{c}}, c); err != nil {
		t.Fatal(err)
	}
	if len(caller.placed) != 0 {
		t.Errorf("dialled %d calls for a held entry", len(caller.placed))
	}
	c.HoldCall = false
	_ = e.place(context.Background(), Plan{Calls: []PlannedCall{c}}, c)
	if len(caller.placed) != 1 {
		t.Errorf("an unheld entry dialled %d calls, want 1", len(caller.placed))
	}
}
