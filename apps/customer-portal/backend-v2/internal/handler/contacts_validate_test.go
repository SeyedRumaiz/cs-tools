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

package handler

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/wso2-open-operations/cs-tools/apps/customer-portal/backend-v2/internal/apierror"
	"github.com/wso2-open-operations/cs-tools/apps/customer-portal/backend-v2/internal/dto"
	"github.com/wso2-open-operations/cs-tools/apps/customer-portal/backend-v2/internal/entity"
	"github.com/wso2-open-operations/cs-tools/apps/customer-portal/backend-v2/internal/usermanagement"
)

func validatePath() string { return contactsPath() + "/validate" }

const validateBody = `{"contactEmail":" shayan+e2e1@wso2.com "}`

func decodeValidation(t *testing.T, body []byte) dto.ContactValidationResponse {
	t.Helper()
	var got dto.ContactValidationResponse
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v; body %s", err, body)
	}
	return got
}

func decodeErrorMessage(t *testing.T, body []byte) string {
	t.Helper()
	var got struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v; body %s", err, body)
	}
	return got.Message
}

// ---- flag on: entity-service ---------------------------------------------

func TestValidateProjectContact_PortalContactsOnNewAddress(t *testing.T) {
	mux, f := newContactMux(true, true)
	f.memberships.validation = entity.ProjectMembershipValidation{Valid: true}

	rec := serveContact(mux, http.MethodPost, validatePath(), validateBody)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body)
	}
	got := decodeValidation(t, rec.Body.Bytes())
	if !got.IsContactValid || got.Message != dto.ContactValidationMsgNewContact || got.ContactDetails != nil {
		t.Errorf("response = %+v", got)
	}
	if !reflect.DeepEqual(f.memberships.calls, []string{"validate"}) || f.memberships.projectID != testProjectUUID {
		t.Fatalf("entity calls = %v on %q, want [validate] on the project UUID", f.memberships.calls, f.memberships.projectID)
	}
	// The address is trimmed, and the inviter is the signed-in user from
	// the verified token, which is what turns on the checks on who invites.
	want := entity.ValidateProjectMembershipRequest{Email: testInvitee, InviterEmail: testCaller}
	if f.memberships.validateReq != want {
		t.Errorf("validate body = %+v, want %+v", f.memberships.validateReq, want)
	}
	if len(f.legacy.calls) != 0 || f.resolver.calls != 0 {
		t.Errorf("legacy calls = %v, GetProject calls = %d; want none", f.legacy.calls, f.resolver.calls)
	}
}

func TestValidateProjectContact_PortalContactsOnExistingContact(t *testing.T) {
	mux, f := newContactMux(true, true)
	account := "001xx"
	f.memberships.validation = entity.ProjectMembershipValidation{
		Valid:                   true,
		ExistingMembershipState: "DEACTIVATED",
		ExistingContact: &entity.ValidatedInvitee{
			ContactSfID: "003xx", Email: testInvitee, FirstName: "Shayan", LastName: "Malinda", AccountSfID: &account,
		},
	}

	rec := serveContact(mux, http.MethodPost, validatePath(), validateBody)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body)
	}
	got := decodeValidation(t, rec.Body.Bytes())
	if !got.IsContactValid || got.Message != dto.ContactValidationMsgExistingContact || got.ContactDetails == nil {
		t.Fatalf("response = %+v", got)
	}
	d := got.ContactDetails
	if d.ID != "003xx" || d.Email != testInvitee || d.FirstName == nil || *d.FirstName != "Shayan" || d.LastName != "Malinda" {
		t.Errorf("contactDetails = %+v", d)
	}
	if d.Account == nil || d.Account.ID == nil || *d.Account.ID != account {
		t.Errorf("contactDetails.account = %+v", d.Account)
	}
}

// TestValidateProjectContact_PortalContactsOnRefusals: each refusal reaches
// the webapp as the status and message it has always shown.
func TestValidateProjectContact_PortalContactsOnRefusals(t *testing.T) {
	const domainMsg = "We're sorry, but the domain associated with your email address is not currently supported by this project."
	cases := []struct {
		name       string
		verdict    entity.ProjectMembershipValidation
		wantStatus int
		wantMsg    string
	}{
		{"already on the project", entity.ProjectMembershipValidation{Reason: entity.MembershipValidationConflict,
			Message: "this address is already a contact on the project; change their roles instead"},
			http.StatusConflict, dto.ContactValidationMsgConflict},
		{"domain not allowed", entity.ProjectMembershipValidation{Reason: entity.MembershipValidationForbidden, Message: domainMsg},
			http.StatusForbidden, domainMsg},
		{"public domain", entity.ProjectMembershipValidation{Reason: entity.MembershipValidationForbidden,
			Message: "We're sorry, but we cannot accept public email domains like gmail.com."},
			http.StatusForbidden, "We're sorry, but we cannot accept public email domains like gmail.com."},
		{"no domain list", entity.ProjectMembershipValidation{Reason: entity.MembershipValidationInvalid,
			Message: "We're sorry, but the supported email domains list for your account has not been defined."},
			http.StatusBadRequest, "We're sorry, but the supported email domains list for your account has not been defined."},
		{"forbidden with no message", entity.ProjectMembershipValidation{Reason: entity.MembershipValidationForbidden},
			http.StatusForbidden, ErrMsgForbidden},
		{"a reason this backend does not know", entity.ProjectMembershipValidation{Reason: "SOMETHING_NEW"},
			http.StatusBadRequest, "Failed to validate project contact."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mux, f := newContactMux(true, true)
			f.memberships.validation = tc.verdict

			rec := serveContact(mux, http.MethodPost, validatePath(), validateBody)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body %s", rec.Code, tc.wantStatus, rec.Body)
			}
			if got := decodeErrorMessage(t, rec.Body.Bytes()); got != tc.wantMsg {
				t.Errorf("message = %q, want %q", got, tc.wantMsg)
			}
		})
	}
}

// TestValidateProjectContact_PortalContactsOnHidesUpstreamDetail: when the
// check itself fails, the webapp gets a generic message, never
// entity-service's own wording about its callers or its dependencies.
func TestValidateProjectContact_PortalContactsOnHidesUpstreamDetail(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantMsg    string
		leak       string
	}{
		{"Salesforce unavailable", apierror.NewUpstreamError(http.StatusServiceUnavailable, []byte(`{"message":"salesentity: token endpoint refused"}`)),
			http.StatusServiceUnavailable, "Failed to validate project contact.", "salesentity"},
		{"portal not an internal caller", apierror.NewUpstreamError(http.StatusForbidden, []byte(`{"message":"membership writes are only available to internal services"}`)),
			http.StatusForbidden, ErrMsgForbidden, "internal services"},
		{"route not deployed", apierror.NewUpstreamError(http.StatusNotFound, nil),
			http.StatusNotFound, ErrMsgNotFound, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mux, f := newContactMux(true, true)
			f.memberships.validateErr = tc.err

			rec := serveContact(mux, http.MethodPost, validatePath(), validateBody)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body %s", rec.Code, tc.wantStatus, rec.Body)
			}
			if got := decodeErrorMessage(t, rec.Body.Bytes()); got != tc.wantMsg {
				t.Errorf("message = %q, want %q", got, tc.wantMsg)
			}
			if tc.leak != "" && strings.Contains(rec.Body.String(), tc.leak) {
				t.Errorf("body %s leaks upstream detail %q", rec.Body, tc.leak)
			}
			if len(f.legacy.calls) != 0 {
				t.Errorf("legacy calls = %v, want none: a failed check must not fall back", f.legacy.calls)
			}
		})
	}
}

// TestValidateProjectContact_PortalContactsOnRequiresProjectAdmin keeps the
// pre-cutover service's admin check: somebody who could not invite must not
// be able to probe addresses either.
func TestValidateProjectContact_PortalContactsOnRequiresProjectAdmin(t *testing.T) {
	mux, f := newContactMux(true, true)
	f.memberships.roles = []string{"external", "customer"}

	rec := serveContact(mux, http.MethodPost, validatePath(), validateBody)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body %s", rec.Code, rec.Body)
	}
	if len(f.memberships.calls) != 0 || len(f.legacy.calls) != 0 {
		t.Errorf("entity calls = %v, legacy calls = %v; want none", f.memberships.calls, f.legacy.calls)
	}
}

func TestValidateProjectContact_PortalContactsOnRejectsMalformedEmail(t *testing.T) {
	mux, f := newContactMux(true, true)

	rec := serveContact(mux, http.MethodPost, validatePath(), `{"contactEmail":"not-an-address"}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body %s", rec.Code, rec.Body)
	}
	if got := decodeErrorMessage(t, rec.Body.Bytes()); got != dto.ContactValidationMsgInvalidEmail {
		t.Errorf("message = %q", got)
	}
	if len(f.memberships.calls) != 0 || f.memberships.listed != 0 {
		t.Errorf("entity calls = %v, list reads = %d; want none", f.memberships.calls, f.memberships.listed)
	}
}

// ---- flag off: pre-cutover onboarding service ----------------------------

func TestValidateProjectContact_PortalContactsOffUsesLegacyService(t *testing.T) {
	mux, f := newContactMux(false, true)

	rec := serveContact(mux, http.MethodPost, validatePath(), `{"contactEmail":"shayan+e2e1@wso2.com"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body)
	}
	got := decodeValidation(t, rec.Body.Bytes())
	if !got.IsContactValid || got.Message != dto.ContactValidationMsgNewContact || got.ContactDetails != nil {
		t.Errorf("response = %+v", got)
	}
	want := usermanagement.ValidationPayload{ProjectID: testProjectSfID, ContactEmail: testInvitee, AdminEmail: testCaller}
	if f.legacy.validateReq != want {
		t.Errorf("legacy payload = %+v, want %+v", f.legacy.validateReq, want)
	}
	if len(f.memberships.calls) != 0 || f.memberships.listed != 0 {
		t.Errorf("entity calls = %v, list reads = %d with the flag off; want none", f.memberships.calls, f.memberships.listed)
	}
}

func TestValidateProjectContact_PortalContactsOffExistingContact(t *testing.T) {
	mux, f := newContactMux(false, true)
	first := "Shayan"
	f.legacy.validation = legacyValidation{contact: &usermanagement.Contact{ID: "003xx", Email: testInvitee, FirstName: &first, LastName: "Malinda"}}

	rec := serveContact(mux, http.MethodPost, validatePath(), validateBody)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body)
	}
	got := decodeValidation(t, rec.Body.Bytes())
	if !got.IsContactValid || got.Message != dto.ContactValidationMsgExistingContact || got.ContactDetails == nil || got.ContactDetails.ID != "003xx" {
		t.Errorf("response = %+v", got)
	}
}

func TestValidateProjectContact_PortalContactsOffConflictAndRefusal(t *testing.T) {
	const legacyMsg = "We're sorry, but we cannot accept public email domains like gmail.com."
	cases := []struct {
		name       string
		answer     legacyValidation
		wantStatus int
		wantMsg    string
	}{
		{"conflict", legacyValidation{conflict: true, err: apierror.NewUpstreamError(http.StatusConflict, []byte(`{"message":"already there"}`))},
			http.StatusConflict, dto.ContactValidationMsgConflict},
		{"refusal", legacyValidation{err: apierror.NewUpstreamError(http.StatusForbidden, []byte(`{"message":"`+legacyMsg+`"}`))},
			http.StatusForbidden, legacyMsg},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mux, f := newContactMux(false, true)
			f.legacy.validation = tc.answer

			rec := serveContact(mux, http.MethodPost, validatePath(), validateBody)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body %s", rec.Code, tc.wantStatus, rec.Body)
			}
			if got := decodeErrorMessage(t, rec.Body.Bytes()); got != tc.wantMsg {
				t.Errorf("message = %q, want %q", got, tc.wantMsg)
			}
		})
	}
}

// TestValidateProjectContact_FlagWithoutClientStaysOnLegacy: the flag on with
// no entity client behind it keeps the pre-cutover path.
func TestValidateProjectContact_FlagWithoutClientStaysOnLegacy(t *testing.T) {
	mux, f := newContactMux(true, false)

	rec := serveContact(mux, http.MethodPost, validatePath(), validateBody)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body)
	}
	if !reflect.DeepEqual(f.legacy.calls, []string{"validate"}) {
		t.Errorf("legacy calls = %v, want [validate]", f.legacy.calls)
	}
}
