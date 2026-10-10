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

package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestCommentByIDRoutesAreInternalOnly pins that GET/PATCH/DELETE on
// /comments/{id} and GET on /comments/{id}/history sit behind internalOnly --
// see routes.go's own doc comment on that registration for why: with no
// author/role check left inside commentService itself (that decision moved
// entirely to csm-portal-backend), an external/customer caller with nothing
// but a valid token could otherwise reach any of these four directly and act
// on a comment they don't own. POST /comments and POST /comments/search are
// deliberately NOT covered here: customers post and read comments on their
// own cases through those two, so they stay open to any validated caller
// (row-level security, not internalOnly, is what scopes them).
//
// Same request shape as TestTimeCardWriteRoutesAreInternalOnly: no token, so
// the request is stopped by the scope check (401, "authorized internal
// client credential") before ever reaching commentHandler. A customer with a
// valid token gets 403 from the same gate, which TestInternalOnly covers.
func TestCommentByIDRoutesAreInternalOnly(t *testing.T) {
	router := newPortalWriteRouter(t, false)
	id := "3f1e8d6a-3b4c-4d5e-8f90-123456789abc"
	for _, tc := range []struct{ name, method, path string }{
		{"get", http.MethodGet, "/comments/" + id},
		{"update", http.MethodPatch, "/comments/" + id},
		{"delete", http.MethodDelete, "/comments/" + id},
		{"history", http.MethodGet, "/comments/" + id + "/history"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(""))
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "authorized internal client credential") {
				t.Errorf("%s %s = %d (%s), want 401 from the internalOnly gate", tc.method, tc.path, rec.Code, strings.TrimSpace(rec.Body.String()))
			}
		})
	}
}

// TestCommentCreateAndSearchStayOpenToAnyValidatedCaller is
// TestCommentByIDRoutesAreInternalOnly's negative complement: POST /comments
// and POST /comments/search must NOT be behind internalOnly, since
// customer-portal-backend-v2 calls both so customers can post and read
// comments on their own cases. A request with no token still reaches
// auth.Middleware's own 401 (every route requires a validated token), but
// NOT internalOnly's distinct "authorized internal client credential"
// message -- the two 401s come from different layers, and this test tells
// them apart to confirm no internalOnly wrapper was added here by mistake.
func TestCommentCreateAndSearchStayOpenToAnyValidatedCaller(t *testing.T) {
	router := newPortalWriteRouter(t, false)
	for _, tc := range []struct{ name, method, path string }{
		{"create", http.MethodPost, "/comments"},
		{"search", http.MethodPost, "/comments/search"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader("{}"))
			router.ServeHTTP(rec, req)
			if strings.Contains(rec.Body.String(), "authorized internal client credential") {
				t.Errorf("%s %s = %d (%s), did not want internalOnly's gate on this route", tc.method, tc.path, rec.Code, strings.TrimSpace(rec.Body.String()))
			}
		})
	}
}
