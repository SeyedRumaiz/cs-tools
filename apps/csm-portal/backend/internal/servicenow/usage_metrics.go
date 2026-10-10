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
	"fmt"
	"net/url"
	"time"
)

// usageMetricsCustomAPIBase is the path prefix of SupportPortalLite's
// custom scoped-app REST API used by every usage-metrics endpoint — mirrors
// the Ballerina snClient->/api/x_wso2_customer_0/... call sites in
// modules/operations/operations.bal.
const usageMetricsCustomAPIBase = "/api/x_wso2_customer_0"

// GetAllProjects lists ServiceNow customer_project records whose
// short_description or number match search (a "contains" match, mirroring
// Ballerina getAllProjects — empty search returns the first 30 projects
// unfiltered). Returns the raw JSON array from ServiceNow's "result" field
// verbatim (each item shaped {sys_id, short_description, number, ...}) —
// this endpoint reshapes nothing beyond unwrapping that envelope, so the
// response bytes are passed through byte-for-byte rather than re-marshaled
// through a typed struct, keeping the frontend's existing contract intact.
func (c *Client) GetAllProjects(ctx context.Context, search string) ([]byte, error) {
	query := ""
	if search != "" {
		if err := SanitizeQueryValue(search); err != nil {
			return nil, err
		}
		// Not a plain BuildEncodedQuery AND-join: mirrors Ballerina's
		// "short_descriptionLIKE" + search + "^ORnumberLIKE" + search
		// exactly, an OR across the two fields.
		query = "short_descriptionLIKE" + search + "^ORnumberLIKE" + search
	}

	raw, err := c.TableQuery(ctx, "customer_project", url.Values{
		"sysparm_query":  {query},
		"sysparm_fields": {"sys_id,short_description,number"},
		"sysparm_limit":  {"30"},
	})
	if err != nil {
		return nil, err
	}

	var envelope struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("servicenow: decode customer_project list response: %w", err)
	}
	if envelope.Result == nil {
		return []byte("[]"), nil
	}
	return envelope.Result, nil
}

// The following ten methods each forward payload verbatim as the POST body
// to a fixed path under SupportPortalLite's custom scoped-app usage-metrics
// API, and return the upstream JSON response verbatim. Ballerina's
// operations.bal does the same for all of them — every one of these
// functions POSTs the caller's payload unchanged and merely
// type-validates the response via cloneWithType, without renaming or
// reshaping any field — so callers here pass a []byte (already
// size-capped and JSON-validated by the handler) straight through with no
// intermediate struct.

func (c *Client) SearchInstanceMetrics(ctx context.Context, payload []byte) ([]byte, error) {
	return c.CustomPost(ctx, usageMetricsCustomAPIBase+"/instances/metrics/search", payload)
}

func (c *Client) GetInstanceMetricsStats(ctx context.Context, payload []byte) ([]byte, error) {
	return c.CustomPost(ctx, usageMetricsCustomAPIBase+"/instances/metrics/stats", payload)
}

func (c *Client) SearchInstanceUsages(ctx context.Context, payload []byte) ([]byte, error) {
	return c.CustomPost(ctx, usageMetricsCustomAPIBase+"/instances/usages/search", payload)
}

func (c *Client) GetInstanceUsagesStats(ctx context.Context, payload []byte) ([]byte, error) {
	return c.CustomPost(ctx, usageMetricsCustomAPIBase+"/instances/usages/stats", payload)
}

func (c *Client) SearchUsageMetricsDeployments(ctx context.Context, payload []byte) ([]byte, error) {
	return c.CustomPost(ctx, usageMetricsCustomAPIBase+"/deployments/search", payload)
}

func (c *Client) SearchUsageMetricsProjects(ctx context.Context, payload []byte) ([]byte, error) {
	return c.CustomPost(ctx, usageMetricsCustomAPIBase+"/projects/search", payload)
}

func (c *Client) SearchUsageMetricsDeployedProducts(ctx context.Context, payload []byte) ([]byte, error) {
	return c.CustomPost(ctx, usageMetricsCustomAPIBase+"/deployed_products/search", payload)
}

func (c *Client) SearchUsageMetricsInstances(ctx context.Context, payload []byte) ([]byte, error) {
	return c.CustomPost(ctx, usageMetricsCustomAPIBase+"/instances/search", payload)
}

// GetDeployedProductMetrics forwards payload verbatim to the per-deployed-
// product metrics endpoint. deployedProductID is the ServiceNow sys_id path
// segment.
func (c *Client) GetDeployedProductMetrics(ctx context.Context, deployedProductID string, payload []byte) ([]byte, error) {
	path := usageMetricsCustomAPIBase + "/deployed_products/" + url.PathEscape(deployedProductID) + "/metrics/search"
	return c.CustomPost(ctx, path, payload)
}

// GetDeployedProductUsageCounts forwards payload verbatim to the
// per-deployed-product usage-counts endpoint. deployedProductID is the
// ServiceNow sys_id path segment.
func (c *Client) GetDeployedProductUsageCounts(ctx context.Context, deployedProductID string, payload []byte) ([]byte, error) {
	path := usageMetricsCustomAPIBase + "/deployed_products/" + url.PathEscape(deployedProductID) + "/metrics/usage-counts/search"
	return c.CustomPost(ctx, path, payload)
}

// maxMetricsDateRangeDays mirrors Ballerina's
// MAX_METRICS_DATE_RANGE_DAYS constant.
const maxMetricsDateRangeDays = 366

// ValidateMetricsDateRange mirrors Ballerina's validateMetricsDateRange:
// startDate/endDate must be valid YYYY-MM-DD dates, endDate must not be
// before startDate, and the span between them must not exceed
// maxMetricsDateRangeDays. Returns a caller-facing error message (safe to
// return directly in a 400 response, matching how the Ballerina original's
// message is shown to the caller) or nil if the range is valid.
func ValidateMetricsDateRange(startDate, endDate string) error {
	const layout = "2006-01-02"
	start, startErr := time.Parse(layout, startDate)
	end, endErr := time.Parse(layout, endDate)
	if startErr != nil || endErr != nil {
		return errors.New("startDate and endDate must be valid dates in YYYY-MM-DD format")
	}
	diff := end.Sub(start)
	if diff < 0 {
		return errors.New("endDate must not be before startDate")
	}
	if diff > maxMetricsDateRangeDays*24*time.Hour {
		return fmt.Errorf("date range must not exceed %d days", maxMetricsDateRangeDays)
	}
	return nil
}
