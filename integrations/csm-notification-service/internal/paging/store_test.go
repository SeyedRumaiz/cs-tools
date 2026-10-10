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
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// The engine's own tests run against an in-memory ladderStore, which keeps
// them about the ladder's behaviour rather than about go-redis. These cover
// the other half: that Store actually implements that contract against a real
// Redis — in particular Create's create-if-absent semantics, which is the
// thing standing between a redelivered incident.created and a ladder that
// re-dials everyone from LEVEL_0.
//
// Skipped unless a Redis is reachable, so `go test ./...` stays dependency-free
// on a laptop and in CI. To run them:
//
//	docker run --rm -p 6379:6379 redis
//	go test ./internal/paging/ -run TestStore -v
//
// REDIS_URL (a rediss:// connection string, as main.go reads it) takes
// precedence, then REDIS_ADDR — the same order the service itself uses, so
// these can run against the managed instance a deployment will actually use
// and not only a local container. Every key they write is namespaced and
// removed on the way out.
func testStore(t *testing.T) (*Store, func()) {
	t.Helper()
	var rdb *redis.Client
	addr := "localhost:6379"
	if url := os.Getenv("REDIS_URL"); url != "" {
		opts, err := redis.ParseURL(url)
		if err != nil {
			t.Skip("REDIS_URL is set but does not parse; skipping")
		}
		addr = opts.Addr
		rdb = redis.NewClient(opts)
	} else {
		if a := os.Getenv("REDIS_ADDR"); a != "" {
			addr = a
		}
		rdb = redis.NewClient(&redis.Options{Addr: addr})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		t.Skipf("no Redis at %s; skipping (docker run --rm -p 6379:6379 redis)", addr)
	}
	return NewStore(rdb), func() { _ = rdb.Close() }
}

// planFor builds a small stored ladder for these tests.
func planFor(t *testing.T) Plan {
	t.Helper()
	plan, err := BuildPlan(context.Background(), testTrigger("P1", ShiftLK), DefaultPolicy, fullResolver(), ChannelCall)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestStore_CreateIsCreateIfAbsent(t *testing.T) {
	store, closeStore := testStore(t)
	defer closeStore()
	ctx := context.Background()
	id := "store-test-create-" + time.Now().Format("150405.000000")
	defer func() { _ = store.Delete(ctx, id) }()

	plan := planFor(t)
	st := LadderState{Plan: plan, Placed: make([]bool, len(plan.Calls))}

	created, err := store.Create(ctx, id, st)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("the first Create must claim the incident")
	}

	// Mark progress, then attempt the claim again the way a redelivered
	// incident.created would.
	st.Placed[0] = true
	if err := store.Save(ctx, id, st); err != nil {
		t.Fatal(err)
	}
	created, err = store.Create(ctx, id, LadderState{Plan: plan, Placed: make([]bool, len(plan.Calls))})
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Error("a second Create claimed an incident that already had a ladder")
	}

	got, found, err := store.Get(ctx, id)
	if err != nil || !found {
		t.Fatalf("Get after the rejected Create: found=%v err=%v", found, err)
	}
	if !got.Placed[0] {
		t.Error("the rejected Create overwrote the running ladder's progress")
	}
}

// A ladder must survive a restart with everything the engine needs to resume:
// the plan, what has been placed, and any cancellation already recorded.
func TestStore_RoundTripsTheLadder(t *testing.T) {
	store, closeStore := testStore(t)
	defer closeStore()
	ctx := context.Background()
	id := "store-test-roundtrip-" + time.Now().Format("150405.000000")
	defer func() { _ = store.Delete(ctx, id) }()

	plan := planFor(t)
	cancelledAt := time.Now().UTC().Truncate(time.Second)
	st := LadderState{
		Plan:         plan,
		Placed:       make([]bool, len(plan.Calls)),
		Cancelled:    &cancelledAt,
		CancelReason: "Public comment added",
	}
	st.Placed[1] = true
	if err := store.Save(ctx, id, st); err != nil {
		t.Fatal(err)
	}

	got, found, err := store.Get(ctx, id)
	if err != nil || !found {
		t.Fatalf("Get: found=%v err=%v", found, err)
	}
	if len(got.Plan.Calls) != len(plan.Calls) {
		t.Errorf("round-tripped %d calls, want %d", len(got.Plan.Calls), len(plan.Calls))
	}
	if !got.Plan.Calls[0].At.Equal(plan.Calls[0].At) {
		t.Errorf("call time did not survive the round trip: %s vs %s", got.Plan.Calls[0].At, plan.Calls[0].At)
	}
	if got.Plan.Trigger.Priority != plan.Trigger.Priority || got.Plan.InitialWait != plan.InitialWait {
		t.Error("the trigger or the initial wait did not survive the round trip")
	}
	if !got.Placed[1] || got.Placed[0] {
		t.Errorf("placed flags did not survive: %v", got.Placed)
	}
	if got.Cancelled == nil || !got.Cancelled.Equal(cancelledAt) || got.CancelReason != "Public comment added" {
		t.Errorf("cancellation did not survive: %v / %q", got.Cancelled, got.CancelReason)
	}
}

func TestStore_GetMissingIsNotAnError(t *testing.T) {
	store, closeStore := testStore(t)
	defer closeStore()

	// An acknowledgement for an incident that never had a ladder is the
	// common case, and must not look like a failure.
	_, found, err := store.Get(context.Background(), "store-test-does-not-exist")
	if err != nil {
		t.Fatalf("a missing ladder must not error: %v", err)
	}
	if found {
		t.Error("found a ladder that was never stored")
	}
}

func TestStore_WakeIndexOrdersAndRetires(t *testing.T) {
	store, closeStore := testStore(t)
	defer closeStore()
	ctx := context.Background()
	id := "store-test-wake-" + time.Now().Format("150405.000000")

	base := time.Now().Add(-time.Hour)
	members := []string{wakeMember(id, 0), wakeMember(id, 1), wakeMember(id, 2)}
	for i, m := range members {
		if err := store.AddWake(ctx, m, base.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	defer func() { _ = store.RemoveWakes(ctx, members...) }()

	// Only what is actually due comes back, oldest first.
	due, err := store.DueMembers(ctx, base.Add(90*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	due = onlyFor(due, id)
	if len(due) != 2 {
		t.Fatalf("got %d due members, want 2: %v", len(due), due)
	}
	if due[0] != members[0] || due[1] != members[1] {
		t.Errorf("due members out of order: %v", due)
	}

	// Retiring a ladder drops every outstanding entry in one call.
	if err := store.RemoveWakes(ctx, members...); err != nil {
		t.Fatal(err)
	}
	after, err := store.DueMembers(ctx, base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if left := onlyFor(after, id); len(left) != 0 {
		t.Errorf("%d wake entries survived removal: %v", len(left), left)
	}

	// Removing nothing is a no-op, not an error — cancelling a ladder whose
	// calls have all been placed passes an empty slice.
	if err := store.RemoveWakes(ctx); err != nil {
		t.Errorf("RemoveWakes() with no members: %v", err)
	}
}

// onlyFor filters to this test's own incident, since the wake index is one
// shared key and a developer's Redis may hold other runs.
func onlyFor(members []string, incidentID string) []string {
	out := make([]string, 0, len(members))
	for _, m := range members {
		if id, _, ok := parseWakeMember(m); ok && id == incidentID {
			out = append(out, m)
		}
	}
	return out
}

func TestParseWakeMember(t *testing.T) {
	// Incident ids are UUIDs today, but the index is split from the right so a
	// id containing the separator still resolves.
	for _, tc := range []struct {
		member string
		id     string
		index  int
		ok     bool
	}{
		{"inc-1|0", "inc-1", 0, true},
		{"inc-1|12", "inc-1", 12, true},
		{"weird|id|3", "weird|id", 3, true},
		{"no-separator", "", 0, false},
		{"inc-1|not-a-number", "", 0, false},
		{"inc-1|-1", "", 0, false},
		{"|0", "", 0, false},
	} {
		t.Run(tc.member, func(t *testing.T) {
			id, index, ok := parseWakeMember(tc.member)
			if ok != tc.ok || id != tc.id || index != tc.index {
				t.Errorf("parseWakeMember(%q) = (%q, %d, %v), want (%q, %d, %v)",
					tc.member, id, index, ok, tc.id, tc.index, tc.ok)
			}
		})
	}
}

// The SME page's dedup against a real Redis: one open page per incident and
// team, closed together by an assignment, which remembers the latest press.
func TestStore_SMEPages(t *testing.T) {
	s, closeFn := testStore(t)
	defer closeFn()
	ctx := context.Background()
	id := "test-sme-" + time.Now().Format("150405.000000000")
	at := time.Date(2026, 10, 8, 4, 30, 0, 0, time.UTC)
	defer func() {
		_ = s.rdb.Del(ctx, smePageKey(id, "asgardeo"), smePageKey(id, "choreo"),
			smeOpenPrefix+id, smeClosedPrefix+id).Err()
	}()

	for _, c := range []struct {
		team string
		want bool
	}{{"asgardeo", true}, {"asgardeo", false}, {"choreo", true}} {
		got, err := s.OpenSMEPage(ctx, id, c.team, at, time.Minute)
		if err != nil || got != c.want {
			t.Fatalf("OpenSMEPage(%s) = %v, %v; want %v", c.team, got, err, c.want)
		}
	}
	if err := s.CloseSMEPage(ctx, id, "choreo"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.OpenSMEPage(ctx, id, "choreo", at.Add(time.Minute), time.Minute); !got {
		t.Fatal("a released page could not be opened again")
	}
	n, err := s.CloseSMEPages(ctx, id, at.Add(time.Minute))
	if err != nil || len(n) != 2 {
		t.Fatalf("CloseSMEPages = %v, %v; want 2 teams", n, err)
	}
	closed, err := s.SMEClosedThrough(ctx, id)
	if err != nil || !closed.Equal(at.Add(time.Minute)) {
		t.Fatalf("SMEClosedThrough = %v, %v; want the latest press", closed, err)
	}
	if got, _ := s.OpenSMEPage(ctx, id, "asgardeo", at.Add(time.Hour), time.Minute); !got {
		t.Fatal("a closed page could not be opened by a later press")
	}
}

// The assignment and the alert are compared by time, whichever is handled
// first: an assignment handled before the alert it answers still answers it,
// and an assignment made before an escalation leaves that page open.
func TestStore_SMEPagesAnsweredByTime(t *testing.T) {
	s, closeFn := testStore(t)
	defer closeFn()
	ctx := context.Background()
	id := "test-sme-order-" + time.Now().Format("150405.000000000")
	alertAt := time.Date(2026, 10, 8, 4, 30, 0, 0, time.UTC)
	defer func() {
		_ = s.rdb.Del(ctx, smePageKey(id, "asgardeo"), smeOpenPrefix+id, smeClosedPrefix+id).Err()
	}()

	// Assigned before the escalation, but handled after its alert: the page stays open.
	if ok, err := s.OpenSMEPage(ctx, id, "asgardeo", alertAt, time.Minute); err != nil || !ok {
		t.Fatalf("OpenSMEPage = %v, %v", ok, err)
	}
	if n, err := s.CloseSMEPages(ctx, id, alertAt.Add(-time.Minute)); err != nil || len(n) != 0 {
		t.Fatalf("an earlier assignment closed %v (%v); want none", n, err)
	}
	if again, _ := s.OpenSMEPage(ctx, id, "asgardeo", alertAt, time.Minute); again {
		t.Fatal("the page raised after the assignment should still be open")
	}

	// Assigned after the escalation: closes it, and the time answers the alert.
	if n, err := s.CloseSMEPages(ctx, id, alertAt.Add(time.Minute)); err != nil || len(n) != 1 || n[0] != "asgardeo" {
		t.Fatalf("CloseSMEPages = %v, %v; want [asgardeo]", n, err)
	}
	closed, err := s.SMEClosedThrough(ctx, id)
	if err != nil || !closed.Equal(alertAt.Add(time.Minute)) {
		t.Fatalf("SMEClosedThrough = %v, %v; want the assignment's time", closed, err)
	}

	// An assignment with no page open still records its time.
	other := id + "-first"
	defer func() { _ = s.rdb.Del(ctx, smeClosedPrefix+other).Err() }()
	if n, err := s.CloseSMEPages(ctx, other, alertAt.Add(time.Minute)); err != nil || len(n) != 0 {
		t.Fatalf("CloseSMEPages with nothing open = %v, %v", n, err)
	}
	if closed, _ := s.SMEClosedThrough(ctx, other); !closed.Equal(alertAt.Add(time.Minute)) {
		t.Fatalf("an assignment handled first left no time (%v): its alert would still page", closed)
	}
}
