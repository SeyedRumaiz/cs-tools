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

// newValidateHarness is the write harness with the REAL InvitationValidator
// wired in over newIV's world (Acme owns the project, domains acme.com and
// acme.io; Partner.io partners it), so the dry run is exercised against the
// same rules the invitation uses rather than a stand-in.
func newValidateHarness(t *testing.T) (*writeHarness, *fakeInvitationSales) {
	t.Helper()
	h := newInternalWriteHarness(t)
	h.repo.target = domain.MembershipWriteTarget{
		ProjectID: writeProjectID, ProjectKey: "ACMEPROD", ProjectSfID: ivProjectSfID,
		AccountID: ivAccountID, AccountSfID: ivCustomerSfID,
	}
	sales, v := newIV()
	h.svc.(*projectMembershipWriteService).deps.Invitations = v
	return h, sales
}

// assertNothingWritten pins the dry run's one promise: no Salesforce write,
// no row, no transaction, no event.
func assertNothingWritten(t *testing.T, h *writeHarness) {
	t.Helper()
	if len(h.se.createdContact)+len(h.se.createdPC)+len(h.se.updates) != 0 {
		t.Errorf("Salesforce written: contacts=%v memberships=%v updates=%v", h.se.createdContact, h.se.createdPC, h.se.updates)
	}
	if len(h.repo.upserts) != 0 || len(h.repo.steps) != 0 {
		t.Errorf("rows written: upserts=%v steps=%v", h.repo.upserts, h.repo.steps)
	}
	if len(h.pub.published) != 0 {
		t.Errorf("published %d events, want 0", len(h.pub.published))
	}
}

func TestValidateInvitation_RejectsBadInput(t *testing.T) {
	cases := []struct {
		name      string
		projectID string
		email     string
	}{
		{"project id not a uuid", "p-1", "new@acme.com"},
		{"no email", writeProjectID, ""},
		{"malformed email", writeProjectID, "not-an-address"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := newValidateHarness(t)
			_, err := h.svc.ValidateInvitation(context.Background(), tc.projectID, domain.ValidateProjectMembershipRequest{Email: tc.email})
			var ve *apierror.ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("err = %v, want ValidationError", err)
			}
			if h.repo.resolves != 0 {
				t.Error("input is rejected before the project is read")
			}
		})
	}
}

func TestValidateInvitation_NewAddressOnAnAllowedDomainIsValid(t *testing.T) {
	h, _ := newValidateHarness(t)

	got, err := h.svc.ValidateInvitation(context.Background(), writeProjectID,
		domain.ValidateProjectMembershipRequest{Email: " New@Acme.com ", InviterEmail: ivCustomerAdmin})
	if err != nil {
		t.Fatalf("ValidateInvitation: %v", err)
	}
	if !got.Valid || got.Reason != "" || got.Message != "" {
		t.Errorf("got %+v, want a plain valid answer", got)
	}
	if got.ExistingContact != nil || got.ExistingMembershipState != "" {
		t.Errorf("a brand-new address has no existing contact or membership: %+v", got)
	}
	if len(h.se.contactSearchs) != 1 || h.se.contactSearchs[0] != "new@acme.com" {
		t.Errorf("contact searches = %v, want the normalized address once", h.se.contactSearchs)
	}
	assertNothingWritten(t, h)
}

func TestValidateInvitation_ReturnsTheContactItWouldAdopt(t *testing.T) {
	h, _ := newValidateHarness(t)
	h.se.contact = existingSalesforceContact()

	got, err := h.svc.ValidateInvitation(context.Background(), writeProjectID, domain.ValidateProjectMembershipRequest{Email: writeEmail})
	if err != nil {
		t.Fatalf("ValidateInvitation: %v", err)
	}
	if !got.Valid || got.ExistingContact == nil {
		t.Fatalf("got %+v, want valid with the existing contact", got)
	}
	c := got.ExistingContact
	if c.ContactSfID != writeContactSfID || c.Email != writeEmail || c.FirstName != "Jane" || c.LastName != "Doe" {
		t.Errorf("existing contact = %+v", c)
	}
	if c.AccountSfID == nil || *c.AccountSfID != writeAccountSfID {
		t.Errorf("account = %v, want %s", c.AccountSfID, writeAccountSfID)
	}
	assertNothingWritten(t, h)
}

// TestValidateInvitation_ActiveMembershipIsAConflict mirrors Invite's own
// 409, and is decided before the Salesforce checks, as it is there.
func TestValidateInvitation_ActiveMembershipIsAConflict(t *testing.T) {
	for _, state := range []string{domain.MembershipStateInvited, domain.MembershipStateRegistered, domain.MembershipStateReInvited} {
		t.Run(state, func(t *testing.T) {
			h, sales := newValidateHarness(t)
			h.repo.existing = &domain.ProjectMembershipRow{ProjectContactID: "pc-1", Email: "new@acme.com", State: state}
			sales.err = errors.New("Salesforce must not be asked")

			got, err := h.svc.ValidateInvitation(context.Background(), writeProjectID, domain.ValidateProjectMembershipRequest{Email: "new@acme.com"})
			if err != nil {
				t.Fatalf("ValidateInvitation: %v", err)
			}
			if got.Valid || got.Reason != domain.MembershipValidationConflict || got.Message != msgAlreadyProjectContact {
				t.Errorf("got %+v, want a CONFLICT answer", got)
			}
			assertNothingWritten(t, h)
		})
	}
}

// TestValidateInvitation_DeactivatedMembershipCanBeBroughtBack: the
// invitation would re-invite, so the answer is valid, says so, and names the
// contact the membership is already linked to.
func TestValidateInvitation_DeactivatedMembershipCanBeBroughtBack(t *testing.T) {
	h, _ := newValidateHarness(t)
	h.repo.existing = &domain.ProjectMembershipRow{
		ProjectContactID: "pc-1", MembershipSfID: writeMembershipID, ContactSfID: writeContactSfID, Email: writeEmail,
		State: domain.MembershipStateDeactivated,
	}
	h.se.contact = existingSalesforceContact()

	got, err := h.svc.ValidateInvitation(context.Background(), writeProjectID, domain.ValidateProjectMembershipRequest{Email: writeEmail})
	if err != nil {
		t.Fatalf("ValidateInvitation: %v", err)
	}
	if !got.Valid || got.ExistingMembershipState != domain.MembershipStateDeactivated || got.ExistingContact == nil {
		t.Errorf("got %+v, want valid, DEACTIVATED, with the contact", got)
	}
	if len(h.se.contactGets) != 1 || h.se.contactGets[0] != writeContactSfID || len(h.se.contactSearchs) != 0 {
		t.Errorf("gets=%v searches=%v, want the linked contact read by id", h.se.contactGets, h.se.contactSearchs)
	}
	assertNothingWritten(t, h)
}

// TestValidateInvitation_RefusalsBecomeAnswers runs the real validator's
// refusals through the dry run: each becomes a verdict carrying the
// validator's own message, never an error status.
func TestValidateInvitation_RefusalsBecomeAnswers(t *testing.T) {
	tests := []struct {
		name       string
		email      string
		inviter    string
		setup      func(*fakeInvitationSales)
		wantReason string
		wantMsg    string
	}{
		{name: "public domain", email: "someone@gmail.com", wantReason: domain.MembershipValidationForbidden,
			wantMsg: "We're sorry, but we cannot accept public email domains like gmail.com."},
		{name: "domain on no list", email: ivUnrelatedInvite, inviter: ivCustomerAdmin,
			wantReason: domain.MembershipValidationForbidden, wantMsg: msgDomainNotAllowed},
		{name: "inviter from an unrelated account", email: "new@acme.com", inviter: "x@unrelated.org",
			wantReason: domain.MembershipValidationForbidden, wantMsg: msgNoPermission},
		{name: "no domain list", email: "new@acme.com", inviter: ivCustomerAdmin,
			setup: func(s *fakeInvitationSales) {
				s.customers[ivCustomerSfID] = salesentity.Customer{ID: ivCustomerSfID}
			}, wantReason: domain.MembershipValidationInvalid, wantMsg: msgNoDomainList},
		{name: "duplicate Salesforce contacts", email: "dup@acme.com",
			setup: func(s *fakeInvitationSales) {
				s.contacts["dup@acme.com"] = []salesentity.Contact{ivContact("dup@acme.com", ivCustomerSfID), ivContact("dup@acme.com", ivCustomerSfID)}
			}, wantReason: domain.MembershipValidationConflict, wantMsg: msgDuplicateContact},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, sales := newValidateHarness(t)
			if tt.setup != nil {
				tt.setup(sales)
			}
			got, err := h.svc.ValidateInvitation(context.Background(), writeProjectID,
				domain.ValidateProjectMembershipRequest{Email: tt.email, InviterEmail: tt.inviter})
			if err != nil {
				t.Fatalf("ValidateInvitation: %v", err)
			}
			if got.Valid || got.Reason != tt.wantReason || got.Message != tt.wantMsg {
				t.Errorf("got %+v, want reason %s message %q", got, tt.wantReason, tt.wantMsg)
			}
			if got.ExistingContact != nil {
				t.Error("a refused invitation carries no contact")
			}
			assertNothingWritten(t, h)
		})
	}
}

// TestValidateInvitation_CheckFailuresStayErrors: when a check cannot be
// answered, the caller must hear that the check failed, not that the
// invitation is fine or refused.
func TestValidateInvitation_CheckFailuresStayErrors(t *testing.T) {
	h, sales := newValidateHarness(t)
	sales.err = &apierror.ServiceUnavailableError{Msg: "salesentity down"}

	_, err := h.svc.ValidateInvitation(context.Background(), writeProjectID, domain.ValidateProjectMembershipRequest{Email: "new@acme.com"})
	var su *apierror.ServiceUnavailableError
	if !errors.As(err, &su) {
		t.Fatalf("err = %v, want ServiceUnavailableError", err)
	}

	h, _ = newValidateHarness(t)
	h.repo.targetErr = &apierror.NotFoundError{Msg: "project not found"}
	_, err = h.svc.ValidateInvitation(context.Background(), writeProjectID, domain.ValidateProjectMembershipRequest{Email: "new@acme.com"})
	var nf *apierror.NotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("err = %v, want NotFoundError", err)
	}
}

// TestValidateInvitation_MissingSalesforceIDIsRefused: a project with no
// Salesforce id is refused as INVALID (never CONFLICT, which callers show as
// "already a contact") with Invite's generic message, before any Salesforce read.
func TestValidateInvitation_MissingSalesforceIDIsRefused(t *testing.T) {
	h, sales := newValidateHarness(t)
	h.repo.target.ProjectSfID = ""
	sales.err = errors.New("the invitation validator must not run")

	got, err := h.svc.ValidateInvitation(context.Background(), writeProjectID, domain.ValidateProjectMembershipRequest{Email: writeEmail})
	if err != nil {
		t.Fatalf("ValidateInvitation: %v", err)
	}
	if got.Valid || got.Reason != domain.MembershipValidationInvalid || got.Message != membershipNotLinkedMessages[membershipOpInvite] {
		t.Errorf("got %+v, want an INVALID refusal with the invite support message", got)
	}
	if len(h.se.contactGets)+len(h.se.contactSearchs) != 0 {
		t.Errorf("Salesforce read before the guard: gets=%v searches=%v", h.se.contactGets, h.se.contactSearchs)
	}
	assertNothingWritten(t, h)
}
