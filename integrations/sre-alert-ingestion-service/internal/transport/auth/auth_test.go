// Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com).
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

package auth

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNone_AcceptsEverything(t *testing.T) {
	if err := (None{}).Authenticate(httptest.NewRequest("POST", "/", nil), "aws"); err != nil {
		t.Errorf("None must accept every request, got %v", err)
	}
}

type rejectAll struct{}

func (rejectAll) Authenticate(*http.Request, string) error {
	return errors.New("no credential presented")
}

// Audit must let the request through and log it, so rollout can't drop alerts.
func TestAudit_NeverRejectsButLogs(t *testing.T) {
	var logs bytes.Buffer
	a := NewAudit(rejectAll{}, slog.New(slog.NewTextHandler(&logs, nil)))

	if err := a.Authenticate(httptest.NewRequest("POST", "/api/wso2/v1/sre_alert_api/aws", nil), "aws"); err != nil {
		t.Errorf("Audit must never reject, got %v", err)
	}
	for _, want := range []string{"auth would reject request", "source=aws", "no credential presented"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("log missing %q: %s", want, logs.String())
		}
	}
}

func TestAudit_SilentWhenInnerAccepts(t *testing.T) {
	var logs bytes.Buffer
	a := NewAudit(None{}, slog.New(slog.NewTextHandler(&logs, nil)))
	if err := a.Authenticate(httptest.NewRequest("POST", "/", nil), "aws"); err != nil || logs.Len() != 0 {
		t.Errorf("err = %v, logs = %q; want nil and nothing logged", err, logs.String())
	}
}
