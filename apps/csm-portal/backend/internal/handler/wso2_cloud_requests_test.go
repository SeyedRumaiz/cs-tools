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
	// byPath answers a call to that path (before stored / result), and
	// pathErrs fails it.
	byPath   map[string]string
	pathErrs map[string]error
}

func (f *fakeWso2CloudRequestClient) Wso2CloudRequestCall(_ context.Context, method, path string, body []byte) ([]byte, error) {
	f.calls = append(f.calls, wcrCall{method, path, string(body)})
	if err := f.pathErrs[path]; err != nil {
		return nil, err
	}
	if out, ok := f.byPath[path]; ok {
		return []byte(out), nil
	}
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
	subs   []wso2cloud.Subscription
	err    error
	orgID  string
	region string
	calls  int
}

func (f *fakeSubscriptions) ListSubscriptions(_ context.Context, region, orgID string) ([]wso2cloud.Subscription, error) {
	f.orgID, f.region = orgID, region
	f.calls++
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
		{"no region", wso2cloud.ErrRegionRequired, http.StatusConflict, "names no WSO2 Cloud region", ""},
		{"a region not configured here", &wso2cloud.RegionError{Region: "eu", Regions: []string{"us"}}, http.StatusConflict, "names no WSO2 Cloud region", ""},
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
		NewWso2CloudRequestHandler(&fakeWso2CloudRequestClient{}, nil).WithSubscriptions(subs, nil).ListSubscriptions(w, req(orgID))
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
				h.WithSubscriptions(tc.subs, nil)
			}
			w := httptest.NewRecorder()
			h.ListSubscriptions(w, req(tc.id))
			if w.Code != tc.status {
				t.Fatalf("status %d, want %d: %s", w.Code, tc.status, w.Body)
			}
		})
	}
}

func TestWso2CloudRequestHandler_ListSubscriptionsByRegion(t *testing.T) {
	const orgID = "0f6c1a52-5b1e-4c39-9a51-2d7e1f3b8a01"
	both := []string{"us", "eu"}
	cases := []struct {
		name       string
		regions    []string
		query      string
		err        error
		status     int
		wantRegion string
		contains   string
	}{
		{"the region asked for", both, "?region=EU", nil, http.StatusOK, "eu", ""},
		{"the only region when none is named", []string{"us"}, "", nil, http.StatusOK, "us", ""},
		{"no region with two", both, "", nil, http.StatusBadRequest, "", "Choose the WSO2 Cloud region"},
		{"a region this environment lacks", []string{"us"}, "?region=eu", nil, http.StatusBadRequest, "", "EU region is not available in this environment. Choose one of: US."},
		{"an unknown region", both, "?region=apac", nil, http.StatusBadRequest, "", "APAC region is not available"},
		{"not in that region (404)", both, "?region=eu", &wso2cloud.APIError{Status: http.StatusNotFound, Message: "x"}, http.StatusNotFound, "eu", "not found in the EU region"},
		{"not in that region (400)", both, "?region=eu", &wso2cloud.APIError{Status: http.StatusBadRequest, Message: "x"}, http.StatusNotFound, "eu", "not found in the EU region"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			subs := &fakeSubscriptions{err: tc.err}
			r := httptest.NewRequest(http.MethodGet, "/x"+tc.query, nil)
			r = r.WithContext(middleware.WithUserInfo(r.Context(), testCsEngineerUser))
			r.SetPathValue("orgId", orgID)
			w := httptest.NewRecorder()
			NewWso2CloudRequestHandler(&fakeWso2CloudRequestClient{}, nil).WithSubscriptions(subs, tc.regions).ListSubscriptions(w, r)
			if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.contains) {
				t.Fatalf("status %d, body %s; want %d containing %q", w.Code, w.Body, tc.status, tc.contains)
			}
			if tc.wantRegion == "" && subs.calls != 0 {
				t.Fatal("WSO2 Cloud was called although the region was refused")
			}
			if tc.wantRegion != "" && subs.region != tc.wantRegion {
				t.Fatalf("listed in %q, want %q", subs.region, tc.wantRegion)
			}
		})
	}
}

func TestWso2CloudRequestHandler_ListRegions(t *testing.T) {
	for _, tc := range []struct {
		regions []string
		want    string
	}{{[]string{"us", "eu"}, `{"regions":["us","eu"]}`}, {nil, `{"regions":[]}`}} {
		r := httptest.NewRequest(http.MethodGet, "/x", nil)
		r = r.WithContext(middleware.WithUserInfo(r.Context(), testCsEngineerUser))
		w := httptest.NewRecorder()
		NewWso2CloudRequestHandler(&fakeWso2CloudRequestClient{}, nil).WithSubscriptions(&fakeSubscriptions{}, tc.regions).ListRegions(w, r)
		if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != tc.want {
			t.Fatalf("status %d, body %s; want %s", w.Code, w.Body, tc.want)
		}
	}
}

func TestWso2CloudRequestHandler_CreateChecksTheRegion(t *testing.T) {
	const upgrade = `{"type":"upgrade_organization","product":"identity","planCode":"identity-enterprise","subscriptionId":"s1","upgradeKind":"internal"`
	plans, _ := wso2cloud.ParsePlans(`{"identity":[{"code":"identity-enterprise","label":"Enterprise"}]}`)
	cases := []struct {
		name    string
		regions []string
		body    string
		wantFwd bool
	}{
		{"a region this environment has", []string{"us", "eu"}, upgrade + `,"region":"eu"}`, true},
		{"no region", []string{"us", "eu"}, upgrade + `}`, false},
		{"no region with one region", []string{"us"}, upgrade + `}`, false},
		{"a region this environment lacks", []string{"us"}, upgrade + `,"region":"eu"}`, false},
		{"not connected: entity-service decides", nil, upgrade + `}`, true},
		{"not an upgrade", []string{"us"}, `{"type":"lock_user","reason":"x"}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeWso2CloudRequestClient{}
			w := httptest.NewRecorder()
			NewWso2CloudRequestHandler(fake, nil).WithPlans(plans).WithSubscriptions(&fakeSubscriptions{}, tc.regions).
				CreateRequest(w, wcrRequest(http.MethodPost, tc.body, testCsEngineerUser))
			if forwarded := len(fake.calls) == 1; forwarded != tc.wantFwd {
				t.Fatalf("forwarded %v (status %d, %s), want %v", forwarded, w.Code, w.Body, tc.wantFwd)
			}
			if !tc.wantFwd && w.Code != http.StatusBadRequest {
				t.Fatalf("status %d, want 400", w.Code)
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

func customerUpgradeWcr(requestedBy string) string {
	return `{"id":"` + wcrID + `","number":"WCR0000002","type":"upgrade_organization","targetId":"org-1","targetLabel":"acme",` +
		`"subscriptionId":"sub-1","planCode":"enterprise","upgradeKind":"customer","status":"pending","requestedBy":"` + requestedBy + `"}`
}

func (f *fakeWso2CloudRequestClient) calledPath(method, path string) bool {
	for _, c := range f.calls {
		if c.method == method && c.path == path {
			return true
		}
	}
	return false
}

func TestWso2CloudRequestHandler_ResolveCustomerUpgrade(t *testing.T) {
	base := "/wso2-cloud-requests/" + wcrID
	active := map[string]string{
		base + "/salesforce-account": `{"id":"001000000000001AAA","hasActiveSubscription":true}`,
		base + "/salesforce-link":    `{"id":"` + wcrID + `","salesforceLinkStatus":"linked"}`,
	}

	t.Run("checked, applied, recorded, then linked", func(t *testing.T) {
		fake := &fakeWso2CloudRequestClient{stored: customerUpgradeWcr("engineer@wso2.com"), byPath: active}
		applier := &fakeApplier{}
		w := httptest.NewRecorder()
		NewWso2CloudRequestHandler(fake, applier).ResolveRequest(w, wcrRequest(http.MethodPost, `{"status":"approved"}`, wcrApprover))
		if w.Code != http.StatusOK || len(applier.applied) != 1 {
			t.Fatalf("status %d, applied %d: %s", w.Code, len(applier.applied), w.Body)
		}
		var order []string
		for _, c := range fake.calls {
			order = append(order, c.method+" "+strings.TrimPrefix(c.path, base))
		}
		want := []string{"GET ", "GET /salesforce-account", "POST /resolve", "PUT /salesforce-link"}
		if fmt.Sprint(order) != fmt.Sprint(want) {
			t.Fatalf("calls %v, want %v", order, want)
		}
		if !strings.Contains(w.Body.String(), `"salesforceLinkStatus":"linked"`) {
			t.Errorf("body = %s, want the linked request", w.Body)
		}
	})

	t.Run("a lapsed subscription is refused before anything is applied", func(t *testing.T) {
		fake := &fakeWso2CloudRequestClient{stored: customerUpgradeWcr("engineer@wso2.com"), byPath: map[string]string{
			base + "/salesforce-account": `{"id":"001000000000001AAA","hasActiveSubscription":false}`,
		}}
		applier := &fakeApplier{}
		w := httptest.NewRecorder()
		NewWso2CloudRequestHandler(fake, applier).ResolveRequest(w, wcrRequest(http.MethodPost, `{"status":"approved"}`, wcrApprover))
		if w.Code != http.StatusConflict || len(applier.applied) != 0 || fake.called(http.MethodPost) {
			t.Fatalf("status %d, applied %d, calls %+v", w.Code, len(applier.applied), fake.calls)
		}
		if !strings.Contains(w.Body.String(), errorCodeWso2CloudNoSubscription) {
			t.Errorf("body = %s, want the errorCode", w.Body)
		}
	})

	t.Run("a failed check refuses, nothing applied", func(t *testing.T) {
		fake := &fakeWso2CloudRequestClient{stored: customerUpgradeWcr("engineer@wso2.com"),
			pathErrs: map[string]error{base + "/salesforce-account": &apierror.Error{StatusCode: http.StatusServiceUnavailable}}}
		applier := &fakeApplier{}
		w := httptest.NewRecorder()
		NewWso2CloudRequestHandler(fake, applier).ResolveRequest(w, wcrRequest(http.MethodPost, `{"status":"approved"}`, wcrApprover))
		if w.Code < 500 || len(applier.applied) != 0 || fake.called(http.MethodPost) {
			t.Fatalf("status %d, applied %d, calls %+v", w.Code, len(applier.applied), fake.calls)
		}
	})

	t.Run("a failed link call still answers 200 with the approval", func(t *testing.T) {
		fake := &fakeWso2CloudRequestClient{stored: customerUpgradeWcr("engineer@wso2.com"), byPath: map[string]string{
			base + "/salesforce-account": `{"hasActiveSubscription":true}`,
		}, pathErrs: map[string]error{base + "/salesforce-link": errors.New("timeout")}}
		w := httptest.NewRecorder()
		NewWso2CloudRequestHandler(fake, &fakeApplier{}).ResolveRequest(w, wcrRequest(http.MethodPost, `{"status":"approved"}`, wcrApprover))
		if w.Code != http.StatusOK || w.Body.String() != `{"ok":true}` {
			t.Fatalf("status %d body %s; want 200 with the resolve answer", w.Code, w.Body)
		}
	})

	t.Run("an internal upgrade never touches Salesforce", func(t *testing.T) {
		stored := strings.Replace(customerUpgradeWcr("engineer@wso2.com"), `"upgradeKind":"customer"`, `"upgradeKind":"internal"`, 1)
		fake := &fakeWso2CloudRequestClient{stored: stored}
		w := httptest.NewRecorder()
		NewWso2CloudRequestHandler(fake, &fakeApplier{}).ResolveRequest(w, wcrRequest(http.MethodPost, `{"status":"approved"}`, wcrApprover))
		if w.Code != http.StatusOK || fake.calledPath(http.MethodGet, base+"/salesforce-account") || fake.called(http.MethodPut) {
			t.Fatalf("status %d, calls %+v", w.Code, fake.calls)
		}
	})
}

func TestWso2CloudRequestHandler_SalesforceRoutes(t *testing.T) {
	t.Run("retry link forwards a PUT", func(t *testing.T) {
		fake := &fakeWso2CloudRequestClient{}
		w := httptest.NewRecorder()
		NewWso2CloudRequestHandler(fake, nil).LinkSalesforce(w, wcrRequest(http.MethodPut, "", wcrApprover))
		if w.Code != http.StatusOK || !fake.calledPath(http.MethodPut, "/wso2-cloud-requests/"+wcrID+"/salesforce-link") {
			t.Fatalf("status %d, calls %+v", w.Code, fake.calls)
		}
	})
	t.Run("organization status, lower-cased", func(t *testing.T) {
		fake := &fakeWso2CloudRequestClient{stored: `{"record":null,"account":null}`}
		r := wcrRequest(http.MethodGet, "", testCsEngineerUser)
		r.SetPathValue("orgId", "6B1F0C2E-8F6B-4F0A-9D5C-5B1F2A3C4D5E")
		w := httptest.NewRecorder()
		NewWso2CloudRequestHandler(fake, nil).GetOrganizationSalesforce(w, r)
		if w.Code != http.StatusOK || !fake.calledPath(http.MethodGet, "/wso2-cloud/organizations/6b1f0c2e-8f6b-4f0a-9d5c-5b1f2a3c4d5e/salesforce") {
			t.Fatalf("status %d, calls %+v", w.Code, fake.calls)
		}
		r.SetPathValue("orgId", "acme")
		w = httptest.NewRecorder()
		NewWso2CloudRequestHandler(fake, nil).GetOrganizationSalesforce(w, r)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("not a UUID: status %d", w.Code)
		}
	})
	t.Run("account check validates the id before any call", func(t *testing.T) {
		for id, want := range map[string]int{"001000000000001": http.StatusOK, "001000000000001AAA": http.StatusOK, "006000000000001": http.StatusBadRequest, "001%2F..": http.StatusBadRequest} {
			fake := &fakeWso2CloudRequestClient{stored: `{"hasActiveSubscription":true}`}
			r := wcrRequest(http.MethodGet, "", testCsEngineerUser)
			r.SetPathValue("accountId", id)
			w := httptest.NewRecorder()
			NewWso2CloudRequestHandler(fake, nil).GetSalesforceAccount(w, r)
			if w.Code != want {
				t.Errorf("%s: status %d, want %d", id, w.Code, want)
			}
			if want == http.StatusBadRequest && len(fake.calls) != 0 {
				t.Errorf("%s: called upstream", id)
			}
		}
	})
	t.Run("the subscription refusal from entity-service keeps its message and code", func(t *testing.T) {
		fake := &fakeWso2CloudRequestClient{errs: map[string]error{http.MethodPost: &apierror.Error{StatusCode: http.StatusConflict,
			Body: `{"message":"this Salesforce account has no active subscription","errorCode":"wso2_cloud_no_subscription"}`}}}
		w := httptest.NewRecorder()
		NewWso2CloudRequestHandler(fake, nil).CreateRequest(w, wcrRequest(http.MethodPost, `{"type":"upgrade_organization","upgradeKind":"customer"}`, testCsEngineerUser))
		if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "wso2_cloud_no_subscription") || !strings.Contains(w.Body.String(), "no active subscription") {
			t.Fatalf("status %d body %s", w.Code, w.Body)
		}
	})
}

func TestWso2CloudRequestHandler_CustomerUpgradeNeedsNoApproval(t *testing.T) {
	base := "/wso2-cloud-requests/" + wcrID
	const raise = `{"type":"upgrade_organization","upgradeKind":"customer"}`
	created := func(extra map[string]string) *fakeWso2CloudRequestClient {
		byPath := map[string]string{
			"/wso2-cloud-requests":       customerUpgradeWcr("engineer@wso2.com"),
			base + "/salesforce-account": `{"hasActiveSubscription":true}`,
			base + "/salesforce-link":    `{"id":"` + wcrID + `","status":"approved","salesforceLinkStatus":"linked"}`,
		}
		for k, v := range extra {
			byPath[k] = v
		}
		return &fakeWso2CloudRequestClient{byPath: byPath}
	}
	order := func(f *fakeWso2CloudRequestClient) string {
		var out []string
		for _, c := range f.calls {
			out = append(out, c.method+" "+strings.TrimPrefix(c.path, base))
		}
		return fmt.Sprint(out)
	}

	t.Run("raised, applied and linked at once, as the requester", func(t *testing.T) {
		fake := created(nil)
		applier := &fakeApplier{}
		w := httptest.NewRecorder()
		NewWso2CloudRequestHandler(fake, applier).CreateRequest(w, wcrRequest(http.MethodPost, raise, testCsEngineerUser))
		if w.Code != http.StatusCreated || len(applier.applied) != 1 {
			t.Fatalf("status %d, applied %d: %s", w.Code, len(applier.applied), w.Body)
		}
		want := "[POST /wso2-cloud-requests GET /salesforce-account POST /resolve PUT /salesforce-link]"
		if got := order(fake); got != want {
			t.Fatalf("calls %s, want %s", got, want)
		}
		if !strings.Contains(w.Body.String(), `"salesforceLinkStatus":"linked"`) {
			t.Errorf("body = %s", w.Body)
		}
	})

	t.Run("a subscription that lapsed in between: stays pending, nothing applied", func(t *testing.T) {
		fake := created(map[string]string{base + "/salesforce-account": `{"hasActiveSubscription":false}`})
		applier := &fakeApplier{}
		w := httptest.NewRecorder()
		NewWso2CloudRequestHandler(fake, applier).CreateRequest(w, wcrRequest(http.MethodPost, raise, testCsEngineerUser))
		if w.Code != http.StatusConflict || len(applier.applied) != 0 || fake.calledPath(http.MethodPost, base+"/resolve") {
			t.Fatalf("status %d, applied %d, calls %s", w.Code, len(applier.applied), order(fake))
		}
	})

	t.Run("cannot be applied: stays pending with the reason", func(t *testing.T) {
		fake := created(nil)
		w := httptest.NewRecorder()
		NewWso2CloudRequestHandler(fake, nil).CreateRequest(w, wcrRequest(http.MethodPost, raise, testCsEngineerUser))
		if w.Code != http.StatusServiceUnavailable || fake.calledPath(http.MethodPost, base+"/resolve") {
			t.Fatalf("status %d, calls %s", w.Code, order(fake))
		}
		if !strings.Contains(w.Body.String(), "stays pending") {
			t.Errorf("body = %s", w.Body)
		}
	})

	t.Run("an internal upgrade waits for an approver", func(t *testing.T) {
		stored := strings.Replace(customerUpgradeWcr("engineer@wso2.com"), `"upgradeKind":"customer"`, `"upgradeKind":"internal"`, 1)
		fake := created(map[string]string{"/wso2-cloud-requests": stored})
		applier := &fakeApplier{}
		w := httptest.NewRecorder()
		NewWso2CloudRequestHandler(fake, applier).CreateRequest(w, wcrRequest(http.MethodPost, `{"type":"upgrade_organization","upgradeKind":"internal"}`, testCsEngineerUser))
		if w.Code != http.StatusCreated || len(applier.applied) != 0 || len(fake.calls) != 1 {
			t.Fatalf("status %d, applied %d, calls %s", w.Code, len(applier.applied), order(fake))
		}
	})

	t.Run("the requester may approve their own pending customer upgrade, not an internal one", func(t *testing.T) {
		fake := &fakeWso2CloudRequestClient{stored: customerUpgradeWcr(wcrApprover.Email), byPath: map[string]string{
			base + "/salesforce-account": `{"hasActiveSubscription":true}`,
		}}
		w := httptest.NewRecorder()
		NewWso2CloudRequestHandler(fake, &fakeApplier{}).ResolveRequest(w, wcrRequest(http.MethodPost, `{"status":"approved"}`, wcrApprover))
		if w.Code != http.StatusOK {
			t.Fatalf("customer: status %d: %s", w.Code, w.Body)
		}
		internal := strings.Replace(customerUpgradeWcr(wcrApprover.Email), `"upgradeKind":"customer"`, `"upgradeKind":"internal"`, 1)
		fake = &fakeWso2CloudRequestClient{stored: internal}
		w = httptest.NewRecorder()
		NewWso2CloudRequestHandler(fake, &fakeApplier{}).ResolveRequest(w, wcrRequest(http.MethodPost, `{"status":"approved"}`, wcrApprover))
		if w.Code != http.StatusForbidden {
			t.Fatalf("internal: status %d", w.Code)
		}
	})
}
