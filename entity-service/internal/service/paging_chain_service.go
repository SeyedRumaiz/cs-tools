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
	"sort"
	"strings"
	"time"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/apierror"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/auth"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/repository"
)

// PagingChainService backs the Team Schedule's Case Paging tab: who is on
// each tier of a team's paging chain, and changing them.
//
// Who may change what (agreed with the CRE rota owners, 2026-10-04):
//
//	1st-3rd responders of a team   that team's Team lead -- or, where the team
//	                               has an America lead, the America lead --
//	                               and a CRE rota admin
//	a team's Team leads            the CRE head and CS head -- or, where the
//	                               team has an America lead, the America lead
//	                               -- and a CRE rota admin
//	the America lead, the CRE      a CRE rota admin or a portal admin
//	head and the CS head
//
// The SRE side is read-only for now: the SRE paging chain takes its tiers
// from the rota itself, so there is nothing on this tab for an SRE edit to
// change. Every rule is checked here, on the write; what the tab locks is
// only a reflection of it.
type PagingChainService interface {
	GetPagingChain(ctx context.Context, family string) (domain.PagingChainResponse, error)
	UpdatePagingMember(ctx context.Context, req domain.UpdatePagingMemberRequest) (domain.PagingChainMember, error)
	// GetPagingReadiness answers whether each chain would reach someone over
	// the next days (days: 1-14, empty for 7). See paging_readiness.go.
	GetPagingReadiness(ctx context.Context, days string) (domain.PagingReadinessResponse, error)

	// Paging-only phone numbers; see paging_phone.go.
	SetPagingPhone(ctx context.Context, req domain.SetPagingPhoneRequest) (domain.PagingPhone, error)
	DeletePagingPhone(ctx context.Context, userID string) error
	RequestTestCall(ctx context.Context, userID string) (domain.PagingTestCallResponse, error)
	ListPagingContacts(ctx context.Context, emails []string) (domain.PagingContactsResponse, error)
	RecordTestResult(ctx context.Context, req domain.PagingTestResultRequest) error
}

type pagingChainService struct {
	repo     repository.PagingChainRepository
	contacts repository.PagingContactRepository
	access   AccessService
	// publisher is the main shared topic's, which test calls go out on;
	// nil turns test calls off.
	publisher EventPublisherService
	// handoff is the specialist handoff routing, read by the readiness check
	// for which SME team each handoff dialog team pages. Nil reads as no
	// dialog teams.
	handoff *SpecialistHandoffConfig
	now     func() time.Time
}

// NewPagingChainService constructs the Case Paging service. contacts and
// publisher may be nil: no paging numbers, and no test calls, respectively.
func NewPagingChainService(repo repository.PagingChainRepository, contacts repository.PagingContactRepository, access AccessService, handoff *SpecialistHandoffConfig, publisher EventPublisherService) PagingChainService {
	return &pagingChainService{repo: repo, contacts: contacts, access: access, handoff: handoff, publisher: publisher, now: time.Now}
}

// Role names this service decides on. The two global roles are the ones
// RotaAdminTeamsFor and the user search already treat as these people.
const (
	roleNameCRERotaAdmin = "cre_rota_admin"
	roleNamePortalAdmin  = "admin"

	memberRoleEngineer = "engineer"
	memberRoleSubLead  = "sub_lead"
	memberRoleLead     = "lead"
	memberRoleAmerica  = "americas_team_lead"
	memberRoleCREHead  = "cre_head"
	memberRoleCSHead   = "cs_head"

	familyCRE = "CRE"
	familySRE = "SRE"
	familySME = "SME"
)

// pagingAuthority is a caller's standing for Case Paging edits.
type pagingAuthority struct {
	portalAdmin  bool
	creRotaAdmin bool
	sreRotaAdmin bool
	smeRotaAdmin bool
	head         bool            // holds cre_head or cs_head somewhere
	leadOf       map[string]bool // a Team lead or the America lead of the team
	headOf       map[string]bool // the America lead of the team
}

func authorityOf(c repository.PagingCaller) pagingAuthority {
	a := pagingAuthority{leadOf: map[string]bool{}, headOf: map[string]bool{}}
	for _, r := range c.GlobalRoles {
		switch strings.ToLower(r) {
		case roleNameCRERotaAdmin:
			a.creRotaAdmin = true
		case roleNameSRERotaAdmin:
			a.sreRotaAdmin = true
		case roleNameSMERotaAdmin:
			a.smeRotaAdmin = true
		case roleNamePortalAdmin:
			a.portalAdmin = true
		}
	}
	for _, m := range c.Memberships {
		key := strings.ToLower(m.TeamKey)
		switch m.Role {
		case memberRoleLead:
			a.leadOf[key] = true
		case memberRoleAmerica:
			a.leadOf[key] = true
			a.headOf[key] = true
		case memberRoleCREHead, memberRoleCSHead:
			a.head = true
		}
	}
	return a
}

// teamFacts is what the rules need to know about the team being changed.
type teamFacts struct {
	key     string
	family  string
	hasHead bool
}

func (a pagingAuthority) canSetResponders(t teamFacts) bool {
	if t.family != familyCRE {
		return false
	}
	if a.creRotaAdmin {
		return true
	}
	if t.hasHead {
		return a.headOf[t.key]
	}
	return a.leadOf[t.key]
}

func (a pagingAuthority) canSetTeamLeads(t teamFacts) bool {
	if t.family != familyCRE {
		return false
	}
	if a.creRotaAdmin {
		return true
	}
	if t.hasHead {
		return a.headOf[t.key]
	}
	return a.head
}

func (a pagingAuthority) canSetHeads(family string) bool {
	return family == familyCRE && (a.creRotaAdmin || a.portalAdmin)
}

func (s *pagingChainService) GetPagingChain(ctx context.Context, family string) (domain.PagingChainResponse, error) {
	if err := RequireInternalCaller(ctx, s.access, "the paging chain is only available to internal staff"); err != nil {
		return domain.PagingChainResponse{}, err
	}
	family = strings.ToUpper(strings.TrimSpace(family))
	if family == "" {
		family = familyCRE
	}
	if family != familyCRE && family != familySRE {
		return domain.PagingChainResponse{}, &apierror.ValidationError{Msg: "family must be CRE or SRE"}
	}
	members, err := s.repo.ListMembers(ctx, family)
	if err != nil {
		return domain.PagingChainResponse{}, err
	}

	resp := domain.PagingChainResponse{
		Family:  family,
		Members: members,
		Count:   len(members),
		CanEdit: domain.PagingChainPermissions{ResponderTeams: []string{}, TeamLeadTeams: []string{}},
	}
	// A machine caller reads the chain but edits nothing: edits are a
	// person's, and attributed to them.
	email := auth.IdentityFromContext(ctx).UserEmail
	if email == "" {
		if err := s.withPhones(ctx, nil, resp.Members); err != nil {
			return domain.PagingChainResponse{}, err
		}
		return resp, nil
	}
	caller, err := s.repo.CallerAuthority(ctx, email)
	if err != nil {
		return domain.PagingChainResponse{}, err
	}
	a := authorityOf(caller)
	if err := s.withPhones(ctx, &a, resp.Members); err != nil {
		return domain.PagingChainResponse{}, err
	}

	teams := map[string]*teamFacts{}
	for _, m := range members {
		key := strings.ToLower(m.TeamKey)
		t, ok := teams[key]
		if !ok {
			t = &teamFacts{key: key, family: m.Family}
			teams[key] = t
		}
		if m.Role == memberRoleAmerica {
			t.hasHead = true
		}
	}
	for _, t := range teams {
		if a.canSetResponders(*t) {
			resp.CanEdit.ResponderTeams = append(resp.CanEdit.ResponderTeams, t.key)
		}
		if a.canSetTeamLeads(*t) {
			resp.CanEdit.TeamLeadTeams = append(resp.CanEdit.TeamLeadTeams, t.key)
		}
	}
	sort.Strings(resp.CanEdit.ResponderTeams)
	sort.Strings(resp.CanEdit.TeamLeadTeams)
	resp.CanEdit.AmericasTeamLead = a.canSetHeads(family)
	resp.CanEdit.Heads = a.canSetHeads(family)
	return resp, nil
}

func (s *pagingChainService) UpdatePagingMember(ctx context.Context, req domain.UpdatePagingMemberRequest) (domain.PagingChainMember, error) {
	if err := RequireInternalCaller(ctx, s.access, "the paging chain is only available to internal staff"); err != nil {
		return domain.PagingChainMember{}, err
	}
	email := auth.IdentityFromContext(ctx).UserEmail
	if email == "" {
		return domain.PagingChainMember{}, &apierror.ForbiddenError{Msg: "changing the paging chain needs a user token, not a service credential"}
	}
	if err := validateUUIDs("membershipId", []string{req.MembershipID}); err != nil {
		return domain.PagingChainMember{}, err
	}
	set := 0
	for _, present := range []bool{req.ResponderRank != nil, req.Role != nil} {
		if present {
			set++
		}
	}
	if set != 1 {
		return domain.PagingChainMember{}, &apierror.ValidationError{Msg: "send exactly one of responderRank or role"}
	}

	target, err := s.repo.GetMember(ctx, req.MembershipID)
	if err != nil {
		return domain.PagingChainMember{}, err
	}
	caller, err := s.repo.CallerAuthority(ctx, email)
	if err != nil {
		return domain.PagingChainMember{}, err
	}
	a := authorityOf(caller)
	team := teamFacts{
		key:     strings.ToLower(target.TeamKey),
		family:  target.Family,
		hasHead: target.TeamHasHead || target.Role == memberRoleAmerica,
	}
	if team.family != familyCRE {
		return domain.PagingChainMember{}, &apierror.ForbiddenError{Msg: "the SRE paging chain comes from the rota and is not changed here"}
	}

	if req.ResponderRank != nil {
		err = s.setResponderRank(ctx, a, team, target, email, *req.ResponderRank)
	} else {
		err = s.setRole(ctx, a, team, target, email, strings.ToLower(strings.TrimSpace(*req.Role)))
	}
	if err != nil {
		return domain.PagingChainMember{}, err
	}
	updated, err := s.repo.GetMember(ctx, req.MembershipID)
	if err != nil {
		return domain.PagingChainMember{}, err
	}
	out := []domain.PagingChainMember{updated.PagingChainMember}
	if err := s.withPhones(ctx, &a, out); err != nil {
		return domain.PagingChainMember{}, err
	}
	return out[0], nil
}

func (s *pagingChainService) setResponderRank(ctx context.Context, a pagingAuthority, team teamFacts, t repository.PagingChainTarget, actor string, rank int) error {
	if rank < 0 || rank > 3 {
		return &apierror.ValidationError{Msg: "responderRank must be 1, 2 or 3, or 0 to clear"}
	}
	if !a.canSetResponders(team) {
		return &apierror.ForbiddenError{Msg: "only this team's lead or a CRE rota admin can pick its responders"}
	}
	if rank > 0 && t.Role != memberRoleEngineer && t.Role != memberRoleSubLead {
		return &apierror.ValidationError{Msg: "a Team lead or head cannot also be a responder"}
	}
	return s.repo.SetResponderRank(ctx, actor, t.MembershipID, rank)
}

func (s *pagingChainService) setRole(ctx context.Context, a pagingAuthority, team teamFacts, t repository.PagingChainTarget, actor, role string) error {
	isTop := func(r string) bool {
		return r == memberRoleCREHead || r == memberRoleCSHead || r == memberRoleAmerica
	}
	switch {
	case isTop(role):
		// Making someone the America lead, CRE head or CS head.
		if !a.canSetHeads(team.family) {
			return &apierror.ForbiddenError{Msg: "only a CRE rota admin or a portal admin can set the America lead, CRE head and CS head"}
		}
	case role == memberRoleLead || role == memberRoleEngineer:
		// Stepping one of those three down is theirs to decide too; anything
		// else about a team's leads is a Team lead decision.
		if isTop(t.Role) {
			if !a.canSetHeads(team.family) {
				return &apierror.ForbiddenError{Msg: "only a CRE rota admin or a portal admin can change the America lead, CRE head and CS head"}
			}
			break
		}
		if !a.canSetTeamLeads(team) {
			return &apierror.ForbiddenError{Msg: "you cannot change this team's Team leads"}
		}
	default:
		return &apierror.ValidationError{Msg: "role must be lead, engineer, americas_team_lead, cre_head or cs_head"}
	}
	if role == t.Role {
		return nil
	}
	return s.repo.SetRole(ctx, actor, t.MembershipID, role)
}
