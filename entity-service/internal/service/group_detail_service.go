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

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/repository"
)

// GroupDetailService reads one group and its members -- what the portal opens
// from an approval stage's assignment group. Postgres only: like TeamService
// there is no ServiceNow-backed counterpart, so under DATA_SOURCE=servicenow
// the route is not registered at all.
type GroupDetailService interface {
	// GetGroupDetail returns the group (name, description, email, manager) and
	// its active internal members. A NotFoundError is returned when no group has this
	// id; a group with no members is not an error.
	GetGroupDetail(ctx context.Context, groupID string) (domain.GroupDetail, error)
}

type groupDetailService struct {
	repo   repository.GroupDetailRepository
	access AccessService
}

// NewGroupDetailService constructs a GroupDetailService over the given
// repository. access is what limits the read to internal callers.
func NewGroupDetailService(repo repository.GroupDetailRepository, access AccessService) GroupDetailService {
	return &groupDetailService{repo: repo, access: access}
}

// GetGroupDetail implements GroupDetailService.
//
// Internal callers only. The members of a group are staff -- names, emails,
// who leads whom -- and belong to no project, so there is no narrower scope an
// external (customer) caller could be given: an internal caller sees the group
// and anyone else sees nothing, which also keeps a customer's own contacts
// (who reach the portal through project-scoped reads only) from being
// enumerable through here. The check runs before the id is even parsed, so a
// non-internal caller learns nothing about which ids exist.
func (s *groupDetailService) GetGroupDetail(ctx context.Context, groupID string) (domain.GroupDetail, error) {
	if err := RequireInternalCaller(ctx, s.access, "group membership is only available to internal staff"); err != nil {
		return domain.GroupDetail{}, err
	}
	if err := validateUUIDs("id", []string{groupID}); err != nil {
		return domain.GroupDetail{}, err
	}
	return s.repo.GetGroupDetail(ctx, groupID)
}
