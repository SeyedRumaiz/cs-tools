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

// The sweep recomputes and replaces availability rows, so a caller without
// an internal client credential must be turned away before it runs.
func TestAvailabilitySweepRouteIsInternalOnly(t *testing.T) {
	router := newPortalWriteRouter(t, false)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/internal/availability/sweep", strings.NewReader("")))
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "authorized internal client credential") {
		t.Errorf("POST /internal/availability/sweep = %d (%s), want 401 from the internalOnly gate", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
}
