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
	"context"
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
)

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
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
	validator := introspect.NewValidator(introspect.Config{
		IssuerBaseURL:             mustEnv("KNOWN_ISSUER_BASE_URL"),
		IntrospectionClientID:     mustEnv("INTROSPECTION_CLIENT_ID"),
		IntrospectionClientSecret: mustEnv("INTROSPECTION_CLIENT_SECRET"),
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

	mux := http.NewServeMux()

	browserChain := func(h http.HandlerFunc) http.Handler {
		return middleware.Auth(validator)(middleware.RequireUser(h))
	}
	mux.Handle("POST /support/chats", browserChain(chatsHandler.HandleEscalate))
	mux.Handle("POST /support/chats/{caseId}/messages", browserChain(chatsHandler.HandleSendMessage))
	mux.Handle("GET /support/chats/{caseId}/stream", browserChain(chatsHandler.HandleStream))

	mux.Handle("POST /internal/chat-events",
		middleware.Auth(validator)(middleware.RequireClientID(csmPortalM2MClientID)(http.HandlerFunc(chatsHandler.HandleChatEvents))))

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handlerChain := middleware.CORS(corsOrigins)(mux)

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
