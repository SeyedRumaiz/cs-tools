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

// Package csm is a client for csm-integration-service, duplicated per-service since Go modules here don't share internal/ packages.
package csm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/sony/gobreaker/v2"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

// Error is returned when csm-integration-service responds with a non-2xx status.
type Error struct {
	StatusCode int
	Body       string
}

func (e *Error) Error() string {
	return fmt.Sprintf("csm-integration-service returned %d: %s", e.StatusCode, e.Body)
}

// tokenFetchTimeout is the HTTP client timeout for token-endpoint requests.
var tokenFetchTimeout = 10 * time.Second

// breakerOpenTimeout is how long the breaker stays open before allowing one probe request through.
const breakerOpenTimeout = 30 * time.Second

// breakerConsecutiveFailures trips the breaker after this many back-to-back failures.
const breakerConsecutiveFailures = 5

type ctxKey string

const correlationIDKey ctxKey = "x-csm-correlation-id" // #nosec G101 -- context map key, not a credential

// WithCorrelationID returns a copy of ctx carrying the id forwarded as X-CSM-Correlation-ID.
func WithCorrelationID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, correlationIDKey, id)
}

func correlationIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(correlationIDKey).(string)
	return v
}

type Config struct {
	BaseURL      string
	TokenURL     string
	ClientID     string
	ClientSecret string
	Scopes       []string
	// HTTPTimeout bounds each CSM call, so one stuck call can't hold a delivery worker and its lock.
	HTTPTimeout time.Duration
}

// Client authenticates via OAuth2 client-credentials grant; tokens are acquired and refreshed automatically.
type Client struct {
	http    *http.Client
	baseURL string
	// breaker trips after a run of failures so a confirmed outage fails fast instead of retrying against a known-down downstream.
	breaker *gobreaker.CircuitBreaker[[]byte]
}

// NewClient constructs a Client using the OAuth2 client-credentials grant.
func NewClient(cfg Config) *Client {
	cc := clientcredentials.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		TokenURL:     cfg.TokenURL,
		Scopes:       cfg.Scopes,
	}

	tokenHTTPClient := &http.Client{
		Timeout:   tokenFetchTimeout,
		Transport: &httpsOnlyTransport{},
	}
	tokenHTTPClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}
	tokenCtx := context.WithValue(context.Background(), oauth2.HTTPClient, tokenHTTPClient)
	httpClient := cc.Client(tokenCtx)
	httpClient.Timeout = cfg.HTTPTimeout
	httpClient.Transport = &httpsOnlyTransport{base: httpClient.Transport}
	httpClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}

	return &Client{
		http:    httpClient,
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
		breaker: newBreaker(),
	}
}

// newBreaker excludes a non-retryable 4xx and genuine caller cancellation from tripping the breaker, but not a bare http.Client timeout, which must count as a real CSM-is-down failure.
func newBreaker() *gobreaker.CircuitBreaker[[]byte] {
	return gobreaker.NewCircuitBreaker[[]byte](gobreaker.Settings{
		Name:    "csm",
		Timeout: breakerOpenTimeout,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures >= breakerConsecutiveFailures
		},
		IsExcluded: func(err error) bool {
			if errors.Is(err, errCallerDone) {
				return true
			}
			var apiErr *Error
			if errors.As(err, &apiErr) {
				return apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 && apiErr.StatusCode != http.StatusTooManyRequests
			}
			return false
		},
	})
}

// errCallerDone marks a failure as caused by the caller's own ctx, not an http.Client-level timeout.
var errCallerDone = errors.New("csm: caller context canceled or deadline exceeded")

// do executes an authenticated request through the circuit breaker (caller owns the returned body slice); when open it returns gobreaker.ErrOpenState with no network call, which callers already treat as retryable.
func (c *Client) do(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	return c.breaker.Execute(func() ([]byte, error) {
		var reqBody io.Reader
		if len(body) > 0 {
			reqBody = bytes.NewReader(body)
		}

		req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reqBody)
		if err != nil {
			return nil, fmt.Errorf("csm: build request %s %s: %w", method, path, err)
		}
		if len(body) > 0 {
			req.Header.Set("Content-Type", "application/json")
		}
		if id := correlationIDFromContext(ctx); id != "" {
			req.Header.Set("X-CSM-Correlation-ID", id)
		}

		resp, err := c.http.Do(req)
		if err != nil {
			return nil, wrapCallerDone(ctx, fmt.Errorf("csm: %s %s: %w", method, path, err))
		}
		defer resp.Body.Close()

		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			const maxErrBody = 256
			excerpt, err := io.ReadAll(io.LimitReader(resp.Body, maxErrBody))
			if err != nil {
				return nil, wrapCallerDone(ctx, fmt.Errorf("csm: read error response body: %w", err))
			}
			return nil, &Error{StatusCode: resp.StatusCode, Body: string(excerpt)}
		}

		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, wrapCallerDone(ctx, fmt.Errorf("csm: read response body: %w", err))
		}

		return respBody, nil
	})
}

// wrapCallerDone wraps err with errCallerDone only if ctx itself is done, so an http.Client timeout stays a plain error (counts as a breaker failure) while genuine caller cancellation is excluded.
func wrapCallerDone(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return fmt.Errorf("%w: %w", errCallerDone, err)
	}
	return err
}

// httpsOnlyTransport blocks non-HTTPS requests since this client always carries a secret or bearer token.
type httpsOnlyTransport struct {
	base http.RoundTripper
}

func (t *httpsOnlyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL == nil || req.URL.Scheme != "https" {
		return nil, fmt.Errorf("csm: refusing non-HTTPS endpoint %s", req.URL.Redacted())
	}
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(req)
}
