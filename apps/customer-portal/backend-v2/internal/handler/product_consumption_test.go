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
	"strings"
	"testing"

	"github.com/wso2-open-operations/cs-tools/apps/customer-portal/backend-v2/internal/apierror"
	"github.com/wso2-open-operations/cs-tools/apps/customer-portal/backend-v2/internal/entity"
	"github.com/wso2-open-operations/cs-tools/apps/customer-portal/backend-v2/internal/productconsumption"
)

const (
	testLicenseProjectID    = "11111111-1111-1111-1111-111111111111"
	testLicenseDeploymentID = "22222222-2222-2222-2222-222222222222"
)

// fakeLicenseProductConsumptionClient records the LicenseDownloadRequest it
// was called with and returns a fixed license (or error).
type fakeLicenseProductConsumptionClient struct {
	gotReq  productconsumption.LicenseDownloadRequest
	calls   int
	license productconsumption.License
	err     error
}

func (f *fakeLicenseProductConsumptionClient) ProcessLicenseDownload(_ context.Context, req productconsumption.LicenseDownloadRequest) (productconsumption.License, error) {
	f.gotReq = req
	f.calls++
	return f.license, f.err
}

func (f *fakeLicenseProductConsumptionClient) ImportDeploymentUsage(context.Context, string, []byte) (productconsumption.ImportUsageResponse, error) {
	return productconsumption.ImportUsageResponse{}, nil
}

// fakeProjectAccessChecker controls whether GetProject succeeds, standing in
// for entity-service's own project-access gate.
type fakeProjectAccessChecker struct {
	err   error
	calls int
}

func (f *fakeProjectAccessChecker) GetProject(context.Context, string) (entity.ProjectDetailsView, error) {
	f.calls++
	return entity.ProjectDetailsView{}, f.err
}

func newLicenseMux(product productConsumptionClient, entityClient entityProjectAccessChecker) *http.ServeMux {
	h := NewProductConsumptionHandler(product, entityClient)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /projects/{projectId}/deployments/{deploymentId}/license", h.GetDeploymentLicense)
	return mux
}

// TestGetDeploymentLicense_RejectsWhenProjectAccessFails confirms the
// project-access gate runs before ProcessLicenseDownload is ever called —
// entity-service's GetProject failing must not still provision anything.
func TestGetDeploymentLicense_RejectsWhenProjectAccessFails(t *testing.T) {
	product := &fakeLicenseProductConsumptionClient{}
	entityClient := &fakeProjectAccessChecker{err: apierror.NewUpstreamError(http.StatusNotFound, []byte(`{"message":"project not found"}`))}

	mux := newLicenseMux(product, entityClient)
	req := authedRequest(http.MethodPost, "/projects/"+testLicenseProjectID+"/deployments/"+testLicenseDeploymentID+"/license", "")
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	if product.calls != 0 {
		t.Fatalf("ProcessLicenseDownload calls = %d, want 0 — project access failed, nothing should have been provisioned", product.calls)
	}
}

// TestGetDeploymentLicense_ForwardsRequestAndReturnsLicense is the success
// path: the caller's email and the path's project/deployment ids reach
// ProcessLicenseDownload unchanged, and the signed subscription data comes
// back through dto.MapLicense byte for byte.
func TestGetDeploymentLicense_ForwardsRequestAndReturnsLicense(t *testing.T) {
	product := &fakeLicenseProductConsumptionClient{
		license: productconsumption.License{
			SubscriptionData: json.RawMessage(`{"clientId":"abc","usageDataPublishingUrl":"https://example.com"}`),
			Signature:        "sig-value",
		},
	}
	entityClient := &fakeProjectAccessChecker{}

	mux := newLicenseMux(product, entityClient)
	req := authedRequest(http.MethodPost, "/projects/"+testLicenseProjectID+"/deployments/"+testLicenseDeploymentID+"/license", "")
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if entityClient.calls != 1 {
		t.Fatalf("GetProject calls = %d, want 1", entityClient.calls)
	}
	if product.gotReq.Email != "customer@example.com" {
		t.Errorf("Email = %q, want the authenticated caller's email", product.gotReq.Email)
	}
	if product.gotReq.ProjectID != testLicenseProjectID {
		t.Errorf("ProjectID = %q, want %q", product.gotReq.ProjectID, testLicenseProjectID)
	}
	if product.gotReq.DeploymentID != testLicenseDeploymentID {
		t.Errorf("DeploymentID = %q, want %q", product.gotReq.DeploymentID, testLicenseDeploymentID)
	}

	var got struct {
		SubscriptionData json.RawMessage `json:"subscriptionData"`
		Signature        string          `json:"signature"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Signature != "sig-value" {
		t.Errorf("Signature = %q, want %q", got.Signature, "sig-value")
	}
	// usageDataPublishingUrl must survive verbatim — it's one of the fields
	// ServiceNow signs, and a closed struct anywhere in this path would drop it.
	if !strings.Contains(string(got.SubscriptionData), "usageDataPublishingUrl") {
		t.Errorf("SubscriptionData = %s, want it to still contain usageDataPublishingUrl", got.SubscriptionData)
	}
}

// TestGetDeploymentLicense_RejectsInvalidUUID confirms the path-parameter
// UUID guard runs before either upstream call.
func TestGetDeploymentLicense_RejectsInvalidUUID(t *testing.T) {
	product := &fakeLicenseProductConsumptionClient{}
	entityClient := &fakeProjectAccessChecker{}

	mux := newLicenseMux(product, entityClient)
	req := authedRequest(http.MethodPost, "/projects/not-a-uuid/deployments/"+testLicenseDeploymentID+"/license", "")
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if entityClient.calls != 0 || product.calls != 0 {
		t.Fatalf("GetProject calls = %d, ProcessLicenseDownload calls = %d, want 0 and 0", entityClient.calls, product.calls)
	}
}
