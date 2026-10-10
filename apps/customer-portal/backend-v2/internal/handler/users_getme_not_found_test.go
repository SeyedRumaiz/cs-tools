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
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wso2-open-operations/cs-tools/apps/customer-portal/backend-v2/internal/apierror"
	"github.com/wso2-open-operations/cs-tools/apps/customer-portal/backend-v2/internal/entity"
)

// notFoundUserClient is an entityUserClient whose GetMe always 404s, as the
// upstream identity service does for a caller whose email has no "user" row
// at all (never provisioned downstream).
type notFoundUserClient struct{}

func (notFoundUserClient) GetMe(context.Context) (entity.GetUserMeResponse, error) {
	return entity.GetUserMeResponse{}, &apierror.Error{StatusCode: http.StatusNotFound}
}

func (notFoundUserClient) PatchMe(context.Context, entity.PatchUserMeRequest) (entity.PatchUserMeResponse, error) {
	return entity.PatchUserMeResponse{}, nil
}

func (notFoundUserClient) RegisterInvitedMemberships(context.Context) error {
	return nil
}

// TestGetMe_UpstreamNotFoundMapsToForbidden is the regression guard for a
// real, reported bug: a caller authenticated by a valid JWT whose email has
// no "user" row at all made the upstream identity service 404 GetMe. Passing
// that 404 straight through left the webapp's profile-fetch hook with
// nothing to render and no 403 state to fall into, spinning forever instead.
// GetMe now maps it to the same 403 the UI already knows how to show.
func TestGetMe_UpstreamNotFoundMapsToForbidden(t *testing.T) {
	h := NewUserHandler(notFoundUserClient{}, noopSCIMUserClient{}, false)

	rec := httptest.NewRecorder()
	h.GetMe(rec, getMeRequest())

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body["message"] != ErrMsgForbidden {
		t.Errorf("message = %v, want %q", body["message"], ErrMsgForbidden)
	}
}
