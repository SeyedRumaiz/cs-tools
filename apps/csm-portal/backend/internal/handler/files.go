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
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/googledrive"
)

// driveClient abstracts the Google Drive operations used by
// FilesHandler.
type driveClient interface {
	ListFiles(ctx context.Context, folderID string) ([]googledrive.DriveFile, error)
	SearchFolder(ctx context.Context, folderName string) (*googledrive.DriveFolder, error)
}

// FilesHandler handles HTTP requests for SupportPortalLite's Google
// Drive browsing endpoints (GET /files, GET /files/search).
type FilesHandler struct {
	drive       driveClient
	accessGuard *AccessGuard
}

// NewFilesHandler creates a FilesHandler backed by the given Drive
// client. accessGuard enforces PermViewerAccess, SupportPortalLite's blanket
// audience gate — these endpoints have no additional
// fine-grained permission check beyond it (confirmed by reading service.bal: the
// `files`/`files/search` resource functions take no http:RequestContext and
// never call authJWT/isUserAuthorized beyond the global request
// interceptor).
func NewFilesHandler(drive driveClient, accessGuard *AccessGuard) *FilesHandler {
	return &FilesHandler{drive: drive, accessGuard: accessGuard}
}

// ListFiles handles GET /files.
//
// The Ballerina original responds with 500 for an empty folderId
// (utils:getHTTPInternalServerErrorResponse), but this backend's own
// convention for a missing/blank required parameter is 400 (see
// CLAUDE.md's handler-conventions guidance and e.g. AccountHandler.GetAccount)
// — deliberately adopted here instead of the Ballerina status code, since
// this is our own input-validation response, not a change to which external
// endpoint is called or how.
func (h *FilesHandler) ListFiles(w http.ResponseWriter, r *http.Request) {
	user, ok := requireViewerAccess(w, r, h.accessGuard)
	if !ok {
		return
	}

	folderID := strings.TrimSpace(r.URL.Query().Get("folderId"))
	if folderID == "" {
		writeError(w, http.StatusBadRequest, "folderId is required.")
		return
	}

	files, err := h.drive.ListFiles(r.Context(), folderID)
	if err != nil {
		slog.ErrorContext(r.Context(), "googledrive ListFiles failed", "userID", user.UserID, "err", err)
		mapUpstreamErrorGeneric(w, err, "Failed to list files.")
		return
	}

	writeJSONValue(w, http.StatusOK, files)
}

// SearchFolder handles GET /files/search.
//
// See ListFiles's doc comment: the empty-folderName case deliberately
// returns 400 here rather than the Ballerina original's 500, matching this
// backend's own input-validation convention.
func (h *FilesHandler) SearchFolder(w http.ResponseWriter, r *http.Request) {
	user, ok := requireViewerAccess(w, r, h.accessGuard)
	if !ok {
		return
	}

	folderName := strings.TrimSpace(r.URL.Query().Get("folderName"))
	if folderName == "" {
		writeError(w, http.StatusBadRequest, "folderName is required.")
		return
	}

	folder, err := h.drive.SearchFolder(r.Context(), folderName)
	if err != nil {
		if errors.Is(err, googledrive.ErrFolderNotFound) {
			writeError(w, http.StatusNotFound, ErrMsgNotFound)
			return
		}
		slog.ErrorContext(r.Context(), "googledrive SearchFolder failed", "userID", user.UserID, "err", err)
		mapUpstreamErrorGeneric(w, err, "Failed to search folder.")
		return
	}

	writeJSONValue(w, http.StatusOK, folder)
}
