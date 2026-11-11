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

package wso2cloud

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestFromEnv(t *testing.T) {
	for _, raw := range []string{"", "  "} {
		c, err := FromEnv(envOf(map[string]string{"WSO2_CLOUD_APPLY_MODE": raw}))
		if err != nil {
			t.Fatalf("FromEnv(%q): %v", raw, err)
		}
		if err := c.Applier.Apply(context.Background(), Request{}, nil); !errors.Is(err, ErrNotConnected) {
			t.Errorf("FromEnv(%q).Apply = %v, want ErrNotConnected", raw, err)
		}
		if _, err := c.Subscriptions.ListSubscriptions(context.Background(), "o1"); !errors.Is(err, ErrNotConnected) {
			t.Errorf("FromEnv(%q).ListSubscriptions = %v, want ErrNotConnected", raw, err)
		}
	}
	c, err := FromEnv(envOf(map[string]string{"WSO2_CLOUD_APPLY_MODE": " dry_run "}))
	if err != nil {
		t.Fatalf("FromEnv(dry_run): %v", err)
	}
	if err := c.Applier.Apply(context.Background(), Request{ID: "x"}, nil); err != nil {
		t.Errorf("dry run Apply = %v, want nil", err)
	}
	if subs, err := c.Subscriptions.ListSubscriptions(context.Background(), "o1"); err != nil || len(subs) == 0 {
		t.Errorf("dry run subscriptions = %v, %v", subs, err)
	}
	for _, raw := range []string{"dry-run", "DRY_RUN", "live", "true"} {
		if _, err := FromEnv(envOf(map[string]string{"WSO2_CLOUD_APPLY_MODE": raw})); err == nil {
			t.Errorf("FromEnv(%q) accepted, want an error", raw)
		}
	}
	if _, err := FromEnv(envOf(map[string]string{"WSO2_CLOUD_APPLY_MODE": "billing_api", "WSO2_CLOUD_BILLING_BASE_URL": "https://x/admin/api/v1"})); err == nil ||
		!strings.Contains(err.Error(), "WSO2_CLOUD_CLIENT_ID") {
		t.Errorf("billing_api without its settings: %v", err)
	}
	c, err = FromEnv(envOf(map[string]string{
		"WSO2_CLOUD_APPLY_MODE": "billing_api", "WSO2_CLOUD_BILLING_BASE_URL": "https://x/admin/api/v1",
		"WSO2_CLOUD_TOKEN_URL": "https://x/token", "WSO2_CLOUD_CLIENT_ID": "id", "WSO2_CLOUD_CLIENT_SECRET": "secret",
	}))
	if err != nil {
		t.Fatalf("billing_api: %v", err)
	}
	if _, ok := c.Applier.(BillingApplier); !ok || c.Mode != ModeBillingAPI {
		t.Errorf("billing_api applier %T, mode %q", c.Applier, c.Mode)
	}
}

// fakeBilling is a billing admin API and token endpoint in one server.
type fakeBilling struct {
	mu           sync.Mutex
	srv          *httptest.Server
	tokenForm    map[string]string
	created      int
	planChanges  []PlanChange
	planStatus   int
	planBody     string
	createStatus int
	subsBody     string
}

func newFakeBilling(t *testing.T) *fakeBilling {
	f := &fakeBilling{planStatus: http.StatusOK, createStatus: http.StatusCreated}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.URL.Path == "/token" {
			_ = r.ParseForm()
			f.tokenForm = map[string]string{"grant_type": r.Form.Get("grant_type"), "audience": r.Form.Get("audience"), "scope": r.Form.Get("scope")}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"access_token":"tok","token_type":"Bearer","expires_in":3600}`)
			return
		}
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/admin/api/v1/organizations/org-1/subscriptions":
			_, _ = io.WriteString(w, f.subsBody)
		case r.Method == http.MethodPost && r.URL.Path == "/admin/api/v1/organizations/org-1/billing-accounts":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["channel"] != "wire" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			f.created++
			w.WriteHeader(f.createStatus)
			_, _ = io.WriteString(w, `{"id":"ba-new"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/admin/api/v1/organizations/org-1/subscriptions/sub-1/plan-change":
			var change PlanChange
			_ = json.NewDecoder(r.Body).Decode(&change)
			f.planChanges = append(f.planChanges, change)
			w.WriteHeader(f.planStatus)
			_, _ = io.WriteString(w, f.planBody)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeBilling) client(t *testing.T) *BillingClient {
	c, err := NewBillingClient(BillingConfig{
		BaseURL: f.srv.URL + "/admin/api/v1/", TokenURL: f.srv.URL + "/token", ClientID: "id", ClientSecret: "secret",
		Scopes: []string{"billing:organizations:read"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

type recordedAccounts struct {
	ids []string
	err error
}

func (r *recordedAccounts) RecordBillingAccount(_ context.Context, id string) error {
	r.ids = append(r.ids, id)
	return r.err
}

func strp(s string) *string { return &s }

func upgrade() Request {
	return Request{ID: "r1", Number: "WCR0000007", Type: "upgrade_organization", TargetID: "org-1",
		SubscriptionID: strp("sub-1"), PlanCode: strp("identity-enterprise"), BillingPeriod: strp("annual"),
		NotifyCustomer: true, Reason: strp("Signed contract")}
}

func TestBillingApplier_Upgrade(t *testing.T) {
	f := newFakeBilling(t)
	rec := &recordedAccounts{}
	if err := (BillingApplier{Client: f.client(t)}).Apply(context.Background(), upgrade(), rec); err != nil {
		t.Fatal(err)
	}
	if f.created != 1 || len(rec.ids) != 1 || rec.ids[0] != "ba-new" {
		t.Fatalf("created %d, recorded %v", f.created, rec.ids)
	}
	want := PlanChange{PlanCode: "identity-enterprise", BillingAccountID: "ba-new", BillingPeriod: "annual", NotifyCustomer: true,
		Notes: "CSM portal request WCR0000007: Signed contract"}
	if len(f.planChanges) != 1 || f.planChanges[0] != want {
		t.Fatalf("plan change %+v", f.planChanges)
	}
	if f.tokenForm["grant_type"] != "client_credentials" || f.tokenForm["audience"] != DefaultBillingAudience || f.tokenForm["scope"] != "billing:organizations:read" {
		t.Fatalf("token request %v", f.tokenForm)
	}
}

func TestBillingApplier_RetryReusesTheRecordedAccount(t *testing.T) {
	f := newFakeBilling(t)
	f.planStatus, f.planBody = http.StatusServiceUnavailable, `{}`
	rec := &recordedAccounts{}
	applier := BillingApplier{Client: f.client(t)}
	if err := applier.Apply(context.Background(), upgrade(), rec); err == nil {
		t.Fatal("first attempt succeeded, want an error")
	}
	// The retry reads the request again, which now carries the account.
	req := upgrade()
	req.BillingAccountID = strp(rec.ids[0])
	f.planStatus = http.StatusOK
	if err := applier.Apply(context.Background(), req, rec); err != nil {
		t.Fatal(err)
	}
	if f.created != 1 || len(rec.ids) != 1 {
		t.Fatalf("a second billing account was created: created %d, recorded %v", f.created, rec.ids)
	}
	if last := f.planChanges[len(f.planChanges)-1]; last.BillingAccountID != "ba-new" {
		t.Fatalf("retry used %q", last.BillingAccountID)
	}
}

func TestBillingApplier_Refusals(t *testing.T) {
	t.Run("a 409 keeps WSO2 Cloud's reason", func(t *testing.T) {
		f := newFakeBilling(t)
		f.planStatus, f.planBody = http.StatusConflict, `{"message":"current usage exceeds the target plan's limits"}`
		err := (BillingApplier{Client: f.client(t)}).Apply(context.Background(), upgrade(), &recordedAccounts{})
		var apiErr *APIError
		if !errors.As(err, &apiErr) || apiErr.Status != http.StatusConflict || apiErr.Message != "current usage exceeds the target plan's limits" {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("a 403 is not authorized", func(t *testing.T) {
		f := newFakeBilling(t)
		f.createStatus = http.StatusForbidden
		err := (BillingApplier{Client: f.client(t)}).Apply(context.Background(), upgrade(), &recordedAccounts{})
		if !errors.Is(err, ErrNotAuthorized) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("an account that cannot be recorded stops before the plan change", func(t *testing.T) {
		f := newFakeBilling(t)
		err := (BillingApplier{Client: f.client(t)}).Apply(context.Background(), upgrade(), &recordedAccounts{err: errors.New("db down")})
		if err == nil || len(f.planChanges) != 0 {
			t.Fatalf("got %v, plan changes %d", err, len(f.planChanges))
		}
	})
	t.Run("other request types are not available", func(t *testing.T) {
		err := (BillingApplier{}).Apply(context.Background(), Request{Type: "lock_user"}, nil)
		if !errors.Is(err, ErrNotAvailable) {
			t.Fatalf("got %v", err)
		}
	})
}

func TestBillingClient_ListSubscriptions(t *testing.T) {
	for name, body := range map[string]string{
		"bare array": `[{"subscription_id":"sub-1","product":"identity","plan_code":"identity-payg","status":"active"},{"id":"sub-2","product_code":"integration","plan":{"code":"int-free"},"status":"trial","trial_end":"2027-01-31"},{"status":"active"}]`,
		"wrapped":    `{"data":[{"subscription_id":"sub-1","product":"identity","plan_code":"identity-payg","status":"active"},{"id":"sub-2","product_code":"integration","plan":{"code":"int-free"},"status":"trial","trial_end":"2027-01-31"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			f := newFakeBilling(t)
			f.subsBody = body
			subs, err := f.client(t).ListSubscriptions(context.Background(), "org-1")
			if err != nil {
				t.Fatal(err)
			}
			want := []Subscription{
				{ID: "sub-1", Product: "identity", PlanCode: "identity-payg", Status: "active"},
				{ID: "sub-2", Product: "integration", PlanCode: "int-free", Status: "trial", TrialEnd: "2027-01-31"},
			}
			if len(subs) != 2 || subs[0] != want[0] || subs[1] != want[1] {
				t.Fatalf("got %+v", subs)
			}
		})
	}
}

func TestParsePlans(t *testing.T) {
	plans, err := ParsePlans(`{"identity":[{"code":"identity-enterprise","label":"Enterprise"},{"code":"identity-ent-2"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if !plans.Has("identity", "identity-enterprise") || plans.Has("identity", "nope") || plans.Has("other", "identity-enterprise") {
		t.Fatalf("Has wrong: %v", plans)
	}
	if plans["identity"][1].Label != "identity-ent-2" {
		t.Fatalf("label default: %v", plans)
	}
	if empty, err := ParsePlans("  "); err != nil || len(empty) != 0 {
		t.Fatalf("empty: %v %v", empty, err)
	}
	for _, bad := range []string{`[]`, `{"":[{"code":"x"}]}`, `{"p":[{"code":" "}]}`, `{"p":[{"code":"x"},{"code":"x"}]}`} {
		if _, err := ParsePlans(bad); err == nil {
			t.Errorf("ParsePlans(%s) accepted", bad)
		}
	}
}
