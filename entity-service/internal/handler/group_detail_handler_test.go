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
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/apierror"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
)

type stubGroupDetailService struct {
	detail domain.GroupDetail
	err    error
	gotID  string
}

func (s *stubGroupDetailService) GetGroupDetail(_ context.Context, id string) (domain.GroupDetail, error) {
	s.gotID = id
	return s.detail, s.err
}

func newGroupDetailMux(svc *stubGroupDetailService) *http.ServeMux {
	h := NewGroupDetailHandler(svc)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /groups/{id}", h.GetGroup)
	return mux
}

func getGroup(t *testing.T, svc *stubGroupDetailService, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	newGroupDetailMux(svc).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestGetGroup_ReturnsTheContract(t *testing.T) {
	desc, email := "Change Advisory Board", "cab@example.test"
	svc := &stubGroupDetailService{detail: domain.GroupDetail{
		ID: "g1", Name: "CAB Approval", Description: &desc, Email: &email,
		Manager: &domain.GroupManagerRef{ID: "m1", Name: "Mia Manager"},
		Members: []domain.GroupMember{{ID: "u1", Name: "Alice", Email: &email}},
		Total:   1,
	}}
	rec := getGroup(t, svc, "/groups/g1")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if svc.gotID != "g1" {
		t.Fatalf("service asked for %q", svc.gotID)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, k := range []string{"id", "name", "description", "email", "manager", "members", "total"} {
		if _, ok := body[k]; !ok {
			t.Fatalf("response is missing %q: %s", k, rec.Body.String())
		}
	}
	if body["total"].(float64) != 1 {
		t.Fatalf("total = %v", body["total"])
	}
	m := body["members"].([]any)[0].(map[string]any)
	for _, k := range []string{"id", "name", "email", "userType", "role"} {
		if _, ok := m[k]; !ok {
			t.Fatalf("member is missing %q: %v", k, m)
		}
	}
}

// A bare group serialises its optional parts as null and its members as [].
func TestGetGroup_BareGroupHasNullsAndAnEmptyList(t *testing.T) {
	svc := &stubGroupDetailService{detail: domain.GroupDetail{ID: "g2", Name: "Empty", Members: []domain.GroupMember{}}}
	rec := getGroup(t, svc, "/groups/g2")
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	for _, k := range []string{"description", "email", "manager"} {
		if v, ok := body[k]; !ok || v != nil {
			t.Fatalf("%s = %v (present %v), want null", k, v, ok)
		}
	}
	if members, ok := body["members"].([]any); !ok || len(members) != 0 {
		t.Fatalf("members = %v, want []", body["members"])
	}
}

func TestGetGroup_MapsServiceErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"unknown id", &apierror.NotFoundError{Msg: "group not found"}, http.StatusNotFound},
		{"bad id", &apierror.ValidationError{Msg: "id contains invalid UUID"}, http.StatusBadRequest},
		{"non-internal caller", &apierror.ForbiddenError{Msg: "group membership is only available to internal staff"}, http.StatusForbidden},
		{"no identity", &apierror.UnauthorizedError{Msg: "x-user-id-token header is required"}, http.StatusUnauthorized},
		{"anything else", errors.New("boom"), http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := getGroup(t, &stubGroupDetailService{err: tc.err}, "/groups/g1")
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}
