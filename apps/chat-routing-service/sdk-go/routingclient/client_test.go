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

package routingclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newTestClient starts an httptest.Server whose handler is the caller's own
// fake chat-routing-service, and returns a Client pointed at it. The server
// is closed automatically via t.Cleanup.
func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return NewClient(Config{BaseURL: srv.URL, InternalToken: "test-token"})
}

// writeConflict writes a 409 response shaped like writeError in
// chat-routing-service/backend/internal/handler/routes.go.
func writeConflict(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)
	_ = json.NewEncoder(w).Encode(map[string]string{"message": message})
}

// TestEscalate_409MapsToErrCaseAlreadyEnded confirms Escalate's do() call
// translates a 409 from POST /route/escalate into ErrCaseAlreadyEnded --
// mirroring router.Router.Escalate's own ErrCaseAlreadyEnded on the server
// side (see routes.go's Escalate handler).
func TestEscalate_409MapsToErrCaseAlreadyEnded(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/route/escalate" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		writeConflict(w, "This chat session has already ended and cannot be escalated again.")
	})

	_, err := c.Escalate(context.Background(), CaseInfo{CaseID: "case-1", ConversationID: "conv-1"})
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !errors.Is(err, ErrCaseAlreadyEnded) {
		t.Fatalf("expected errors.Is(err, ErrCaseAlreadyEnded), got: %v", err)
	}
	if errors.Is(err, ErrDuplicateOpenChat) {
		t.Fatalf("did not expect this 409 to also match ErrDuplicateOpenChat: %v", err)
	}
}

// TestCreateWorkItem_409MapsToErrDuplicateOpenChat confirms CreateWorkItem's
// do() call translates a 409 from POST /route/workitem into
// ErrDuplicateOpenChat -- mirroring router.ErrDuplicateOpenChat on the
// server side (see routes.go's CreateWorkItem handler). Uses a distinct
// sentinel from Escalate's own 409 mapping despite both being status 409,
// since the two endpoints mean different things by it -- see do()'s own
// doc comment on the per-call conflictErr parameter.
func TestCreateWorkItem_409MapsToErrDuplicateOpenChat(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/route/workitem" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		writeConflict(w, "This customer already has an open live chat for this project.")
	})

	err := c.CreateWorkItem(context.Background(), CaseInfo{CaseID: "case-1", ConversationID: "conv-1"})
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !errors.Is(err, ErrDuplicateOpenChat) {
		t.Fatalf("expected errors.Is(err, ErrDuplicateOpenChat), got: %v", err)
	}
	if errors.Is(err, ErrCaseAlreadyEnded) {
		t.Fatalf("did not expect this 409 to also match ErrCaseAlreadyEnded: %v", err)
	}
}

// TestDecline_409HasNoConflictErrMapping confirms a call that passes nil
// for conflictErr (every method other than Escalate/CreateWorkItem) still
// reports a plain, non-sentinel error on 409 rather than panicking or
// spuriously matching one of the two sentinels above -- a 409 has no
// defined meaning for this endpoint.
func TestDecline_409HasNoConflictErrMapping(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeConflict(w, "unexpected conflict")
	})

	_, err := c.Decline(context.Background(), "user-1", "case-1")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if errors.Is(err, ErrCaseAlreadyEnded) || errors.Is(err, ErrDuplicateOpenChat) || errors.Is(err, ErrConversationEnded) {
		t.Fatalf("expected a plain error with no sentinel mapping for Decline's 409, got: %v", err)
	}
}

// TestAddComment_410StillMapsToErrConversationEnded is a regression guard:
// adding the conflictErr parameter to do() must not disturb the pre-existing
// 410 -> ErrConversationEnded mapping, which every endpoint shares
// regardless of conflictErr.
func TestAddComment_410StillMapsToErrConversationEnded(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusGone)
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "This chat session has already ended."})
	})

	err := c.AddComment(context.Background(), "case-1", "customer@example.com", "hello")
	if !errors.Is(err, ErrConversationEnded) {
		t.Fatalf("expected errors.Is(err, ErrConversationEnded), got: %v", err)
	}
}

// TestEscalate_SuccessStillDecodesNormally is a sanity check that adding
// the conflictErr parameter didn't disturb the successful-response path.
func TestEscalate_SuccessStillDecodesNormally(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(EscalateResult{EngineerUserID: "engineer-1"})
	})

	result, err := c.Escalate(context.Background(), CaseInfo{CaseID: "case-1", ConversationID: "conv-1"})
	if err != nil {
		t.Fatalf("Escalate: %v", err)
	}
	if result.EngineerUserID != "engineer-1" {
		t.Fatalf("expected EngineerUserID engineer-1, got %+v", result)
	}
}
