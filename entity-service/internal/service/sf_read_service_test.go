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
	"testing"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/apierror"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
)

type fakeSFReadRepo struct {
	calls     int
	filters   []string
	page      domain.Pagination
	total     int
	opps      []domain.Opportunity
	invoices  []domain.Invoice
	links     []domain.ProjectOpportunityLink
	getResult error
}

func (f *fakeSFReadRepo) SearchOpportunities(_ context.Context, accountID string, p domain.Pagination) ([]domain.Opportunity, int, error) {
	f.calls++
	f.filters, f.page = []string{accountID}, p
	return f.opps, f.total, nil
}

func (f *fakeSFReadRepo) GetOpportunity(_ context.Context, id string) (domain.Opportunity, error) {
	f.calls++
	return domain.Opportunity{ID: id}, f.getResult
}

func (f *fakeSFReadRepo) SearchInvoices(_ context.Context, opportunityID string, p domain.Pagination) ([]domain.Invoice, int, error) {
	f.calls++
	f.filters, f.page = []string{opportunityID}, p
	return f.invoices, f.total, nil
}

func (f *fakeSFReadRepo) GetInvoice(_ context.Context, id string) (domain.Invoice, error) {
	f.calls++
	return domain.Invoice{ID: id}, f.getResult
}

func (f *fakeSFReadRepo) SearchProjectOpportunityLinks(_ context.Context, projectID, opportunityID string, p domain.Pagination) ([]domain.ProjectOpportunityLink, int, error) {
	f.calls++
	f.filters, f.page = []string{projectID, opportunityID}, p
	return f.links, f.total, nil
}

var (
	sfInternal   = stubAccess{scope: AccessScope{Unrestricted: true}}
	sfRestricted = stubAccess{scope: AccessScope{ProjectIDs: []string{m2mProjectID}}}
)

const sfSampleID = "a8b5a114-1b01-c710-a002-c9d3604bcb6b"

func TestPGOpportunityService_Search(t *testing.T) {
	repo := &fakeSFReadRepo{total: 3, opps: []domain.Opportunity{{ID: sfSampleID}}}
	resp, err := NewOpportunityService(repo, sfInternal).SearchOpportunities(context.Background(),
		domain.SearchOpportunitiesRequest{AccountID: sfSampleID, Pagination: domain.Pagination{Limit: 1, Offset: 1}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.filters[0] != sfSampleID || repo.page.Limit != 1 || repo.page.Offset != 1 {
		t.Errorf("repo got filters %v page %+v", repo.filters, repo.page)
	}
	if resp.Total != 3 || !resp.HasMore || len(resp.Opportunities) != 1 {
		t.Errorf("resp = %+v, want total 3, hasMore, one row", resp)
	}
}

func TestPGInvoiceService_SearchDefaultsPagination(t *testing.T) {
	repo := &fakeSFReadRepo{total: 1, invoices: []domain.Invoice{{ID: sfSampleID}}}
	resp, err := NewInvoiceService(repo, sfInternal).SearchInvoices(context.Background(), domain.SearchInvoicesRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.page.Limit != defaultLimit || resp.HasMore {
		t.Errorf("limit = %d hasMore = %v, want %d and false", repo.page.Limit, resp.HasMore, defaultLimit)
	}
}

func TestPGProjectOpportunityLinkService_PassesBothFilters(t *testing.T) {
	repo := &fakeSFReadRepo{}
	_, err := NewProjectOpportunityLinkService(repo, sfInternal).SearchProjectOpportunityLinks(context.Background(),
		domain.SearchProjectOpportunityLinksRequest{ProjectID: m2mProjectID, OpportunityID: sfSampleID})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.filters[0] != m2mProjectID || repo.filters[1] != sfSampleID {
		t.Errorf("filters = %v", repo.filters)
	}
}

// TestPGSalesforceRead_Refusals covers the guards every operation shares: bad input is a
// 400, a non-internal caller a 403, and neither reaches the repository.
func TestPGSalesforceRead_Refusals(t *testing.T) {
	ctx := context.Background()
	ops := map[string]func(repo *fakeSFReadRepo, access AccessService, id string) error{
		"search opportunities": func(r *fakeSFReadRepo, a AccessService, id string) error {
			_, err := NewOpportunityService(r, a).SearchOpportunities(ctx, domain.SearchOpportunitiesRequest{AccountID: id})
			return err
		},
		"get opportunity": func(r *fakeSFReadRepo, a AccessService, id string) error {
			_, err := NewOpportunityService(r, a).GetOpportunityByID(ctx, id)
			return err
		},
		"search invoices": func(r *fakeSFReadRepo, a AccessService, id string) error {
			_, err := NewInvoiceService(r, a).SearchInvoices(ctx, domain.SearchInvoicesRequest{OpportunityID: id})
			return err
		},
		"get invoice": func(r *fakeSFReadRepo, a AccessService, id string) error {
			_, err := NewInvoiceService(r, a).GetInvoiceByID(ctx, id)
			return err
		},
		"search links": func(r *fakeSFReadRepo, a AccessService, id string) error {
			_, err := NewProjectOpportunityLinkService(r, a).SearchProjectOpportunityLinks(ctx, domain.SearchProjectOpportunityLinksRequest{ProjectID: id})
			return err
		},
	}
	for name, op := range ops {
		t.Run(name+" invalid id", func(t *testing.T) {
			repo := &fakeSFReadRepo{}
			var verr *apierror.ValidationError
			if err := op(repo, sfInternal, "not-a-uuid"); !errors.As(err, &verr) || repo.calls != 0 {
				t.Errorf("err = %v calls = %d, want ValidationError and no repo call", err, repo.calls)
			}
		})
		t.Run(name+" restricted caller", func(t *testing.T) {
			repo := &fakeSFReadRepo{}
			var ferr *apierror.ForbiddenError
			if err := op(repo, sfRestricted, sfSampleID); !errors.As(err, &ferr) || repo.calls != 0 {
				t.Errorf("err = %v calls = %d, want ForbiddenError and no repo call", err, repo.calls)
			}
		})
	}
}

func TestPGSalesforceRead_LimitTooLarge(t *testing.T) {
	_, err := NewInvoiceService(&fakeSFReadRepo{}, sfInternal).SearchInvoices(context.Background(),
		domain.SearchInvoicesRequest{Pagination: domain.Pagination{Limit: maxLimit + 1}})
	var verr *apierror.ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("err = %v, want ValidationError", err)
	}
}

func TestPGSalesforceRead_GetPropagatesNotFound(t *testing.T) {
	repo := &fakeSFReadRepo{getResult: &apierror.NotFoundError{Msg: "invoice not found"}}
	_, err := NewInvoiceService(repo, sfInternal).GetInvoiceByID(context.Background(), sfSampleID)
	var nf *apierror.NotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("err = %v, want NotFoundError", err)
	}
}
