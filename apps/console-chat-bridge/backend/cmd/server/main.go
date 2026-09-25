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

// Command server runs console-chat-bridge: the small backend that lets
// identity-apps' Console "Chat with an Engineer" button (see
// features/admin.copilot.v1) reach the existing cs-tools live-engineer-chat
// framework (chat-routing-service / csm-portal/backend) without the
// Console's browser ever calling either directly, and without any
// chat-routing-service/csm-portal credential ever reaching the browser.
//
// POC-scoped to a single known WSO2 IS instance -- see internal/introspect's
// own package doc comment and this repo's
// console-ask-ai-engineer-escalation-investigation.md (auth decision #1).
// See .env.example for every setting below and README.md for the one-time
// setup this requires on that IS instance and on csm-portal/backend.
package main

import (
	"bufio"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/wso2-open-operations/cs-tools/apps/console-chat-bridge/backend/internal/csmchat"
	"github.com/wso2-open-operations/cs-tools/apps/console-chat-bridge/backend/internal/handler"
	"github.com/wso2-open-operations/cs-tools/apps/console-chat-bridge/backend/internal/introspect"
	"github.com/wso2-open-operations/cs-tools/apps/console-chat-bridge/backend/internal/middleware"
	"github.com/wso2-open-operations/cs-tools/apps/console-chat-bridge/backend/internal/stream"
	"github.com/wso2-open-operations/cs-tools/apps/console-chat-bridge/backend/internal/tenant"
)

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// loadDotEnv reads a .env file and sets any unset environment variables from
// it. Silently ignored if the file does not exist; logs a warning for any
// other error. Mirrors csm-portal/backend's own cmd/server/main.go helper of
// the same name so this service can be run the same way: cp .env.example
// .env, fill in values, go run ./cmd/server.
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
		// Strip surrounding quotes from value.
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

func splitComma(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		slog.Error("missing required environment variable", "key", key)
		os.Exit(1)
	}
	return v
}

func main() {
	loadDotEnv(".env")

	validator := introspect.NewValidator(introspect.Config{
		IssuerBaseURL:             mustEnv("KNOWN_ISSUER_BASE_URL"),
		IntrospectionClientID:     mustEnv("INTROSPECTION_CLIENT_ID"),
		IntrospectionClientSecret: mustEnv("INTROSPECTION_CLIENT_SECRET"),
		InsecureSkipVerify:        envOrDefault("INTROSPECTION_INSECURE_SKIP_VERIFY", "false") == "true",
	})

	csmClient := csmchat.NewClient(csmchat.Config{
		BaseURL:      mustEnv("CSM_PORTAL_BASE_URL"),
		TokenURL:     mustEnv("CSM_PORTAL_TOKEN_URL"),
		ClientID:     mustEnv("CSM_PORTAL_CLIENT_ID"),
		ClientSecret: mustEnv("CSM_PORTAL_CLIENT_SECRET"),
		Scopes:       splitComma(envOrDefault("CSM_PORTAL_SCOPES", "internal_console_chat_escalate")),
	})

	hub := stream.NewHub()
	chatsHandler := handler.NewChatsHandler(csmClient, hub)

	// csmPortalM2MClientID is the OAuth2 client ID csm-portal/backend's own
	// outbound push (its chatNotifiers["asgardeo"] entry, see that
	// service's cmd/server/main.go) authenticates as when calling this
	// bridge's own /internal/chat-events -- checked by RequireClientID so
	// only that specific client can post events into this bridge, even
	// though its token is introspected against the same pinned IS instance
	// as every browser-facing call.
	csmPortalM2MClientID := mustEnv("CSM_PORTAL_PUSH_CLIENT_ID")

	corsOrigins := splitComma(mustEnv("CORS_ALLOWED_ORIGINS"))

	// tenantTable backs the generic /v1/{tenant}/... API only -- the legacy
	// /support/chats routes above never consult it, and keep using
	// validator/corsOrigins exactly as before (see internal/tenant's own
	// package doc comment). TENANT_REGISTRY unset synthesizes a single
	// "identity-console" tenant from the same KNOWN_ISSUER_BASE_URL/
	// INTROSPECTION_*/CORS_ALLOWED_ORIGINS vars validator/corsOrigins
	// already use, so /v1/identity-console/... works with zero additional
	// configuration. BRIDGE_ROUTING_SOURCE is every explicit registry
	// row's default RoutingSource when that row leaves it blank -- see
	// csm-portal/backend's own dual "asgardeo"/"console-chat-bridge"
	// chatNotifiers registration, which this default is designed to match.
	tenantTable, err := tenant.BuildTable(
		os.Getenv("TENANT_REGISTRY"),
		envOrDefault("BRIDGE_ROUTING_SOURCE", "console-chat-bridge"),
		nil,
		nil,
		tenant.LegacyConfig{
			IssuerBaseURL:      os.Getenv("KNOWN_ISSUER_BASE_URL"),
			ClientID:           os.Getenv("INTROSPECTION_CLIENT_ID"),
			ClientSecret:       os.Getenv("INTROSPECTION_CLIENT_SECRET"),
			InsecureSkipVerify: envOrDefault("INTROSPECTION_INSECURE_SKIP_VERIFY", "false") == "true",
			AllowedOrigins:     corsOrigins,
			// SCIM_CLIENT_ID unset (the default) leaves SCIM unconfigured --
			// this tenant keeps resolving Subject via UserInfo exactly as
			// before. Set all of SCIM_CLIENT_ID/SCIM_CLIENT_SECRET to enable
			// it (see internal/tenant.LegacyConfig's own doc comment and
			// README.md's SCIM setup step); SCIM_BASE_URL/SCIM_TOKEN_URL
			// default to KNOWN_ISSUER_BASE_URL and its own "/oauth2/token"
			// when left blank.
			SCIMBaseURL:      os.Getenv("SCIM_BASE_URL"),
			SCIMTokenURL:     os.Getenv("SCIM_TOKEN_URL"),
			SCIMClientID:     os.Getenv("SCIM_CLIENT_ID"),
			SCIMClientSecret: os.Getenv("SCIM_CLIENT_SECRET"),
			SCIMScopes:       splitComma(envOrDefault("SCIM_SCOPES", "internal_user_mgt_list")),
		},
	)
	if err != nil {
		slog.Error("failed to build tenant table", "err", err)
		os.Exit(1)
	}

	mux := http.NewServeMux()

	browserChain := func(h http.HandlerFunc) http.Handler {
		return middleware.Auth(validator)(middleware.RequireUser(h))
	}
	mux.Handle("POST /support/chats", browserChain(chatsHandler.HandleEscalate))
	mux.Handle("POST /support/chats/{caseId}/messages", browserChain(chatsHandler.HandleSendMessage))
	mux.Handle("GET /support/chats/{caseId}/stream", browserChain(chatsHandler.HandleStream))

	mux.Handle("POST /internal/chat-events",
		middleware.Auth(validator)(middleware.RequireClientID(csmPortalM2MClientID)(http.HandlerFunc(chatsHandler.HandleChatEvents))))

	// v1Chain: TenantAuth (per-tenant validator, resolved into context by
	// ResolveTenant -- see this middleware chain's own composition below)
	// then RequireSubject (see that middleware's own doc comment on why
	// /v1 never falls back to Username the way browserChain's RequireUser
	// does).
	v1Chain := func(h http.HandlerFunc) http.Handler {
		return middleware.TenantAuth()(middleware.RequireSubject(h))
	}
	mux.Handle("POST /v1/{tenant}/chats", v1Chain(chatsHandler.HandleEscalateV1))
	mux.Handle("POST /v1/{tenant}/chats/{caseId}/messages", v1Chain(chatsHandler.HandleSendMessageV1))
	mux.Handle("GET /v1/{tenant}/chats/{caseId}/events", v1Chain(chatsHandler.HandleStreamV1))
	mux.Handle("POST /v1/{tenant}/chats/{caseId}/complete", v1Chain(chatsHandler.HandleCompleteV1))

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// ResolveTenant must be OUTSIDE (run before) CORS -- see that
	// middleware's own doc comment on why an unknown tenant must 404
	// before CORS or Auth ever run, including for a bare OPTIONS
	// preflight. It only ever acts on a /v1/... path; every other route
	// above (legacy, internal, health) passes through it untouched, so
	// this wrapping is purely additive over today's single
	// middleware.CORS(corsOrigins)(mux) chain.
	handlerChain := middleware.ResolveTenant(tenantTable)(middleware.CORS(corsOrigins)(mux))

	addr := ":" + envOrDefault("PORT", "8090")
	srv := &http.Server{
		Addr:         addr,
		Handler:      handlerChain,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 0, // SSE streams stay open indefinitely.
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		slog.Info("console-chat-bridge listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server failed", "err", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}
