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

// Package stream is a minimal per-case, in-memory Server-Sent Events
// pub/sub for this bridge's GET /support/chats/{caseId}/stream. POC-scoped
// deliberately: single-process, in-memory only (a case's one subscribing
// Console browser must be connected to this same process instance) --
// mirrors csm-portal/backend's own internal/stream.BroadcastHub in spirit
// (never blocks a publisher; drops for a slow/gone subscriber rather than
// stalling) but keyed per caseId instead of per engineer, since this bridge
// only ever has one browser subscribed per case.
package stream

import "sync"

// Hub is a minimal per-case pub/sub.
type Hub struct {
	mu   sync.Mutex
	subs map[string]map[chan string]struct{}
	// last holds the most recently published payload for each caseID, so a
	// subscriber that connects *after* a publish (e.g. the Console browser's
	// GET /stream is still mid-flight -- introspection + the SDK's Web
	// Worker round trip can take a few seconds -- when the CSM engineer
	// accepts the case) still sees that event instead of it being silently
	// dropped. Without this, Publish's "fan out to whoever's currently
	// registered" semantics mean the one event that tells the browser
	// "you're connected" can fire into an empty room and be lost forever --
	// this was an observed, reproducible failure during manual testing, not
	// a hypothetical. Deliberately just the latest payload, not a full
	// backlog: this Hub already documents itself as POC-scoped, in-memory,
	// single-process, and a full event log belongs in the same durable
	// storage this bridge otherwise has none of.
	last map[string]string
}

// NewHub constructs an empty Hub.
func NewHub() *Hub {
	return &Hub{
		last: make(map[string]string),
		subs: make(map[string]map[chan string]struct{}),
	}
}

// Subscribe registers a new channel for caseID. The caller must defer the
// returned unsubscribe func. If a payload was already published for caseID
// before this call (see Hub's own doc comment on the connect race this
// closes), it's replayed into the new channel immediately so the
// subscriber catches up instead of missing it.
func (h *Hub) Subscribe(caseID string) (ch chan string, unsubscribe func()) {
	ch = make(chan string, 16)
	h.mu.Lock()
	if h.subs[caseID] == nil {
		h.subs[caseID] = make(map[chan string]struct{})
	}
	h.subs[caseID][ch] = struct{}{}
	if last, ok := h.last[caseID]; ok {
		ch <- last
	}
	h.mu.Unlock()

	return ch, func() {
		h.mu.Lock()
		delete(h.subs[caseID], ch)
		if len(h.subs[caseID]) == 0 {
			delete(h.subs, caseID)
		}
		h.mu.Unlock()
		close(ch)
	}
}

// Publish fans payload out to every subscriber currently registered for
// caseID, and remembers it as caseID's latest payload for the next
// Subscribe (see Hub's own doc comment). Never blocks: a subscriber whose
// buffer is full is skipped for this event rather than stalling every
// other subscriber/publisher.
func (h *Hub) Publish(caseID, payload string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.last[caseID] = payload
	for ch := range h.subs[caseID] {
		select {
		case ch <- payload:
		default:
		}
	}
}
