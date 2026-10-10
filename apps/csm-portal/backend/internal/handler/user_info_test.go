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
)

func TestSplGetUserInfo(t *testing.T) {
	t.Run("requires authenticated user", func(t *testing.T) {
		h := NewUserInfoHandler(&mockEntityUserClient{}, viewerAccessGuard)
		r := httptest.NewRequest(http.MethodGet, "/spl/user-info", nil)
		w := httptest.NewRecorder()
		h.GetUserInfo(w, r)
		assertStatus(t, w, http.StatusUnauthorized)
		assertErrorMessage(t, w, ErrMsgUnauthorized)
	})

	t.Run("rejects a role that doesn't grant PermViewerAccess", func(t *testing.T) {
		h := NewUserInfoHandler(&mockEntityUserClient{}, viewerAccessGuard)
		r := httptest.NewRequest(http.MethodGet, "/spl/user-info", nil)
		// Authenticated but holds no role granting PermViewerAccess.
		r = r.WithContext(middleware.WithUserInfo(r.Context(), &middleware.UserInfo{Email: "nobody@example.com", UserID: "u-nobody"}))
		w := httptest.NewRecorder()
		h.GetUserInfo(w, r)
		assertStatus(t, w, http.StatusForbidden)
		assertErrorMessage(t, w, ErrMsgForbidden)
	})

	t.Run("resolves the caller's own name from entity-service", func(t *testing.T) {
		var called bool
		h := NewUserInfoHandler(&mockEntityUserClient{
			getUserMeFn: func(ctx context.Context) ([]byte, error) {
				called = true
				return []byte(`{"id":"u-1","email":"agent@example.com","firstName":"Agent","lastName":"Example"}`), nil
			},
		}, viewerAccessGuard)
		r := withUser(httptest.NewRequest(http.MethodGet, "/spl/user-info", nil))
		w := httptest.NewRecorder()
		h.GetUserInfo(w, r)
		assertStatus(t, w, http.StatusOK)
		if !called {
			t.Error("entity GetUserMe was not called")
		}
		view := decodeJSON[UserInfoView](t, w)
		if view.FirstName != "Agent" || view.LastName != "Example" {
			t.Errorf("view = %+v, want FirstName=Agent LastName=Example", view)
		}
	})

	t.Run("maps upstream failure to a generic 500", func(t *testing.T) {
		h := NewUserInfoHandler(&mockEntityUserClient{
			getUserMeFn: func(ctx context.Context) ([]byte, error) {
				return nil, context.DeadlineExceeded
			},
		}, viewerAccessGuard)
		r := withUser(httptest.NewRequest(http.MethodGet, "/spl/user-info", nil))
		w := httptest.NewRecorder()
		h.GetUserInfo(w, r)
		assertStatus(t, w, http.StatusInternalServerError)
		assertErrorMessage(t, w, "Failed to retrieve user info.")
	})
}
