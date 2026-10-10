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

package service

import (
	"context"
	"time"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/apierror"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/repository"
)

// feedbackRatingLabels names each rating value for a "rating" bucket whose
// rows carry no stored rating_label.
var feedbackRatingLabels = map[int]string{
	1: "Very Dissatisfied",
	2: "Dissatisfied",
	3: "Neutral",
	4: "Satisfied",
	5: "Very Satisfied",
}

const feedbackBucketErrMsg = "bucket must be one of: day, week, month, rating, reasons_very_dissatisfied, reasons_dissatisfied, reasons_neutral, reasons_satisfied, reasons_very_satisfied"

// pgFeedbackService implements FeedbackService over Postgres
// (work_item_feedback). It serves reads only: feedback is submitted through
// the customer-facing product and lands in this table via sync, so there is
// nothing for the CSM side to write.
//
// The table stores rating, label and comment but not which follow-up reason
// chips the customer ticked, so every "reasons_*" bucket aggregates to an
// empty result (see AggregateFeedback).
type pgFeedbackService struct {
	repo repository.FeedbackRepository
}

// NewPostgresFeedbackService constructs a FeedbackService backed by Postgres.
func NewPostgresFeedbackService(repo repository.FeedbackRepository) FeedbackService {
	return &pgFeedbackService{repo: repo}
}

// SearchFeedback implements FeedbackService.
func (s *pgFeedbackService) SearchFeedback(ctx context.Context, req domain.SearchFeedbackRequest) (domain.SearchFeedbackResponse, error) {
	if req.Page < 0 {
		return domain.SearchFeedbackResponse{}, &apierror.ValidationError{Msg: "page must not be negative"}
	}
	if req.PageSize < 0 {
		return domain.SearchFeedbackResponse{}, &apierror.ValidationError{Msg: "pageSize must not be negative"}
	}
	if err := validateUUIDs("accountIds", req.Filters.AccountIDs); err != nil {
		return domain.SearchFeedbackResponse{}, err
	}
	if req.Filters.CaseID != "" {
		if err := validateUUIDs("caseId", []string{req.Filters.CaseID}); err != nil {
			return domain.SearchFeedbackResponse{}, err
		}
	}
	if req.Filters.Rating != nil && (*req.Filters.Rating < 1 || *req.Filters.Rating > 5) {
		return domain.SearchFeedbackResponse{}, &apierror.ValidationError{Msg: "rating must be between 1 and 5"}
	}
	from, before, err := feedbackDateRange(req.Filters.DateFrom, req.Filters.DateTo)
	if err != nil {
		return domain.SearchFeedbackResponse{}, err
	}

	page, pageSize := req.Page, req.PageSize
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = defaultLimit
	}
	if pageSize > maxLimit {
		return domain.SearchFeedbackResponse{}, &apierror.ValidationError{Msg: "pageSize cannot exceed 50"}
	}

	results, total, err := s.repo.SearchFeedback(ctx, repository.FeedbackFilter{
		CaseID:     req.Filters.CaseID,
		AccountIDs: req.Filters.AccountIDs,
		Rating:     req.Filters.Rating,
		From:       from,
		Before:     before,
	}, pageSize, (page-1)*pageSize)
	if err != nil {
		return domain.SearchFeedbackResponse{}, err
	}
	if results == nil {
		results = []domain.CaseFeedback{}
	}
	return domain.SearchFeedbackResponse{Results: results, TotalRecords: total}, nil
}

// AggregateFeedback implements FeedbackService.
func (s *pgFeedbackService) AggregateFeedback(ctx context.Context, req domain.AggregateFeedbackRequest) (domain.AggregateFeedbackResponse, error) {
	if !validFeedbackBuckets[req.Bucket] {
		return domain.AggregateFeedbackResponse{}, &apierror.ValidationError{Msg: feedbackBucketErrMsg}
	}
	if err := validateUUIDs("accountIds", req.Filters.AccountIDs); err != nil {
		return domain.AggregateFeedbackResponse{}, err
	}
	from, before, err := feedbackDateRange(req.Filters.DateFrom, req.Filters.DateTo)
	if err != nil {
		return domain.AggregateFeedbackResponse{}, err
	}
	filter := repository.FeedbackFilter{AccountIDs: req.Filters.AccountIDs, From: from, Before: before}

	buckets := []domain.FeedbackBucketResult{}
	switch req.Bucket {
	case domain.FeedbackBucketDay, domain.FeedbackBucketWeek, domain.FeedbackBucketMonth:
		buckets, err = s.repo.AggregateFeedbackByPeriod(ctx, filter, string(req.Bucket))
		if err != nil {
			return domain.AggregateFeedbackResponse{}, err
		}
	case domain.FeedbackBucketRating:
		rows, err := s.repo.AggregateFeedbackByRating(ctx, filter)
		if err != nil {
			return domain.AggregateFeedbackResponse{}, err
		}
		for _, row := range rows {
			label := row.Label
			if label == "" {
				label = feedbackRatingLabels[row.Rating]
			}
			buckets = append(buckets, domain.FeedbackBucketResult{BucketStart: label, AvgRating: row.AvgRating, Count: row.Count})
		}
	default:
		// reasons_*: the reason chips are not stored in work_item_feedback.
	}
	if buckets == nil {
		buckets = []domain.FeedbackBucketResult{}
	}

	total := 0
	for _, b := range buckets {
		total += b.Count
	}
	return domain.AggregateFeedbackResponse{Buckets: buckets, TotalRecords: total}, nil
}

// feedbackDateRange parses the optional inclusive dateFrom/dateTo filters into
// a half-open [from, before) range. Both accept YYYY-MM-DD (whole days, UTC) or
// an RFC 3339 timestamp; a date-only dateTo covers that whole day.
func feedbackDateRange(dateFrom, dateTo string) (from, before *time.Time, err error) {
	if dateFrom != "" {
		t, _, perr := parseFeedbackDate(dateFrom)
		if perr != nil {
			return nil, nil, &apierror.ValidationError{Msg: "dateFrom must be YYYY-MM-DD or an RFC 3339 timestamp"}
		}
		from = &t
	}
	if dateTo != "" {
		t, dateOnly, perr := parseFeedbackDate(dateTo)
		if perr != nil {
			return nil, nil, &apierror.ValidationError{Msg: "dateTo must be YYYY-MM-DD or an RFC 3339 timestamp"}
		}
		if dateOnly {
			t = t.AddDate(0, 0, 1)
		} else {
			t = t.Add(time.Microsecond)
		}
		before = &t
	}
	if from != nil && before != nil && !from.Before(*before) {
		return nil, nil, &apierror.ValidationError{Msg: "dateFrom must not be after dateTo"}
	}
	return from, before, nil
}

func parseFeedbackDate(v string) (t time.Time, dateOnly bool, err error) {
	if t, err = time.Parse("2006-01-02", v); err == nil {
		return t, true, nil
	}
	t, err = time.Parse(time.RFC3339, v)
	return t.UTC(), false, err
}
