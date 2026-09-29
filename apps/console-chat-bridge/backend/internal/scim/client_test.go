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

package scim

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestClient spins up a fake token endpoint and a fake SCIM2 endpoint,
// returning a Client pointed at the latter plus the last request URL the
// fake endpoint received (for asserting the filter this package built).
func newTestClient(t *testing.T, scimHandler http.HandlerFunc) *Client {
	t.Helper()

	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"test-token","token_type":"Bearer","expires_in":3600}`))
	}))
	t.Cleanup(tokenSrv.Close)

	scimSrv := httptest.NewServer(scimHandler)
	t.Cleanup(scimSrv.Close)

	return NewClient(Config{
		BaseURL:      scimSrv.URL,
		TokenURL:     tokenSrv.URL,
		ClientID:     "test-client",
		ClientSecret: "test-secret",
		Scopes:       []string{"internal_user_mgt_list"},
	})
}

func TestResolveUserID_SingleMatch_ReturnsID(t *testing.T) {
	var gotURL string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.String()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"totalResults":1,"Resources":[{"id":"user-uuid-1","userName":"admin"}]}`))
	})

	id, err := c.ResolveUserID(context.Background(), "admin@carbon.super")
	if err != nil {
		t.Fatalf("ResolveUserID: %v", err)
	}
	if id != "user-uuid-1" {
		t.Errorf("id = %q, want %q", id, "user-uuid-1")
	}
	if !strings.Contains(gotURL, `userName+eq+%22admin%22`) && !strings.Contains(gotURL, `userName%20eq%20%22admin%22`) {
		t.Errorf("request URL = %q, want a filter for bare %q (tenant suffix stripped), not the tenant-qualified username", gotURL, "admin")
	}
	if strings.Contains(gotURL, "carbon.super") {
		t.Errorf("request URL = %q, must not contain the tenant-domain suffix", gotURL)
	}
}

func TestResolveUserID_UsernameWithQuoteAndBackslash_EscapedSafely(t *testing.T) {
	var gotFilter string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotFilter = r.URL.Query().Get("filter")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"totalResults":1,"Resources":[{"id":"user-uuid-3"}]}`))
	})

	// If this were embedded unescaped (or merely URL-encoded, not
	// JSON-string-escaped), it could break out of the filter's quoted
	// string literal and inject additional filter syntax -- e.g. widening
	// the search to every user via `... or userName pr "`.
	malicious := `x" or userName pr "`
	if _, err := c.ResolveUserID(context.Background(), malicious+"@carbon.super"); err != nil {
		t.Fatalf("ResolveUserID: %v", err)
	}

	const wantPrefix = "userName eq "
	if !strings.HasPrefix(gotFilter, wantPrefix) {
		t.Fatalf("filter = %q, want it to start with %q", gotFilter, wantPrefix)
	}
	literal := strings.TrimPrefix(gotFilter, wantPrefix)

	// The literal must be a single, valid JSON string that decodes back to
	// exactly the (tenant-suffix-stripped) input -- proving every '"' and
	// '\' inside it was escaped, not passed through raw.
	var decoded string
	if err := json.Unmarshal([]byte(literal), &decoded); err != nil {
		t.Fatalf("filter literal %q is not a valid JSON string: %v", literal, err)
	}
	if decoded != malicious {
		t.Errorf("decoded filter literal = %q, want %q", decoded, malicious)
	}

	// Confirms escaping genuinely happened (not an accidental round-trip):
	// the raw, still-escaped literal must contain a backslash-escaped
	// quote, never a bare one.
	if !strings.Contains(literal, `\"`) {
		t.Errorf("filter literal %q does not appear to escape the embedded double quote", literal)
	}
}

func TestResolveUserID_NoTenantSuffix_UsesUsernameAsIs(t *testing.T) {
	var gotURL string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.String()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"totalResults":1,"Resources":[{"id":"user-uuid-2"}]}`))
	})

	if _, err := c.ResolveUserID(context.Background(), "plainuser"); err != nil {
		t.Fatalf("ResolveUserID: %v", err)
	}
	if !strings.Contains(gotURL, "plainuser") {
		t.Errorf("request URL = %q, want it to search for %q", gotURL, "plainuser")
	}
}

func TestResolveUserID_NoMatch_FailsClosed(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"totalResults":0,"Resources":[]}`))
	})

	_, err := c.ResolveUserID(context.Background(), "nobody@carbon.super")
	if !errors.Is(err, ErrNoMatch) {
		t.Errorf("err = %v, want errors.Is(err, ErrNoMatch)", err)
	}
}

func TestResolveUserID_Ambiguous_FailsClosed(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"totalResults":2,"Resources":[{"id":"a"},{"id":"b"}]}`))
	})

	_, err := c.ResolveUserID(context.Background(), "duplicate@carbon.super")
	if !errors.Is(err, ErrAmbiguous) {
		t.Errorf("err = %v, want errors.Is(err, ErrAmbiguous)", err)
	}
}

func TestResolveUserID_ServerError_FailsClosed(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`oops`))
	})

	_, err := c.ResolveUserID(context.Background(), "admin@carbon.super")
	if !errors.Is(err, ErrUnavailable) {
		t.Errorf("err = %v, want errors.Is(err, ErrUnavailable)", err)
	}
}

func TestResolveUserID_AuthFailure_FailsClosed(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail":"insufficient_scope"}`))
	})

	_, err := c.ResolveUserID(context.Background(), "admin@carbon.super")
	if !errors.Is(err, ErrUnavailable) {
		t.Errorf("err = %v, want errors.Is(err, ErrUnavailable)", err)
	}
}

func TestResolveUserID_Unreachable_FailsClosed(t *testing.T) {
	c := NewClient(Config{
		BaseURL:      "http://127.0.0.1:0",
		TokenURL:     "http://127.0.0.1:0",
		ClientID:     "x",
		ClientSecret: "y",
	})

	_, err := c.ResolveUserID(context.Background(), "admin@carbon.super")
	if !errors.Is(err, ErrUnavailable) {
		t.Errorf("err = %v, want errors.Is(err, ErrUnavailable)", err)
	}
}

func TestResolveUserID_MalformedResponse_FailsClosed(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`not json`))
	})

	_, err := c.ResolveUserID(context.Background(), "admin@carbon.super")
	if !errors.Is(err, ErrUnavailable) {
		t.Errorf("err = %v, want errors.Is(err, ErrUnavailable)", err)
	}
}

func TestResolveUserID_MatchedUserHasNoID_FailsClosed(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"totalResults":1,"Resources":[{"userName":"admin"}]}`))
	})

	_, err := c.ResolveUserID(context.Background(), "admin@carbon.super")
	if !errors.Is(err, ErrUnavailable) {
		t.Errorf("err = %v, want errors.Is(err, ErrUnavailable)", err)
	}
}
