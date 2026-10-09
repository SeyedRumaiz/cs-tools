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

package assistant

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

// noveraReadTimeout bounds the wait for Novera's next event; an answer that
// uses tools can pause for a while between events.
const noveraReadTimeout = 2 * time.Minute

// noveraMaxFrameBytes bounds one event from Novera.
const noveraMaxFrameBytes = 256 << 10

// NoveraConfig locates the Novera support assistant and the client
// credentials used to reach it through its API gateway.
type NoveraConfig struct {
	// WSBaseURL is Novera's WebSocket base URL, without the /ws path.
	WSBaseURL    string
	TokenURL     string
	ClientID     string
	ClientSecret string
	Scopes       []string
}

// Novera is a Provider backed by the Novera support assistant (Claude with
// WSO2 knowledge-base search), over its WebSocket protocol: one connection
// per answer, a user_message frame, then token, thinking_step, final or
// error events. Novera keeps each conversation's history itself, keyed by
// account and conversation.
type Novera struct {
	baseURL string
	tokens  oauth2.TokenSource
	dialer  *websocket.Dialer
}

// NewNovera builds a Novera provider.
func NewNovera(cfg NoveraConfig) *Novera {
	cc := clientcredentials.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		TokenURL:     cfg.TokenURL,
		Scopes:       cfg.Scopes,
	}
	tokenCtx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Timeout: 15 * time.Second})
	return &Novera{
		baseURL: strings.TrimRight(cfg.WSBaseURL, "/"),
		tokens:  cc.TokenSource(tokenCtx),
		dialer:  &websocket.Dialer{HandshakeTimeout: 15 * time.Second},
	}
}

// noveraEvent covers every Novera event field the provider reads.
type noveraEvent struct {
	Type    string `json:"type"`
	Content string `json:"content"`
	Message string `json:"message"`
	Step    string `json:"step"`
	Label   string `json:"label"`
	Payload struct {
		Message string `json:"message"`
	} `json:"payload"`
}

// Answer implements Provider.
func (n *Novera) Answer(ctx context.Context, turn Turn, emit func(Event)) {
	fail := func(msg string, err error) {
		slog.ErrorContext(ctx, "assistant: novera answer failed", "conversationId", turn.ConversationID, "err", err)
		emit(Event{Type: EventError, Text: msg})
	}

	token, err := n.tokens.Token()
	if err != nil {
		fail("The assistant is not available right now.", err)
		return
	}
	wsURL := strings.Replace(n.baseURL, "https://", "wss://", 1)
	wsURL = strings.Replace(wsURL, "http://", "ws://", 1)
	wsURL += "/ws?sessionId=" + url.QueryEscape(turn.AccountID+":"+turn.ConversationID)
	header := http.Header{"Authorization": {"Bearer " + token.AccessToken}}

	conn, _, err := n.dialer.DialContext(ctx, wsURL, header)
	if err != nil {
		fail("The assistant is not available right now.", err)
		return
	}
	defer conn.Close()
	conn.SetReadLimit(noveraMaxFrameBytes)
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()

	frame, _ := json.Marshal(map[string]string{
		"type":           "user_message",
		"accountId":      turn.AccountID,
		"conversationId": turn.ConversationID,
		"message":        turn.Message,
	})
	if err := conn.WriteMessage(websocket.TextMessage, frame); err != nil {
		fail("The assistant is not available right now.", err)
		return
	}

	var answer strings.Builder
	for {
		_ = conn.SetReadDeadline(time.Now().Add(noveraReadTimeout))
		_, data, err := conn.ReadMessage()
		if err != nil {
			fail("The assistant stopped before finishing its answer.", err)
			return
		}
		var evt noveraEvent
		if json.Unmarshal(data, &evt) != nil {
			continue
		}
		switch evt.Type {
		case "token":
			answer.WriteString(evt.Content)
			emit(Event{Type: EventToken, Text: evt.Content})
		case "thinking_step":
			// Tool steps carry a readable label; reasoning steps carry raw
			// model thoughts, which are not shown to customers.
			if evt.Step == "tool_activity" && evt.Label != "" {
				emit(Event{Type: EventStatus, Text: evt.Label})
			}
		case "token_limit", "error":
			msg := evt.Message
			if msg == "" {
				msg = "The assistant could not answer that."
			}
			emit(Event{Type: EventError, Text: msg})
			return
		case "final":
			text := evt.Payload.Message
			if text == "" {
				text = answer.String()
			}
			emit(Event{Type: EventDone, Text: text})
			_ = conn.WriteControl(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseNormalClosure, "answer complete"),
				time.Now().Add(2*time.Second))
			return
		}
	}
}
