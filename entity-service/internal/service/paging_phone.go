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
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/apierror"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/auth"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/events"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/repository"
)

// Paging-only phone numbers (migration 0216). Paging calls the number on a
// person's Asgardeo profile; where that has none, a lead or rota admin can
// store one here. It is never written to Asgardeo. A "Test call" asks
// csm-notification-service to ring it (paging.test_call_requested), which
// reports the outcome back.
//
// Who may set a person's number:
//
//	a lead (lead or americas_team_lead) of a team the person is on;
//	the rota admin of a family one of the person's teams is in
//	  (cre_rota_admin, sre_rota_admin, sme_rota_admin);
//	the CRE head or CS head;
//	a portal admin.
//
// Reading the stored numbers (GET /team-schedule/paging-contacts) is for
// the service that pages, and internal staff.

const (
	roleNameSRERotaAdmin = "sre_rota_admin"
	roleNameSMERotaAdmin = "sme_rota_admin"

	// pagingTestCooldown is how long after one test call another may be asked.
	pagingTestCooldown = 2 * time.Minute
	// maxPagingContactEmails caps one GET.
	maxPagingContactEmails = 200

	pagingTestPending = "pending"
)

// e164 is the number shape paging_contact's CHECK enforces, and the shape a
// profile number must have to be called.
var e164 = regexp.MustCompile(repository.CallablePhonePattern)

// validPagingTestResult are the outcomes the calling service may report.
var validPagingTestResult = map[string]bool{"completed": true, "no-answer": true, "busy": true, "failed": true}

// canEditPhone reports whether the caller may set the paging number of a
// person on teams.
func (a pagingAuthority) canEditPhone(teams []repository.PagingTeamRef) bool {
	if a.portalAdmin || a.head {
		return true
	}
	for _, t := range teams {
		if a.leadOf[strings.ToLower(t.TeamKey)] {
			return true
		}
		switch t.Family {
		case familyCRE:
			if a.creRotaAdmin {
				return true
			}
		case familySRE:
			if a.sreRotaAdmin {
				return true
			}
		case familySME:
			if a.smeRotaAdmin {
				return true
			}
		}
	}
	return false
}

// twoDigitCallingCodes are the E.164 country codes of two digits; 1 and 7
// are the one-digit ones and every other code has three.
var twoDigitCallingCodes = map[string]bool{
	"20": true, "27": true, "30": true, "31": true, "32": true, "33": true, "34": true, "36": true, "39": true,
	"40": true, "41": true, "43": true, "44": true, "45": true, "46": true, "47": true, "48": true, "49": true,
	"51": true, "52": true, "53": true, "54": true, "55": true, "56": true, "57": true, "58": true,
	"60": true, "61": true, "62": true, "63": true, "64": true, "65": true, "66": true,
	"81": true, "82": true, "84": true, "86": true,
	"90": true, "91": true, "92": true, "93": true, "94": true, "95": true, "98": true,
}

// maskPhone keeps an E.164 number's country code and last three digits:
// "+94771234123" is "+94•••••123". The hidden part is always five bullets,
// so the mask does not give away the number's length.
func maskPhone(phone string) string {
	digits := strings.TrimPrefix(phone, "+")
	cc := 3
	switch {
	case strings.HasPrefix(digits, "1"), strings.HasPrefix(digits, "7"):
		cc = 1
	case len(digits) >= 2 && twoDigitCallingCodes[digits[:2]]:
		cc = 2
	}
	if len(digits) < cc+4 {
		return "+•••••"
	}
	return "+" + digits[:cc] + "•••••" + digits[len(digits)-3:]
}

// pagingPhoneView is a stored number as the Case Paging tab shows it: the
// full number only for a caller who may edit it.
func pagingPhoneView(c repository.PagingContactRow, withNumber bool) *domain.PagingPhone {
	p := &domain.PagingPhone{
		Masked:         maskPhone(c.Phone),
		SetBy:          c.SetBy,
		SetAt:          c.SetAt.UTC().Format(time.RFC3339),
		LastTestStatus: c.LastTestStatus,
	}
	if withNumber {
		p.Phone = c.Phone
	}
	if c.LastTestAt != nil {
		at := c.LastTestAt.UTC().Format(time.RFC3339)
		p.LastTestAt = &at
	}
	return p
}

// testCallRefusal is why a test call cannot be asked for now: no number
// (409), or one asked for less than pagingTestCooldown ago (429).
func testCallRefusal(c *repository.PagingContactRow, now time.Time) error {
	if c == nil {
		return &apierror.ConflictError{Msg: "this person has no paging number to test"}
	}
	if c.LastTestAt != nil && now.Sub(*c.LastTestAt) < pagingTestCooldown {
		wait := pagingTestCooldown - now.Sub(*c.LastTestAt)
		return &apierror.TooManyRequestsError{Msg: fmt.Sprintf("a test call was asked for less than two minutes ago; try again in %d seconds", int(wait.Seconds())+1)}
	}
	return nil
}

// phoneEditor is the checks every write by a person makes: an internal
// caller with a user token, a well-formed user id, an existing user, and the
// right to edit that user's number. It returns the caller's email and the
// user.
func (s *pagingChainService) phoneEditor(ctx context.Context, userID string) (string, repository.PagingUser, error) {
	if err := RequireInternalCaller(ctx, s.access, "paging numbers are only available to internal staff"); err != nil {
		return "", repository.PagingUser{}, err
	}
	email := auth.IdentityFromContext(ctx).UserEmail
	if email == "" {
		return "", repository.PagingUser{}, &apierror.ForbiddenError{Msg: "changing a paging number needs a user token, not a service credential"}
	}
	if err := validateUUIDs("userId", []string{userID}); err != nil {
		return "", repository.PagingUser{}, err
	}
	if s.contacts == nil {
		return "", repository.PagingUser{}, &apierror.ServiceUnavailableError{Msg: "paging numbers are not available on this deployment"}
	}
	user, err := s.contacts.User(ctx, userID)
	if err != nil {
		return "", repository.PagingUser{}, err
	}
	caller, err := s.repo.CallerAuthority(ctx, email)
	if err != nil {
		return "", repository.PagingUser{}, err
	}
	teams, err := s.contacts.UserTeams(ctx, []string{userID})
	if err != nil {
		return "", repository.PagingUser{}, err
	}
	if !authorityOf(caller).canEditPhone(teams[userID]) {
		return "", repository.PagingUser{}, &apierror.ForbiddenError{Msg: "only a lead of this person's team, a rota admin of its family, the CRE or CS head, or a portal admin can change their paging number"}
	}
	return email, user, nil
}

// SetPagingPhone implements PagingChainService.
func (s *pagingChainService) SetPagingPhone(ctx context.Context, req domain.SetPagingPhoneRequest) (domain.PagingPhone, error) {
	phone := strings.TrimSpace(req.Phone)
	if !e164.MatchString(phone) {
		return domain.PagingPhone{}, &apierror.ValidationError{Msg: "phone must be an E.164 number: a + and 7 to 15 digits, the first not 0, e.g. +94771234567"}
	}
	email, user, err := s.phoneEditor(ctx, req.UserID)
	if err != nil {
		return domain.PagingPhone{}, err
	}
	row, err := s.contacts.Upsert(ctx, email, user.ID, phone)
	if err != nil {
		return domain.PagingPhone{}, err
	}
	return *pagingPhoneView(row, true), nil
}

// DeletePagingPhone implements PagingChainService. Removing a number nobody
// stored is not an error.
func (s *pagingChainService) DeletePagingPhone(ctx context.Context, userID string) error {
	email, user, err := s.phoneEditor(ctx, userID)
	if err != nil {
		return err
	}
	return s.contacts.Delete(ctx, email, user.ID)
}

// RequestTestCall implements PagingChainService: it marks the test pending
// and publishes paging.test_call_requested on the main topic. A publish that
// fails is recorded as a failed test, so the cooldown does not hold the next
// try back for a call that was never placed.
func (s *pagingChainService) RequestTestCall(ctx context.Context, userID string) (domain.PagingTestCallResponse, error) {
	email, user, err := s.phoneEditor(ctx, userID)
	if err != nil {
		return domain.PagingTestCallResponse{}, err
	}
	if s.publisher == nil {
		return domain.PagingTestCallResponse{}, &apierror.ServiceUnavailableError{Msg: "test calls are not available: event publishing is not configured"}
	}
	now := s.now().UTC().Truncate(time.Second)
	current, err := s.contacts.Get(ctx, user.ID)
	if err != nil {
		return domain.PagingTestCallResponse{}, err
	}
	if err := testCallRefusal(current, now); err != nil {
		return domain.PagingTestCallResponse{}, err
	}
	row, marked, err := s.contacts.MarkTestPending(ctx, email, user.ID, now, now.Add(-pagingTestCooldown))
	if err != nil {
		return domain.PagingTestCallResponse{}, err
	}
	if !marked {
		// Something changed between the read and the write: the number was
		// removed, or another test was asked for. Say which.
		again, gerr := s.contacts.Get(ctx, user.ID)
		if gerr != nil {
			return domain.PagingTestCallResponse{}, gerr
		}
		if err := testCallRefusal(again, now); err != nil {
			return domain.PagingTestCallResponse{}, err
		}
		return domain.PagingTestCallResponse{}, &apierror.TooManyRequestsError{Msg: "a test call was asked for less than two minutes ago"}
	}

	payload, err := json.Marshal(events.PagingTestCallRequestedPayload{
		UserID: user.ID, Email: user.Email, Name: user.Name, Phone: row.Phone,
		RequestedBy: email, RequestedAt: now.Format(time.RFC3339),
	})
	if err == nil {
		err = s.publisher.Publish(ctx, events.TypePagingTestCallRequested, user.ID, payload)
	}
	if err != nil {
		// Not logging err itself: it can carry Event Hub client detail.
		slog.ErrorContext(ctx, "paging: publish paging.test_call_requested failed", "userId", user.ID)
		if _, rerr := s.contacts.RecordTestResult(ctx, email, user.ID, "failed", now); rerr != nil {
			slog.ErrorContext(ctx, "paging: recording the failed test call failed", "userId", user.ID, "error", rerr)
		}
		return domain.PagingTestCallResponse{}, &apierror.ServiceUnavailableError{Msg: "the test call could not be requested; try again"}
	}
	return domain.PagingTestCallResponse{LastTestStatus: pagingTestPending}, nil
}

// ListPagingContacts implements PagingChainService: for the service that
// pages and for internal staff, the numbers of the people with the given
// emails -- the one on their own profile ("user".phone), their paging-only
// one, and which of the two to call: the profile number when it is callable,
// else the paging-only number. Someone with neither is left out.
func (s *pagingChainService) ListPagingContacts(ctx context.Context, emails []string) (domain.PagingContactsResponse, error) {
	if err := RequireInternalCaller(ctx, s.access, "paging numbers are only available to internal callers"); err != nil {
		return domain.PagingContactsResponse{}, err
	}
	emails = nonEmptyTrimmed(emails)
	if len(emails) > maxPagingContactEmails {
		return domain.PagingContactsResponse{}, &apierror.ValidationError{Msg: fmt.Sprintf("at most %d emails per request", maxPagingContactEmails)}
	}
	resp := domain.PagingContactsResponse{Contacts: []domain.PagingContact{}}
	if len(emails) == 0 || s.contacts == nil {
		return resp, nil
	}
	rows, err := s.contacts.DialNumbersByEmails(ctx, emails)
	if err != nil {
		return domain.PagingContactsResponse{}, err
	}
	for _, d := range rows {
		c := domain.PagingContact{UserID: d.UserID, Email: d.Email, ProfilePhone: d.ProfilePhone}
		if d.Paging != nil {
			v := pagingPhoneView(*d.Paging, true)
			c.Phone, c.SetBy, c.SetAt = d.Paging.Phone, v.SetBy, v.SetAt
			c.LastTestAt, c.LastTestStatus = v.LastTestAt, v.LastTestStatus
		}
		c.DialPhone, c.DialSource = dialNumber(d.ProfilePhone, c.Phone)
		resp.Contacts = append(resp.Contacts, c)
	}
	return resp, nil
}

// dialNumber is the number paging calls and where it comes from: the
// profile number when it is callable, else the paging-only number.
func dialNumber(profile, paging string) (number, source string) {
	if profile = strings.TrimSpace(profile); e164.MatchString(profile) {
		return profile, dialSourceProfile
	}
	if paging = strings.TrimSpace(paging); paging != "" {
		return paging, dialSourcePaging
	}
	return "", ""
}

const (
	dialSourceProfile = "profile"
	dialSourcePaging  = "paging"
)

// RecordTestResult implements PagingChainService: the outcome of a test
// call, from the service that placed it. A person's token is refused: only
// a service credential reports outcomes.
func (s *pagingChainService) RecordTestResult(ctx context.Context, req domain.PagingTestResultRequest) error {
	if err := RequireInternalCaller(ctx, s.access, "test call results are only accepted from internal services"); err != nil {
		return err
	}
	id := auth.IdentityFromContext(ctx)
	if id.UserEmail != "" {
		return &apierror.ForbiddenError{Msg: "test call results are reported by the service that placed the call, not by a person"}
	}
	if err := validateUUIDs("userId", []string{req.UserID}); err != nil {
		return err
	}
	if !validPagingTestResult[req.Status] {
		return &apierror.ValidationError{Msg: "status must be completed, no-answer, busy or failed"}
	}
	testedAt, err := time.Parse(time.RFC3339, strings.TrimSpace(req.TestedAt))
	if err != nil {
		return &apierror.ValidationError{Msg: "testedAt must be an RFC3339 date-time"}
	}
	if s.contacts == nil {
		return &apierror.ServiceUnavailableError{Msg: "paging numbers are not available on this deployment"}
	}
	actor := "service"
	if id.ClientID != "" {
		actor = "service:" + id.ClientID
	}
	found, err := s.contacts.RecordTestResult(ctx, actor, req.UserID, req.Status, testedAt)
	if err != nil {
		return err
	}
	if !found {
		return &apierror.NotFoundError{Msg: "this person has no paging number"}
	}
	return nil
}

// withPhones fills each member's canEditPhone and pagingPhone. a is the
// caller's authority, nil for a service credential, which may edit nothing
// and is shown masked numbers only.
func (s *pagingChainService) withPhones(ctx context.Context, a *pagingAuthority, members []domain.PagingChainMember) error {
	if s.contacts == nil || len(members) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var ids []string
	for _, m := range members {
		if m.UserID != "" && !seen[m.UserID] {
			seen[m.UserID] = true
			ids = append(ids, m.UserID)
		}
	}
	contacts, err := s.contacts.ByUserIDs(ctx, ids)
	if err != nil {
		return err
	}
	teams := map[string][]repository.PagingTeamRef{}
	if a != nil {
		if teams, err = s.contacts.UserTeams(ctx, ids); err != nil {
			return err
		}
	}
	profiles, err := s.contacts.ProfilePhoneUserIDs(ctx, ids)
	if err != nil {
		return err
	}
	for i := range members {
		m := &members[i]
		m.CanEditPhone = a != nil && a.canEditPhone(teams[m.UserID])
		if c, ok := contacts[m.UserID]; ok {
			m.PagingPhone = pagingPhoneView(c, m.CanEditPhone)
		}
		has := profiles[m.UserID]
		m.HasProfilePhone = &has
	}
	return nil
}
