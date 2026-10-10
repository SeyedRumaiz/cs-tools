// Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com).
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

// Package hub is alert-core-service's wake endpoint: alert-ingestion POSTs to it to wake the poller early; poller does the real work.
package hub

import "net/http"

// waker is satisfied by *poll.Poller; kept narrow so hub doesn't need to import poll.
type waker interface {
	Wake()
}

// Hub wakes the poller on every request it serves.
type Hub struct {
	poller waker
}

// New returns a ready hub.
func New(p waker) *Hub {
	return &Hub{poller: p}
}

// ServeAlert wakes the poller on POST requests; any other method is rejected.
func (h *Hub) ServeAlert(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	h.poller.Wake()
	w.WriteHeader(http.StatusAccepted)
}
