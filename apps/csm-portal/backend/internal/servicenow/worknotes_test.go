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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPostWorkNote_SendsExpectedPatch(t *testing.T) {
	var capturedPatchPath string
	var capturedBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte(`{"result":[{"sys_id":"sid1","state":"1"}]}`))
		case http.MethodPatch:
			capturedPatchPath = r.URL.Path
			_ = json.NewDecoder(r.Body).Decode(&capturedBody)
			_, _ = w.Write([]byte(`{"result":{"number":"CS001","sys_updated_on":"2024-01-01 10:00:00"}}`))
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL, Username: "u", Password: "p"})

	result, err := c.PostWorkNote(context.Background(), "CS001", "hello", "agent@example.com")
	if err != nil {
		t.Fatalf("PostWorkNote returned error: %v", err)
	}
	if result.Number != "CS001" || result.UpdatedOn != "2024-01-01 10:00:00" {
		t.Errorf("result = %+v", result)
	}
	if capturedPatchPath != "/api/now/table/sn_customerservice_case/sid1" {
		t.Errorf("PATCH path = %q, want /api/now/table/sn_customerservice_case/sid1", capturedPatchPath)
	}
	if !strings.Contains(capturedBody["work_notes"], "hello") || !strings.Contains(capturedBody["work_notes"], "agent@example.com") {
		t.Errorf("work_notes body = %q, want it to contain the note text and submitter email", capturedBody["work_notes"])
	}
}

func TestPostWorkNote_RejectsClosedCase(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":[{"sys_id":"sid1","state":"3"}]}`))
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL, Username: "u", Password: "p"})

	_, err := c.PostWorkNote(context.Background(), "CS001", "hello", "agent@example.com")
	if err != ErrCaseClosed {
		t.Fatalf("err = %v, want ErrCaseClosed", err)
	}
}

func TestPostWorkNote_CaseNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":[]}`))
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL, Username: "u", Password: "p"})

	_, err := c.PostWorkNote(context.Background(), "does-not-exist", "hello", "agent@example.com")
	if err != ErrCaseSysIDNotFound {
		t.Fatalf("err = %v, want ErrCaseSysIDNotFound", err)
	}
}
