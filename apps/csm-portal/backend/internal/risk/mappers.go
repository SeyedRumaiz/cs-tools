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

package risk

import (
	"strings"

	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/entity"
)

// entity-service's Postgres enums are UPPER_SNAKE_CASE (e.g. "AT_RISK",
// "IN_PROGRESS") -- its own established convention, see that repo's own
// CLAUDE.md. The old MySQL-backed version of this package, and every
// consumer of it (the portal frontend's string-literal comparisons against
// "at_risk"/"open"/"in_progress"/etc., ported verbatim from the original
// Ballerina backend), used lowercase snake_case throughout. Converting case
// at this package's boundary -- lowercase every enum value coming out,
// uppercase every one going in -- keeps every existing caller (frontend and
// otherwise) working unchanged, rather than pushing this translation onto
// every consumer.
func lowerEnum(s string) string { return strings.ToLower(s) }
func upperEnum(s string) string { return strings.ToUpper(s) }

func upperEnumPtr(s *string) *string {
	if s == nil {
		return nil
	}
	v := strings.ToUpper(*s)
	return &v
}

// mapActionItem converts an entity-service action item into this package's
// own wire shape, reformatting its projectId/accountId back into
// upstream-sys_id form and its status/priority back into the lowercase
// snake_case convention every caller expects -- riskId/id stay as the UUID
// strings entity-service assigned them (see types.go's own doc comment).
func mapActionItem(item entity.CHRiskActionItem) RiskActionItem {
	return RiskActionItem{
		ID:                item.ID,
		RiskID:            item.RiskID,
		ProjectSysID:      uuidToSysID(item.ProjectID),
		AccountSysID:      uuidToSysID(item.AccountID),
		Title:             item.Title,
		Description:       item.Description,
		Priority:          lowerEnum(item.Priority),
		Status:            lowerEnum(item.Status),
		AssignedToEmail:   item.AssignedToEmail,
		DueDate:           item.DueDate,
		ResolutionComment: item.ResolutionComment,
		ResolvedByEmail:   item.ResolvedByEmail,
		ResolvedOn:        item.ResolvedOn,
		CreatedByEmail:    item.CreatedByEmail,
		CreatedOn:         item.CreatedOn,
		UpdatedOn:         item.UpdatedOn,
		CommentCount:      item.CommentCount,
	}
}

func mapActionItems(items []entity.CHRiskActionItem) []RiskActionItem {
	mapped := make([]RiskActionItem, len(items))
	for i, item := range items {
		mapped[i] = mapActionItem(item)
	}
	return mapped
}

func mapRisk(r entity.CHProjectRisk) ProjectRisk {
	return ProjectRisk{
		ID:            r.ID,
		ProjectSysID:  uuidToSysID(r.ProjectID),
		AccountSysID:  uuidToSysID(r.AccountID),
		Status:        lowerEnum(r.Status),
		OpenedComment: r.OpenedComment,
		OpenedByEmail: r.OpenedByEmail,
		OpenedOn:      r.OpenedOn,
		ClosedComment: r.ClosedComment,
		ClosedByEmail: r.ClosedByEmail,
		ClosedOn:      r.ClosedOn,
		ActionItems:   mapActionItems(r.ActionItems),
	}
}

func mapHealthStatus(s entity.CHProjectHealthStatus) HealthStatusRecord {
	return HealthStatusRecord{
		ID:              s.ID,
		ProjectSysID:    uuidToSysID(s.ProjectID),
		AccountSysID:    uuidToSysID(s.AccountID),
		Status:          lowerEnum(s.Status),
		ReviewedByEmail: s.ReviewedByEmail,
		ReviewedOn:      s.ReviewedOn,
	}
}

func mapProjectHealthWithOpenRisk(p entity.CHProjectHealthWithOpenRisk) ProjectHealthStatus {
	var openRisk *ProjectRisk
	if p.OpenRisk != nil {
		mapped := mapRisk(*p.OpenRisk)
		openRisk = &mapped
	}
	return ProjectHealthStatus{
		ProjectSysID: uuidToSysID(p.ProjectID),
		HealthStatus: mapHealthStatus(p.HealthStatus),
		OpenRisk:     openRisk,
	}
}

func mapComment(c entity.CHActionItemComment) ActionItemComment {
	return ActionItemComment{
		ID:             c.ID,
		ActionItemID:   c.ActionItemID,
		Comment:        c.Comment,
		CreatedByEmail: c.CreatedByEmail,
		CreatedOn:      c.CreatedOn,
	}
}

func mapComments(comments []entity.CHActionItemComment) []ActionItemComment {
	mapped := make([]ActionItemComment, len(comments))
	for i, c := range comments {
		mapped[i] = mapComment(c)
	}
	return mapped
}
