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

package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/apierror"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/repository"
)

// fakeRegistryRepo records what the service hands the repository and returns
// canned rows or an error.
type fakeRegistryRepo struct {
	called   bool
	gotReq   domain.SearchCasesRequest
	gotMax   int
	cases    []domain.SearchCaseView
	err      error
	gotScope repository.SearchScope
}

func (f *fakeRegistryRepo) SearchAnnouncementCases(_ context.Context, req domain.SearchCasesRequest, scope repository.SearchScope, maxRows int) ([]domain.SearchCaseView, error) {
	f.called = true
	f.gotReq = req
	f.gotScope = scope
	f.gotMax = maxRows
	return f.cases, f.err
}

// The registry is staff data: a non-internal caller is refused before the
// repository is reached.
func TestAnnouncementRegistryRequiresInternalCaller(t *testing.T) {
	t.Parallel()
	repo := &fakeRegistryRepo{}
	_, err := NewAnnouncementRegistryService(repo, restrictedAccess{}).
		SearchRegistryCases(context.Background(), domain.SearchCasesRequest{})
	var forbidden *apierror.ForbiddenError
	if !errors.As(err, &forbidden) {
		t.Fatalf("want a ForbiddenError, got %v", err)
	}
	if repo.called {
		t.Fatal("repository was called for a non-internal caller")
	}
}

// Whatever type the caller sends, this endpoint reads announcements only.
func TestAnnouncementRegistryAlwaysFiltersToAnnouncements(t *testing.T) {
	t.Parallel()
	repo := &fakeRegistryRepo{}
	svc := NewAnnouncementRegistryService(repo, alwaysUnrestrictedAccess{})
	req := domain.SearchCasesRequest{Filters: domain.SearchCasesFilters{Filters: []domain.CaseFieldFilter{
		{Field: "type", Op: "in", Values: []string{"case", "engagement"}},
		{Field: "state", Op: "in", Values: []string{"open"}},
	}}}
	if _, err := svc.SearchRegistryCases(context.Background(), req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !repo.called {
		t.Fatal("repository was not called")
	}
	types := repo.gotReq.Parsed.Types
	if len(types) != 1 || !strings.EqualFold(types[0], "announcement") {
		t.Fatalf("repository got Types=%v, want only announcement", types)
	}
	// The state filter the caller did send survives alongside it.
	if len(repo.gotReq.Parsed.States) != 1 || repo.gotReq.Parsed.States[0] != domain.CaseStateOpen {
		t.Fatalf("repository got States=%v, want [open]", repo.gotReq.Parsed.States)
	}
	if repo.gotMax != maxAnnouncementRegistryRows {
		t.Fatalf("repository got maxRows=%d, want %d", repo.gotMax, maxAnnouncementRegistryRows)
	}
}

// anyOf is rejected rather than silently ignored, which would widen the result.
func TestAnnouncementRegistryRejectsAnyOf(t *testing.T) {
	t.Parallel()
	repo := &fakeRegistryRepo{}
	req := domain.SearchCasesRequest{Filters: domain.SearchCasesFilters{AnyOf: []domain.CaseFilterBranch{{}}}}
	_, err := NewAnnouncementRegistryService(repo, alwaysUnrestrictedAccess{}).SearchRegistryCases(context.Background(), req)
	var validation *apierror.ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("want a ValidationError, got %v", err)
	}
	if repo.called {
		t.Fatal("repository was called for a rejected request")
	}
}

// Invalid filters fail with the same validation as /cases/search, before any query.
func TestAnnouncementRegistryRejectsInvalidFilters(t *testing.T) {
	t.Parallel()
	repo := &fakeRegistryRepo{}
	req := domain.SearchCasesRequest{Filters: domain.SearchCasesFilters{Filters: []domain.CaseFieldFilter{
		{Field: "projectId", Op: "in", Values: []string{"not-a-uuid"}},
	}}}
	_, err := NewAnnouncementRegistryService(repo, alwaysUnrestrictedAccess{}).SearchRegistryCases(context.Background(), req)
	var validation *apierror.ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("want a ValidationError, got %v", err)
	}
	if repo.called {
		t.Fatal("repository was called for an invalid request")
	}
}

// More rows than the bound is a clear validation error, never a truncated list.
func TestAnnouncementRegistryTooManyRows(t *testing.T) {
	t.Parallel()
	repo := &fakeRegistryRepo{err: repository.ErrTooManyRegistryRows}
	_, err := NewAnnouncementRegistryService(repo, alwaysUnrestrictedAccess{}).
		SearchRegistryCases(context.Background(), domain.SearchCasesRequest{})
	var validation *apierror.ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("want a ValidationError, got %v", err)
	}
}

// Rows pass straight through, total is their count, and other repository
// errors are not swallowed.
func TestAnnouncementRegistryResultAndErrors(t *testing.T) {
	t.Parallel()
	subject := "a"
	repo := &fakeRegistryRepo{cases: []domain.SearchCaseView{{ID: "1", Subject: &subject}, {ID: "2"}}}
	resp, err := NewAnnouncementRegistryService(repo, alwaysUnrestrictedAccess{}).
		SearchRegistryCases(context.Background(), domain.SearchCasesRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Total != 2 || len(resp.Cases) != 2 || resp.Cases[0].ID != "1" || resp.Cases[1].ID != "2" {
		t.Fatalf("unexpected response: %+v", resp)
	}

	boom := errors.New("boom")
	_, err = NewAnnouncementRegistryService(&fakeRegistryRepo{err: boom}, alwaysUnrestrictedAccess{}).
		SearchRegistryCases(context.Background(), domain.SearchCasesRequest{})
	if !errors.Is(err, boom) {
		t.Fatalf("want the repository error to pass through, got %v", err)
	}
}
