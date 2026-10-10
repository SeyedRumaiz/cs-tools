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
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/apierror"
	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/middleware"
	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/risk"
	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/servicenow"
)

// fakeRiskClient is a hand-rolled riskClient test double: only the
// methods a given test needs are set; every other call panics via a nil
// func, which surfaces immediately as a test failure rather than a silent
// zero value. riskID/actionItemID are UUID strings -- entity-service's own
// primary keys -- not the auto-increment ints the old MySQL-backed client
// used, and there is no longer an explicit email parameter: entity-service
// resolves the actor itself from the forwarded x-user-id-token.
type fakeRiskClient struct {
	openProjectRisk           func(ctx context.Context, projectSysID, comment string) (*risk.ProjectRisk, error)
	closeProjectRisk          func(ctx context.Context, riskID, comment string) (*risk.ProjectRisk, error)
	markProjectHealthy        func(ctx context.Context, projectSysID string, comment *string) (*risk.HealthStatusRecord, error)
	revertProjectHealth       func(ctx context.Context, projectSysID string) (*risk.HealthStatusRecord, error)
	getAccountHealthStatus    func(ctx context.Context, accountSysID string) ([]risk.ProjectHealthStatus, error)
	getAccountHealthSummary   func(ctx context.Context, accountSysID string) (*risk.HealthSummary, error)
	getProjectRiskHistory     func(ctx context.Context, projectSysID string) ([]risk.ProjectRisk, error)
	getBatchHealthSummaries   func(ctx context.Context, accountSysIDs []string) (map[string]string, error)
	getAccountsByHealthStatus func(ctx context.Context, healthStatus string) ([]string, error)
	initProjectHealthRows     func(ctx context.Context, projectSysIDs []string, accountSysID string) error
	createActionItem          func(ctx context.Context, riskID string, payload risk.CreateActionItemRequest) (*risk.RiskActionItem, error)
	updateActionItemStatus    func(ctx context.Context, actionItemID, newStatus string, resolutionComment *string) (*risk.RiskActionItem, error)
	updateActionItem          func(ctx context.Context, actionItemID string, payload risk.UpdateActionItemRequest) (*risk.RiskActionItem, error)
	getActionItemsByRisk      func(ctx context.Context, riskID string, statusFilter *string) ([]risk.RiskActionItem, error)
	getActionItemsByAccount   func(ctx context.Context, accountSysID string, projectSysID, statusFilter *string) ([]risk.RiskActionItem, error)
	createActionItemComment   func(ctx context.Context, actionItemID, comment string) (*risk.ActionItemComment, error)
	getActionItemComments     func(ctx context.Context, actionItemID string) ([]risk.ActionItemComment, error)
}

func (f *fakeRiskClient) OpenProjectRisk(ctx context.Context, projectSysID, comment string) (*risk.ProjectRisk, error) {
	return f.openProjectRisk(ctx, projectSysID, comment)
}
func (f *fakeRiskClient) CloseProjectRisk(ctx context.Context, riskID, comment string) (*risk.ProjectRisk, error) {
	return f.closeProjectRisk(ctx, riskID, comment)
}
func (f *fakeRiskClient) MarkProjectHealthy(ctx context.Context, projectSysID string, comment *string) (*risk.HealthStatusRecord, error) {
	return f.markProjectHealthy(ctx, projectSysID, comment)
}
func (f *fakeRiskClient) RevertProjectHealth(ctx context.Context, projectSysID string) (*risk.HealthStatusRecord, error) {
	return f.revertProjectHealth(ctx, projectSysID)
}
func (f *fakeRiskClient) GetAccountHealthStatus(ctx context.Context, accountSysID string) ([]risk.ProjectHealthStatus, error) {
	return f.getAccountHealthStatus(ctx, accountSysID)
}
func (f *fakeRiskClient) GetAccountHealthSummary(ctx context.Context, accountSysID string) (*risk.HealthSummary, error) {
	return f.getAccountHealthSummary(ctx, accountSysID)
}
func (f *fakeRiskClient) GetProjectRiskHistory(ctx context.Context, projectSysID string) ([]risk.ProjectRisk, error) {
	return f.getProjectRiskHistory(ctx, projectSysID)
}
func (f *fakeRiskClient) GetBatchHealthSummaries(ctx context.Context, accountSysIDs []string) (map[string]string, error) {
	return f.getBatchHealthSummaries(ctx, accountSysIDs)
}
func (f *fakeRiskClient) GetAccountsByHealthStatus(ctx context.Context, healthStatus string) ([]string, error) {
	return f.getAccountsByHealthStatus(ctx, healthStatus)
}
func (f *fakeRiskClient) InitProjectHealthRows(ctx context.Context, projectSysIDs []string, accountSysID string) error {
	return f.initProjectHealthRows(ctx, projectSysIDs, accountSysID)
}
func (f *fakeRiskClient) CreateActionItem(ctx context.Context, riskID string, payload risk.CreateActionItemRequest) (*risk.RiskActionItem, error) {
	return f.createActionItem(ctx, riskID, payload)
}
func (f *fakeRiskClient) UpdateActionItemStatus(ctx context.Context, actionItemID, newStatus string, resolutionComment *string) (*risk.RiskActionItem, error) {
	return f.updateActionItemStatus(ctx, actionItemID, newStatus, resolutionComment)
}
func (f *fakeRiskClient) UpdateActionItem(ctx context.Context, actionItemID string, payload risk.UpdateActionItemRequest) (*risk.RiskActionItem, error) {
	return f.updateActionItem(ctx, actionItemID, payload)
}
func (f *fakeRiskClient) GetActionItemsByRisk(ctx context.Context, riskID string, statusFilter *string) ([]risk.RiskActionItem, error) {
	return f.getActionItemsByRisk(ctx, riskID, statusFilter)
}
func (f *fakeRiskClient) GetActionItemsByAccount(ctx context.Context, accountSysID string, projectSysID, statusFilter *string) ([]risk.RiskActionItem, error) {
	return f.getActionItemsByAccount(ctx, accountSysID, projectSysID, statusFilter)
}
func (f *fakeRiskClient) CreateActionItemComment(ctx context.Context, actionItemID, comment string) (*risk.ActionItemComment, error) {
	return f.createActionItemComment(ctx, actionItemID, comment)
}
func (f *fakeRiskClient) GetActionItemComments(ctx context.Context, actionItemID string) ([]risk.ActionItemComment, error) {
	return f.getActionItemComments(ctx, actionItemID)
}

type fakeSNCustomerHealthClient struct {
	getCustomerHealthSummary func(ctx context.Context, email, phrase, risks *string, region []string, product, abtTeam *string, offset, limit int) (*servicenow.AccountSummaryResponse, error)
	getCustomerHealthDetail  func(ctx context.Context, accountID string) (*servicenow.AccountDetail, error)
}

func (f *fakeSNCustomerHealthClient) GetCustomerHealthSummary(ctx context.Context, email, phrase, risks *string, region []string, product, abtTeam *string, offset, limit int) (*servicenow.AccountSummaryResponse, error) {
	return f.getCustomerHealthSummary(ctx, email, phrase, risks, region, product, abtTeam, offset, limit)
}
func (f *fakeSNCustomerHealthClient) GetCustomerHealthDetail(ctx context.Context, accountID string) (*servicenow.AccountDetail, error) {
	return f.getCustomerHealthDetail(ctx, accountID)
}

// upstreamErr builds the *apierror.Error a risk.Client method would return
// for an entity-service refusal: a JSON {"message": "..."} body (the same
// envelope entity-service's apierror.WriteJSON writes) at the given status.
func upstreamErr(status int, message string) error {
	return &apierror.Error{StatusCode: status, Body: `{"message":"` + message + `"}`}
}

// testRiskID/testActionItemID are well-formed UUIDs for path params that
// now require uuidRe to match -- entity-service's own primary key shape.
const (
	testRiskID       = "11111111-1111-1111-1111-111111111111"
	testActionItemID = "22222222-2222-2222-2222-222222222222"
)

func TestCustomerHealthHandler_GetSummary_NoFilterEnrichesFromRisk(t *testing.T) {
	sn := &fakeSNCustomerHealthClient{
		getCustomerHealthSummary: func(ctx context.Context, email, phrase, risks *string, region []string, product, abtTeam *string, offset, limit int) (*servicenow.AccountSummaryResponse, error) {
			return &servicenow.AccountSummaryResponse{
				Data:       []servicenow.AccountSummary{{AccountSysID: "acct-1"}, {AccountSysID: "acct-2"}},
				TotalCount: 2,
			}, nil
		},
	}
	rc := &fakeRiskClient{
		getBatchHealthSummaries: func(ctx context.Context, accountSysIDs []string) (map[string]string, error) {
			return map[string]string{"acct-1": "at_risk", "acct-2": "healthy"}, nil
		},
	}
	h := NewCustomerHealthHandler(rc, sn, viewerAccessGuard)

	req := withUser(httptest.NewRequest(http.MethodPost, "/spl/customer-health/summary", bytes.NewReader([]byte(`{}`))))
	w := httptest.NewRecorder()
	h.GetSummary(w, req)

	assertStatus(t, w, http.StatusOK)
	resp := decodeJSON[accountSummaryResponse](t, w)
	if resp.TotalCount != 2 || len(resp.Data) != 2 {
		t.Fatalf("resp = %+v", resp)
	}
	if resp.Data[0].HealthStatus == nil || *resp.Data[0].HealthStatus != "at_risk" {
		t.Errorf("Data[0].HealthStatus = %v, want at_risk", resp.Data[0].HealthStatus)
	}
}

func TestCustomerHealthHandler_GetSummary_HealthStatusFilterEmptyMatchShortCircuits(t *testing.T) {
	rc := &fakeRiskClient{
		getAccountsByHealthStatus: func(ctx context.Context, healthStatus string) ([]string, error) {
			return nil, nil
		},
	}
	sn := &fakeSNCustomerHealthClient{} // must not be called
	h := NewCustomerHealthHandler(rc, sn, viewerAccessGuard)

	body := []byte(`{"healthStatus":"at_risk"}`)
	req := withUser(httptest.NewRequest(http.MethodPost, "/spl/customer-health/summary", bytes.NewReader(body)))
	w := httptest.NewRecorder()
	h.GetSummary(w, req)

	assertStatus(t, w, http.StatusOK)
	resp := decodeJSON[accountSummaryResponse](t, w)
	if resp.TotalCount != 0 || len(resp.Data) != 0 {
		t.Fatalf("resp = %+v, want empty", resp)
	}
}

// TestCustomerHealthHandler_GetSummary_HealthStatusFilterStopsOnTotalCount
// guards the fix for the pagination loop that only stopped on a
// short page: with ServiceNow's page size at exactly the loop's batchSize,
// the old condition (len(batch.Data) < batchSize) never stops there,
// requiring an extra round trip at best and looping forever at worst if
// ServiceNow ignores offset and keeps returning a full page. The fix uses
// the response's own TotalCount instead.
func TestCustomerHealthHandler_GetSummary_HealthStatusFilterStopsOnTotalCount(t *testing.T) {
	fullPage := make([]servicenow.AccountSummary, 200)
	for i := range fullPage {
		fullPage[i] = servicenow.AccountSummary{AccountSysID: "acct-at-risk"}
	}
	calls := 0
	sn := &fakeSNCustomerHealthClient{
		getCustomerHealthSummary: func(ctx context.Context, email, phrase, risks *string, region []string, product, abtTeam *string, offset, limit int) (*servicenow.AccountSummaryResponse, error) {
			calls++
			// Simulates ServiceNow ignoring offset (a real observed upstream
			// quirk): every call returns the same full page, never a short
			// one, and would keep this loop going indefinitely without the
			// TotalCount-based stop condition this test guards.
			return &servicenow.AccountSummaryResponse{Data: fullPage, TotalCount: 200}, nil
		},
	}
	rc := &fakeRiskClient{
		getAccountsByHealthStatus: func(ctx context.Context, healthStatus string) ([]string, error) {
			return []string{"acct-at-risk"}, nil
		},
		getBatchHealthSummaries: func(ctx context.Context, accountSysIDs []string) (map[string]string, error) {
			return map[string]string{}, nil
		},
	}
	h := NewCustomerHealthHandler(rc, sn, viewerAccessGuard)

	body := []byte(`{"healthStatus":"at_risk","limit":10}`)
	req := withUser(httptest.NewRequest(http.MethodPost, "/spl/customer-health/summary", bytes.NewReader(body)))
	w := httptest.NewRecorder()
	h.GetSummary(w, req)

	assertStatus(t, w, http.StatusOK)
	if calls != 1 {
		t.Fatalf("GetCustomerHealthSummary called %d times, want exactly 1 (TotalCount reached after the first full page)", calls)
	}
	resp := decodeJSON[accountSummaryResponse](t, w)
	if resp.TotalCount != 200 {
		t.Errorf("TotalCount = %d, want 200", resp.TotalCount)
	}
}

func TestCustomerHealthHandler_OpenRisk_RejectsMissingUser(t *testing.T) {
	h := NewCustomerHealthHandler(&fakeRiskClient{}, &fakeSNCustomerHealthClient{}, viewerAccessGuard)
	req := httptest.NewRequest(http.MethodPost, "/spl/customer-health/projects/proj-1/risk", bytes.NewReader([]byte(`{}`)))
	req.SetPathValue("projectSysId", "proj-1")
	w := httptest.NewRecorder()
	h.OpenRisk(w, req)
	assertStatus(t, w, http.StatusUnauthorized)
}

func TestCustomerHealthHandler_OpenRisk_RejectsMissingSPLAccess(t *testing.T) {
	h := NewCustomerHealthHandler(&fakeRiskClient{}, &fakeSNCustomerHealthClient{}, viewerAccessGuard)
	req := httptest.NewRequest(http.MethodPost, "/spl/customer-health/projects/proj-1/risk", bytes.NewReader([]byte(`{}`)))
	// Authenticated but holds no role granting PermViewerAccess.
	req = req.WithContext(middleware.WithUserInfo(req.Context(), &middleware.UserInfo{Email: "nobody@example.com", UserID: "u-nobody"}))
	req.SetPathValue("projectSysId", "proj-1")
	w := httptest.NewRecorder()
	h.OpenRisk(w, req)
	assertStatus(t, w, http.StatusForbidden)
}

func TestCustomerHealthHandler_CloseRisk_UpstreamConflictMapsTo409(t *testing.T) {
	rc := &fakeRiskClient{
		closeProjectRisk: func(ctx context.Context, riskID, comment string) (*risk.ProjectRisk, error) {
			return nil, upstreamErr(http.StatusConflict, "Cannot close risk: 1 action item(s) are still open.")
		},
	}
	h := NewCustomerHealthHandler(rc, &fakeSNCustomerHealthClient{}, viewerAccessGuard)

	req := withUser(httptest.NewRequest(http.MethodPut, "/spl/customer-health/risks/"+testRiskID+"/close", bytes.NewReader([]byte(`{"comment":"done"}`))))
	req.SetPathValue("riskId", testRiskID)
	w := httptest.NewRecorder()
	h.CloseRisk(w, req)

	assertStatus(t, w, http.StatusConflict)
	assertErrorMessage(t, w, "Cannot close risk: 1 action item(s) are still open.")
}

func TestCustomerHealthHandler_CloseRisk_InvalidRiskIDIs400(t *testing.T) {
	h := NewCustomerHealthHandler(&fakeRiskClient{}, &fakeSNCustomerHealthClient{}, viewerAccessGuard)
	req := withUser(httptest.NewRequest(http.MethodPut, "/spl/customer-health/risks/not-a-uuid/close", bytes.NewReader([]byte(`{}`))))
	req.SetPathValue("riskId", "not-a-uuid")
	w := httptest.NewRecorder()
	h.CloseRisk(w, req)
	assertStatus(t, w, http.StatusBadRequest)
}

func TestCustomerHealthHandler_GetAccountDetail_NotFoundMapsTo404(t *testing.T) {
	sn := &fakeSNCustomerHealthClient{
		getCustomerHealthDetail: func(ctx context.Context, accountID string) (*servicenow.AccountDetail, error) {
			return nil, servicenow.ErrAccountNotFound
		},
	}
	h := NewCustomerHealthHandler(&fakeRiskClient{}, sn, viewerAccessGuard)

	req := withUser(httptest.NewRequest(http.MethodGet, "/spl/customer-health/accounts/acct-1", nil))
	req.SetPathValue("accountId", "acct-1")
	w := httptest.NewRecorder()
	h.GetAccountDetail(w, req)

	assertStatus(t, w, http.StatusNotFound)
}

func TestCustomerHealthHandler_UpdateActionItemStatus_RequiresResolutionComment(t *testing.T) {
	rc := &fakeRiskClient{
		updateActionItemStatus: func(ctx context.Context, actionItemID, newStatus string, resolutionComment *string) (*risk.RiskActionItem, error) {
			return nil, upstreamErr(http.StatusBadRequest, "resolutionComment is required when status is RESOLVED or CANCELLED")
		},
	}
	h := NewCustomerHealthHandler(rc, &fakeSNCustomerHealthClient{}, viewerAccessGuard)

	req := withUser(httptest.NewRequest(http.MethodPut, "/spl/customer-health/action-items/"+testActionItemID+"/status", bytes.NewReader([]byte(`{"status":"resolved"}`))))
	req.SetPathValue("actionItemId", testActionItemID)
	w := httptest.NewRecorder()
	h.UpdateActionItemStatus(w, req)

	assertStatus(t, w, http.StatusBadRequest)
	assertErrorMessage(t, w, "resolutionComment is required when status is RESOLVED or CANCELLED")
}

func TestCustomerHealthHandler_InitHealthTracking_Returns202(t *testing.T) {
	rc := &fakeRiskClient{
		initProjectHealthRows: func(ctx context.Context, projectSysIDs []string, accountSysID string) error {
			if accountSysID != "acct-1" || len(projectSysIDs) != 2 {
				t.Errorf("unexpected args: accountSysID=%q projectSysIDs=%v", accountSysID, projectSysIDs)
			}
			return nil
		},
	}
	h := NewCustomerHealthHandler(rc, &fakeSNCustomerHealthClient{}, viewerAccessGuard)

	body := []byte(`{"projectSysIds":["proj-1","proj-2"]}`)
	req := withUser(httptest.NewRequest(http.MethodPost, "/spl/customer-health/accounts/acct-1/init-health-tracking", bytes.NewReader(body)))
	req.SetPathValue("accountSysId", "acct-1")
	w := httptest.NewRecorder()
	h.InitHealthTracking(w, req)

	assertStatus(t, w, http.StatusAccepted)
}
