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

// ABTTeamScheduleData is the portal-shaped ABT team schedule response —
// mirrors Ballerina modules/operations/types.bal's ABTTeamScheduleData.
type ABTTeamScheduleData struct {
	List     []ABTTeamScheduleList     `json:"list"`
	Metadata []ABTTeamScheduleMetadata `json:"metadata"`
	SnURL    string                    `json:"snURL"`
}

// ABTTeamScheduleList mirrors Ballerina ABTTeamScheduleList.
type ABTTeamScheduleList struct {
	Label   string                  `json:"label"`
	Members []ABTTeamScheduleMember `json:"members"`
}

// ABTTeamScheduleMember mirrors Ballerina ABTTeamScheduleMember.
type ABTTeamScheduleMember struct {
	Name     string                                     `json:"name"`
	Roles    []ABTTeamScheduleMemberRole                `json:"roles"`
	Schedule map[string][]ABTTeamScheduleMemberSchedule `json:"schedule"`
}

// ABTTeamScheduleMemberRole mirrors Ballerina ABTTeamScheduleMemberRoles.
type ABTTeamScheduleMemberRole struct {
	Name  string `json:"name"`
	Label string `json:"label"`
}

// ABTTeamScheduleMemberSchedule mirrors Ballerina
// ABTTeamScheduleMemberSchedule.
type ABTTeamScheduleMemberSchedule struct {
	Name  string `json:"name"`
	Label string `json:"label"`
}

// ABTTeamScheduleMetadata mirrors Ballerina ABTTeamScheduleMetadata.
//
// FIXED (caught during frontend reconciliation, see git log/PR): an earlier
// version of this file declared this as an empty struct, on the mistaken
// belief that getABTTeamScheduleDetails never populated it. It does —
// modules/operations/operations.bal lines ~242-263 fill Teams/EventTypes
// from snTeamScheduleData.result.metadata, and the SPL frontend's team/
// event-type filter dropdowns (TeamSchedulePage.tsx) depend on this data
// being present. If those dropdowns are ever empty against a real backend
// again, check that ServiceNow's actual response still nests
// result.metadata[].teams/eventTypes the way snABTTeamScheduleData below
// expects.
type ABTTeamScheduleMetadata struct {
	Teams      []ABTTeamScheduleMetadataTeam      `json:"teams"`
	EventTypes []ABTTeamScheduleMetadataEventType `json:"eventTypes"`
}

// ABTTeamScheduleMetadataTeam mirrors Ballerina ABTTeamScheduleMetadataTeams.
type ABTTeamScheduleMetadataTeam struct {
	Label string `json:"label"`
	ID    string `json:"id"`
	Link  string `json:"link"`
}

// ABTTeamScheduleMetadataEventType mirrors Ballerina
// ABTTeamScheduleMetadataEventTypes.
type ABTTeamScheduleMetadataEventType struct {
	Name  string `json:"name"`
	Label string `json:"label"`
}

type snABTTeamScheduleData struct {
	Result struct {
		List []struct {
			Label   string `json:"label"`
			Members []struct {
				Name  string `json:"name"`
				Roles []struct {
					Name  string `json:"name"`
					Label string `json:"label"`
				} `json:"roles"`
				Schedule map[string][]struct {
					Name  string `json:"name"`
					Label string `json:"label"`
				} `json:"schedule"`
			} `json:"members"`
		} `json:"list"`
		Metadata []struct {
			Teams []struct {
				Label string `json:"label"`
				ID    string `json:"id"`
				Link  string `json:"link"`
			} `json:"teams"`
			EventTypes []struct {
				Name  string `json:"name"`
				Label string `json:"label"`
			} `json:"eventTypes"`
		} `json:"metadata"`
	} `json:"result"`
}

// GetABTTeamSchedule retrieves an ABT team's schedule — ported from
// Ballerina getABTTeamSchedule + getABTTeamScheduleDetails
// (modules/operations/operations.bal). teamScheduleURL is the
// Ballerina configurable teamScheduleUrl value, echoed back verbatim as
// the response's snURL field (it is never used to build the request) —
// pass it from the caller's configuration rather than storing it on
// Client, since it is specific to this one endpoint.
func (c *Client) GetABTTeamSchedule(ctx context.Context, from, duration, teamID, eventType, teamScheduleURL string) (ABTTeamScheduleData, error) {
	raw, err := c.CustomGet(ctx, "/api/wso2/wso2_team_schedule/schedule", url.Values{
		"sysparm_from":       {from},
		"sysparm_duration":   {duration},
		"sysparm_team":       {teamID},
		"sysparm_event_type": {eventType},
	})
	if err != nil {
		return ABTTeamScheduleData{}, err
	}

	var snData snABTTeamScheduleData
	if err := json.Unmarshal(raw, &snData); err != nil {
		return ABTTeamScheduleData{}, err
	}

	result := ABTTeamScheduleData{
		List:     make([]ABTTeamScheduleList, 0, len(snData.Result.List)),
		Metadata: make([]ABTTeamScheduleMetadata, 0, len(snData.Result.Metadata)),
		SnURL:    teamScheduleURL,
	}
	for _, item := range snData.Result.List {
		members := make([]ABTTeamScheduleMember, 0, len(item.Members))
		for _, member := range item.Members {
			roles := make([]ABTTeamScheduleMemberRole, 0, len(member.Roles))
			for _, role := range member.Roles {
				roles = append(roles, ABTTeamScheduleMemberRole{Name: role.Name, Label: role.Label})
			}
			schedule := make(map[string][]ABTTeamScheduleMemberSchedule, len(member.Schedule))
			for date, entries := range member.Schedule {
				scheduleEntries := make([]ABTTeamScheduleMemberSchedule, 0, len(entries))
				for _, e := range entries {
					scheduleEntries = append(scheduleEntries, ABTTeamScheduleMemberSchedule{Name: e.Name, Label: e.Label})
				}
				schedule[date] = scheduleEntries
			}
			members = append(members, ABTTeamScheduleMember{Name: member.Name, Roles: roles, Schedule: schedule})
		}
		result.List = append(result.List, ABTTeamScheduleList{Label: item.Label, Members: members})
	}

	for _, item := range snData.Result.Metadata {
		teams := make([]ABTTeamScheduleMetadataTeam, 0, len(item.Teams))
		for _, t := range item.Teams {
			teams = append(teams, ABTTeamScheduleMetadataTeam{Label: t.Label, ID: t.ID, Link: t.Link})
		}
		eventTypes := make([]ABTTeamScheduleMetadataEventType, 0, len(item.EventTypes))
		for _, e := range item.EventTypes {
			eventTypes = append(eventTypes, ABTTeamScheduleMetadataEventType{Name: e.Name, Label: e.Label})
		}
		result.Metadata = append(result.Metadata, ABTTeamScheduleMetadata{Teams: teams, EventTypes: eventTypes})
	}

	return result, nil
}
