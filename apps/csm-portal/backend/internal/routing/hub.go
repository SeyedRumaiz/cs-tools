// // Copyright (c) 2026 WSO2 LLC. (https://www.wso2.com).
// //
// // WSO2 LLC. licenses this file to you under the Apache License,
// // Version 2.0 (the "License"); you may not use this file except
// // in compliance with the License. You may obtain a copy of the License at
// //
// // http://www.apache.org/licenses/LICENSE-2.0
// //
// // Unless required by applicable law or agreed to in writing,
// // software distributed under the License is distributed on an
// // "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// // KIND, either express or implied.  See the License for the
// // specific language governing permissions and limitations
// // under the License.

package routing

import (
	"log/slog"
	"sync"

	"github.com/gorilla/websocket"
)

type BroadcastHub struct {
	mu            sync.Mutex
	clients       map[*websocket.Conn]bool
	engineerConns map[string]int
}

var Hub = &BroadcastHub{
	clients:       make(map[*websocket.Conn]bool),
	engineerConns: make(map[string]int),
}

func (h *BroadcastHub) RegisterEngineer(conn *websocket.Conn, engineerID string) {
	h.mu.Lock()
	h.clients[conn] = true
	if engineerID != "" {
		h.engineerConns[engineerID]++
	}
	clientCount := len(h.clients)
	engConnCount := h.engineerConns[engineerID]
	h.mu.Unlock()

	slog.Info("Client connected to WebSocket",
		"active_clients", clientCount,
		"engineerId", engineerID,
		"engineer_active_conns", engConnCount,
	)
}

func (h *BroadcastHub) UnregisterEngineer(conn *websocket.Conn, engineerID string) int {
	h.mu.Lock()
	delete(h.clients, conn)
	remainingEngConns := 0
	if engineerID != "" {
		if h.engineerConns[engineerID] > 0 {
			h.engineerConns[engineerID]--
		}
		remainingEngConns = h.engineerConns[engineerID]
		if remainingEngConns == 0 {
			delete(h.engineerConns, engineerID)
		}
	}
	clientCount := len(h.clients)
	h.mu.Unlock()

	slog.Info("Client disconnected from WebSocket",
		"active_clients", clientCount,
		"engineerId", engineerID,
		"remaining_engineer_conns", remainingEngConns,
	)

	return remainingEngConns
}

func (h *BroadcastHub) IsEngineerOnline(engineerID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.engineerConns[engineerID] > 0
}

// BroadcastToAllEngineers sends JSON messages directly across all active WebSockets
func (h *BroadcastHub) BroadcastToAllEngineers(message interface{}) {
	h.mu.Lock()
	defer h.mu.Unlock()

	for conn := range h.clients {
		err := conn.WriteJSON(message)
		if err != nil {
			slog.Error("Failed to send WS message to client", "err", err)
			conn.Close()
			delete(h.clients, conn)
		}
	}
}
