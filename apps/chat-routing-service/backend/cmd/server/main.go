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
// State (engineer presence + escalation queue) is persisted in this
// service's own PostgreSQL database — see migrations/ and internal/db —
// so, unlike the original in-memory prototype, restarting this process no
// longer loses in-flight routing state, and a future multi-replica
// deployment would share state correctly. Still single-process only for
// now: no leader election or cross-replica coordination beyond what
// Postgres's own row locking already gives each request.
package main

import (
	"bufio"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/wso2-open-operations/cs-tools/apps/chat-routing-service/backend/internal/config"
	"github.com/wso2-open-operations/cs-tools/apps/chat-routing-service/backend/internal/db"
	"github.com/wso2-open-operations/cs-tools/apps/chat-routing-service/backend/internal/handler"
	"github.com/wso2-open-operations/cs-tools/apps/chat-routing-service/backend/internal/middleware"
	"github.com/wso2-open-operations/cs-tools/apps/chat-routing-service/backend/internal/router"
)

// dbConnectTimeout bounds how long startup waits for the initial pool
// connection + ping before giving up — a hung/unreachable database should
// fail fast at startup, not hang the process indefinitely.
const dbConnectTimeout = 10 * time.Second

// shutdownTimeout bounds graceful shutdown, mirroring entity-service's
// cmd/api/main.go.
const shutdownTimeout = 10 * time.Second

func main() {
	loadDotEnv(".env")

	token := mustEnv("ROUTING_SERVICE_TOKEN")

	dbCfg := config.LoadDB()
	if err := dbCfg.Validate(); err != nil {
		slog.Error("invalid database configuration", "err", err)
		os.Exit(1)
	}

	connectCtx, connectCancel := context.WithTimeout(context.Background(), dbConnectTimeout)
	pool, err := db.NewPool(connectCtx, dbCfg.DSN())
	connectCancel()
	if err != nil {
		slog.Error("connect to database", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	r := router.NewRouter(pool)
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
	srv := &http.Server{Addr: addr, Handler: topMux}

	go func() {
		slog.Info("Chat Routing Service started", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) { // #nosec G114 -- local prototype service, no external exposure
			slog.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("graceful shutdown failed", "err", err)
		os.Exit(1)
	}
	slog.Info("server stopped")
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
