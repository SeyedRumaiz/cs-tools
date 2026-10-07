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

package stream

import "testing"

// TestPublishBeforeSubscribeIsReplayed reproduces the exact race observed
// during manual testing: csm-portal/backend pushes the "engineer_assigned"
// event before the Console browser's SSE GET /stream has finished
// connecting (introspection + the SDK's Web Worker round trip can take
// several seconds). Without replay, that event is fanned out to zero
// subscribers and lost -- the browser never learns the case was accepted.
func TestPublishBeforeSubscribeIsReplayed(t *testing.T) {
	h := NewHub()

	h.Publish("case-1", `{"type":"engineer_assigned"}`)

	ch, unsubscribe := h.Subscribe("case-1")
	defer unsubscribe()

	select {
	case payload := <-ch:
		if payload != `{"type":"engineer_assigned"}` {
			t.Fatalf("got replayed payload %q, want the published event", payload)
		}
	default:
		t.Fatal("expected the pre-subscribe publish to be replayed, got nothing")
	}
}

// TestSubscribeWithNoPriorPublishGetsNothing confirms a case with no
// history yet doesn't spuriously receive anything on Subscribe.
func TestSubscribeWithNoPriorPublishGetsNothing(t *testing.T) {
	h := NewHub()

	ch, unsubscribe := h.Subscribe("case-2")
	defer unsubscribe()

	select {
	case payload := <-ch:
		t.Fatalf("expected no replay for a case with no prior publish, got %q", payload)
	default:
	}
}

// TestPublishAfterSubscribeStillFansOutLive confirms the normal, already-
// connected-subscriber path (Publish's original behavior) is unaffected.
func TestPublishAfterSubscribeStillFansOutLive(t *testing.T) {
	h := NewHub()

	ch, unsubscribe := h.Subscribe("case-3")
	defer unsubscribe()

	h.Publish("case-3", `{"type":"engineer_message","message":"hi"}`)

	select {
	case payload := <-ch:
		if payload != `{"type":"engineer_message","message":"hi"}` {
			t.Fatalf("got %q, want the live-published event", payload)
		}
	default:
		t.Fatal("expected the live publish to be delivered, got nothing")
	}
}

// TestReplayDoesNotLeakAcrossCases ensures a Publish for one caseID is never
// replayed to a Subscribe for a different caseID.
func TestReplayDoesNotLeakAcrossCases(t *testing.T) {
	h := NewHub()

	h.Publish("case-a", `{"type":"engineer_assigned"}`)

	ch, unsubscribe := h.Subscribe("case-b")
	defer unsubscribe()

	select {
	case payload := <-ch:
		t.Fatalf("case-b subscriber should not see case-a's event, got %q", payload)
	default:
	}
}

// TestPublishLiveIsNotReplayed: a live-only payload reaches current
// subscribers, and a later subscriber still gets the last replayable one.
func TestPublishLiveIsNotReplayed(t *testing.T) {
	h := NewHub()

	live, unsubscribeLive := h.Subscribe("case-4")
	defer unsubscribeLive()

	h.Publish("case-4", `{"type":"engineer_assigned"}`)
	h.PublishLive("case-4", `{"type":"engineer_message","message":"hi"}`)

	for _, want := range []string{`{"type":"engineer_assigned"}`, `{"type":"engineer_message","message":"hi"}`} {
		select {
		case got := <-live:
			if got != want {
				t.Fatalf("live subscriber got %q, want %q", got, want)
			}
		default:
			t.Fatalf("live subscriber missed %q", want)
		}
	}

	late, unsubscribeLate := h.Subscribe("case-4")
	defer unsubscribeLate()

	select {
	case got := <-late:
		if got != `{"type":"engineer_assigned"}` {
			t.Fatalf("late subscriber got %q, want only the assigned event", got)
		}
	default:
		t.Fatal("late subscriber should get the assigned event")
	}
	select {
	case got := <-late:
		t.Fatalf("late subscriber should get nothing else, got %q", got)
	default:
	}
}
