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

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wso2-open-operations/cs-tools/apps/console-chat-bridge/backend/internal/csmchat"
	"github.com/wso2-open-operations/cs-tools/apps/console-chat-bridge/backend/internal/middleware"
	"github.com/wso2-open-operations/cs-tools/apps/console-chat-bridge/backend/internal/stream"
	"github.com/wso2-open-operations/cs-tools/apps/console-chat-bridge/backend/internal/tenant"
	"github.com/wso2-open-operations/cs-tools/apps/console-chat-bridge/backend/internal/tokenvalidator"
)

type fakeValidator struct{ identity tokenvalidator.Identity }

func (f fakeValidator) Validate(context.Context, string) (*tokenvalidator.Identity, error) {
	id := f.identity
	return &id, nil
}

// fakeCSMPortal serves a client-credentials token endpoint and csm-portal's
// open-chat lookup, recording the query it received.
func fakeCSMPortal(t *testing.T, status int, body string) (*csmchat.Client, *http.Request) {
	t.Helper()
	got := &http.Request{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"m2m","token_type":"bearer","expires_in":3600}`))
		case "/internal/chat/open-chat":
			*got = *r.Clone(context.Background())
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return csmchat.NewClient(csmchat.Config{BaseURL: srv.URL, TokenURL: srv.URL + "/token", ClientID: "c", ClientSecret: "s"}), got
}

func getCurrentChat(t *testing.T, h *ChatsHandler, identity tokenvalidator.Identity) *httptest.ResponseRecorder {
	t.Helper()
	tn := &tenant.Tenant{Validator: fakeValidator{identity: identity}}
	tn.Slug = "devant"
	tn.ProjectID = "devant-support"
	r := httptest.NewRequest(http.MethodGet, "/v1/devant/chats/current", nil)
	r.Header.Set("Authorization", "Bearer user-token")
	r = r.WithContext(tenant.WithTenant(r.Context(), tn))
	w := httptest.NewRecorder()
	middleware.TenantAuth()(http.HandlerFunc(h.HandleCurrentChatV1)).ServeHTTP(w, r)
	return w
}

func TestHandleCurrentChatV1(t *testing.T) {
	jane := tokenvalidator.Identity{Subject: "sub-1", Username: "jane@example.com"}

	t.Run("returns the open chat looked up by the same customer key as escalation", func(t *testing.T) {
		csm, got := fakeCSMPortal(t, http.StatusOK, `{"openChat":{"caseId":"c-1","conversationId":"conv-1","accepted":true,"engineerEmail":"eng@example.com"}}`)
		h := NewChatsHandler(csm, stream.NewHub())

		w := getCurrentChat(t, h, jane)
		if w.Code != http.StatusOK {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
		if q := got.URL.Query(); q.Get("customerEmail") != "jane@example.com" || q.Get("projectId") != "devant-support" {
			t.Errorf("lookup query = %v", q)
		}
		var resp v1CurrentChatResponse
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		want := v1CurrentChatResponse{CaseID: "c-1", ConversationID: "conv-1", Status: "connected", EngineerEmail: "eng@example.com"}
		if resp != want {
			t.Errorf("got %+v, want %+v", resp, want)
		}
		if rec, ok := h.cases["c-1"]; !ok || rec.tenantSlug != "devant" || rec.ownerSubject != "sub-1" {
			t.Errorf("the case should be cached for later calls, got %+v", rec)
		}
	})

	t.Run("a chat not yet accepted is waiting", func(t *testing.T) {
		csm, _ := fakeCSMPortal(t, http.StatusOK, `{"openChat":{"caseId":"c-2","conversationId":"conv-2","accepted":false}}`)
		w := getCurrentChat(t, NewChatsHandler(csm, stream.NewHub()), jane)
		var resp v1CurrentChatResponse
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		if w.Code != http.StatusOK || resp.Status != "waiting" {
			t.Errorf("status %d, body %+v", w.Code, resp)
		}
	})

	t.Run("falls back to the subject when the token has no username", func(t *testing.T) {
		csm, got := fakeCSMPortal(t, http.StatusOK, `{"openChat":null}`)
		getCurrentChat(t, NewChatsHandler(csm, stream.NewHub()), tokenvalidator.Identity{Subject: "sub-only"})
		if got.URL.Query().Get("customerEmail") != "sub-only" {
			t.Errorf("customerEmail = %q, want the subject", got.URL.Query().Get("customerEmail"))
		}
	})

	t.Run("no open chat is 404", func(t *testing.T) {
		csm, _ := fakeCSMPortal(t, http.StatusOK, `{"openChat":null}`)
		if w := getCurrentChat(t, NewChatsHandler(csm, stream.NewHub()), jane); w.Code != http.StatusNotFound {
			t.Errorf("status %d, want 404", w.Code)
		}
	})

	t.Run("csm-portal failure is 502", func(t *testing.T) {
		csm, _ := fakeCSMPortal(t, http.StatusInternalServerError, `{}`)
		if w := getCurrentChat(t, NewChatsHandler(csm, stream.NewHub()), jane); w.Code != http.StatusBadGateway {
			t.Errorf("status %d, want 502", w.Code)
		}
	})

	t.Run("a token without a subject is rejected", func(t *testing.T) {
		csm, _ := fakeCSMPortal(t, http.StatusOK, `{"openChat":null}`)
		if w := getCurrentChat(t, NewChatsHandler(csm, stream.NewHub()), tokenvalidator.Identity{Username: "x@example.com"}); w.Code != http.StatusUnauthorized {
			t.Errorf("status %d, want 401", w.Code)
		}
	})
}
