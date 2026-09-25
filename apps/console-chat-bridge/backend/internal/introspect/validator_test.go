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
	"sync/atomic"
	"testing"
)

// newTestValidator spins up fake introspection and userinfo endpoints and
// returns a Validator pointed at both, plus a counter of how many times the
// userinfo endpoint was actually called -- several scenarios below assert
// on it being exactly 0 (the whole point of the fallback being conditional).
func newTestValidator(t *testing.T, introspectHandler, userinfoHandler http.HandlerFunc) (*Validator, *int32) {
	t.Helper()

	var userinfoCalls int32

	introspectSrv := httptest.NewServer(introspectHandler)
	t.Cleanup(introspectSrv.Close)

	var userinfoSrv *httptest.Server
	if userinfoHandler != nil {
		userinfoSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&userinfoCalls, 1)
			userinfoHandler(w, r)
		}))
		t.Cleanup(userinfoSrv.Close)
	} else {
		// No handler supplied means the test expects userinfo to never be
		// called at all -- fail loudly if it is.
		userinfoSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&userinfoCalls, 1)
			t.Errorf("userinfo endpoint was called but should not have been: %s", r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}))
		t.Cleanup(userinfoSrv.Close)
	}

	v := NewValidator(Config{
		IssuerBaseURL:    introspectSrv.URL,
		IntrospectionURL: introspectSrv.URL + "/oauth2/introspect",
		UserinfoURL:      userinfoSrv.URL + "/oauth2/userinfo",
	})
	return v, &userinfoCalls
}

func jsonHandler(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

func TestValidateBearer_IntrospectionHasSub_SkipsUserinfo(t *testing.T) {
	v, userinfoCalls := newTestValidator(t,
		jsonHandler(http.StatusOK, `{"active":true,"sub":"user-uuid-1","username":"jane@example.com","client_id":"c1"}`),
		nil, // userinfo must not be called
	)

	id, err := v.ValidateBearer(context.Background(), "tok")
	if err != nil {
		t.Fatalf("ValidateBearer: %v", err)
	}
	if id.Subject != "user-uuid-1" {
		t.Errorf("Subject = %q, want %q", id.Subject, "user-uuid-1")
	}
	if atomic.LoadInt32(userinfoCalls) != 0 {
		t.Errorf("userinfo calls = %d, want 0", atomic.LoadInt32(userinfoCalls))
	}
}

func TestValidateBearer_InactiveToken_SkipsUserinfo(t *testing.T) {
	v, userinfoCalls := newTestValidator(t,
		jsonHandler(http.StatusOK, `{"active":false}`),
		nil, // userinfo must not be called
	)

	_, err := v.ValidateBearer(context.Background(), "tok")
	if err == nil {
		t.Fatal("ValidateBearer: expected an error for an inactive token, got nil")
	}
	if atomic.LoadInt32(userinfoCalls) != 0 {
		t.Errorf("userinfo calls = %d, want 0", atomic.LoadInt32(userinfoCalls))
	}
}

func TestValidateBearer_NoSub_FallsBackToUserinfo(t *testing.T) {
	v, userinfoCalls := newTestValidator(t,
		jsonHandler(http.StatusOK, `{"active":true,"username":"jane@example.com","client_id":"c1"}`),
		jsonHandler(http.StatusOK, `{"sub":"user-uuid-2"}`),
	)

	id, err := v.ValidateBearer(context.Background(), "tok")
	if err != nil {
		t.Fatalf("ValidateBearer: %v", err)
	}
	if id.Subject != "user-uuid-2" {
		t.Errorf("Subject = %q, want %q", id.Subject, "user-uuid-2")
	}
	if id.Username != "jane@example.com" {
		t.Errorf("Username = %q, want %q", id.Username, "jane@example.com")
	}
	if atomic.LoadInt32(userinfoCalls) != 1 {
		t.Errorf("userinfo calls = %d, want 1", atomic.LoadInt32(userinfoCalls))
	}
}

func TestValidateBearer_UserinfoInsufficientScope_FailsSafely(t *testing.T) {
	v, _ := newTestValidator(t,
		jsonHandler(http.StatusOK, `{"active":true,"username":"jane@example.com","client_id":"c1"}`),
		jsonHandler(http.StatusForbidden, `{"error":"insufficient_scope"}`),
	)

	_, err := v.ValidateBearer(context.Background(), "tok")
	if err == nil {
		t.Fatal("ValidateBearer: expected an error, got nil")
	}
	if !errors.Is(err, ErrUserinfoInsufficientScope) {
		t.Errorf("err = %v, want errors.Is(err, ErrUserinfoInsufficientScope)", err)
	}
}

func TestValidateBearer_Userinfo401_FailsSafelyAsInsufficientScope(t *testing.T) {
	v, _ := newTestValidator(t,
		jsonHandler(http.StatusOK, `{"active":true,"username":"jane@example.com","client_id":"c1"}`),
		jsonHandler(http.StatusUnauthorized, `{"error":"invalid_token"}`),
	)

	_, err := v.ValidateBearer(context.Background(), "tok")
	if !errors.Is(err, ErrUserinfoInsufficientScope) {
		t.Errorf("err = %v, want errors.Is(err, ErrUserinfoInsufficientScope)", err)
	}
}

func TestValidateBearer_UserinfoNoSub_FailsSafely(t *testing.T) {
	v, _ := newTestValidator(t,
		jsonHandler(http.StatusOK, `{"active":true,"username":"jane@example.com","client_id":"c1"}`),
		jsonHandler(http.StatusOK, `{}`),
	)

	_, err := v.ValidateBearer(context.Background(), "tok")
	if !errors.Is(err, ErrUserinfoNoSubject) {
		t.Errorf("err = %v, want errors.Is(err, ErrUserinfoNoSubject)", err)
	}
}

func TestValidateBearer_UserinfoServerError_FailsSafelyAsUnavailable(t *testing.T) {
	v, _ := newTestValidator(t,
		jsonHandler(http.StatusOK, `{"active":true,"username":"jane@example.com","client_id":"c1"}`),
		jsonHandler(http.StatusInternalServerError, `oops`),
	)

	_, err := v.ValidateBearer(context.Background(), "tok")
	if !errors.Is(err, ErrUserinfoUnavailable) {
		t.Errorf("err = %v, want errors.Is(err, ErrUserinfoUnavailable)", err)
	}
}

func TestValidateBearer_UserinfoNetworkFailure_FailsSafelyAsUnavailable(t *testing.T) {
	introspectSrv := httptest.NewServer(jsonHandler(http.StatusOK, `{"active":true,"username":"jane@example.com","client_id":"c1"}`))
	t.Cleanup(introspectSrv.Close)

	// A userinfo server that's already closed -- every call fails at the
	// transport level (connection refused), not with any HTTP status.
	deadSrv := httptest.NewServer(http.NotFoundHandler())
	deadSrv.Close()

	v := NewValidator(Config{
		IssuerBaseURL:    introspectSrv.URL,
		IntrospectionURL: introspectSrv.URL + "/oauth2/introspect",
		UserinfoURL:      deadSrv.URL + "/oauth2/userinfo",
	})

	_, err := v.ValidateBearer(context.Background(), "tok")
	if !errors.Is(err, ErrUserinfoUnavailable) {
		t.Errorf("err = %v, want errors.Is(err, ErrUserinfoUnavailable)", err)
	}
}

func TestValidateBearer_MalformedUserinfoJSON_FailsSafely(t *testing.T) {
	v, _ := newTestValidator(t,
		jsonHandler(http.StatusOK, `{"active":true,"username":"jane@example.com","client_id":"c1"}`),
		jsonHandler(http.StatusOK, `not json`),
	)

	_, err := v.ValidateBearer(context.Background(), "tok")
	if !errors.Is(err, ErrUserinfoUnavailable) {
		t.Errorf("err = %v, want errors.Is(err, ErrUserinfoUnavailable)", err)
	}
}

func TestValidateBearer_ExplicitUserinfoURL_IsUsedExactly(t *testing.T) {
	var gotPath string
	introspectSrv := httptest.NewServer(jsonHandler(http.StatusOK, `{"active":true,"username":"jane@example.com","client_id":"c1"}`))
	t.Cleanup(introspectSrv.Close)

	userinfoSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sub":"user-uuid-3"}`))
	}))
	t.Cleanup(userinfoSrv.Close)

	// A deliberately non-conventional path -- proves the explicit
	// UserinfoURL is used verbatim, not derived from IssuerBaseURL.
	v := NewValidator(Config{
		IssuerBaseURL:    introspectSrv.URL,
		IntrospectionURL: introspectSrv.URL + "/oauth2/introspect",
		UserinfoURL:      userinfoSrv.URL + "/custom/userinfo/path",
	})

	id, err := v.ValidateBearer(context.Background(), "tok")
	if err != nil {
		t.Fatalf("ValidateBearer: %v", err)
	}
	if id.Subject != "user-uuid-3" {
		t.Errorf("Subject = %q, want %q", id.Subject, "user-uuid-3")
	}
	if gotPath != "/custom/userinfo/path" {
		t.Errorf("userinfo request path = %q, want %q", gotPath, "/custom/userinfo/path")
	}
}

func TestValidateBearer_DerivedUserinfoURL_WhenUnset(t *testing.T) {
	var gotPath string
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth2/introspect", jsonHandler(http.StatusOK, `{"active":true,"username":"jane@example.com","client_id":"c1"}`))
	mux.HandleFunc("/oauth2/userinfo", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sub":"user-uuid-4"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	// IntrospectionURL/UserinfoURL both deliberately left unset -- both
	// must derive from IssuerBaseURL (…/oauth2/introspect and …/oauth2/
	// userinfo respectively).
	v := NewValidator(Config{IssuerBaseURL: srv.URL})

	id, err := v.ValidateBearer(context.Background(), "tok")
	if err != nil {
		t.Fatalf("ValidateBearer: %v", err)
	}
	if id.Subject != "user-uuid-4" {
		t.Errorf("Subject = %q, want %q", id.Subject, "user-uuid-4")
	}
	if gotPath != "/oauth2/userinfo" {
		t.Errorf("userinfo request path = %q, want %q", gotPath, "/oauth2/userinfo")
	}
}

func TestValidateBearer_ClientCredentialsToken_NeverCallsUserinfo(t *testing.T) {
	// No Username, no Sub, only ClientID -- a pure M2M token. Must not
	// attempt the userinfo fallback at all: it has no end-user identity to
	// resolve, and calling userinfo with it would just fail pointlessly.
	v, userinfoCalls := newTestValidator(t,
		jsonHandler(http.StatusOK, `{"active":true,"client_id":"m2m-client"}`),
		nil,
	)

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
	if atomic.LoadInt32(userinfoCalls) != 0 {
		t.Errorf("userinfo calls = %d, want 0", atomic.LoadInt32(userinfoCalls))
	}
}
