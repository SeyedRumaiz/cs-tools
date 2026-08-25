import sys

path = "apps/csm-portal/backend/cmd/server/main.go"
with open(path, "r", encoding="utf-8") as f:
    src = f.read()

def replace_once(src, old, new, label):
    n = src.count(old)
    if n != 1:
        print(f"FAIL[{label}]: found {n} occurrences (expected 1)")
        sys.exit(1)
    return src.replace(old, new, 1)

# A) import
old_a = '''	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/caseevents"
	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/dashboard"'''
new_a = '''	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/caseevents"
	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/chatnotify"
	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/dashboard"'''
src = replace_once(src, old_a, new_a, "import")

# B) handler construction
old_b = '''	caseHandler := handler.NewCaseHandler(customerEntityClient, eventPublisher, activityHub)
	dashboardHandler := handler.NewDashboardHandler(customerEntityClient)'''
new_b = '''	caseHandler := handler.NewCaseHandler(customerEntityClient, eventPublisher, activityHub)

	// Live-engineer-chat escalation feature (see internal/handler/chat.go).
	// engineerHub is unconditional — unlike activityHub above, this feature
	// has no Kafka-backed fallback to degrade to, so it is always
	// constructed regardless of EVENT_HUB_BROKER.
	engineerHub := stream.NewBroadcastHub()
	// internalChatToken authenticates the two directions of
	// service-to-service traffic this feature needs with customer-portal/
	// backend-v2 (see internal/middleware.InternalToken and
	// internal/chatnotify's doc comments). Both backends must be configured
	// with the same value.
	internalChatToken := os.Getenv("INTERNAL_CHAT_TOKEN")
	chatNotifyClient := chatnotify.NewClient(chatnotify.Config{
		// customer-portal/backend-v2's WS_PORT listener — the same
		// unauthenticated-by-user-JWT listener GET /ws already runs on
		// (see that backend's cmd/server/main.go), since neither route can
		// carry an x-jwt-assertion header.
		BaseURL:       envOrDefault("CUSTOMER_PORTAL_INTERNAL_BASE_URL", "http://localhost:8082"),
		InternalToken: internalChatToken,
	})
	chatHandler := handler.NewChatHandler(customerEntityClient, engineerHub, chatNotifyClient)

	dashboardHandler := handler.NewDashboardHandler(customerEntityClient)'''
src = replace_once(src, old_b, new_b, "handler-construction")

# C) route registrations
old_c = '	mux.HandleFunc("POST /problems/search", problemHandler.SearchProblems)'
new_c = '''	mux.HandleFunc("POST /problems/search", problemHandler.SearchProblems)

	// Live-engineer-chat escalation (see internal/handler/chat.go).
	mux.HandleFunc("POST /api/v1/chat/sessions/{id}/accept", chatHandler.HandleAcceptSession)
	mux.HandleFunc("POST /api/v1/chat/sessions/{id}/messages", chatHandler.HandleEngineerMessage)
	mux.HandleFunc("POST /api/v1/chat/sessions/{id}/complete", chatHandler.HandleCompleteSession)'''
src = replace_once(src, old_c, new_c, "routes")

# D) new listeners construction, before the main srv.Serve goroutine
old_d = '''	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {'''
new_d = '''	// Live-engineer-chat alert stream (GET /api/v1/chat/alerts/stream) needs
	// the same "no WriteTimeout/IdleTimeout" treatment as the case-activity
	// stream above, but — unlike that one — is always started: it has no
	// Event-Hub-gated fallback (see engineerHub's construction above).
	chatStreamMux := http.NewServeMux()
	chatStreamMux.HandleFunc("GET /api/v1/chat/alerts/stream", chatHandler.StreamEngineerAlerts)

	chatStreamAddr := ":" + mustPort("CHAT_STREAM_PORT", "9094")
	chatStreamLn, err := (&net.ListenConfig{}).Listen(ctx, "tcp", chatStreamAddr)
	if err != nil {
		slog.Error("failed to bind", "addr", chatStreamAddr, "err", err)
		os.Exit(1)
	}
	chatStreamSrv := &http.Server{
		Handler: middleware.SecurityHeaders(
			middleware.CORS(splitComma(os.Getenv("STREAM_CORS_ALLOWED_ORIGINS")))(
				middleware.CorrelationID(
					authMiddleware(
						middleware.Logger(chatStreamMux),
					),
				),
			),
		),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      0,
		IdleTimeout:       0,
	}

	// Internal listener for service-to-service calls from customer-portal/
	// backend-v2 (POST /internal/chat/escalate, POST /internal/chat/
	// customer-message) — see internal/middleware.InternalToken's doc
	// comment for why these cannot go through authMiddleware/mux above:
	// backend-v2 has no engineer JWT to present. Ordinary short-lived-request
	// timeouts are fine here (unlike the two SSE listeners), since neither
	// route holds a connection open.
	internalChatMux := http.NewServeMux()
	internalChatMux.HandleFunc("POST /internal/chat/escalate", chatHandler.HandleEscalate)
	internalChatMux.HandleFunc("POST /internal/chat/customer-message", chatHandler.HandleCustomerMessage)

	internalChatAddr := ":" + mustPort("INTERNAL_CHAT_PORT", "9095")
	internalChatLn, err := (&net.ListenConfig{}).Listen(ctx, "tcp", internalChatAddr)
	if err != nil {
		slog.Error("failed to bind", "addr", internalChatAddr, "err", err)
		os.Exit(1)
	}
	internalChatSrv := &http.Server{
		Handler: middleware.SecurityHeaders(
			middleware.CorrelationID(
				middleware.InternalToken(internalChatToken)(
					middleware.Logger(internalChatMux),
				),
			),
		),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {'''
src = replace_once(src, old_d, new_d, "listener-construction")

# E) start goroutines for new listeners
old_e = '''	if streamSrv != nil {
		go func() {
			if err := streamSrv.Serve(streamLn); err != nil && err != http.ErrServerClosed {
				slog.Error("stream server exited", "err", err)
				os.Exit(1)
			}
		}()
		slog.Info("case-activity stream server started", "addr", streamLn.Addr().String())
	}

	if caseEventsConsumer != nil {'''
new_e = '''	if streamSrv != nil {
		go func() {
			if err := streamSrv.Serve(streamLn); err != nil && err != http.ErrServerClosed {
				slog.Error("stream server exited", "err", err)
				os.Exit(1)
			}
		}()
		slog.Info("case-activity stream server started", "addr", streamLn.Addr().String())
	}

	go func() {
		if err := chatStreamSrv.Serve(chatStreamLn); err != nil && err != http.ErrServerClosed {
			slog.Error("chat alert stream server exited", "err", err)
			os.Exit(1)
		}
	}()
	slog.Info("engineer chat alert stream server started", "addr", chatStreamLn.Addr().String())

	go func() {
		if err := internalChatSrv.Serve(internalChatLn); err != nil && err != http.ErrServerClosed {
			slog.Error("internal chat server exited", "err", err)
			os.Exit(1)
		}
	}()
	slog.Info("internal chat server started", "addr", internalChatLn.Addr().String())

	if caseEventsConsumer != nil {'''
src = replace_once(src, old_e, new_e, "start-goroutines")

# F) graceful shutdown
old_f = '''	var wg sync.WaitGroup
	if streamSrv != nil {
		wg.Go(func() {
			if err := streamSrv.Shutdown(shutdownCtx); err != nil {
				slog.Error("stream server graceful shutdown failed", "err", err)
			}
		})
	}
	var srvErr error'''
new_f = '''	var wg sync.WaitGroup
	if streamSrv != nil {
		wg.Go(func() {
			if err := streamSrv.Shutdown(shutdownCtx); err != nil {
				slog.Error("stream server graceful shutdown failed", "err", err)
			}
		})
	}
	wg.Go(func() {
		if err := chatStreamSrv.Shutdown(shutdownCtx); err != nil {
			slog.Error("chat alert stream server graceful shutdown failed", "err", err)
		}
	})
	wg.Go(func() {
		if err := internalChatSrv.Shutdown(shutdownCtx); err != nil {
			slog.Error("internal chat server graceful shutdown failed", "err", err)
		}
	})
	var srvErr error'''
src = replace_once(src, old_f, new_f, "graceful-shutdown")

with open(path, "w", encoding="utf-8") as f:
    f.write(src)

print("OK: all 6 patches applied")
