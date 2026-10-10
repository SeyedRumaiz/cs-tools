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

// Package availability triggers entity-service's nightly uptime
// recalculation — the Go replacement for ServiceNow's "Calculate
// Availability" scheduled job.
//
// *** A TRIGGER, NOT A CALCULATOR. *** All the arithmetic is in
// entity-service, deliberately: the sweep reads every outage for ~146
// subjects and writes up to eight rows each into a table the Cloud Status
// Dashboard is concurrently reading. Doing that over HTTP from here would
// mean pulling the whole working set across the wire on every run, and this
// component holds no database credentials anyway. Same split as
// internal/cloudstatus.
package availability

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"

	"github.com/wso2-open-operations/cs-tools/operations/csm-scheduled-tasks/internal/apierror"
	"github.com/wso2-open-operations/cs-tools/operations/csm-scheduled-tasks/internal/httpsec"
)

var tokenFetchTimeout = 10 * time.Second

// sweepTimeout bounds one run.
//
// *** FIVE MINUTES, NOT THE SIXTY SECONDS THE OTHER SWEEPS USE. *** This one
// is not small: ~146 subjects, each with an outage query over a twelve-month
// window and up to eight period rows written. The neighbouring sweeps bound
// themselves at a minute because their candidate sets are tiny and a slow
// run means trouble rather than volume; here volume is the normal case, and
// a minute would cut a healthy run off partway through — leaving some
// subjects updated and the rest stale, which is worse than not running.
const sweepTimeout = 5 * time.Minute

// Config holds this client's configuration.
type Config struct {
	BaseURL      string
	TokenURL     string
	ClientID     string
	ClientSecret string
	Scopes       []string
}

// Client calls entity-service's availability sweep.
type Client struct {
	http    *http.Client
	baseURL string
}

// NewClient constructs a Client authenticated via the client credentials grant.
func NewClient(cfg Config) (*Client, error) {
	if err := httpsec.RequireSecureURL(cfg.TokenURL); err != nil {
		return nil, fmt.Errorf("availability: token URL: %w", err)
	}
	if err := httpsec.RequireSecureURL(cfg.BaseURL); err != nil {
		return nil, fmt.Errorf("availability: base URL: %w", err)
	}
	cc := clientcredentials.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		TokenURL:     cfg.TokenURL,
		Scopes:       cfg.Scopes,
	}
	tokenHTTP := &http.Client{Timeout: tokenFetchTimeout}
	httpsec.RejectInsecureRedirects(tokenHTTP)
	httpClient := cc.Client(context.WithValue(context.Background(), oauth2.HTTPClient, tokenHTTP))
	httpClient.Timeout = sweepTimeout
	httpsec.RejectInsecureRedirects(httpClient)

	return &Client{http: httpClient, baseURL: strings.TrimRight(cfg.BaseURL, "/")}, nil
}

// SweepResult is entity-service's wire shape.
type SweepResult struct {
	Subjects int `json:"subjects"`
	Rows     int `json:"rows"`
	Failed   int `json:"failed"`
}

// Sweep asks entity-service to recompute every subject's availability.
//
// Idempotent by construction: fixed periods are replaced by their natural
// key and rolling windows are deleted and rewritten, so running twice in a
// day produces the same table as running once. That matters because this is
// the only safe way to recover a missed night — there is no catch-up mode,
// and a day whose rows were never written stays empty until somebody
// reruns.
func (c *Client) Sweep(ctx context.Context) (SweepResult, error) {
	const path = "/internal/availability/sweep"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, nil)
	if err != nil {
		return SweepResult{}, fmt.Errorf("availability: build request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return SweepResult{}, fmt.Errorf("availability: POST %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return SweepResult{}, fmt.Errorf("availability: read response body: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return SweepResult{}, &apierror.Error{StatusCode: resp.StatusCode, Body: string(body)}
	}
	var parsed SweepResult
	if err := json.Unmarshal(body, &parsed); err != nil {
		return SweepResult{}, fmt.Errorf("availability: decode response: %w", err)
	}
	return parsed, nil
}
