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

// ----- request payloads -----
//
// AccountSysID used to be required on OpenRiskRequest/MarkHealthyRequest/
// RevertHealthRequest/CreateActionItemRequest because the old MySQL tables
// had no foreign key to derive it from the project/risk row itself -- the
// caller had to supply it independently, with nothing enforcing it actually
// matched the project's real account. entity-service derives it server-side
// instead (project -> account, risk -> project -> account), so these fields
// are gone; a caller that still sends accountSysId in the request body is
// harmless (it's simply ignored by the JSON decoder).

// OpenRiskRequest is the payload for opening a new risk on a project.
type OpenRiskRequest struct {
	Comment string `json:"comment"`
}

// CloseRiskRequest is the payload for closing an open risk.
type CloseRiskRequest struct {
	Comment string `json:"comment"`
}

// MarkHealthyRequest is the payload for marking a project healthy.
type MarkHealthyRequest struct {
	Comment *string `json:"comment"`
}

// RevertHealthRequest is the payload for reverting a project's health
// status back to "to_be_reviewed". It carries no fields any more (see the
// package doc comment above) but is kept as a named type since the handler
// still decodes the request body into it.
type RevertHealthRequest struct{}

// CreateActionItemRequest is the payload for creating a new action item on
// an open risk.
type CreateActionItemRequest struct {
	Title           string  `json:"title"`
	Description     *string `json:"description"`
	Priority        string  `json:"priority"`
	AssignedToEmail *string `json:"assignedToEmail"`
	DueDate         string  `json:"dueDate"`
}

// UpdateActionItemStatusRequest is the payload for updating an action
// item's status.
type UpdateActionItemStatusRequest struct {
	Status            string  `json:"status"`
	ResolutionComment *string `json:"resolutionComment"`
}

// UpdateActionItemRequest is the payload for updating an action item's
// details.
type UpdateActionItemRequest struct {
	Title           string  `json:"title"`
	Description     *string `json:"description"`
	Priority        string  `json:"priority"`
	AssignedToEmail *string `json:"assignedToEmail"`
	DueDate         *string `json:"dueDate"`
}

// CreateCommentRequest is the payload for posting a comment on an action
// item.
type CreateCommentRequest struct {
	Comment string `json:"comment"`
}

// InitHealthTrackingRequest is the payload for initialising health-tracking
// rows for a list of projects under an account.
type InitHealthTrackingRequest struct {
	ProjectSysIDs []string `json:"projectSysIds"`
}

// ----- API response types -----
//
// ID/RiskID/ActionItemID are now UUID strings, not auto-increment ints: the
// old MySQL tables generated sequential integer primary keys, but
// entity-service's project_risk/risk_action_item/action_item_comment tables
// (migration 0219, entity-service repo) use gen_random_uuid() like every
// other table in that schema. ProjectSysID/AccountSysID stay in their
// original upstream-sys_id (32-char hex, no dashes) shape -- see
// sysIDToUUID/uuidToSysID in client.go -- since those identify rows
// entity-service already has a sys_id for (project, account), unlike the
// risk/action-item/comment rows themselves, which are new resources with no
// upstream equivalent and so no sys_id to preserve.

// RiskActionItem is an action item attached to a project risk.
type RiskActionItem struct {
	ID                string  `json:"id"`
	RiskID            string  `json:"riskId"`
	ProjectSysID      string  `json:"projectSysId"`
	AccountSysID      string  `json:"accountSysId"`
	Title             string  `json:"title"`
	Description       *string `json:"description"`
	Priority          string  `json:"priority"`
	Status            string  `json:"status"`
	AssignedToEmail   *string `json:"assignedToEmail"`
	DueDate           *string `json:"dueDate"`
	ResolutionComment *string `json:"resolutionComment"`
	ResolvedByEmail   *string `json:"resolvedByEmail"`
	ResolvedOn        *string `json:"resolvedOn"`
	CreatedByEmail    string  `json:"createdByEmail"`
	CreatedOn         string  `json:"createdOn"`
	UpdatedOn         string  `json:"updatedOn"`
	CommentCount      int     `json:"commentCount"`
}

// ProjectRisk is a project's risk record, open or closed, together with its
// action items.
type ProjectRisk struct {
	ID            string           `json:"id"`
	ProjectSysID  string           `json:"projectSysId"`
	AccountSysID  string           `json:"accountSysId"`
	Status        string           `json:"status"`
	OpenedComment string           `json:"openedComment"`
	OpenedByEmail string           `json:"openedByEmail"`
	OpenedOn      string           `json:"openedOn"`
	ClosedComment *string          `json:"closedComment"`
	ClosedByEmail *string          `json:"closedByEmail"`
	ClosedOn      *string          `json:"closedOn"`
	ActionItems   []RiskActionItem `json:"actionItems"`
}

// HealthStatusRecord is a project's current health-review status.
type HealthStatusRecord struct {
	ID              string  `json:"id"`
	ProjectSysID    string  `json:"projectSysId"`
	AccountSysID    string  `json:"accountSysId"`
	Status          string  `json:"status"`
	ReviewedByEmail *string `json:"reviewedByEmail"`
	ReviewedOn      *string `json:"reviewedOn"`
}

// ProjectHealthStatus is a project's health status together with its
// currently open risk, if any.
type ProjectHealthStatus struct {
	ProjectSysID string             `json:"projectSysId"`
	HealthStatus HealthStatusRecord `json:"healthStatus"`
	OpenRisk     *ProjectRisk       `json:"openRisk"`
}

// HealthSummary is an account's aggregated overall health status, derived
// from its projects' individual health statuses.
type HealthSummary struct {
	AccountSysID  string `json:"accountSysId"`
	OverallStatus string `json:"overallStatus"`
}

// ActionItemComment is a comment posted on an action item.
type ActionItemComment struct {
	ID             string `json:"id"`
	ActionItemID   string `json:"actionItemId"`
	Comment        string `json:"comment"`
	CreatedByEmail string `json:"createdByEmail"`
	CreatedOn      string `json:"createdOn"`
}
