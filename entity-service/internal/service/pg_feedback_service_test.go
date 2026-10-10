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
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/apierror"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/repository"
)

const (
	fbCaseID    = "00000000-0000-0000-0000-000000000000"
	fbAccountID = "00000000-0000-0000-0000-000000000001"
)

// fakeFeedbackRepo records the arguments it is called with and returns canned rows.
type fakeFeedbackRepo struct {
	searchRows   []domain.CaseFeedback
	searchTotal  int
	periodRows   []domain.FeedbackBucketResult
	ratingRows   []repository.FeedbackRatingBucket
	err          error
	calls        int
	gotFilter    repository.FeedbackFilter
	gotLimit     int
	gotOffset    int
	gotPeriod    string
	gotRatingAgg bool
}

func (f *fakeFeedbackRepo) SearchFeedback(_ context.Context, flt repository.FeedbackFilter, limit, offset int) ([]domain.CaseFeedback, int, error) {
	f.calls++
	f.gotFilter, f.gotLimit, f.gotOffset = flt, limit, offset
	return f.searchRows, f.searchTotal, f.err
}

func (f *fakeFeedbackRepo) AggregateFeedbackByPeriod(_ context.Context, flt repository.FeedbackFilter, unit string) ([]domain.FeedbackBucketResult, error) {
	f.calls++
	f.gotFilter, f.gotPeriod = flt, unit
	return f.periodRows, f.err
}

func (f *fakeFeedbackRepo) AggregateFeedbackByRating(_ context.Context, flt repository.FeedbackFilter) ([]repository.FeedbackRatingBucket, error) {
	f.calls++
	f.gotFilter, f.gotRatingAgg = flt, true
	return f.ratingRows, f.err
}

func requireValidation(t *testing.T, err error, wantSubstr string) {
	t.Helper()
	var ve *apierror.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("err = %v (%T), want *apierror.ValidationError", err, err)
	}
	if !strings.Contains(ve.Msg, wantSubstr) {
		t.Errorf("Msg = %q, want it to contain %q", ve.Msg, wantSubstr)
	}
}

func TestPgFeedbackSearch_Validation(t *testing.T) {
	cases := []struct {
		name string
		req  domain.SearchFeedbackRequest
		want string
	}{
		{"negative page", domain.SearchFeedbackRequest{Page: -1}, "page must not be negative"},
		{"negative pageSize", domain.SearchFeedbackRequest{PageSize: -1}, "pageSize must not be negative"},
		{"pageSize over max", domain.SearchFeedbackRequest{PageSize: 51}, "pageSize cannot exceed 50"},
		{"bad account id", domain.SearchFeedbackRequest{Filters: domain.SearchFeedbackFilters{AccountIDs: []string{"nope"}}}, "accountIds"},
		{"bad case id", domain.SearchFeedbackRequest{Filters: domain.SearchFeedbackFilters{CaseID: "nope"}}, "caseId"},
		{"rating zero", domain.SearchFeedbackRequest{Filters: domain.SearchFeedbackFilters{Rating: intPtr(0)}}, "rating must be between 1 and 5"},
		{"rating six", domain.SearchFeedbackRequest{Filters: domain.SearchFeedbackFilters{Rating: intPtr(6)}}, "rating must be between 1 and 5"},
		{"bad dateFrom", domain.SearchFeedbackRequest{Filters: domain.SearchFeedbackFilters{DateFrom: "01/02/2026"}}, "dateFrom"},
		{"bad dateTo", domain.SearchFeedbackRequest{Filters: domain.SearchFeedbackFilters{DateTo: "x"}}, "dateTo"},
		{"from after to", domain.SearchFeedbackRequest{Filters: domain.SearchFeedbackFilters{DateFrom: "2026-09-02", DateTo: "2026-09-01"}}, "must not be after"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeFeedbackRepo{}
			_, err := NewPostgresFeedbackService(repo).SearchFeedback(context.Background(), tc.req)
			requireValidation(t, err, tc.want)
			if repo.calls != 0 {
				t.Errorf("repository called %d times on invalid input", repo.calls)
			}
		})
	}
}

func TestPgFeedbackSearch_DefaultsAndFilters(t *testing.T) {
	repo := &fakeFeedbackRepo{searchTotal: 42}
	svc := NewPostgresFeedbackService(repo)

	// No paging supplied: page 1, default page size.
	if _, err := svc.SearchFeedback(context.Background(), domain.SearchFeedbackRequest{}); err != nil {
		t.Fatal(err)
	}
	if repo.gotLimit != 20 || repo.gotOffset != 0 {
		t.Errorf("defaults: limit=%d offset=%d, want 20/0", repo.gotLimit, repo.gotOffset)
	}

	// Page 3 of 10 -> offset 20; filters passed through with dateTo made exclusive.
	_, err := svc.SearchFeedback(context.Background(), domain.SearchFeedbackRequest{
		Filters: domain.SearchFeedbackFilters{
			CaseID: fbCaseID, AccountIDs: []string{fbAccountID}, Rating: intPtr(4),
			DateFrom: "2026-08-01", DateTo: "2026-08-31",
		},
		Page: 3, PageSize: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if repo.gotLimit != 10 || repo.gotOffset != 20 {
		t.Errorf("paging: limit=%d offset=%d, want 10/20", repo.gotLimit, repo.gotOffset)
	}
	f := repo.gotFilter
	if f.CaseID != fbCaseID || !reflect.DeepEqual(f.AccountIDs, []string{fbAccountID}) || f.Rating == nil || *f.Rating != 4 {
		t.Errorf("filter = %+v", f)
	}
	wantFrom := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	wantBefore := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	if f.From == nil || !f.From.Equal(wantFrom) {
		t.Errorf("From = %v, want %v", f.From, wantFrom)
	}
	if f.Before == nil || !f.Before.Equal(wantBefore) {
		t.Errorf("Before = %v, want %v (inclusive dateTo -> next midnight)", f.Before, wantBefore)
	}
}

func TestPgFeedbackSearch_Results(t *testing.T) {
	comment := "Great help"
	repo := &fakeFeedbackRepo{
		searchRows:  []domain.CaseFeedback{{InstanceID: fbCaseID, CaseID: fbCaseID, Rating: 5, RatingLabel: "Very Satisfied", Comment: &comment}},
		searchTotal: 1,
	}
	resp, err := NewPostgresFeedbackService(repo).SearchFeedback(context.Background(), domain.SearchFeedbackRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.TotalRecords != 1 || len(resp.Results) != 1 || resp.Results[0].Rating != 5 {
		t.Errorf("resp = %+v", resp)
	}
}

func TestPgFeedbackSearch_EmptyIsNonNilSlice(t *testing.T) {
	resp, err := NewPostgresFeedbackService(&fakeFeedbackRepo{}).SearchFeedback(context.Background(), domain.SearchFeedbackRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Results == nil || len(resp.Results) != 0 || resp.TotalRecords != 0 {
		t.Errorf("resp = %+v, want empty non-nil results and zero total (JSON [] not null)", resp)
	}
}

func TestPgFeedbackSearch_RepoErrorPropagates(t *testing.T) {
	boom := errors.New("db down")
	_, err := NewPostgresFeedbackService(&fakeFeedbackRepo{err: boom}).SearchFeedback(context.Background(), domain.SearchFeedbackRequest{})
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want %v", err, boom)
	}
}

func TestPgFeedbackDateRange_Timestamps(t *testing.T) {
	from, before, err := feedbackDateRange("2026-08-01T10:00:00+02:00", "2026-08-31T23:59:59Z")
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC); !from.Equal(want) {
		t.Errorf("from = %v, want %v", from, want)
	}
	if !before.After(time.Date(2026, 8, 31, 23, 59, 59, 0, time.UTC)) {
		t.Errorf("before = %v, want just after the inclusive dateTo", before)
	}
}

func TestPgFeedbackAggregate_Validation(t *testing.T) {
	svc := NewPostgresFeedbackService(&fakeFeedbackRepo{})
	ctx := context.Background()

	_, err := svc.AggregateFeedback(ctx, domain.AggregateFeedbackRequest{})
	requireValidation(t, err, "bucket must be one of")
	_, err = svc.AggregateFeedback(ctx, domain.AggregateFeedbackRequest{Bucket: "year"})
	requireValidation(t, err, "bucket must be one of")
	_, err = svc.AggregateFeedback(ctx, domain.AggregateFeedbackRequest{
		Bucket: domain.FeedbackBucketDay, Filters: domain.AggregateFeedbackFilters{AccountIDs: []string{"x"}},
	})
	requireValidation(t, err, "accountIds")
	_, err = svc.AggregateFeedback(ctx, domain.AggregateFeedbackRequest{
		Bucket: domain.FeedbackBucketDay, Filters: domain.AggregateFeedbackFilters{DateFrom: "nope"},
	})
	requireValidation(t, err, "dateFrom")
}

func TestPgFeedbackAggregate_PeriodBuckets(t *testing.T) {
	for _, bucket := range []domain.FeedbackBucket{domain.FeedbackBucketDay, domain.FeedbackBucketWeek, domain.FeedbackBucketMonth} {
		t.Run(string(bucket), func(t *testing.T) {
			repo := &fakeFeedbackRepo{periodRows: []domain.FeedbackBucketResult{
				{BucketStart: "2026-07-01", AvgRating: 4.5, Count: 2},
				{BucketStart: "2026-08-01", AvgRating: 3, Count: 3},
			}}
			resp, err := NewPostgresFeedbackService(repo).AggregateFeedback(context.Background(), domain.AggregateFeedbackRequest{
				Bucket:  bucket,
				Filters: domain.AggregateFeedbackFilters{AccountIDs: []string{fbAccountID}, DateFrom: "2026-07-01"},
			})
			if err != nil {
				t.Fatal(err)
			}
			if repo.gotPeriod != string(bucket) {
				t.Errorf("period = %q, want %q", repo.gotPeriod, bucket)
			}
			if repo.gotFilter.CaseID != "" || len(repo.gotFilter.AccountIDs) != 1 || repo.gotFilter.From == nil {
				t.Errorf("filter = %+v", repo.gotFilter)
			}
			if len(resp.Buckets) != 2 || resp.TotalRecords != 5 {
				t.Errorf("resp = %+v, want 2 buckets, total 5", resp)
			}
		})
	}
}

func TestPgFeedbackAggregate_RatingBuckets(t *testing.T) {
	repo := &fakeFeedbackRepo{ratingRows: []repository.FeedbackRatingBucket{
		{Rating: 2, Label: "", AvgRating: 2, Count: 1},
		{Rating: 5, Label: "Delighted", AvgRating: 5, Count: 4},
	}}
	resp, err := NewPostgresFeedbackService(repo).AggregateFeedback(context.Background(), domain.AggregateFeedbackRequest{Bucket: domain.FeedbackBucketRating})
	if err != nil {
		t.Fatal(err)
	}
	if !repo.gotRatingAgg {
		t.Fatal("rating aggregate not used")
	}
	want := []domain.FeedbackBucketResult{
		{BucketStart: "Dissatisfied", AvgRating: 2, Count: 1}, // label falls back to the canonical name
		{BucketStart: "Delighted", AvgRating: 5, Count: 4},    // stored label wins
	}
	if !reflect.DeepEqual(resp.Buckets, want) || resp.TotalRecords != 5 {
		t.Errorf("resp = %+v, want buckets %+v total 5", resp, want)
	}
}

func TestPgFeedbackAggregate_ReasonsBucketsAreEmpty(t *testing.T) {
	for _, bucket := range []domain.FeedbackBucket{
		domain.FeedbackBucketReasonsVeryDissatisfied, domain.FeedbackBucketReasonsDissatisfied,
		domain.FeedbackBucketReasonsNeutral, domain.FeedbackBucketReasonsSatisfied, domain.FeedbackBucketReasonsVerySatisfied,
	} {
		t.Run(string(bucket), func(t *testing.T) {
			repo := &fakeFeedbackRepo{}
			resp, err := NewPostgresFeedbackService(repo).AggregateFeedback(context.Background(), domain.AggregateFeedbackRequest{Bucket: bucket})
			if err != nil {
				t.Fatal(err)
			}
			if resp.Buckets == nil || len(resp.Buckets) != 0 || resp.TotalRecords != 0 {
				t.Errorf("resp = %+v, want empty non-nil buckets", resp)
			}
			if repo.calls != 0 {
				t.Errorf("repository called %d times for a bucket it cannot serve", repo.calls)
			}
		})
	}
}

func TestPgFeedbackAggregate_EmptyAndRepoError(t *testing.T) {
	resp, err := NewPostgresFeedbackService(&fakeFeedbackRepo{}).AggregateFeedback(context.Background(), domain.AggregateFeedbackRequest{Bucket: domain.FeedbackBucketMonth})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Buckets == nil || len(resp.Buckets) != 0 || resp.TotalRecords != 0 {
		t.Errorf("resp = %+v", resp)
	}
	boom := errors.New("db down")
	for _, b := range []domain.FeedbackBucket{domain.FeedbackBucketMonth, domain.FeedbackBucketRating} {
		_, err = NewPostgresFeedbackService(&fakeFeedbackRepo{err: boom}).AggregateFeedback(context.Background(), domain.AggregateFeedbackRequest{Bucket: b})
		if !errors.Is(err, boom) {
			t.Errorf("bucket %s: err = %v, want %v", b, err, boom)
		}
	}
}
