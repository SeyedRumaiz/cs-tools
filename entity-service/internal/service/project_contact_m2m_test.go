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
	"errors"
	"reflect"
	"testing"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/apierror"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/repository"
)

type fakeProjectContactRepo struct {
	rows        []repository.ProjectContactRow
	callerEmail string
	called      bool
}

func (f *fakeProjectContactRepo) SearchProjectContacts(_ context.Context, _ string, _ domain.SearchProjectContactsRequest, callerEmail string) ([]repository.ProjectContactRow, int, error) {
	f.called, f.callerEmail = true, callerEmail
	return f.rows, len(f.rows), nil
}

func (f *fakeProjectContactRepo) GetProjectContactByUserID(_ context.Context, _, _, callerEmail string) (repository.ProjectContactRow, error) {
	f.called, f.callerEmail = true, callerEmail
	return repository.ProjectContactRow{}, nil
}

const m2mProjectID = "5b1e8d6a-3b4c-4d5e-8f90-123456789abc"

// TestSearchProjectContacts_CallerResolution pins that an internal client with no user
// token (csm-integration-service) is served, while any other tokenless caller is refused.
func TestSearchProjectContacts_CallerResolution(t *testing.T) {
	cases := []struct {
		name       string
		ctx        context.Context
		access     AccessService
		wantErr    any
		wantCaller string
	}{
		{"internal client without token", context.Background(), stubAccess{scope: AccessScope{Unrestricted: true}}, nil, ""},
		{"restricted caller without token", context.Background(), stubAccess{scope: AccessScope{ProjectIDs: []string{m2mProjectID}}}, &apierror.UnauthorizedError{}, ""},
		{"scope resolution fails", context.Background(), stubAccess{err: &apierror.UnauthorizedError{Msg: "no"}}, &apierror.UnauthorizedError{}, ""},
		{"user token still resolves the caller email", contextWithUserIDToken(fakeJWTWithEmail(t, "jane@acme.com")), stubAccess{err: errors.New("must not be consulted")}, nil, "jane@acme.com"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeProjectContactRepo{}
			svc := NewProjectContactService(repo, tc.access)
			_, err := svc.SearchProjectContacts(tc.ctx, m2mProjectID, domain.SearchProjectContactsRequest{})
			if tc.wantErr != nil {
				var unauth *apierror.UnauthorizedError
				if !errors.As(err, &unauth) {
					t.Fatalf("err = %v, want UnauthorizedError", err)
				}
				if repo.called {
					t.Fatal("repository must not be queried for a refused caller")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if repo.callerEmail != tc.wantCaller {
				t.Errorf("callerEmail = %q, want %q", repo.callerEmail, tc.wantCaller)
			}
		})
	}
}

// TestSearchProjectContacts_ReturnsBusinessContactRole pins that BUSINESS_CONTACT, held
// through "Business Contact  Group", reaches the response roles like any other role.
func TestSearchProjectContacts_ReturnsBusinessContactRole(t *testing.T) {
	repo := &fakeProjectContactRepo{rows: []repository.ProjectContactRow{
		{Email: "deep@acme.com", RegistrationState: "INVITED", Roles: []string{"BUSINESS_CONTACT"}},
		{Email: "sam@acme.com", RegistrationState: "REGISTERED", Roles: []string{"BUSINESS_CONTACT", "PORTAL_USER", "SECURITY_CONTACT"}},
	}}
	svc := NewProjectContactService(repo, stubAccess{scope: AccessScope{Unrestricted: true}})
	resp, err := svc.SearchProjectContacts(context.Background(), m2mProjectID, domain.SearchProjectContactsRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := [][]string{{"BUSINESS_CONTACT"}, {"BUSINESS_CONTACT", "PORTAL_USER", "SECURITY_CONTACT"}}
	for i, c := range resp.Contacts {
		if !reflect.DeepEqual(c.Roles, want[i]) {
			t.Errorf("contact %d roles = %v, want %v", i, c.Roles, want[i])
		}
	}
}

type fakeAccountContactRepo struct{ callerEmail string }

func (f *fakeAccountContactRepo) SearchAccountContacts(_ context.Context, _ string, _ domain.SearchAccountContactsRequest, callerEmail string) ([]repository.AccountContactRow, int, error) {
	f.callerEmail = callerEmail
	return nil, 0, nil
}

func TestSearchAccountContacts_InternalClientWithoutToken(t *testing.T) {
	svc := NewAccountContactService(&fakeAccountContactRepo{}, stubAccess{scope: AccessScope{Unrestricted: true}})
	if _, err := svc.SearchAccountContacts(context.Background(), m2mProjectID, domain.SearchAccountContactsRequest{}); err != nil {
		t.Fatalf("internal client must be served, got %v", err)
	}
	svc = NewAccountContactService(&fakeAccountContactRepo{}, stubAccess{scope: AccessScope{}})
	var unauth *apierror.UnauthorizedError
	if _, err := svc.SearchAccountContacts(context.Background(), m2mProjectID, domain.SearchAccountContactsRequest{}); !errors.As(err, &unauth) {
		t.Fatalf("err = %v, want UnauthorizedError", err)
	}
}
