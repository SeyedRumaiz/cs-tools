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

package m2mclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newTestClient spins up a fake token endpoint (always issues a fixed
// bearer token) and a fake target endpoint, returning a Client pointed at
// the target plus a way to inspect the last request the target received.
func newTestClient(t *testing.T, targetHandler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()

	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"test-token","token_type":"Bearer","expires_in":3600}`))
	}))
	t.Cleanup(tokenSrv.Close)

	targetSrv := httptest.NewServer(targetHandler)
	t.Cleanup(targetSrv.Close)

	c := NewClient(Config{
		BaseURL:      targetSrv.URL,
		TokenURL:     tokenSrv.URL,
		ClientID:     "test-client",
		ClientSecret: "test-secret",
	})
	return c, targetSrv
}

func TestDo_AttachesBearerTokenAndContentType(t *testing.T) {
	var gotAuth, gotContentType string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
	})

	_, status, err := c.Do(context.Background(), http.MethodPost, "/push", []byte(`{}`), nil)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if gotAuth != "Bearer test-token" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer test-token")
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", gotContentType)
	}
}

func TestDo_ExtraHeadersOverrideDefaults(t *testing.T) {
	var gotHeader string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-User-Id-Token")
		w.WriteHeader(http.StatusCreated)
	})

	_, _, err := c.Do(context.Background(), http.MethodPost, "/create-case", []byte(`{}`),
		map[string]string{"X-User-Id-Token": "abc123"})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if gotHeader != "abc123" {
		t.Errorf("X-User-Id-Token = %q, want abc123", gotHeader)
	}
}

func TestDo_NonSuccessStatusIsNotAnError(t *testing.T) {
	// A 409 (e.g. "you already have an open live chat") must come back as a
	// normal result, not an error -- callers like console-chat-bridge's
	// HandleEscalate pass a specific non-2xx status straight through to
	// their own caller instead of treating every non-2xx as failure.
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"message":"already open"}`))
	})

	body, status, err := c.Do(context.Background(), http.MethodPost, "/escalate", []byte(`{}`), nil)
	if err != nil {
		t.Fatalf("Do returned an error for a 409, want (body, 409, nil): %v", err)
	}
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want 409", status)
	}
	var decoded map[string]string
	if err := json.Unmarshal(body, &decoded); err != nil || decoded["message"] != "already open" {
		t.Errorf("unexpected body: %s", body)
	}
}

func TestDo_TransportFailureIsAnError(t *testing.T) {
	c := NewClient(Config{
		BaseURL:      "http://127.0.0.1:0", // nothing listens here
		TokenURL:     "http://127.0.0.1:0",
		ClientID:     "x",
		ClientSecret: "y",
	})
	_, _, err := c.Do(context.Background(), http.MethodPost, "/push", []byte(`{}`), nil)
	if err == nil {
		t.Fatal("expected an error for an unreachable target, got nil")
	}
}

func TestDo_NeverFollowsRedirect(t *testing.T) {
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("redirect target must never be reached -- the client must not follow the redirect")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(redirectTarget.Close)

	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, redirectTarget.URL, http.StatusFound)
	})

	_, status, err := c.Do(context.Background(), http.MethodPost, "/push", []byte(`{}`), nil)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if status != http.StatusFound {
		t.Errorf("status = %d, want 302 (the redirect response itself, not followed)", status)
	}
}

func TestSuccess(t *testing.T) {
	cases := map[int]bool{199: false, 200: true, 201: true, 299: true, 300: false, 404: false, 500: false}
	for status, want := range cases {
		if got := Success(status); got != want {
			t.Errorf("Success(%d) = %v, want %v", status, got, want)
		}
	}
}
