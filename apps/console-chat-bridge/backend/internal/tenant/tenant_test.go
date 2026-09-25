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

import "testing"

func noSecrets(string) string { return "" }

func TestBuildTable_LegacyFallback_DerivesUserinfoURL(t *testing.T) {
	table, err := BuildTable("", "console-chat-bridge", noSecrets, LegacyConfig{
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
	table, err := BuildTable(raw, "console-chat-bridge", noSecrets, LegacyConfig{})
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
	table, err := BuildTable(raw, "console-chat-bridge", noSecrets, LegacyConfig{})
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
	if _, err := BuildTable(raw, "console-chat-bridge", noSecrets, LegacyConfig{}); err == nil {
		t.Fatal("BuildTable: expected an error for a malformed row, got nil")
	}
}
