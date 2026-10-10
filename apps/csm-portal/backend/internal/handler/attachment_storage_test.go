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

package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/apierror"
	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/sftpgo"
)

// ----- mock SFTPGo client -----

type mockSftpgoClient struct {
	mintTokenFn    func(ctx context.Context, email, jwtAssertion string) (*sftpgo.Token, error)
	createShareFn  func(ctx context.Context, accessToken, storageKey string, scope int, ttl time.Duration) (string, error)
	uploadBytesFn  func(ctx context.Context, shareID, storageKey string, data []byte, contentType string) error
	publicShareURL func(shareID string) string
	baseURL        string

	mintTokenCalls    []string // records the jwtAssertion passed on each call
	createShareCalls  []string // records the storageKey passed on each call
	createShareScopes []int    // records the scope passed on each CreateShare call
	uploadBytesCalls  []string // records the storageKey passed on each UploadBytes call
	removeFileCalls   []string // records the storageKey passed on each RemoveFile call
	removeFileCtxErrs []error  // records ctx.Err() at the time of each RemoveFile call

	removeFileFn func(ctx context.Context, accessToken, storageKey string) error
}

// RemoveFile satisfies inlineImageSftpgoClient's rollback byte-cleanup
// operation (see inline_images.go).
func (m *mockSftpgoClient) RemoveFile(ctx context.Context, accessToken, storageKey string) error {
	m.removeFileCalls = append(m.removeFileCalls, storageKey)
	m.removeFileCtxErrs = append(m.removeFileCtxErrs, ctx.Err())
	if m.removeFileFn != nil {
		return m.removeFileFn(ctx, accessToken, storageKey)
	}
	return nil
}

// UploadBytes satisfies inlineImageSftpgoClient (see inline_images.go) in
// addition to the read/write-share operations above, so this same mock
// serves both AttachmentStorageHandler's and InlineImageProcessor's tests.
func (m *mockSftpgoClient) UploadBytes(ctx context.Context, shareID, storageKey string, data []byte, contentType string) error {
	m.uploadBytesCalls = append(m.uploadBytesCalls, storageKey)
	if m.uploadBytesFn != nil {
		return m.uploadBytesFn(ctx, shareID, storageKey, data, contentType)
	}
	return nil
}

func (m *mockSftpgoClient) MintToken(ctx context.Context, email, jwtAssertion string) (*sftpgo.Token, error) {
	m.mintTokenCalls = append(m.mintTokenCalls, jwtAssertion)
	if m.mintTokenFn != nil {
		return m.mintTokenFn(ctx, email, jwtAssertion)
	}
	return &sftpgo.Token{AccessToken: "mock-access-token", ExpiresAt: json.RawMessage(`"2026-08-27T12:00:00Z"`)}, nil
}

func (m *mockSftpgoClient) CreateShare(ctx context.Context, accessToken, storageKey string, scope int, ttl time.Duration) (string, error) {
	m.createShareCalls = append(m.createShareCalls, storageKey)
	m.createShareScopes = append(m.createShareScopes, scope)
	if m.createShareFn != nil {
		return m.createShareFn(ctx, accessToken, storageKey, scope, ttl)
	}
	return "mock-share-id", nil
}

func (m *mockSftpgoClient) PublicShareURL(shareID string) string {
	if m.publicShareURL != nil {
		return m.publicShareURL(shareID)
	}
	return "https://share.example.com/web/client/pubshares/" + shareID + "?compress=false"
}

func (m *mockSftpgoClient) BaseURL() string {
	if m.baseURL != "" {
		return m.baseURL
	}
	return "https://sftpgo.example.com"
}

// ----- MintUploadToken -----

// validMintUploadTokenBody returns a well-formed POST
// /cases/{id}/attachments/upload-token request body: since MintUploadToken
// now creates the attachment's metadata row up front, the frontend must
// supply the file's name/mimeType/size before any bytes are uploaded.
func validMintUploadTokenBody() io.Reader {
	return strings.NewReader(`{"filename":"report.pdf","mimeType":"application/pdf","sizeBytes":1024}`)
}

// mockAttachmentID is the attachment id returned by a mock entity
// CreateCaseAttachment call configured for a successful pending-row create.
const mockAttachmentID = "33333333-3333-3333-3333-333333333333"

// createCaseAttachmentFnPending returns a mockEntityCaseClient.createCaseAttachmentFn
// that mimics the entity service's successful response to a "pending" status
// CreateCaseAttachment call, carrying mockAttachmentID as the created row's id.
func createCaseAttachmentFnPending(_ context.Context, _ []byte) ([]byte, error) {
	return []byte(`{"message":"Attachment created successfully","attachment":{"id":"` + mockAttachmentID + `","status":"pending"}}`), nil
}

func TestMintUploadTokenRequiresAuth(t *testing.T) {
	t.Parallel()
	h := NewAttachmentStorageHandler(&mockEntityCaseClient{}, &mockSftpgoClient{})

	req := httptest.NewRequest(http.MethodPost, "/cases/11111111-1111-1111-1111-111111111111/attachments/upload-token", nil)
	req.SetPathValue("id", "11111111-1111-1111-1111-111111111111")
	w := httptest.NewRecorder()

	h.MintUploadToken(w, req)

	assertStatus(t, w, http.StatusUnauthorized)
}

func TestMintUploadTokenRejectsInvalidCaseID(t *testing.T) {
	t.Parallel()
	h := NewAttachmentStorageHandler(&mockEntityCaseClient{}, &mockSftpgoClient{})

	req := withUser(httptest.NewRequest(http.MethodPost, "/cases/not-a-uuid/attachments/upload-token", nil))
	req.SetPathValue("id", "not-a-uuid")
	req.Header.Set("x-jwt-assertion", "raw-jwt")
	w := httptest.NewRecorder()

	h.MintUploadToken(w, req)

	assertStatus(t, w, http.StatusBadRequest)
}

// TestMintUploadTokenBlocksClosedCase verifies the ACL check runs BEFORE any
// token is minted: a closed case must produce a 409 and zero calls into the
// SFTPGo client.
func TestMintUploadTokenBlocksClosedCase(t *testing.T) {
	t.Parallel()
	entity := &mockEntityCaseClient{
		getCaseFn: func(ctx context.Context, caseID string) ([]byte, error) {
			return []byte(`{"state":"closed"}`), nil
		},
	}
	sftpgoMock := &mockSftpgoClient{}
	h := NewAttachmentStorageHandler(entity, sftpgoMock)

	caseID := "11111111-1111-1111-1111-111111111111"
	req := withUser(httptest.NewRequest(http.MethodPost, "/cases/"+caseID+"/attachments/upload-token", validMintUploadTokenBody()))
	req.SetPathValue("id", caseID)
	req.Header.Set("x-jwt-assertion", "raw-jwt")
	w := httptest.NewRecorder()

	h.MintUploadToken(w, req)

	assertStatus(t, w, http.StatusConflict)
	assertErrorMessage(t, w, ErrMsgAttachmentOnClosedCase)
	if len(sftpgoMock.mintTokenCalls) != 0 {
		t.Errorf("MintToken was called %d times; want 0 — a token must never be minted for a closed case", len(sftpgoMock.mintTokenCalls))
	}
}

// TestMintUploadTokenRequiresJWTAssertionHeader verifies the raw
// x-jwt-assertion header value (not some other credential) is what gets
// forwarded as the mint password, and that a request without it never
// reaches the SFTPGo client.
func TestMintUploadTokenRequiresJWTAssertionHeader(t *testing.T) {
	t.Parallel()
	entity := &mockEntityCaseClient{
		getCaseFn: func(ctx context.Context, caseID string) ([]byte, error) {
			return []byte(`{"state":"work_in_progress"}`), nil
		},
	}
	sftpgoMock := &mockSftpgoClient{}
	h := NewAttachmentStorageHandler(entity, sftpgoMock)

	caseID := "11111111-1111-1111-1111-111111111111"
	req := withUser(httptest.NewRequest(http.MethodPost, "/cases/"+caseID+"/attachments/upload-token", validMintUploadTokenBody()))
	req.SetPathValue("id", caseID)
	// Deliberately no x-jwt-assertion header set.
	w := httptest.NewRecorder()

	h.MintUploadToken(w, req)

	assertStatus(t, w, http.StatusUnauthorized)
	if len(sftpgoMock.mintTokenCalls) != 0 {
		t.Errorf("MintToken was called %d times; want 0", len(sftpgoMock.mintTokenCalls))
	}
}

// TestMintUploadTokenRejectsIncompleteBody verifies a request missing any of
// filename/mimeType/sizeBytes is rejected before any entity or SFTPGo call —
// this backend never sees the file's bytes, so these three fields are the
// only source of truth for the pending row's metadata.
func TestMintUploadTokenRejectsIncompleteBody(t *testing.T) {
	t.Parallel()
	entity := &mockEntityCaseClient{
		getCaseFn: func(ctx context.Context, caseID string) ([]byte, error) {
			return []byte(`{"state":"work_in_progress"}`), nil
		},
	}
	sftpgoMock := &mockSftpgoClient{}
	h := NewAttachmentStorageHandler(entity, sftpgoMock)

	caseID := "11111111-1111-1111-1111-111111111111"
	req := withUser(httptest.NewRequest(http.MethodPost, "/cases/"+caseID+"/attachments/upload-token",
		strings.NewReader(`{"filename":"report.pdf"}`)))
	req.SetPathValue("id", caseID)
	req.Header.Set("x-jwt-assertion", "raw-jwt")
	w := httptest.NewRecorder()

	h.MintUploadToken(w, req)

	assertStatus(t, w, http.StatusBadRequest)
	if len(sftpgoMock.mintTokenCalls) != 0 {
		t.Errorf("MintToken was called %d times; want 0 — an incomplete body must be rejected before any upstream call", len(sftpgoMock.mintTokenCalls))
	}
}

// TestMintUploadTokenSuccess verifies a successful mint on an open case
// forwards the exact x-jwt-assertion header value and returns the expected
// response shape.
func TestMintUploadTokenSuccess(t *testing.T) {
	t.Parallel()
	const caseID = "11111111-1111-1111-1111-111111111111"
	var createCaseAttachmentCalls int
	entity := &mockEntityCaseClient{
		getCaseFn: func(ctx context.Context, caseID string) ([]byte, error) {
			return []byte(`{"state":"work_in_progress"}`), nil
		},
		createCaseAttachmentFn: func(ctx context.Context, body []byte) ([]byte, error) {
			createCaseAttachmentCalls++
			var req struct {
				ReferenceID   string `json:"referenceId"`
				ReferenceType string `json:"referenceType"`
				Name          string `json:"name"`
				Type          string `json:"type"`
				StorageKey    string `json:"storageKey"`
				SizeBytes     int    `json:"sizeBytes"`
				Status        string `json:"status"`
			}
			if err := json.Unmarshal(body, &req); err != nil {
				t.Fatalf("decode CreateCaseAttachment body: %v; raw: %s", err, body)
			}
			if req.ReferenceID != caseID {
				t.Errorf("CreateCaseAttachment referenceId = %q, want %q", req.ReferenceID, caseID)
			}
			if req.ReferenceType != "case" {
				t.Errorf("CreateCaseAttachment referenceType = %q, want %q", req.ReferenceType, "case")
			}
			if req.Name != "report.pdf" {
				t.Errorf("CreateCaseAttachment name = %q, want %q", req.Name, "report.pdf")
			}
			if req.Type != "application/pdf" {
				t.Errorf("CreateCaseAttachment type = %q, want %q", req.Type, "application/pdf")
			}
			if req.SizeBytes != 1024 {
				t.Errorf("CreateCaseAttachment sizeBytes = %d, want 1024", req.SizeBytes)
			}
			if req.Status != "pending" {
				t.Errorf("CreateCaseAttachment status = %q, want %q — the row must be created pending, before the share is minted", req.Status, "pending")
			}
			if req.StorageKey == "" {
				t.Errorf("CreateCaseAttachment storageKey was empty")
			}
			return createCaseAttachmentFnPending(ctx, body)
		},
	}
	sftpgoMock := &mockSftpgoClient{baseURL: "https://sftpgo.example.com"}
	h := NewAttachmentStorageHandler(entity, sftpgoMock)

	req := withUser(httptest.NewRequest(http.MethodPost, "/cases/"+caseID+"/attachments/upload-token", validMintUploadTokenBody()))
	req.SetPathValue("id", caseID)
	req.Header.Set("x-jwt-assertion", "the-raw-jwt-assertion")
	w := httptest.NewRecorder()

	h.MintUploadToken(w, req)

	assertStatus(t, w, http.StatusOK)
	if createCaseAttachmentCalls != 1 {
		t.Fatalf("CreateCaseAttachment was called %d times; want 1", createCaseAttachmentCalls)
	}
	if len(sftpgoMock.mintTokenCalls) != 1 || sftpgoMock.mintTokenCalls[0] != "the-raw-jwt-assertion" {
		t.Fatalf("mintTokenCalls = %v, want exactly one call with the raw x-jwt-assertion value", sftpgoMock.mintTokenCalls)
	}

	rawBody := w.Body.String()
	resp := decodeJSON[uploadTokenResponse](t, w)
	if resp.ID != mockAttachmentID {
		t.Errorf("ID = %q, want %q — the id of the pending row CreateCaseAttachment created", resp.ID, mockAttachmentID)
	}
	if resp.ShareID != "mock-share-id" {
		t.Errorf("ShareID = %q, want mock-share-id", resp.ShareID)
	}
	if resp.SftpgoBaseURL != "https://sftpgo.example.com" {
		t.Errorf("SftpgoBaseURL = %q, want https://sftpgo.example.com", resp.SftpgoBaseURL)
	}
	// The share must be scoped to storageKey's parent directory — the
	// attachment's OWN directory under the new nested layout, NOT the whole
	// case's directory — confirmed against a real SFTPGo instance that a
	// share scoped to the exact file makes every shares-chunked-uploads call
	// against it fail (see MintUploadToken's doc comment on shareDir).
	wantShareDir := path.Dir(resp.StorageKey)
	if len(sftpgoMock.createShareCalls) != 1 || sftpgoMock.createShareCalls[0] != wantShareDir {
		t.Fatalf("createShareCalls = %v, want exactly [%q] (storageKey's parent directory)", sftpgoMock.createShareCalls, wantShareDir)
	}
	if len(sftpgoMock.createShareScopes) != 1 || sftpgoMock.createShareScopes[0] != sftpgo.ShareScopeWrite {
		t.Errorf("createShareScopes = %v, want exactly [%d] (write)", sftpgoMock.createShareScopes, sftpgo.ShareScopeWrite)
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(rawBody), &raw); err != nil {
		t.Fatalf("decode raw response: %v; raw: %s", err, rawBody)
	}
	if _, hasToken := raw["sftpgoAccessToken"]; hasToken {
		t.Errorf("response body carried sftpgoAccessToken, want no bearer credential exposed to the frontend")
	}
	if _, hasExpiry := raw["expiresAt"]; hasExpiry {
		t.Errorf("response body carried expiresAt, want none — the share's own server-side expiry is not surfaced to the frontend")
	}
	// The case fixture above carries no "projectId", so this must fall back
	// to the project-less path shape rather than emitting a malformed
	// "project-" segment.
	wantKey := "/attachments/cases/" + caseID + "/"
	if !strings.HasPrefix(resp.StorageKey, wantKey) {
		t.Errorf("StorageKey = %q, want prefix %q (no-project fallback)", resp.StorageKey, wantKey)
	}
	// The path must be "<attachmentId>/<original filename>" — the attachment
	// id is now a directory, and the leaf is the real filename — so SFTPGo's
	// path.Base()-derived Content-Disposition filename on download is the
	// real name, not a UUID.
	rest := strings.TrimPrefix(resp.StorageKey, wantKey)
	if !strings.HasSuffix(rest, "/report.pdf") {
		t.Errorf("StorageKey rest = %q, want suffix %q (original filename preserved as the leaf)", rest, "/report.pdf")
	}
	attachmentID := strings.TrimSuffix(rest, "/report.pdf")
	if !uuidRe.MatchString(attachmentID) {
		t.Errorf("StorageKey %q does not carry a well-formed UUID directory, got %q", resp.StorageKey, attachmentID)
	}
}

// TestMintUploadTokenStorageKeyIncludesProject verifies that when the case's
// own record carries a projectId, the minted storageKey follows the
// documented convention:
// /attachments/project-<projectId>/cases/<caseId>/<attachmentId>/<filename>.
func TestMintUploadTokenStorageKeyIncludesProject(t *testing.T) {
	t.Parallel()
	const projectID = "22222222-2222-2222-2222-222222222222"
	entity := &mockEntityCaseClient{
		getCaseFn: func(ctx context.Context, caseID string) ([]byte, error) {
			return []byte(`{"state":"work_in_progress","projectId":"` + projectID + `"}`), nil
		},
		createCaseAttachmentFn: createCaseAttachmentFnPending,
	}
	sftpgoMock := &mockSftpgoClient{}
	h := NewAttachmentStorageHandler(entity, sftpgoMock)

	caseID := "11111111-1111-1111-1111-111111111111"
	req := withUser(httptest.NewRequest(http.MethodPost, "/cases/"+caseID+"/attachments/upload-token", validMintUploadTokenBody()))
	req.SetPathValue("id", caseID)
	req.Header.Set("x-jwt-assertion", "raw-jwt")
	w := httptest.NewRecorder()

	h.MintUploadToken(w, req)

	assertStatus(t, w, http.StatusOK)
	resp := decodeJSON[uploadTokenResponse](t, w)

	wantPrefix := "/attachments/project-" + projectID + "/cases/" + caseID + "/"
	if !strings.HasPrefix(resp.StorageKey, wantPrefix) {
		t.Fatalf("StorageKey = %q, want prefix %q", resp.StorageKey, wantPrefix)
	}
	rest := strings.TrimPrefix(resp.StorageKey, wantPrefix)
	if !strings.HasSuffix(rest, "/report.pdf") {
		t.Errorf("StorageKey rest = %q, want suffix %q (original filename preserved as the leaf)", rest, "/report.pdf")
	}
	attachmentID := strings.TrimSuffix(rest, "/report.pdf")
	if !uuidRe.MatchString(attachmentID) {
		t.Errorf("StorageKey %q does not carry a well-formed UUID directory, got %q", resp.StorageKey, attachmentID)
	}
}

// TestMintUploadTokenStorageKeyUniquePerCall verifies each mint generates a
// fresh attachment id, never reusing one across calls.
func TestMintUploadTokenStorageKeyUniquePerCall(t *testing.T) {
	t.Parallel()
	entity := &mockEntityCaseClient{
		getCaseFn: func(ctx context.Context, caseID string) ([]byte, error) {
			return []byte(`{"state":"work_in_progress"}`), nil
		},
		createCaseAttachmentFn: createCaseAttachmentFnPending,
	}
	h := NewAttachmentStorageHandler(entity, &mockSftpgoClient{})

	caseID := "11111111-1111-1111-1111-111111111111"
	seen := make(map[string]bool)
	for i := 0; i < 5; i++ {
		req := withUser(httptest.NewRequest(http.MethodPost, "/cases/"+caseID+"/attachments/upload-token", validMintUploadTokenBody()))
		req.SetPathValue("id", caseID)
		req.Header.Set("x-jwt-assertion", "raw-jwt")
		w := httptest.NewRecorder()
		h.MintUploadToken(w, req)
		assertStatus(t, w, http.StatusOK)
		resp := decodeJSON[uploadTokenResponse](t, w)
		if seen[resp.StorageKey] {
			t.Fatalf("StorageKey %q was generated twice", resp.StorageKey)
		}
		seen[resp.StorageKey] = true
	}
}

func TestMintUploadTokenPropagatesSftpgoFailure(t *testing.T) {
	t.Parallel()
	entity := &mockEntityCaseClient{
		getCaseFn: func(ctx context.Context, caseID string) ([]byte, error) {
			return []byte(`{"state":"work_in_progress"}`), nil
		},
	}
	sftpgoMock := &mockSftpgoClient{
		mintTokenFn: func(ctx context.Context, email, jwtAssertion string) (*sftpgo.Token, error) {
			return nil, &apierror.Error{StatusCode: http.StatusUnauthorized, Body: "denied"}
		},
	}
	h := NewAttachmentStorageHandler(entity, sftpgoMock)

	caseID := "11111111-1111-1111-1111-111111111111"
	req := withUser(httptest.NewRequest(http.MethodPost, "/cases/"+caseID+"/attachments/upload-token", validMintUploadTokenBody()))
	req.SetPathValue("id", caseID)
	req.Header.Set("x-jwt-assertion", "raw-jwt")
	w := httptest.NewRecorder()

	h.MintUploadToken(w, req)

	assertStatus(t, w, http.StatusBadGateway)
}

// TestMintUploadTokenPropagatesCreateShareFailure verifies a failure from the
// write-share creation call (as opposed to the token mint) also surfaces as
// a 502, and that the response never leaks a partially-built token/share.
func TestMintUploadTokenPropagatesCreateShareFailure(t *testing.T) {
	t.Parallel()
	entity := &mockEntityCaseClient{
		getCaseFn: func(ctx context.Context, caseID string) ([]byte, error) {
			return []byte(`{"state":"work_in_progress"}`), nil
		},
		createCaseAttachmentFn: createCaseAttachmentFnPending,
	}
	sftpgoMock := &mockSftpgoClient{
		createShareFn: func(ctx context.Context, accessToken, storageKey string, scope int, ttl time.Duration) (string, error) {
			return "", &apierror.Error{StatusCode: http.StatusInternalServerError, Body: "boom"}
		},
	}
	h := NewAttachmentStorageHandler(entity, sftpgoMock)

	caseID := "11111111-1111-1111-1111-111111111111"
	req := withUser(httptest.NewRequest(http.MethodPost, "/cases/"+caseID+"/attachments/upload-token", validMintUploadTokenBody()))
	req.SetPathValue("id", caseID)
	req.Header.Set("x-jwt-assertion", "raw-jwt")
	w := httptest.NewRecorder()

	h.MintUploadToken(w, req)

	assertStatus(t, w, http.StatusBadGateway)
	if len(sftpgoMock.mintTokenCalls) != 1 {
		t.Errorf("MintToken was called %d times; want 1 (still needed to authenticate the CreateShare call)", len(sftpgoMock.mintTokenCalls))
	}
}

// TestMintUploadTokenCreatesPendingRowBeforeShare verifies the call order
// that closes the reliability gap this change exists for: the entity
// service's CreateCaseAttachment (with status "pending") must be called
// strictly before SFTPGo's CreateShare, recorded here via a single shared,
// ordered call log both mocks append to.
func TestMintUploadTokenCreatesPendingRowBeforeShare(t *testing.T) {
	t.Parallel()
	var calls []string
	entity := &mockEntityCaseClient{
		getCaseFn: func(ctx context.Context, caseID string) ([]byte, error) {
			return []byte(`{"state":"work_in_progress"}`), nil
		},
		createCaseAttachmentFn: func(ctx context.Context, body []byte) ([]byte, error) {
			calls = append(calls, "createCaseAttachment")
			return createCaseAttachmentFnPending(ctx, body)
		},
	}
	sftpgoMock := &mockSftpgoClient{
		createShareFn: func(ctx context.Context, accessToken, storageKey string, scope int, ttl time.Duration) (string, error) {
			calls = append(calls, "createShare")
			return "mock-share-id", nil
		},
	}
	h := NewAttachmentStorageHandler(entity, sftpgoMock)

	caseID := "11111111-1111-1111-1111-111111111111"
	req := withUser(httptest.NewRequest(http.MethodPost, "/cases/"+caseID+"/attachments/upload-token", validMintUploadTokenBody()))
	req.SetPathValue("id", caseID)
	req.Header.Set("x-jwt-assertion", "raw-jwt")
	w := httptest.NewRecorder()

	h.MintUploadToken(w, req)

	assertStatus(t, w, http.StatusOK)
	if got := strings.Join(calls, ","); got != "createCaseAttachment,createShare" {
		t.Fatalf("call order = %q, want %q — the pending row must be created before the share is minted", got, "createCaseAttachment,createShare")
	}
}

// TestMintUploadTokenSkipsShareWhenEntityCreateFails verifies that when the
// entity service's CreateCaseAttachment call fails, MintUploadToken returns
// an error and never mints a share: a share with no corresponding CSM
// metadata row would recreate the exact orphan-upload gap this design
// change closes.
func TestMintUploadTokenSkipsShareWhenEntityCreateFails(t *testing.T) {
	t.Parallel()
	entity := &mockEntityCaseClient{
		getCaseFn: func(ctx context.Context, caseID string) ([]byte, error) {
			return []byte(`{"state":"work_in_progress"}`), nil
		},
		createCaseAttachmentFn: func(ctx context.Context, body []byte) ([]byte, error) {
			return nil, &apierror.Error{StatusCode: http.StatusInternalServerError, Body: "boom"}
		},
	}
	sftpgoMock := &mockSftpgoClient{}
	h := NewAttachmentStorageHandler(entity, sftpgoMock)

	caseID := "11111111-1111-1111-1111-111111111111"
	req := withUser(httptest.NewRequest(http.MethodPost, "/cases/"+caseID+"/attachments/upload-token", validMintUploadTokenBody()))
	req.SetPathValue("id", caseID)
	req.Header.Set("x-jwt-assertion", "raw-jwt")
	w := httptest.NewRecorder()

	h.MintUploadToken(w, req)

	assertStatus(t, w, http.StatusInternalServerError)
	if len(sftpgoMock.createShareCalls) != 0 {
		t.Errorf("CreateShare was called %d times; want 0 — a share must never be minted when the pending row failed to create", len(sftpgoMock.createShareCalls))
	}
	// MintToken (the sftpgo access-token mint used to authenticate the
	// CreateShare call) may or may not have already run by this point in the
	// call sequence; what matters is that CreateShare itself never fires.
}

// TestMintUploadTokenExplicitCaseReferenceType verifies referenceType "case"
// sent explicitly behaves exactly like the default: the case write guard
// runs and the persisted row carries referenceType "case".
func TestMintUploadTokenExplicitCaseReferenceType(t *testing.T) {
	t.Parallel()
	const caseID = "11111111-1111-1111-1111-111111111111"
	var getCaseCalls int
	var persistedReferenceType string
	entity := &mockEntityCaseClient{
		getCaseFn: func(ctx context.Context, id string) ([]byte, error) {
			getCaseCalls++
			return []byte(`{"state":"work_in_progress"}`), nil
		},
		createCaseAttachmentFn: func(ctx context.Context, body []byte) ([]byte, error) {
			var req struct {
				ReferenceID   string `json:"referenceId"`
				ReferenceType string `json:"referenceType"`
			}
			if err := json.Unmarshal(body, &req); err != nil {
				t.Fatalf("decode CreateCaseAttachment body: %v; raw: %s", err, body)
			}
			persistedReferenceType = req.ReferenceType
			if req.ReferenceID != caseID {
				t.Errorf("referenceId = %q, want %q", req.ReferenceID, caseID)
			}
			return createCaseAttachmentFnPending(ctx, body)
		},
	}
	sftpgoMock := &mockSftpgoClient{}
	h := NewAttachmentStorageHandler(entity, sftpgoMock)

	body := strings.NewReader(`{"filename":"report.pdf","mimeType":"application/pdf","sizeBytes":1024,"referenceType":"case"}`)
	req := withUser(httptest.NewRequest(http.MethodPost, "/cases/"+caseID+"/attachments/upload-token", body))
	req.SetPathValue("id", caseID)
	req.Header.Set("x-jwt-assertion", "raw-jwt")
	w := httptest.NewRecorder()

	h.MintUploadToken(w, req)

	assertStatus(t, w, http.StatusOK)
	if getCaseCalls != 1 {
		t.Errorf("GetCase (case write guard) was called %d times; want 1", getCaseCalls)
	}
	if persistedReferenceType != "case" {
		t.Errorf("persisted referenceType = %q, want %q", persistedReferenceType, "case")
	}
}

// TestMintUploadTokenChangeRequestReference verifies the mint endpoint's
// change_request handling: the caller's access to the change request is
// checked first (an inaccessible CR yields the upstream's own 403/404, never
// a capability hint), and an accessible one yields a clear 422 — the
// direct-upload storage mode cannot persist CR attachments yet — with no row
// created and no SFTPGo call made.
func TestMintUploadTokenChangeRequestReference(t *testing.T) {
	t.Parallel()
	const crID = "44444444-4444-4444-4444-444444444444"

	newRequest := func() (*http.Request, *httptest.ResponseRecorder) {
		body := strings.NewReader(`{"filename":"report.pdf","mimeType":"application/pdf","sizeBytes":1024,"referenceType":"change_request"}`)
		req := withUser(httptest.NewRequest(http.MethodPost, "/cases/"+crID+"/attachments/upload-token", body))
		req.SetPathValue("id", crID)
		req.Header.Set("x-jwt-assertion", "raw-jwt")
		return req, httptest.NewRecorder()
	}

	t.Run("accessible CR yields 422 unsupported", func(t *testing.T) {
		t.Parallel()
		var crLookups []string
		var createCalls int
		entity := &mockEntityCaseClient{
			getChangeRequestFn: func(ctx context.Context, id string) ([]byte, error) {
				crLookups = append(crLookups, id)
				return []byte(`{}`), nil
			},
			createCaseAttachmentFn: func(ctx context.Context, body []byte) ([]byte, error) {
				createCalls++
				return createCaseAttachmentFnPending(ctx, body)
			},
			getCaseFn: func(ctx context.Context, id string) ([]byte, error) {
				t.Error("GetCase must not be called for a change_request reference")
				return []byte(`{}`), nil
			},
		}
		sftpgoMock := &mockSftpgoClient{}
		h := NewAttachmentStorageHandler(entity, sftpgoMock)
		req, w := newRequest()

		h.MintUploadToken(w, req)

		assertStatus(t, w, http.StatusUnprocessableEntity)
		assertErrorMessage(t, w, ErrMsgAttachmentStorageUnsupportedRef)
		if len(crLookups) != 1 || crLookups[0] != crID {
			t.Errorf("GetChangeRequest lookups = %v, want exactly [%q]", crLookups, crID)
		}
		if createCalls != 0 {
			t.Errorf("CreateCaseAttachment was called %d times; want 0", createCalls)
		}
		if len(sftpgoMock.mintTokenCalls) != 0 || len(sftpgoMock.createShareCalls) != 0 {
			t.Errorf("sftpgo was called (mint=%d, share=%d); want 0", len(sftpgoMock.mintTokenCalls), len(sftpgoMock.createShareCalls))
		}
	})

	t.Run("inaccessible CR yields upstream 404", func(t *testing.T) {
		t.Parallel()
		entity := &mockEntityCaseClient{
			getChangeRequestFn: func(ctx context.Context, id string) ([]byte, error) {
				return nil, &apierror.Error{StatusCode: http.StatusNotFound, Body: "not found"}
			},
		}
		sftpgoMock := &mockSftpgoClient{}
		h := NewAttachmentStorageHandler(entity, sftpgoMock)
		req, w := newRequest()

		h.MintUploadToken(w, req)

		assertStatus(t, w, http.StatusNotFound)
	})
}

// TestMintUploadTokenIncidentReference mirrors the change_request behavior
// for referenceType "incident": access checked via GetIncident, then a clear
// 422 for the unsupported storage mode.
func TestMintUploadTokenIncidentReference(t *testing.T) {
	t.Parallel()
	const incidentID = "55555555-5555-5555-5555-555555555555"
	var incidentLookups []string
	entity := &mockEntityCaseClient{
		getIncidentFn: func(ctx context.Context, id string) ([]byte, error) {
			incidentLookups = append(incidentLookups, id)
			return []byte(`{}`), nil
		},
	}
	sftpgoMock := &mockSftpgoClient{}
	h := NewAttachmentStorageHandler(entity, sftpgoMock)

	body := strings.NewReader(`{"filename":"report.pdf","mimeType":"application/pdf","sizeBytes":1024,"referenceType":"incident"}`)
	req := withUser(httptest.NewRequest(http.MethodPost, "/cases/"+incidentID+"/attachments/upload-token", body))
	req.SetPathValue("id", incidentID)
	req.Header.Set("x-jwt-assertion", "raw-jwt")
	w := httptest.NewRecorder()

	h.MintUploadToken(w, req)

	assertStatus(t, w, http.StatusUnprocessableEntity)
	assertErrorMessage(t, w, ErrMsgAttachmentStorageUnsupportedRef)
	if len(incidentLookups) != 1 || incidentLookups[0] != incidentID {
		t.Errorf("GetIncident lookups = %v, want exactly [%q]", incidentLookups, incidentID)
	}
}

// TestMintUploadTokenRejectsUnknownReferenceType verifies an unrecognised
// referenceType is rejected with 400 before any upstream call.
func TestMintUploadTokenRejectsUnknownReferenceType(t *testing.T) {
	t.Parallel()
	const id = "11111111-1111-1111-1111-111111111111"
	entity := &mockEntityCaseClient{
		getCaseFn: func(ctx context.Context, caseID string) ([]byte, error) {
			t.Error("GetCase must not be called for an unrecognised reference type")
			return []byte(`{}`), nil
		},
	}
	sftpgoMock := &mockSftpgoClient{}
	h := NewAttachmentStorageHandler(entity, sftpgoMock)

	body := strings.NewReader(`{"filename":"report.pdf","mimeType":"application/pdf","sizeBytes":1024,"referenceType":"deployment"}`)
	req := withUser(httptest.NewRequest(http.MethodPost, "/cases/"+id+"/attachments/upload-token", body))
	req.SetPathValue("id", id)
	req.Header.Set("x-jwt-assertion", "raw-jwt")
	w := httptest.NewRecorder()

	h.MintUploadToken(w, req)

	assertStatus(t, w, http.StatusBadRequest)
	if len(sftpgoMock.mintTokenCalls) != 0 {
		t.Errorf("MintToken was called %d times; want 0", len(sftpgoMock.mintTokenCalls))
	}
}

// ----- CreateAttachmentShare -----

func TestCreateAttachmentShareRequiresAuth(t *testing.T) {
	t.Parallel()
	h := NewAttachmentStorageHandler(&mockEntityCaseClient{}, &mockSftpgoClient{})

	req := httptest.NewRequest(http.MethodPost, "/attachments/11111111-1111-1111-1111-111111111111/share", nil)
	req.SetPathValue("id", "11111111-1111-1111-1111-111111111111")
	w := httptest.NewRecorder()

	h.CreateAttachmentShare(w, req)

	assertStatus(t, w, http.StatusUnauthorized)
}

func TestCreateAttachmentShareRejectsInvalidAttachmentID(t *testing.T) {
	t.Parallel()
	h := NewAttachmentStorageHandler(&mockEntityCaseClient{}, &mockSftpgoClient{})

	req := withUser(httptest.NewRequest(http.MethodPost, "/attachments/not-a-uuid/share", nil))
	req.SetPathValue("id", "not-a-uuid")
	w := httptest.NewRecorder()

	h.CreateAttachmentShare(w, req)

	assertStatus(t, w, http.StatusBadRequest)
}

// TestCreateAttachmentShareEnforcesReadAccessBeforeMinting verifies the case
// read-access check runs, and fails, BEFORE any SFTPGo call is made.
func TestCreateAttachmentShareEnforcesReadAccessBeforeMinting(t *testing.T) {
	t.Parallel()
	storageKey := "/attachments/att-1"
	caseID := "22222222-2222-2222-2222-222222222222"
	entity := &mockEntityCaseClient{
		getCaseAttachmentFn: func(ctx context.Context, attachmentID string) ([]byte, error) {
			return []byte(`{"referenceId":"` + caseID + `","referenceType":"case","storageKey":"` + storageKey + `"}`), nil
		},
		getCaseFn: func(ctx context.Context, id string) ([]byte, error) {
			return nil, &apierror.Error{StatusCode: http.StatusNotFound, Body: "not found"}
		},
	}
	sftpgoMock := &mockSftpgoClient{}
	h := NewAttachmentStorageHandler(entity, sftpgoMock)

	attachmentID := "11111111-1111-1111-1111-111111111111"
	req := withUser(httptest.NewRequest(http.MethodPost, "/attachments/"+attachmentID+"/share", nil))
	req.SetPathValue("id", attachmentID)
	req.Header.Set("x-jwt-assertion", "raw-jwt")
	w := httptest.NewRecorder()

	h.CreateAttachmentShare(w, req)

	assertStatus(t, w, http.StatusNotFound)
	if len(sftpgoMock.mintTokenCalls) != 0 || len(sftpgoMock.createShareCalls) != 0 {
		t.Errorf("sftpgo was called (mint=%d, share=%d); want 0 — access must be checked before minting/sharing",
			len(sftpgoMock.mintTokenCalls), len(sftpgoMock.createShareCalls))
	}
}

// TestCreateAttachmentShareDeniesEmptyReferenceType verifies the fail-closed
// authorization: an attachment whose details carry NO referenceType (as
// reported for attachments on the data source that predates the field) must
// be denied outright — never assumed to be a case — and nothing may reach
// the SFTPGo client. This closes the bypass where an unpopulated
// referenceType skipped the read-access check entirely and let any
// authenticated user mint a public share URL for any attachment id.
func TestCreateAttachmentShareDeniesEmptyReferenceType(t *testing.T) {
	t.Parallel()
	var getCaseCalls int
	entity := &mockEntityCaseClient{
		getCaseAttachmentFn: func(ctx context.Context, attachmentID string) ([]byte, error) {
			return []byte(`{"referenceId":"22222222-2222-2222-2222-222222222222","storageKey":"/attachments/att-1"}`), nil
		},
		getCaseFn: func(ctx context.Context, id string) ([]byte, error) {
			getCaseCalls++
			return []byte(`{}`), nil
		},
	}
	sftpgoMock := &mockSftpgoClient{}
	h := NewAttachmentStorageHandler(entity, sftpgoMock)

	attachmentID := "11111111-1111-1111-1111-111111111111"
	req := withUser(httptest.NewRequest(http.MethodPost, "/attachments/"+attachmentID+"/share", nil))
	req.SetPathValue("id", attachmentID)
	req.Header.Set("x-jwt-assertion", "raw-jwt")
	w := httptest.NewRecorder()

	h.CreateAttachmentShare(w, req)

	assertStatus(t, w, http.StatusNotFound)
	if getCaseCalls != 0 {
		t.Errorf("GetCase was called %d times; want 0 — an empty referenceType must never be treated as a case", getCaseCalls)
	}
	if len(sftpgoMock.mintTokenCalls) != 0 || len(sftpgoMock.createShareCalls) != 0 {
		t.Errorf("sftpgo was called (mint=%d, share=%d); want 0 — an unverifiable reference must fail closed",
			len(sftpgoMock.mintTokenCalls), len(sftpgoMock.createShareCalls))
	}
}

// TestCreateAttachmentShareDeniesUnrecognisedReferenceType verifies a
// reference type this handler has no access check for is denied rather than
// let through unauthorized.
func TestCreateAttachmentShareDeniesUnrecognisedReferenceType(t *testing.T) {
	t.Parallel()
	entity := &mockEntityCaseClient{
		getCaseAttachmentFn: func(ctx context.Context, attachmentID string) ([]byte, error) {
			return []byte(`{"referenceId":"22222222-2222-2222-2222-222222222222","referenceType":"deployment","storageKey":"/attachments/att-1"}`), nil
		},
	}
	sftpgoMock := &mockSftpgoClient{}
	h := NewAttachmentStorageHandler(entity, sftpgoMock)

	attachmentID := "11111111-1111-1111-1111-111111111111"
	req := withUser(httptest.NewRequest(http.MethodPost, "/attachments/"+attachmentID+"/share", nil))
	req.SetPathValue("id", attachmentID)
	req.Header.Set("x-jwt-assertion", "raw-jwt")
	w := httptest.NewRecorder()

	h.CreateAttachmentShare(w, req)

	assertStatus(t, w, http.StatusNotFound)
	if len(sftpgoMock.mintTokenCalls) != 0 || len(sftpgoMock.createShareCalls) != 0 {
		t.Errorf("sftpgo was called (mint=%d, share=%d); want 0", len(sftpgoMock.mintTokenCalls), len(sftpgoMock.createShareCalls))
	}
}

// TestCreateAttachmentShareChangeRequestReference verifies a change_request
// attachment's read check goes through GetChangeRequest (not GetCase), both
// denying on upstream 403 and proceeding on success.
func TestCreateAttachmentShareChangeRequestReference(t *testing.T) {
	t.Parallel()
	crID := "22222222-2222-2222-2222-222222222222"
	attachmentID := "11111111-1111-1111-1111-111111111111"

	newRequest := func() (*http.Request, *httptest.ResponseRecorder) {
		req := withUser(httptest.NewRequest(http.MethodPost, "/attachments/"+attachmentID+"/share", nil))
		req.SetPathValue("id", attachmentID)
		req.Header.Set("x-jwt-assertion", "raw-jwt")
		return req, httptest.NewRecorder()
	}
	newEntity := func(getCR func(ctx context.Context, id string) ([]byte, error)) *mockEntityCaseClient {
		return &mockEntityCaseClient{
			getCaseAttachmentFn: func(ctx context.Context, attachmentID string) ([]byte, error) {
				return []byte(`{"referenceId":"` + crID + `","referenceType":"change_request","storageKey":"/attachments/att-1"}`), nil
			},
			getCaseFn: func(ctx context.Context, id string) ([]byte, error) {
				t.Error("GetCase must not be called for a change_request reference")
				return []byte(`{}`), nil
			},
			getChangeRequestFn: getCR,
		}
	}

	t.Run("denied on upstream 403", func(t *testing.T) {
		t.Parallel()
		sftpgoMock := &mockSftpgoClient{}
		h := NewAttachmentStorageHandler(newEntity(func(ctx context.Context, id string) ([]byte, error) {
			return nil, &apierror.Error{StatusCode: http.StatusForbidden, Body: "forbidden"}
		}), sftpgoMock)
		req, w := newRequest()

		h.CreateAttachmentShare(w, req)

		assertStatus(t, w, http.StatusForbidden)
		if len(sftpgoMock.createShareCalls) != 0 {
			t.Errorf("createShareCalls = %v, want 0", sftpgoMock.createShareCalls)
		}
	})

	t.Run("allowed when readable", func(t *testing.T) {
		t.Parallel()
		var crLookups []string
		sftpgoMock := &mockSftpgoClient{}
		h := NewAttachmentStorageHandler(newEntity(func(ctx context.Context, id string) ([]byte, error) {
			crLookups = append(crLookups, id)
			return []byte(`{}`), nil
		}), sftpgoMock)
		req, w := newRequest()

		h.CreateAttachmentShare(w, req)

		assertStatus(t, w, http.StatusCreated)
		if len(crLookups) != 1 || crLookups[0] != crID {
			t.Errorf("GetChangeRequest lookups = %v, want exactly [%q]", crLookups, crID)
		}
	})
}

// TestCreateAttachmentShareIncidentReferenceDenied verifies an incident
// attachment's read check goes through GetIncident and denies on upstream
// 404.
func TestCreateAttachmentShareIncidentReferenceDenied(t *testing.T) {
	t.Parallel()
	incidentID := "22222222-2222-2222-2222-222222222222"
	entity := &mockEntityCaseClient{
		getCaseAttachmentFn: func(ctx context.Context, attachmentID string) ([]byte, error) {
			return []byte(`{"referenceId":"` + incidentID + `","referenceType":"incident","storageKey":"/attachments/att-1"}`), nil
		},
		getIncidentFn: func(ctx context.Context, id string) ([]byte, error) {
			return nil, &apierror.Error{StatusCode: http.StatusNotFound, Body: "not found"}
		},
	}
	sftpgoMock := &mockSftpgoClient{}
	h := NewAttachmentStorageHandler(entity, sftpgoMock)

	attachmentID := "11111111-1111-1111-1111-111111111111"
	req := withUser(httptest.NewRequest(http.MethodPost, "/attachments/"+attachmentID+"/share", nil))
	req.SetPathValue("id", attachmentID)
	req.Header.Set("x-jwt-assertion", "raw-jwt")
	w := httptest.NewRecorder()

	h.CreateAttachmentShare(w, req)

	assertStatus(t, w, http.StatusNotFound)
	if len(sftpgoMock.mintTokenCalls) != 0 {
		t.Errorf("MintToken was called %d times; want 0", len(sftpgoMock.mintTokenCalls))
	}
}

// TestCreateAttachmentShareRejectsMissingStorageKey verifies an attachment
// with no storageKey (e.g. stored the old, non-SFTPGo way) fails cleanly
// rather than attempting to share an empty path.
func TestCreateAttachmentShareRejectsMissingStorageKey(t *testing.T) {
	t.Parallel()
	caseID := "22222222-2222-2222-2222-222222222222"
	entity := &mockEntityCaseClient{
		getCaseAttachmentFn: func(ctx context.Context, attachmentID string) ([]byte, error) {
			return []byte(`{"referenceId":"` + caseID + `","referenceType":"case"}`), nil
		},
		getCaseFn: func(ctx context.Context, id string) ([]byte, error) {
			return []byte(`{"state":"work_in_progress"}`), nil
		},
	}
	sftpgoMock := &mockSftpgoClient{}
	h := NewAttachmentStorageHandler(entity, sftpgoMock)

	attachmentID := "11111111-1111-1111-1111-111111111111"
	req := withUser(httptest.NewRequest(http.MethodPost, "/attachments/"+attachmentID+"/share", nil))
	req.SetPathValue("id", attachmentID)
	req.Header.Set("x-jwt-assertion", "raw-jwt")
	w := httptest.NewRecorder()

	h.CreateAttachmentShare(w, req)

	assertStatus(t, w, http.StatusConflict)
	assertErrorMessage(t, w, ErrMsgAttachmentNotShareable)
	if len(sftpgoMock.mintTokenCalls) != 0 {
		t.Errorf("MintToken was called %d times; want 0", len(sftpgoMock.mintTokenCalls))
	}
}

// TestCreateAttachmentShareSuccess verifies the happy path: the storage key
// is forwarded to CreateShare verbatim, and the response carries the URL
// built from PublicShareURL(shareID) — never a hand-rolled URL.
func TestCreateAttachmentShareSuccess(t *testing.T) {
	t.Parallel()
	caseID := "22222222-2222-2222-2222-222222222222"
	storageKey := "/attachments/att-1"
	entity := &mockEntityCaseClient{
		getCaseAttachmentFn: func(ctx context.Context, attachmentID string) ([]byte, error) {
			return []byte(`{"referenceId":"` + caseID + `","referenceType":"case","storageKey":"` + storageKey + `"}`), nil
		},
		getCaseFn: func(ctx context.Context, id string) ([]byte, error) {
			return []byte(`{"state":"closed"}`), nil // reads are allowed even on a closed case
		},
	}
	sftpgoMock := &mockSftpgoClient{
		createShareFn: func(ctx context.Context, accessToken, gotStorageKey string, scope int, ttl time.Duration) (string, error) {
			if ttl != shareTTL {
				t.Errorf("ttl = %v, want %v", ttl, shareTTL)
			}
			if scope != sftpgo.ShareScopeRead {
				t.Errorf("scope = %d, want %d (read) — this download-share path must stay read-only", scope, sftpgo.ShareScopeRead)
			}
			return "share-xyz", nil
		},
	}
	h := NewAttachmentStorageHandler(entity, sftpgoMock)

	attachmentID := "11111111-1111-1111-1111-111111111111"
	req := withUser(httptest.NewRequest(http.MethodPost, "/attachments/"+attachmentID+"/share", nil))
	req.SetPathValue("id", attachmentID)
	req.Header.Set("x-jwt-assertion", "raw-jwt")
	w := httptest.NewRecorder()

	h.CreateAttachmentShare(w, req)

	assertStatus(t, w, http.StatusCreated)
	if len(sftpgoMock.createShareCalls) != 1 || sftpgoMock.createShareCalls[0] != storageKey {
		t.Fatalf("createShareCalls = %v, want exactly [%q]", sftpgoMock.createShareCalls, storageKey)
	}

	resp := decodeJSON[shareResponse](t, w)
	want := sftpgoMock.PublicShareURL("share-xyz")
	if resp.ShareURL != want {
		t.Errorf("ShareURL = %q, want %q", resp.ShareURL, want)
	}
}

func TestCreateAttachmentSharePropagatesSftpgoFailure(t *testing.T) {
	t.Parallel()
	caseID := "22222222-2222-2222-2222-222222222222"
	storageKey := "/attachments/att-1"
	entity := &mockEntityCaseClient{
		getCaseAttachmentFn: func(ctx context.Context, attachmentID string) ([]byte, error) {
			return []byte(`{"referenceId":"` + caseID + `","referenceType":"case","storageKey":"` + storageKey + `"}`), nil
		},
		getCaseFn: func(ctx context.Context, id string) ([]byte, error) {
			return []byte(`{"state":"work_in_progress"}`), nil
		},
	}
	sftpgoMock := &mockSftpgoClient{
		createShareFn: func(ctx context.Context, accessToken, gotStorageKey string, scope int, ttl time.Duration) (string, error) {
			return "", &apierror.Error{StatusCode: http.StatusInternalServerError, Body: "boom"}
		},
	}
	h := NewAttachmentStorageHandler(entity, sftpgoMock)

	attachmentID := "11111111-1111-1111-1111-111111111111"
	req := withUser(httptest.NewRequest(http.MethodPost, "/attachments/"+attachmentID+"/share", nil))
	req.SetPathValue("id", attachmentID)
	req.Header.Set("x-jwt-assertion", "raw-jwt")
	w := httptest.NewRecorder()

	h.CreateAttachmentShare(w, req)

	assertStatus(t, w, http.StatusBadGateway)
}

// ----- ConfirmUpload -----

func TestConfirmUploadRequiresAuth(t *testing.T) {
	t.Parallel()
	h := NewAttachmentStorageHandler(&mockEntityCaseClient{}, &mockSftpgoClient{})

	caseID := "11111111-1111-1111-1111-111111111111"
	attachmentID := "22222222-2222-2222-2222-222222222222"
	req := httptest.NewRequest(http.MethodPost, "/cases/"+caseID+"/attachments/"+attachmentID+"/confirm", nil)
	req.SetPathValue("caseId", caseID)
	req.SetPathValue("attachmentId", attachmentID)
	w := httptest.NewRecorder()

	h.ConfirmUpload(w, req)

	assertStatus(t, w, http.StatusUnauthorized)
}

func TestConfirmUploadRejectsInvalidCaseID(t *testing.T) {
	t.Parallel()
	h := NewAttachmentStorageHandler(&mockEntityCaseClient{}, &mockSftpgoClient{})

	attachmentID := "22222222-2222-2222-2222-222222222222"
	req := withUser(httptest.NewRequest(http.MethodPost, "/cases/not-a-uuid/attachments/"+attachmentID+"/confirm", nil))
	req.SetPathValue("caseId", "not-a-uuid")
	req.SetPathValue("attachmentId", attachmentID)
	w := httptest.NewRecorder()

	h.ConfirmUpload(w, req)

	assertStatus(t, w, http.StatusBadRequest)
}

func TestConfirmUploadRejectsInvalidAttachmentID(t *testing.T) {
	t.Parallel()
	h := NewAttachmentStorageHandler(&mockEntityCaseClient{}, &mockSftpgoClient{})

	caseID := "11111111-1111-1111-1111-111111111111"
	req := withUser(httptest.NewRequest(http.MethodPost, "/cases/"+caseID+"/attachments/not-a-uuid/confirm", nil))
	req.SetPathValue("caseId", caseID)
	req.SetPathValue("attachmentId", "not-a-uuid")
	w := httptest.NewRecorder()

	h.ConfirmUpload(w, req)

	assertStatus(t, w, http.StatusBadRequest)
}

// TestConfirmUploadSuccess verifies the happy path: ConfirmUpload calls the
// entity service's ConfirmCaseAttachment with exactly the path's attachment
// id, and forwards its response body and 200 status to the caller unchanged.
func TestConfirmUploadSuccess(t *testing.T) {
	t.Parallel()
	var gotAttachmentID string
	entity := &mockEntityCaseClient{
		confirmCaseAttachmentFn: func(ctx context.Context, attachmentID string) ([]byte, error) {
			gotAttachmentID = attachmentID
			return []byte(`{"message":"Attachment confirmed successfully","attachment":{"id":"` + attachmentID + `","status":"complete"}}`), nil
		},
	}
	h := NewAttachmentStorageHandler(entity, &mockSftpgoClient{})

	caseID := "11111111-1111-1111-1111-111111111111"
	attachmentID := "22222222-2222-2222-2222-222222222222"
	req := withUser(httptest.NewRequest(http.MethodPost, "/cases/"+caseID+"/attachments/"+attachmentID+"/confirm", nil))
	req.SetPathValue("caseId", caseID)
	req.SetPathValue("attachmentId", attachmentID)
	w := httptest.NewRecorder()

	h.ConfirmUpload(w, req)

	assertStatus(t, w, http.StatusOK)
	if gotAttachmentID != attachmentID {
		t.Errorf("ConfirmCaseAttachment called with attachmentID = %q, want %q", gotAttachmentID, attachmentID)
	}
	var resp struct {
		Attachment struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"attachment"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v; raw: %s", err, w.Body.String())
	}
	if resp.Attachment.ID != attachmentID || resp.Attachment.Status != "complete" {
		t.Errorf("response attachment = %+v, want id=%q status=complete", resp.Attachment, attachmentID)
	}
}

// TestConfirmUploadMapsNotFound verifies a 404 from the entity service (the
// attachment id does not exist) maps to a 404 on this backend's response.
func TestConfirmUploadMapsNotFound(t *testing.T) {
	t.Parallel()
	entity := &mockEntityCaseClient{
		confirmCaseAttachmentFn: func(ctx context.Context, attachmentID string) ([]byte, error) {
			return nil, &apierror.Error{StatusCode: http.StatusNotFound, Body: "not found"}
		},
	}
	h := NewAttachmentStorageHandler(entity, &mockSftpgoClient{})

	caseID := "11111111-1111-1111-1111-111111111111"
	attachmentID := "22222222-2222-2222-2222-222222222222"
	req := withUser(httptest.NewRequest(http.MethodPost, "/cases/"+caseID+"/attachments/"+attachmentID+"/confirm", nil))
	req.SetPathValue("caseId", caseID)
	req.SetPathValue("attachmentId", attachmentID)
	w := httptest.NewRecorder()

	h.ConfirmUpload(w, req)

	assertStatus(t, w, http.StatusNotFound)
	assertErrorMessage(t, w, ErrMsgNotFound)
}

// TestConfirmUploadMapsForbidden verifies a 403 from the entity service (the
// caller is not the attachment's original uploader) maps to a 403 here.
func TestConfirmUploadMapsForbidden(t *testing.T) {
	t.Parallel()
	entity := &mockEntityCaseClient{
		confirmCaseAttachmentFn: func(ctx context.Context, attachmentID string) ([]byte, error) {
			return nil, &apierror.Error{StatusCode: http.StatusForbidden, Body: "attachment was not created by the current user"}
		},
	}
	h := NewAttachmentStorageHandler(entity, &mockSftpgoClient{})

	caseID := "11111111-1111-1111-1111-111111111111"
	attachmentID := "22222222-2222-2222-2222-222222222222"
	req := withUser(httptest.NewRequest(http.MethodPost, "/cases/"+caseID+"/attachments/"+attachmentID+"/confirm", nil))
	req.SetPathValue("caseId", caseID)
	req.SetPathValue("attachmentId", attachmentID)
	w := httptest.NewRecorder()

	h.ConfirmUpload(w, req)

	assertStatus(t, w, http.StatusForbidden)
	assertErrorMessage(t, w, ErrMsgForbidden)
}

// TestConfirmUploadMapsConflict verifies a 409 from the entity service (the
// attachment is not in "pending" status, e.g. already confirmed) maps to a
// 409 here, since mapUpstreamErrorGeneric preserves 409 as the status code.
func TestConfirmUploadMapsConflict(t *testing.T) {
	t.Parallel()
	entity := &mockEntityCaseClient{
		confirmCaseAttachmentFn: func(ctx context.Context, attachmentID string) ([]byte, error) {
			return nil, &apierror.Error{StatusCode: http.StatusConflict, Body: `attachment is not pending (current status: "complete")`}
		},
	}
	h := NewAttachmentStorageHandler(entity, &mockSftpgoClient{})

	caseID := "11111111-1111-1111-1111-111111111111"
	attachmentID := "22222222-2222-2222-2222-222222222222"
	req := withUser(httptest.NewRequest(http.MethodPost, "/cases/"+caseID+"/attachments/"+attachmentID+"/confirm", nil))
	req.SetPathValue("caseId", caseID)
	req.SetPathValue("attachmentId", attachmentID)
	w := httptest.NewRecorder()

	h.ConfirmUpload(w, req)

	assertStatus(t, w, http.StatusConflict)
}

// ----- sanitizeFilenameForStorageKey / buildStorageKey -----

func TestSanitizeFilenameForStorageKey(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"ordinary name with extension", "quarterly-report.pdf", "quarterly-report.pdf"},
		{"spaces and parens", "Q3 report (final).docx", "Q3 report (final).docx"},
		{"unicode name", "報告書.pdf", "報告書.pdf"},
		{"forward slashes stripped", "a/b/c.txt", "abc.txt"},
		{"backslashes stripped", `a\b\c.txt`, "abc.txt"},
		{"parent traversal stripped", "../../etc/passwd", "etcpasswd"},
		{"traversal reassembly attempt", "..\\/../secret.txt", "secret.txt"},
		{"leading dots stripped", "...hidden.txt", "hidden.txt"},
		{"bare dot", ".", ""},
		{"bare dotdot", "..", ""},
		{"control characters stripped", "bad\x00name\x01.txt", "badname.txt"},
		{"empty input", "", ""},
		{"only invalid characters", "/\\..", ""},
		{"very long name truncated", strings.Repeat("a", 500) + ".txt", strings.Repeat("a", 200)},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := sanitizeFilenameForStorageKey(tc.input)
			if got != tc.want {
				t.Errorf("sanitizeFilenameForStorageKey(%q) = %q, want %q", tc.input, got, tc.want)
			}
			if len(got) > maxSanitizedFilenameLen {
				t.Errorf("sanitizeFilenameForStorageKey(%q) length = %d, want <= %d", tc.input, len(got), maxSanitizedFilenameLen)
			}
			if strings.Contains(got, "/") || strings.Contains(got, `\`) {
				t.Errorf("sanitizeFilenameForStorageKey(%q) = %q, must not contain a path separator", tc.input, got)
			}
			if strings.Contains(got, "..") {
				t.Errorf("sanitizeFilenameForStorageKey(%q) = %q, must not contain \"..\"", tc.input, got)
			}
		})
	}
}

func TestBuildStorageKeyLeafFormat(t *testing.T) {
	t.Parallel()
	const caseID = "11111111-1111-1111-1111-111111111111"
	const attachmentID = "22222222-2222-2222-2222-222222222222"

	t.Run("no project, valid filename", func(t *testing.T) {
		t.Parallel()
		got := buildStorageKey("", caseID, attachmentID, "report.pdf")
		want := "/attachments/cases/" + caseID + "/" + attachmentID + "/report.pdf"
		if got != want {
			t.Errorf("buildStorageKey = %q, want %q", got, want)
		}
		if path.Dir(got) != "/attachments/cases/"+caseID+"/"+attachmentID {
			t.Errorf("path.Dir(buildStorageKey(...)) = %q, want the attachment's own directory %q", path.Dir(got), "/attachments/cases/"+caseID+"/"+attachmentID)
		}
	})

	t.Run("with project, valid filename", func(t *testing.T) {
		t.Parallel()
		const projectID = "33333333-3333-3333-3333-333333333333"
		got := buildStorageKey(projectID, caseID, attachmentID, "report.pdf")
		want := "/attachments/project-" + projectID + "/cases/" + caseID + "/" + attachmentID + "/report.pdf"
		if got != want {
			t.Errorf("buildStorageKey = %q, want %q", got, want)
		}
		if path.Dir(got) != "/attachments/project-"+projectID+"/cases/"+caseID+"/"+attachmentID {
			t.Errorf("path.Dir(buildStorageKey(...)) = %q, want the attachment's own directory", path.Dir(got))
		}
	})

	t.Run("filename sanitizes to empty falls back to attachment id as the leaf name", func(t *testing.T) {
		t.Parallel()
		got := buildStorageKey("", caseID, attachmentID, "../..")
		want := "/attachments/cases/" + caseID + "/" + attachmentID + "/" + attachmentID
		if got != want {
			t.Errorf("buildStorageKey = %q, want %q (attachment-id leaf fallback)", got, want)
		}
	})

	t.Run("path traversal filename cannot escape the attachment directory", func(t *testing.T) {
		t.Parallel()
		got := buildStorageKey("", caseID, attachmentID, "../../../etc/passwd")
		if path.Dir(got) != "/attachments/cases/"+caseID+"/"+attachmentID {
			t.Errorf("path.Dir(buildStorageKey(...)) = %q, want attachment directory %q — traversal must not escape it", path.Dir(got), "/attachments/cases/"+caseID+"/"+attachmentID)
		}
		if strings.Contains(got, "..") {
			t.Errorf("buildStorageKey(...) = %q, must not contain \"..\"", got)
		}
	})
}
