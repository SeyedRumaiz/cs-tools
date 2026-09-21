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

package middleware_test

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/middleware"
)

// noopHandler writes 200 OK and is used as the inner handler in middleware tests.
var noopHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
})

// testConfig returns an auth config with signature validation disabled (safe for tests).
func testConfig() middleware.Config {
	return middleware.Config{
		JWKSEndpoint:          "http://unused.example.com",
		Issuer:                "test-issuer",
		Audiences:             []string{"test-audience"},
		TokenValidatorEnabled: false,
	}
}

// makeTestJWT builds a minimal unsigned JWT with the given claims.
// The token is accepted when TokenValidatorEnabled is false.
func makeTestJWT(claims map[string]any) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	claimsJSON, _ := json.Marshal(claims)
	payload := base64.RawURLEncoding.EncodeToString(claimsJSON)
	sig := base64.RawURLEncoding.EncodeToString([]byte("test-sig"))
	return header + "." + payload + "." + sig
}

func validToken() string {
	return makeTestJWT(map[string]any{
		"email":  "user@example.com",
		"userid": "uid-123",
	})
}

// serve is a convenience wrapper that runs the auth middleware around noopHandler.
func serve(r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	middleware.Auth(testConfig())(noopHandler).ServeHTTP(w, r)
	return w
}

// ----- health check -----

func TestAuth_HealthCheck(t *testing.T) {
	t.Run("skips auth and returns 200", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/health", nil)
		w := serve(r)
		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", w.Code)
		}
	})
}

// ----- token validation -----

func TestAuth_TokenValidation(t *testing.T) {
	tests := []struct {
		name     string
		token    string // empty string means no header set
		wantCode int
	}{
		{"missing token header", "", http.StatusUnauthorized},
		{"too few segments", "only-one-segment", http.StatusUnauthorized},
		{"too many segments", "a.b.c.d", http.StatusUnauthorized},
		{"valid token passes", validToken(), http.StatusOK},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/cases", nil)
			if tc.token != "" {
				r.Header.Set("x-jwt-assertion", tc.token)
			}
			w := serve(r)
			if w.Code != tc.wantCode {
				t.Errorf("status = %d, want %d", w.Code, tc.wantCode)
			}
		})
	}
}

// ----- required claims -----

func TestAuth_RequiredClaims(t *testing.T) {
	tests := []struct {
		name   string
		claims map[string]any
	}{
		{"missing email", map[string]any{"userid": "uid-123"}},
		{"missing userid", map[string]any{"email": "user@example.com"}},
		{"both claims missing", map[string]any{"sub": "someone"}},
	}
	for _, tc := range tests {
		t.Run(tc.name+" returns 401", func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/cases", nil)
			r.Header.Set("x-jwt-assertion", makeTestJWT(tc.claims))
			w := serve(r)
			if w.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", w.Code)
			}
		})
	}
}

// ----- user info injection -----

func TestAuth_UserInfoInjection(t *testing.T) {
	t.Run("injects email, userid and groups from token into context", func(t *testing.T) {
		var captured *middleware.UserInfo
		capture := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			captured = middleware.UserInfoFromContext(r.Context())
			w.WriteHeader(http.StatusOK)
		})

		r := httptest.NewRequest(http.MethodGet, "/cases", nil)
		r.Header.Set("x-jwt-assertion", makeTestJWT(map[string]any{
			"email":  "agent@wso2.com",
			"userid": "uid-456",
			"groups": []string{"csm-agents", "csm-admins"},
		}))
		w := httptest.NewRecorder()
		middleware.Auth(testConfig())(capture).ServeHTTP(w, r)

		if captured == nil {
			t.Fatal("UserInfo not set in context")
		}
		if captured.Email != "agent@wso2.com" {
			t.Errorf("email = %q, want agent@wso2.com", captured.Email)
		}
		if captured.UserID != "uid-456" {
			t.Errorf("userID = %q, want uid-456", captured.UserID)
		}
		if len(captured.Groups) != 2 {
			t.Errorf("groups = %v, want 2 entries", captured.Groups)
		}
	})
}

// ----- security headers -----

func TestAuth_SecurityHeaders(t *testing.T) {
	wantHeaders := map[string]string{
		"X-Content-Type-Options":    "nosniff",
		"Content-Security-Policy":   "upgrade-insecure-requests",
		"Strict-Transport-Security": "max-age=31536000; includeSubDomains",
	}

	cases := []struct {
		name  string
		setup func(*http.Request)
	}{
		{"authenticated request", func(r *http.Request) { r.Header.Set("x-jwt-assertion", validToken()) }},
		{"unauthenticated request", func(_ *http.Request) {}},
		{"health check", func(r *http.Request) { /* no token needed */ }},
	}
	for _, tc := range cases {
		t.Run(tc.name+" has security headers", func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/cases", nil)
			if tc.name == "health check" {
				r = httptest.NewRequest(http.MethodGet, "/health", nil)
			}
			tc.setup(r)
			w := serve(r)
			for name, want := range wantHeaders {
				if got := w.Header().Get(name); got != want {
					t.Errorf("header %s = %q, want %q", name, got, want)
				}
			}
		})
	}
}

// ----- M2M-exempt internal routes -----

// TestAuth_M2MExemptRoutes verifies that the two pure machine-to-machine
// internal chat routes (see middleware.m2mExemptRoutes) bypass token
// validation entirely -- no x-jwt-assertion header needed -- while a
// same-path GET (the wrong method) and an unrelated POST still require one,
// so the exemption is scoped to exactly "METHOD path", not the path alone.
func TestAuth_M2MExemptRoutes(t *testing.T) {
	exempt := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/internal/chat/escalate"},
		{http.MethodPost, "/internal/chat/customer-message"},
	}
	for _, tc := range exempt {
		t.Run(tc.method+" "+tc.path+" skips auth with no token", func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.path, nil)
			w := serve(r)
			if w.Code != http.StatusOK {
				t.Errorf("status = %d, want 200 (exempt route, no token needed)", w.Code)
			}
		})
	}

	notExempt := []struct {
		name   string
		method string
		path   string
	}{
		{"GET on an exempt path still requires auth", http.MethodGet, "/internal/chat/escalate"},
		{"unrelated internal-looking path still requires auth", http.MethodPost, "/internal/chat/escalate-typo"},
	}
	for _, tc := range notExempt {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.path, nil)
			w := serve(r)
			if w.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401 (not an exempt route)", w.Code)
			}
		})
	}
}

// ----- error response shape -----

func TestAuth_ErrorResponse(t *testing.T) {
	t.Run("is JSON with a message field", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/cases", nil) // no token
		w := serve(r)

		if ct := w.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}
		var body struct {
			Message string `json:"message"`
		}
		if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
			t.Fatalf("decode error body: %v", err)
		}
		if body.Message == "" {
			t.Error("expected non-empty message in error response")
		}
	})
}

// ----- local-development Authorization: Bearer fallback -----

// newTestJWKSServer starts a local httptest server serving a syntactically
// valid, empty JWKS document, purely so Auth's construction-time JWKS fetch
// succeeds when a test needs TokenValidatorEnabled=true. None of the tests
// below that use it ever reach real signature verification -- each one is
// rejected (missing/empty token) before the keyFunc is ever invoked -- so an
// empty keyset is sufficient.
func newTestJWKSServer(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"keys":[]}`))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func tokenFor(email string) string {
	return makeTestJWT(map[string]any{
		"email":  email,
		"userid": "uid-" + email,
	})
}

func serveWithConfig(cfg middleware.Config, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	middleware.Auth(cfg)(noopHandler).ServeHTTP(w, r)
	return w
}

func TestAuth_ValidatorEnabled_NoAssertion_Rejected(t *testing.T) {
	cfg := testConfig()
	cfg.TokenValidatorEnabled = true
	cfg.JWKSEndpoint = newTestJWKSServer(t)

	tests := []struct {
		name    string
		headers func(*http.Request)
	}{
		{"no headers at all", func(_ *http.Request) {}},
		{
			"Authorization: Bearer present but no x-jwt-assertion",
			func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+validToken()) },
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/cases", nil)
			tc.headers(r)
			w := serveWithConfig(cfg, r)
			if w.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401 (the Authorization fallback must never apply when TokenValidatorEnabled=true)", w.Code)
			}
		})
	}
}

func TestAuth_ValidatorDisabled_BearerFallback_Accepted(t *testing.T) {
	cfg := testConfig() // TokenValidatorEnabled: false

	t.Run("Bearer token accepted with no x-jwt-assertion", func(t *testing.T) {
		var captured *middleware.UserInfo
		capture := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			captured = middleware.UserInfoFromContext(r.Context())
			w.WriteHeader(http.StatusOK)
		})

		r := httptest.NewRequest(http.MethodGet, "/cases", nil)
		r.Header.Set("Authorization", "Bearer "+tokenFor("bearer-user@example.com"))
		w := httptest.NewRecorder()
		middleware.Auth(cfg)(capture).ServeHTTP(w, r)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		if captured == nil {
			t.Fatal("UserInfo not set in context")
		}
		if captured.Email != "bearer-user@example.com" {
			t.Errorf("email = %q, want bearer-user@example.com", captured.Email)
		}
	})

	t.Run("lowercase bearer scheme also accepted", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/cases", nil)
		r.Header.Set("Authorization", "bearer "+validToken())
		w := serveWithConfig(cfg, r)
		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", w.Code)
		}
	})
}

func TestAuth_AssertionTakesPrecedenceOverBearer(t *testing.T) {
	cfg := testConfig() // TokenValidatorEnabled: false

	t.Run("x-jwt-assertion identity wins when both headers are present", func(t *testing.T) {
		var captured *middleware.UserInfo
		capture := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			captured = middleware.UserInfoFromContext(r.Context())
			w.WriteHeader(http.StatusOK)
		})

		r := httptest.NewRequest(http.MethodGet, "/cases", nil)
		r.Header.Set("x-jwt-assertion", tokenFor("assertion-user@example.com"))
		r.Header.Set("Authorization", "Bearer "+tokenFor("bearer-user@example.com"))
		w := httptest.NewRecorder()
		middleware.Auth(cfg)(capture).ServeHTTP(w, r)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		if captured == nil {
			t.Fatal("UserInfo not set in context")
		}
		if captured.Email != "assertion-user@example.com" {
			t.Errorf("email = %q, want assertion-user@example.com (x-jwt-assertion should win)", captured.Email)
		}
	})

	t.Run("a malformed Authorization header does not block a valid x-jwt-assertion", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/cases", nil)
		r.Header.Set("x-jwt-assertion", validToken())
		r.Header.Set("Authorization", "not-a-bearer-token")
		w := serve(r)
		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want 200 (x-jwt-assertion alone is sufficient)", w.Code)
		}
	})
}

func TestAuth_MalformedOrMissingBearer_Rejected(t *testing.T) {
	tests := []struct {
		name    string
		headers func(*http.Request)
	}{
		{"no Authorization header at all", func(_ *http.Request) {}},
		{"empty Authorization header", func(r *http.Request) { r.Header.Set("Authorization", "") }},
		{"wrong scheme", func(r *http.Request) { r.Header.Set("Authorization", "Basic "+validToken()) }},
		{"scheme with no token", func(r *http.Request) { r.Header.Set("Authorization", "Bearer") }},
		{"scheme with no space", func(r *http.Request) { r.Header.Set("Authorization", "Bearer"+validToken()) }},
		{"Bearer with only whitespace as the token", func(r *http.Request) { r.Header.Set("Authorization", "Bearer    ") }},
		{"malformed token: too few JWT segments", func(r *http.Request) { r.Header.Set("Authorization", "Bearer only-one-segment") }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/cases", nil)
			tc.headers(r)
			w := serve(r) // uses testConfig(): TokenValidatorEnabled false
			if w.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", w.Code)
			}
		})
	}
}
