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
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/apierror"
	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/middleware"
	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/wso2cloud"
)

const wcrID = "11111111-2222-3333-4444-555555555555"

type wcrCall struct{ method, path, body string }

// fakeWso2CloudRequestClient answers GET with stored and every other call with
// result (or the error set for that method).
type fakeWso2CloudRequestClient struct {
	stored string
	errs   map[string]error
	calls  []wcrCall
}

func (f *fakeWso2CloudRequestClient) Wso2CloudRequestCall(_ context.Context, method, path string, body []byte) ([]byte, error) {
	f.calls = append(f.calls, wcrCall{method, path, string(body)})
	if err := f.errs[method]; err != nil {
		return nil, err
	}
	if method == http.MethodGet {
		return []byte(f.stored), nil
	}
	return []byte(`{"ok":true}`), nil
}

func (f *fakeWso2CloudRequestClient) called(method string) bool {
	for _, c := range f.calls {
		if c.method == method {
			return true
		}
	}
	return false
}

type fakeApplier struct {
	err     error
	applied []wso2cloud.Request
	// record, when set, is passed to the recorder before Apply returns.
	record string
}

func (a *fakeApplier) Apply(ctx context.Context, req wso2cloud.Request, rec wso2cloud.Recorder) error {
	a.applied = append(a.applied, req)
	if a.record != "" {
		if err := rec.RecordBillingAccount(ctx, a.record); err != nil {
			return err
		}
	}
	return a.err
}

// fakeSubscriptions answers ListSubscriptions with subs or err.
type fakeSubscriptions struct {
	subs  []wso2cloud.Subscription
	err   error
	orgID string
}

func (f *fakeSubscriptions) ListSubscriptions(_ context.Context, orgID string) ([]wso2cloud.Subscription, error) {
	f.orgID = orgID
	return f.subs, f.err
}

func pendingWcr(requestedBy string) string {
	return `{"id":"` + wcrID + `","number":"WCR0000001","type":"lock_user","targetId":"scim-1","targetLabel":"jane@acme.com","status":"pending","requestedBy":"` + requestedBy + `"}`
}

func wcrRequest(method, body string, user *middleware.UserInfo) *http.Request {
	r := httptest.NewRequest(method, "/x", strings.NewReader(body))
	r = r.WithContext(middleware.WithUserInfo(r.Context(), user))
	r.SetPathValue("id", wcrID)
	return r
}

var wcrApprover = &middleware.UserInfo{Email: "approver@wso2.com", UserID: "u-approver", Roles: []string{"test-wso2-cloud-approver"}}

func TestWso2CloudRequestHandler_ForwardsReads(t *testing.T) {
	cases := []struct {
		name       string
		method     string
		body       string
		call       func(h *Wso2CloudRequestHandler) http.HandlerFunc
		wantMethod string
		wantPath   string
		wantStatus int
	}{
		{"create", http.MethodPost, `{"type":"lock_user"}`, func(h *Wso2CloudRequestHandler) http.HandlerFunc { return h.CreateRequest },
			http.MethodPost, "/wso2-cloud-requests", http.StatusCreated},
		{"search", http.MethodPost, `{}`, func(h *Wso2CloudRequestHandler) http.HandlerFunc { return h.SearchRequests },
			http.MethodPost, "/wso2-cloud-requests/search", http.StatusOK},
		{"get", http.MethodGet, "", func(h *Wso2CloudRequestHandler) http.HandlerFunc { return h.GetRequest },
			http.MethodGet, "/wso2-cloud-requests/" + wcrID, http.StatusOK},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fake := &fakeWso2CloudRequestClient{stored: pendingWcr("someone@wso2.com")}
			w := httptest.NewRecorder()
			c.call(NewWso2CloudRequestHandler(fake, nil))(w, wcrRequest(c.method, c.body, testCsEngineerUser))
			if w.Code != c.wantStatus || len(fake.calls) != 1 || fake.calls[0].method != c.wantMethod || fake.calls[0].path != c.wantPath {
				t.Fatalf("status %d, calls %+v; want %d, %s %s", w.Code, fake.calls, c.wantStatus, c.wantMethod, c.wantPath)
			}
			if c.body != "" && fake.calls[0].body != c.body {
				t.Errorf("body forwarded = %s, want %s", fake.calls[0].body, c.body)
			}
		})
	}
}

func TestWso2CloudRequestHandler_Resolve(t *testing.T) {
	t.Run("approval is applied, then recorded", func(t *testing.T) {
		fake := &fakeWso2CloudRequestClient{stored: pendingWcr("engineer@wso2.com")}
		applier := &fakeApplier{}
		w := httptest.NewRecorder()
		NewWso2CloudRequestHandler(fake, applier).ResolveRequest(w, wcrRequest(http.MethodPost, `{"status":"approved"}`, wcrApprover))
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", w.Code, w.Body)
		}
		if len(applier.applied) != 1 || applier.applied[0].TargetID != "scim-1" || applier.applied[0].Type != "lock_user" {
			t.Fatalf("applied = %+v", applier.applied)
		}
		last := fake.calls[len(fake.calls)-1]
		if last.method != http.MethodPost || last.path != "/wso2-cloud-requests/"+wcrID+"/resolve" || last.body != `{"status":"approved"}` {
			t.Fatalf("resolve call = %+v", last)
		}
	})

	t.Run("rejection is recorded without applying or reading", func(t *testing.T) {
		fake := &fakeWso2CloudRequestClient{stored: pendingWcr("engineer@wso2.com")}
		applier := &fakeApplier{}
		w := httptest.NewRecorder()
		NewWso2CloudRequestHandler(fake, applier).ResolveRequest(w, wcrRequest(http.MethodPost, `{"status":"rejected","remark":"no contract"}`, wcrApprover))
		if w.Code != http.StatusOK || len(applier.applied) != 0 || len(fake.calls) != 1 || fake.calls[0].method != http.MethodPost {
			t.Fatalf("status %d, applied %d, calls %+v", w.Code, len(applier.applied), fake.calls)
		}
	})

	t.Run("not connected: refused, nothing recorded", func(t *testing.T) {
		fake := &fakeWso2CloudRequestClient{stored: pendingWcr("engineer@wso2.com")}
		w := httptest.NewRecorder()
		NewWso2CloudRequestHandler(fake, nil).ResolveRequest(w, wcrRequest(http.MethodPost, `{"status":"approved"}`, wcrApprover))
		if w.Code != http.StatusServiceUnavailable || fake.called(http.MethodPost) {
			t.Fatalf("status %d, calls %+v; want 503 and no resolve", w.Code, fake.calls)
		}
		if !strings.Contains(w.Body.String(), "stays pending") {
			t.Errorf("body = %s", w.Body)
		}
	})

	t.Run("apply failure: refused, nothing recorded", func(t *testing.T) {
		fake := &fakeWso2CloudRequestClient{stored: pendingWcr("engineer@wso2.com")}
		w := httptest.NewRecorder()
		NewWso2CloudRequestHandler(fake, &fakeApplier{err: errors.New("boom")}).ResolveRequest(w, wcrRequest(http.MethodPost, `{"status":"approved"}`, wcrApprover))
		if w.Code != http.StatusBadGateway || fake.called(http.MethodPost) {
			t.Fatalf("status %d, calls %+v; want 502 and no resolve", w.Code, fake.calls)
		}
	})

	t.Run("the billing account is recorded on the request", func(t *testing.T) {
		fake := &fakeWso2CloudRequestClient{stored: pendingWcr("engineer@wso2.com")}
		w := httptest.NewRecorder()
		NewWso2CloudRequestHandler(fake, &fakeApplier{record: "ba-1"}).ResolveRequest(w, wcrRequest(http.MethodPost, `{"status":"approved"}`, wcrApprover))
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", w.Code, w.Body)
		}
		var recorded *wcrCall
		for i := range fake.calls {
			if fake.calls[i].path == "/wso2-cloud-requests/"+wcrID+"/billing-account" {
				recorded = &fake.calls[i]
			}
		}
		if recorded == nil || recorded.method != http.MethodPost || recorded.body != `{"billingAccountId":"ba-1"}` {
			t.Fatalf("billing account call = %+v (calls %+v)", recorded, fake.calls)
		}
	})

	applyErrors := []struct {
		name     string
		err      error
		status   int
		contains string
		code     string
	}{
		{"not available", wso2cloud.ErrNotAvailable, http.StatusServiceUnavailable, "does not offer this kind of change", ""},
		{"not authorized", fmt.Errorf("create billing account: %w", wso2cloud.ErrNotAuthorized), http.StatusBadGateway, "did not allow", ""},
		{"refused by WSO2 Cloud", fmt.Errorf("change plan: %w", &wso2cloud.APIError{Status: http.StatusConflict, Message: "the subscription is suspended"}),
			http.StatusConflict, "WSO2 Cloud refused the change: the subscription is suspended The request stays pending.", errorCodeWso2CloudRefused},
	}
	for _, tc := range applyErrors {
		t.Run("apply error: "+tc.name, func(t *testing.T) {
			fake := &fakeWso2CloudRequestClient{stored: pendingWcr("engineer@wso2.com")}
			w := httptest.NewRecorder()
			NewWso2CloudRequestHandler(fake, &fakeApplier{err: tc.err}).ResolveRequest(w, wcrRequest(http.MethodPost, `{"status":"approved"}`, wcrApprover))
			if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.contains) || fake.called(http.MethodPost) {
				t.Fatalf("status %d, body %s, calls %+v", w.Code, w.Body, fake.calls)
			}
			if tc.code != "" && !strings.Contains(w.Body.String(), `"errorCode":"`+tc.code+`"`) {
				t.Errorf("no errorCode in %s", w.Body)
			}
		})
	}

	t.Run("own request: refused before applying", func(t *testing.T) {
		fake := &fakeWso2CloudRequestClient{stored: pendingWcr("APPROVER@wso2.com")}
		applier := &fakeApplier{}
		w := httptest.NewRecorder()
		NewWso2CloudRequestHandler(fake, applier).ResolveRequest(w, wcrRequest(http.MethodPost, `{"status":"approved"}`, wcrApprover))
		if w.Code != http.StatusForbidden || len(applier.applied) != 0 || fake.called(http.MethodPost) {
			t.Fatalf("status %d, applied %d, calls %+v", w.Code, len(applier.applied), fake.calls)
		}
	})

	t.Run("no longer pending: refused before applying", func(t *testing.T) {
		stored := strings.Replace(pendingWcr("engineer@wso2.com"), `"status":"pending"`, `"status":"rejected"`, 1)
		fake := &fakeWso2CloudRequestClient{stored: stored}
		applier := &fakeApplier{}
		w := httptest.NewRecorder()
		NewWso2CloudRequestHandler(fake, applier).ResolveRequest(w, wcrRequest(http.MethodPost, `{"status":"approved"}`, wcrApprover))
		if w.Code != http.StatusConflict || len(applier.applied) != 0 {
			t.Fatalf("status %d, applied %d", w.Code, len(applier.applied))
		}
	})

	t.Run("entity refusal keeps its message", func(t *testing.T) {
		fake := &fakeWso2CloudRequestClient{errs: map[string]error{http.MethodPost: &apierror.Error{StatusCode: http.StatusBadRequest, Body: `{"message":"remark is required to reject"}`}}}
		w := httptest.NewRecorder()
		NewWso2CloudRequestHandler(fake, nil).ResolveRequest(w, wcrRequest(http.MethodPost, `{"status":"rejected"}`, wcrApprover))
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "remark is required to reject") {
			t.Fatalf("status %d, body %s", w.Code, w.Body)
		}
	})

	t.Run("bad id and bad body", func(t *testing.T) {
		fake := &fakeWso2CloudRequestClient{}
		r := wcrRequest(http.MethodPost, `{"status":"approved"}`, wcrApprover)
		r.SetPathValue("id", "not-a-uuid")
		w := httptest.NewRecorder()
		NewWso2CloudRequestHandler(fake, nil).ResolveRequest(w, r)
		if w.Code != http.StatusBadRequest {
			t.Errorf("bad id: status = %d, want 400", w.Code)
		}
		w = httptest.NewRecorder()
		NewWso2CloudRequestHandler(fake, nil).ResolveRequest(w, wcrRequest(http.MethodPost, `[1]`, wcrApprover))
		if w.Code != http.StatusBadRequest || len(fake.calls) != 0 {
			t.Errorf("bad body: status = %d, calls %+v", w.Code, fake.calls)
		}
	})
}

func TestWso2CloudRequestHandler_Cancel(t *testing.T) {
	guard := NewAccessGuard(testAccessConfig())
	admin := &middleware.UserInfo{Email: "admin@wso2.com", UserID: "u-admin", Roles: []string{"test-admin"}}
	other := &middleware.UserInfo{Email: "other@wso2.com", UserID: "u-other", Roles: []string{"test-cs-engineer"}}
	requester := &middleware.UserInfo{Email: "Engineer@WSO2.com", UserID: "u-eng", Roles: []string{"test-cs-engineer"}}

	cases := []struct {
		name       string
		user       *middleware.UserInfo
		stored     string
		guard      *AccessGuard
		wantStatus int
		wantDelete bool
	}{
		{"requester", requester, pendingWcr("engineer@wso2.com"), guard, http.StatusNoContent, true},
		{"admin", admin, pendingWcr("engineer@wso2.com"), guard, http.StatusNoContent, true},
		{"someone else", other, pendingWcr("engineer@wso2.com"), guard, http.StatusForbidden, false},
		{"admin with no guard", admin, pendingWcr("engineer@wso2.com"), nil, http.StatusForbidden, false},
		{"not pending", requester, strings.Replace(pendingWcr("engineer@wso2.com"), `"pending"`, `"approved"`, 1), guard, http.StatusConflict, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fake := &fakeWso2CloudRequestClient{stored: c.stored}
			h := NewWso2CloudRequestHandler(fake, nil)
			if c.guard != nil {
				h = h.WithAccessGuard(c.guard)
			}
			w := httptest.NewRecorder()
			h.CancelRequest(w, wcrRequest(http.MethodDelete, "", c.user))
			if w.Code != c.wantStatus || fake.called(http.MethodDelete) != c.wantDelete {
				t.Fatalf("status %d, calls %+v; want %d, delete %v", w.Code, fake.calls, c.wantStatus, c.wantDelete)
			}
		})
	}

	t.Run("unknown request", func(t *testing.T) {
		fake := &fakeWso2CloudRequestClient{errs: map[string]error{http.MethodGet: &apierror.Error{StatusCode: http.StatusNotFound, Body: `{"message":"request not found"}`}}}
		w := httptest.NewRecorder()
		NewWso2CloudRequestHandler(fake, nil).WithAccessGuard(guard).CancelRequest(w, wcrRequest(http.MethodDelete, "", requester))
		if w.Code != http.StatusNotFound || fake.called(http.MethodDelete) {
			t.Fatalf("status %d, calls %+v", w.Code, fake.calls)
		}
	})
}

func TestAccessGuard_Wso2CloudApproverPermission(t *testing.T) {
	g := NewAccessGuard(testAccessConfig())
	for _, role := range []string{"test-wso2-cloud-approver", "test-admin"} {
		if status, _ := serveWithRoles(g, PermApproveWso2CloudRequest, []string{role}); status != http.StatusNoContent {
			t.Errorf("%s PermApproveWso2CloudRequest: status = %d, want 204", role, status)
		}
	}
	for _, role := range []string{"test-cs-engineer", "test-viewer", "test-security-team", "test-onboarding-lead"} {
		if status, _ := serveWithRoles(g, PermApproveWso2CloudRequest, []string{role}); status != http.StatusForbidden {
			t.Errorf("%s must not hold PermApproveWso2CloudRequest: status = %d, want 403", role, status)
		}
	}
	for _, perm := range []Permission{PermView, PermViewOperations, PermWrite, PermAdmin} {
		if status, _ := serveWithRoles(g, perm, []string{"test-wso2-cloud-approver"}); status != http.StatusForbidden {
			t.Errorf("wso2_cloud_approver must not hold permission %d: status = %d, want 403", perm, status)
		}
	}
	if got := g.RolesFor([]string{"test-wso2-cloud-approver"}); len(got) != 1 || got[0] != "wso2_cloud_approver" {
		t.Errorf("RolesFor = %v, want [wso2_cloud_approver]", got)
	}
}

func TestWso2CloudRequestHandler_CreateChecksThePlan(t *testing.T) {
	plans, err := wso2cloud.ParsePlans(`{"identity":[{"code":"identity-enterprise","label":"Enterprise"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		body    string
		wantFwd bool
	}{
		{"a configured plan", `{"type":"upgrade_organization","product":"identity","planCode":"identity-enterprise","subscriptionId":"s1"}`, true},
		{"an unknown plan", `{"type":"upgrade_organization","product":"identity","planCode":"identity-gold","subscriptionId":"s1"}`, false},
		{"a plan of another product", `{"type":"upgrade_organization","product":"integration","planCode":"identity-enterprise"}`, false},
		{"no product", `{"type":"upgrade_organization","planCode":"identity-enterprise"}`, false},
		{"a respelt key is read too", `{"type":"upgrade_organization","product":"identity","PLANCODE":"identity-gold"}`, false},
		{"no plan: entity-service says what is missing", `{"type":"upgrade_organization","product":"identity"}`, true},
		{"not an upgrade", `{"type":"lock_user","reason":"x"}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeWso2CloudRequestClient{}
			w := httptest.NewRecorder()
			NewWso2CloudRequestHandler(fake, nil).WithPlans(plans).CreateRequest(w, wcrRequest(http.MethodPost, tc.body, testCsEngineerUser))
			if forwarded := len(fake.calls) == 1; forwarded != tc.wantFwd {
				t.Fatalf("forwarded %v (status %d, %s), want %v", forwarded, w.Code, w.Body, tc.wantFwd)
			}
			if !tc.wantFwd && w.Code != http.StatusBadRequest {
				t.Fatalf("status %d, want 400", w.Code)
			}
		})
	}
}

func TestWso2CloudRequestHandler_ListSubscriptions(t *testing.T) {
	const orgID = "0F6C1A52-5B1E-4C39-9A51-2D7E1F3B8A01"
	req := func(id string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/x", nil)
		r = r.WithContext(middleware.WithUserInfo(r.Context(), testCsEngineerUser))
		r.SetPathValue("orgId", id)
		return r
	}
	t.Run("lists them", func(t *testing.T) {
		subs := &fakeSubscriptions{subs: []wso2cloud.Subscription{{ID: "sub-1", Product: "identity", PlanCode: "identity-payg", Status: "active"}}}
		w := httptest.NewRecorder()
		NewWso2CloudRequestHandler(&fakeWso2CloudRequestClient{}, nil).WithSubscriptions(subs).ListSubscriptions(w, req(orgID))
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"planCode":"identity-payg"`) || subs.orgID != strings.ToLower(orgID) {
			t.Fatalf("status %d, body %s, org %q", w.Code, w.Body, subs.orgID)
		}
	})
	for _, tc := range []struct {
		name   string
		subs   *fakeSubscriptions
		id     string
		status int
	}{
		{"not connected", &fakeSubscriptions{err: wso2cloud.ErrNotConnected}, orgID, http.StatusServiceUnavailable},
		{"no lister", nil, orgID, http.StatusServiceUnavailable},
		{"unknown organization", &fakeSubscriptions{err: &wso2cloud.APIError{Status: http.StatusNotFound, Message: "x"}}, orgID, http.StatusNotFound},
		{"other failure", &fakeSubscriptions{err: errors.New("boom")}, orgID, http.StatusBadGateway},
		{"bad id", &fakeSubscriptions{}, "org-1", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewWso2CloudRequestHandler(&fakeWso2CloudRequestClient{}, nil)
			if tc.subs != nil {
				h.WithSubscriptions(tc.subs)
			}
			w := httptest.NewRecorder()
			h.ListSubscriptions(w, req(tc.id))
			if w.Code != tc.status {
				t.Fatalf("status %d, want %d: %s", w.Code, tc.status, w.Body)
			}
		})
	}
}

func TestWso2CloudRequestHandler_ListPlans(t *testing.T) {
	plans, _ := wso2cloud.ParsePlans(`{"identity":[{"code":"identity-enterprise","label":"Enterprise"}]}`)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	r = r.WithContext(middleware.WithUserInfo(r.Context(), testCsEngineerUser))
	NewWso2CloudRequestHandler(&fakeWso2CloudRequestClient{}, nil).WithPlans(plans).ListPlans(w, r)
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"plans":{"identity":[{"code":"identity-enterprise","label":"Enterprise"}]}}` {
		t.Fatalf("status %d, body %q", w.Code, w.Body)
	}
}
