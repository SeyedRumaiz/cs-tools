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
	"strings"
	"time"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/apierror"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/middleware"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/repository"
)

// CustomerHealthService implements the customer-health risk-tracking feature
// (migration 0219), ported from apps/csm-portal/backend's own standalone
// MySQL internal/risk package. It validates input and resolves the caller's
// identity for attribution before delegating to the repository, which holds
// the actual transactional/locking business logic.
type CustomerHealthService interface {
	OpenProjectRisk(ctx context.Context, projectID string, req domain.OpenProjectRiskRequest) (domain.ProjectRisk, error)
	CloseProjectRisk(ctx context.Context, riskID string, req domain.CloseProjectRiskRequest) (domain.ProjectRisk, error)
	MarkProjectHealthy(ctx context.Context, projectID string, req domain.MarkProjectHealthyRequest) (domain.ProjectHealthStatus, error)
	RevertProjectHealth(ctx context.Context, projectID string) (domain.ProjectHealthStatus, error)
	GetAccountProjectHealthStatuses(ctx context.Context, accountID string) (domain.GetAccountProjectHealthStatusesResponse, error)
	GetAccountHealthSummary(ctx context.Context, accountID string) (domain.AccountHealthSummary, error)
	GetBatchAccountHealthSummaries(ctx context.Context, req domain.BatchAccountHealthSummariesRequest) (domain.BatchAccountHealthSummariesResponse, error)
	GetAccountsByHealthStatus(ctx context.Context, req domain.AccountsByHealthStatusRequest) (domain.AccountsByHealthStatusResponse, error)
	GetProjectRiskHistory(ctx context.Context, projectID string) (domain.GetProjectRiskHistoryResponse, error)
	InitProjectHealthTracking(ctx context.Context, accountID string, req domain.InitProjectHealthTrackingRequest) error

	CreateRiskActionItem(ctx context.Context, riskID string, req domain.CreateRiskActionItemRequest) (domain.RiskActionItem, error)
	UpdateRiskActionItemStatus(ctx context.Context, actionItemID string, req domain.UpdateRiskActionItemStatusRequest) (domain.RiskActionItem, error)
	UpdateRiskActionItem(ctx context.Context, actionItemID string, req domain.UpdateRiskActionItemRequest) (domain.RiskActionItem, error)
	GetActionItemsByRisk(ctx context.Context, riskID string, status *string) (domain.GetRiskActionItemsResponse, error)
	GetActionItemsByAccount(ctx context.Context, accountID string, projectID, status *string) (domain.GetRiskActionItemsResponse, error)

	CreateActionItemComment(ctx context.Context, actionItemID string, req domain.CreateActionItemCommentRequest) (domain.ActionItemComment, error)
	GetActionItemComments(ctx context.Context, actionItemID string) (domain.GetActionItemCommentsResponse, error)
}

type customerHealthService struct {
	repo     repository.CustomerHealthRepository
	userRepo repository.UserRepository
}

// NewCustomerHealthService constructs a CustomerHealthService. userRepo
// resolves the caller's x-user-id-token into an actor email for attribution
// (opened_by_email, created_by_email, ...), the same resolveActor pattern
// caseService/escalationService use.
func NewCustomerHealthService(repo repository.CustomerHealthRepository, userRepo repository.UserRepository) CustomerHealthService {
	return &customerHealthService{repo: repo, userRepo: userRepo}
}

// resolveActorEmail authenticates the caller from the x-user-id-token header
// carried on ctx and returns their email -- the same authentication step
// caseService.resolveActor performs, but returning just the email since every
// write here only needs that for attribution, never the full user row.
func (s *customerHealthService) resolveActorEmail(ctx context.Context) (string, error) {
	token := middleware.UserIDTokenFromContext(ctx)
	if token == "" {
		return "", &apierror.UnauthorizedError{Msg: "x-user-id-token header is required"}
	}
	email, err := emailFromJWT(token)
	if err != nil {
		return "", &apierror.ValidationError{Msg: "x-user-id-token: " + err.Error()}
	}
	return email, nil
}

var validRiskActionItemPriority = map[domain.RiskActionItemPriority]bool{
	domain.RiskActionItemPriorityHigh:   true,
	domain.RiskActionItemPriorityMedium: true,
	domain.RiskActionItemPriorityLow:    true,
}

var validRiskActionItemStatus = map[domain.RiskActionItemStatus]bool{
	domain.RiskActionItemStatusOpen:       true,
	domain.RiskActionItemStatusInProgress: true,
	domain.RiskActionItemStatusResolved:   true,
	domain.RiskActionItemStatusCancelled:  true,
}

var validProjectHealthStatusValue = map[domain.ProjectHealthStatusValue]bool{
	domain.ProjectHealthStatusToBeReviewed: true,
	domain.ProjectHealthStatusHealthy:      true,
	domain.ProjectHealthStatusAtRisk:       true,
}

// dueDateLayout is the plain calendar-date format every due-date field on
// this feature uses -- matching the Postgres DATE column risk_action_item.due_date
// maps to (no time-of-day, no zone).
const dueDateLayout = "2006-01-02"

func validateDueDate(field, value string) error {
	if value == "" {
		return nil
	}
	if _, err := time.Parse(dueDateLayout, value); err != nil {
		return &apierror.ValidationError{Msg: field + " must be a valid date in YYYY-MM-DD format"}
	}
	return nil
}

// ----- risk open/close, health status -----

func (s *customerHealthService) OpenProjectRisk(ctx context.Context, projectID string, req domain.OpenProjectRiskRequest) (domain.ProjectRisk, error) {
	if err := validateUUIDs("projectId", []string{projectID}); err != nil {
		return domain.ProjectRisk{}, err
	}
	if strings.TrimSpace(req.Comment) == "" {
		return domain.ProjectRisk{}, &apierror.ValidationError{Msg: "comment is required"}
	}
	email, err := s.resolveActorEmail(ctx)
	if err != nil {
		return domain.ProjectRisk{}, err
	}
	return s.repo.OpenProjectRisk(ctx, projectID, req.Comment, email)
}

func (s *customerHealthService) CloseProjectRisk(ctx context.Context, riskID string, req domain.CloseProjectRiskRequest) (domain.ProjectRisk, error) {
	if err := validateUUIDs("riskId", []string{riskID}); err != nil {
		return domain.ProjectRisk{}, err
	}
	if strings.TrimSpace(req.Comment) == "" {
		return domain.ProjectRisk{}, &apierror.ValidationError{Msg: "comment is required"}
	}
	email, err := s.resolveActorEmail(ctx)
	if err != nil {
		return domain.ProjectRisk{}, err
	}
	return s.repo.CloseProjectRisk(ctx, riskID, req.Comment, email)
}

func (s *customerHealthService) MarkProjectHealthy(ctx context.Context, projectID string, req domain.MarkProjectHealthyRequest) (domain.ProjectHealthStatus, error) {
	if err := validateUUIDs("projectId", []string{projectID}); err != nil {
		return domain.ProjectHealthStatus{}, err
	}
	email, err := s.resolveActorEmail(ctx)
	if err != nil {
		return domain.ProjectHealthStatus{}, err
	}
	return s.repo.MarkProjectHealthy(ctx, projectID, email, req.Comment)
}

func (s *customerHealthService) RevertProjectHealth(ctx context.Context, projectID string) (domain.ProjectHealthStatus, error) {
	if err := validateUUIDs("projectId", []string{projectID}); err != nil {
		return domain.ProjectHealthStatus{}, err
	}
	return s.repo.RevertProjectHealth(ctx, projectID)
}

func (s *customerHealthService) GetAccountProjectHealthStatuses(ctx context.Context, accountID string) (domain.GetAccountProjectHealthStatusesResponse, error) {
	if err := validateUUIDs("accountId", []string{accountID}); err != nil {
		return domain.GetAccountProjectHealthStatusesResponse{}, err
	}
	projects, err := s.repo.GetAccountProjectHealthStatuses(ctx, accountID)
	if err != nil {
		return domain.GetAccountProjectHealthStatusesResponse{}, err
	}
	return domain.GetAccountProjectHealthStatusesResponse{Projects: projects}, nil
}

func (s *customerHealthService) GetAccountHealthSummary(ctx context.Context, accountID string) (domain.AccountHealthSummary, error) {
	if err := validateUUIDs("accountId", []string{accountID}); err != nil {
		return domain.AccountHealthSummary{}, err
	}
	return s.repo.GetAccountHealthSummary(ctx, accountID)
}

func (s *customerHealthService) GetBatchAccountHealthSummaries(ctx context.Context, req domain.BatchAccountHealthSummariesRequest) (domain.BatchAccountHealthSummariesResponse, error) {
	if err := validateUUIDs("accountIds", req.AccountIDs); err != nil {
		return domain.BatchAccountHealthSummariesResponse{}, err
	}
	summaries, err := s.repo.GetBatchAccountHealthSummaries(ctx, req.AccountIDs)
	if err != nil {
		return domain.BatchAccountHealthSummariesResponse{}, err
	}
	return domain.BatchAccountHealthSummariesResponse{Summaries: summaries}, nil
}

func (s *customerHealthService) GetAccountsByHealthStatus(ctx context.Context, req domain.AccountsByHealthStatusRequest) (domain.AccountsByHealthStatusResponse, error) {
	if !validProjectHealthStatusValue[req.Status] {
		return domain.AccountsByHealthStatusResponse{}, &apierror.ValidationError{Msg: "invalid status: " + string(req.Status)}
	}
	ids, err := s.repo.GetAccountsByHealthStatus(ctx, req.Status)
	if err != nil {
		return domain.AccountsByHealthStatusResponse{}, err
	}
	return domain.AccountsByHealthStatusResponse{AccountIDs: ids}, nil
}

func (s *customerHealthService) GetProjectRiskHistory(ctx context.Context, projectID string) (domain.GetProjectRiskHistoryResponse, error) {
	if err := validateUUIDs("projectId", []string{projectID}); err != nil {
		return domain.GetProjectRiskHistoryResponse{}, err
	}
	risks, err := s.repo.GetProjectRiskHistory(ctx, projectID)
	if err != nil {
		return domain.GetProjectRiskHistoryResponse{}, err
	}
	return domain.GetProjectRiskHistoryResponse{Risks: risks}, nil
}

func (s *customerHealthService) InitProjectHealthTracking(ctx context.Context, accountID string, req domain.InitProjectHealthTrackingRequest) error {
	if err := validateUUIDs("accountId", []string{accountID}); err != nil {
		return err
	}
	if len(req.ProjectIDs) == 0 {
		return &apierror.ValidationError{Msg: "projectIds is required"}
	}
	if err := validateUUIDs("projectIds", req.ProjectIDs); err != nil {
		return err
	}
	return s.repo.InitProjectHealthTracking(ctx, req.ProjectIDs, accountID)
}

// ----- risk action items -----

func (s *customerHealthService) CreateRiskActionItem(ctx context.Context, riskID string, req domain.CreateRiskActionItemRequest) (domain.RiskActionItem, error) {
	if err := validateUUIDs("riskId", []string{riskID}); err != nil {
		return domain.RiskActionItem{}, err
	}
	if strings.TrimSpace(req.Title) == "" {
		return domain.RiskActionItem{}, &apierror.ValidationError{Msg: "title is required"}
	}
	if !validRiskActionItemPriority[req.Priority] {
		return domain.RiskActionItem{}, &apierror.ValidationError{Msg: "invalid priority: " + string(req.Priority)}
	}
	if err := validateDueDate("dueDate", req.DueDate); err != nil {
		return domain.RiskActionItem{}, err
	}
	email, err := s.resolveActorEmail(ctx)
	if err != nil {
		return domain.RiskActionItem{}, err
	}
	return s.repo.CreateRiskActionItem(ctx, riskID, req, email)
}

func (s *customerHealthService) UpdateRiskActionItemStatus(ctx context.Context, actionItemID string, req domain.UpdateRiskActionItemStatusRequest) (domain.RiskActionItem, error) {
	if err := validateUUIDs("actionItemId", []string{actionItemID}); err != nil {
		return domain.RiskActionItem{}, err
	}
	if !validRiskActionItemStatus[req.Status] {
		return domain.RiskActionItem{}, &apierror.ValidationError{Msg: "invalid status: " + string(req.Status)}
	}
	email, err := s.resolveActorEmail(ctx)
	if err != nil {
		return domain.RiskActionItem{}, err
	}
	return s.repo.UpdateRiskActionItemStatus(ctx, actionItemID, req.Status, req.ResolutionComment, email)
}

func (s *customerHealthService) UpdateRiskActionItem(ctx context.Context, actionItemID string, req domain.UpdateRiskActionItemRequest) (domain.RiskActionItem, error) {
	if err := validateUUIDs("actionItemId", []string{actionItemID}); err != nil {
		return domain.RiskActionItem{}, err
	}
	if strings.TrimSpace(req.Title) == "" {
		return domain.RiskActionItem{}, &apierror.ValidationError{Msg: "title is required"}
	}
	if !validRiskActionItemPriority[req.Priority] {
		return domain.RiskActionItem{}, &apierror.ValidationError{Msg: "invalid priority: " + string(req.Priority)}
	}
	if req.DueDate != nil {
		if err := validateDueDate("dueDate", *req.DueDate); err != nil {
			return domain.RiskActionItem{}, err
		}
	}
	return s.repo.UpdateRiskActionItem(ctx, actionItemID, req)
}

func (s *customerHealthService) GetActionItemsByRisk(ctx context.Context, riskID string, status *string) (domain.GetRiskActionItemsResponse, error) {
	if err := validateUUIDs("riskId", []string{riskID}); err != nil {
		return domain.GetRiskActionItemsResponse{}, err
	}
	statusFilter, err := parseRiskActionItemStatusFilter(status)
	if err != nil {
		return domain.GetRiskActionItemsResponse{}, err
	}
	items, err := s.repo.GetActionItemsByRisk(ctx, riskID, statusFilter)
	if err != nil {
		return domain.GetRiskActionItemsResponse{}, err
	}
	return domain.GetRiskActionItemsResponse{ActionItems: items}, nil
}

func (s *customerHealthService) GetActionItemsByAccount(ctx context.Context, accountID string, projectID, status *string) (domain.GetRiskActionItemsResponse, error) {
	if err := validateUUIDs("accountId", []string{accountID}); err != nil {
		return domain.GetRiskActionItemsResponse{}, err
	}
	if projectID != nil {
		if err := validateUUIDs("projectId", []string{*projectID}); err != nil {
			return domain.GetRiskActionItemsResponse{}, err
		}
	}
	statusFilter, err := parseRiskActionItemStatusFilter(status)
	if err != nil {
		return domain.GetRiskActionItemsResponse{}, err
	}
	items, err := s.repo.GetActionItemsByAccount(ctx, accountID, projectID, statusFilter)
	if err != nil {
		return domain.GetRiskActionItemsResponse{}, err
	}
	return domain.GetRiskActionItemsResponse{ActionItems: items}, nil
}

func parseRiskActionItemStatusFilter(status *string) (*domain.RiskActionItemStatus, error) {
	if status == nil || *status == "" {
		return nil, nil
	}
	s := domain.RiskActionItemStatus(strings.ToUpper(*status))
	if !validRiskActionItemStatus[s] {
		return nil, &apierror.ValidationError{Msg: "invalid status: " + *status}
	}
	return &s, nil
}

// ----- action item comments -----

func (s *customerHealthService) CreateActionItemComment(ctx context.Context, actionItemID string, req domain.CreateActionItemCommentRequest) (domain.ActionItemComment, error) {
	if err := validateUUIDs("actionItemId", []string{actionItemID}); err != nil {
		return domain.ActionItemComment{}, err
	}
	if strings.TrimSpace(req.Comment) == "" {
		return domain.ActionItemComment{}, &apierror.ValidationError{Msg: "comment is required"}
	}
	email, err := s.resolveActorEmail(ctx)
	if err != nil {
		return domain.ActionItemComment{}, err
	}
	return s.repo.CreateActionItemComment(ctx, actionItemID, req.Comment, email)
}

func (s *customerHealthService) GetActionItemComments(ctx context.Context, actionItemID string) (domain.GetActionItemCommentsResponse, error) {
	if err := validateUUIDs("actionItemId", []string{actionItemID}); err != nil {
		return domain.GetActionItemCommentsResponse{}, err
	}
	comments, err := s.repo.GetActionItemComments(ctx, actionItemID)
	if err != nil {
		return domain.GetActionItemCommentsResponse{}, err
	}
	return domain.GetActionItemCommentsResponse{Comments: comments}, nil
}
