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

import "context"

// CreateActionItem creates a new action item on the given risk. The action
// item's project and account are derived by entity-service from the risk
// row itself. Fails if the risk doesn't exist or is no longer open -- see
// CustomerHealthRepository.CreateRiskActionItem's own doc comment.
func (c *Client) CreateActionItem(ctx context.Context, riskID string, payload CreateActionItemRequest) (*RiskActionItem, error) {
	resp, err := c.entity.CreateRiskActionItem(ctx, riskID, payload.Title, payload.Description, upperEnum(payload.Priority), payload.AssignedToEmail, payload.DueDate)
	if err != nil {
		return nil, err
	}
	mapped := mapActionItem(resp)
	return &mapped, nil
}

// UpdateActionItemStatus updates an action item's status. See
// CustomerHealthRepository.UpdateRiskActionItemStatus's own doc comment for
// the exact validation rules (resolutionComment required for resolved/
// cancelled; reopening blocked unless the parent risk is open).
func (c *Client) UpdateActionItemStatus(ctx context.Context, actionItemID, newStatus string, resolutionComment *string) (*RiskActionItem, error) {
	resp, err := c.entity.UpdateRiskActionItemStatus(ctx, actionItemID, upperEnum(newStatus), resolutionComment)
	if err != nil {
		return nil, err
	}
	mapped := mapActionItem(resp)
	return &mapped, nil
}

// UpdateActionItem updates an action item's details. Only allowed while its
// status is "open" or "in_progress" -- entity-service returns a 409
// otherwise.
func (c *Client) UpdateActionItem(ctx context.Context, actionItemID string, payload UpdateActionItemRequest) (*RiskActionItem, error) {
	resp, err := c.entity.UpdateRiskActionItem(ctx, actionItemID, payload.Title, payload.Description, upperEnum(payload.Priority), payload.AssignedToEmail, payload.DueDate)
	if err != nil {
		return nil, err
	}
	mapped := mapActionItem(resp)
	return &mapped, nil
}

// GetActionItemsByRisk retrieves all action items for a risk, optionally
// filtered by status, newest first, enriched with comment counts.
func (c *Client) GetActionItemsByRisk(ctx context.Context, riskID string, statusFilter *string) ([]RiskActionItem, error) {
	resp, err := c.entity.GetActionItemsByRisk(ctx, riskID, upperEnumPtr(statusFilter))
	if err != nil {
		return nil, err
	}
	return mapActionItems(resp), nil
}

// GetActionItemsByAccount retrieves all action items belonging to open
// risks under an account, optionally filtered by project and/or status,
// newest first, enriched with comment counts.
func (c *Client) GetActionItemsByAccount(ctx context.Context, accountSysID string, projectSysID, statusFilter *string) ([]RiskActionItem, error) {
	var projectID *string
	if projectSysID != nil {
		converted := sysIDToUUID(*projectSysID)
		projectID = &converted
	}
	resp, err := c.entity.GetActionItemsByAccount(ctx, sysIDToUUID(accountSysID), projectID, upperEnumPtr(statusFilter))
	if err != nil {
		return nil, err
	}
	return mapActionItems(resp), nil
}

// CreateActionItemComment posts a comment on an action item. Fails if
// comment is blank.
func (c *Client) CreateActionItemComment(ctx context.Context, actionItemID, comment string) (*ActionItemComment, error) {
	resp, err := c.entity.CreateActionItemComment(ctx, actionItemID, comment)
	if err != nil {
		return nil, err
	}
	mapped := mapComment(resp)
	return &mapped, nil
}

// GetActionItemComments retrieves all comments for an action item, oldest
// first.
func (c *Client) GetActionItemComments(ctx context.Context, actionItemID string) ([]ActionItemComment, error) {
	resp, err := c.entity.GetActionItemComments(ctx, actionItemID)
	if err != nil {
		return nil, err
	}
	return mapComments(resp), nil
}
