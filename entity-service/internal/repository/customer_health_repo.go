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

// CustomerHealthRepository backs the customer-health risk tracking feature
// (migration 0219_customer_health_risk_tables.sql), ported from
// apps/csm-portal/backend's own standalone MySQL internal/risk package
// (that package's own doc comments name the exact Ballerina source this was
// itself ported from). project_health_status, project_risk,
// risk_action_item, and action_item_comment are covered by one repository
// file, not four, because nearly every write touches at least two of them in
// the same transaction (opening a risk also sets the project's health
// status; closing one also resets it; an action item's own project/account
// are read off its parent risk) -- splitting them would mean either a
// cross-repository transaction (not supported by this codebase's
// pgx.Tx-per-repository shape) or duplicated read helpers in both files.
//
// None of these 4 tables has row-level security (no RLS migration touches
// them), so this repository takes a raw *pgxpool.Pool, matching the plain-
// pool repositories elsewhere in this package (e.g. ProductVulnerabilityRepository)
// rather than *Scoped.
package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/apierror"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
)

// CustomerHealthRepository defines the persistence operations for the
// customer-health risk-tracking tables.
type CustomerHealthRepository interface {
	// OpenProjectRisk opens a new risk record for a project and sets its
	// health status to AT_RISK, in one transaction. Returns
	// *apierror.NotFoundError if the project doesn't exist, or
	// *apierror.ConflictError if it already has an open risk.
	OpenProjectRisk(ctx context.Context, projectID, comment, actorEmail string) (domain.ProjectRisk, error)
	// CloseProjectRisk closes an open risk and resets the project's health
	// status to TO_BE_REVIEWED, in one transaction. Returns
	// *apierror.NotFoundError if the risk doesn't exist, or
	// *apierror.ConflictError if it is not open or still has open/in-progress
	// action items.
	CloseProjectRisk(ctx context.Context, riskID, comment, actorEmail string) (domain.ProjectRisk, error)
	// MarkProjectHealthy sets a project's health status to HEALTHY and
	// records a closed risk entry for audit history, in one transaction.
	// Returns *apierror.NotFoundError if the project doesn't exist.
	MarkProjectHealthy(ctx context.Context, projectID, actorEmail string, comment *string) (domain.ProjectHealthStatus, error)
	// RevertProjectHealth reverts a project's health status back to
	// TO_BE_REVIEWED. Returns *apierror.NotFoundError if the project has no
	// health-status row yet.
	RevertProjectHealth(ctx context.Context, projectID string) (domain.ProjectHealthStatus, error)
	// GetAccountProjectHealthStatuses returns the health status of every
	// project under an account, each with its currently open risk (and that
	// risk's action items), if any.
	GetAccountProjectHealthStatuses(ctx context.Context, accountID string) ([]domain.ProjectHealthWithOpenRisk, error)
	// GetAccountHealthSummary returns an account's aggregated overall health
	// status, derived from its projects' individual statuses: AT_RISK if any
	// project is, else HEALTHY if every project is, else TO_BE_REVIEWED.
	GetAccountHealthSummary(ctx context.Context, accountID string) (domain.AccountHealthSummary, error)
	// GetBatchAccountHealthSummaries is GetAccountHealthSummary for many
	// accounts in one query, keyed by account id. An account with no
	// project_health_status rows at all is reported as TO_BE_REVIEWED.
	GetBatchAccountHealthSummaries(ctx context.Context, accountIDs []string) (map[string]domain.ProjectHealthStatusValue, error)
	// GetAccountsByHealthStatus returns the ids of accounts matching the
	// given overall status (AT_RISK: any project has an open risk; HEALTHY:
	// every project_health_status row is HEALTHY). Any other status value
	// returns an empty list -- there is no single query that defines
	// "account-wide TO_BE_REVIEWED" the way the other two are defined.
	GetAccountsByHealthStatus(ctx context.Context, status domain.ProjectHealthStatusValue) ([]string, error)
	// GetProjectRiskHistory returns the full risk history for a project,
	// newest first, each with its action items.
	GetProjectRiskHistory(ctx context.Context, projectID string) ([]domain.ProjectRisk, error)
	// InitProjectHealthTracking seeds a TO_BE_REVIEWED health-status row for
	// every listed project id that doesn't already have one; existing rows
	// are left untouched. Returns *apierror.ValidationError if a project id
	// doesn't belong to accountID (a real FK check this package's old MySQL
	// predecessor had no way to make).
	InitProjectHealthTracking(ctx context.Context, projectIDs []string, accountID string) error

	// CreateRiskActionItem creates a new action item on riskID, taking the
	// item's project/account from the risk row itself. Returns
	// *apierror.NotFoundError if the risk doesn't exist, or
	// *apierror.ConflictError if it is not open.
	CreateRiskActionItem(ctx context.Context, riskID string, req domain.CreateRiskActionItemRequest, actorEmail string) (domain.RiskActionItem, error)
	// UpdateRiskActionItemStatus updates an action item's status. Returns
	// *apierror.ValidationError if resolutionComment is required (status
	// RESOLVED or CANCELLED) but missing/blank, *apierror.NotFoundError if
	// the item doesn't exist, or *apierror.ConflictError if reopening it
	// (OPEN/IN_PROGRESS) would leave an active item on a risk that isn't
	// open.
	UpdateRiskActionItemStatus(ctx context.Context, actionItemID string, status domain.RiskActionItemStatus, resolutionComment *string, actorEmail string) (domain.RiskActionItem, error)
	// UpdateRiskActionItem updates an action item's details. Returns
	// *apierror.ConflictError if its status is not OPEN or IN_PROGRESS.
	UpdateRiskActionItem(ctx context.Context, actionItemID string, req domain.UpdateRiskActionItemRequest) (domain.RiskActionItem, error)
	// GetActionItemsByRisk returns a risk's action items, newest first, each
	// enriched with its comment count. statusFilter narrows to one status
	// when non-nil.
	GetActionItemsByRisk(ctx context.Context, riskID string, statusFilter *domain.RiskActionItemStatus) ([]domain.RiskActionItem, error)
	// GetActionItemsByAccount returns the action items belonging to open
	// risks under an account, newest first, each enriched with its comment
	// count. projectID/statusFilter narrow the result when non-nil.
	GetActionItemsByAccount(ctx context.Context, accountID string, projectID *string, statusFilter *domain.RiskActionItemStatus) ([]domain.RiskActionItem, error)

	// CreateActionItemComment posts a comment on an action item. Returns
	// *apierror.ValidationError if comment is blank.
	CreateActionItemComment(ctx context.Context, actionItemID, comment, actorEmail string) (domain.ActionItemComment, error)
	// GetActionItemComments returns an action item's comments, oldest first.
	GetActionItemComments(ctx context.Context, actionItemID string) ([]domain.ActionItemComment, error)
}

type customerHealthRepo struct {
	db *pgxpool.Pool
}

// NewCustomerHealthRepository constructs a CustomerHealthRepository backed by
// the given connection pool.
func NewCustomerHealthRepository(db *pgxpool.Pool) CustomerHealthRepository {
	return &customerHealthRepo{db: db}
}

const projectRiskColumns = "id, project_id, account_id, status, opened_comment, opened_by_email, opened_on, closed_comment, closed_by_email, closed_on"

const healthStatusColumns = "id, project_id, account_id, status, reviewed_by_email, reviewed_on"

const riskActionItemColumns = "id, risk_id, project_id, account_id, title, description, priority, status, " +
	"assigned_to_email, due_date, resolution_comment, resolved_by_email, resolved_on, created_by_email, created_on, updated_on"

const actionItemCommentColumns = "id, action_item_id, comment, created_by_email, created_on"

// projectRiskRow/healthStatusRow/riskActionItemRow are the raw scanned shapes
// -- kept distinct from the domain.* types (which use formatted RFC3339/date
// strings, not time.Time) the same way case_feedback_repo.go's CaseFeedbackRow
// keeps its own intermediate shape.
type projectRiskRow struct {
	ID            string
	ProjectID     string
	AccountID     string
	Status        string
	OpenedComment string
	OpenedByEmail string
	OpenedOn      time.Time
	ClosedComment *string
	ClosedByEmail *string
	ClosedOn      *time.Time
}

type healthStatusRow struct {
	ID              string
	ProjectID       string
	AccountID       string
	Status          string
	ReviewedByEmail *string
	ReviewedOn      *time.Time
}

type riskActionItemRow struct {
	ID                string
	RiskID            string
	ProjectID         string
	AccountID         string
	Title             string
	Description       *string
	Priority          string
	Status            string
	AssignedToEmail   *string
	DueDate           *time.Time
	ResolutionComment *string
	ResolvedByEmail   *string
	ResolvedOn        *time.Time
	CreatedByEmail    string
	CreatedOn         time.Time
	UpdatedOn         time.Time
}

func scanProjectRiskRow(row pgx.Row) (projectRiskRow, error) {
	var r projectRiskRow
	err := row.Scan(&r.ID, &r.ProjectID, &r.AccountID, &r.Status, &r.OpenedComment, &r.OpenedByEmail,
		&r.OpenedOn, &r.ClosedComment, &r.ClosedByEmail, &r.ClosedOn)
	return r, err
}

func scanHealthStatusRow(row pgx.Row) (healthStatusRow, error) {
	var r healthStatusRow
	err := row.Scan(&r.ID, &r.ProjectID, &r.AccountID, &r.Status, &r.ReviewedByEmail, &r.ReviewedOn)
	return r, err
}

func scanRiskActionItemRow(row pgx.Row) (riskActionItemRow, error) {
	var r riskActionItemRow
	err := row.Scan(&r.ID, &r.RiskID, &r.ProjectID, &r.AccountID, &r.Title, &r.Description, &r.Priority,
		&r.Status, &r.AssignedToEmail, &r.DueDate, &r.ResolutionComment, &r.ResolvedByEmail, &r.ResolvedOn,
		&r.CreatedByEmail, &r.CreatedOn, &r.UpdatedOn)
	return r, err
}

func formatTimePtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}

func formatDatePtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format("2006-01-02")
	return &s
}

func mapRiskRow(row projectRiskRow, actionItems []domain.RiskActionItem) domain.ProjectRisk {
	if actionItems == nil {
		actionItems = []domain.RiskActionItem{}
	}
	return domain.ProjectRisk{
		ID:            row.ID,
		ProjectID:     row.ProjectID,
		AccountID:     row.AccountID,
		Status:        domain.ProjectRiskStatus(row.Status),
		OpenedComment: row.OpenedComment,
		OpenedByEmail: row.OpenedByEmail,
		OpenedOn:      row.OpenedOn.UTC().Format(time.RFC3339),
		ClosedComment: row.ClosedComment,
		ClosedByEmail: row.ClosedByEmail,
		ClosedOn:      formatTimePtr(row.ClosedOn),
		ActionItems:   actionItems,
	}
}

func mapHealthStatusRow(row healthStatusRow) domain.ProjectHealthStatus {
	return domain.ProjectHealthStatus{
		ID:              row.ID,
		ProjectID:       row.ProjectID,
		AccountID:       row.AccountID,
		Status:          domain.ProjectHealthStatusValue(row.Status),
		ReviewedByEmail: row.ReviewedByEmail,
		ReviewedOn:      formatTimePtr(row.ReviewedOn),
	}
}

func mapRiskActionItemRow(row riskActionItemRow) domain.RiskActionItem {
	return domain.RiskActionItem{
		ID:                row.ID,
		RiskID:            row.RiskID,
		ProjectID:         row.ProjectID,
		AccountID:         row.AccountID,
		Title:             row.Title,
		Description:       row.Description,
		Priority:          domain.RiskActionItemPriority(row.Priority),
		Status:            domain.RiskActionItemStatus(row.Status),
		AssignedToEmail:   row.AssignedToEmail,
		DueDate:           formatDatePtr(row.DueDate),
		ResolutionComment: row.ResolutionComment,
		ResolvedByEmail:   row.ResolvedByEmail,
		ResolvedOn:        formatTimePtr(row.ResolvedOn),
		CreatedByEmail:    row.CreatedByEmail,
		CreatedOn:         row.CreatedOn.UTC().Format(time.RFC3339),
		UpdatedOn:         row.UpdatedOn.UTC().Format(time.RFC3339),
	}
}

// ----- risk open/close, health status -----

func (r *customerHealthRepo) OpenProjectRisk(ctx context.Context, projectID, comment, actorEmail string) (domain.ProjectRisk, error) {
	return inTxReturning(ctx, r.db, func(tx pgx.Tx) (domain.ProjectRisk, error) {
		return openProjectRiskTx(ctx, tx, projectID, comment, actorEmail)
	})
}

func openProjectRiskTx(ctx context.Context, tx pgx.Tx, projectID, comment, actorEmail string) (domain.ProjectRisk, error) {
	// Lock the parent project row first -- it always exists (unlike the
	// open-risk row below, which usually doesn't yet), so this is what
	// actually serializes two concurrent OpenProjectRisk calls for the same
	// project against each other. A `FOR UPDATE` query that returns no rows
	// locks nothing, so locking only the (likely absent) open-risk row would
	// let both calls pass the check below and both insert an OPEN risk.
	var accountID string
	err := tx.QueryRow(ctx, `SELECT account_id FROM project WHERE id = $1 FOR UPDATE`, projectID).Scan(&accountID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ProjectRisk{}, &apierror.NotFoundError{Msg: "project not found"}
	}
	if err != nil {
		return domain.ProjectRisk{}, fmt.Errorf("resolve project account: %w", err)
	}

	var existingID string
	err = tx.QueryRow(ctx,
		`SELECT id FROM project_risk WHERE project_id = $1 AND status = 'OPEN'`,
		projectID).Scan(&existingID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return domain.ProjectRisk{}, fmt.Errorf("check existing open risk: %w", err)
	}
	if err == nil {
		return domain.ProjectRisk{}, &apierror.ConflictError{Msg: "project already has an open risk: " + existingID}
	}

	var riskID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO project_risk (id, project_id, account_id, status, opened_comment, opened_by_email, opened_on)
		VALUES (gen_random_uuid(), $1, $2, 'OPEN', $3, $4, NOW())
		RETURNING id`,
		projectID, accountID, comment, actorEmail,
	).Scan(&riskID); err != nil {
		return domain.ProjectRisk{}, fmt.Errorf("insert project_risk: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO project_health_status (id, project_id, account_id, status, reviewed_by_email, reviewed_on)
		VALUES (gen_random_uuid(), $1, $2, 'AT_RISK', $3, NOW())
		ON CONFLICT (project_id) DO UPDATE SET status = 'AT_RISK', reviewed_by_email = $3, reviewed_on = NOW()`,
		projectID, accountID, actorEmail,
	); err != nil {
		return domain.ProjectRisk{}, fmt.Errorf("upsert project_health_status: %w", err)
	}

	return getRiskByID(ctx, tx, riskID)
}

func (r *customerHealthRepo) CloseProjectRisk(ctx context.Context, riskID, comment, actorEmail string) (domain.ProjectRisk, error) {
	return inTxReturning(ctx, r.db, func(tx pgx.Tx) (domain.ProjectRisk, error) {
		return closeProjectRiskTx(ctx, tx, riskID, comment, actorEmail)
	})
}

func closeProjectRiskTx(ctx context.Context, tx pgx.Tx, riskID, comment, actorEmail string) (domain.ProjectRisk, error) {
	riskRow, err := scanProjectRiskRow(tx.QueryRow(ctx, "SELECT "+projectRiskColumns+" FROM project_risk WHERE id = $1 FOR UPDATE", riskID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ProjectRisk{}, &apierror.NotFoundError{Msg: "risk not found: " + riskID}
	}
	if err != nil {
		return domain.ProjectRisk{}, fmt.Errorf("query project_risk by id: %w", err)
	}
	if riskRow.Status != string(domain.ProjectRiskStatusOpen) {
		return domain.ProjectRisk{}, &apierror.ConflictError{Msg: "risk " + riskID + " is not open"}
	}

	var openCount int
	if err := tx.QueryRow(ctx,
		`SELECT COUNT(*) FROM risk_action_item WHERE risk_id = $1 AND status NOT IN ('RESOLVED', 'CANCELLED')`,
		riskID).Scan(&openCount); err != nil {
		return domain.ProjectRisk{}, fmt.Errorf("count open action items: %w", err)
	}
	if openCount > 0 {
		return domain.ProjectRisk{}, &apierror.ConflictError{Msg: fmt.Sprintf("cannot close risk: %d action item(s) are still open", openCount)}
	}

	if _, err := tx.Exec(ctx, `
		UPDATE project_risk
		SET status = 'CLOSED', closed_comment = $1, closed_by_email = $2, closed_on = NOW()
		WHERE id = $3 AND status = 'OPEN'`, comment, actorEmail, riskID); err != nil {
		return domain.ProjectRisk{}, fmt.Errorf("close project_risk: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE project_health_status
		SET status = 'TO_BE_REVIEWED', reviewed_by_email = NULL, reviewed_on = NULL
		WHERE project_id = $1 AND account_id = $2`, riskRow.ProjectID, riskRow.AccountID); err != nil {
		return domain.ProjectRisk{}, fmt.Errorf("reset project_health_status: %w", err)
	}

	return getRiskByID(ctx, tx, riskID)
}

func (r *customerHealthRepo) MarkProjectHealthy(ctx context.Context, projectID, actorEmail string, comment *string) (domain.ProjectHealthStatus, error) {
	closedComment := "Reviewed and confirmed healthy"
	if comment != nil {
		closedComment = *comment
	}

	return inTxReturning(ctx, r.db, func(tx pgx.Tx) (domain.ProjectHealthStatus, error) {
		var accountID string
		err := tx.QueryRow(ctx, `SELECT account_id FROM project WHERE id = $1`, projectID).Scan(&accountID)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ProjectHealthStatus{}, &apierror.NotFoundError{Msg: "project not found"}
		}
		if err != nil {
			return domain.ProjectHealthStatus{}, fmt.Errorf("resolve project account: %w", err)
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO project_health_status (id, project_id, account_id, status, reviewed_by_email, reviewed_on)
			VALUES (gen_random_uuid(), $1, $2, 'HEALTHY', $3, NOW())
			ON CONFLICT (project_id) DO UPDATE SET status = 'HEALTHY', reviewed_by_email = $3, reviewed_on = NOW()`,
			projectID, accountID, actorEmail,
		); err != nil {
			return domain.ProjectHealthStatus{}, fmt.Errorf("upsert project_health_status: %w", err)
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO project_risk
				(id, project_id, account_id, status, opened_comment, opened_by_email, opened_on,
				 closed_comment, closed_by_email, closed_on)
			VALUES (gen_random_uuid(), $1, $2, 'CLOSED', 'Marked as healthy', $3, NOW(), $4, $3, NOW())`,
			projectID, accountID, actorEmail, closedComment,
		); err != nil {
			return domain.ProjectHealthStatus{}, fmt.Errorf("insert closed project_risk: %w", err)
		}

		return getHealthStatusByProject(ctx, tx, projectID)
	})
}

func (r *customerHealthRepo) RevertProjectHealth(ctx context.Context, projectID string) (domain.ProjectHealthStatus, error) {
	ct, err := r.db.Exec(ctx, `
		UPDATE project_health_status
		SET status = 'TO_BE_REVIEWED', reviewed_by_email = NULL, reviewed_on = NULL
		WHERE project_id = $1`, projectID)
	if err != nil {
		return domain.ProjectHealthStatus{}, fmt.Errorf("reset project_health_status: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return domain.ProjectHealthStatus{}, &apierror.NotFoundError{Msg: "project has no health status record: " + projectID}
	}
	return getHealthStatusByProject(ctx, r.db, projectID)
}

func (r *customerHealthRepo) GetAccountProjectHealthStatuses(ctx context.Context, accountID string) ([]domain.ProjectHealthWithOpenRisk, error) {
	rows, err := r.db.Query(ctx, "SELECT "+healthStatusColumns+" FROM project_health_status WHERE account_id = $1", accountID)
	if err != nil {
		return nil, fmt.Errorf("query project_health_status: %w", err)
	}
	defer rows.Close()

	var statusRows []healthStatusRow
	for rows.Next() {
		sr, err := scanHealthStatusRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan project_health_status row: %w", err)
		}
		statusRows = append(statusRows, sr)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate project_health_status rows: %w", err)
	}

	results := make([]domain.ProjectHealthWithOpenRisk, 0, len(statusRows))
	for _, sr := range statusRows {
		var openRisk *domain.ProjectRisk
		if sr.Status == string(domain.ProjectHealthStatusAtRisk) {
			riskRow, err := queryOpenRiskForProject(ctx, r.db, sr.ProjectID)
			if err != nil {
				return nil, err
			}
			if riskRow != nil {
				items, err := getActionItemsByRiskID(ctx, r.db, riskRow.ID)
				if err != nil {
					return nil, err
				}
				risk := mapRiskRow(*riskRow, items)
				openRisk = &risk
			}
		}
		results = append(results, domain.ProjectHealthWithOpenRisk{
			ProjectID:    sr.ProjectID,
			HealthStatus: mapHealthStatusRow(sr),
			OpenRisk:     openRisk,
		})
	}
	return results, nil
}

func queryOpenRiskForProject(ctx context.Context, q rowQuerier, projectID string) (*projectRiskRow, error) {
	r, err := scanProjectRiskRow(q.QueryRow(ctx,
		"SELECT "+projectRiskColumns+" FROM project_risk WHERE project_id = $1 AND status = 'OPEN' ORDER BY opened_on DESC LIMIT 1",
		projectID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query open project_risk: %w", err)
	}
	return &r, nil
}

func (r *customerHealthRepo) GetAccountHealthSummary(ctx context.Context, accountID string) (domain.AccountHealthSummary, error) {
	statuses, err := queryHealthStatusValues(ctx, r.db, "account_id = $1", accountID)
	if err != nil {
		return domain.AccountHealthSummary{}, err
	}
	return domain.AccountHealthSummary{AccountID: accountID, OverallStatus: overallStatus(statuses)}, nil
}

func queryHealthStatusValues(ctx context.Context, q rowsQuerier, whereClause string, args ...any) ([]string, error) {
	rows, err := q.Query(ctx, "SELECT status FROM project_health_status WHERE "+whereClause, args...)
	if err != nil {
		return nil, fmt.Errorf("query project_health_status statuses: %w", err)
	}
	defer rows.Close()

	var statuses []string
	for rows.Next() {
		var status string
		if err := rows.Scan(&status); err != nil {
			return nil, fmt.Errorf("scan status: %w", err)
		}
		statuses = append(statuses, status)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate statuses: %w", err)
	}
	return statuses, nil
}

func overallStatus(statuses []string) domain.ProjectHealthStatusValue {
	if len(statuses) == 0 {
		return domain.ProjectHealthStatusToBeReviewed
	}
	hasAtRisk := false
	allHealthy := true
	for _, status := range statuses {
		if status == string(domain.ProjectHealthStatusAtRisk) {
			hasAtRisk = true
		}
		if status != string(domain.ProjectHealthStatusHealthy) {
			allHealthy = false
		}
	}
	switch {
	case hasAtRisk:
		return domain.ProjectHealthStatusAtRisk
	case allHealthy:
		return domain.ProjectHealthStatusHealthy
	default:
		return domain.ProjectHealthStatusToBeReviewed
	}
}

func (r *customerHealthRepo) GetBatchAccountHealthSummaries(ctx context.Context, accountIDs []string) (map[string]domain.ProjectHealthStatusValue, error) {
	result := make(map[string]domain.ProjectHealthStatusValue, len(accountIDs))
	if len(accountIDs) == 0 {
		return result, nil
	}

	rows, err := r.db.Query(ctx,
		"SELECT account_id, status FROM project_health_status WHERE account_id = ANY($1)", accountIDs)
	if err != nil {
		return nil, fmt.Errorf("query batch health summaries: %w", err)
	}
	defer rows.Close()

	hasAtRisk := make(map[string]bool)
	hasNonHealthy := make(map[string]bool)
	hasAnyRow := make(map[string]bool)
	for rows.Next() {
		var accountID, status string
		if err := rows.Scan(&accountID, &status); err != nil {
			return nil, fmt.Errorf("scan batch health summary row: %w", err)
		}
		hasAnyRow[accountID] = true
		if status == string(domain.ProjectHealthStatusAtRisk) {
			hasAtRisk[accountID] = true
		}
		if status != string(domain.ProjectHealthStatusHealthy) {
			hasNonHealthy[accountID] = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate batch health summary rows: %w", err)
	}

	for _, accountID := range accountIDs {
		switch {
		case hasAtRisk[accountID]:
			result[accountID] = domain.ProjectHealthStatusAtRisk
		case hasAnyRow[accountID] && !hasNonHealthy[accountID]:
			result[accountID] = domain.ProjectHealthStatusHealthy
		default:
			result[accountID] = domain.ProjectHealthStatusToBeReviewed
		}
	}
	return result, nil
}

func (r *customerHealthRepo) GetAccountsByHealthStatus(ctx context.Context, status domain.ProjectHealthStatusValue) ([]string, error) {
	var query string
	switch status {
	case domain.ProjectHealthStatusAtRisk:
		query = "SELECT DISTINCT account_id FROM project_risk WHERE status = 'OPEN'"
	case domain.ProjectHealthStatusHealthy:
		query = `SELECT DISTINCT account_id FROM project_health_status
			WHERE account_id NOT IN (
				SELECT DISTINCT account_id FROM project_health_status WHERE status != 'HEALTHY'
			)`
	default:
		return []string{}, nil
	}

	rows, err := r.db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query accounts by health status: %w", err)
	}
	defer rows.Close()

	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan account id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate account id rows: %w", err)
	}
	return ids, nil
}

func (r *customerHealthRepo) GetProjectRiskHistory(ctx context.Context, projectID string) ([]domain.ProjectRisk, error) {
	rows, err := r.db.Query(ctx, "SELECT "+projectRiskColumns+" FROM project_risk WHERE project_id = $1 ORDER BY opened_on DESC", projectID)
	if err != nil {
		return nil, fmt.Errorf("query project_risk: %w", err)
	}
	defer rows.Close()

	var riskRows []projectRiskRow
	for rows.Next() {
		rr, err := scanProjectRiskRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan project_risk row: %w", err)
		}
		riskRows = append(riskRows, rr)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate project_risk rows: %w", err)
	}

	risks := make([]domain.ProjectRisk, 0, len(riskRows))
	for _, rr := range riskRows {
		items, err := getActionItemsByRiskID(ctx, r.db, rr.ID)
		if err != nil {
			return nil, err
		}
		risks = append(risks, mapRiskRow(rr, items))
	}
	return risks, nil
}

func (r *customerHealthRepo) InitProjectHealthTracking(ctx context.Context, projectIDs []string, accountID string) error {
	for _, projectID := range projectIDs {
		var realAccountID string
		if err := r.db.QueryRow(ctx, `SELECT account_id FROM project WHERE id = $1`, projectID).Scan(&realAccountID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return &apierror.ValidationError{Msg: "project not found: " + projectID}
			}
			return fmt.Errorf("resolve project account for %s: %w", projectID, err)
		}
		if realAccountID != accountID {
			return &apierror.ValidationError{Msg: "project " + projectID + " does not belong to account " + accountID}
		}

		if _, err := r.db.Exec(ctx, `
			INSERT INTO project_health_status (id, project_id, account_id, status)
			VALUES (gen_random_uuid(), $1, $2, 'TO_BE_REVIEWED')
			ON CONFLICT (project_id) DO NOTHING`, projectID, accountID); err != nil {
			return fmt.Errorf("init health row for project %s: %w", projectID, err)
		}
	}
	return nil
}

func getRiskRowByID(ctx context.Context, q rowQuerier, riskID string) (projectRiskRow, error) {
	r, err := scanProjectRiskRow(q.QueryRow(ctx, "SELECT "+projectRiskColumns+" FROM project_risk WHERE id = $1", riskID))
	if errors.Is(err, pgx.ErrNoRows) {
		return projectRiskRow{}, &apierror.NotFoundError{Msg: "risk not found: " + riskID}
	}
	if err != nil {
		return projectRiskRow{}, fmt.Errorf("query project_risk by id: %w", err)
	}
	return r, nil
}

func getRiskByID(ctx context.Context, q interface {
	rowQuerier
	rowsQuerier
}, riskID string) (domain.ProjectRisk, error) {
	riskRow, err := getRiskRowByID(ctx, q, riskID)
	if err != nil {
		return domain.ProjectRisk{}, err
	}
	items, err := getActionItemsByRiskID(ctx, q, riskRow.ID)
	if err != nil {
		return domain.ProjectRisk{}, err
	}
	return mapRiskRow(riskRow, items), nil
}

func getHealthStatusByProject(ctx context.Context, q rowQuerier, projectID string) (domain.ProjectHealthStatus, error) {
	r, err := scanHealthStatusRow(q.QueryRow(ctx, "SELECT "+healthStatusColumns+" FROM project_health_status WHERE project_id = $1", projectID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ProjectHealthStatus{}, &apierror.NotFoundError{Msg: "project has no health status record: " + projectID}
	}
	if err != nil {
		return domain.ProjectHealthStatus{}, fmt.Errorf("query project_health_status by project: %w", err)
	}
	return mapHealthStatusRow(r), nil
}

// ----- risk action items -----

func (r *customerHealthRepo) CreateRiskActionItem(ctx context.Context, riskID string, req domain.CreateRiskActionItemRequest, actorEmail string) (domain.RiskActionItem, error) {
	return inTxReturning(ctx, r.db, func(tx pgx.Tx) (domain.RiskActionItem, error) {
		// Locked so this insert can't race a concurrent CloseProjectRisk
		// (which only counts items not already resolved/cancelled): without
		// the lock, this could read the risk as OPEN, lose the race to a
		// close that commits first, then insert an OPEN item onto the now-
		// closed risk.
		riskRow, err := scanProjectRiskRow(tx.QueryRow(ctx, "SELECT "+projectRiskColumns+" FROM project_risk WHERE id = $1 FOR UPDATE", riskID))
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.RiskActionItem{}, &apierror.NotFoundError{Msg: "risk not found: " + riskID}
		}
		if err != nil {
			return domain.RiskActionItem{}, fmt.Errorf("query project_risk by id: %w", err)
		}
		if riskRow.Status != string(domain.ProjectRiskStatusOpen) {
			return domain.RiskActionItem{}, &apierror.ConflictError{Msg: "action items can only be added to open risks"}
		}

		// due_date is a DATE column; an omitted dueDate arrives here as "" (the
		// request field is a non-pointer string -- see validateDueDate's own
		// doc comment, which accepts an empty value), and Postgres rejects ''
		// as invalid date input. nil binds a real SQL NULL instead.
		var dueDate *string
		if req.DueDate != "" {
			dueDate = &req.DueDate
		}
		var itemID string
		if err := tx.QueryRow(ctx, `
			INSERT INTO risk_action_item
				(id, risk_id, project_id, account_id, title, description, priority, status,
				 assigned_to_email, due_date, created_by_email, created_on, updated_on)
			VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, $6, 'OPEN', $7, $8, $9, NOW(), NOW())
			RETURNING id`,
			riskID, riskRow.ProjectID, riskRow.AccountID, req.Title, req.Description, req.Priority,
			req.AssignedToEmail, dueDate, actorEmail,
		).Scan(&itemID); err != nil {
			return domain.RiskActionItem{}, fmt.Errorf("insert risk_action_item: %w", err)
		}
		return getActionItemByID(ctx, tx, itemID)
	})
}

func (r *customerHealthRepo) UpdateRiskActionItemStatus(ctx context.Context, actionItemID string, status domain.RiskActionItemStatus, resolutionComment *string, actorEmail string) (domain.RiskActionItem, error) {
	if (status == domain.RiskActionItemStatusResolved || status == domain.RiskActionItemStatusCancelled) &&
		(resolutionComment == nil || strings.TrimSpace(*resolutionComment) == "") {
		return domain.RiskActionItem{}, &apierror.ValidationError{Msg: "resolutionComment is required when status is RESOLVED or CANCELLED"}
	}

	return inTxReturning(ctx, r.db, func(tx pgx.Tx) (domain.RiskActionItem, error) {
		row, err := scanRiskActionItemRow(tx.QueryRow(ctx, "SELECT "+riskActionItemColumns+" FROM risk_action_item WHERE id = $1", actionItemID))
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.RiskActionItem{}, &apierror.NotFoundError{Msg: "action item not found: " + actionItemID}
		}
		if err != nil {
			return domain.RiskActionItem{}, fmt.Errorf("query risk_action_item by id: %w", err)
		}

		if status == domain.RiskActionItemStatusOpen || status == domain.RiskActionItemStatusInProgress {
			// Locked for the same reason CloseProjectRisk's own lock exists:
			// without it, this could read the risk as OPEN, lose a race to a
			// concurrent close, and then write OPEN onto an item whose risk
			// just closed.
			riskRow, err := scanProjectRiskRow(tx.QueryRow(ctx, "SELECT "+projectRiskColumns+" FROM project_risk WHERE id = $1 FOR UPDATE", row.RiskID))
			if errors.Is(err, pgx.ErrNoRows) {
				return domain.RiskActionItem{}, &apierror.NotFoundError{Msg: "risk not found: " + row.RiskID}
			}
			if err != nil {
				return domain.RiskActionItem{}, fmt.Errorf("query project_risk by id: %w", err)
			}
			if riskRow.Status != string(domain.ProjectRiskStatusOpen) {
				return domain.RiskActionItem{}, &apierror.ConflictError{Msg: "cannot reopen an action item on a risk that is not open"}
			}
		}

		if status == domain.RiskActionItemStatusResolved || status == domain.RiskActionItemStatusCancelled {
			if _, err := tx.Exec(ctx, `
				UPDATE risk_action_item
				SET status = $1, resolution_comment = $2, resolved_by_email = $3, resolved_on = NOW(), updated_on = NOW()
				WHERE id = $4`, status, *resolutionComment, actorEmail, actionItemID); err != nil {
				return domain.RiskActionItem{}, fmt.Errorf("resolve/cancel action item: %w", err)
			}
		} else {
			if _, err := tx.Exec(ctx, `UPDATE risk_action_item SET status = $1, updated_on = NOW() WHERE id = $2`, status, actionItemID); err != nil {
				return domain.RiskActionItem{}, fmt.Errorf("update action item status: %w", err)
			}
		}

		return getActionItemByID(ctx, tx, actionItemID)
	})
}

func (r *customerHealthRepo) UpdateRiskActionItem(ctx context.Context, actionItemID string, req domain.UpdateRiskActionItemRequest) (domain.RiskActionItem, error) {
	row, err := getActionItemRowByID(ctx, r.db, actionItemID)
	if err != nil {
		return domain.RiskActionItem{}, err
	}
	if row.Status != string(domain.RiskActionItemStatusOpen) && row.Status != string(domain.RiskActionItemStatusInProgress) {
		return domain.RiskActionItem{}, &apierror.ConflictError{Msg: fmt.Sprintf(
			"cannot edit action item with status %q; only OPEN or IN_PROGRESS items can be edited", row.Status)}
	}

	// due_date is a DATE column; see CreateRiskActionItem's own comment on why
	// "" / a pointer to "" must become nil rather than bind a literal empty
	// string.
	var dueDate *string
	if req.DueDate != nil && *req.DueDate != "" {
		dueDate = req.DueDate
	}

	// The editable-status condition is repeated in the UPDATE's own WHERE
	// clause (not just the read above) so a concurrent
	// UpdateRiskActionItemStatus resolving/cancelling the item between the
	// read and this write can't have this edit silently apply to it anyway.
	ct, err := r.db.Exec(ctx, `
		UPDATE risk_action_item
		SET title = $1, description = $2, priority = $3, assigned_to_email = $4, due_date = $5, updated_on = NOW()
		WHERE id = $6 AND status IN ('OPEN', 'IN_PROGRESS')`,
		req.Title, req.Description, req.Priority, req.AssignedToEmail, dueDate, actionItemID,
	)
	if err != nil {
		return domain.RiskActionItem{}, fmt.Errorf("update action item: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return domain.RiskActionItem{}, &apierror.ConflictError{Msg: "action item is no longer OPEN or IN_PROGRESS"}
	}
	return getActionItemByID(ctx, r.db, actionItemID)
}

func (r *customerHealthRepo) GetActionItemsByRisk(ctx context.Context, riskID string, statusFilter *domain.RiskActionItemStatus) ([]domain.RiskActionItem, error) {
	query := "SELECT " + riskActionItemColumns + " FROM risk_action_item WHERE risk_id = $1"
	args := []any{riskID}
	if statusFilter != nil {
		query += " AND status = $2"
		args = append(args, *statusFilter)
	}
	query += " ORDER BY created_on DESC"

	items, err := queryActionItems(ctx, r.db, query, args...)
	if err != nil {
		return nil, err
	}
	return enrichWithCommentCounts(ctx, r.db, items)
}

func (r *customerHealthRepo) GetActionItemsByAccount(ctx context.Context, accountID string, projectID *string, statusFilter *domain.RiskActionItemStatus) ([]domain.RiskActionItem, error) {
	query := `SELECT ai.id, ai.risk_id, ai.project_id, ai.account_id, ai.title, ai.description, ai.priority, ai.status,
		ai.assigned_to_email, ai.due_date, ai.resolution_comment, ai.resolved_by_email, ai.resolved_on,
		ai.created_by_email, ai.created_on, ai.updated_on
		FROM risk_action_item ai
		INNER JOIN project_risk pr ON ai.risk_id = pr.id
		WHERE ai.account_id = $1 AND pr.status = 'OPEN'`
	args := []any{accountID}

	if projectID != nil {
		args = append(args, *projectID)
		query += fmt.Sprintf(" AND ai.project_id = $%d", len(args))
	}
	if statusFilter != nil {
		args = append(args, *statusFilter)
		query += fmt.Sprintf(" AND ai.status = $%d", len(args))
	}
	query += " ORDER BY ai.created_on DESC"

	items, err := queryActionItems(ctx, r.db, query, args...)
	if err != nil {
		return nil, err
	}
	return enrichWithCommentCounts(ctx, r.db, items)
}

func queryActionItems(ctx context.Context, q rowsQuerier, query string, args ...any) ([]domain.RiskActionItem, error) {
	rows, err := q.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query risk_action_item: %w", err)
	}
	defer rows.Close()

	items := []domain.RiskActionItem{}
	for rows.Next() {
		row, err := scanRiskActionItemRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan risk_action_item row: %w", err)
		}
		items = append(items, mapRiskActionItemRow(row))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate risk_action_item rows: %w", err)
	}
	return items, nil
}

func getActionItemsByRiskID(ctx context.Context, q interface {
	rowQuerier
	rowsQuerier
}, riskID string) ([]domain.RiskActionItem, error) {
	items, err := queryActionItems(ctx, q, "SELECT "+riskActionItemColumns+" FROM risk_action_item WHERE risk_id = $1 ORDER BY created_on DESC", riskID)
	if err != nil {
		return nil, err
	}
	return enrichWithCommentCounts(ctx, q, items)
}

// enrichWithCommentCounts populates CommentCount on each item via a single
// batch query.
func enrichWithCommentCounts(ctx context.Context, q rowsQuerier, items []domain.RiskActionItem) ([]domain.RiskActionItem, error) {
	if len(items) == 0 {
		return items, nil
	}

	ids := make([]string, len(items))
	for i, item := range items {
		ids[i] = item.ID
	}

	rows, err := q.Query(ctx,
		"SELECT action_item_id, COUNT(*) FROM action_item_comment WHERE action_item_id = ANY($1) GROUP BY action_item_id", ids)
	if err != nil {
		return nil, fmt.Errorf("query comment counts: %w", err)
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var actionItemID string
		var count int
		if err := rows.Scan(&actionItemID, &count); err != nil {
			return nil, fmt.Errorf("scan comment count row: %w", err)
		}
		counts[actionItemID] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate comment count rows: %w", err)
	}

	enriched := make([]domain.RiskActionItem, len(items))
	for i, item := range items {
		item.CommentCount = counts[item.ID]
		enriched[i] = item
	}
	return enriched, nil
}

func getActionItemRowByID(ctx context.Context, q rowQuerier, actionItemID string) (riskActionItemRow, error) {
	r, err := scanRiskActionItemRow(q.QueryRow(ctx, "SELECT "+riskActionItemColumns+" FROM risk_action_item WHERE id = $1", actionItemID))
	if errors.Is(err, pgx.ErrNoRows) {
		return riskActionItemRow{}, &apierror.NotFoundError{Msg: "action item not found: " + actionItemID}
	}
	if err != nil {
		return riskActionItemRow{}, fmt.Errorf("query risk_action_item by id: %w", err)
	}
	return r, nil
}

func getActionItemByID(ctx context.Context, q rowQuerier, actionItemID string) (domain.RiskActionItem, error) {
	row, err := getActionItemRowByID(ctx, q, actionItemID)
	if err != nil {
		return domain.RiskActionItem{}, err
	}
	return mapRiskActionItemRow(row), nil
}

// ----- action item comments -----

func (r *customerHealthRepo) CreateActionItemComment(ctx context.Context, actionItemID, comment, actorEmail string) (domain.ActionItemComment, error) {
	if strings.TrimSpace(comment) == "" {
		return domain.ActionItemComment{}, &apierror.ValidationError{Msg: "comment cannot be empty"}
	}

	var commentID string
	err := r.db.QueryRow(ctx, `
		INSERT INTO action_item_comment (id, action_item_id, comment, created_by_email, created_on)
		VALUES (gen_random_uuid(), $1, $2, $3, NOW())
		RETURNING id`, actionItemID, comment, actorEmail,
	).Scan(&commentID)
	if err != nil {
		var pgErr interface{ SQLState() string }
		if errors.As(err, &pgErr) && pgErr.SQLState() == "23503" {
			return domain.ActionItemComment{}, &apierror.NotFoundError{Msg: "action item not found: " + actionItemID}
		}
		return domain.ActionItemComment{}, fmt.Errorf("insert action_item_comment: %w", err)
	}
	return getCommentByID(ctx, r.db, commentID)
}

func (r *customerHealthRepo) GetActionItemComments(ctx context.Context, actionItemID string) ([]domain.ActionItemComment, error) {
	rows, err := r.db.Query(ctx,
		"SELECT "+actionItemCommentColumns+" FROM action_item_comment WHERE action_item_id = $1 ORDER BY created_on ASC",
		actionItemID)
	if err != nil {
		return nil, fmt.Errorf("query action_item_comment: %w", err)
	}
	defer rows.Close()

	comments := []domain.ActionItemComment{}
	for rows.Next() {
		var id, actionItemID, commentText, createdByEmail string
		var createdOn time.Time
		if err := rows.Scan(&id, &actionItemID, &commentText, &createdByEmail, &createdOn); err != nil {
			return nil, fmt.Errorf("scan action_item_comment row: %w", err)
		}
		comments = append(comments, domain.ActionItemComment{
			ID: id, ActionItemID: actionItemID, Comment: commentText,
			CreatedByEmail: createdByEmail, CreatedOn: createdOn.UTC().Format(time.RFC3339),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate action_item_comment rows: %w", err)
	}
	return comments, nil
}

func getCommentByID(ctx context.Context, q rowQuerier, commentID string) (domain.ActionItemComment, error) {
	var id, actionItemID, commentText, createdByEmail string
	var createdOn time.Time
	err := q.QueryRow(ctx, "SELECT "+actionItemCommentColumns+" FROM action_item_comment WHERE id = $1", commentID).
		Scan(&id, &actionItemID, &commentText, &createdByEmail, &createdOn)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ActionItemComment{}, &apierror.NotFoundError{Msg: "comment not found: " + commentID}
	}
	if err != nil {
		return domain.ActionItemComment{}, fmt.Errorf("query action_item_comment by id: %w", err)
	}
	return domain.ActionItemComment{
		ID: id, ActionItemID: actionItemID, Comment: commentText,
		CreatedByEmail: createdByEmail, CreatedOn: createdOn.UTC().Format(time.RFC3339),
	}, nil
}
