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
	"strings"
	"testing"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/service"
)

// searchCapturingService records the request Search was called with; every
// other AnnouncementRequestService method is left nil-embedded, so a test
// that accidentally reaches one fails loudly instead of silently passing.
type searchCapturingService struct {
	service.AnnouncementRequestService
	got domain.SearchAnnouncementRequestsRequest
}

func (s *searchCapturingService) Search(_ context.Context, req domain.SearchAnnouncementRequestsRequest) (domain.SearchAnnouncementRequestsResponse, error) {
	s.got = req
	return domain.SearchAnnouncementRequestsResponse{}, nil
}

// decodeRequest rejects unknown JSON fields outright, so the wire contract of
// POST /announcement-requests/search is worth pinning: the new `states` list
// must be accepted, and the older bodies (single `state`, the scheduled-publish
// cron's `readyForScheduledPublish`) must keep decoding unchanged.
func TestSearchAnnouncementRequests_RequestBodies(t *testing.T) {
	post := func(t *testing.T, body string) (*httptest.ResponseRecorder, *searchCapturingService) {
		t.Helper()
		svc := &searchCapturingService{}
		h := NewAnnouncementRequestHandler(svc)
		req := httptest.NewRequest(http.MethodPost, "/announcement-requests/search", strings.NewReader(body))
		rec := httptest.NewRecorder()
		h.SearchAnnouncementRequests(rec, req)
		return rec, svc
	}

	t.Run("accepts a states list", func(t *testing.T) {
		rec, svc := post(t, `{"states":["draft","approved"],"pagination":{"offset":0,"limit":10}}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
		if len(svc.got.States) != 2 || svc.got.States[0] != domain.AnnouncementRequestStateDraft || svc.got.States[1] != domain.AnnouncementRequestStateApproved {
			t.Fatalf("States = %v, want [draft approved]", svc.got.States)
		}
		if svc.got.State != nil {
			t.Fatalf("State = %v, want nil", *svc.got.State)
		}
	})

	t.Run("still accepts the single state field", func(t *testing.T) {
		rec, svc := post(t, `{"state":"pending_approval","pagination":{"offset":0,"limit":10}}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
		if svc.got.State == nil || *svc.got.State != domain.AnnouncementRequestStatePendingApproval {
			t.Fatalf("State = %v, want pending_approval", svc.got.State)
		}
		if len(svc.got.States) != 0 {
			t.Fatalf("States = %v, want none", svc.got.States)
		}
	})

	t.Run("still accepts the scheduled-publish cron body", func(t *testing.T) {
		rec, svc := post(t, `{"readyForScheduledPublish":true,"pagination":{"offset":0,"limit":100}}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
		if !svc.got.ReadyForScheduledPublish {
			t.Fatal("ReadyForScheduledPublish not decoded")
		}
	})

	// The service tells "states sent but empty" from "states omitted" by
	// nil-ness (so a contradictory `state` + `"states": []` is rejected), which
	// only works if the JSON decode keeps that distinction.
	t.Run("keeps an explicit empty states list distinct from an omitted one", func(t *testing.T) {
		rec, svc := post(t, `{"states":[],"pagination":{"offset":0,"limit":10}}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
		if svc.got.States == nil {
			t.Fatal("explicit empty States decoded as nil, indistinguishable from omitted")
		}
		_, omitted := post(t, `{"pagination":{"offset":0,"limit":10}}`)
		if omitted.got.States != nil {
			t.Fatalf("omitted States = %v, want nil", omitted.got.States)
		}
	})

	t.Run("accepts no state filter at all", func(t *testing.T) {
		rec, svc := post(t, `{"pagination":{"offset":0,"limit":10}}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
		if svc.got.State != nil || len(svc.got.States) != 0 {
			t.Fatalf("got State=%v States=%v, want neither", svc.got.State, svc.got.States)
		}
	})

	t.Run("still rejects an unknown field", func(t *testing.T) {
		rec, _ := post(t, `{"stateList":["draft"],"pagination":{"offset":0,"limit":10}}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})
}
