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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
)

// TestDecodeCreateIncident_AcceptsCorrelationIDAndEnvironment checks the strict decoder no longer 400s the body alert-core-service sends.
func TestDecodeCreateIncident_AcceptsCorrelationIDAndEnvironment(t *testing.T) {
	body := `{"callerId":"11111111-1111-1111-1111-111111111111","category":"SERVICE_INTERRUPTION","serviceId":"22222222-2222-2222-2222-222222222222","impact":"HIGH","urgency":"HIGH","subject":"s","workNotes":"n","contactType":"AZURE","correlationId":"[fp:abc123def456:1]","environment":"Staging"}`
	r := httptest.NewRequest(http.MethodPost, "/incidents", strings.NewReader(body))
	w := httptest.NewRecorder()

	var req domain.CreateIncidentRequest
	if !decodeRequest(w, r, &req) {
		t.Fatalf("decode rejected the body: %d %s", w.Code, w.Body.String())
	}
	if req.CorrelationID == nil || *req.CorrelationID != "[fp:abc123def456:1]" {
		t.Fatalf("correlationId: got %v", req.CorrelationID)
	}
	if req.Environment == nil || *req.Environment != "Staging" {
		t.Fatalf("environment: got %v", req.Environment)
	}
}

// TestDecodeSearchIncidents_AcceptsCorrelationIDFilter checks the strict decoder accepts filters.correlationId.
func TestDecodeSearchIncidents_AcceptsCorrelationIDFilter(t *testing.T) {
	body := `{"filters":{"correlationId":"[fp:abc123def456:1]"},"pagination":{"limit":1,"offset":0}}`
	r := httptest.NewRequest(http.MethodPost, "/incidents/search", strings.NewReader(body))
	w := httptest.NewRecorder()

	var req domain.SearchIncidentsRequest
	if !decodeRequest(w, r, &req) {
		t.Fatalf("decode rejected the body: %d %s", w.Code, w.Body.String())
	}
	if req.Filters.CorrelationID == nil || *req.Filters.CorrelationID != "[fp:abc123def456:1]" {
		t.Fatalf("filters.correlationId: got %v", req.Filters.CorrelationID)
	}
}
