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
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestGetAllProjects_BuildsExpectedQueryAndUnwrapsResult(t *testing.T) {
	var capturedQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":[{"sys_id":"1","short_description":"Proj A","number":"PRJ0001"}]}`))
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL, Username: "u", Password: "p"})

	result, err := c.GetAllProjects(context.Background(), "Acme")
	if err != nil {
		t.Fatalf("GetAllProjects returned error: %v", err)
	}
	wantQuery := "short_descriptionLIKEAcme^ORnumberLIKEAcme"
	if got := capturedQuery.Get("sysparm_query"); got != wantQuery {
		t.Errorf("sysparm_query = %q, want %q", got, wantQuery)
	}
	if capturedQuery.Get("sysparm_limit") != "30" {
		t.Errorf("sysparm_limit = %q, want 30", capturedQuery.Get("sysparm_limit"))
	}

	var items []struct {
		SysID string `json:"sys_id"`
	}
	if err := json.Unmarshal(result, &items); err != nil {
		t.Fatalf("result is not the unwrapped array: %v (%s)", err, result)
	}
	if len(items) != 1 || items[0].SysID != "1" {
		t.Errorf("items = %+v, want one item with sys_id=1", items)
	}
}

func TestGetAllProjects_EmptySearchSendsEmptyQuery(t *testing.T) {
	var capturedQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":[]}`))
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL, Username: "u", Password: "p"})

	if _, err := c.GetAllProjects(context.Background(), ""); err != nil {
		t.Fatalf("GetAllProjects returned error: %v", err)
	}
	if got := capturedQuery.Get("sysparm_query"); got != "" {
		t.Errorf("sysparm_query = %q, want empty", got)
	}
}

func TestGetAllProjects_RejectsUnsafeSearch(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL, Username: "u", Password: "p"})

	_, err := c.GetAllProjects(context.Background(), "Acme^OR active=true")
	if err == nil {
		t.Fatal("expected an error for an unsafe search value, got nil")
	}
	if called {
		t.Error("upstream should not have been called for an unsafe search value")
	}
}

func TestUsageMetricsForwarders_PostPayloadVerbatim(t *testing.T) {
	payload := []byte(`{"projectIds":["p1"],"startDate":"2026-01-01","endDate":"2026-01-31"}`)
	want := []byte(`{"data":"ok"}`)

	tests := []struct {
		name     string
		wantPath string
		call     func(c *Client) ([]byte, error)
	}{
		{"SearchInstanceMetrics", "/api/x_wso2_customer_0/instances/metrics/search", func(c *Client) ([]byte, error) {
			return c.SearchInstanceMetrics(context.Background(), payload)
		}},
		{"GetInstanceMetricsStats", "/api/x_wso2_customer_0/instances/metrics/stats", func(c *Client) ([]byte, error) {
			return c.GetInstanceMetricsStats(context.Background(), payload)
		}},
		{"SearchInstanceUsages", "/api/x_wso2_customer_0/instances/usages/search", func(c *Client) ([]byte, error) {
			return c.SearchInstanceUsages(context.Background(), payload)
		}},
		{"GetInstanceUsagesStats", "/api/x_wso2_customer_0/instances/usages/stats", func(c *Client) ([]byte, error) {
			return c.GetInstanceUsagesStats(context.Background(), payload)
		}},
		{"SearchUsageMetricsDeployments", "/api/x_wso2_customer_0/deployments/search", func(c *Client) ([]byte, error) {
			return c.SearchUsageMetricsDeployments(context.Background(), payload)
		}},
		{"SearchUsageMetricsProjects", "/api/x_wso2_customer_0/projects/search", func(c *Client) ([]byte, error) {
			return c.SearchUsageMetricsProjects(context.Background(), payload)
		}},
		{"SearchUsageMetricsDeployedProducts", "/api/x_wso2_customer_0/deployed_products/search", func(c *Client) ([]byte, error) {
			return c.SearchUsageMetricsDeployedProducts(context.Background(), payload)
		}},
		{"SearchUsageMetricsInstances", "/api/x_wso2_customer_0/instances/search", func(c *Client) ([]byte, error) {
			return c.SearchUsageMetricsInstances(context.Background(), payload)
		}},
		{"GetDeployedProductMetrics", "/api/x_wso2_customer_0/deployed_products/dp-1/metrics/search", func(c *Client) ([]byte, error) {
			return c.GetDeployedProductMetrics(context.Background(), "dp-1", payload)
		}},
		{"GetDeployedProductUsageCounts", "/api/x_wso2_customer_0/deployed_products/dp-1/metrics/usage-counts/search", func(c *Client) ([]byte, error) {
			return c.GetDeployedProductUsageCounts(context.Background(), "dp-1", payload)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var capturedPath, capturedMethod string
			var capturedBody []byte
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedPath = r.URL.Path
				capturedMethod = r.Method
				capturedBody, _ = io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(want)
			}))
			defer srv.Close()

			c := NewClient(Config{BaseURL: srv.URL, Username: "u", Password: "p"})
			got, err := tt.call(c)
			if err != nil {
				t.Fatalf("call returned error: %v", err)
			}
			if capturedMethod != http.MethodPost {
				t.Errorf("method = %q, want POST", capturedMethod)
			}
			if capturedPath != tt.wantPath {
				t.Errorf("path = %q, want %q", capturedPath, tt.wantPath)
			}
			if string(capturedBody) != string(payload) {
				t.Errorf("forwarded body = %s, want %s (must be byte-identical, no reshaping)", capturedBody, payload)
			}
			if string(got) != string(want) {
				t.Errorf("response = %s, want %s (must be byte-identical passthrough)", got, want)
			}
		})
	}
}

func TestValidateMetricsDateRange(t *testing.T) {
	tests := []struct {
		name      string
		startDate string
		endDate   string
		wantErr   bool
	}{
		{"valid same-day range", "2026-01-01", "2026-01-01", false},
		{"valid range within limit", "2026-01-01", "2026-06-01", false},
		{"end before start", "2026-06-01", "2026-01-01", true},
		{"malformed start date", "not-a-date", "2026-01-01", true},
		{"malformed end date", "2026-01-01", "not-a-date", true},
		{"range exceeds 366 days", "2025-01-01", "2026-06-01", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateMetricsDateRange(tt.startDate, tt.endDate)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateMetricsDateRange(%q, %q) error = %v, wantErr %v", tt.startDate, tt.endDate, err, tt.wantErr)
			}
		})
	}
}
