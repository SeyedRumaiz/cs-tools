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
)

// WorkNoteResponse is the portal-shaped response to posting a work note —
// mirrors Ballerina modules/operations/types.bal's WorkNoteResponse.
type WorkNoteResponse struct {
	Number    string `json:"number"`
	UpdatedOn string `json:"updatedOn"`
}

// caseStateClosedCode is ServiceNow's raw numeric state code for "Closed" —
// see caseStateFromSNMap in reports.go ("3": "Closed").
const caseStateClosedCode = "3"

// ErrCaseClosed is returned by PostWorkNote when the target case is already
// closed — mirrors Ballerina postWorkNotes' "Case is closed. Cannot add
// work notes." 400 response.
var ErrCaseClosed = &notFoundError{"case is closed; work notes cannot be added"}

// ErrCaseSysIDNotFound is returned by PostWorkNote (and reused by other
// files in this package) when a case number does not resolve to a
// ServiceNow sys_id — mirrors Ballerina getCaseSysId's SupportLiteBadRequest
// fallback.
var ErrCaseSysIDNotFound = &notFoundError{"no case found for the given case number"}

type snCaseLookup struct {
	Result []struct {
		SysID string `json:"sys_id"`
		State string `json:"state"`
	} `json:"result"`
}

type snWorkNoteList struct {
	Result struct {
		Number       string `json:"number"`
		SysUpdatedOn string `json:"sys_updated_on"`
	} `json:"result"`
}

// PostWorkNote adds a work note to a ServiceNow case, identified by its
// case number (not sys_id) — ported from Ballerina postWorkNotes
// (modules/operations/operations.bal). caseNumber must already be validated
// by the caller (SanitizeQueryValue) before being passed here, since it is
// concatenated into a sysparm_query string below.
//
// This intentionally re-resolves the case's own sys_id/state via a direct
// Table API call rather than depending on another domain's
// GetCaseByNumber/GetCaseSysID, to keep this file self-contained — see this
// file's package comment / the migration's file-ownership convention. The
// worknote text itself is not query-injection-relevant here (it is sent as
// a PATCH body field value, not concatenated into a query string), so it is
// not run through SanitizeQueryValue.
func (c *Client) PostWorkNote(ctx context.Context, caseNumber, worknote, submitterEmail string) (WorkNoteResponse, error) {
	caseRaw, err := c.TableQuery(ctx, "sn_customerservice_case", url.Values{
		"sysparm_query":  {"number=" + caseNumber},
		"sysparm_fields": {"sys_id,state"},
		"sysparm_limit":  {"1"},
	})
	if err != nil {
		return WorkNoteResponse{}, err
	}
	var caseLookup snCaseLookup
	if err := json.Unmarshal(caseRaw, &caseLookup); err != nil {
		return WorkNoteResponse{}, err
	}
	if len(caseLookup.Result) == 0 {
		return WorkNoteResponse{}, ErrCaseSysIDNotFound
	}
	caseRow := caseLookup.Result[0]
	if caseRow.State == caseStateClosedCode {
		return WorkNoteResponse{}, ErrCaseClosed
	}

	body, err := json.Marshal(map[string]string{
		"work_notes": "[code]" + worknote + "<br><b>Submitted by: " + submitterEmail + "</b><br>[/code]",
	})
	if err != nil {
		return WorkNoteResponse{}, err
	}

	patchRaw, err := c.TablePatch(ctx, "sn_customerservice_case", caseRow.SysID, body)
	if err != nil {
		return WorkNoteResponse{}, err
	}
	var patchResult snWorkNoteList
	if err := json.Unmarshal(patchRaw, &patchResult); err != nil {
		return WorkNoteResponse{}, err
	}

	return WorkNoteResponse{
		Number:    patchResult.Result.Number,
		UpdatedOn: patchResult.Result.SysUpdatedOn,
	}, nil
}
