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

// Chat Routing Service — a prototype standalone service that receives
// webhook-style calls from csm-portal/backend and routes live-engineer-chat
// escalations to available engineers. See internal/router.Router for the
// full state machine (Available/Busy/Offline, single-dedicated-session
// capacity, FIFO waiting queue) and csm-portal/backend's
// internal/routingclient for the caller side.
//
// In-memory only, single-process — an accepted prototype limitation, not an
// oversight (see internal/router's package doc comment).
package main

import (
	"bufio"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/wso2-open-operations/cs-tools/apps/chat-routing-service/backend/internal/handler"
	"github.com/wso2-open-operations/cs-tools/apps/chat-routing-service/backend/internal/middleware"
	"github.com/wso2-open-operations/cs-tools/apps/chat-routing-service/backend/internal/router"
)

func main() {
	loadDotEnv(".env")

	token := mustEnv("ROUTING_SERVICE_TOKEN")

	r := router.NewRouter()
	h := handler.NewRoutingHandler(r)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /route/escalate", h.Escalate)
	mux.HandleFunc("POST /route/presence", h.SetPresence)
	mux.HandleFunc("POST /route/completed", h.Completed)
	mux.HandleFunc("POST /route/decline", h.Decline)
	mux.HandleFunc("GET /route/presence/{email}", h.GetPresence)
	mux.HandleFunc("GET /route/debug/state", h.DebugState)

	// /health is deliberately outside the InternalToken gate below (an
	// explicit "GET /health" pattern on topMux takes priority over the
	// catch-all "/" pattern per net/http's ServeMux matching rules) so a
	// local liveness check doesn't need the shared secret.
	topMux := http.NewServeMux()
	topMux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	topMux.Handle("/", middleware.SecurityHeaders(
		middleware.InternalToken(token)(
			middleware.Logger(mux),
		),
	))

	addr := ":" + mustPort("ROUTING_SERVICE_PORT", "9096")
	slog.Info("Chat Routing Service started", "addr", addr)
	if err := http.ListenAndServe(addr, topMux); err != nil { // #nosec G114 -- local prototype service, no external exposure
		slog.Error("server exited", "err", err)
		os.Exit(1)
	}
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		slog.Error("required environment variable is not set", "key", key)
		os.Exit(1)
	}
	return v
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// mustPort returns the value of the given environment variable (or def if
// unset) as a bare port number, e.g. "9096" — not an address like ":9096".
// Exits the process if the value isn't a valid TCP port, matching
// csm-portal/backend's own mustPort helper.
func mustPort(key, def string) string {
	v := envOrDefault(key, def)
	port, err := strconv.Atoi(v)
	if err != nil || port < 1 || port > 65535 {
		slog.Error("environment variable must be a plain port number (e.g. \"9096\"), not an address", "key", key, "value", v)
		os.Exit(1)
	}
	return v
}

// loadDotEnv reads a .env file and sets any unset environment variables
// from it. Silently ignored if the file does not exist; logs a warning for
// any other error. Copied from csm-portal/backend's cmd/server/main.go —
// this service is small enough that sharing it via a common internal
// package would be more indirection than the duplication it avoids.
func loadDotEnv(path string) {
	f, err := os.Open(path) // #nosec G304 -- path is always the hardcoded literal ".env" at the only call site
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			slog.Warn("loadDotEnv: failed to open .env file", "err", err)
		}
		return
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if len(v) >= 2 && ((v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'')) {
			v = v[1 : len(v)-1]
		}
		if os.Getenv(k) == "" {
			_ = os.Setenv(k, v)
		}
	}
	if err := scanner.Err(); err != nil {
		slog.Warn("loadDotEnv: error reading .env file", "err", err)
	}
}
