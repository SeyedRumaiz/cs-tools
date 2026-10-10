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

	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/googledrive"
	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/middleware"
)

// mockDriveClient is a test double for driveClient.
type mockDriveClient struct {
	listFilesFn    func(ctx context.Context, folderID string) ([]googledrive.DriveFile, error)
	searchFolderFn func(ctx context.Context, folderName string) (*googledrive.DriveFolder, error)
}

func (m *mockDriveClient) ListFiles(ctx context.Context, folderID string) ([]googledrive.DriveFile, error) {
	return m.listFilesFn(ctx, folderID)
}

func (m *mockDriveClient) SearchFolder(ctx context.Context, folderName string) (*googledrive.DriveFolder, error) {
	return m.searchFolderFn(ctx, folderName)
}

func TestSplFilesHandler_ListFiles(t *testing.T) {
	t.Run("requires authentication", func(t *testing.T) {
		h := NewFilesHandler(&mockDriveClient{}, viewerAccessGuard)
		r := httptest.NewRequest(http.MethodGet, "/spl/files?folderId=abc", nil)
		w := httptest.NewRecorder()

		h.ListFiles(w, r)

		assertStatus(t, w, http.StatusUnauthorized)
	})

	t.Run("requires a role granting PermViewerAccess", func(t *testing.T) {
		h := NewFilesHandler(&mockDriveClient{}, viewerAccessGuard)
		r := httptest.NewRequest(http.MethodGet, "/spl/files?folderId=abc", nil)
		// Authenticated but holds no role granting PermViewerAccess.
		r = r.WithContext(middleware.WithUserInfo(r.Context(), &middleware.UserInfo{Email: "nobody@example.com", UserID: "u-nobody"}))
		w := httptest.NewRecorder()

		h.ListFiles(w, r)

		assertStatus(t, w, http.StatusForbidden)
	})

	t.Run("rejects empty folderId with 400", func(t *testing.T) {
		h := NewFilesHandler(&mockDriveClient{}, viewerAccessGuard)
		r := withUser(httptest.NewRequest(http.MethodGet, "/spl/files?folderId=", nil))
		w := httptest.NewRecorder()

		h.ListFiles(w, r)

		assertStatus(t, w, http.StatusBadRequest)
	})

	t.Run("returns files from the drive client", func(t *testing.T) {
		want := []googledrive.DriveFile{{ID: "f1", Name: "report.pdf", MimeType: "application/pdf"}}
		var capturedFolderID string
		mock := &mockDriveClient{
			listFilesFn: func(_ context.Context, folderID string) ([]googledrive.DriveFile, error) {
				capturedFolderID = folderID
				return want, nil
			},
		}
		h := NewFilesHandler(mock, viewerAccessGuard)
		r := withUser(httptest.NewRequest(http.MethodGet, "/spl/files?folderId=folder-1", nil))
		w := httptest.NewRecorder()

		h.ListFiles(w, r)

		assertStatus(t, w, http.StatusOK)
		if capturedFolderID != "folder-1" {
			t.Errorf("folderID = %q, want %q", capturedFolderID, "folder-1")
		}
		got := decodeJSON[[]googledrive.DriveFile](t, w)
		if len(got) != 1 || got[0].ID != "f1" {
			t.Errorf("files = %+v, want %+v", got, want)
		}
	})

	t.Run("maps client errors to a generic failure response", func(t *testing.T) {
		mock := &mockDriveClient{
			listFilesFn: func(_ context.Context, _ string) ([]googledrive.DriveFile, error) {
				return nil, context.DeadlineExceeded
			},
		}
		h := NewFilesHandler(mock, viewerAccessGuard)
		r := withUser(httptest.NewRequest(http.MethodGet, "/spl/files?folderId=folder-1", nil))
		w := httptest.NewRecorder()

		h.ListFiles(w, r)

		assertStatus(t, w, http.StatusInternalServerError)
	})
}

func TestSplFilesHandler_SearchFolder(t *testing.T) {
	t.Run("requires authentication", func(t *testing.T) {
		h := NewFilesHandler(&mockDriveClient{}, viewerAccessGuard)
		r := httptest.NewRequest(http.MethodGet, "/spl/files/search?folderName=Acme", nil)
		w := httptest.NewRecorder()

		h.SearchFolder(w, r)

		assertStatus(t, w, http.StatusUnauthorized)
	})

	t.Run("rejects empty folderName with 400", func(t *testing.T) {
		h := NewFilesHandler(&mockDriveClient{}, viewerAccessGuard)
		r := withUser(httptest.NewRequest(http.MethodGet, "/spl/files/search?folderName=", nil))
		w := httptest.NewRecorder()

		h.SearchFolder(w, r)

		assertStatus(t, w, http.StatusBadRequest)
	})

	t.Run("returns the matching folder", func(t *testing.T) {
		want := &googledrive.DriveFolder{ID: "d1", Name: "Acme Corp"}
		mock := &mockDriveClient{
			searchFolderFn: func(_ context.Context, folderName string) (*googledrive.DriveFolder, error) {
				if folderName != "Acme Corp" {
					t.Errorf("folderName = %q, want %q", folderName, "Acme Corp")
				}
				return want, nil
			},
		}
		h := NewFilesHandler(mock, viewerAccessGuard)
		r := withUser(httptest.NewRequest(http.MethodGet, "/spl/files/search?folderName=Acme+Corp", nil))
		w := httptest.NewRecorder()

		h.SearchFolder(w, r)

		assertStatus(t, w, http.StatusOK)
		got := decodeJSON[googledrive.DriveFolder](t, w)
		if got.ID != "d1" {
			t.Errorf("folder = %+v, want %+v", got, want)
		}
	})

	t.Run("maps ErrFolderNotFound to 404", func(t *testing.T) {
		mock := &mockDriveClient{
			searchFolderFn: func(_ context.Context, _ string) (*googledrive.DriveFolder, error) {
				return nil, googledrive.ErrFolderNotFound
			},
		}
		h := NewFilesHandler(mock, viewerAccessGuard)
		r := withUser(httptest.NewRequest(http.MethodGet, "/spl/files/search?folderName=Nope", nil))
		w := httptest.NewRecorder()

		h.SearchFolder(w, r)

		assertStatus(t, w, http.StatusNotFound)
	})
}

// Compile-time check that *googledrive.Client satisfies driveClient.
var _ driveClient = (*googledrive.Client)(nil)
