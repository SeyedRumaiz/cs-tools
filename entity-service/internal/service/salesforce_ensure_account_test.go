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
	"strings"
	"testing"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/apierror"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/repository"
)

const testAccountRowID = "7c9e6679-7425-40de-944b-e07fc1f90ae7"

// TestEnsureAccount_Present: an account already in CSM is one lookup, no
// Sales Entity call, no write — twice over, which is the idempotency claim.
func TestEnsureAccount_Present(t *testing.T) {
	se := &stubSalesEntityClient{customer: sampleCustomer()}
	repo := &stubSalesforceAccountRepo{accountsBySfID: map[string]string{sampleCustomer().ID: testAccountRowID}}
	svc := NewSalesforceEventService(repo, se, SalesforceIngestSupport{Accounts: repo}).(*salesforceEventService)

	for i := 0; i < 2; i++ {
		id, err := svc.EnsureAccount(context.Background(), " "+sampleCustomer().ID+" ")
		if err != nil {
			t.Fatalf("call %d: unexpected error: %v", i+1, err)
		}
		if id != testAccountRowID {
			t.Errorf("call %d: id = %q, want %q", i+1, id, testAccountRowID)
		}
	}
	if repo.lookupBySfIDCalls != 2 || repo.upsertCalls != 0 || se.calls != 0 {
		t.Errorf("lookups=%d upserts=%d salesEntityCalls=%d, want 2/0/0", repo.lookupBySfIDCalls, repo.upsertCalls, se.calls)
	}
}

// TestEnsureAccount_AbsentIngestOn: with the Account ingest enabled the
// missing parent is fetched and written through the ordinary upsert, then
// read back.
func TestEnsureAccount_AbsentIngestOn(t *testing.T) {
	se := &stubSalesEntityClient{customer: sampleCustomer()}
	repo := &stubSalesforceAccountRepo{
		accountsBySfID:   map[string]string{},
		registerOnUpsert: testAccountRowID,
		lookup:           map[string]string{"owner@example.com": "user-1"},
	}
	svc := NewSalesforceEventService(repo, se, SalesforceIngestSupport{Accounts: repo}).(*salesforceEventService)

	id, err := svc.EnsureAccount(context.Background(), sampleCustomer().ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != testAccountRowID {
		t.Errorf("id = %q, want %q", id, testAccountRowID)
	}
	if repo.upsertCalls != 1 || se.calls != 1 || se.lastID != sampleCustomer().ID {
		t.Errorf("upserts=%d salesEntityCalls=%d lastID=%q, want one fetch and one write of the account", repo.upsertCalls, se.calls, se.lastID)
	}
	if repo.lastUpsert.SfID != sampleCustomer().ID || repo.lastUpsert.Name != "Acme" {
		t.Errorf("upsert row = %+v, want the mapped customer", repo.lastUpsert)
	}
	if repo.lookupBySfIDCalls != 2 {
		t.Errorf("lookups = %d, want 2 (before and after the upsert)", repo.lookupBySfIDCalls)
	}
}

// TestEnsureAccount_AbsentIngestOff: with the Account ingest off (nil write
// repository) the account can only arrive through the ServiceNow sync, so
// the caller gets a NotFoundError to fail its event on — and nothing is
// fetched or written.
func TestEnsureAccount_AbsentIngestOff(t *testing.T) {
	se := &stubSalesEntityClient{customer: sampleCustomer()}
	lookup := &stubSalesforceAccountRepo{accountsBySfID: map[string]string{}}
	svc := NewSalesforceEventService(nil, se, SalesforceIngestSupport{Accounts: lookup}).(*salesforceEventService)

	id, err := svc.EnsureAccount(context.Background(), sampleCustomer().ID)
	var nf *apierror.NotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("err = %v, want NotFoundError", err)
	}
	// The delayed-retry job only re-runs rows whose last_error has the
	// missing-parent prefix, so the message must carry it.
	if want := `account not found for sfId "` + sampleCustomer().ID + `"`; !strings.HasPrefix(nf.Msg, want) {
		t.Errorf("message = %q, want prefix %q", nf.Msg, want)
	}
	if !repository.IsMissingParentError(nf.Msg) {
		t.Errorf("message = %q is not recognised by repository.IsMissingParentError", nf.Msg)
	}
	if id != "" || lookup.upsertCalls != 0 || se.calls != 0 {
		t.Errorf("id=%q upserts=%d salesEntityCalls=%d, want nothing written or fetched", id, lookup.upsertCalls, se.calls)
	}
}

// TestEnsureAccount_UpsertFails: a failed parent ingest is the caller's
// error (Service Bus redelivers), not a NotFoundError, and there is no
// second lookup.
func TestEnsureAccount_UpsertFails(t *testing.T) {
	boom := &apierror.ServiceUnavailableError{Msg: "sales entity down"}
	se := &stubSalesEntityClient{err: boom}
	repo := &stubSalesforceAccountRepo{accountsBySfID: map[string]string{}}
	svc := NewSalesforceEventService(repo, se, SalesforceIngestSupport{Accounts: repo}).(*salesforceEventService)

	_, err := svc.EnsureAccount(context.Background(), sampleCustomer().ID)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the Sales Entity error", err)
	}
	if repo.lookupBySfIDCalls != 1 {
		t.Errorf("lookups = %d, want 1", repo.lookupBySfIDCalls)
	}
}

// TestEnsureAccount_WrittenButUnreadable: the upsert reporting success while
// the read-back finds nothing is a wiring fault worth a loud error, not a
// silent empty id.
func TestEnsureAccount_WrittenButUnreadable(t *testing.T) {
	se := &stubSalesEntityClient{customer: sampleCustomer()}
	repo := &stubSalesforceAccountRepo{accountsBySfID: map[string]string{}}
	svc := NewSalesforceEventService(repo, se, SalesforceIngestSupport{Accounts: repo}).(*salesforceEventService)

	id, err := svc.EnsureAccount(context.Background(), sampleCustomer().ID)
	if err == nil || !strings.Contains(err.Error(), "cannot be read back") {
		t.Fatalf("err = %v, want a read-back error", err)
	}
	if id != "" {
		t.Errorf("id = %q, want empty", id)
	}
}

// TestEnsureAccount_FallsBackToWriteRepoForReads: without
// SalesforceIngestSupport.Accounts the write repository (which can read too)
// serves the lookup, so an Account-ingest-only deployment still works.
func TestEnsureAccount_FallsBackToWriteRepoForReads(t *testing.T) {
	repo := &stubSalesforceAccountRepo{accountsBySfID: map[string]string{sampleCustomer().ID: testAccountRowID}}
	svc := NewSalesforceEventService(repo, &stubSalesEntityClient{}, SalesforceIngestSupport{}).(*salesforceEventService)

	id, err := svc.EnsureAccount(context.Background(), sampleCustomer().ID)
	if err != nil || id != testAccountRowID {
		t.Fatalf("id, err = %q, %v; want %q, nil", id, err, testAccountRowID)
	}
}

// TestEnsureAccount_Unconfigured: neither a lookup nor a write repository is
// a programming error, reported as such.
func TestEnsureAccount_Unconfigured(t *testing.T) {
	svc := NewSalesforceEventService(nil, &stubSalesEntityClient{}, SalesforceIngestSupport{}).(*salesforceEventService)
	if _, err := svc.EnsureAccount(context.Background(), sampleCustomer().ID); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("err = %v, want 'not configured'", err)
	}
}

// TestEnsureAccount_BlankID rejects an empty Salesforce id before touching
// anything.
func TestEnsureAccount_BlankID(t *testing.T) {
	repo := &stubSalesforceAccountRepo{accountsBySfID: map[string]string{}}
	svc := NewSalesforceEventService(repo, &stubSalesEntityClient{}, SalesforceIngestSupport{Accounts: repo}).(*salesforceEventService)
	_, err := svc.EnsureAccount(context.Background(), "  ")
	var ve *apierror.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("err = %v, want ValidationError", err)
	}
	if repo.lookupBySfIDCalls != 0 {
		t.Errorf("lookups = %d, want 0", repo.lookupBySfIDCalls)
	}
}

// TestEnsureAccount_LookupError propagates a database error from the lookup.
func TestEnsureAccount_LookupError(t *testing.T) {
	boom := errors.New("db down")
	repo := &stubSalesforceAccountRepo{lookupBySfIDErr: boom}
	svc := NewSalesforceEventService(repo, &stubSalesEntityClient{}, SalesforceIngestSupport{Accounts: repo}).(*salesforceEventService)
	if _, err := svc.EnsureAccount(context.Background(), sampleCustomer().ID); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the lookup error", err)
	}
	if repo.upsertCalls != 0 {
		t.Errorf("upserts = %d, want 0", repo.upsertCalls)
	}
}
