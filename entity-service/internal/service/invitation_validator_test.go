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
	"testing"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/apierror"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/salesentity"
)

const (
	ivProjectSfID     = "a0d000000000001AAA"
	ivCustomerSfID    = "001000000000001AAA"
	ivPartnerSfID     = "001000000000002AAA"
	ivOtherSfID       = "001000000000003AAA"
	ivAccountID       = "11111111-2222-3333-4444-555555555555"
	ivCustomerAdmin   = "admin@acme.com"
	ivPartnerAdmin    = "lead@partner.io"
	ivUnrelatedInvite = "someone@unrelated.org"
)

var ivTarget = domain.MembershipWriteTarget{
	ProjectID: "p-1", ProjectSfID: ivProjectSfID, AccountID: ivAccountID, AccountSfID: ivCustomerSfID,
}

type fakeInvitationSales struct {
	contacts  map[string][]salesentity.Contact
	customers map[string]salesentity.Customer
	err       error
}

func (f *fakeInvitationSales) SearchContactsByEmail(_ context.Context, email string, limit int) ([]salesentity.Contact, error) {
	if f.err != nil {
		return nil, f.err
	}
	rows := f.contacts[email]
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}

func (f *fakeInvitationSales) GetCustomer(_ context.Context, id string) (salesentity.Customer, error) {
	if f.err != nil {
		return salesentity.Customer{}, f.err
	}
	c, ok := f.customers[id]
	if !ok {
		return salesentity.Customer{}, &apierror.ServiceUnavailableError{Msg: "salesentity: customer not found"}
	}
	return c, nil
}

type fakePartnerRepo struct {
	ids []string
	err error
}

func (f fakePartnerRepo) PartnerAccountSfIDs(context.Context, string) ([]string, error) {
	return f.ids, f.err
}

func ivContact(email, accountSfID string, memberships ...salesentity.ContactMembership) salesentity.Contact {
	return salesentity.Contact{
		ID: sampleStr("003" + email), Email: sampleStr(email),
		Account: &salesentity.ContactAccount{ID: sampleStr(accountSfID)}, Memberships: memberships,
	}
}

// newIV builds the usual world: Acme (customer, owns the project, domains
// acme.com) is partnered by Partner.io (domains partner.io); an unrelated
// account exists with its own domain.
func newIV() (*fakeInvitationSales, *invitationValidator) {
	sales := &fakeInvitationSales{
		contacts: map[string][]salesentity.Contact{
			ivCustomerAdmin:   {ivContact(ivCustomerAdmin, ivCustomerSfID)},
			ivPartnerAdmin:    {ivContact(ivPartnerAdmin, ivPartnerSfID)},
			"x@unrelated.org": {ivContact("x@unrelated.org", ivOtherSfID)},
		},
		customers: map[string]salesentity.Customer{
			ivCustomerSfID: {ID: ivCustomerSfID, DomainList: sampleStr("acme.com, Acme.io")},
			ivPartnerSfID:  {ID: ivPartnerSfID, DomainList: sampleStr("partner.io")},
			ivOtherSfID:    {ID: ivOtherSfID, DomainList: sampleStr("unrelated.org")},
		},
	}
	v := &invitationValidator{sales: sales, partners: fakePartnerRepo{ids: []string{ivPartnerSfID}}}
	return sales, v
}

func wantErrKind(t *testing.T, err error, want any) {
	t.Helper()
	switch want.(type) {
	case nil:
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
	case *apierror.ForbiddenError:
		var e *apierror.ForbiddenError
		if !errors.As(err, &e) {
			t.Fatalf("err = %T %v, want ForbiddenError", err, err)
		}
	case *apierror.ValidationError:
		var e *apierror.ValidationError
		if !errors.As(err, &e) {
			t.Fatalf("err = %T %v, want ValidationError", err, err)
		}
	case *apierror.ConflictError:
		var e *apierror.ConflictError
		if !errors.As(err, &e) {
			t.Fatalf("err = %T %v, want ConflictError", err, err)
		}
	}
}

func TestInvitationValidator_Rules(t *testing.T) {
	tests := []struct {
		name    string
		email   string
		inviter string
		setup   func(*fakeInvitationSales, *invitationValidator)
		want    any
	}{
		// Checks on the invitee's address: both portals.
		{name: "customer domain, CSM Portal", email: "new@acme.com", want: nil},
		{name: "second listed domain, any case", email: "new@ACME.io", want: nil},
		{name: "partner's domain, CSM Portal", email: "new@partner.io", want: nil},
		{name: "public domain", email: "someone@gmail.com", want: &apierror.ForbiddenError{}},
		{name: "public domain even from an inviter", email: "someone@outlook.com", inviter: ivCustomerAdmin, want: &apierror.ForbiddenError{}},
		{name: "domain on no list", email: ivUnrelatedInvite, want: &apierror.ForbiddenError{}},
		{name: "not an address", email: "nobody", want: &apierror.ValidationError{}},
		{name: "duplicate Salesforce contacts", email: "dup@acme.com", setup: func(s *fakeInvitationSales, _ *invitationValidator) {
			s.contacts["dup@acme.com"] = []salesentity.Contact{ivContact("dup@acme.com", ivCustomerSfID), ivContact("dup@acme.com", ivCustomerSfID)}
		}, want: &apierror.ConflictError{}},
		{name: "project account has no domain list", email: "new@acme.com", setup: func(s *fakeInvitationSales, _ *invitationValidator) {
			s.customers[ivCustomerSfID] = salesentity.Customer{ID: ivCustomerSfID}
		}, want: &apierror.ValidationError{}},

		// Checks on who invites: Customer Portal only.
		{name: "customer admin invites own domain", email: "new@acme.com", inviter: ivCustomerAdmin, want: nil},
		{name: "customer admin invites a partner's domain", email: "new@partner.io", inviter: ivCustomerAdmin, want: nil},
		{name: "partner admin invites own domain", email: "new@partner.io", inviter: ivPartnerAdmin, want: nil},
		// As before: a partner's allowed domains are its own plus the
		// project's partners', not the customer's own list.
		{name: "partner admin invites the customer's domain", email: "new@acme.com", inviter: ivPartnerAdmin, want: &apierror.ForbiddenError{}},
		{name: "inviter from an unrelated account", email: "new@acme.com", inviter: "x@unrelated.org", want: &apierror.ForbiddenError{}},
		{name: "inviter with no Salesforce contact", email: "new@acme.com", inviter: "ghost@acme.com", want: &apierror.ForbiddenError{}},
		{name: "existing contact from another organisation with a live membership", email: "bob@partner.io", inviter: ivCustomerAdmin,
			setup: func(s *fakeInvitationSales, _ *invitationValidator) {
				s.contacts["bob@partner.io"] = []salesentity.Contact{ivContact("bob@partner.io", ivPartnerSfID,
					salesentity.ContactMembership{SubscriptionID: sampleStr(ivProjectSfID), State: sampleStr("INVITED")})}
			}, want: &apierror.ValidationError{}},
		{name: "existing contact from another organisation, membership deactivated", email: "bob@partner.io", inviter: ivCustomerAdmin,
			setup: func(s *fakeInvitationSales, _ *invitationValidator) {
				s.contacts["bob@partner.io"] = []salesentity.Contact{ivContact("bob@partner.io", ivPartnerSfID,
					salesentity.ContactMembership{SubscriptionID: sampleStr(ivProjectSfID), State: sampleStr("DEACTIVATED")})}
			}, want: nil},
		{name: "inviter's account has no domain list", email: "new@partner.io", inviter: ivPartnerAdmin,
			setup: func(s *fakeInvitationSales, _ *invitationValidator) {
				s.customers[ivPartnerSfID] = salesentity.Customer{ID: ivPartnerSfID, DomainList: sampleStr(" , ")}
			}, want: &apierror.ValidationError{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sales, v := newIV()
			if tt.setup != nil {
				tt.setup(sales, v)
			}
			wantErrKind(t, v.Validate(context.Background(), ivTarget, tt.email, tt.inviter), tt.want)
		})
	}
}

// TestInvitationValidator_LookupFailuresRefuse: when a check cannot be
// answered, the invitation must not go ahead.
func TestInvitationValidator_LookupFailuresRefuse(t *testing.T) {
	sales, v := newIV()
	sales.err = &apierror.ServiceUnavailableError{Msg: "salesentity down"}
	if err := v.Validate(context.Background(), ivTarget, "new@acme.com", ""); err == nil {
		t.Fatal("Salesforce failure: got nil, want an error")
	}

	_, v = newIV()
	v.partners = fakePartnerRepo{err: errors.New("db down")}
	if err := v.Validate(context.Background(), ivTarget, "new@acme.com", ""); err == nil {
		t.Fatal("partner lookup failure: got nil, want an error")
	}
}

type refusingValidator struct{ calls int }

func (r *refusingValidator) Validate(context.Context, domain.MembershipWriteTarget, string, string) error {
	r.calls++
	return &apierror.ForbiddenError{Msg: "domain not allowed"}
}

// TestMembershipWrite_InviteRefusedByValidatorWritesNothing: a refused
// invitation must leave Salesforce and the database exactly as they were.
func TestMembershipWrite_InviteRefusedByValidatorWritesNothing(t *testing.T) {
	h := newInternalWriteHarness(t)
	refuse := &refusingValidator{}
	h.svc.(*projectMembershipWriteService).deps.Invitations = refuse

	_, err := h.svc.Invite(context.Background(), writeProjectID, inviteReq("Portal user"))

	var forbidden *apierror.ForbiddenError
	if !errors.As(err, &forbidden) {
		t.Fatalf("err = %v, want ForbiddenError", err)
	}
	if refuse.calls != 1 {
		t.Errorf("validator calls = %d, want 1", refuse.calls)
	}
	if len(h.se.createdContact) != 0 || len(h.se.createdPC) != 0 || len(h.se.updates) != 0 || len(h.se.contactSearchs) != 0 {
		t.Errorf("Salesforce touched: contacts=%v memberships=%v updates=%v searches=%v",
			h.se.createdContact, h.se.createdPC, h.se.updates, h.se.contactSearchs)
	}
	if len(h.pub.published) != 0 {
		t.Errorf("published %d invitation events, want 0", len(h.pub.published))
	}
}
