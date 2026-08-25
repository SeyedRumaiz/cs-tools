import sys

path = "apps/customer-portal/backend-v2/cmd/server/main.go"
with open(path, "r", encoding="utf-8") as f:
    src = f.read()

def replace_once(src, old, new, label):
    n = src.count(old)
    if n != 1:
        print(f"FAIL[{label}]: found {n} occurrences (expected 1)")
        sys.exit(1)
    return src.replace(old, new, 1)

# A) import
old_a = '''	"github.com/wso2-open-operations/cs-tools/apps/customer-portal/backend-v2/internal/aichatagent"
	"github.com/wso2-open-operations/cs-tools/apps/customer-portal/backend-v2/internal/entity"'''
new_a = '''	"github.com/wso2-open-operations/cs-tools/apps/customer-portal/backend-v2/internal/aichatagent"
	"github.com/wso2-open-operations/cs-tools/apps/customer-portal/backend-v2/internal/csmchat"
	"github.com/wso2-open-operations/cs-tools/apps/customer-portal/backend-v2/internal/entity"'''
src = replace_once(src, old_a, new_a, "import")

# B) construct csmchat client + handlers, right after webSocketHandler
old_b = '''	webSocketHandler := handler.NewWebSocketHandler(aiChatAgentWsClient, entityClient, tokenValidator, nil)'''
new_b = '''	webSocketHandler := handler.NewWebSocketHandler(aiChatAgentWsClient, entityClient, tokenValidator, nil)

	// Live-engineer-chat escalation feature — see internal/handler/chat.go
	// and csm-portal/backend's own internal/handler/chat.go for the full
	// design (case creation happens here, in this backend, because only it
	// has the deployment/deployed-product context a case requires).
	// internalChatToken authenticates both directions of service-to-service
	// traffic this feature needs with csm-portal/backend; both backends
	// must be configured with the same value.
	internalChatToken := os.Getenv("INTERNAL_CHAT_TOKEN")
	csmChatClient := csmchat.NewClient(csmchat.Config{
		// csm-portal/backend's INTERNAL_CHAT_PORT listener.
		BaseURL:       envOrDefault("CSM_PORTAL_INTERNAL_BASE_URL", "http://localhost:9095"),
		InternalToken: internalChatToken,
	})
	chatEscalationHandler := handler.NewChatEscalationHandler(entityClient, csmChatClient)
	chatEventsHandler := handler.NewChatEventsHandler(webSocketHandler)'''
src = replace_once(src, old_b, new_b, "handler-construction")

# C) register the two customer-facing routes on the main mux, near the
#    other AI-chat routes.
old_c = '''	mux.HandleFunc("GET /projects/{id}/conversations/{conversationId}/summary", aiChatHandler.GetConversationSummary)'''
new_c = '''	mux.HandleFunc("GET /projects/{id}/conversations/{conversationId}/summary", aiChatHandler.GetConversationSummary)

	// Live-engineer-chat escalation (see internal/handler/chat.go).
	mux.HandleFunc("POST /projects/{id}/support/chat/escalate", chatEscalationHandler.HandleEscalate)
	mux.HandleFunc("POST /projects/{id}/support/chat/{conversationId}/message", chatEscalationHandler.HandleSendMessage)'''
src = replace_once(src, old_c, new_c, "customer-routes")

# D) register the internal receiver on wsMux, behind InternalToken instead
#    of Auth (mirrors GET /ws's own reasoning for living outside Auth).
old_d = '''	wsMux := http.NewServeMux()
	wsMux.HandleFunc("GET /ws", webSocketHandler.HandleWebSocket)'''
new_d = '''	wsMux := http.NewServeMux()
	wsMux.HandleFunc("GET /ws", webSocketHandler.HandleWebSocket)
	// POST /internal/chat-events is not browser-facing — it is csm-portal/
	// backend pushing a live-engineer-chat event into this connection's
	// already-open WebSocket (see handler.ChatEventsHandler). It lives on
	// this listener because, like GET /ws, it cannot go through the normal
	// Auth middleware (no customer x-jwt-assertion to present); it is
	// instead wrapped individually below with middleware.InternalToken,
	// since wsSrv's shared handler chain (built further down) applies to
	// every route on wsMux uniformly and GET /ws must stay un-gated by it.
	wsMux.Handle("POST /internal/chat-events", middleware.InternalToken(internalChatToken)(http.HandlerFunc(chatEventsHandler.Handle)))'''
src = replace_once(src, old_d, new_d, "internal-route")

with open(path, "w", encoding="utf-8") as f:
    f.write(src)

print("OK: all 4 patches applied")
