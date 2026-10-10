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
	"os"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
)

// These run the delayed-retry job's reads and writes against a live
// PostgreSQL instance. Skipped unless ENTITY_TEST_DATABASE_URL is set (see
// account_repo_salesforce_integration_test.go for the setup).

const (
	rsiEntity    = "retry-it-widget"
	rsiOther     = "retry-it-other"
	rsiStepMSfID = "a0eRETRYIT0000001AAA"
)

func newRetryStateIntegrationPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("ENTITY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("ENTITY_TEST_DATABASE_URL is not set; skipping the live-database tests")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	clean := func() {
		if _, err := pool.Exec(ctx, `DELETE FROM salesforce_ingest_state WHERE entity IN ($1, $2)`, rsiEntity, rsiOther); err != nil {
			t.Fatalf("clean ledger: %v", err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM onboarding_step WHERE membership_sf_id = $1`, rsiStepMSfID); err != nil {
			t.Fatalf("clean steps: %v", err)
		}
	}
	clean()
	t.Cleanup(clean)
	return pool
}

// attempt_count counts consecutive failures: it restarts at 1 on a success
// and on the first failure after one.
func TestSalesforceIngestStateIntegration_AttemptCountIsConsecutiveFailures(t *testing.T) {
	pool := newRetryStateIntegrationPool(t)
	ctx := context.Background()
	repo := &salesforceIngestStateRepo{db: pool}
	base := time.Date(2026, 9, 18, 6, 0, 0, 0, time.UTC)
	errMsg := `account not found for sfId "001X"`

	write := func(status domain.SalesforceIngestStatus, minute int) domain.SalesforceIngestState {
		t.Helper()
		req := domain.UpsertSalesforceIngestStateRequest{
			Entity: rsiEntity, SfID: "w-1", EventModifiedOn: base.Add(time.Duration(minute) * time.Minute),
			EventType: domain.SalesforceEventUpdated, Status: status,
		}
		if status == domain.SalesforceIngestFailed {
			req.LastError = &errMsg
		}
		st, err := repo.Upsert(ctx, req)
		if err != nil {
			t.Fatalf("upsert: %v", err)
		}
		return st
	}
	for i, tc := range []struct {
		status domain.SalesforceIngestStatus
		minute int
		want   int
	}{
		{domain.SalesforceIngestFailed, 0, 1},
		{domain.SalesforceIngestFailed, 0, 2},
		{domain.SalesforceIngestFailed, 1, 3},
		{domain.SalesforceIngestSucceeded, 2, 1},
		{domain.SalesforceIngestSucceeded, 3, 1},
		{domain.SalesforceIngestSucceeded, 4, 1},
		{domain.SalesforceIngestFailed, 5, 1},
		{domain.SalesforceIngestFailed, 5, 2},
		// A stale SUCCEEDED does not move the FAILED outcome, so the row is
		// still failing and the count keeps going.
		{domain.SalesforceIngestSucceeded, 1, 3},
	} {
		if got := write(tc.status, tc.minute); got.AttemptCount != tc.want {
			t.Errorf("write %d (%s @%dm): attempt_count = %d, want %d (status now %s)", i, tc.status, tc.minute, got.AttemptCount, tc.want, got.Status)
		}
	}
}

// ListMissingParentFailures applies every eligibility rule in SQL, with the
// cap on retry_count, and RecordRetryAttempt counts only into retry_count.
func TestSalesforceIngestStateIntegration_RetryReadAndAttempt(t *testing.T) {
	pool := newRetryStateIntegrationPool(t)
	ctx := context.Background()
	repo := &salesforceIngestStateRepo{db: pool}

	insert := func(entity, sfID, status, lastError string, retries int) {
		t.Helper()
		if _, err := pool.Exec(ctx, `INSERT INTO salesforce_ingest_state
			(entity, sf_id, event_modified_on, event_type, status, last_error, attempt_count, retry_count, updated_on)
			VALUES ($1, $2, now(), 'UPDATED', $3, NULLIF($4, ''), 20, $5, now() - interval '1 hour')`,
			entity, sfID, status, lastError, retries); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	insert(rsiEntity, "eligible", "FAILED", `account not found for sfId "001X"`, 0)
	insert(rsiEntity, "other-error", "FAILED", "customer is missing Name", 1)
	insert(rsiEntity, "capped", "FAILED", "project not found", 12)
	insert(rsiEntity, "succeeded", "SUCCEEDED", "", 1)
	insert(rsiOther, "no-retrier", "FAILED", "project not found", 1)

	rows, err := repo.ListMissingParentFailures(ctx, []string{rsiEntity}, time.Minute, 12, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 || rows[0].SfID != "eligible" {
		t.Fatalf("rows = %+v, want only the eligible one", rows)
	}
	if none, err := repo.ListMissingParentFailures(ctx, nil, time.Minute, 12, 10); err != nil || len(none) != 0 {
		t.Errorf("no entities: rows = %v, err = %v", none, err)
	}

	seen := rows[0].UpdatedOn
	if ok, err := repo.RecordRetryAttempt(ctx, rsiEntity, "succeeded"); err != nil || ok {
		t.Errorf("a SUCCEEDED row must not count: ok=%v err=%v", ok, err)
	}
	if ok, err := repo.RecordRetryAttempt(ctx, rsiEntity, "eligible"); err != nil || !ok {
		t.Fatalf("RecordRetryAttempt: ok=%v err=%v", ok, err)
	}
	got, err := repo.Get(ctx, rsiEntity, "eligible")
	if err != nil || got == nil {
		t.Fatalf("get: %v", err)
	}
	if got.RetryCount != 1 || got.AttemptCount != 20 || got.LastError == nil || *got.LastError != `account not found for sfId "001X"` || !got.UpdatedOn.After(seen) {
		t.Errorf("after the attempt: %+v", got)
	}
	// Now inside the interval: not listed again until it has waited.
	if again, _ := repo.ListMissingParentFailures(ctx, []string{rsiEntity}, time.Minute, 12, 10); len(again) != 0 {
		t.Errorf("a just-attempted row must wait an interval: %+v", again)
	}
}

func TestOnboardingStepIntegration_RecordRetryAttempt(t *testing.T) {
	pool := newRetryStateIntegrationPool(t)
	ctx := context.Background()
	repo := &onboardingStepRepo{db: pool}

	var id string
	var seen time.Time
	if err := pool.QueryRow(ctx, `INSERT INTO onboarding_step
		(created_by, updated_by, membership_sf_id, email, step, status, attempt_count, retry_count, last_error, event_type, event_modified_on, updated_on)
		VALUES ('retry-it', 'retry-it', $1, 'retry-it@example.test', 'DATABASE', 'FAILED', 3, 2, 'project not found for key "RETRYIT" / sfId ""', 'UPDATED', now(), now() - interval '1 hour')
		RETURNING id::text, updated_on`, rsiStepMSfID).Scan(&id, &seen); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if ok, err := repo.RecordRetryAttempt(ctx, id); err != nil || !ok {
		t.Fatalf("RecordRetryAttempt: ok=%v err=%v", ok, err)
	}
	var attempts, retries int
	var lastError string
	var updatedOn time.Time
	read := func() {
		t.Helper()
		if err := pool.QueryRow(ctx, `SELECT attempt_count, retry_count, last_error, updated_on FROM onboarding_step WHERE id = $1::uuid`, id).Scan(&attempts, &retries, &lastError, &updatedOn); err != nil {
			t.Fatal(err)
		}
	}
	read()
	if attempts != 3 || retries != 3 || lastError != `project not found for key "RETRYIT" / sfId ""` || !updatedOn.After(seen) {
		t.Errorf("attempt_count = %d, retry_count = %d, last_error = %q; want 3, 3 and the missing-parent error kept", attempts, retries, lastError)
	}

	// The project arriving by key re-queues the step; another key does not.
	if n, err := repo.RequeueMissingParentFailures(ctx, MissingParent{Kind: MissingParentProject, SfID: "a0pOTHER", Key: "RETRYIT2"}); err != nil || n != 0 {
		t.Errorf("other project: n=%d err=%v, want 0", n, err)
	}
	if n, err := repo.RequeueMissingParentFailures(ctx, MissingParent{Kind: MissingParentProject, SfID: "a0pRETRYIT", Key: "RETRYIT"}); err != nil || n != 1 {
		t.Errorf("its project: n=%d err=%v, want 1", n, err)
	}
	read()
	if retries != 0 || attempts != 3 {
		t.Errorf("after requeue: retry_count = %d, attempt_count = %d; want 0 and 3", retries, attempts)
	}
}

// Event writes never add to retry_count: it is kept while the row stays
// FAILED, cleared by a success, and reset when its parent arrives.
func TestSalesforceIngestStateIntegration_RetryCountIsTheJobs(t *testing.T) {
	pool := newRetryStateIntegrationPool(t)
	ctx := context.Background()
	repo := &salesforceIngestStateRepo{db: pool}
	errMsg := `account not found for sfId "001RETRYIT0000001AA"`
	write := func(status domain.SalesforceIngestStatus) domain.SalesforceIngestState {
		t.Helper()
		req := domain.UpsertSalesforceIngestStateRequest{Entity: rsiEntity, SfID: "w-1", EventModifiedOn: time.Now(), EventType: domain.SalesforceEventUpdated, Status: status}
		if status == domain.SalesforceIngestFailed {
			req.LastError = &errMsg
		}
		st, err := repo.Upsert(ctx, req)
		if err != nil {
			t.Fatalf("upsert: %v", err)
		}
		return st
	}
	write(domain.SalesforceIngestFailed)
	for i := 0; i < 2; i++ {
		if _, err := repo.RecordRetryAttempt(ctx, rsiEntity, "w-1"); err != nil {
			t.Fatal(err)
		}
	}
	if got := write(domain.SalesforceIngestFailed); got.RetryCount != 2 || got.AttemptCount != 2 {
		t.Errorf("redelivery: retry_count = %d, attempt_count = %d; want 2 and 2", got.RetryCount, got.AttemptCount)
	}
	if n, err := repo.RequeueMissingParentFailures(ctx, MissingParent{Kind: MissingParentProject, SfID: "001RETRYIT0000001AA"}); err != nil || n != 0 {
		t.Errorf("a project with the same id must not match an account error: n=%d err=%v", n, err)
	}
	if n, err := repo.RequeueMissingParentFailures(ctx, MissingParent{Kind: MissingParentAccount, SfID: "001RETRYIT0000001AAA"}); err != nil || n != 0 {
		t.Errorf("a longer id must not match: n=%d err=%v", n, err)
	}
	if n, err := repo.RequeueMissingParentFailures(ctx, MissingParent{Kind: MissingParentAccount, SfID: "001RETRYIT0000001AA"}); err != nil || n != 1 {
		t.Errorf("its account: n=%d err=%v, want 1", n, err)
	}
	if _, err := repo.RecordRetryAttempt(ctx, rsiEntity, "w-1"); err != nil {
		t.Fatal(err)
	}
	if got := write(domain.SalesforceIngestSucceeded); got.RetryCount != 0 {
		t.Errorf("success: retry_count = %d, want 0", got.RetryCount)
	}
}

func TestMissingParentMatch(t *testing.T) {
	cases := map[string]struct {
		parent      MissingParent
		wantPrefix  string
		wantNeedles []string
	}{
		"project with key": {MissingParent{Kind: MissingParentProject, SfID: "a0p1", Key: "ACME"}, "project not found%", []string{`sfId "a0p1"`, `key "ACME"`}},
		"18-char account":  {MissingParent{Kind: MissingParentAccount, SfID: "001E2000025AYH2IAO", Key: "ignored"}, "account not found%", []string{`sfId "001E2000025AYH2IAO"`, `sfId "001E2000025AYH2"`}},
		"nothing to match": {MissingParent{Kind: MissingParentAccount}, "account not found%", nil},
		"unknown kind":     {MissingParent{Kind: "opportunity", SfID: "006X"}, "", nil},
	}
	for name, tc := range cases {
		prefix, needles := tc.parent.match()
		if prefix != tc.wantPrefix || !slices.Equal(needles, tc.wantNeedles) {
			t.Errorf("%s: match() = %q, %q; want %q, %q", name, prefix, needles, tc.wantPrefix, tc.wantNeedles)
		}
	}
}
