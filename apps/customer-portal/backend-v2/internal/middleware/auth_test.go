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

	"github.com/wso2-open-operations/cs-tools/apps/customer-portal/backend-v2/internal/middleware"
)

// noopHandler writes 200 OK and is used as the inner handler in middleware tests.
var noopHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
})

// testConfig returns an auth config with signature validation disabled --
// the .env.example local-development default (AUTH_TOKEN_VALIDATOR_ENABLED=
// false). Tests that need TokenValidatorEnabled=true override it explicitly
// and must also supply a reachable JWKSEndpoint (see newTestJWKSServer)
// since NewTokenValidator fetches the JWKS eagerly and panics if that fails.
func testConfig() middleware.Config {
	return middleware.Config{
		JWKSEndpoint:          "http://unused.example.com",
		Issuer:                "test-issuer",
		Audiences:             []string{"test-audience"},
		TokenValidatorEnabled: false,
	}
}

// newTestJWKSServer starts a local httptest server serving a syntactically
// valid, empty JWKS document, purely so NewTokenValidator's construction-time
// fetch succeeds when a test needs TokenValidatorEnabled=true. None of the
// tests below that use it ever reach real signature verification -- each one
// is rejected (missing/empty token) before Validate's keyFunc is ever
// invoked -- so an empty keyset is sufficient.
func newTestJWKSServer(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"keys":[]}`))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
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

func tokenFor(email string) string {
	return makeTestJWT(map[string]any{
		"email":  email,
		"userid": "uid-" + email,
	})
}

func validToken() string {
	return tokenFor("user@example.com")
}

func serve(cfg middleware.Config, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	middleware.Auth(cfg)(noopHandler).ServeHTTP(w, r)
	return w
}

// ----- validator enabled: x-jwt-assertion required exactly as before -----

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
			r := httptest.NewRequest(http.MethodGet, "/users/me", nil)
			tc.headers(r)
			w := serve(cfg, r)
			if w.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401 (the Authorization fallback must never apply when TokenValidatorEnabled=true)", w.Code)
			}
		})
	}
}

// ----- validator disabled: Authorization: Bearer fallback -----

func TestAuth_ValidatorDisabled_BearerFallback_Accepted(t *testing.T) {
	cfg := testConfig() // TokenValidatorEnabled: false

	t.Run("Bearer token accepted with no x-jwt-assertion", func(t *testing.T) {
		var captured *middleware.UserInfo
		capture := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			captured = middleware.UserInfoFromContext(r.Context())
			w.WriteHeader(http.StatusOK)
		})

		r := httptest.NewRequest(http.MethodGet, "/users/me", nil)
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
		r := httptest.NewRequest(http.MethodGet, "/users/me", nil)
		r.Header.Set("Authorization", "bearer "+validToken())
		w := serve(cfg, r)
		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", w.Code)
		}
	})
}

// ----- x-jwt-assertion still takes precedence over Authorization -----

func TestAuth_AssertionTakesPrecedenceOverBearer(t *testing.T) {
	cfg := testConfig() // TokenValidatorEnabled: false

	t.Run("x-jwt-assertion identity wins when both headers are present", func(t *testing.T) {
		var captured *middleware.UserInfo
		capture := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			captured = middleware.UserInfoFromContext(r.Context())
			w.WriteHeader(http.StatusOK)
		})

		r := httptest.NewRequest(http.MethodGet, "/users/me", nil)
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
		r := httptest.NewRequest(http.MethodGet, "/users/me", nil)
		r.Header.Set("x-jwt-assertion", validToken())
		r.Header.Set("Authorization", "not-a-bearer-token")
		w := serve(cfg, r)
		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want 200 (x-jwt-assertion alone is sufficient)", w.Code)
		}
	})
}

// ----- malformed/missing Bearer token -----

func TestAuth_MalformedOrMissingBearer_Rejected(t *testing.T) {
	cfg := testConfig() // TokenValidatorEnabled: false

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
			r := httptest.NewRequest(http.MethodGet, "/users/me", nil)
			tc.headers(r)
			w := serve(cfg, r)
			if w.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", w.Code)
			}
		})
	}
}
