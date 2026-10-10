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

package servicenow

import (
	"context"
	"encoding/json"
	"net/url"
	"sort"
	"strings"
)

type snNameList struct {
	Result []struct {
		Name string `json:"name"`
	} `json:"result"`
}

// GetProductList returns the sorted list of unique, non-empty product
// names — ported from Ballerina getProductList
// (modules/operations/operations.bal). No caller-supplied input, so there
// is nothing to sanitize.
func (c *Client) GetProductList(ctx context.Context) ([]string, error) {
	raw, err := c.TableQuery(ctx, "cmdb_software_product_model", url.Values{
		"sysparm_fields": {"name"},
		"sysparm_query":  {"nameISNOTEMPTY"},
		"sysparm_limit":  {"500"},
	})
	if err != nil {
		return nil, err
	}

	var data snNameList
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, err
	}

	seen := make(map[string]bool)
	products := make([]string, 0, len(data.Result))
	for _, item := range data.Result {
		name := strings.TrimSpace(item.Name)
		if name != "" && !seen[name] {
			seen[name] = true
			products = append(products, name)
		}
	}
	sort.Strings(products)
	return products, nil
}

// GetABTTeamList returns the sorted list of non-empty ABT team names —
// ported from Ballerina getABTTeamList (modules/operations/operations.bal).
// No caller-supplied input, so there is nothing to sanitize.
func (c *Client) GetABTTeamList(ctx context.Context) ([]string, error) {
	raw, err := c.TableQuery(ctx, "sys_user_group", url.Values{
		"sysparm_fields": {"name"},
		"sysparm_query":  {"parent.name=CS_INT_CRT_LK"},
		"sysparm_limit":  {"500"},
	})
	if err != nil {
		return nil, err
	}

	var data snNameList
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, err
	}

	teams := make([]string, 0, len(data.Result))
	for _, item := range data.Result {
		name := strings.TrimSpace(item.Name)
		if name != "" {
			teams = append(teams, name)
		}
	}
	sort.Strings(teams)
	return teams, nil
}

// ABTTeamRosterMember is one member of an ABT team's roster, without the
// employee-thumbnail enrichment (that stays a caller-level concern — see
// SplABTTeamMembersHandler, which is shared across both the ServiceNow and
// Postgres data sources).
type ABTTeamRosterMember struct {
	Name  string
	Email string
	Role  string
}

type snABTTeamMemberRow struct {
	UserName  string `json:"user.name"`
	UserEmail string `json:"user.email"`
}

type snABTTeamMembersResult struct {
	Result []snABTTeamMemberRow `json:"result"`
}

type snTeamMemberRoleRow struct {
	Role string `json:"u_role"`
}

type snTeamMemberRolesResult struct {
	Result []snTeamMemberRoleRow `json:"result"`
}

// GetABTTeamMembers returns the roster of the ABT team with the given
// ServiceNow sys_id, with each member's role — ported out of
// SplABTTeamMembersHandler (moved here so the handler can depend on a small
// domain interface instead of raw TableQuery, the same shape every other SPL
// domain already uses, which is what let the Postgres data source implement
// the same interface). teamID is not sanitized here — the caller
// (SplABTTeamMembersHandler) already does that before calling in.
func (c *Client) GetABTTeamMembers(ctx context.Context, teamID string) ([]ABTTeamRosterMember, error) {
	membersRaw, err := c.TableQuery(ctx, "sys_user_grmember", url.Values{
		"sysparm_query":  {BuildEncodedQuery("group=" + teamID)},
		"sysparm_fields": {"user.name, user.email"},
	})
	if err != nil {
		return nil, err
	}

	var members snABTTeamMembersResult
	if err := json.Unmarshal(membersRaw, &members); err != nil {
		return nil, err
	}

	roster := make([]ABTTeamRosterMember, 0, len(members.Result))
	for _, m := range members.Result {
		member := ABTTeamRosterMember{Name: m.UserName, Email: m.UserEmail}

		// user.name is ServiceNow's own returned value for this row, not
		// caller-supplied input, so it is not run through SanitizeQueryValue
		// — mirroring the Ballerina source, which also concatenates it
		// unescaped.
		roleRaw, err := c.TableQuery(ctx, "u_team_member_role", url.Values{
			"sysparm_query":  {BuildEncodedQuery("u_member.name=" + m.UserName)},
			"sysparm_fields": {"u_role"},
		})
		if err == nil {
			var roles snTeamMemberRolesResult
			if json.Unmarshal(roleRaw, &roles) == nil && len(roles.Result) > 0 {
				member.Role = roles.Result[0].Role
			}
		}
		roster = append(roster, member)
	}
	return roster, nil
}
