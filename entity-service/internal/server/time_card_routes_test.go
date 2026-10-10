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

// TestTimeCardWriteRoutesAreInternalOnly pins that POST/PATCH/DELETE on
// /time-cards sit behind internalOnly. The request carries no token and an
// empty body: without the gate the handler would reject the body (400) or
// ask for the x-user-id-token header; with it, the request is stopped first
// by the scope check (401, "authorized internal client credential"). A
// customer with a valid token gets 403 from the same gate, which
// TestInternalOnly covers.
func TestTimeCardWriteRoutesAreInternalOnly(t *testing.T) {
	router := newPortalWriteRouter(t, false)
	id := "3f1e8d6a-3b4c-4d5e-8f90-123456789abc"
	for _, tc := range []struct{ name, method, path string }{
		{"create", http.MethodPost, "/time-cards"},
		{"update", http.MethodPatch, "/time-cards/" + id},
		{"delete", http.MethodDelete, "/time-cards/" + id},
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
