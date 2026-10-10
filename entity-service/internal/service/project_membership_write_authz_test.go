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
)

const authzCustomerEmail = "admin@acme.com"

type fakeAccountAdmins struct {
	admin bool
	err   error
	asked []string
}

func (f *fakeAccountAdmins) HoldsAccountAdminRole(_ context.Context, email string) (bool, error) {
	f.asked = append(f.asked, email)
	return f.admin, f.err
}

type inviterCapturingValidator struct{ inviters []string }

func (r *inviterCapturingValidator) Validate(_ context.Context, _ domain.MembershipWriteTarget, _, inviter string) error {
	r.inviters = append(r.inviters, inviter)
	return nil
}

// customerHarness is a write harness whose caller is a customer with the given projects in scope.
func customerHarness(t *testing.T, projectIDs []string, admins *fakeAccountAdmins) (*writeHarness, *inviterCapturingValidator) {
	t.Helper()
	h := newWriteHarness(t, stubAccess{scope: AccessScope{ProjectIDs: projectIDs, ViewerEmail: authzCustomerEmail}})
	iv := &inviterCapturingValidator{}
	deps := &h.svc.(*projectMembershipWriteService).deps
	deps.Invitations = iv
	if admins != nil {
		deps.Admins = admins
	}
	return h, iv
}

// allMembershipOps runs every gated operation and returns each one's error.
func allMembershipOps(h *writeHarness) map[string]error {
	ctx := context.Background()
	_, inviteErr := h.svc.Invite(ctx, writeProjectID, inviteReq("Portal user"))
	_, validateErr := h.svc.ValidateInvitation(ctx, writeProjectID, domain.ValidateProjectMembershipRequest{Email: "new@acme.com"})
	_, rolesErr := h.svc.UpdateRoles(ctx, writeProjectID, writeEmail, domain.UpdateProjectMembershipRolesRequest{Roles: []string{"Admin"}})
	return map[string]error{
		"Invite": inviteErr, "ValidateInvitation": validateErr, "UpdateRoles": rolesErr,
		"Deactivate": h.svc.Deactivate(ctx, writeProjectID, writeEmail), "ResendInvitation": h.svc.ResendInvitation(ctx, writeProjectID, writeEmail),
	}
}

func assertUntouched(t *testing.T, h *writeHarness) {
	t.Helper()
	if h.repo.resolves != 0 || len(h.repo.upserts) != 0 || len(h.pub.published) != 0 {
		t.Error("nothing may be read, written or published for a refused caller")
	}
	if len(h.se.contactSearchs)+len(h.se.createdContact)+len(h.se.createdPC)+len(h.se.updates) != 0 {
		t.Error("Salesforce must not be touched for a refused caller")
	}
}

func TestMembershipWriteAuthz_CustomerOutsideProjectGetsNotFound(t *testing.T) {
	admins := &fakeAccountAdmins{admin: true}
	h, _ := customerHarness(t, []string{"11111111-1111-1111-1111-111111111111"}, admins)
	for name, err := range allMembershipOps(h) {
		var nf *apierror.NotFoundError
		if !errors.As(err, &nf) {
			t.Errorf("%s: err = %v, want NotFoundError", name, err)
		}
	}
	if len(admins.asked) != 0 {
		t.Error("the admin role is not consulted for a project outside scope")
	}
	assertUntouched(t, h)
}

func TestMembershipWriteAuthz_CustomerWithoutAdminRoleIsForbidden(t *testing.T) {
	h, _ := customerHarness(t, []string{writeProjectID}, &fakeAccountAdmins{admin: false})
	for name, err := range allMembershipOps(h) {
		var fe *apierror.ForbiddenError
		if !errors.As(err, &fe) || fe.Msg != msgNotAccountAdmin {
			t.Errorf("%s: err = %v, want ForbiddenError %q", name, err, msgNotAccountAdmin)
		}
	}
	assertUntouched(t, h)
}

func TestMembershipWriteAuthz_NoAdminLookupRefusesCustomers(t *testing.T) {
	h, _ := customerHarness(t, []string{writeProjectID}, nil)
	for name, err := range allMembershipOps(h) {
		var fe *apierror.ForbiddenError
		if !errors.As(err, &fe) {
			t.Errorf("%s: err = %v, want ForbiddenError", name, err)
		}
	}
	assertUntouched(t, h)
}

func TestMembershipWriteAuthz_AdminLookupFailureIsReturned(t *testing.T) {
	boom := errors.New("db down")
	h, _ := customerHarness(t, []string{writeProjectID}, &fakeAccountAdmins{err: boom})
	if _, err := h.svc.Invite(context.Background(), writeProjectID, inviteReq("Portal user")); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the lookup error", err)
	}
	assertUntouched(t, h)
}

func TestMembershipWriteAuthz_CustomerAdminInvitesAsThemselves(t *testing.T) {
	admins := &fakeAccountAdmins{admin: true}
	h, iv := customerHarness(t, []string{writeProjectID}, admins)
	req := inviteReq("Portal user")
	req.InviterEmail = "" // a customer can't skip the inviter checks by leaving this out

	if _, err := h.svc.Invite(context.Background(), writeProjectID, req); err != nil {
		t.Fatalf("Invite: %v", err)
	}
	if len(admins.asked) != 1 || admins.asked[0] != authzCustomerEmail {
		t.Errorf("admin lookups = %v, want the caller's own email", admins.asked)
	}
	if len(iv.inviters) != 1 || iv.inviters[0] != authzCustomerEmail {
		t.Errorf("validator inviter = %v, want %q", iv.inviters, authzCustomerEmail)
	}
	if len(h.repo.upserts) != 1 {
		t.Errorf("upserts = %d, want 1", len(h.repo.upserts))
	}
}

func TestMembershipWriteAuthz_CustomerCannotSpoofInviter(t *testing.T) {
	h, iv := customerHarness(t, []string{writeProjectID}, &fakeAccountAdmins{admin: true})
	_, err := h.svc.ValidateInvitation(context.Background(), writeProjectID,
		domain.ValidateProjectMembershipRequest{Email: "new@acme.com", InviterEmail: "someone-else@other.com"})
	if err != nil {
		t.Fatalf("ValidateInvitation: %v", err)
	}
	if len(iv.inviters) != 1 || iv.inviters[0] != authzCustomerEmail {
		t.Errorf("validator inviter = %v, want the caller's own %q", iv.inviters, authzCustomerEmail)
	}
}

func TestMembershipWriteAuthz_UnrestrictedCallerKeepsBodyInviter(t *testing.T) {
	h := newInternalWriteHarness(t)
	iv := &inviterCapturingValidator{}
	h.svc.(*projectMembershipWriteService).deps.Invitations = iv

	_, err := h.svc.ValidateInvitation(context.Background(), writeProjectID,
		domain.ValidateProjectMembershipRequest{Email: "new@acme.com", InviterEmail: "portal-user@acme.com"})
	if err != nil {
		t.Fatalf("ValidateInvitation: %v", err)
	}
	if len(iv.inviters) != 1 || iv.inviters[0] != "portal-user@acme.com" {
		t.Errorf("validator inviter = %v, want the body's inviter", iv.inviters)
	}
}
