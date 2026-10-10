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
	"slices"
	"testing"
	"time"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/repository"
)

// fakeIngestStateRepo is an in-memory salesforce_ingest_state, keyed by
// entity + "/" + sfId.
type fakeIngestStateRepo struct {
	rows     map[string]domain.SalesforceIngestState
	failed   []domain.SalesforceIngestState
	upserts  []domain.UpsertSalesforceIngestStateRequest
	getCalls int
	getErr   error
	listErr  error
	// listEntities records the entities each ListMissingParentFailures
	// call asked for; retryAttempts each RecordRetryAttempt call.
	listEntities  [][]string
	retryAttempts []string
	requeued      []repository.MissingParent
}

func (f *fakeIngestStateRepo) Get(_ context.Context, entity, sfID string) (*domain.SalesforceIngestState, error) {
	f.getCalls++
	if f.getErr != nil {
		return nil, f.getErr
	}
	st, ok := f.rows[entity+"/"+sfID]
	if !ok {
		return nil, nil
	}
	return &st, nil
}

func (f *fakeIngestStateRepo) Upsert(_ context.Context, req domain.UpsertSalesforceIngestStateRequest) (domain.SalesforceIngestState, error) {
	f.upserts = append(f.upserts, req)
	return domain.SalesforceIngestState{Entity: req.Entity, SfID: req.SfID, Status: req.Status, EventType: req.EventType, EventModifiedOn: req.EventModifiedOn}, nil
}

// apply records req the way upsertSalesforceIngestState does inside the
// account transaction: the outcome moves only for an event at least as new as
// the recorded one, or over a row stamped DELETED, and event_modified_on only
// moves forward. A nil receiver ignores the write.
func (f *fakeIngestStateRepo) apply(req domain.UpsertSalesforceIngestStateRequest) {
	if f == nil {
		return
	}
	if f.rows == nil {
		f.rows = map[string]domain.SalesforceIngestState{}
	}
	key := req.Entity + "/" + req.SfID
	cur, ok := f.rows[key]
	if !ok || !req.EventModifiedOn.Before(cur.EventModifiedOn) || cur.EventType == domain.SalesforceEventDeleted {
		modified := req.EventModifiedOn
		if ok && cur.EventModifiedOn.After(modified) {
			modified = cur.EventModifiedOn
		}
		cur = domain.SalesforceIngestState{
			Entity: req.Entity, SfID: req.SfID, Status: req.Status, EventType: req.EventType,
			EventModifiedOn: modified, LastError: req.LastError,
		}
	}
	f.rows[key] = cur
}

func (f *fakeIngestStateRepo) ListMissingParentFailures(_ context.Context, entities []string, _ time.Duration, maxAttempts, limit int) ([]domain.SalesforceIngestState, error) {
	f.listEntities = append(f.listEntities, entities)
	if f.listErr != nil {
		return nil, f.listErr
	}
	// The same eligibility the SQL applies, before the limit.
	out := []domain.SalesforceIngestState{}
	for _, st := range f.failed {
		if len(out) == limit {
			break
		}
		if st.Status == domain.SalesforceIngestFailed && slices.Contains(entities, st.Entity) &&
			repository.IsMissingParentError(derefString(st.LastError)) && st.RetryCount < maxAttempts {
			out = append(out, st)
		}
	}
	return out, nil
}

func (f *fakeIngestStateRepo) RecordRetryAttempt(_ context.Context, entity, sfID string) (bool, error) {
	f.retryAttempts = append(f.retryAttempts, entity+"/"+sfID)
	return true, nil
}

func (f *fakeIngestStateRepo) RequeueMissingParentFailures(_ context.Context, parent repository.MissingParent) (int64, error) {
	f.requeued = append(f.requeued, parent)
	return 0, nil
}

func ingestStateRow(status domain.SalesforceIngestStatus, eventType, modified string) domain.SalesforceIngestState {
	on, ok := parseSalesforceLastModified(&modified)
	if !ok {
		panic("bad fixture date " + modified)
	}
	return domain.SalesforceIngestState{
		Entity: domain.SalesforceIngestEntityAccount, SfID: testAccountID,
		Status: status, EventType: eventType, EventModifiedOn: on,
	}
}

// TestShouldSkipIngest pins the three rules the guard shares with
// ingestMembership: a SUCCEEDED row at or past the incoming version skips, a
// row stamped DELETED never blocks, and FAILED / older rows let the event
// through.
func TestShouldSkipIngest(t *testing.T) {
	const (
		older    = "2026-09-18T06:00:00.000+0000"
		incoming = "2026-09-18T06:37:07.000+0000"
		newer    = "2026-09-18T07:00:00.000+0000"
	)
	cases := []struct {
		name     string
		recorded *domain.SalesforceIngestState
		skip     bool
	}{
		{"never ingested", nil, false},
		{"succeeded, same version", ptrState(ingestStateRow(domain.SalesforceIngestSucceeded, domain.SalesforceEventUpdated, incoming)), true},
		{"succeeded, newer version already written", ptrState(ingestStateRow(domain.SalesforceIngestSucceeded, domain.SalesforceEventUpdated, newer)), true},
		{"succeeded, older version", ptrState(ingestStateRow(domain.SalesforceIngestSucceeded, domain.SalesforceEventUpdated, older)), false},
		{"failed, same version", ptrState(ingestStateRow(domain.SalesforceIngestFailed, domain.SalesforceEventUpdated, incoming)), false},
		{"failed, newer version", ptrState(ingestStateRow(domain.SalesforceIngestFailed, domain.SalesforceEventUpdated, newer)), false},
		{"deleted, same version (restore must get through)", ptrState(ingestStateRow(domain.SalesforceIngestSucceeded, domain.SalesforceEventDeleted, incoming)), false},
		{"deleted, newer version", ptrState(ingestStateRow(domain.SalesforceIngestSucceeded, domain.SalesforceEventDeleted, newer)), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			states := &fakeIngestStateRepo{rows: map[string]domain.SalesforceIngestState{}}
			if tc.recorded != nil {
				states.rows[tc.recorded.Entity+"/"+tc.recorded.SfID] = *tc.recorded
			}
			lastModified := incoming
			skip, on, err := shouldSkipIngest(context.Background(), states, domain.SalesforceIngestEntityAccount, testAccountID, domain.SalesforceEventRestored, &lastModified)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if skip != tc.skip {
				t.Errorf("skip = %v, want %v", skip, tc.skip)
			}
			want, _ := parseSalesforceLastModified(&lastModified)
			if !on.Equal(want) {
				t.Errorf("eventModifiedOn = %v, want the incoming version %v", on, want)
			}
			if states.getCalls != 1 {
				t.Errorf("ledger read %d times, want 1", states.getCalls)
			}
		})
	}
}

// TestShouldSkipIngest_NoParseableDate: a missing or malformed
// LastModifiedDate skips the guard (the ledger is not even read), never the
// event, and the version handed back is "now" so the ledger row written
// afterwards still orders after everything before it.
func TestShouldSkipIngest_NoParseableDate(t *testing.T) {
	garbage := "yesterday-ish"
	for name, raw := range map[string]*string{"nil": nil, "empty": sampleStr("  "), "garbage": &garbage} {
		t.Run(name, func(t *testing.T) {
			states := &fakeIngestStateRepo{rows: map[string]domain.SalesforceIngestState{
				domain.SalesforceIngestEntityAccount + "/" + testAccountID: ingestStateRow(domain.SalesforceIngestSucceeded, domain.SalesforceEventUpdated, "2099-01-01T00:00:00.000+0000"),
			}}
			before := time.Now().UTC()
			skip, on, err := shouldSkipIngest(context.Background(), states, domain.SalesforceIngestEntityAccount, testAccountID, domain.SalesforceEventUpdated, raw)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if skip {
				t.Error("an unguarded event must not be skipped")
			}
			if on.Before(before) || on.After(time.Now().UTC().Add(time.Second)) {
				t.Errorf("eventModifiedOn = %v, want roughly now", on)
			}
			if states.getCalls != 0 {
				t.Errorf("ledger read %d times, want 0 when the guard is skipped", states.getCalls)
			}
		})
	}
}

// TestShouldSkipIngest_LedgerError: a database error reading the ledger is
// returned, not swallowed into "not a duplicate" — the caller's event fails
// and Service Bus redelivers it.
func TestShouldSkipIngest_LedgerError(t *testing.T) {
	boom := errors.New("db down")
	states := &fakeIngestStateRepo{getErr: boom}
	lastModified := testLastModified
	skip, _, err := shouldSkipIngest(context.Background(), states, domain.SalesforceIngestEntityAccount, testAccountID, domain.SalesforceEventUpdated, &lastModified)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the ledger error", err)
	}
	if skip {
		t.Error("skip must be false alongside an error")
	}
}

func ptrState(s domain.SalesforceIngestState) *domain.SalesforceIngestState { return &s }
