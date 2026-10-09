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
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

// httpMaxResponseBytes bounds a non-streamed answer.
const httpMaxResponseBytes = 1 << 20

// HTTPConfig locates a product's own AI service. Auth is a client
// credentials token when TokenURL is set, else Token as a fixed bearer
// token when set, else none.
type HTTPConfig struct {
	URL          string
	Token        string
	TokenURL     string
	ClientID     string
	ClientSecret string
	Scopes       []string
}

// HTTP is a Provider backed by a product's own AI service, for products
// that keep their own assistant (its own model, knowledge and tools)
// behind the shared chat panel. The bridge POSTs each question as JSON:
//
//	{"conversationId": "...", "accountId": "...", "tenant": "...", "message": "..."}
//
// and the service answers either as Server-Sent Events, each data line one
// of {"type": "status"|"token"|"done"|"error", "text": "..."}, or as one
// JSON object {"answer": "..."}. The service keeps each conversation's
// history itself, keyed by conversationId. See docs/ASSISTANT_PROVIDERS.md.
type HTTP struct {
	url    string
	token  string
	tokens oauth2.TokenSource
	client *http.Client
}

// NewHTTP builds an HTTP provider.
func NewHTTP(cfg HTTPConfig) *HTTP {
	h := &HTTP{url: cfg.URL, token: cfg.Token, client: &http.Client{}}
	if cfg.TokenURL != "" {
		cc := clientcredentials.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			TokenURL:     cfg.TokenURL,
			Scopes:       cfg.Scopes,
		}
		tokenCtx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Timeout: 15 * time.Second})
		h.tokens = cc.TokenSource(tokenCtx)
	}
	return h
}

// Answer implements Provider.
func (h *HTTP) Answer(ctx context.Context, turn Turn, emit func(Event)) {
	fail := func(err error) {
		slog.ErrorContext(ctx, "assistant: http answer failed", "conversationId", turn.ConversationID, "err", err)
		emit(Event{Type: EventError, Text: "The assistant is not available right now."})
	}

	body, _ := json.Marshal(map[string]string{
		"conversationId": turn.ConversationID,
		"accountId":      turn.AccountID,
		"tenant":         turn.TenantSlug,
		"message":        turn.Message,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.url, bytes.NewReader(body))
	if err != nil {
		fail(err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream, application/json")
	switch {
	case h.tokens != nil:
		token, err := h.tokens.Token()
		if err != nil {
			fail(err)
			return
		}
		req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	case h.token != "":
		req.Header.Set("Authorization", "Bearer "+h.token)
	}

	resp, err := h.client.Do(req)
	if err != nil {
		fail(err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		fail(fmt.Errorf("the AI service answered with status %d", resp.StatusCode))
		return
	}

	mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if mediaType == "text/event-stream" {
		h.relayStream(resp.Body, emit, fail)
		return
	}
	var answer struct {
		Answer string `json:"answer"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, httpMaxResponseBytes)).Decode(&answer); err != nil || answer.Answer == "" {
		fail(fmt.Errorf("the AI service did not answer with {\"answer\": \"...\"}: %v", err))
		return
	}
	emit(Event{Type: EventToken, Text: answer.Answer})
	emit(Event{Type: EventDone, Text: answer.Answer})
}

// relayStream forwards the service's events until done or error. A stream
// that ends without either still counts as done when it sent some text.
func (h *HTTP) relayStream(body io.Reader, emit func(Event), fail func(error)) {
	var answer strings.Builder
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64<<10), httpMaxResponseBytes)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		var evt Event
		if json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &evt) != nil {
			continue
		}
		switch evt.Type {
		case EventStatus:
			emit(evt)
		case EventToken:
			answer.WriteString(evt.Text)
			emit(evt)
		case EventDone:
			if evt.Text == "" {
				evt.Text = answer.String()
			}
			emit(evt)
			return
		case EventError:
			if evt.Text == "" {
				evt.Text = "The assistant could not answer that."
			}
			emit(evt)
			return
		}
	}
	if err := scanner.Err(); err != nil || answer.Len() == 0 {
		fail(fmt.Errorf("the AI service's stream ended without an answer: %v", err))
		return
	}
	emit(Event{Type: EventDone, Text: answer.String()})
}
