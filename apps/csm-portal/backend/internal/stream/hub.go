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

// Package stream is the in-process pub-sub side of case-activity SSE: a
// BroadcastHub that fans a per-case string payload out to every open
// /cases/{id}/activities/stream connection on this replica. It carries no
// auth or transport concerns of its own — the HTTP handler that registers
// with it (internal/handler.CaseHandler.StreamCaseActivities) sits behind
// the same middleware.Auth chain as every other endpoint.
package stream

import "sync"

// subscriberBuffer bounds each subscriber channel so Publish never blocks on
// a slow reader — a client that isn't draining its channel fast enough just
// misses the newest ping (see Publish); it still gets the next one, and a
// browser SSE reconnect (or the frontend's own query staleTime) covers the
// gap either way.
const subscriberBuffer = 4

// BroadcastHub fans a payload out to every subscriber registered for a given
// case ID. Safe for concurrent use.
type BroadcastHub struct {
	mu   sync.Mutex
	subs map[string]map[chan string]bool
}

// NewBroadcastHub constructs an empty BroadcastHub.
func NewBroadcastHub() *BroadcastHub {
	return &BroadcastHub{subs: make(map[string]map[chan string]bool)}
}

// Register opens a new subscription for caseID and returns the channel to
// read from. Call Unregister with the same caseID/channel when the caller is
// done listening (e.g. the SSE request's context is done) — the channel is
// closed there, not here.
func (h *BroadcastHub) Register(caseID string) chan string {
	ch := make(chan string, subscriberBuffer)
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.subs[caseID] == nil {
		h.subs[caseID] = make(map[chan string]bool)
	}
	h.subs[caseID][ch] = true
	return ch
}

// Unregister removes ch from caseID's subscriber set and closes it. Safe to
// call exactly once per channel returned by Register; call it via defer in
// the same goroutine that reads from the channel.
func (h *BroadcastHub) Unregister(caseID string, ch chan string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if subs, ok := h.subs[caseID]; ok {
		if _, ok := subs[ch]; ok {
			delete(subs, ch)
			close(ch)
		}
		if len(subs) == 0 {
			delete(h.subs, caseID)
		}
	}
}

// Publish fans payload out to every subscriber currently registered for
// caseID. A subscriber whose buffer is full (it isn't draining fast enough)
// is skipped rather than blocking every other subscriber and the caller —
// see subscriberBuffer.
func (h *BroadcastHub) Publish(caseID, payload string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs[caseID] {
		select {
		case ch <- payload:
		default:
		}
	}
}
