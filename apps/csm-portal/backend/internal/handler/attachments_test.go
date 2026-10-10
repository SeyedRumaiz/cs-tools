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
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/middleware"
	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/servicenow"
)

type mockAttachmentsClient struct {
	body                     []byte
	contentType              string
	err                      error
	requireCaseAttachmentErr error
}

func (m *mockAttachmentsClient) RequireCaseAttachment(ctx context.Context, attachmentSysID string) error {
	return m.requireCaseAttachmentErr
}

func (m *mockAttachmentsClient) DownloadAttachment(ctx context.Context, attachmentSysID string) ([]byte, string, string, error) {
	return m.body, m.contentType, "", m.err
}

func newAttachmentDownloadRequest(attachmentID string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/attachments/"+attachmentID+"/download", nil)
	req.SetPathValue("attachmentId", attachmentID)
	return withUser(req)
}

func TestDownloadAttachment_Success(t *testing.T) {
	mock := &mockAttachmentsClient{body: []byte("%PDF-1.4"), contentType: "application/pdf"}
	h := NewAttachmentsHandler(mock, viewerAccessGuard)
	w := httptest.NewRecorder()

	h.DownloadAttachment(w, newAttachmentDownloadRequest("att-1"))

	assertStatus(t, w, http.StatusOK)
	assertContentType(t, w, "application/pdf")
	if w.Header().Get("Content-Disposition") != "attachment" {
		t.Errorf("Content-Disposition = %q, want %q", w.Header().Get("Content-Disposition"), "attachment")
	}
	if w.Body.String() != "%PDF-1.4" {
		t.Errorf("body = %q", w.Body.String())
	}
}

func TestDownloadAttachment_CoercesUnsafeContentType(t *testing.T) {
	mock := &mockAttachmentsClient{body: []byte("<script>"), contentType: "text/html"}
	h := NewAttachmentsHandler(mock, viewerAccessGuard)
	w := httptest.NewRecorder()

	h.DownloadAttachment(w, newAttachmentDownloadRequest("att-1"))

	assertContentType(t, w, "application/octet-stream")
}

func TestDownloadAttachment_RejectsNonCaseAttachment(t *testing.T) {
	mock := &mockAttachmentsClient{requireCaseAttachmentErr: servicenow.ErrAttachmentNotFound}
	h := NewAttachmentsHandler(mock, viewerAccessGuard)
	w := httptest.NewRecorder()

	h.DownloadAttachment(w, newAttachmentDownloadRequest("att-1"))

	assertStatus(t, w, http.StatusNotFound)
}

func TestDownloadAttachment_RejectsMissingDownloadPermission(t *testing.T) {
	h := NewAttachmentsHandler(&mockAttachmentsClient{}, viewerAccessGuard)
	// SPL access (sales_solutions) but no attachment_downloader/cs_engineer/
	// admin — passes PermViewerAccess, fails the additional
	// PermDownloadAttachment check.
	req := httptest.NewRequest(http.MethodGet, "/attachments/att-1/download", nil)
	req.SetPathValue("attachmentId", "att-1")
	req = req.WithContext(middleware.WithUserInfo(req.Context(), &middleware.UserInfo{
		Email: "sales@example.com", UserID: "u-sales", Roles: []string{"test-sales-solutions"},
	}))
	w := httptest.NewRecorder()

	h.DownloadAttachment(w, req)

	assertStatus(t, w, http.StatusForbidden)
}

func TestDownloadAttachment_RejectsEmptyID(t *testing.T) {
	h := NewAttachmentsHandler(&mockAttachmentsClient{}, viewerAccessGuard)
	req := withUser(httptest.NewRequest(http.MethodGet, "/attachments//download", nil))
	req.SetPathValue("attachmentId", "")
	w := httptest.NewRecorder()

	h.DownloadAttachment(w, req)

	assertStatus(t, w, http.StatusBadRequest)
}
