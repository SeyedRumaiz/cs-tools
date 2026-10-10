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
	"time"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/apierror"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
)

// recordingInvitationValidator counts its calls, so a test can assert the
// missing-id guard ran before the invitation checks (which read Salesforce
// against the project's sf_id).
type recordingInvitationValidator struct{ calls int }

func (v *recordingInvitationValidator) Validate(context.Context, domain.MembershipWriteTarget, string, string) error {
	v.calls++
	return nil
}

// assertNoSideEffects fails the test if the write reached Salesforce,
// wrote a row or published an event.
func assertNoSideEffects(t *testing.T, h *writeHarness) {
	t.Helper()
	se := h.se
	if len(se.contactGets)+len(se.contactSearchs)+len(se.pcSearches) != 0 {
		t.Errorf("Salesforce was read: gets=%v searches=%v pcSearches=%v", se.contactGets, se.contactSearchs, se.pcSearches)
	}
	if len(se.createdContact)+len(se.createdPC)+len(se.updates) != 0 {
		t.Errorf("Salesforce was written: contacts=%v memberships=%v updates=%v", se.createdContact, se.createdPC, se.updates)
	}
	if len(h.repo.upserts) != 0 {
		t.Errorf("rows were written: %v", h.repo.upserts)
	}
	if len(h.pub.published) != 0 {
		t.Errorf("an event was published: %v", h.pub.published)
	}
}

// assertNotLinkedConflict checks the error is the caller-safe 409 for the
// operation, and that its message names nothing internal.
func assertNotLinkedConflict(t *testing.T, err error, operation string) {
	t.Helper()
	var ce *apierror.ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v (%T), want ConflictError", err, err)
	}
	if ce.Msg != membershipNotLinkedMessages[operation] {
		t.Errorf("message = %q, want %q", ce.Msg, membershipNotLinkedMessages[operation])
	}
	lower := strings.ToLower(ce.Msg)
	for _, leak := range []string{"sf_id", "sfid", "salesforce", "project_contact", "account"} {
		if strings.Contains(lower, leak) {
			t.Errorf("message %q leaks %q", ce.Msg, leak)
		}
	}
}

func linkedRow(state string) *domain.ProjectMembershipRow {
	return &domain.ProjectMembershipRow{
		ProjectContactID: "pc-1", MembershipSfID: writeMembershipID, ContactSfID: writeContactSfID,
		Email: writeEmail, State: state, ProjectGroups: []string{projectGroupGeneralAccess},
	}
}

// missingLinkCases are the ways a row a write depends on can lack its
// Salesforce id. needsExisting marks the ones that only apply to a write on a
// membership that is already there.
var missingLinkCases = []struct {
	name          string
	needsExisting bool
	apply         func(h *writeHarness)
}{
	{"project sf_id empty", false, func(h *writeHarness) { h.repo.target.ProjectSfID = "" }},
	{"project sf_id blank", false, func(h *writeHarness) { h.repo.target.ProjectSfID = "   " }},
	{"account sf_id empty", false, func(h *writeHarness) { h.repo.target.AccountSfID = "" }},
	{"project has no account", false, func(h *writeHarness) { h.repo.target.AccountID, h.repo.target.AccountSfID = "", "" }},
	{"membership sf_id empty", true, func(h *writeHarness) { h.repo.existing.MembershipSfID = "" }},
	{"contact sf_id empty", true, func(h *writeHarness) { h.repo.existing.ContactSfID = "" }},
}

func TestMembershipWrite_InviteRefusesAMissingSalesforceIdBeforeAnyCall(t *testing.T) {
	for _, tc := range missingLinkCases {
		if tc.needsExisting {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			h := newInternalWriteHarness(t)
			v := &recordingInvitationValidator{}
			h.svc = NewProjectMembershipWriteService(MembershipWriteDeps{
				Memberships: h.repo, Steps: h.steps, SalesEntity: h.se, Publisher: h.pub,
				Failures: h.failures, Access: alwaysUnrestrictedAccess{}, Invitations: v,
			})
			tc.apply(h)

			_, err := h.svc.Invite(context.Background(), writeProjectID, inviteReq("Portal user"))
			assertNotLinkedConflict(t, err, membershipOpInvite)
			assertNoSideEffects(t, h)
			if v.calls != 0 {
				t.Error("the invitation checks must not run against a project with a missing Salesforce id")
			}
		})
	}
}

// TestMembershipWrite_ReInviteRefusesAMissingSalesforceId covers the
// re-invitation of a DEACTIVATED membership, the one Invite path that works
// on a row already there.
func TestMembershipWrite_ReInviteRefusesAMissingSalesforceId(t *testing.T) {
	for _, tc := range missingLinkCases {
		t.Run(tc.name, func(t *testing.T) {
			h := newInternalWriteHarness(t)
			h.repo.existing = linkedRow(domain.MembershipStateDeactivated)
			h.se.contact = existingSalesforceContact()
			tc.apply(h)

			_, err := h.svc.Invite(context.Background(), writeProjectID, inviteReq("Portal user"))
			assertNotLinkedConflict(t, err, membershipOpInvite)
			assertNoSideEffects(t, h)
		})
	}
}

func TestMembershipWrite_UpdateRolesRefusesAMissingSalesforceId(t *testing.T) {
	for _, tc := range missingLinkCases {
		t.Run(tc.name, func(t *testing.T) {
			h := newInternalWriteHarness(t)
			h.repo.existing = linkedRow(domain.MembershipStateRegistered)
			h.se.contact = existingSalesforceContact()
			h.se.membership = nil // before the guard, this made a missing id create a new membership
			tc.apply(h)

			_, err := h.svc.UpdateRoles(context.Background(), writeProjectID, writeEmail,
				domain.UpdateProjectMembershipRolesRequest{Roles: []string{"Admin"}})
			assertNotLinkedConflict(t, err, membershipOpUpdateRoles)
			assertNoSideEffects(t, h)
		})
	}
}

func TestMembershipWrite_DeactivateRefusesAMissingSalesforceId(t *testing.T) {
	for _, tc := range missingLinkCases {
		t.Run(tc.name, func(t *testing.T) {
			h := newInternalWriteHarness(t)
			h.repo.existing = linkedRow(domain.MembershipStateRegistered)
			h.se.contact = existingSalesforceContact()
			tc.apply(h)

			err := h.svc.Deactivate(context.Background(), writeProjectID, writeEmail)
			assertNotLinkedConflict(t, err, membershipOpDeactivate)
			assertNoSideEffects(t, h)
		})
	}
}

func TestMembershipWrite_ResendRefusesAMissingSalesforceId(t *testing.T) {
	cases := []struct {
		name  string
		apply func(row *domain.ProjectMembershipRow)
	}{
		{"membership sf_id empty", func(row *domain.ProjectMembershipRow) { row.MembershipSfID = "" }},
		{"contact sf_id empty", func(row *domain.ProjectMembershipRow) { row.ContactSfID = " " }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newInternalWriteHarness(t)
			row := invitedMembershipRow()
			tc.apply(row)
			h.repo.existing = row
			h.steps.existing = []domain.OnboardingStep{{
				MembershipSfID: writeMembershipID, Step: domain.OnboardingStepEmail,
				Status: domain.OnboardingStepSucceeded, UpdatedOn: time.Now().Add(-30 * time.Minute),
			}}

			err := h.svc.ResendInvitation(context.Background(), writeProjectID, writeEmail)
			assertNotLinkedConflict(t, err, membershipOpResend)
			if len(h.pub.published) != 0 {
				t.Error("nothing may be published for a membership with a missing Salesforce id")
			}
		})
	}
}

// TestMembershipWrite_FullyLinkedRowsStillWrite guards the guard: with every
// id present, each Salesforce-writing path still goes through.
func TestMembershipWrite_FullyLinkedRowsStillWrite(t *testing.T) {
	ctx := context.Background()

	h := newInternalWriteHarness(t)
	if _, err := h.svc.Invite(ctx, writeProjectID, inviteReq("Portal user")); err != nil {
		t.Errorf("Invite: %v", err)
	}

	h = newInternalWriteHarness(t)
	h.repo.existing = linkedRow(domain.MembershipStateRegistered)
	h.se.contact = existingSalesforceContact()
	if _, err := h.svc.UpdateRoles(ctx, writeProjectID, writeEmail, domain.UpdateProjectMembershipRolesRequest{Roles: []string{"Admin"}}); err != nil {
		t.Errorf("UpdateRoles: %v", err)
	}

	h = newInternalWriteHarness(t)
	h.repo.existing = linkedRow(domain.MembershipStateRegistered)
	h.se.contact = existingSalesforceContact()
	if err := h.svc.Deactivate(ctx, writeProjectID, writeEmail); err != nil {
		t.Errorf("Deactivate: %v", err)
	}
}
