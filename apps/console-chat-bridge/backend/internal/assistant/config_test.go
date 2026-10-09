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

package assistant

import (
	"strings"
	"testing"
)

func envOf(vars map[string]string) func(string) string {
	return func(key string) string { return vars[key] }
}

var testTenants = map[string]string{"devant": "DEVANT", "identity-console": "IDENTITY_CONSOLE", "acme": "ACME"}

func TestFromEnv_DefaultAndPerTenantChoices(t *testing.T) {
	p, err := FromEnv(envOf(map[string]string{
		"NOVERA_WS_BASE_URL":                         "wss://novera.example",
		"NOVERA_TOKEN_URL":                           "https://idp.example/token",
		"TENANT_DEVANT_ASSISTANT_PROVIDER":           "http",
		"TENANT_DEVANT_ASSISTANT_HTTP_URL":           "https://devant-ai.example/ask",
		"TENANT_DEVANT_ASSISTANT_HTTP_TOKEN":         "secret",
		"TENANT_IDENTITY_CONSOLE_ASSISTANT_PROVIDER": "off",
	}), testTenants)
	if err != nil {
		t.Fatal(err)
	}
	got := p.Describe([]string{"devant", "identity-console", "acme", "unknown"})
	want := map[string]string{"devant": "http", "identity-console": "off", "acme": "novera", "unknown": "novera"}
	for slug, kind := range want {
		if got[slug] != kind {
			t.Errorf("%s: %s, want %s", slug, got[slug], kind)
		}
	}
	if h, ok := p.For("devant").(*HTTP); !ok || h.url != "https://devant-ai.example/ask" || h.token != "secret" {
		t.Errorf("devant provider = %#v", p.For("devant"))
	}
}

func TestFromEnv_NoSettingsMeansOff(t *testing.T) {
	p, err := FromEnv(envOf(nil), testTenants)
	if err != nil || p.For("devant") != nil {
		t.Fatalf("provider = %v, err = %v", p.For("devant"), err)
	}
}

func TestFromEnv_DisabledTenantsWin(t *testing.T) {
	p, err := FromEnv(envOf(map[string]string{
		"ASSISTANT_PROVIDER":         "mock",
		"ASSISTANT_DISABLED_TENANTS": "acme, devant",
	}), testTenants)
	if err != nil {
		t.Fatal(err)
	}
	if p.For("devant") != nil || p.For("acme") != nil || p.For("identity-console") == nil {
		t.Fatalf("got %v", p.Describe([]string{"devant", "acme", "identity-console"}))
	}
}

func TestFromEnv_RejectsIncompleteSettings(t *testing.T) {
	cases := map[string]map[string]string{
		"unknown provider":    {"ASSISTANT_PROVIDER": "gpt"},
		"novera without urls": {"ASSISTANT_PROVIDER": "novera"},
		"http without url":    {"TENANT_ACME_ASSISTANT_PROVIDER": "http"},
	}
	for name, vars := range cases {
		if _, err := FromEnv(envOf(vars), testTenants); err == nil {
			t.Errorf("%s: no error", name)
		} else if name == "http without url" && !strings.Contains(err.Error(), "TENANT_ACME_ASSISTANT_HTTP_URL") {
			t.Errorf("%s: error %q should name the missing setting", name, err)
		}
	}
}
