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

type mockLookupsClient struct {
	products []string
	teams    []string
	err      error
}

func (m *mockLookupsClient) GetProductList(ctx context.Context) ([]string, error) {
	return m.products, m.err
}
func (m *mockLookupsClient) GetABTTeamList(ctx context.Context) ([]string, error) {
	return m.teams, m.err
}

func TestGetProducts_Success(t *testing.T) {
	mock := &mockLookupsClient{products: []string{"A", "B"}}
	h := NewLookupsHandler(mock, viewerAccessGuard)
	req := withUser(httptest.NewRequest(http.MethodGet, "/spl/products", nil))
	w := httptest.NewRecorder()

	h.GetProducts(w, req)

	assertStatus(t, w, http.StatusOK)
	got := decodeJSON[[]string](t, w)
	if len(got) != 2 || got[0] != "A" {
		t.Errorf("products = %v", got)
	}
}

func TestGetABTTeams_RejectsMissingSPLAccess(t *testing.T) {
	h := NewLookupsHandler(&mockLookupsClient{}, viewerAccessGuard)
	req := httptest.NewRequest(http.MethodGet, "/spl/abt-teams", nil)
	// Authenticated but holds no role granting PermViewerAccess.
	req = req.WithContext(middleware.WithUserInfo(req.Context(), &middleware.UserInfo{Email: "nobody@example.com", UserID: "u-nobody"}))
	w := httptest.NewRecorder()

	h.GetABTTeams(w, req)

	assertStatus(t, w, http.StatusForbidden)
}
