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
}

// NewHub constructs an empty Hub.
func NewHub() *Hub {
	return &Hub{subs: make(map[string]map[chan string]struct{})}
}

// Subscribe registers a new channel for caseID. The caller must defer the
// returned unsubscribe func.
func (h *Hub) Subscribe(caseID string) (ch chan string, unsubscribe func()) {
	ch = make(chan string, 16)
	h.mu.Lock()
	if h.subs[caseID] == nil {
		h.subs[caseID] = make(map[chan string]struct{})
	}
	h.subs[caseID][ch] = struct{}{}
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
// caseID. Never blocks: a subscriber whose buffer is full is skipped for
// this event rather than stalling every other subscriber/publisher.
func (h *Hub) Publish(caseID, payload string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs[caseID] {
		select {
		case ch <- payload:
		default:
		}
	}
}
