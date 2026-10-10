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
	"strings"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/repository"
)

type projectContactService struct {
	repo   repository.ProjectContactRepository
	access AccessService
}

// NewProjectContactService constructs a ProjectContactService backed by Postgres.
func NewProjectContactService(repo repository.ProjectContactRepository, access AccessService) ProjectContactService {
	return &projectContactService{repo: repo, access: access}
}

// registeredProjectContactState is the state the access check requires.
const registeredProjectContactState = "REGISTERED"

func projectContactRowToDomain(row repository.ProjectContactRow) domain.ProjectContact {
	pc := domain.ProjectContact{
		Email:             row.Email,
		RegistrationState: row.RegistrationState,
		// NotificationsEnabled has no backing column anywhere in this
		// schema (project_contact, migration 0027, carries no preference
		// column). Defaulted true rather than false: an invited/registered
		// contact is assumed opted-in until a real preference column
		// exists, matching this schema's general "absence means the
		// permissive default" posture elsewhere (e.g. nullable boolean
		// flags read as false only when explicitly set).
		NotificationsEnabled: true,
		Roles:                row.Roles,
		AccountRoles:         row.AccountRoles,
	}
	if pc.Roles == nil {
		pc.Roles = []string{}
	}
	if pc.AccountRoles == nil {
		pc.AccountRoles = []string{}
	}
	if row.ResolvedName != nil && *row.ResolvedName != "" {
		name := *row.ResolvedName
		pc.Name = &name
	}
	if row.ResolvedUserID != nil {
		id := *row.ResolvedUserID
		pc.ID = &id
		// CustomerContactPresent: a "user" row was resolved via
		// account_contact.user_name at all -- see ProjectContactRow's own
		// doc comment.
		pc.CustomerContactPresent = true
		// GrantsCaseAccess mirrors AccessRepository.RegisteredProjectIDs: the
		// user's email matches the invited address and the row is REGISTERED.
		if row.ResolvedEmail != nil && strings.EqualFold(*row.ResolvedEmail, row.Email) &&
			row.RegistrationState == registeredProjectContactState {
			pc.GrantsCaseAccess = true
		}
		// Email is the linked user's address, else the invited one (as on ServiceNow).
		if row.ResolvedEmail != nil && *row.ResolvedEmail != "" {
			pc.Email = *row.ResolvedEmail
		}
	}
	return pc
}

// SearchProjectContacts implements ProjectContactService.
func (s *projectContactService) SearchProjectContacts(ctx context.Context, projectID string, req domain.SearchProjectContactsRequest) (domain.SearchProjectContactsResponse, error) {
	if err := validateUUIDs("id", []string{projectID}); err != nil {
		return domain.SearchProjectContactsResponse{}, err
	}
	if err := normalizePagination(&req.Pagination); err != nil {
		return domain.SearchProjectContactsResponse{}, err
	}
	if err := validateSearchQuery(req.Filters.SearchQuery); err != nil {
		return domain.SearchProjectContactsResponse{}, err
	}

	callerEmail, err := resolveContactCaller(ctx, s.access)
	if err != nil {
		return domain.SearchProjectContactsResponse{}, err
	}

	rows, total, err := s.repo.SearchProjectContacts(ctx, projectID, req, callerEmail)
	if err != nil {
		return domain.SearchProjectContactsResponse{}, err
	}

	contacts := make([]domain.ProjectContact, 0, len(rows))
	for _, row := range rows {
		contacts = append(contacts, projectContactRowToDomain(row))
	}

	return domain.SearchProjectContactsResponse{
		Contacts: contacts,
		Total:    total,
		Limit:    req.Pagination.Limit,
		Offset:   req.Pagination.Offset,
	}, nil
}

// GetProjectContact implements ProjectContactService.
func (s *projectContactService) GetProjectContact(ctx context.Context, projectID, contactID string) (domain.ProjectContact, error) {
	if err := validateUUIDs("id", []string{projectID}); err != nil {
		return domain.ProjectContact{}, err
	}
	if err := validateUUIDs("contactId", []string{contactID}); err != nil {
		return domain.ProjectContact{}, err
	}

	callerEmail, err := resolveContactCaller(ctx, s.access)
	if err != nil {
		return domain.ProjectContact{}, err
	}

	row, err := s.repo.GetProjectContactByUserID(ctx, projectID, contactID, callerEmail)
	if err != nil {
		return domain.ProjectContact{}, err
	}

	return projectContactRowToDomain(row), nil
}
