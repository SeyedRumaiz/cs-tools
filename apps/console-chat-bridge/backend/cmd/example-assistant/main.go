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

// Command example-assistant is a minimal AI service implementing the
// bridge's "http" assistant provider contract (docs/ASSISTANT_PROVIDERS.md),
// for trying the provider locally and as a reference for product teams. It
// has no model: it streams a reply that shows the conversation it
// remembers.
//
//	go run ./cmd/example-assistant            # listens on :8095
//	TENANT_DEVANT_ASSISTANT_PROVIDER=http
//	TENANT_DEVANT_ASSISTANT_HTTP_URL=http://localhost:8095/ask
package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

type question struct {
	ConversationID string `json:"conversationId"`
	AccountID      string `json:"accountId"`
	Tenant         string `json:"tenant"`
	Message        string `json:"message"`
}

func main() {
	var mu sync.Mutex
	history := map[string][]string{}

	http.HandleFunc("POST /ask", func(w http.ResponseWriter, r *http.Request) {
		var q question
		if err := json.NewDecoder(r.Body).Decode(&q); err != nil || q.ConversationID == "" || q.Message == "" {
			http.Error(w, "conversationId and message are required", http.StatusBadRequest)
			return
		}
		mu.Lock()
		history[q.ConversationID] = append(history[q.ConversationID], q.Message)
		asked := len(history[q.ConversationID])
		mu.Unlock()

		answer := fmt.Sprintf("This answer comes from **%s**'s own AI service (an example). "+
			"You asked: \"%s\". That is question %d in this conversation.", q.Tenant, q.Message, asked)

		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		send := func(eventType, text string) {
			payload, _ := json.Marshal(map[string]string{"type": eventType, "text": text})
			fmt.Fprintf(w, "data: %s\n\n", payload)
			if flusher != nil {
				flusher.Flush()
			}
		}
		send("status", "Checking your question")
		for _, word := range strings.SplitAfter(answer, " ") {
			time.Sleep(40 * time.Millisecond)
			send("token", word)
		}
		send("done", answer)
	})

	addr := ":" + envOr("PORT", "8095")
	slog.Info("example assistant listening", "addr", addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		slog.Error("server error", "err", err)
		os.Exit(1)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
