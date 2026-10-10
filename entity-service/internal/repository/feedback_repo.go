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
// KIND, either express or implied. See the License for the
// specific language governing permissions and limitations
// under the License.

package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
)

// FeedbackFilter is the already-validated filter set shared by the feedback
// search and aggregate queries. Empty/nil fields are not applied.
type FeedbackFilter struct {
	// CaseID restricts to one case (work_item.id). Search only.
	CaseID string
	// AccountIDs restricts to cases under these accounts (work_item.account_id).
	AccountIDs []string
	// Rating is an exact-match rating filter.
	Rating *int
	// From is the inclusive lower bound on the submission time.
	From *time.Time
	// Before is the EXCLUSIVE upper bound on the submission time. The service
	// turns an inclusive dateTo into "start of the following day".
	Before *time.Time
}

// FeedbackRatingBucket is one row of a group-by-rating aggregate.
type FeedbackRatingBucket struct {
	Rating int
	// Label is the stored rating_label for the bucket, empty when no row in
	// the bucket carries one.
	Label     string
	AvgRating float64
	Count     int
}

// FeedbackRepository defines the read operations over work_item_feedback
// (migration 0102), joined to the case's work_item and the submitting user.
//
// Only rows that carry a rating count as feedback: the row is created when
// the survey instance is issued, and rating/comment are patched in later, so
// a NULL rating is an unanswered survey, not feedback.
type FeedbackRepository interface {
	// SearchFeedback returns one page of feedback, newest submission first,
	// with the total number of matching rows before pagination.
	SearchFeedback(ctx context.Context, f FeedbackFilter, limit, offset int) ([]domain.CaseFeedback, int, error)
	// AggregateFeedbackByPeriod groups feedback by submission date truncated
	// to unit ("day", "week" or "month", UTC), oldest bucket first. BucketStart
	// is formatted YYYY-MM-DD.
	AggregateFeedbackByPeriod(ctx context.Context, f FeedbackFilter, unit string) ([]domain.FeedbackBucketResult, error)
	// AggregateFeedbackByRating groups feedback by rating value, rating ascending.
	AggregateFeedbackByRating(ctx context.Context, f FeedbackFilter) ([]FeedbackRatingBucket, error)
}

type feedbackRepo struct {
	db *Scoped
}

// NewFeedbackRepository constructs a FeedbackRepository backed by the given
// Scoped wrapper: every query joins work_item, which is RLS-protected, so it
// must run under the caller's identity (see Scoped).
func NewFeedbackRepository(db *Scoped) FeedbackRepository {
	return &feedbackRepo{db: db}
}

// feedbackFromJoins joins feedback to its case. The inner join to "case"
// keeps this endpoint to case feedback even if other work item types ever
// gain feedback rows.
const feedbackFromJoins = `FROM work_item_feedback f
		JOIN work_item wi ON wi.id = f.work_item_id
		JOIN "case" c ON c.id = wi.id`

// feedbackSubmittedAt is the effective submission time: submitted_at, or the
// row's creation time when the survey response carries no explicit timestamp.
const feedbackSubmittedAt = "COALESCE(f.submitted_at, f.created_on)"

// buildFeedbackWhere renders the shared WHERE clause. Placeholders are
// numbered from 1.
func buildFeedbackWhere(f FeedbackFilter) (string, []any) {
	conds := []string{"f.rating IS NOT NULL"}
	args := []any{}
	add := func(cond string, v any) {
		args = append(args, v)
		conds = append(conds, fmt.Sprintf(cond, len(args)))
	}
	if f.CaseID != "" {
		add("f.work_item_id = $%d::uuid", f.CaseID)
	}
	if len(f.AccountIDs) > 0 {
		add("wi.account_id = ANY($%d::uuid[])", f.AccountIDs)
	}
	if f.Rating != nil {
		add("f.rating = $%d", *f.Rating)
	}
	if f.From != nil {
		add(feedbackSubmittedAt+" >= $%d", *f.From)
	}
	if f.Before != nil {
		add(feedbackSubmittedAt+" < $%d", *f.Before)
	}
	return "WHERE " + strings.Join(conds, " AND "), args
}

func (r *feedbackRepo) SearchFeedback(ctx context.Context, f FeedbackFilter, limit, offset int) ([]domain.CaseFeedback, int, error) {
	where, args := buildFeedbackWhere(f)

	var total int
	if err := r.db.QueryRow(ctx, "SELECT COUNT(*) "+feedbackFromJoins+" "+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count feedback: %w", err)
	}

	args = append(args, limit, offset)
	query := fmt.Sprintf(`
		SELECT f.id::TEXT, wi.id::TEXT, wi.number, wi.wso2_id, f.rating, f.rating_label, f.comment,
		       %s, u.name, u.email
		%s
		LEFT JOIN "user" u ON u.id = f.submitted_by_id
		%s
		ORDER BY %s DESC, f.id
		LIMIT $%d OFFSET $%d`,
		feedbackSubmittedAt, feedbackFromJoins, where, feedbackSubmittedAt, len(args)-1, len(args))
	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("search feedback: %w", err)
	}
	defer rows.Close()

	out := []domain.CaseFeedback{}
	for rows.Next() {
		var (
			fb          domain.CaseFeedback
			number      string
			internalID  *string
			ratingLabel *string
			submittedAt time.Time
		)
		if err := rows.Scan(&fb.InstanceID, &fb.CaseID, &number, &internalID, &fb.Rating, &ratingLabel,
			&fb.Comment, &submittedAt, &fb.SubmitterName, &fb.SubmitterEmail); err != nil {
			return nil, 0, fmt.Errorf("scan feedback: %w", err)
		}
		fb.CaseNumber = &number
		fb.CaseInternalID = internalID
		fb.RatingLabel = stringOrEmpty(ratingLabel)
		fb.SubmittedAt = submittedAt.UTC().Format(time.RFC3339)
		out = append(out, fb)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate feedback: %w", err)
	}
	return out, total, nil
}

// feedbackPeriodUnits is the closed set of date_trunc units the period
// aggregate accepts; the unit is interpolated into SQL, so it is checked
// against this set rather than trusted.
var feedbackPeriodUnits = map[string]bool{"day": true, "week": true, "month": true}

func (r *feedbackRepo) AggregateFeedbackByPeriod(ctx context.Context, f FeedbackFilter, unit string) ([]domain.FeedbackBucketResult, error) {
	if !feedbackPeriodUnits[unit] {
		return nil, fmt.Errorf("aggregate feedback: unsupported period %q", unit)
	}
	where, args := buildFeedbackWhere(f)
	query := fmt.Sprintf(`
		SELECT to_char(date_trunc('%s', %s AT TIME ZONE 'UTC'), 'YYYY-MM-DD') AS bucket_start,
		       ROUND(AVG(f.rating)::numeric, 2)::float8, COUNT(*)
		%s %s
		GROUP BY bucket_start
		ORDER BY bucket_start`, unit, feedbackSubmittedAt, feedbackFromJoins, where)
	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("aggregate feedback by %s: %w", unit, err)
	}
	defer rows.Close()

	out := []domain.FeedbackBucketResult{}
	for rows.Next() {
		var b domain.FeedbackBucketResult
		if err := rows.Scan(&b.BucketStart, &b.AvgRating, &b.Count); err != nil {
			return nil, fmt.Errorf("scan feedback bucket: %w", err)
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate feedback buckets: %w", err)
	}
	return out, nil
}

func (r *feedbackRepo) AggregateFeedbackByRating(ctx context.Context, f FeedbackFilter) ([]FeedbackRatingBucket, error) {
	where, args := buildFeedbackWhere(f)
	query := fmt.Sprintf(`
		SELECT f.rating, MAX(f.rating_label), COUNT(*)
		%s %s
		GROUP BY f.rating
		ORDER BY f.rating`, feedbackFromJoins, where)
	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("aggregate feedback by rating: %w", err)
	}
	defer rows.Close()

	out := []FeedbackRatingBucket{}
	for rows.Next() {
		var (
			b     FeedbackRatingBucket
			label *string
		)
		if err := rows.Scan(&b.Rating, &label, &b.Count); err != nil {
			return nil, fmt.Errorf("scan feedback rating bucket: %w", err)
		}
		b.Label = stringOrEmpty(label)
		b.AvgRating = float64(b.Rating)
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate feedback rating buckets: %w", err)
	}
	return out, nil
}
