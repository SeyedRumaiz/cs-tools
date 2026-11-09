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

package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/errgroup"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/apierror"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
)

// WSO2CloudRequestRepository reads and writes the WSO2 Cloud tab's requests (migration 0278).
type WSO2CloudRequestRepository interface {
	// Create stores a pending request raised by requestedBy; a ConflictError when a request of the same type for the
	// same target is already pending.
	Create(ctx context.Context, requestedBy string, req domain.CreateWSO2CloudRequestRequest) (domain.WSO2CloudRequest, error)
	// Get returns the request with that id, or nil when there is none.
	Get(ctx context.Context, id string) (*domain.WSO2CloudRequest, error)
	// Search returns a page of requests, newest first, and how many match.
	Search(ctx context.Context, req domain.SearchWSO2CloudRequestsRequest) ([]domain.WSO2CloudRequest, int, error)
	// Resolve moves a pending request to approved or rejected; nil when the request is no longer pending (or gone),
	// so a request is never resolved twice.
	Resolve(ctx context.Context, id string, status domain.WSO2CloudRequestStatus, resolvedBy string, remark *string) (*domain.WSO2CloudRequest, error)
	// Delete removes a pending request; false when there is no pending request with that id.
	Delete(ctx context.Context, id string) (bool, error)
}

type wso2CloudRequestRepo struct {
	db *pgxpool.Pool
}

// NewWSO2CloudRequestRepository constructs a Postgres-backed WSO2CloudRequestRepository.
func NewWSO2CloudRequestRepository(db *pgxpool.Pool) WSO2CloudRequestRepository {
	return &wso2CloudRequestRepo{db: db}
}

const wso2CloudRequestColumns = `id::text, number, type, target_id, target_label, reason, customer_consent,
	tier, limits, previous_tier, previous_limits, status, requested_by, requested_on, resolved_by, resolved_on, remark`

func scanWSO2CloudRequest(row pgx.Row) (domain.WSO2CloudRequest, error) {
	var r domain.WSO2CloudRequest
	var tier, previousTier *string
	var limits, previousLimits []byte
	err := row.Scan(&r.ID, &r.Number, &r.Type, &r.TargetID, &r.TargetLabel, &r.Reason, &r.CustomerConsent,
		&tier, &limits, &previousTier, &previousLimits, &r.Status, &r.RequestedBy, &r.RequestedOn,
		&r.ResolvedBy, &r.ResolvedOn, &r.Remark)
	if err != nil {
		return r, err
	}
	if tier != nil {
		t := domain.WSO2CloudTier(*tier)
		r.Tier = &t
	}
	if previousTier != nil {
		t := domain.WSO2CloudTier(*previousTier)
		r.PreviousTier = &t
	}
	if len(limits) > 0 {
		r.Limits = json.RawMessage(limits)
	}
	if len(previousLimits) > 0 {
		r.PreviousLimits = json.RawMessage(previousLimits)
	}
	return r, nil
}

// jsonParam binds a JSON object for a ::jsonb parameter; an empty value is NULL.
func jsonParam(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	return string(raw)
}

func tierParam(t *domain.WSO2CloudTier) any {
	if t == nil {
		return nil
	}
	return string(*t)
}

func (r *wso2CloudRequestRepo) Create(ctx context.Context, requestedBy string, req domain.CreateWSO2CloudRequestRequest) (domain.WSO2CloudRequest, error) {
	out, err := scanWSO2CloudRequest(r.db.QueryRow(ctx,
		`INSERT INTO wso2_cloud_request
		   (type, target_id, target_label, reason, customer_consent, tier, limits, previous_tier, previous_limits, requested_by)
		 VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8, $9::jsonb, $10)
		 RETURNING `+wso2CloudRequestColumns,
		string(req.Type), req.TargetID, req.TargetLabel, req.Reason, req.CustomerConsent,
		tierParam(req.Tier), jsonParam(req.Limits), tierParam(req.PreviousTier), jsonParam(req.PreviousLimits), requestedBy))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "wso2_cloud_request_one_pending_key" {
			return out, &apierror.ConflictError{Msg: "a " + strings.ReplaceAll(string(req.Type), "_", " ") +
				" request for this target is already pending"}
		}
		return out, fmt.Errorf("create wso2 cloud request: %w", err)
	}
	return out, nil
}

func (r *wso2CloudRequestRepo) Get(ctx context.Context, id string) (*domain.WSO2CloudRequest, error) {
	out, err := scanWSO2CloudRequest(r.db.QueryRow(ctx,
		`SELECT `+wso2CloudRequestColumns+` FROM wso2_cloud_request WHERE id = $1::uuid`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get wso2 cloud request: %w", err)
	}
	return &out, nil
}

func (r *wso2CloudRequestRepo) Search(ctx context.Context, req domain.SearchWSO2CloudRequestsRequest) ([]domain.WSO2CloudRequest, int, error) {
	var where []string
	var args []any
	add := func(clause string, arg any) {
		args = append(args, arg)
		where = append(where, fmt.Sprintf(clause, len(args)))
	}
	f := req.Filters
	if len(f.Statuses) > 0 {
		statuses := make([]string, len(f.Statuses))
		for i, s := range f.Statuses {
			statuses[i] = string(s)
		}
		add("status = ANY($%d::text[])", statuses)
	}
	if len(f.Types) > 0 {
		types := make([]string, len(f.Types))
		for i, t := range f.Types {
			types[i] = string(t)
		}
		add("type = ANY($%d::text[])", types)
	}
	if f.TargetID != nil {
		add("target_id = $%d", *f.TargetID)
	}
	if f.RequestedBy != nil {
		add("LOWER(requested_by) = LOWER($%d)", *f.RequestedBy)
	}
	whereSQL := ""
	if len(where) > 0 {
		whereSQL = " WHERE " + strings.Join(where, " AND ")
	}

	var total int
	out := []domain.WSO2CloudRequest{}
	eg, egCtx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		if err := r.db.QueryRow(egCtx, `SELECT COUNT(*) FROM wso2_cloud_request`+whereSQL, args...).Scan(&total); err != nil {
			return fmt.Errorf("count wso2 cloud requests: %w", err)
		}
		return nil
	})
	eg.Go(func() error {
		pageArgs := append(append([]any{}, args...), req.Pagination.Limit, req.Pagination.Offset)
		rows, err := r.db.Query(egCtx,
			`SELECT `+wso2CloudRequestColumns+` FROM wso2_cloud_request`+whereSQL+
				fmt.Sprintf(` ORDER BY requested_on DESC, number DESC LIMIT $%d OFFSET $%d`, len(args)+1, len(args)+2),
			pageArgs...)
		if err != nil {
			return fmt.Errorf("search wso2 cloud requests: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			item, err := scanWSO2CloudRequest(rows)
			if err != nil {
				return fmt.Errorf("search wso2 cloud requests: scan: %w", err)
			}
			out = append(out, item)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("search wso2 cloud requests: %w", err)
		}
		return nil
	})
	if err := eg.Wait(); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

func (r *wso2CloudRequestRepo) Resolve(ctx context.Context, id string, status domain.WSO2CloudRequestStatus, resolvedBy string, remark *string) (*domain.WSO2CloudRequest, error) {
	out, err := scanWSO2CloudRequest(r.db.QueryRow(ctx,
		`UPDATE wso2_cloud_request
		    SET status = $2, resolved_by = $3, resolved_on = NOW(), remark = $4
		  WHERE id = $1::uuid AND status = 'pending'
		  RETURNING `+wso2CloudRequestColumns,
		id, string(status), resolvedBy, remark))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("resolve wso2 cloud request: %w", err)
	}
	return &out, nil
}

func (r *wso2CloudRequestRepo) Delete(ctx context.Context, id string) (bool, error) {
	tag, err := r.db.Exec(ctx, `DELETE FROM wso2_cloud_request WHERE id = $1::uuid AND status = 'pending'`, id)
	if err != nil {
		return false, fmt.Errorf("delete wso2 cloud request: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}
