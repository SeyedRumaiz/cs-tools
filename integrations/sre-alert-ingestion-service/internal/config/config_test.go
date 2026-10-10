// Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com).
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

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoad_MissingFileUsesDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "absent.toml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg != Defaults() {
		t.Errorf("cfg = %+v, want defaults", cfg)
	}
}

func TestLoad_ExampleMatchesDefaults(t *testing.T) {
	cfg, err := Load("../../config.toml.example")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg != Defaults() {
		t.Errorf("config.toml.example = %+v, want it to match Defaults() %+v", cfg, Defaults())
	}
}

func TestLoad_PartialFileOverridesFieldByField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[allocator]\nmax_batch = 50\n[store]\nquery_timeout = \"3s\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Allocator.MaxBatch != 50 {
		t.Errorf("MaxBatch = %d, want 50", cfg.Allocator.MaxBatch)
	}
	if cfg.Store.QueryTimeout.Duration() != 3*time.Second {
		t.Errorf("QueryTimeout = %v, want 3s", cfg.Store.QueryTimeout.Duration())
	}
	if cfg.Allocator.QueueSize != Defaults().Allocator.QueueSize {
		t.Errorf("QueueSize = %d, want untouched default", cfg.Allocator.QueueSize)
	}
}

func TestLoad_RejectsInvalidValues(t *testing.T) {
	cases := map[string]string{
		"zero batch":           "[allocator]\nmax_batch = 0\n",
		"negative timeout":     "[store]\nquery_timeout = \"-1s\"\n",
		"bad duration":         "[wake]\ntimeout = \"soon\"\n",
		"zero idle":            "[server]\nidle_timeout = \"0s\"\n",
		"zero payload drain":   "[server]\npayload_drain = \"0s\"\n",
		"payload over grace":   "[server]\npayload_drain = \"6s\"\n",
		"zero drain delay":     "[server]\ndrain_delay = \"0s\"\n",
		"budget over grace":    "[server]\nshutdown_grace = \"10s\"\n",
		"steps over grace":     "[server]\nrequest_wait = \"20s\"\n",
		"wait near write":      "[server]\nwrite_timeout = \"10500ms\"\n",
		"deadline at gap":      "[store]\nwrite_deadline = \"10m\"\n",
		"zero deadline":        "[store]\nwrite_deadline = \"0s\"\n",
		"queue bytes small":    "[allocator]\nqueue_max_bytes = 1048576\n",
		"negative min conns":   "[postgres]\nmin_conns = -1\n",
		"zero auth timeout":    "[postgres]\nauth_timeout = \"0s\"\n",
		"negative payload log": "[log]\npayload_max_bytes = -1\n",
		"zero flush interval":  "[payloads]\nflush_interval = \"0s\"\n",
		"zero flush timeout":   "[payloads]\nflush_timeout = \"0s\"\n",
		"payload buffer small": "[payloads]\nmax_buffer_bytes = 1048576\n",
		"zero auth refresh":    "[postgres]\nauth_refresh_interval = \"0s\"\n",
		"stale under refresh":  "[postgres]\nauth_max_stale = \"30s\"\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil {
				t.Error("Load should fail")
			}
		})
	}
}

func TestLoadEnv(t *testing.T) {
	t.Setenv("PORT", "")
	t.Setenv("ALERT_CORE_WAKE_URL", " http://core/alertz ")
	e, err := LoadEnv()
	if err != nil {
		t.Fatalf("LoadEnv: %v", err)
	}
	if e.Port != "8080" {
		t.Errorf("Port = %q, want 8080 default", e.Port)
	}
	if e.WakeURL != "http://core/alertz" {
		t.Errorf("WakeURL = %q", e.WakeURL)
	}
}

func TestLoadEnv_AuthSwitches(t *testing.T) {
	for _, tc := range []struct {
		enabled, auditOnly     string
		wantEnabled, wantAudit bool
	}{
		{"", "", false, false}, // unset: auth off
		{"true", "", true, false},
		{"true", "true", true, true},
		{"false", "true", false, true}, // main ignores audit-only when auth is off
		{" TRUE ", "", true, false},    // a console paste can carry case and spaces
		{"1", "0", true, false},
	} {
		t.Setenv("AUTH_ENABLED", tc.enabled)
		t.Setenv("AUTH_AUDIT_ONLY", tc.auditOnly)
		e, err := LoadEnv()
		if err != nil {
			t.Fatalf("AUTH_ENABLED=%q AUTH_AUDIT_ONLY=%q: %v", tc.enabled, tc.auditOnly, err)
		}
		if e.AuthEnabled != tc.wantEnabled || e.AuthAuditOnly != tc.wantAudit {
			t.Errorf("AUTH_ENABLED=%q AUTH_AUDIT_ONLY=%q: got (%v, %v), want (%v, %v)",
				tc.enabled, tc.auditOnly, e.AuthEnabled, e.AuthAuditOnly, tc.wantEnabled, tc.wantAudit)
		}
	}
}

// A typo in a security switch must fail at startup, not silently leave auth off.
func TestLoadEnv_RejectsAmbiguousAuthSwitch(t *testing.T) {
	for _, v := range []string{"ture", "enabled", "2"} {
		t.Setenv("AUTH_ENABLED", v)
		if _, err := LoadEnv(); err == nil || !strings.Contains(err.Error(), "AUTH_ENABLED") {
			t.Errorf("AUTH_ENABLED=%q: err = %v, want an error naming AUTH_ENABLED", v, err)
		}
	}
}

// A config.toml still carrying [auth] is flagged, so it can't silently switch auth off.
func TestLoad_FlagsLegacyAuthSection(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "legacy.toml")
	os.WriteFile(legacy, []byte("[auth]\nmode = \"integration_users\"\n"), 0o644)
	clean := filepath.Join(dir, "clean.toml")
	os.WriteFile(clean, []byte("[server]\nmax_body_bytes = 2097152\n"), 0o644)

	if cfg, err := Load(legacy); err != nil || !cfg.LegacyAuthSection {
		t.Errorf("legacy [auth]: LegacyAuthSection = %v, err = %v; want true", cfg.LegacyAuthSection, err)
	}
	if cfg, err := Load(clean); err != nil || cfg.LegacyAuthSection {
		t.Errorf("no [auth]: LegacyAuthSection = %v, err = %v; want false", cfg.LegacyAuthSection, err)
	}
}
