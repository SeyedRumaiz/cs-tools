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

package servicenow

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/apierror"
)

func TestTableQuery_SendsBasicAuthAndParams(t *testing.T) {
	var capturedPath string
	var capturedQuery url.Values
	var capturedUser, capturedPass string
	var capturedOK bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedQuery = r.URL.Query()
		capturedUser, capturedPass, capturedOK = r.BasicAuth()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":[]}`))
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL, Username: "svc-user", Password: "svc-pass"})

	_, err := c.TableQuery(context.Background(), "customer_account", url.Values{
		"sysparm_query": {"number=123"},
		"sysparm_limit": {"1"},
	})
	if err != nil {
		t.Fatalf("TableQuery returned error: %v", err)
	}
	if capturedPath != "/api/now/table/customer_account" {
		t.Errorf("path = %q, want %q", capturedPath, "/api/now/table/customer_account")
	}
	if capturedQuery.Get("sysparm_query") != "number=123" {
		t.Errorf("sysparm_query = %q, want %q", capturedQuery.Get("sysparm_query"), "number=123")
	}
	if !capturedOK || capturedUser != "svc-user" || capturedPass != "svc-pass" {
		t.Errorf("BasicAuth = (%q, %q, %v), want (svc-user, svc-pass, true)", capturedUser, capturedPass, capturedOK)
	}
}

func TestDo_MapsUpstreamStatusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"not found"}`))
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL, Username: "u", Password: "p"})

	_, err := c.TableQuery(context.Background(), "customer_account", nil)
	var apiErr *apierror.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *apierror.Error, got %T: %v", err, err)
	}
	if apiErr.StatusCode != http.StatusNotFound {
		t.Errorf("StatusCode = %d, want %d", apiErr.StatusCode, http.StatusNotFound)
	}
}

func TestRetryTransport_RetriesOnServerError(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&attempts, 1)
		if n < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":[]}`))
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL, Username: "u", Password: "p"})

	_, err := c.TableQuery(context.Background(), "customer_account", nil)
	if err != nil {
		t.Fatalf("expected the third attempt to succeed, got error: %v", err)
	}
	if got := atomic.LoadInt32(&attempts); got != 3 {
		t.Errorf("attempts = %d, want 3 (1 initial + 2 retries)", got)
	}
}

func TestRetryTransport_GivesUpAfterMaxRetries(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL, Username: "u", Password: "p"})

	_, err := c.TableQuery(context.Background(), "customer_account", nil)
	if err == nil {
		t.Fatal("expected an error after exhausting retries, got nil")
	}
	if got := atomic.LoadInt32(&attempts); got != 3 {
		t.Errorf("attempts = %d, want 3 (1 initial + 2 retries, then give up)", got)
	}
}

func TestGetBinary_AllowsResponseAtTheSizeLimit(t *testing.T) {
	body := make([]byte, maxBinaryResponseBytes)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL, Username: "u", Password: "p"})
	got, _, _, err := c.GetBinary(context.Background(), "/api/now/attachment/x/file", nil)
	if err != nil {
		t.Fatalf("expected a response exactly at the limit to succeed, got error: %v", err)
	}
	if len(got) != maxBinaryResponseBytes {
		t.Errorf("len(got) = %d, want %d", len(got), maxBinaryResponseBytes)
	}
}

func TestGetBinary_RejectsResponseOverTheSizeLimit(t *testing.T) {
	body := make([]byte, maxBinaryResponseBytes+1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL, Username: "u", Password: "p"})
	_, _, _, err := c.GetBinary(context.Background(), "/api/now/attachment/x/file", nil)
	if err == nil {
		t.Fatal("expected an error for a response over the size limit, got nil")
	}
}

func TestSanitizeQueryValue(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{"plain email", "user@example.com", false},
		{"plain phrase", "Acme Corp", false},
		{"caret injects a clause", "user@example.com^OR active=true", true},
		{"control character", "user@example.com\n", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := SanitizeQueryValue(tt.value)
			if (err != nil) != tt.wantErr {
				t.Errorf("SanitizeQueryValue(%q) error = %v, wantErr %v", tt.value, err, tt.wantErr)
			}
		})
	}
}

func TestBuildEncodedQuery(t *testing.T) {
	got := BuildEncodedQuery("u_owner.email=a@b.com", "", "activeNOT LIKEzzz")
	want := "u_owner.email=a@b.com^activeNOT LIKEzzz"
	if got != want {
		t.Errorf("BuildEncodedQuery = %q, want %q", got, want)
	}
}
