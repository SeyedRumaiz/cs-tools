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

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/apierror"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/repository"
)

// pgSalesforceReadService backs OpportunityService, InvoiceService and
// ProjectOpportunityLinkService from the Salesforce-ingested sf_* tables.
type pgSalesforceReadService struct {
	repo   repository.SalesforceReadRepository
	access AccessService
}

// NewOpportunityService constructs a Postgres-backed OpportunityService.
func NewOpportunityService(repo repository.SalesforceReadRepository, access AccessService) OpportunityService {
	return &pgSalesforceReadService{repo: repo, access: access}
}

// NewInvoiceService constructs a Postgres-backed InvoiceService.
func NewInvoiceService(repo repository.SalesforceReadRepository, access AccessService) InvoiceService {
	return &pgSalesforceReadService{repo: repo, access: access}
}

// NewProjectOpportunityLinkService constructs a Postgres-backed ProjectOpportunityLinkService.
func NewProjectOpportunityLinkService(repo repository.SalesforceReadRepository, access AccessService) ProjectOpportunityLinkService {
	return &pgSalesforceReadService{repo: repo, access: access}
}

// requireInternalCaller limits commercial data to unrestricted callers
// (allow-listed internal services and INTERNAL users).
func (s *pgSalesforceReadService) requireInternalCaller(ctx context.Context) error {
	scope, err := s.access.ResolveScope(ctx)
	if err != nil {
		return err
	}
	if !scope.Unrestricted {
		return &apierror.ForbiddenError{Msg: "opportunities and invoices are only available to internal callers"}
	}
	return nil
}

// prepareSearch validates pagination and the optional uuid filters, then checks the caller.
func (s *pgSalesforceReadService) prepareSearch(ctx context.Context, p *domain.Pagination, filters ...[2]string) error {
	if err := normalizePagination(p); err != nil {
		return err
	}
	for _, f := range filters {
		if f[1] == "" {
			continue
		}
		if err := validateUUIDs(f[0], []string{f[1]}); err != nil {
			return err
		}
	}
	return s.requireInternalCaller(ctx)
}

func (s *pgSalesforceReadService) prepareGet(ctx context.Context, id string) error {
	if err := validateUUIDs("id", []string{id}); err != nil {
		return err
	}
	return s.requireInternalCaller(ctx)
}

// SearchOpportunities implements OpportunityService.
func (s *pgSalesforceReadService) SearchOpportunities(ctx context.Context, req domain.SearchOpportunitiesRequest) (domain.SearchOpportunitiesResponse, error) {
	if err := s.prepareSearch(ctx, &req.Pagination, [2]string{"accountId", req.AccountID}); err != nil {
		return domain.SearchOpportunitiesResponse{}, err
	}
	rows, total, err := s.repo.SearchOpportunities(ctx, req.AccountID, req.Pagination)
	if err != nil {
		return domain.SearchOpportunitiesResponse{}, err
	}
	return domain.SearchOpportunitiesResponse{
		Opportunities: rows,
		Total:         total,
		Limit:         req.Pagination.Limit,
		Offset:        req.Pagination.Offset,
		HasMore:       req.Pagination.Offset+len(rows) < total,
	}, nil
}

// GetOpportunityByID implements OpportunityService.
func (s *pgSalesforceReadService) GetOpportunityByID(ctx context.Context, id string) (domain.Opportunity, error) {
	if err := s.prepareGet(ctx, id); err != nil {
		return domain.Opportunity{}, err
	}
	return s.repo.GetOpportunity(ctx, id)
}

// SearchInvoices implements InvoiceService.
func (s *pgSalesforceReadService) SearchInvoices(ctx context.Context, req domain.SearchInvoicesRequest) (domain.SearchInvoicesResponse, error) {
	if err := s.prepareSearch(ctx, &req.Pagination, [2]string{"opportunityId", req.OpportunityID}); err != nil {
		return domain.SearchInvoicesResponse{}, err
	}
	rows, total, err := s.repo.SearchInvoices(ctx, req.OpportunityID, req.Pagination)
	if err != nil {
		return domain.SearchInvoicesResponse{}, err
	}
	return domain.SearchInvoicesResponse{
		Invoices: rows,
		Total:    total,
		Limit:    req.Pagination.Limit,
		Offset:   req.Pagination.Offset,
		HasMore:  req.Pagination.Offset+len(rows) < total,
	}, nil
}

// GetInvoiceByID implements InvoiceService.
func (s *pgSalesforceReadService) GetInvoiceByID(ctx context.Context, id string) (domain.Invoice, error) {
	if err := s.prepareGet(ctx, id); err != nil {
		return domain.Invoice{}, err
	}
	return s.repo.GetInvoice(ctx, id)
}

// SearchProjectOpportunityLinks implements ProjectOpportunityLinkService.
func (s *pgSalesforceReadService) SearchProjectOpportunityLinks(ctx context.Context, req domain.SearchProjectOpportunityLinksRequest) (domain.SearchProjectOpportunityLinksResponse, error) {
	if err := s.prepareSearch(ctx, &req.Pagination,
		[2]string{"projectId", req.ProjectID}, [2]string{"opportunityId", req.OpportunityID}); err != nil {
		return domain.SearchProjectOpportunityLinksResponse{}, err
	}
	rows, total, err := s.repo.SearchProjectOpportunityLinks(ctx, req.ProjectID, req.OpportunityID, req.Pagination)
	if err != nil {
		return domain.SearchProjectOpportunityLinksResponse{}, err
	}
	return domain.SearchProjectOpportunityLinksResponse{
		Links:   rows,
		Total:   total,
		Limit:   req.Pagination.Limit,
		Offset:  req.Pagination.Offset,
		HasMore: req.Pagination.Offset+len(rows) < total,
	}, nil
}
