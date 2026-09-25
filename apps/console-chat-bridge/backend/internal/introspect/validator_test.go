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

package introspect

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func jsonHandler(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

// failIfCalledHandler fails the test if the endpoint it's mounted on is
// ever hit -- used below to prove ValidateBearer never touches userinfo
// under any circumstances, not just the ones where a Subject happens to
// already be present.
func failIfCalledHandler(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("userinfo endpoint was called but should not have been: %s", r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}
}

// ---- ValidateBearer: base validation only, NEVER touches userinfo ----
//
// This is the legacy /support path's entire validation contract (via
// middleware.Auth, which calls ValidateBearer directly) -- these tests
// prove it has no dependency on UserInfo being configured, reachable, or
// even correctly implemented by the IdP, regardless of whether the
// introspection response happens to carry a "sub" or not. See
// tokenvalidator.IntrospectionValidator's own tests for the /v1-specific
// enrichment/requirement behavior built on top of this.

func TestValidateBearer_IntrospectionHasSub_NeverCallsUserinfo(t *testing.T) {
	introspectSrv := httptest.NewServer(jsonHandler(http.StatusOK, `{"active":true,"sub":"user-uuid-1","username":"jane@example.com","client_id":"c1"}`))
	t.Cleanup(introspectSrv.Close)
	userinfoSrv := httptest.NewServer(failIfCalledHandler(t))
	t.Cleanup(userinfoSrv.Close)

	v := NewValidator(Config{IssuerBaseURL: introspectSrv.URL, UserinfoURL: userinfoSrv.URL})

	id, err := v.ValidateBearer(context.Background(), "tok")
	if err != nil {
		t.Fatalf("ValidateBearer: %v", err)
	}
	if id.Subject != "user-uuid-1" {
		t.Errorf("Subject = %q, want %q", id.Subject, "user-uuid-1")
	}
}

func TestValidateBearer_InactiveToken_NeverCallsUserinfo(t *testing.T) {
	introspectSrv := httptest.NewServer(jsonHandler(http.StatusOK, `{"active":false}`))
	t.Cleanup(introspectSrv.Close)
	userinfoSrv := httptest.NewServer(failIfCalledHandler(t))
	t.Cleanup(userinfoSrv.Close)

	v := NewValidator(Config{IssuerBaseURL: introspectSrv.URL, UserinfoURL: userinfoSrv.URL})

	_, err := v.ValidateBearer(context.Background(), "tok")
	if err == nil {
		t.Fatal("ValidateBearer: expected an error for an inactive token, got nil")
	}
}

// TestValidateBearer_NoSub_SucceedsWithEmptySubject_EvenWhenUserinfoIsDown
// is the regression test for the bug this file's rewrite fixes: an opaque
// token whose introspection response has no "sub" must still validate
// successfully -- with Username populated for the legacy path's own
// Subject-or-Username fallback (see ownerIdentity in internal/handler/
// chats.go) -- even when the configured UserinfoURL points at nothing
// reachable at all. Base validation (this method) must never gain a
// dependency on UserInfo availability.
func TestValidateBearer_NoSub_SucceedsWithEmptySubject_EvenWhenUserinfoIsDown(t *testing.T) {
	introspectSrv := httptest.NewServer(jsonHandler(http.StatusOK, `{"active":true,"username":"jane@example.com","client_id":"c1"}`))
	t.Cleanup(introspectSrv.Close)

	// A UserinfoURL pointing at an address nothing is listening on --
	// every call would fail at the transport level. ValidateBearer must
	// never attempt it, so this dead address never actually gets dialed.
	v := NewValidator(Config{
		IssuerBaseURL: introspectSrv.URL,
		UserinfoURL:   "http://127.0.0.1:1/oauth2/userinfo",
	})

	id, err := v.ValidateBearer(context.Background(), "tok")
	if err != nil {
		t.Fatalf("ValidateBearer: %v (must succeed regardless of UserInfo availability)", err)
	}
	if id.Subject != "" {
		t.Errorf("Subject = %q, want empty (base validation must not enrich it)", id.Subject)
	}
	if id.Username != "jane@example.com" {
		t.Errorf("Username = %q, want %q (legacy Subject-or-Username fallback needs this)", id.Username, "jane@example.com")
	}
}

func TestValidateBearer_ClientCredentialsToken_NeverCallsUserinfo(t *testing.T) {
	introspectSrv := httptest.NewServer(jsonHandler(http.StatusOK, `{"active":true,"client_id":"m2m-client"}`))
	t.Cleanup(introspectSrv.Close)
	userinfoSrv := httptest.NewServer(failIfCalledHandler(t))
	t.Cleanup(userinfoSrv.Close)

	v := NewValidator(Config{IssuerBaseURL: introspectSrv.URL, UserinfoURL: userinfoSrv.URL})

	id, err := v.ValidateBearer(context.Background(), "tok")
	if err != nil {
		t.Fatalf("ValidateBearer: %v", err)
	}
	if id.Subject != "" {
		t.Errorf("Subject = %q, want empty for a client-credentials token", id.Subject)
	}
	if id.ClientID != "m2m-client" {
		t.Errorf("ClientID = %q, want %q", id.ClientID, "m2m-client")
	}
}

// ---- ResolveSubjectViaUserinfo: the opt-in enrichment primitive ----
//
// Called only by tokenvalidator.IntrospectionValidator (the /v1-specific
// layer) -- never by ValidateBearer itself. These tests exercise it
// directly, in isolation from any caller's policy about whether a failure
// here should be fatal.

func newUserinfoValidator(t *testing.T, userinfoHandler http.HandlerFunc) *Validator {
	t.Helper()
	userinfoSrv := httptest.NewServer(userinfoHandler)
	t.Cleanup(userinfoSrv.Close)
	return NewValidator(Config{IssuerBaseURL: "https://issuer.example.invalid", UserinfoURL: userinfoSrv.URL})
}

func TestResolveSubjectViaUserinfo_Success(t *testing.T) {
	v := newUserinfoValidator(t, jsonHandler(http.StatusOK, `{"sub":"user-uuid-2"}`))

	sub, err := v.ResolveSubjectViaUserinfo(context.Background(), "tok")
	if err != nil {
		t.Fatalf("ResolveSubjectViaUserinfo: %v", err)
	}
	if sub != "user-uuid-2" {
		t.Errorf("sub = %q, want %q", sub, "user-uuid-2")
	}
}

func TestResolveSubjectViaUserinfo_InsufficientScope403(t *testing.T) {
	v := newUserinfoValidator(t, jsonHandler(http.StatusForbidden, `{"error":"insufficient_scope"}`))

	_, err := v.ResolveSubjectViaUserinfo(context.Background(), "tok")
	if !errors.Is(err, ErrUserinfoInsufficientScope) {
		t.Errorf("err = %v, want errors.Is(err, ErrUserinfoInsufficientScope)", err)
	}
}

func TestResolveSubjectViaUserinfo_401_IsInsufficientScope(t *testing.T) {
	v := newUserinfoValidator(t, jsonHandler(http.StatusUnauthorized, `{"error":"invalid_token"}`))

	_, err := v.ResolveSubjectViaUserinfo(context.Background(), "tok")
	if !errors.Is(err, ErrUserinfoInsufficientScope) {
		t.Errorf("err = %v, want errors.Is(err, ErrUserinfoInsufficientScope)", err)
	}
}

func TestResolveSubjectViaUserinfo_NoSubInResponse(t *testing.T) {
	v := newUserinfoValidator(t, jsonHandler(http.StatusOK, `{}`))

	_, err := v.ResolveSubjectViaUserinfo(context.Background(), "tok")
	if !errors.Is(err, ErrUserinfoNoSubject) {
		t.Errorf("err = %v, want errors.Is(err, ErrUserinfoNoSubject)", err)
	}
}

func TestResolveSubjectViaUserinfo_ServerError_IsUnavailable(t *testing.T) {
	v := newUserinfoValidator(t, jsonHandler(http.StatusInternalServerError, `oops`))

	_, err := v.ResolveSubjectViaUserinfo(context.Background(), "tok")
	if !errors.Is(err, ErrUserinfoUnavailable) {
		t.Errorf("err = %v, want errors.Is(err, ErrUserinfoUnavailable)", err)
	}
}

func TestResolveSubjectViaUserinfo_NetworkFailure_IsUnavailable(t *testing.T) {
	deadSrv := httptest.NewServer(http.NotFoundHandler())
	deadSrv.Close()

	v := NewValidator(Config{IssuerBaseURL: "https://issuer.example.invalid", UserinfoURL: deadSrv.URL + "/oauth2/userinfo"})

	_, err := v.ResolveSubjectViaUserinfo(context.Background(), "tok")
	if !errors.Is(err, ErrUserinfoUnavailable) {
		t.Errorf("err = %v, want errors.Is(err, ErrUserinfoUnavailable)", err)
	}
}

func TestResolveSubjectViaUserinfo_MalformedJSON_IsUnavailable(t *testing.T) {
	v := newUserinfoValidator(t, jsonHandler(http.StatusOK, `not json`))

	_, err := v.ResolveSubjectViaUserinfo(context.Background(), "tok")
	if !errors.Is(err, ErrUserinfoUnavailable) {
		t.Errorf("err = %v, want errors.Is(err, ErrUserinfoUnavailable)", err)
	}
}

func TestResolveSubjectViaUserinfo_ExplicitURL_IsUsedExactly(t *testing.T) {
	var gotPath string
	userinfoSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sub":"user-uuid-3"}`))
	}))
	t.Cleanup(userinfoSrv.Close)

	// A deliberately non-conventional path -- proves the explicit
	// UserinfoURL is used verbatim, not derived from IssuerBaseURL.
	v := NewValidator(Config{
		IssuerBaseURL: "https://issuer.example.invalid",
		UserinfoURL:   userinfoSrv.URL + "/custom/userinfo/path",
	})

	sub, err := v.ResolveSubjectViaUserinfo(context.Background(), "tok")
	if err != nil {
		t.Fatalf("ResolveSubjectViaUserinfo: %v", err)
	}
	if sub != "user-uuid-3" {
		t.Errorf("sub = %q, want %q", sub, "user-uuid-3")
	}
	if gotPath != "/custom/userinfo/path" {
		t.Errorf("userinfo request path = %q, want %q", gotPath, "/custom/userinfo/path")
	}
}

func TestResolveSubjectViaUserinfo_DerivedURL_WhenUnset(t *testing.T) {
	var gotPath string
	userinfoSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sub":"user-uuid-4"}`))
	}))
	t.Cleanup(userinfoSrv.Close)

	// UserinfoURL deliberately left unset -- must derive
	// {IssuerBaseURL}/oauth2/userinfo.
	v := NewValidator(Config{IssuerBaseURL: userinfoSrv.URL})

	sub, err := v.ResolveSubjectViaUserinfo(context.Background(), "tok")
	if err != nil {
		t.Fatalf("ResolveSubjectViaUserinfo: %v", err)
	}
	if sub != "user-uuid-4" {
		t.Errorf("sub = %q, want %q", sub, "user-uuid-4")
	}
	if gotPath != "/oauth2/userinfo" {
		t.Errorf("userinfo request path = %q, want %q", gotPath, "/oauth2/userinfo")
	}
}

func TestResolveSubjectViaUserinfo_SendsBearerToken(t *testing.T) {
	var gotAuth string
	v := newUserinfoValidator(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sub":"user-uuid-5"}`))
	})

	if _, err := v.ResolveSubjectViaUserinfo(context.Background(), "the-same-bearer-token"); err != nil {
		t.Fatalf("ResolveSubjectViaUserinfo: %v", err)
	}
	if gotAuth != "Bearer the-same-bearer-token" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer the-same-bearer-token")
	}
}
