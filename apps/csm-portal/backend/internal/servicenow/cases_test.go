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
	"errors"
	"net/http"
	"testing"
)

func TestTrimCodeBlock(t *testing.T) {
	got := trimCodeBlock("[code]hello <b>world</b>[/code]")
	if got != "hello <b>world</b>" {
		t.Errorf("trimCodeBlock = %q, want %q", got, "hello <b>world</b>")
	}
}

func TestGetCaseByNumber_MapsResponseAndPriority(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"result": []map[string]any{
				{
					"number": "CS0001", "u_wso2_case_id": "WSO2-1", "short_description": "Something broke",
					"priority": "10", "state": "1", "description": "[code]details[/code]",
					"account.u_ai_gen_response": "true",
				},
			},
		})
	})

	result, err := c.GetCaseByNumber(context.Background(), "CS0001")
	if err != nil {
		t.Fatalf("GetCaseByNumber returned error: %v", err)
	}
	if result.Priority != "Critical (P1)" || result.State != "Open" {
		t.Errorf("Priority/State = %q/%q, want Critical (P1)/Open", result.Priority, result.State)
	}
	if result.Description != "details" {
		t.Errorf("Description = %q, want %q (code markers stripped)", result.Description, "details")
	}
	if !result.IsGenAiUsageAllowed {
		t.Error("IsGenAiUsageAllowed = false, want true")
	}
}

func TestGetCaseByNumber_NotFound(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":[]}`))
	})

	_, err := c.GetCaseByNumber(context.Background(), "CS9999")
	if !errors.Is(err, ErrCaseNotFound) {
		t.Fatalf("expected ErrCaseNotFound, got %v", err)
	}
}

func TestGetCases_ReadsTotalCountHeader(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Total-Count", "42")
		_, _ = w.Write([]byte(`{"result":[]}`))
	})

	result, err := c.GetCases(context.Background(), nil, nil, 0, 10)
	if err != nil {
		t.Fatalf("GetCases returned error: %v", err)
	}
	if result.Count != 42 {
		t.Errorf("Count = %d, want 42", result.Count)
	}
}

func TestGetCommentsAndWorknotes_UsesCaseSysIDAndTotalCount(t *testing.T) {
	var sawCountQuery, sawListQuery bool
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/now/table/sn_customerservice_case":
			_, _ = w.Write([]byte(`{"result":[{"sys_id":"case-sys-1"}]}`))
		case "/api/now/table/sys_journal_field":
			if r.URL.Query().Get("sysparm_count") == "true" {
				sawCountQuery = true
				w.Header().Set("X-Total-Count", "5")
				_, _ = w.Write([]byte(`{"result":[]}`))
				return
			}
			sawListQuery = true
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{
					{"sys_created_on": "2026-01-01", "value": "[code]note[/code]", "sys_created_by": "agent", "element": "work_notes"},
				},
			})
		default:
			t.Fatalf("unexpected request path: %s", r.URL.Path)
		}
	})

	result, err := c.GetCommentsAndWorknotes(context.Background(), "CS0001", 0, 10)
	if err != nil {
		t.Fatalf("GetCommentsAndWorknotes returned error: %v", err)
	}
	if !sawCountQuery || !sawListQuery {
		t.Errorf("expected both a count query and a list query, got count=%v list=%v", sawCountQuery, sawListQuery)
	}
	if result.Total != 5 {
		t.Errorf("Total = %d, want 5", result.Total)
	}
	if len(result.Comments) != 1 || result.Comments[0].Value != "note" {
		t.Errorf("unexpected comments: %+v", result.Comments)
	}
}

func TestGetAttachmentsInfo_CaseNotFound(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":[]}`))
	})

	_, err := c.GetAttachmentsInfo(context.Background(), "CS9999", 0, 10)
	if !errors.Is(err, ErrCaseNotFound) {
		t.Fatalf("expected ErrCaseNotFound, got %v", err)
	}
}
