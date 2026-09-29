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

package tenant

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func noSecrets(string) string { return "" }

func TestBuildTable_LegacyFallback_DerivesUserinfoURL(t *testing.T) {
	table, err := BuildTable("", "console-chat-bridge", noSecrets, noSecrets, LegacyConfig{
		IssuerBaseURL: "https://localhost:9444",
		ClientID:      "introspection-client",
		ClientSecret:  "secret",
	})
	if err != nil {
		t.Fatalf("BuildTable: %v", err)
	}

	tt, ok := table.Lookup("identity-console")
	if !ok {
		t.Fatal("expected the legacy identity-console tenant to exist")
	}
	if tt.UserinfoURL != "https://localhost:9444/oauth2/userinfo" {
		t.Errorf("UserinfoURL = %q, want %q", tt.UserinfoURL, "https://localhost:9444/oauth2/userinfo")
	}
	if tt.IntrospectionURL != "https://localhost:9444/oauth2/introspect" {
		t.Errorf("IntrospectionURL = %q, want %q", tt.IntrospectionURL, "https://localhost:9444/oauth2/introspect")
	}
}

func TestBuildTable_ExplicitRow_12Fields_LeavesUserinfoURLBlank(t *testing.T) {
	raw := "acme|introspection|https://idp.example.com|https://idp.example.com/introspect|||client-1|false|https://app.example.com||channel-1|project-1"
	table, err := BuildTable(raw, "console-chat-bridge", noSecrets, noSecrets, LegacyConfig{})
	if err != nil {
		t.Fatalf("BuildTable: %v", err)
	}

	tt, ok := table.Lookup("acme")
	if !ok {
		t.Fatal("expected tenant \"acme\" to exist")
	}
	if tt.UserinfoURL != "" {
		t.Errorf("UserinfoURL = %q, want empty for a 12-field row (backward compatible)", tt.UserinfoURL)
	}
}

func TestBuildTable_ExplicitRow_13Fields_SetsUserinfoURL(t *testing.T) {
	raw := "acme|introspection|https://idp.example.com|https://idp.example.com/introspect|||client-1|false|https://app.example.com||channel-1|project-1|https://idp.example.com/custom-userinfo"
	table, err := BuildTable(raw, "console-chat-bridge", noSecrets, noSecrets, LegacyConfig{})
	if err != nil {
		t.Fatalf("BuildTable: %v", err)
	}

	tt, ok := table.Lookup("acme")
	if !ok {
		t.Fatal("expected tenant \"acme\" to exist")
	}
	if tt.UserinfoURL != "https://idp.example.com/custom-userinfo" {
		t.Errorf("UserinfoURL = %q, want %q", tt.UserinfoURL, "https://idp.example.com/custom-userinfo")
	}
}

func TestBuildTable_ExplicitRow_WrongFieldCount_Errors(t *testing.T) {
	raw := "acme|introspection|https://idp.example.com" // way too few fields
	if _, err := BuildTable(raw, "console-chat-bridge", noSecrets, noSecrets, LegacyConfig{}); err == nil {
		t.Fatal("BuildTable: expected an error for a malformed row, got nil")
	}
}

// fakeIdP spins up introspection/userinfo/SCIM/token fake HTTP servers and
// a matching 17-field TENANT_REGISTRY row (or a 13-field one when
// withSCIM is false), so a test can drive a *built* validator's Validate
// end to end and observe -- via which fake server actually got called --
// whether a SCIM resolver was really attached, without reaching into any
// unexported field.
type fakeIdP struct {
	introspectCalled, userinfoCalled, scimCalled bool
	row                                          string
}

func newFakeIdP(t *testing.T, slug string, withSCIM bool) *fakeIdP {
	t.Helper()
	f := &fakeIdP{}

	introspectSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.introspectCalled = true
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"active":true,"username":"alice@tenant.example","client_id":"c1"}`))
	}))
	t.Cleanup(introspectSrv.Close)

	userinfoSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.userinfoCalled = true
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sub":"userinfo-resolved-subject"}`))
	}))
	t.Cleanup(userinfoSrv.Close)

	fields := []string{
		slug, "introspection", "https://issuer.example.com", introspectSrv.URL,
		"", "", "client-1", "false", "", "", "", "project-1", userinfoSrv.URL,
	}

	if withSCIM {
		scimSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			f.scimCalled = true
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"totalResults":1,"Resources":[{"id":"scim-resolved-subject"}]}`))
		}))
		t.Cleanup(scimSrv.Close)

		tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"scim-m2m-token","token_type":"Bearer","expires_in":3600}`))
		}))
		t.Cleanup(tokenSrv.Close)

		fields = append(fields, "scim-client", scimSrv.URL, tokenSrv.URL, "internal_user_mgt_list")
	}

	f.row = strings.Join(fields, "|")
	return f
}

func TestBuildTable_ExplicitRow_WithSCIM_ResolvesViaSCIM(t *testing.T) {
	f := newFakeIdP(t, "acme", true)

	table, err := BuildTable(f.row, "console-chat-bridge", noSecrets, noSecrets, LegacyConfig{})
	if err != nil {
		t.Fatalf("BuildTable: %v", err)
	}
	tt, ok := table.Lookup("acme")
	if !ok {
		t.Fatal("expected tenant \"acme\" to exist")
	}

	id, err := tt.Validator.Validate(context.Background(), "tok")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if id.Subject != "scim-resolved-subject" {
		t.Errorf("Subject = %q, want %q (resolved via SCIM)", id.Subject, "scim-resolved-subject")
	}
	if !f.scimCalled {
		t.Error("expected the SCIM server to be called")
	}
	if f.userinfoCalled {
		t.Error("UserInfo must not be called when this tenant's SCIM resolver is configured")
	}
}

func TestBuildTable_ExplicitRow_WithoutSCIM_ResolvesViaUserinfo(t *testing.T) {
	f := newFakeIdP(t, "acme", false)

	table, err := BuildTable(f.row, "console-chat-bridge", noSecrets, noSecrets, LegacyConfig{})
	if err != nil {
		t.Fatalf("BuildTable: %v", err)
	}
	tt, ok := table.Lookup("acme")
	if !ok {
		t.Fatal("expected tenant \"acme\" to exist")
	}

	id, err := tt.Validator.Validate(context.Background(), "tok")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if id.Subject != "userinfo-resolved-subject" {
		t.Errorf("Subject = %q, want %q (resolved via UserInfo, unaffected by SCIM existing as a capability)", id.Subject, "userinfo-resolved-subject")
	}
	if !f.userinfoCalled {
		t.Error("expected UserInfo to be called for a tenant with no SCIM configured")
	}
	if f.scimCalled {
		t.Error("SCIM must never be called for a tenant that never configured it")
	}
}

func TestBuildTable_LegacyFallback_WithSCIM_ResolvesViaSCIM(t *testing.T) {
	var scimCalled, userinfoCalled bool

	// IssuerBaseURL below points at this fake, so both IntrospectionURL and
	// UserinfoURL (both derived from it -- see buildLegacyTenant) resolve
	// to fakes this test controls, keeping it self-contained.
	introspectSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth2/introspect":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"active":true,"username":"alice@carbon.super","client_id":"c1"}`))
		case "/oauth2/userinfo":
			userinfoCalled = true
			w.WriteHeader(http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(introspectSrv.Close)

	scimSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scimCalled = true
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"totalResults":1,"Resources":[{"id":"legacy-scim-resolved-subject"}]}`))
	}))
	t.Cleanup(scimSrv.Close)

	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"scim-m2m-token","token_type":"Bearer","expires_in":3600}`))
	}))
	t.Cleanup(tokenSrv.Close)

	table, err := BuildTable("", "console-chat-bridge", noSecrets, noSecrets, LegacyConfig{
		IssuerBaseURL:    introspectSrv.URL,
		ClientID:         "introspection-client",
		ClientSecret:     "secret",
		SCIMClientID:     "scim-client",
		SCIMClientSecret: "scim-secret",
		SCIMBaseURL:      scimSrv.URL,
		SCIMTokenURL:     tokenSrv.URL,
		SCIMScopes:       []string{"internal_user_mgt_list"},
	})
	if err != nil {
		t.Fatalf("BuildTable: %v", err)
	}
	tt, ok := table.Lookup("identity-console")
	if !ok {
		t.Fatal("expected the legacy identity-console tenant to exist")
	}

	id, err := tt.Validator.Validate(context.Background(), "tok")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if id.Subject != "legacy-scim-resolved-subject" {
		t.Errorf("Subject = %q, want %q (resolved via SCIM through the refactored buildLegacyTenant)", id.Subject, "legacy-scim-resolved-subject")
	}
	if !scimCalled {
		t.Error("expected the SCIM server to be called")
	}
	if userinfoCalled {
		t.Error("UserInfo must not be called when the legacy tenant's SCIM resolver is configured")
	}
}

func TestBuildTable_IdentityConsoleAndLiveChatDemo_CoexistInOneRegistry(t *testing.T) {
	console := newFakeIdP(t, "identity-console", true)
	demo := newFakeIdP(t, "live-chat-demo", false)

	raw := console.row + ";" + demo.row
	table, err := BuildTable(raw, "console-chat-bridge", noSecrets, noSecrets, LegacyConfig{})
	if err != nil {
		t.Fatalf("BuildTable: %v", err)
	}

	consoleTenant, ok := table.Lookup("identity-console")
	if !ok {
		t.Fatal("expected tenant \"identity-console\" to exist")
	}
	demoTenant, ok := table.Lookup("live-chat-demo")
	if !ok {
		t.Fatal("expected tenant \"live-chat-demo\" to exist")
	}

	consoleID, err := consoleTenant.Validator.Validate(context.Background(), "tok")
	if err != nil {
		t.Fatalf("identity-console Validate: %v", err)
	}
	if consoleID.Subject != "scim-resolved-subject" {
		t.Errorf("identity-console Subject = %q, want %q", consoleID.Subject, "scim-resolved-subject")
	}
	if !console.scimCalled {
		t.Error("expected identity-console's own SCIM server to be called")
	}

	demoID, err := demoTenant.Validator.Validate(context.Background(), "tok")
	if err != nil {
		t.Fatalf("live-chat-demo Validate: %v", err)
	}
	if demoID.Subject != "userinfo-resolved-subject" {
		t.Errorf("live-chat-demo Subject = %q, want %q", demoID.Subject, "userinfo-resolved-subject")
	}
	if !demo.userinfoCalled {
		t.Error("expected live-chat-demo's own UserInfo server to be called")
	}

	// Cross-check: each tenant's own fake servers were the ones called --
	// proves the two tenants' validators are genuinely independent, not
	// accidentally sharing state or a client.
	if demo.scimCalled {
		t.Error("live-chat-demo has no SCIM configured and must never call any SCIM server")
	}
	if console.userinfoCalled {
		t.Error("identity-console has SCIM configured and must never fall back to UserInfo")
	}
}
