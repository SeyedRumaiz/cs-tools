import sys

path = "apps/customer-portal/backend-v2/internal/handler/websocket.go"
with open(path, "r", encoding="utf-8") as f:
    src = f.read()

def replace_once(src, old, new, label):
    n = src.count(old)
    if n != 1:
        print(f"FAIL[{label}]: found {n} occurrences (expected 1)")
        sys.exit(1)
    return src.replace(old, new, 1)

# A) import "sync"
old_a = '''import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"'''
new_a = '''import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"'''
src = replace_once(src, old_a, new_a, "import-sync")

# B) struct fields
old_b = '''type WebSocketHandler struct {
	ai      wsStreamer
	entity  entityCommentCreator
	auth    wsTokenValidator
	upgrade websocket.Upgrader
}'''
new_b = '''type WebSocketHandler struct {
	ai      wsStreamer
	entity  entityCommentCreator
	auth    wsTokenValidator
	upgrade websocket.Upgrader

	// connsMu guards conns — the live-engineer-chat feature's registry of
	// which open browser connection currently owns a given conversation
	// (see registerConn/unregisterConnAll/PushEvent). Populated/cleared
	// entirely within HandleWebSocket's own lifecycle; unrelated to ai/
	// entity/auth/upgrade above, which are immutable after construction.
	connsMu sync.Mutex
	conns   map[string]*websocket.Conn
}'''
src = replace_once(src, old_b, new_b, "struct-fields")

# C) registerConn / unregisterConnAll / PushEvent methods, placed right
#    before HandleWebSocket's doc comment.
old_c = '''// HandleWebSocket handles GET /ws?sessionId={projectId}. The query parameter'''
new_c = '''// registerConn records that conversationID's live-engineer-chat events
// should be delivered to conn — called from handleMessage as soon as a
// conversationId is seen on this connection (see that method). Overwriting
// an existing entry is expected, not a bug: the frontend's
// useChatWebSocket.connect(sessionId) reuses one open connection for the
// project's lifetime (see this file's own package doc comment reference in
// NoveraChatPage.tsx), so every message on a given conversation re-registers
// the same conn — a cheap no-op in the common case.
func (h *WebSocketHandler) registerConn(conversationID string, conn *websocket.Conn) {
	h.connsMu.Lock()
	defer h.connsMu.Unlock()
	if h.conns == nil {
		h.conns = make(map[string]*websocket.Conn)
	}
	h.conns[conversationID] = conn
}

// unregisterConnAll removes every conversationID currently mapped to conn.
// Called once from HandleWebSocket's own deferred cleanup rather than
// threading a per-connection "which conversationIDs did I register" set
// through handleMessage — conns is small (open chat sessions on this
// replica, not a global table), so the linear scan here is cheap.
func (h *WebSocketHandler) unregisterConnAll(conn *websocket.Conn) {
	h.connsMu.Lock()
	defer h.connsMu.Unlock()
	for id, c := range h.conns {
		if c == conn {
			delete(h.conns, id)
		}
	}
}

// PushEvent delivers evt into conversationID's currently-registered
// connection, if any is open on this replica. Returns false — not an error,
// just "nothing to deliver to right now" — when no connection is
// registered or the write itself fails (e.g. the browser tab just closed);
// callers (see handler.ChatEventsHandler) treat both as best-effort and log
// rather than fail their own caller-facing response over it.
//
// Known limitation, shared with the rest of this live-engineer-chat feature
// (see csm-portal/backend's internal/handler/chat.go doc comment): conns is
// per-replica, in-memory only. A multi-replica deployment where the
// customer's WebSocket landed on a different pod than the one that receives
// this push would silently fail to deliver — there is no cross-replica fan-
// out here, matching this feature's other accepted simplifications at the
// project's current scale.
func (h *WebSocketHandler) PushEvent(conversationID string, evt wsEvent) bool {
	h.connsMu.Lock()
	conn := h.conns[conversationID]
	h.connsMu.Unlock()
	if conn == nil {
		return false
	}
	return writeWSJSON(conn, evt) == nil
}

// HandleWebSocket handles GET /ws?sessionId={projectId}. The query parameter'''
src = replace_once(src, old_c, new_c, "new-methods")

# D) defer cleanup in HandleWebSocket
old_d = '''	conn, err := h.upgrade.Upgrade(w, r, nil)
	if err != nil {
		slog.ErrorContext(r.Context(), "websocket upgrade failed", "userID", user.UserID, "err", summarizeErr(err))
		return
	}
	defer conn.Close()'''
new_d = '''	conn, err := h.upgrade.Upgrade(w, r, nil)
	if err != nil {
		slog.ErrorContext(r.Context(), "websocket upgrade failed", "userID", user.UserID, "err", summarizeErr(err))
		return
	}
	defer conn.Close()
	// Live-engineer-chat: drop every conversationId this connection ever
	// registered for (see registerConn/unregisterConnAll) once it closes,
	// so PushEvent never writes to a dead connection.
	defer h.unregisterConnAll(conn)'''
src = replace_once(src, old_d, new_d, "defer-cleanup")

# E) registration line at top of handleMessage
old_e = '''func (h *WebSocketHandler) handleMessage(ctx context.Context, conn *websocket.Conn, user *middleware.UserInfo, projectID, accountID string, data []byte) {
	trimmed := strings.TrimSpace(strings.ToLower(string(data)))
	var parsed map[string]any
	_ = json.Unmarshal(data, &parsed)'''
new_e = '''func (h *WebSocketHandler) handleMessage(ctx context.Context, conn *websocket.Conn, user *middleware.UserInfo, projectID, accountID string, data []byte) {
	trimmed := strings.TrimSpace(strings.ToLower(string(data)))
	var parsed map[string]any
	_ = json.Unmarshal(data, &parsed)

	// Live-engineer-chat: whichever conversation this message names becomes
	// (or stays) the one PushEvent delivers into on this connection — see
	// registerConn. Done unconditionally, before the ping/side-channel/main
	// dispatch below, since all three message kinds carry a conversationId
	// and any of them arriving is equally good evidence this connection is
	// live for that conversation right now.
	if convID, _ := parsed["conversationId"].(string); convID != "" && uuidRe.MatchString(convID) {
		h.registerConn(convID, conn)
	}'''
src = replace_once(src, old_e, new_e, "register-call")

with open(path, "w", encoding="utf-8") as f:
    f.write(src)

print("OK: all 5 patches applied")
