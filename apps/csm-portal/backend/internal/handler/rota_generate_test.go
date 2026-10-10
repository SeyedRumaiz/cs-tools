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

	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/apierror"
)

type fakeRotaGenerateClient struct {
	gotCode string
	gotBody string
	resp    []byte
	err     error
}

func (f *fakeRotaGenerateClient) GenerateRotaMonth(_ context.Context, code string, body []byte) ([]byte, error) {
	f.gotCode, f.gotBody = code, string(body)
	return f.resp, f.err
}

func generateRequest(body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/team-schedule/rotas/SRE_SAAS/generate", strings.NewReader(body))
	r.SetPathValue("code", "SRE_SAAS")
	return r
}

func TestGenerateRotaMonth_ForwardsTheRequestUntouched(t *testing.T) {
	for _, c := range []struct {
		body       string
		wantStatus int
	}{
		{`{"month":"2026-11","dryRun":true}`, http.StatusOK},
		{`{"month":"2026-11"}`, http.StatusCreated},
		{`{"month":"2026-11","regenerate":true,"from":"2026-11-16"}`, http.StatusCreated},
	} {
		fake := &fakeRotaGenerateClient{resp: []byte(`{"month":"2026-11"}`)}
		w := httptest.NewRecorder()
		NewRotaGenerateHandler(fake).GenerateRotaMonth(w, withUser(generateRequest(c.body)))
		if w.Code != c.wantStatus {
			t.Fatalf("%s: status %d, want %d", c.body, w.Code, c.wantStatus)
		}
		if fake.gotCode != "SRE_SAAS" || fake.gotBody != c.body {
			t.Fatalf("forwarded %q %q", fake.gotCode, fake.gotBody)
		}
	}
}

func TestGenerateRotaMonth_NeedsAUser(t *testing.T) {
	w := httptest.NewRecorder()
	NewRotaGenerateHandler(&fakeRotaGenerateClient{}).GenerateRotaMonth(w, generateRequest(`{"month":"2026-11"}`))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status %d", w.Code)
	}
}

// A refusal keeps entity-service's words: the lead needs to know to
// regenerate, or that the month is over, or that only a lead may generate.
func TestGenerateRotaMonth_KeepsTheRefusalReason(t *testing.T) {
	for _, c := range []struct {
		status int
		msg    string
		want   string
	}{
		{http.StatusConflict, "2026-11 has already been generated; regenerate to replace the generated turns", "regenerate"},
		{http.StatusBadRequest, "that month is over; only the current month or a later one can be generated", "that month is over"},
		{http.StatusForbidden, "generating the SaaS rota needs a lead of one of its teams, or an SRE rota admin", "needs a lead"},
		{http.StatusNotFound, `rota "CRE" cannot be generated; only SRE_SAAS can`, "cannot be generated"},
		{http.StatusInternalServerError, "pq: relation does not exist", "Failed to generate the rota."},
	} {
		fake := &fakeRotaGenerateClient{err: &apierror.Error{StatusCode: c.status, Body: `{"code":1,"message":"` + strings.ReplaceAll(c.msg, `"`, `\"`) + `"}`}}
		w := httptest.NewRecorder()
		NewRotaGenerateHandler(fake).GenerateRotaMonth(w, withUser(generateRequest(`{"month":"2026-11"}`)))
		if w.Code != c.status {
			t.Fatalf("status %d, want %d", w.Code, c.status)
		}
		if !strings.Contains(w.Body.String(), c.want) {
			t.Fatalf("body %s, want it to carry %q", w.Body.String(), c.want)
		}
		if c.status == http.StatusInternalServerError && strings.Contains(w.Body.String(), "relation") {
			t.Fatal("upstream internals leaked")
		}
	}
}
