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
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/wso2-open-operations/cs-tools/apps/console-chat-bridge/backend/internal/introspect"
	"github.com/wso2-open-operations/cs-tools/apps/console-chat-bridge/backend/internal/scim"
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
// fakeSCIM is a scimResolver test double -- a plain func so each test can
// assert exactly what it needs (a fixed result, a call counter, a sentinel
// error) without a real SCIM2 server.
type fakeSCIM struct {
	fn      func(ctx context.Context, username string) (string, error)
	calls   int
	lastArg string
}

func (f *fakeSCIM) ResolveUserID(ctx context.Context, username string) (string, error) {
	f.calls++
	f.lastArg = username
	return f.fn(ctx, username)
}

func TestValidate_IntrospectionHasSub_SkipsSCIM(t *testing.T) {
	iv := newIntrospectionValidator(t,
		jsonHandler(http.StatusOK, `{"active":true,"sub":"user-uuid-jwt","username":"jane@example.com","client_id":"c1"}`),
		failIfCalledHandler(t),
	)
	scim := &fakeSCIM{fn: func(context.Context, string) (string, error) {
		t.Fatal("SCIM resolver was called but introspection already had sub")
		return "", nil
	}}
	iv.WithSCIMResolver(scim)

	id, err := iv.Validate(context.Background(), "tok")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if id.Subject != "user-uuid-jwt" {
		t.Errorf("Subject = %q, want %q", id.Subject, "user-uuid-jwt")
	}
	if scim.calls != 0 {
		t.Errorf("SCIM was called %d times, want 0", scim.calls)
	}
}

func TestValidate_NoSub_SCIMConfigured_ResolvesSubject(t *testing.T) {
	iv := newIntrospectionValidator(t,
		jsonHandler(http.StatusOK, `{"active":true,"username":"admin@carbon.super","client_id":"c1"}`),
		failIfCalledHandler(t), // userinfo must never be called once SCIM is configured
	)
	scim := &fakeSCIM{fn: func(context.Context, string) (string, error) {
		return "scim-resolved-uuid", nil
	}}
	iv.WithSCIMResolver(scim)

	id, err := iv.Validate(context.Background(), "tok")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if id.Subject != "scim-resolved-uuid" {
		t.Errorf("Subject = %q, want %q", id.Subject, "scim-resolved-uuid")
	}
	if scim.calls != 1 {
		t.Fatalf("SCIM was called %d times, want 1", scim.calls)
	}
	if scim.lastArg != "admin@carbon.super" {
		t.Errorf("SCIM was called with username %q, want %q", scim.lastArg, "admin@carbon.super")
	}
}

func TestValidate_SCIM_NoMatch_FailsClosed(t *testing.T) {
	iv := newIntrospectionValidator(t,
		jsonHandler(http.StatusOK, `{"active":true,"username":"nobody@carbon.super","client_id":"c1"}`),
		failIfCalledHandler(t),
	)
	iv.WithSCIMResolver(&fakeSCIM{fn: func(context.Context, string) (string, error) {
		return "", scim.ErrNoMatch
	}})

	_, err := iv.Validate(context.Background(), "tok")
	if !errors.Is(err, scim.ErrNoMatch) {
		t.Errorf("err = %v, want errors.Is(err, scim.ErrNoMatch)", err)
	}
}

func TestValidate_SCIM_Ambiguous_FailsClosed(t *testing.T) {
	iv := newIntrospectionValidator(t,
		jsonHandler(http.StatusOK, `{"active":true,"username":"dup@carbon.super","client_id":"c1"}`),
		failIfCalledHandler(t),
	)
	iv.WithSCIMResolver(&fakeSCIM{fn: func(context.Context, string) (string, error) {
		return "", scim.ErrAmbiguous
	}})

	_, err := iv.Validate(context.Background(), "tok")
	if !errors.Is(err, scim.ErrAmbiguous) {
		t.Errorf("err = %v, want errors.Is(err, scim.ErrAmbiguous)", err)
	}
}

func TestValidate_SCIM_Unavailable_FailsSafely(t *testing.T) {
	iv := newIntrospectionValidator(t,
		jsonHandler(http.StatusOK, `{"active":true,"username":"admin@carbon.super","client_id":"c1"}`),
		failIfCalledHandler(t),
	)
	iv.WithSCIMResolver(&fakeSCIM{fn: func(context.Context, string) (string, error) {
		return "", fmt.Errorf("%w: network timeout", scim.ErrUnavailable)
	}})

	id, err := iv.Validate(context.Background(), "tok")
	if !errors.Is(err, scim.ErrUnavailable) {
		t.Errorf("err = %v, want errors.Is(err, scim.ErrUnavailable)", err)
	}
	if id != nil {
		t.Errorf("id = %+v, want nil -- a SCIM failure must not return a partially-populated Identity", id)
	}
}

func TestValidate_ClientCredentialsToken_SCIMConfigured_SkipsSCIM(t *testing.T) {
	iv := newIntrospectionValidator(t,
		jsonHandler(http.StatusOK, `{"active":true,"client_id":"m2m-client"}`),
		failIfCalledHandler(t),
	)
	scim := &fakeSCIM{fn: func(context.Context, string) (string, error) {
		t.Fatal("SCIM resolver was called for a client-credentials token with no Username")
		return "", nil
	}}
	iv.WithSCIMResolver(scim)

	id, err := iv.Validate(context.Background(), "tok")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if id.Subject != "" {
		t.Errorf("Subject = %q, want empty", id.Subject)
	}
	if scim.calls != 0 {
		t.Errorf("SCIM was called %d times, want 0", scim.calls)
	}
}

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

// --- JWKSValidator ---

const testKID = "test-key-1"

// jwksServer serves a single RSA public key as a JWKS document at its root.
func jwksServer(t *testing.T, kid string, pub *rsa.PublicKey) *httptest.Server {
	t.Helper()
	jwk := map[string]string{
		"kty": "RSA",
		"kid": kid,
		"use": "sig",
		"alg": "RS256",
		"n":   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
	}
	body, err := json.Marshal(map[string]any{"keys": []any{jwk}})
	if err != nil {
		t.Fatalf("marshal JWKS: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// signToken builds and signs a JWT with claims, using key and kid. A test
// mutates claims (or the token's own header, via headerMutators) before
// signing to exercise one specific failure mode.
func signToken(t *testing.T, key *rsa.PrivateKey, kid string, claims jwksClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = kid
	signed, err := tok.SignedString(key)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return signed
}

func validClaims() jwksClaims {
	return jwksClaims{
		Username: "jane@example.com",
		ClientID: "c1",
		Scope:    "openid profile",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "user-uuid-1",
			Issuer:    "https://idp.test",
			Audience:  jwt.ClaimStrings{"my-audience"},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
}

func TestJWKSValidator_ValidToken_Succeeds(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	srv := jwksServer(t, testKID, &key.PublicKey)

	jv, err := NewJWKSValidator(context.Background(), JWKSValidatorConfig{
		JWKSURI:  srv.URL,
		Issuer:   "https://idp.test",
		Audience: "my-audience",
	})
	if err != nil {
		t.Fatalf("NewJWKSValidator: %v", err)
	}

	token := signToken(t, key, testKID, validClaims())
	id, err := jv.Validate(context.Background(), token)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if id.Subject != "user-uuid-1" {
		t.Errorf("Subject = %q, want %q", id.Subject, "user-uuid-1")
	}
	if id.Username != "jane@example.com" {
		t.Errorf("Username = %q, want %q", id.Username, "jane@example.com")
	}
	if id.ClientID != "c1" {
		t.Errorf("ClientID = %q, want %q", id.ClientID, "c1")
	}
	if id.Issuer != "https://idp.test" {
		t.Errorf("Issuer = %q, want %q", id.Issuer, "https://idp.test")
	}
	wantScopes := []string{"openid", "profile"}
	if len(id.Scopes) != len(wantScopes) || id.Scopes[0] != wantScopes[0] || id.Scopes[1] != wantScopes[1] {
		t.Errorf("Scopes = %v, want %v", id.Scopes, wantScopes)
	}
}

func TestJWKSValidator_ExpiredToken_Fails(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	srv := jwksServer(t, testKID, &key.PublicKey)
	jv, err := NewJWKSValidator(context.Background(), JWKSValidatorConfig{JWKSURI: srv.URL})
	if err != nil {
		t.Fatalf("NewJWKSValidator: %v", err)
	}

	claims := validClaims()
	claims.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-time.Hour))
	token := signToken(t, key, testKID, claims)

	if _, err := jv.Validate(context.Background(), token); err == nil {
		t.Fatal("Validate: expected an error for an expired token, got nil")
	}
}

func TestJWKSValidator_WrongIssuer_Fails(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	srv := jwksServer(t, testKID, &key.PublicKey)
	jv, err := NewJWKSValidator(context.Background(), JWKSValidatorConfig{
		JWKSURI: srv.URL,
		Issuer:  "https://expected.test",
	})
	if err != nil {
		t.Fatalf("NewJWKSValidator: %v", err)
	}

	claims := validClaims()
	claims.Issuer = "https://someone-else.test"
	token := signToken(t, key, testKID, claims)

	if _, err := jv.Validate(context.Background(), token); err == nil {
		t.Fatal("Validate: expected an error for a mismatched issuer, got nil")
	}
}

func TestJWKSValidator_WrongAudience_Fails(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	srv := jwksServer(t, testKID, &key.PublicKey)
	jv, err := NewJWKSValidator(context.Background(), JWKSValidatorConfig{
		JWKSURI:  srv.URL,
		Audience: "expected-audience",
	})
	if err != nil {
		t.Fatalf("NewJWKSValidator: %v", err)
	}

	claims := validClaims()
	claims.Audience = jwt.ClaimStrings{"someone-elses-audience"}
	token := signToken(t, key, testKID, claims)

	if _, err := jv.Validate(context.Background(), token); err == nil {
		t.Fatal("Validate: expected an error for a mismatched audience, got nil")
	}
}

// A token signed by a key never published in the tenant's own JWKS must be
// rejected -- the whole point of JWKS verification.
func TestJWKSValidator_WrongSigningKey_Fails(t *testing.T) {
	published, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	attacker, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	srv := jwksServer(t, testKID, &published.PublicKey)
	jv, err := NewJWKSValidator(context.Background(), JWKSValidatorConfig{JWKSURI: srv.URL})
	if err != nil {
		t.Fatalf("NewJWKSValidator: %v", err)
	}

	token := signToken(t, attacker, testKID, validClaims())
	if _, err := jv.Validate(context.Background(), token); err == nil {
		t.Fatal("Validate: expected an error for a token signed by an unpublished key, got nil")
	}
}

func TestJWKSValidator_NoUsableIdentity_Fails(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	srv := jwksServer(t, testKID, &key.PublicKey)
	jv, err := NewJWKSValidator(context.Background(), JWKSValidatorConfig{JWKSURI: srv.URL})
	if err != nil {
		t.Fatalf("NewJWKSValidator: %v", err)
	}

	claims := validClaims()
	claims.Subject = ""
	claims.Username = ""
	claims.ClientID = ""
	token := signToken(t, key, testKID, claims)

	if _, err := jv.Validate(context.Background(), token); err == nil {
		t.Fatal("Validate: expected an error for a token with no sub/username/client_id, got nil")
	}
}

func TestJWKSValidator_NoIssuerOrAudienceConfigured_SkipsThoseChecks(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	srv := jwksServer(t, testKID, &key.PublicKey)
	// Issuer/Audience both left blank -- any value in the token is accepted.
	jv, err := NewJWKSValidator(context.Background(), JWKSValidatorConfig{JWKSURI: srv.URL})
	if err != nil {
		t.Fatalf("NewJWKSValidator: %v", err)
	}

	token := signToken(t, key, testKID, validClaims())
	if _, err := jv.Validate(context.Background(), token); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestNewJWKSValidator_EmptyJWKSURI_Fails(t *testing.T) {
	if _, err := NewJWKSValidator(context.Background(), JWKSValidatorConfig{}); err == nil {
		t.Fatal("NewJWKSValidator: expected an error for an empty JWKSURI, got nil")
	}
}

// keyfunc tolerates an unreachable JWKS endpoint at construction time (it
// logs and keeps retrying in the background) rather than failing fast --
// see NewJWKSValidator's own doc comment. The observable effect is that
// every token is rejected as unverifiable until a fetch eventually
// succeeds, not a construction-time error.
func TestNewJWKSValidator_UnreachableJWKSURI_ConstructsButRejectsEveryToken(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	srv.Close() // closed before use -- guaranteed unreachable, no network flakiness

	jv, err := NewJWKSValidator(context.Background(), JWKSValidatorConfig{JWKSURI: srv.URL})
	if err != nil {
		t.Fatalf("NewJWKSValidator: %v", err)
	}

	token := signToken(t, key, testKID, validClaims())
	if _, err := jv.Validate(context.Background(), token); err == nil {
		t.Fatal("Validate: expected an error when the JWKS was never successfully fetched, got nil")
	}
}
