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

package sftpgo

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/apierror"
)

func TestMintTokenSendsCorrectBasicAuth(t *testing.T) {
	t.Parallel()

	var gotAuthHeader, gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthHeader = r.Header.Get("Authorization")
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"tok-123","expires_at":"2026-08-27T12:00:00Z"}`))
	}))
	defer srv.Close()

	client := NewClient(Config{BaseURL: srv.URL})

	tok, err := client.MintToken(context.Background(), "jane.doe@example.com", "raw-jwt-assertion")
	if err != nil {
		t.Fatalf("MintToken: %v", err)
	}

	if gotMethod != http.MethodGet {
		t.Errorf("method = %q, want GET", gotMethod)
	}
	if gotPath != "/api/v2/user/token" {
		t.Errorf("path = %q, want /api/v2/user/token", gotPath)
	}

	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("jane.doe@example.com:raw-jwt-assertion"))
	if gotAuthHeader != wantAuth {
		t.Errorf("Authorization header = %q, want %q", gotAuthHeader, wantAuth)
	}

	if tok.AccessToken != "tok-123" {
		t.Errorf("AccessToken = %q, want tok-123", tok.AccessToken)
	}
	var expiresAt string
	if err := json.Unmarshal(tok.ExpiresAt, &expiresAt); err != nil {
		t.Fatalf("decode ExpiresAt: %v", err)
	}
	if expiresAt != "2026-08-27T12:00:00Z" {
		t.Errorf("ExpiresAt = %q, want 2026-08-27T12:00:00Z", expiresAt)
	}
}

func TestMintTokenPropagatesUpstreamError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"invalid credentials"}`))
	}))
	defer srv.Close()

	client := NewClient(Config{BaseURL: srv.URL})

	_, err := client.MintToken(context.Background(), "jane.doe@example.com", "bad-jwt")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	apiErr, ok := err.(*apierror.Error)
	if !ok {
		t.Fatalf("error type = %T, want *apierror.Error", err)
	}
	if apiErr.StatusCode != http.StatusUnauthorized {
		t.Errorf("StatusCode = %d, want 401", apiErr.StatusCode)
	}
}

func TestMintTokenRejectsMissingAccessToken(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	client := NewClient(Config{BaseURL: srv.URL})

	if _, err := client.MintToken(context.Background(), "jane.doe@example.com", "raw-jwt"); err == nil {
		t.Fatal("expected error for a response with no access_token, got nil")
	}
}

func TestCreateShareSendsCorrectRequestShape(t *testing.T) {
	t.Parallel()

	var gotAuthHeader, gotMethod, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthHeader = r.Header.Get("Authorization")
		gotMethod = r.Method
		gotPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)

		w.Header().Set("X-Object-Id", "share-abc-123")
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	client := NewClient(Config{BaseURL: srv.URL})

	before := time.Now()
	shareID, err := client.CreateShare(context.Background(), "access-tok", "/attachments/00000000-0000-0000-0000-000000000000", ShareScopeRead, 5*time.Minute)
	if err != nil {
		t.Fatalf("CreateShare: %v", err)
	}
	after := time.Now()

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/api/v2/user/shares" {
		t.Errorf("path = %q, want /api/v2/user/shares", gotPath)
	}
	if gotAuthHeader != "Bearer access-tok" {
		t.Errorf("Authorization header = %q, want %q", gotAuthHeader, "Bearer access-tok")
	}
	if shareID != "share-abc-123" {
		t.Errorf("shareID = %q, want share-abc-123 (from X-Object-Id header)", shareID)
	}

	paths, _ := gotBody["paths"].([]any)
	if len(paths) != 1 || paths[0] != "/attachments/00000000-0000-0000-0000-000000000000" {
		t.Errorf("paths = %v, want a single-element array with the storage key", gotBody["paths"])
	}
	if scope, _ := gotBody["scope"].(float64); scope != ShareScopeRead {
		t.Errorf("scope = %v, want %d (read-only)", gotBody["scope"], ShareScopeRead)
	}
	if _, hasPassword := gotBody["password"]; hasPassword {
		t.Errorf("request body carried a password field, want none")
	}
	expiresAtMs, _ := gotBody["expires_at"].(float64)
	wantMin := float64(before.Add(5 * time.Minute).UnixMilli())
	wantMax := float64(after.Add(5 * time.Minute).UnixMilli())
	if expiresAtMs < wantMin || expiresAtMs > wantMax {
		t.Errorf("expires_at = %v, want between %v and %v (now+5m in unix ms)", expiresAtMs, wantMin, wantMax)
	}
}

func TestCreateShareFallsBackToJSONBodyWhenHeaderMissing(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"share-from-body"}`))
	}))
	defer srv.Close()

	client := NewClient(Config{BaseURL: srv.URL})

	shareID, err := client.CreateShare(context.Background(), "access-tok", "/attachments/x", ShareScopeRead, time.Minute)
	if err != nil {
		t.Fatalf("CreateShare: %v", err)
	}
	if shareID != "share-from-body" {
		t.Errorf("shareID = %q, want share-from-body", shareID)
	}
}

func TestCreateSharePrefersHeaderOverBody(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Object-Id", "from-header")
		_, _ = w.Write([]byte(`{"id":"from-body"}`))
	}))
	defer srv.Close()

	client := NewClient(Config{BaseURL: srv.URL})

	shareID, err := client.CreateShare(context.Background(), "access-tok", "/attachments/x", ShareScopeRead, time.Minute)
	if err != nil {
		t.Fatalf("CreateShare: %v", err)
	}
	if shareID != "from-header" {
		t.Errorf("shareID = %q, want from-header (header takes precedence)", shareID)
	}
}

func TestCreateShareErrorsWhenNoIDFound(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	client := NewClient(Config{BaseURL: srv.URL})

	if _, err := client.CreateShare(context.Background(), "access-tok", "/attachments/x", ShareScopeRead, time.Minute); err == nil {
		t.Fatal("expected error when neither header nor body carry an id, got nil")
	}
}

func TestCreateSharePropagatesUpstreamError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	client := NewClient(Config{BaseURL: srv.URL})

	_, err := client.CreateShare(context.Background(), "access-tok", "/attachments/x", ShareScopeRead, time.Minute)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	apiErr, ok := err.(*apierror.Error)
	if !ok {
		t.Fatalf("error type = %T, want *apierror.Error", err)
	}
	if apiErr.StatusCode != http.StatusForbidden {
		t.Errorf("StatusCode = %d, want 403", apiErr.StatusCode)
	}
}

func TestCreateShareSendsWriteScopeAndNoPassword(t *testing.T) {
	t.Parallel()

	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("X-Object-Id", "write-share-abc")
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	client := NewClient(Config{BaseURL: srv.URL})

	shareID, err := client.CreateShare(context.Background(), "access-tok", "/attachments/upload-target", ShareScopeWrite, 45*time.Minute)
	if err != nil {
		t.Fatalf("CreateShare: %v", err)
	}
	if shareID != "write-share-abc" {
		t.Errorf("shareID = %q, want write-share-abc", shareID)
	}

	paths, _ := gotBody["paths"].([]any)
	if len(paths) != 1 || paths[0] != "/attachments/upload-target" {
		t.Errorf("paths = %v, want a single-element array with the storage key", gotBody["paths"])
	}
	if scope, _ := gotBody["scope"].(float64); scope != ShareScopeWrite {
		t.Errorf("scope = %v, want %d (write)", gotBody["scope"], ShareScopeWrite)
	}
	if _, hasPassword := gotBody["password"]; hasPassword {
		t.Errorf("request body carried a password field, want none — a server-minted upload share must have no password")
	}
}

func TestPublicShareURL(t *testing.T) {
	t.Parallel()

	t.Run("uses PublicBaseURL when set", func(t *testing.T) {
		t.Parallel()
		client := NewClient(Config{BaseURL: "https://sftpgo-api.internal.example.com", PublicBaseURL: "https://share.example.com"})
		got := client.PublicShareURL("abc-123")
		want := "https://share.example.com/web/client/pubshares/abc-123?compress=false"
		if got != want {
			t.Errorf("PublicShareURL = %q, want %q", got, want)
		}
	})

	t.Run("defaults to BaseURL when PublicBaseURL unset", func(t *testing.T) {
		t.Parallel()
		client := NewClient(Config{BaseURL: "https://sftpgo.example.com/"})
		got := client.PublicShareURL("abc-123")
		want := "https://sftpgo.example.com/web/client/pubshares/abc-123?compress=false"
		if got != want {
			t.Errorf("PublicShareURL = %q, want %q", got, want)
		}
	})

	t.Run("path-escapes the share id", func(t *testing.T) {
		t.Parallel()
		client := NewClient(Config{BaseURL: "https://sftpgo.example.com"})
		got := client.PublicShareURL("abc/def")
		if !strings.Contains(got, "abc%2Fdef") {
			t.Errorf("PublicShareURL = %q, want the share id path-escaped", got)
		}
	})

	t.Run("does not use the /shares/{id} path", func(t *testing.T) {
		t.Parallel()
		client := NewClient(Config{BaseURL: "https://sftpgo.example.com"})
		got := client.PublicShareURL("abc-123")
		if strings.Contains(got, "/shares/abc-123") && !strings.Contains(got, "/web/client/pubshares/abc-123") {
			t.Errorf("PublicShareURL = %q, must use /web/client/pubshares/, not /shares/", got)
		}
	})
}

func TestBaseURL(t *testing.T) {
	t.Parallel()
	client := NewClient(Config{BaseURL: "https://sftpgo.example.com/"})
	if got := client.BaseURL(); got != "https://sftpgo.example.com" {
		t.Errorf("BaseURL() = %q, want trailing slash trimmed", got)
	}
}

// TestClientRefusesInsecureRedirect proves the http.Client this package
// constructs does not follow an HTTPS-to-HTTP redirect (which, under Go's
// default CheckRedirect, would copy the Authorization header onto the
// downgraded, cleartext request) — see refuseInsecureRedirect. downgradeSrv
// stands in for the "same host, now-plain-HTTP" redirect target: since
// CheckRedirect must refuse the redirect before the client ever issues a
// request to it, downgradeSrv should never see any request at all, bearer
// token or otherwise.
func TestClientRefusesInsecureRedirect(t *testing.T) {
	t.Parallel()

	downgradeHit := false
	downgradeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		downgradeHit = true
		w.WriteHeader(http.StatusOK)
	}))
	defer downgradeSrv.Close()

	tlsSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Redirect to the same logical host but downgraded to plain HTTP —
		// the scenario refuseInsecureRedirect exists to block.
		http.Redirect(w, r, downgradeSrv.URL+"/api/v2/user/shares", http.StatusFound)
	}))
	defer tlsSrv.Close()

	client := NewClient(Config{BaseURL: tlsSrv.URL})
	// Trust the TLS test server's self-signed certificate; keep our own
	// CheckRedirect override intact.
	client.http.Transport = tlsSrv.Client().Transport

	_, err := client.CreateShare(context.Background(), "access-tok", "/attachments/00000000-0000-0000-0000-000000000000", ShareScopeWrite, 5*time.Minute)
	if err == nil {
		t.Fatal("CreateShare: expected an error refusing the insecure redirect, got nil")
	}
	if !strings.Contains(err.Error(), "refusing to follow redirect") {
		t.Errorf("CreateShare error = %v, want it to mention refusing the redirect", err)
	}
	if downgradeHit {
		t.Error("downgrade target received a request; CheckRedirect should have refused before dialing it (Authorization would have leaked over plain HTTP)")
	}
}

// TestClientRefusesCrossOriginRedirectOnTUSCreate proves a TUS create (POST
// /api/v2/shares-chunked-uploads) redirected to a DIFFERENT HTTPS origin is
// refused before the foreign host is ever dialed — a 307/308 there would
// forward the Upload-Metadata header, whose share_id is the entire upload
// credential, to a host this client was never configured to trust.
func TestClientRefusesCrossOriginRedirectOnTUSCreate(t *testing.T) {
	t.Parallel()

	foreignHit := false
	foreignSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		foreignHit = true
		w.WriteHeader(http.StatusOK)
	}))
	defer foreignSrv.Close()

	tlsSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// HTTPS-to-HTTPS, but a different origin: the scenario the
		// same-origin check in refuseInsecureRedirect exists to block.
		http.Redirect(w, r, foreignSrv.URL+"/api/v2/shares-chunked-uploads", http.StatusTemporaryRedirect)
	}))
	defer tlsSrv.Close()

	client := NewClient(Config{BaseURL: tlsSrv.URL})
	client.http.Transport = tlsSrv.Client().Transport

	err := client.UploadBytes(context.Background(), "share-abc", "/attachments/x/img.png", []byte("payload"), "image/png")
	if err == nil {
		t.Fatal("UploadBytes: expected an error refusing the cross-origin redirect, got nil")
	}
	if !strings.Contains(err.Error(), "refusing to follow redirect to foreign origin") {
		t.Errorf("UploadBytes error = %v, want it to mention refusing the foreign-origin redirect", err)
	}
	if foreignHit {
		t.Error("foreign origin received a request; CheckRedirect should have refused before dialing it (Upload-Metadata's share_id would have leaked)")
	}
}

// TestClientRefusesCrossOriginRedirectOnTUSPatch is the PATCH-leg twin of the
// create-leg test above: the create succeeds on the configured origin, then
// the PATCH carrying the file bytes is redirected to a different HTTPS origin
// and must be refused before that host sees the body.
func TestClientRefusesCrossOriginRedirectOnTUSPatch(t *testing.T) {
	t.Parallel()

	foreignHit := false
	foreignSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		foreignHit = true
		w.WriteHeader(http.StatusNoContent)
	}))
	defer foreignSrv.Close()

	tlsSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			w.Header().Set("Location", "/api/v2/shares-chunked-uploads/upload-1")
			w.WriteHeader(http.StatusCreated)
		case http.MethodPatch:
			http.Redirect(w, r, foreignSrv.URL+"/api/v2/shares-chunked-uploads/upload-1", http.StatusTemporaryRedirect)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer tlsSrv.Close()

	client := NewClient(Config{BaseURL: tlsSrv.URL})
	client.http.Transport = tlsSrv.Client().Transport

	err := client.UploadBytes(context.Background(), "share-abc", "/attachments/x/img.png", []byte("payload"), "image/png")
	if err == nil {
		t.Fatal("UploadBytes: expected an error refusing the cross-origin redirect, got nil")
	}
	if !strings.Contains(err.Error(), "refusing to follow redirect to foreign origin") {
		t.Errorf("UploadBytes error = %v, want it to mention refusing the foreign-origin redirect", err)
	}
	if foreignHit {
		t.Error("foreign origin received a request; CheckRedirect should have refused before dialing it (the PATCH body's bytes would have leaked)")
	}
}

// TestClientFollowsSameOriginRedirect proves the same-origin check does not
// over-block: an HTTPS redirect to a different path on the SAME origin is
// still followed, on both the TUS create and PATCH legs.
func TestClientFollowsSameOriginRedirect(t *testing.T) {
	t.Parallel()

	var patchLanded bool
	var mux http.ServeMux
	mux.HandleFunc("POST /api/v2/shares-chunked-uploads", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/relocated-create", http.StatusTemporaryRedirect)
	})
	mux.HandleFunc("POST /relocated-create", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "/api/v2/shares-chunked-uploads/upload-1")
		w.WriteHeader(http.StatusCreated)
	})
	mux.HandleFunc("PATCH /api/v2/shares-chunked-uploads/upload-1", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/relocated-patch", http.StatusTemporaryRedirect)
	})
	mux.HandleFunc("PATCH /relocated-patch", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if string(body) != "payload" {
			t.Errorf("redirected PATCH body = %q, want %q", body, "payload")
		}
		patchLanded = true
		w.WriteHeader(http.StatusNoContent)
	})
	tlsSrv := httptest.NewTLSServer(&mux)
	defer tlsSrv.Close()

	client := NewClient(Config{BaseURL: tlsSrv.URL})
	client.http.Transport = tlsSrv.Client().Transport

	if err := client.UploadBytes(context.Background(), "share-abc", "/attachments/x/img.png", []byte("payload"), "image/png"); err != nil {
		t.Fatalf("UploadBytes: %v (a same-origin redirect must still be followed)", err)
	}
	if !patchLanded {
		t.Error("redirected PATCH target was never reached")
	}
}

// TestDoTruncatesHugeErrorBody proves a non-2xx response with an arbitrarily
// large body surfaces as an *apierror.Error whose Body excerpt is bounded at
// maxErrBodyBytes — the client reads only just past the limit rather than
// buffering the whole thing.
func TestDoTruncatesHugeErrorBody(t *testing.T) {
	t.Parallel()

	huge := strings.Repeat("x", 1<<20) // 1 MiB, far past maxErrBodyBytes
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, huge)
	}))
	defer srv.Close()

	client := NewClient(Config{BaseURL: srv.URL})

	_, err := client.MintToken(context.Background(), "jane.doe@example.com", "raw-jwt")
	var apiErr *apierror.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *apierror.Error", err)
	}
	if apiErr.StatusCode != http.StatusInternalServerError {
		t.Errorf("StatusCode = %d, want 500", apiErr.StatusCode)
	}
	if len(apiErr.Body) != maxErrBodyBytes {
		t.Errorf("len(Body) = %d, want exactly %d (truncated)", len(apiErr.Body), maxErrBodyBytes)
	}
	if apiErr.Body != huge[:maxErrBodyBytes] {
		t.Errorf("Body = %q, want the first %d bytes of the upstream body", apiErr.Body, maxErrBodyBytes)
	}
}

func TestRemoveFileSendsCorrectRequestShape(t *testing.T) {
	t.Parallel()

	var gotMethod, gotPath, gotQueryPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotQueryPath = r.URL.Query().Get("path")
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := NewClient(Config{BaseURL: srv.URL})

	storageKey := "/attachments/cases/case-1/att-1/report file.pdf"
	if err := client.RemoveFile(context.Background(), "tok-123", storageKey); err != nil {
		t.Fatalf("RemoveFile: %v", err)
	}

	if gotMethod != http.MethodDelete {
		t.Errorf("method = %q, want DELETE", gotMethod)
	}
	if gotPath != "/api/v2/user/files" {
		t.Errorf("path = %q, want /api/v2/user/files", gotPath)
	}
	if gotQueryPath != storageKey {
		t.Errorf("query path = %q, want %q (must round-trip through URL escaping unchanged)", gotQueryPath, storageKey)
	}
	if gotAuth != "Bearer tok-123" {
		t.Errorf("Authorization = %q, want Bearer tok-123", gotAuth)
	}
}

func TestRemoveFilePropagatesUpstreamError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"no such file"}`))
	}))
	defer srv.Close()

	client := NewClient(Config{BaseURL: srv.URL})

	err := client.RemoveFile(context.Background(), "tok-123", "/attachments/missing")
	var apiErr *apierror.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *apierror.Error", err)
	}
	if apiErr.StatusCode != http.StatusNotFound {
		t.Errorf("StatusCode = %d, want 404", apiErr.StatusCode)
	}
}
