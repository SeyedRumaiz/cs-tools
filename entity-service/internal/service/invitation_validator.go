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
	"strings"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/apierror"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/repository"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/salesentity"
)

// InvitationValidator checks an invitation before anything is written, with
// the rules the project-contact onboarding service applied before this
// service took over the write. Validate returns nil when the invitation may
// go ahead.
type InvitationValidator interface {
	Validate(ctx context.Context, target domain.MembershipWriteTarget, email, inviterEmail string) error
}

// invitationSalesClient is the Salesforce reads the checks need.
type invitationSalesClient interface {
	SearchContactsByEmail(ctx context.Context, email string, limit int) ([]salesentity.Contact, error)
	GetCustomer(ctx context.Context, id string) (salesentity.Customer, error)
}

type invitationValidator struct {
	sales    invitationSalesClient
	partners repository.AccountPartnerRepository
}

// NewInvitationValidator constructs an InvitationValidator. Domain lists are
// read from Salesforce on every call and never stored: the CSM database has
// no copy of them. Partner links are read from account_relationship.
func NewInvitationValidator(sales invitationSalesClient, partners repository.AccountPartnerRepository) InvitationValidator {
	return &invitationValidator{sales: sales, partners: partners}
}

// publicEmailDomains are refused outright, whatever an account's domain list
// says: an address on them says nothing about which organisation the person
// belongs to.
var publicEmailDomains = map[string]bool{
	"gmail.com":   true,
	"yahoo.com":   true,
	"outlook.com": true,
	"hotmail.com": true,
	"live.com":    true,
}

// Messages carried over from the project-contact onboarding service, so an
// admin sees the same answer as before the cutover.
const (
	msgInviterNoAccount = "Access denied. Please sign in with a valid account or contact your account manager for assistance."
	msgNoPermission     = "Sorry, but you do not have the necessary permissions to add/remove users in this project."
	msgNoDomainList     = "We're sorry, but the supported email domains list for your account has not been defined. " +
		"Please reach out to your account manager for assistance."
	msgDomainNotAllowed = "We're sorry, but the domain associated with your email address is not currently supported by " +
		"this project. Please use an email address with a supported domain or reach out to your account manager for assistance."
	msgDuplicateContact = "We're sorry, but there was an error while validating the contact information. " +
		"The contact already exists in our system."
)

// Validate implements InvitationValidator.
//
// inviterEmail is the address of the person sending the invitation. The
// Customer Portal sets it, from the signed-in user's verified token, and that
// turns on the checks about who may invite: the inviter must belong to an
// account that owns the project or partners the project's account, and the
// allowed domains start from the inviter's own account. The CSM Portal
// leaves it empty; its staff are authorized by that portal, so only the
// checks on the invitee's address apply and the allowed domains start from
// the project's own account.
//
// In order:
//  1. The address is not on a public email provider.
//  2. Salesforce holds at most one contact for the address.
//  3. With an inviter: the inviter has a Salesforce contact with an account,
//     and that account owns the project or is one of its partners.
//  4. With an inviter: an existing contact from another organisation, with
//     a live membership on this project, is not taken over.
//  5. The base account (the inviter's, else the project's) has a domain list.
//  6. The address's domain is in the base account's list or in the list of
//     any partner of the project's account.
func (v *invitationValidator) Validate(ctx context.Context, target domain.MembershipWriteTarget, email, inviterEmail string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	inviterEmail = strings.ToLower(strings.TrimSpace(inviterEmail))
	domainPart := emailDomain(email)
	if domainPart == "" {
		return &apierror.ValidationError{Msg: "email is not a valid address"}
	}

	if publicEmailDomains[domainPart] {
		return &apierror.ForbiddenError{Msg: "We're sorry, but we cannot accept public email domains like " + domainPart + "."}
	}

	invitees, err := v.sales.SearchContactsByEmail(ctx, email, 2)
	if err != nil {
		return err
	}
	if len(invitees) > 1 {
		return &apierror.ConflictError{Msg: msgDuplicateContact}
	}

	partnerSfIDs, err := v.partners.PartnerAccountSfIDs(ctx, target.AccountID)
	if err != nil {
		return err
	}

	baseAccountSfID := target.AccountSfID
	if inviterEmail != "" {
		inviters, err := v.sales.SearchContactsByEmail(ctx, inviterEmail, 1)
		if err != nil {
			return err
		}
		if len(inviters) == 0 || accountSfIDOf(inviters[0]) == "" {
			return &apierror.ForbiddenError{Msg: msgInviterNoAccount}
		}
		inviterAccount := accountSfIDOf(inviters[0])
		if inviterAccount != target.AccountSfID && !containsID(partnerSfIDs, inviterAccount) {
			return &apierror.ForbiddenError{Msg: msgNoPermission}
		}
		if len(invitees) == 1 && belongsElsewhereWithLiveMembership(invitees[0], target.ProjectSfID, inviterAccount) {
			return &apierror.ValidationError{Msg: msgDuplicateContact}
		}
		baseAccountSfID = inviterAccount
	}

	base, err := v.sales.GetCustomer(ctx, baseAccountSfID)
	if err != nil {
		return err
	}
	baseDomains := splitDomainList(base.DomainList)
	if len(baseDomains) == 0 {
		return &apierror.ValidationError{Msg: msgNoDomainList}
	}
	if containsID(baseDomains, domainPart) {
		return nil
	}
	for _, id := range partnerSfIDs {
		partner, err := v.sales.GetCustomer(ctx, id)
		if err != nil {
			return err
		}
		if containsID(splitDomainList(partner.DomainList), domainPart) {
			return nil
		}
	}
	return &apierror.ForbiddenError{Msg: msgDomainNotAllowed}
}

// belongsElsewhereWithLiveMembership reports whether an existing contact
// belongs to an account other than inviterAccount while still holding a
// membership of this project that has not been deactivated.
func belongsElsewhereWithLiveMembership(c salesentity.Contact, projectSfID, inviterAccount string) bool {
	account := accountSfIDOf(c)
	if account == "" || account == inviterAccount {
		return false
	}
	for _, m := range c.Memberships {
		if derefString(m.SubscriptionID) != projectSfID {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(derefString(m.State)), domain.MembershipStateDeactivated) {
			return true
		}
	}
	return false
}

func accountSfIDOf(c salesentity.Contact) string {
	if c.Account == nil {
		return ""
	}
	return strings.TrimSpace(derefString(c.Account.ID))
}

// emailDomain returns the lower-cased part after the last "@", or "" when
// there is none.
func emailDomain(email string) string {
	at := strings.LastIndex(email, "@")
	if at <= 0 || at == len(email)-1 {
		return ""
	}
	return strings.ToLower(email[at+1:])
}

// splitDomainList turns Salesforce's "acme.com, acme.io" into its lower-cased
// entries, ignoring blanks.
func splitDomainList(list *string) []string {
	if list == nil {
		return nil
	}
	var out []string
	for _, d := range strings.Split(*list, ",") {
		if d = strings.ToLower(strings.TrimSpace(d)); d != "" {
			out = append(out, d)
		}
	}
	return out
}

func containsID(list []string, want string) bool {
	for _, v := range list {
		if strings.EqualFold(v, want) {
			return true
		}
	}
	return false
}
