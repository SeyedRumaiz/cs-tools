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

package tokenvalidator

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wso2-open-operations/cs-tools/apps/console-chat-bridge/backend/internal/introspect"
)

func jsonHandler(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

func failIfCalledHandler(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("userinfo endpoint was called but should not have been: %s", r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}
}

func newIntrospectionValidator(t *testing.T, introspectHandler, userinfoHandler http.HandlerFunc) *IntrospectionValidator {
	t.Helper()

	introspectSrv := httptest.NewServer(introspectHandler)
	t.Cleanup(introspectSrv.Close)

	userinfoSrv := httptest.NewServer(userinfoHandler)
	t.Cleanup(userinfoSrv.Close)

	v := introspect.NewValidator(introspect.Config{
		IssuerBaseURL: introspectSrv.URL,
		UserinfoURL:   userinfoSrv.URL,
	})
	return NewIntrospectionValidator(v)
}

// This is the /v1-specific counterpart to introspect's own
// TestValidateBearer_NoSub_SucceedsWithEmptySubject_EvenWhenUserinfoIsDown:
// the generic API requires a canonical Subject, so the SAME situation that
// succeeds (with an empty Subject) for the legacy path must FAIL here.
func TestValidate_NoSub_UserinfoUnavailable_Fails(t *testing.T) {
	iv := newIntrospectionValidator(t,
		jsonHandler(http.StatusOK, `{"active":true,"username":"jane@example.com","client_id":"c1"}`),
		jsonHandler(http.StatusInternalServerError, `oops`),
	)

	_, err := iv.Validate(context.Background(), "tok")
	if err == nil {
		t.Fatal("Validate: expected an error when Subject cannot be obtained, got nil")
	}
	if !errors.Is(err, introspect.ErrUserinfoUnavailable) {
		t.Errorf("err = %v, want errors.Is(err, introspect.ErrUserinfoUnavailable)", err)
	}
}

func TestValidate_NoSub_UserinfoInsufficientScope_Fails(t *testing.T) {
	iv := newIntrospectionValidator(t,
		jsonHandler(http.StatusOK, `{"active":true,"username":"jane@example.com","client_id":"c1"}`),
		jsonHandler(http.StatusForbidden, `{"error":"insufficient_scope"}`),
	)

	_, err := iv.Validate(context.Background(), "tok")
	if !errors.Is(err, introspect.ErrUserinfoInsufficientScope) {
		t.Errorf("err = %v, want errors.Is(err, introspect.ErrUserinfoInsufficientScope)", err)
	}
}

func TestValidate_NoSub_UserinfoNoSubject_Fails(t *testing.T) {
	iv := newIntrospectionValidator(t,
		jsonHandler(http.StatusOK, `{"active":true,"username":"jane@example.com","client_id":"c1"}`),
		jsonHandler(http.StatusOK, `{}`),
	)

	_, err := iv.Validate(context.Background(), "tok")
	if !errors.Is(err, introspect.ErrUserinfoNoSubject) {
		t.Errorf("err = %v, want errors.Is(err, introspect.ErrUserinfoNoSubject)", err)
	}
}

func TestValidate_NoSub_UserinfoSucceeds_PopulatesSubject(t *testing.T) {
	iv := newIntrospectionValidator(t,
		jsonHandler(http.StatusOK, `{"active":true,"username":"jane@example.com","client_id":"c1"}`),
		jsonHandler(http.StatusOK, `{"sub":"user-uuid-1"}`),
	)

	id, err := iv.Validate(context.Background(), "tok")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if id.Subject != "user-uuid-1" {
		t.Errorf("Subject = %q, want %q", id.Subject, "user-uuid-1")
	}
	if id.Username != "jane@example.com" {
		t.Errorf("Username = %q, want %q", id.Username, "jane@example.com")
	}
}

// Keeps the JWT/already-has-sub fast path: when introspection's own
// response already carries "sub" (e.g. a JWT-typed access token, confirmed
// via live testing to behave this way against this bridge's own pinned
// local WSO2 IS instance), Validate must never call userinfo at all.
func TestValidate_IntrospectionHasSub_SkipsUserinfo(t *testing.T) {
	iv := newIntrospectionValidator(t,
		jsonHandler(http.StatusOK, `{"active":true,"sub":"user-uuid-2","username":"jane@example.com","client_id":"c1"}`),
		failIfCalledHandler(t),
	)

	id, err := iv.Validate(context.Background(), "tok")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if id.Subject != "user-uuid-2" {
		t.Errorf("Subject = %q, want %q", id.Subject, "user-uuid-2")
	}
}

func TestValidate_InactiveToken_SkipsUserinfo(t *testing.T) {
	iv := newIntrospectionValidator(t,
		jsonHandler(http.StatusOK, `{"active":false}`),
		failIfCalledHandler(t),
	)

	if _, err := iv.Validate(context.Background(), "tok"); err == nil {
		t.Fatal("Validate: expected an error for an inactive token, got nil")
	}
}

// A pure client-credentials token has no Username and no end-user Subject
// to resolve -- Validate must not attempt userinfo for it, and must not
// fail just because Subject is empty (RequireClientID-gated routes never
// need one).
func TestValidate_ClientCredentialsToken_SkipsUserinfo_NoError(t *testing.T) {
	iv := newIntrospectionValidator(t,
		jsonHandler(http.StatusOK, `{"active":true,"client_id":"m2m-client"}`),
		failIfCalledHandler(t),
	)

	id, err := iv.Validate(context.Background(), "tok")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if id.Subject != "" {
		t.Errorf("Subject = %q, want empty", id.Subject)
	}
	if id.ClientID != "m2m-client" {
		t.Errorf("ClientID = %q, want %q", id.ClientID, "m2m-client")
	}
}
