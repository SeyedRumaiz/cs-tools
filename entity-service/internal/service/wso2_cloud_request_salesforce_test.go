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

package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/apierror"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/salesentity"
)

const (
	testSFAccount15 = "001000000000001"
	testSFAccount18 = "001000000000001AAA"
	testCloudOrgID  = "6b1f0c2e-8f6b-4f0a-9d5c-5b1f2a3c4d5e"
)

// fakeWCRSalesforce stands in for Sales Entity.
type fakeWCRSalesforce struct {
	org      *salesentity.Organization
	customer *salesentity.Customer
	opps     []salesentity.Opportunity
	linkErr  error
	links    []salesentity.LinkOrganizationInput
	linkCorr string
}

func (f *fakeWCRSalesforce) FindOrganization(context.Context, string) (salesentity.Organization, bool, error) {
	if f.org == nil {
		return salesentity.Organization{}, false, nil
	}
	return *f.org, true, nil
}

func (f *fakeWCRSalesforce) FindCustomer(context.Context, string) (salesentity.Customer, bool, error) {
	if f.customer == nil {
		return salesentity.Customer{}, false, nil
	}
	return *f.customer, true, nil
}

func (f *fakeWCRSalesforce) ActiveOpportunities(context.Context, string, int) ([]salesentity.Opportunity, error) {
	return f.opps, nil
}

func (f *fakeWCRSalesforce) LinkOrganization(_ context.Context, in salesentity.LinkOrganizationInput, corr string) (salesentity.Organization, error) {
	f.links = append(f.links, in)
	f.linkCorr = corr
	return salesentity.Organization{ID: "a0X1"}, f.linkErr
}

// subscribedSF is an account with one active opportunity.
func subscribedSF() *fakeWCRSalesforce {
	return &fakeWCRSalesforce{
		customer: &salesentity.Customer{ID: testSFAccount18, Name: strp("Acme")},
		opps:     []salesentity.Opportunity{{ID: "006A", Name: strp("Acme 2027"), SupportAccountEndDateRollUp: strp("2027-06-30")}},
	}
}

func customerUpgrade(change func(*domain.CreateWSO2CloudRequestRequest)) domain.CreateWSO2CloudRequestRequest {
	return withUpgrade(func(r *domain.CreateWSO2CloudRequestRequest) {
		r.UpgradeKind = kindp(domain.WSO2CloudUpgradeCustomer)
		r.SalesforceAccountID = strp(testSFAccount15)
		r.Region = strp("eu")
		if change != nil {
			change(r)
		}
	})
}

func TestWSO2CloudRequestService_UpgradeKindValidation(t *testing.T) {
	cases := []struct {
		name    string
		req     domain.CreateWSO2CloudRequestRequest
		wantErr string
	}{
		{"no kind", withUpgrade(func(r *domain.CreateWSO2CloudRequestRequest) { r.UpgradeKind = nil }), "upgradeKind is required"},
		{"unknown kind", withUpgrade(func(r *domain.CreateWSO2CloudRequestRequest) { r.UpgradeKind = kindp("partner") }), "not a valid upgrade kind"},
		{"customer without an account", customerUpgrade(func(r *domain.CreateWSO2CloudRequestRequest) { r.SalesforceAccountID = strp(" ") }), "salesforceAccountId is required"},
		{"not an Account id", customerUpgrade(func(r *domain.CreateWSO2CloudRequestRequest) { r.SalesforceAccountID = strp("006000000000001") }), "must be a Salesforce Account Id"},
		{"customer without a region", customerUpgrade(func(r *domain.CreateWSO2CloudRequestRequest) { r.Region = nil }), "region is required"},
		{"unknown region", customerUpgrade(func(r *domain.CreateWSO2CloudRequestRequest) { r.Region = strp("apac") }), "not a valid WSO2 Cloud region"},
		{"internal with an account", withUpgrade(func(r *domain.CreateWSO2CloudRequestRequest) { r.SalesforceAccountID = strp(testSFAccount15) }), "only accepted on a customer upgrade"},
		{"internal with a region", withUpgrade(func(r *domain.CreateWSO2CloudRequestRequest) { r.Region = strp("us") }), "only accepted on a customer upgrade"},
		{"kind on another type", domain.CreateWSO2CloudRequestRequest{Type: domain.WSO2CloudRequestDeleteOrganization, TargetID: "o1", TargetLabel: "acme", UpgradeKind: kindp(domain.WSO2CloudUpgradeInternal)}, "only accepted on an upgrade_organization request"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeWCRRepo{}
			sf := subscribedSF()
			svc := NewWSO2CloudRequestService(repo, internalAccess{}, sf)
			_, err := svc.Create(wcrUserCtx("engineer@wso2.com"), tc.req)
			var ve *apierror.ValidationError
			if !errors.As(err, &ve) || !strings.Contains(ve.Msg, tc.wantErr) {
				t.Fatalf("got %v, want a ValidationError containing %q", err, tc.wantErr)
			}
			if len(repo.created) != 0 {
				t.Fatal("a refused request reached the repository")
			}
		})
	}
}

func TestWSO2CloudRequestService_CreateCustomerUpgrade(t *testing.T) {
	t.Run("checked, stored with the 18-character id and a lower-case region", func(t *testing.T) {
		repo := &fakeWCRRepo{}
		svc := NewWSO2CloudRequestService(repo, internalAccess{}, subscribedSF())
		if _, err := svc.Create(wcrUserCtx("engineer@wso2.com"), customerUpgrade(func(r *domain.CreateWSO2CloudRequestRequest) { r.Region = strp(" EU ") })); err != nil {
			t.Fatal(err)
		}
		got := repo.created[0]
		if *got.UpgradeKind != domain.WSO2CloudUpgradeCustomer || *got.SalesforceAccountID != testSFAccount18 || *got.Region != "eu" {
			t.Fatalf("stored %+v", got)
		}
	})
	t.Run("no active subscription is a 409 with its code", func(t *testing.T) {
		repo := &fakeWCRRepo{}
		sf := subscribedSF()
		sf.opps = nil
		svc := NewWSO2CloudRequestService(repo, internalAccess{}, sf)
		_, err := svc.Create(wcrUserCtx("engineer@wso2.com"), customerUpgrade(nil))
		var ce *apierror.ConflictError
		if !errors.As(err, &ce) || ce.Code != apierror.CodeWSO2CloudNoSubscription {
			t.Fatalf("got %v, want a ConflictError %s", err, apierror.CodeWSO2CloudNoSubscription)
		}
		if len(repo.created) != 0 {
			t.Fatal("stored without a subscription")
		}
	})
	t.Run("an unknown account is a 400", func(t *testing.T) {
		svc := NewWSO2CloudRequestService(&fakeWCRRepo{}, internalAccess{}, &fakeWCRSalesforce{})
		_, err := svc.Create(wcrUserCtx("engineer@wso2.com"), customerUpgrade(nil))
		var ve *apierror.ValidationError
		if !errors.As(err, &ve) || !strings.Contains(ve.Msg, "no account") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("without Salesforce a customer upgrade is unavailable, an internal one still works", func(t *testing.T) {
		repo := &fakeWCRRepo{}
		svc := NewWSO2CloudRequestService(repo, internalAccess{}, nil)
		_, err := svc.Create(wcrUserCtx("engineer@wso2.com"), customerUpgrade(nil))
		var su *apierror.ServiceUnavailableError
		if !errors.As(err, &su) {
			t.Fatalf("got %v, want a ServiceUnavailableError", err)
		}
		if _, err := svc.Create(wcrUserCtx("engineer@wso2.com"), wcrUpgrade()); err != nil {
			t.Fatalf("internal upgrade: %v", err)
		}
	})
}

func TestWSO2CloudRequestService_SalesforceOrganization(t *testing.T) {
	t.Run("no record", func(t *testing.T) {
		svc := NewWSO2CloudRequestService(&fakeWCRRepo{}, internalAccess{}, &fakeWCRSalesforce{})
		got, err := svc.SalesforceOrganization(wcrUserCtx("e@wso2.com"), testCloudOrgID)
		if err != nil || got.Record != nil || got.Account != nil {
			t.Fatalf("got %+v, %v", got, err)
		}
	})
	t.Run("record under a subscribed account", func(t *testing.T) {
		sf := subscribedSF()
		sf.org = &salesentity.Organization{ID: "a0X1", Handle: strp("acme"), Region: strp("eu"), CustomerID: strp(testSFAccount18)}
		svc := NewWSO2CloudRequestService(&fakeWCRRepo{}, internalAccess{}, sf)
		got, err := svc.SalesforceOrganization(wcrUserCtx("e@wso2.com"), testCloudOrgID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Record == nil || got.Record.ID != "a0X1" || got.Account == nil || !got.Account.HasActiveSubscription ||
			len(got.Account.ActiveOpportunities) != 1 || *got.Account.ActiveOpportunities[0].EndDate != "2027-06-30" {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("not a UUID", func(t *testing.T) {
		svc := NewWSO2CloudRequestService(&fakeWCRRepo{}, internalAccess{}, &fakeWCRSalesforce{})
		var ve *apierror.ValidationError
		if _, err := svc.SalesforceOrganization(wcrUserCtx("e@wso2.com"), "acme"); !errors.As(err, &ve) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("internal callers only", func(t *testing.T) {
		svc := NewWSO2CloudRequestService(&fakeWCRRepo{}, customerAccess{}, &fakeWCRSalesforce{})
		var fe *apierror.ForbiddenError
		if _, err := svc.SalesforceOrganization(wcrUserCtx("e@example.com"), testCloudOrgID); !errors.As(err, &fe) {
			t.Fatalf("got %v", err)
		}
	})
}

func TestWSO2CloudRequestService_SalesforceAccount(t *testing.T) {
	svc := NewWSO2CloudRequestService(&fakeWCRRepo{}, internalAccess{}, subscribedSF())
	got, err := svc.SalesforceAccount(wcrUserCtx("e@wso2.com"), " "+testSFAccount15+" ")
	if err != nil || got.ID != testSFAccount18 || !got.HasActiveSubscription {
		t.Fatalf("got %+v, %v", got, err)
	}
	var ve *apierror.ValidationError
	if _, err := svc.SalesforceAccount(wcrUserCtx("e@wso2.com"), "x' OR 1=1"); !errors.As(err, &ve) {
		t.Fatalf("bad id: got %v", err)
	}
	none := NewWSO2CloudRequestService(&fakeWCRRepo{}, internalAccess{}, &fakeWCRSalesforce{})
	var nf *apierror.NotFoundError
	if _, err := none.SalesforceAccount(wcrUserCtx("e@wso2.com"), testSFAccount15); !errors.As(err, &nf) {
		t.Fatalf("unknown: got %v", err)
	}
}

// storedCustomerUpgrade is an approved customer upgrade.
func storedCustomerUpgrade() *domain.WSO2CloudRequest {
	return &domain.WSO2CloudRequest{
		ID: testWCRID, Number: "WCR0000007", Type: domain.WSO2CloudRequestUpgradeOrganization,
		TargetID: strings.ToUpper(testCloudOrgID), TargetLabel: "acme", Status: domain.WSO2CloudRequestApproved,
		UpgradeKind: kindp(domain.WSO2CloudUpgradeCustomer), SalesforceAccountID: strp(testSFAccount18), Region: strp("eu"),
		RequestedBy: "a@wso2.com",
	}
}

func TestWSO2CloudRequestService_RequestSalesforceAccount(t *testing.T) {
	sf := subscribedSF()
	svc := NewWSO2CloudRequestService(&fakeWCRRepo{stored: storedCustomerUpgrade()}, internalAccess{}, sf)
	got, err := svc.RequestSalesforceAccount(wcrUserCtx("e@wso2.com"), testWCRID)
	if err != nil || !got.HasActiveSubscription {
		t.Fatalf("got %+v, %v", got, err)
	}
	sf.opps = nil
	got, err = svc.RequestSalesforceAccount(wcrUserCtx("e@wso2.com"), testWCRID)
	if err != nil || got.HasActiveSubscription {
		t.Fatalf("lapsed: got %+v, %v", got, err)
	}
	internal := storedCustomerUpgrade()
	internal.UpgradeKind, internal.SalesforceAccountID, internal.Region = kindp(domain.WSO2CloudUpgradeInternal), nil, nil
	svc = NewWSO2CloudRequestService(&fakeWCRRepo{stored: internal}, internalAccess{}, sf)
	var ve *apierror.ValidationError
	if _, err := svc.RequestSalesforceAccount(wcrUserCtx("e@wso2.com"), testWCRID); !errors.As(err, &ve) {
		t.Fatalf("internal: got %v", err)
	}
}

func TestWSO2CloudRequestService_LinkSalesforce(t *testing.T) {
	t.Run("links with the org's handle and region under the account", func(t *testing.T) {
		repo := &fakeWCRRepo{stored: storedCustomerUpgrade()}
		sf := subscribedSF()
		svc := NewWSO2CloudRequestService(repo, internalAccess{}, sf)
		got, err := svc.LinkSalesforce(wcrUserCtx("approver@wso2.com"), testWCRID)
		if err != nil {
			t.Fatal(err)
		}
		want := salesentity.LinkOrganizationInput{OrganizationID: testCloudOrgID, Handle: "acme", Region: "eu", CustomerID: testSFAccount18}
		if len(sf.links) != 1 || sf.links[0] != want || sf.linkCorr != "WCR0000007" {
			t.Fatalf("link %+v corr %q", sf.links, sf.linkCorr)
		}
		if *got.SalesforceLinkStatus != domain.WSO2CloudSalesforceLinked || got.SalesforceLinkError != nil {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("a refusal is recorded as a failed link with a reason", func(t *testing.T) {
		repo := &fakeWCRRepo{stored: storedCustomerUpgrade()}
		sf := subscribedSF()
		sf.linkErr = &salesentity.LinkRefusedError{Status: 409, Code: "ORG_OTHER_ACCOUNT"}
		svc := NewWSO2CloudRequestService(repo, internalAccess{}, sf)
		got, err := svc.LinkSalesforce(wcrUserCtx("approver@wso2.com"), testWCRID)
		if err != nil {
			t.Fatal(err)
		}
		if *got.SalesforceLinkStatus != domain.WSO2CloudSalesforceFailed || !strings.Contains(*got.SalesforceLinkError, "under another account") {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("an unreachable Salesforce is a failed link too", func(t *testing.T) {
		sf := subscribedSF()
		sf.linkErr = &apierror.ServiceUnavailableError{Msg: "salesentity: organization link returned 502"}
		svc := NewWSO2CloudRequestService(&fakeWCRRepo{stored: storedCustomerUpgrade()}, internalAccess{}, sf)
		got, err := svc.LinkSalesforce(wcrUserCtx("approver@wso2.com"), testWCRID)
		if err != nil || *got.SalesforceLinkStatus != domain.WSO2CloudSalesforceFailed || !strings.Contains(*got.SalesforceLinkError, "could not be reached") {
			t.Fatalf("got %+v, %v", got, err)
		}
	})
	t.Run("an odd code is not quoted", func(t *testing.T) {
		sf := subscribedSF()
		sf.linkErr = &salesentity.LinkRefusedError{Status: 400, Code: "<script>"}
		svc := NewWSO2CloudRequestService(&fakeWCRRepo{stored: storedCustomerUpgrade()}, internalAccess{}, sf)
		got, _ := svc.LinkSalesforce(wcrUserCtx("approver@wso2.com"), testWCRID)
		if *got.SalesforceLinkError != "Salesforce refused the link." {
			t.Fatalf("got %q", *got.SalesforceLinkError)
		}
	})
	t.Run("already linked is not linked again", func(t *testing.T) {
		stored := storedCustomerUpgrade()
		linked := domain.WSO2CloudSalesforceLinked
		stored.SalesforceLinkStatus = &linked
		sf := subscribedSF()
		repo := &fakeWCRRepo{stored: stored}
		svc := NewWSO2CloudRequestService(repo, internalAccess{}, sf)
		if _, err := svc.LinkSalesforce(wcrUserCtx("approver@wso2.com"), testWCRID); err != nil {
			t.Fatal(err)
		}
		if len(sf.links) != 0 || len(repo.linkArgs) != 0 {
			t.Fatal("linked again")
		}
	})
	t.Run("a pending request is not linked", func(t *testing.T) {
		stored := storedCustomerUpgrade()
		stored.Status = domain.WSO2CloudRequestPending
		sf := subscribedSF()
		svc := NewWSO2CloudRequestService(&fakeWCRRepo{stored: stored}, internalAccess{}, sf)
		var ce *apierror.ConflictError
		if _, err := svc.LinkSalesforce(wcrUserCtx("approver@wso2.com"), testWCRID); !errors.As(err, &ce) || len(sf.links) != 0 {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("an internal upgrade is not linked", func(t *testing.T) {
		stored := storedCustomerUpgrade()
		stored.UpgradeKind, stored.SalesforceAccountID, stored.Region = kindp(domain.WSO2CloudUpgradeInternal), nil, nil
		sf := subscribedSF()
		svc := NewWSO2CloudRequestService(&fakeWCRRepo{stored: stored}, internalAccess{}, sf)
		var ve *apierror.ValidationError
		if _, err := svc.LinkSalesforce(wcrUserCtx("approver@wso2.com"), testWCRID); !errors.As(err, &ve) || len(sf.links) != 0 {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("needs a person", func(t *testing.T) {
		sf := subscribedSF()
		svc := NewWSO2CloudRequestService(&fakeWCRRepo{stored: storedCustomerUpgrade()}, internalAccess{}, sf)
		var ue *apierror.UnauthorizedError
		if _, err := svc.LinkSalesforce(context.Background(), testWCRID); !errors.As(err, &ue) {
			t.Fatalf("got %v", err)
		}
	})
}

func TestWSO2CloudRequestService_CustomerUpgradeNeedsNoApproval(t *testing.T) {
	pendingCustomer := func() *domain.WSO2CloudRequest {
		r := storedCustomerUpgrade()
		r.Status = domain.WSO2CloudRequestPending
		return r
	}
	t.Run("its requester may approve it", func(t *testing.T) {
		repo := &fakeWCRRepo{stored: pendingCustomer()}
		svc := NewWSO2CloudRequestService(repo, internalAccess{}, nil)
		if _, err := svc.Resolve(wcrUserCtx("A@wso2.com"), testWCRID, domain.ResolveWSO2CloudRequestRequest{Status: domain.WSO2CloudRequestApproved}); err != nil {
			t.Fatal(err)
		}
		if repo.resolveArgs == nil || repo.resolveArgs[2] != "A@wso2.com" {
			t.Fatalf("resolve args %v", repo.resolveArgs)
		}
	})
	t.Run("but may not reject it", func(t *testing.T) {
		repo := &fakeWCRRepo{stored: pendingCustomer()}
		svc := NewWSO2CloudRequestService(repo, internalAccess{}, nil)
		var fe *apierror.ForbiddenError
		if _, err := svc.Resolve(wcrUserCtx("a@wso2.com"), testWCRID, domain.ResolveWSO2CloudRequestRequest{Status: domain.WSO2CloudRequestRejected, Remark: strp("no")}); !errors.As(err, &fe) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("an internal upgrade still needs someone else", func(t *testing.T) {
		stored := pendingCustomer()
		stored.UpgradeKind, stored.SalesforceAccountID, stored.Region = kindp(domain.WSO2CloudUpgradeInternal), nil, nil
		repo := &fakeWCRRepo{stored: stored}
		svc := NewWSO2CloudRequestService(repo, internalAccess{}, nil)
		var fe *apierror.ForbiddenError
		if _, err := svc.Resolve(wcrUserCtx("a@wso2.com"), testWCRID, domain.ResolveWSO2CloudRequestRequest{Status: domain.WSO2CloudRequestApproved}); !errors.As(err, &fe) {
			t.Fatalf("got %v", err)
		}
	})
}
