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

// OpenProjectRisk opens a new risk record for a project and sets the
// project's health status to "at_risk". entity-service derives the
// project's account and the acting user (from the x-user-id-token this
// backend's own Auth middleware already attached to ctx, forwarded
// automatically by entity.CustomerEntityClient) server-side -- see that
// repo's CustomerHealthRepository.OpenProjectRisk doc comment for the exact
// locking/validation rules (fails if the project already has an open risk).
func (c *Client) OpenProjectRisk(ctx context.Context, projectSysID, comment string) (*ProjectRisk, error) {
	resp, err := c.entity.OpenProjectRisk(ctx, sysIDToUUID(projectSysID), comment)
	if err != nil {
		return nil, err
	}
	mapped := mapRisk(resp)
	return &mapped, nil
}

// CloseProjectRisk closes an open risk for a project. See
// CustomerHealthRepository.CloseProjectRisk's own doc comment for the exact
// validation rules (fails if the risk doesn't exist, isn't open, or still
// has open/in_progress action items).
func (c *Client) CloseProjectRisk(ctx context.Context, riskID, comment string) (*ProjectRisk, error) {
	resp, err := c.entity.CloseProjectRisk(ctx, riskID, comment)
	if err != nil {
		return nil, err
	}
	mapped := mapRisk(resp)
	return &mapped, nil
}

// MarkProjectHealthy marks a project healthy and records a closed risk
// entry for audit history.
func (c *Client) MarkProjectHealthy(ctx context.Context, projectSysID string, comment *string) (*HealthStatusRecord, error) {
	resp, err := c.entity.MarkProjectHealthy(ctx, sysIDToUUID(projectSysID), comment)
	if err != nil {
		return nil, err
	}
	mapped := mapHealthStatus(resp)
	return &mapped, nil
}

// RevertProjectHealth reverts a project's health status back to
// "to_be_reviewed".
func (c *Client) RevertProjectHealth(ctx context.Context, projectSysID string) (*HealthStatusRecord, error) {
	resp, err := c.entity.RevertProjectHealth(ctx, sysIDToUUID(projectSysID))
	if err != nil {
		return nil, err
	}
	mapped := mapHealthStatus(resp)
	return &mapped, nil
}

// GetAccountHealthStatus retrieves the health status for every project
// under an account, including each project's currently open risk, if any.
func (c *Client) GetAccountHealthStatus(ctx context.Context, accountSysID string) ([]ProjectHealthStatus, error) {
	resp, err := c.entity.GetAccountProjectHealthStatuses(ctx, sysIDToUUID(accountSysID))
	if err != nil {
		return nil, err
	}
	results := make([]ProjectHealthStatus, len(resp))
	for i, p := range resp {
		results[i] = mapProjectHealthWithOpenRisk(p)
	}
	return results, nil
}

// GetAccountHealthSummary retrieves an account's aggregated overall health
// status derived from all its projects' individual statuses.
func (c *Client) GetAccountHealthSummary(ctx context.Context, accountSysID string) (*HealthSummary, error) {
	resp, err := c.entity.GetAccountHealthSummary(ctx, sysIDToUUID(accountSysID))
	if err != nil {
		return nil, err
	}
	return &HealthSummary{AccountSysID: accountSysID, OverallStatus: lowerEnum(resp.OverallStatus)}, nil
}

// GetProjectRiskHistory retrieves the full risk history for a project,
// newest first.
func (c *Client) GetProjectRiskHistory(ctx context.Context, projectSysID string) ([]ProjectRisk, error) {
	resp, err := c.entity.GetProjectRiskHistory(ctx, sysIDToUUID(projectSysID))
	if err != nil {
		return nil, err
	}
	risks := make([]ProjectRisk, len(resp))
	for i, r := range resp {
		risks[i] = mapRisk(r)
	}
	return risks, nil
}

// GetBatchHealthSummaries retrieves the overall health status for a batch
// of accounts in a single query, returning a map keyed by account sys_id.
func (c *Client) GetBatchHealthSummaries(ctx context.Context, accountSysIDs []string) (map[string]string, error) {
	result := make(map[string]string, len(accountSysIDs))
	if len(accountSysIDs) == 0 {
		return result, nil
	}
	accountIDs := make([]string, len(accountSysIDs))
	for i, sysID := range accountSysIDs {
		accountIDs[i] = sysIDToUUID(sysID)
	}
	resp, err := c.entity.GetBatchAccountHealthSummaries(ctx, accountIDs)
	if err != nil {
		return nil, err
	}
	for i, sysID := range accountSysIDs {
		result[sysID] = lowerEnum(resp[accountIDs[i]])
	}
	return result, nil
}

// GetAccountsByHealthStatus retrieves the account sys_ids matching a given
// health status ("at_risk" or "healthy"); any other value returns an empty
// list, without a round trip to entity-service. Needed because entity-service
// accepts every value this package's domain actually has a status for
// (including "to_be_reviewed") but 400s on one outside that set entirely --
// filtering here first is what makes this wrapper's own "any other value
// returns an empty list" contract hold for an arbitrary/malformed value too,
// not just the one entity-service happens to already treat as "empty".
func (c *Client) GetAccountsByHealthStatus(ctx context.Context, healthStatus string) ([]string, error) {
	if healthStatus != "at_risk" && healthStatus != "healthy" {
		return []string{}, nil
	}
	resp, err := c.entity.GetAccountsByHealthStatus(ctx, upperEnum(healthStatus))
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(resp))
	for i, uuid := range resp {
		ids[i] = uuidToSysID(uuid)
	}
	return ids, nil
}

// InitProjectHealthRows seeds a "to_be_reviewed" health-status row for every
// project sys_id that doesn't already have one under the given account.
// Existing rows are left untouched.
func (c *Client) InitProjectHealthRows(ctx context.Context, projectSysIDs []string, accountSysID string) error {
	projectIDs := make([]string, len(projectSysIDs))
	for i, sysID := range projectSysIDs {
		projectIDs[i] = sysIDToUUID(sysID)
	}
	return c.entity.InitProjectHealthTracking(ctx, sysIDToUUID(accountSysID), projectIDs)
}
