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

// Chat Routing Service receives webhook-style calls from csm-portal/backend
// and routes live-engineer-chat escalations to available engineers.
//
// Engineer presence and the escalation queue are persisted in Postgres, so
// restarting the process doesn't lose in-flight routing state. Still
// single-process only, no leader election across replicas.
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

// dbConnectTimeout bounds how long startup waits for the initial db
// connection so an unreachable database fails fast instead of hanging.
const dbConnectTimeout = 10 * time.Second

// shutdownTimeout bounds graceful shutdown.
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

	// how long an engineer can sit PENDING before sweep-timeouts reassigns
	// the case (their own chat_status is untouched -- see the 2026-09-10
	// concurrent-chat-capacity change; this comment used to say it also
	// marked the engineer OFFLINE, which stopped being true then)
	pendingTimeout := envDurationSeconds("PENDING_TIMEOUT_SECONDS", 90)
	// how long a case can sit WAITING_FOR_ENGINEER (never assigned to
	// anyone at all -- every engineer OFFLINE/BUSY/at capacity when it
	// arrived) before sweep-timeouts gives up on it -- see router.Router.
	// SweepAbandonedQueue's own doc comment for why this exists. Default
	// of 30 minutes is deliberately much longer than PENDING_TIMEOUT_
	// SECONDS: that one bounds a single engineer's accept window, this one
	// bounds how long a customer should realistically keep waiting with
	// nobody free at all.
	queueAbandonTimeout := envDurationSeconds("QUEUE_ABANDON_SECONDS", 1800)
	h := handler.NewRoutingHandler(r, pendingTimeout, queueAbandonTimeout)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /route/escalate", h.Escalate)
	mux.HandleFunc("POST /route/presence", h.SetPresence)
	mux.HandleFunc("POST /route/completed", h.Completed)
	mux.HandleFunc("POST /route/decline", h.Decline)
	mux.HandleFunc("POST /route/accept", h.Accept)
	mux.HandleFunc("GET /route/presence/{userId}", h.GetPresence)
	mux.HandleFunc("PATCH /route/capacity", h.SetCapacity)
	// stand-in persistence endpoints until real persistence lands
	mux.HandleFunc("POST /route/workitem", h.CreateWorkItem)
	mux.HandleFunc("POST /route/workitem/{caseId}/info", h.GetCaseInfo)
	mux.HandleFunc("POST /route/comment", h.AddComment)
	mux.HandleFunc("GET /route/debug/workitem/{caseId}", h.DebugWorkItem)
	mux.HandleFunc("GET /route/debug/state", h.DebugState)
	mux.HandleFunc("POST /route/sweep-timeouts", h.SweepTimeouts)
	// chat-first escalation: engineer-initiated conversion to a real case
	mux.HandleFunc("POST /route/convert-to-case", h.ConvertToCase)
	// tenant-scoped (customer-initiated) session end -- see router.Router.EndByTenant
	mux.HandleFunc("POST /route/end-by-tenant", h.EndByTenant)

	// /health sits outside the InternalToken gate below -- ServeMux matches
	// the exact "GET /health" pattern before falling through to "/", so
	// liveness checks don't need the shared secret.
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

// envDurationSeconds reads key as a plain whole number of seconds (e.g.
// "90", not "90s"), or defSeconds if unset. Exits the process if the value
// isn't a positive integer.
func envDurationSeconds(key string, defSeconds int) time.Duration {
	v := envOrDefault(key, strconv.Itoa(defSeconds))
	secs, err := strconv.Atoi(v)
	if err != nil || secs <= 0 {
		slog.Error("environment variable must be a positive whole number of seconds", "key", key, "value", v)
		os.Exit(1)
	}
	return time.Duration(secs) * time.Second
}

// mustPort returns the given environment variable (or def if unset) as a
// bare port number, e.g. "9096", not an address like ":9096". Exits the
// process if the value isn't a valid TCP port.
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
// from it. A missing file is fine; any other read error just gets logged.
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
