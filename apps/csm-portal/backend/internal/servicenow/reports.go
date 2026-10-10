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
	"strconv"
)

// SLAReportDetails is the portal-shaped SLA report response — mirrors
// Ballerina modules/operations/types.bal's SLAReportDetails.
type SLAReportDetails struct {
	ProjectName        string               `json:"projectName"`
	ProjectKey         string               `json:"projectKey"`
	PercentileDataList []PercentileDataList `json:"percentileDataList"`
	CaseDataList       []CaseDataList       `json:"caseDataList"`
}

// PercentileDataList mirrors Ballerina PercentileDataList.
type PercentileDataList struct {
	Key            string `json:"key"`
	CaseType       string `json:"caseType"`
	Priority       string `json:"priority"`
	ResponseTime   string `json:"responseTime"`
	WorkaroundTime string `json:"workaroundTime"`
	ResolutionTime string `json:"resolutionTime"`
}

// CaseDataList mirrors Ballerina CaseDataList.
type CaseDataList struct {
	CaseSysID       string `json:"caseSysId"`
	CaseID          string `json:"caseId"`
	CaseNumber      string `json:"caseNumber"`
	CaseType        string `json:"caseType"`
	CasePriority    string `json:"casePriority"`
	CaseState       string `json:"caseState"`
	Opened          string `json:"opened"`
	Response        string `json:"response"`
	ResponseSysID   string `json:"responseSysId"`
	Workaround      string `json:"workaround"`
	WorkaroundSysID string `json:"workaroundSysId"`
	Resolution      string `json:"resolution"`
	ResolutionSysID string `json:"resolutionSysId"`
}

type snSLAReportData struct {
	Result struct {
		ProjectName        string `json:"project_name"`
		ProjectKey         string `json:"project_key"`
		PercentileDataList []struct {
			Key            string `json:"key"`
			CaseType       string `json:"case_type"`
			Priority       string `json:"priority"`
			ResponseTime   string `json:"response_time"`
			WorkaroundTime string `json:"workaround_time"`
			ResolutionTime string `json:"resolution_time"`
		} `json:"percentileDataList"`
		CaseDataList []struct {
			CaseSysID       string `json:"case_sys_id"`
			CaseID          string `json:"case_id"`
			CaseNumber      string `json:"case_number"`
			CaseType        string `json:"case_type"`
			CasePriority    string `json:"case_priority"`
			CaseState       string `json:"case_state"`
			Opened          string `json:"opened"`
			Response        string `json:"response"`
			ResponseSysID   string `json:"response_sys_id"`
			Workaround      string `json:"workaround"`
			WorkaroundSysID string `json:"workaround_sys_id"`
			Resolution      string `json:"resolution"`
			ResolutionSysID string `json:"resolution_sys_id"`
		} `json:"caseDataList"`
	} `json:"result"`
}

// GetSLAReport retrieves the SLA report for a project within a date range —
// ported from Ballerina getSLAReport + getSLAReportDetails
// (modules/operations/operations.bal). projectSysId, from, and to must
// already be validated by the caller (SanitizeQueryValue) before being
// passed here, since they are forwarded as request query parameters, not
// concatenated into a sysparm_query string — ServiceNow's custom
// case_sla/report endpoint takes them as plain query params, mirroring the
// Ballerina snClient->/api/wso2/case_sla/report(...) call shape.
func (c *Client) GetSLAReport(ctx context.Context, projectSysID, from, to string) (SLAReportDetails, error) {
	raw, err := c.CustomGet(ctx, "/api/wso2/case_sla/report", url.Values{
		"project_id": {projectSysID},
		"from":       {from},
		"to":         {to},
	})
	if err != nil {
		return SLAReportDetails{}, err
	}

	var snData snSLAReportData
	if err := json.Unmarshal(raw, &snData); err != nil {
		return SLAReportDetails{}, err
	}

	result := SLAReportDetails{
		ProjectName:        snData.Result.ProjectName,
		ProjectKey:         snData.Result.ProjectKey,
		PercentileDataList: make([]PercentileDataList, 0, len(snData.Result.PercentileDataList)),
		CaseDataList:       make([]CaseDataList, 0, len(snData.Result.CaseDataList)),
	}
	for _, item := range snData.Result.PercentileDataList {
		result.PercentileDataList = append(result.PercentileDataList, PercentileDataList{
			Key: item.Key, CaseType: item.CaseType, Priority: item.Priority,
			ResponseTime: item.ResponseTime, WorkaroundTime: item.WorkaroundTime, ResolutionTime: item.ResolutionTime,
		})
	}
	for _, item := range snData.Result.CaseDataList {
		result.CaseDataList = append(result.CaseDataList, CaseDataList{
			CaseSysID: item.CaseSysID, CaseID: item.CaseID, CaseNumber: item.CaseNumber, CaseType: item.CaseType,
			CasePriority: item.CasePriority, CaseState: item.CaseState, Opened: item.Opened,
			Response: item.Response, ResponseSysID: item.ResponseSysID,
			Workaround: item.Workaround, WorkaroundSysID: item.WorkaroundSysID,
			Resolution: item.Resolution, ResolutionSysID: item.ResolutionSysID,
		})
	}
	return result, nil
}

// CSReportDetails is the portal-shaped customer-success report response —
// mirrors Ballerina modules/operations/types.bal's CSReportDetails.
type CSReportDetails struct {
	SubscriptionDetails SubscriptionDetail  `json:"subscriptionDetails"`
	CasesRecords        []CaseRecordDetail  `json:"casesRecords"`
	SlaDetails          SlaStats            `json:"slaDetails"`
	ProjectDeployments  []ProjectDeployment `json:"projectDeployments"`
	MonthlyCounts       []MonthlyCount      `json:"monthlyCounts"`
}

// SubscriptionDetail mirrors Ballerina SubscriptionDetail.
type SubscriptionDetail struct {
	ProjectName        string `json:"projectName"`
	ProjectKey         string `json:"projectKey"`
	ProjectType        string `json:"projectType"`
	AccountName        string `json:"accountName"`
	StartDate          string `json:"startDate"`
	EndDate            string `json:"endDate"`
	SupportTier        string `json:"supportTier"`
	Subscription       string `json:"subscription"`
	TotalQueryHours    string `json:"totalQueryHours"`
	ConsumedQueryHours string `json:"consumedQueryHours"`
}

// CaseRecordDetail mirrors Ballerina CaseRecordDetail.
type CaseRecordDetail struct {
	CaseSysID      string `json:"caseSysId"`
	CaseNumber     string `json:"caseNumber"`
	EngagementType string `json:"engagementType"`
	CaseType       string `json:"caseType"`
	CasePriority   string `json:"casePriority"`
	CaseState      string `json:"caseState"`
	Opened         string `json:"opened"`
	Description    string `json:"description"`
	Updated        string `json:"updated"`
	Deployment     string `json:"deployment"`
	ProductName    string `json:"productName"`
}

// SlaStats mirrors Ballerina SlaStats.
type SlaStats struct {
	SlaRecords          []SlaRecordDetail   `json:"slaRecords"`
	SlaPerformanceStats SlaPerformanceStats `json:"slaPerformanceStats"`
}

// SlaRecordDetail mirrors Ballerina SlaRecordDetail.
type SlaRecordDetail struct {
	Task                      string `json:"task"`
	SlaDefinition             string `json:"slaDefinition"`
	BusinessElapsedPercentage string `json:"businessElapsedPercentage"`
}

// SlaStatsDetail mirrors Ballerina SlaStatsDetail.
type SlaStatsDetail struct {
	Fraction   json.RawMessage `json:"fraction"` // Ballerina declares this string|int; preserve whichever shape ServiceNow sends rather than guessing one.
	Percentage string          `json:"percentage"`
}

// SlaPerformanceStats mirrors Ballerina SlaPerformanceStats. Field names
// (Workaround/Resolution/Response) are capitalized exactly as the Ballerina
// record declares them — this API response shape is a wire contract the
// (not-yet-ported) SupportPortalLite frontend depends on, so it is
// preserved verbatim rather than normalized to camelCase.
type SlaPerformanceStats struct {
	Workaround SlaStatsDetail `json:"Workaround"`
	Resolution SlaStatsDetail `json:"Resolution"`
	Response   SlaStatsDetail `json:"Response"`
}

// ProjectDeployment mirrors Ballerina ProjectDeployment.
type ProjectDeployment struct {
	Name     string    `json:"name"`
	Products []Product `json:"products"`
}

// Product mirrors Ballerina Product.
type Product struct {
	Name                           string `json:"name"`
	Version                        string `json:"version"`
	SupportStatus                  string `json:"supportStatus"`
	EOLDate                        string `json:"eolDate"`
	Cores                          string `json:"cores"`
	TPS                            string `json:"tps"`
	UpdateLevelInfo                int    `json:"updateLevelInfo"`
	EarliestPossibleSupportEOLDate string `json:"earliestPossibleSupportEOLDate"`
}

// MonthlyCount mirrors Ballerina MonthlyCount.
type MonthlyCount struct {
	YearAndMonth string `json:"yearAndMonth"`
	Counts       Count  `json:"counts"`
}

// Count mirrors Ballerina Count.
type Count struct {
	IncidentCount int `json:"incidentCount"`
	QueryCount    int `json:"queryCount"`
}

// ErrProjectReportNotFound is returned by GetProjectReportDetails when
// ServiceNow has no report data for the given project.
var ErrProjectReportNotFound = &notFoundError{"no CS report data found for project"}

type notFoundError struct{ msg string }

func (e *notFoundError) Error() string { return e.msg }

type snReportDetails struct {
	Result struct {
		SubscriptionDetails struct {
			ProjectName        string `json:"projectName"`
			ProjectKey         string `json:"projectKey"`
			ProjectType        string `json:"projectType"`
			AccountName        string `json:"accountName"`
			StartDate          string `json:"startDate"`
			EndDate            string `json:"endDate"`
			SupportTier        string `json:"supportTier"`
			Subscription       string `json:"subscription"`
			TotalQueryHours    string `json:"totalQueryHours"`
			ConsumedQueryHours string `json:"consumedQueryHours"`
		} `json:"subscriptionDetails"`
		CasesRecords []struct {
			CaseSysID      string `json:"caseSysId"`
			CaseNumber     string `json:"caseNumber"`
			EngagementType string `json:"engagementType"`
			CaseType       string `json:"caseType"`
			CasePriority   string `json:"casePriority"`
			CaseState      string `json:"caseState"`
			Opened         string `json:"opened"`
			Description    string `json:"description"`
			Updated        string `json:"updated"`
			Deployment     string `json:"deployment"`
			ProductName    string `json:"productName"`
		} `json:"casesRecords"`
		SlaDetails struct {
			SlaRecords []struct {
				Task                      string `json:"task"`
				SlaDefinition             string `json:"slaDefinition"`
				BusinessElapsedPercentage string `json:"businessElapsedPercentage"`
			} `json:"slaRecords"`
			SlaPerformanceStats SlaPerformanceStats `json:"slaPerformanceStats"`
		} `json:"slaDetails"`
		ProjectDeployments []struct {
			Name     string `json:"name"`
			Products []struct {
				Name                           string          `json:"name"`
				Version                        string          `json:"version"`
				SupportStatus                  string          `json:"supportStatus"`
				EOLDate                        string          `json:"eolDate"`
				Cores                          json.RawMessage `json:"cores"`
				TPS                            json.RawMessage `json:"tps"`
				UpdateLevelInfo                *int            `json:"updateLevelInfo"`
				EarliestPossibleSupportEOLDate string          `json:"earliestPossibleSupportEOLDate"`
			} `json:"products"`
		} `json:"projectDeployments"`
		MonthlyCounts []struct {
			YearAndMonth string `json:"yearAndMonth"`
			Counts       Count  `json:"counts"`
		} `json:"monthlyCounts"`
	} `json:"result"`
}

// rawToDisplayString converts a Ballerina "string? cores/tps" style
// json.RawMessage (which may be a JSON string, a JSON number, or JSON null)
// to a display string — mirrors Ballerina getCSReportDetails' inline
// `product.cores is string ? product.cores : (product.cores is () ? "" :
// product.cores.toString())` conversion exactly.
func rawToDisplayString(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	// Not a JSON string — strip surrounding quotes only if present; numbers
	// decode to their literal text form, matching Ballerina's toString().
	return string(raw)
}

// GetProjectReportDetails retrieves the customer-success report for a
// project within a date range — ported from Ballerina
// getProjectDetailsByProject + getCSReportDetails
// (modules/operations/operations.bal). projectSysId, from, and to must
// already be validated by the caller before being passed here.
func (c *Client) GetProjectReportDetails(ctx context.Context, projectSysID, from, to string) (CSReportDetails, error) {
	raw, err := c.CustomGet(ctx, "/api/wso2/cs_report/project-insights", url.Values{
		"projectId": {projectSysID},
		"from":      {from},
		"to":        {to},
	})
	if err != nil {
		return CSReportDetails{}, err
	}

	var snData snReportDetails
	if err := json.Unmarshal(raw, &snData); err != nil {
		return CSReportDetails{}, err
	}

	result := CSReportDetails{
		SubscriptionDetails: SubscriptionDetail{
			ProjectName: snData.Result.SubscriptionDetails.ProjectName, ProjectKey: snData.Result.SubscriptionDetails.ProjectKey,
			ProjectType: snData.Result.SubscriptionDetails.ProjectType, AccountName: snData.Result.SubscriptionDetails.AccountName,
			StartDate: snData.Result.SubscriptionDetails.StartDate, EndDate: snData.Result.SubscriptionDetails.EndDate,
			SupportTier: snData.Result.SubscriptionDetails.SupportTier, Subscription: snData.Result.SubscriptionDetails.Subscription,
			TotalQueryHours: snData.Result.SubscriptionDetails.TotalQueryHours, ConsumedQueryHours: snData.Result.SubscriptionDetails.ConsumedQueryHours,
		},
		CasesRecords:       make([]CaseRecordDetail, 0, len(snData.Result.CasesRecords)),
		SlaDetails:         SlaStats{SlaRecords: make([]SlaRecordDetail, 0, len(snData.Result.SlaDetails.SlaRecords)), SlaPerformanceStats: snData.Result.SlaDetails.SlaPerformanceStats},
		ProjectDeployments: make([]ProjectDeployment, 0, len(snData.Result.ProjectDeployments)),
		MonthlyCounts:      make([]MonthlyCount, 0, len(snData.Result.MonthlyCounts)),
	}
	for _, item := range snData.Result.CasesRecords {
		result.CasesRecords = append(result.CasesRecords, CaseRecordDetail{
			CaseSysID: item.CaseSysID, CaseNumber: item.CaseNumber, EngagementType: item.EngagementType, CaseType: item.CaseType,
			CasePriority: item.CasePriority, CaseState: item.CaseState, Opened: item.Opened, Description: item.Description,
			Updated: item.Updated, Deployment: item.Deployment, ProductName: item.ProductName,
		})
	}
	for _, item := range snData.Result.SlaDetails.SlaRecords {
		result.SlaDetails.SlaRecords = append(result.SlaDetails.SlaRecords, SlaRecordDetail{
			Task: item.Task, SlaDefinition: item.SlaDefinition, BusinessElapsedPercentage: item.BusinessElapsedPercentage,
		})
	}
	for _, deployment := range snData.Result.ProjectDeployments {
		products := make([]Product, 0, len(deployment.Products))
		for _, p := range deployment.Products {
			updateLevelInfo := 0
			if p.UpdateLevelInfo != nil {
				updateLevelInfo = *p.UpdateLevelInfo
			}
			products = append(products, Product{
				Name: p.Name, Version: p.Version, SupportStatus: p.SupportStatus, EOLDate: p.EOLDate,
				Cores: rawToDisplayString(p.Cores), TPS: rawToDisplayString(p.TPS),
				UpdateLevelInfo: updateLevelInfo, EarliestPossibleSupportEOLDate: p.EarliestPossibleSupportEOLDate,
			})
		}
		result.ProjectDeployments = append(result.ProjectDeployments, ProjectDeployment{Name: deployment.Name, Products: products})
	}
	for _, m := range snData.Result.MonthlyCounts {
		result.MonthlyCounts = append(result.MonthlyCounts, MonthlyCount{YearAndMonth: m.YearAndMonth, Counts: m.Counts})
	}
	return result, nil
}

// TimeLogBreakdownDetails is the portal-shaped time log breakdown report
// response — mirrors Ballerina TimeLogBreakdownDetails.
type TimeLogBreakdownDetails struct {
	ProjectName         string                 `json:"projectName"`
	ProjectKey          string                 `json:"projectKey"`
	ProjectType         string                 `json:"projectType"`
	RemainingQueryHours string                 `json:"remainingQueryHours"`
	TotalQueryHours     string                 `json:"totalQueryHours"`
	Cases               []TimeLogBreakdownCase `json:"cases"`
}

// TimeLogBreakdownCase mirrors Ballerina TimeLogBreakdownCase.
type TimeLogBreakdownCase struct {
	CaseNumber         string            `json:"caseNumber"`
	CaseID             string            `json:"caseId"`
	CaseType           string            `json:"caseType"`
	ShortDescription   string            `json:"shortDescription"`
	Priority           string            `json:"priority"`
	State              string            `json:"state"`
	TotalHours         string            `json:"totalHours"`
	ConsumedQueryHours string            `json:"consumedQueryHours"`
	TimeCards          []TimeCardDetails `json:"timeCards"`
}

// TimeCardDetails mirrors Ballerina TimeCardDetails.
type TimeCardDetails struct {
	Total      string `json:"total"`
	CreatedOn  string `json:"createdOn"`
	CreatedBy  string `json:"createdBy"`
	UpdatedOn  string `json:"updatedOn"`
	UpdatedBy  string `json:"updatedBy"`
	IsBillable string `json:"isBillable"`
	State      string `json:"state"`
}

// casePriorityMap mirrors Ballerina modules/utils/utils.bal's
// casePriorityMap — ServiceNow's numeric priority code to the portal's
// display label. Kept local to this file rather than a shared file, per
// this migration's file-ownership convention.
var casePriorityMap = map[string]string{
	"10": "Critical (P1)",
	"11": "High (P2)",
	"12": "Medium (P3)",
	"13": "Low (P4)",
	"23": "General Query (P4)",
}

// caseStateFromSNMap mirrors Ballerina caseStateFromSNMap — ServiceNow's
// numeric state code to the portal's display label.
var caseStateFromSNMap = map[string]string{
	"1":    "Open",
	"10":   "Work In Progress",
	"18":   "Awaiting Info",
	"6":    "Solution Proposed",
	"3":    "Closed",
	"7":    "Cancelled",
	"1001": "In Progress",
	"1002": "Waiting on Client",
	"1003": "Waiting on WSO2",
	"1006": "Reopened",
	"1007": "Differed",
}

// formatTime mirrors Ballerina formatTime: minutes to "1h 2m"/"2m".
func formatTime(minutes int) string {
	hours := minutes / 60
	mins := minutes % 60
	if hours == 0 {
		return strconv.Itoa(mins) + "m"
	}
	return strconv.Itoa(hours) + "h " + strconv.Itoa(mins) + "m"
}

// snProjectLookup mirrors Ballerina SNProjectData's fields as used by
// getProjectById (modules/operations/operations.bal) — field names verified
// against that function's exact sysparm_fields list, not guessed.
type snProjectLookup struct {
	Result []struct {
		Number              string `json:"number"`
		SysID               string `json:"sys_id"`
		ShortDescription    string `json:"short_description"`
		ProjectKey          string `json:"u_project_key"`
		RemainingQueryHours string `json:"u_remaining_query_hours"`
		TotalQueryHours     string `json:"u_total_query_hour"`
		ProjectType         string `json:"u_project_type.u_name"`
	} `json:"result"`
}

type snCaseDetailsList struct {
	Result []struct {
		Number       string `json:"number"`
		WSO2CaseID   string `json:"u_wso2_case_id"`
		ShortDesc    string `json:"short_description"`
		CaseTypeName string `json:"u_case_type.u_name"`
		Priority     string `json:"priority"`
		State        string `json:"state"`
	} `json:"result"`
}

type snTimeCardData struct {
	Result []struct {
		Total      string `json:"total"`
		IsBillable string `json:"u_is_billable"`
		CreatedOn  string `json:"sys_created_on"`
		CreatedBy  string `json:"sys_created_by"`
		UpdatedOn  string `json:"sys_updated_on"`
		UpdatedBy  string `json:"sys_updated_by"`
		State      string `json:"state"`
	} `json:"result"`
}

// GetTimeLogBreakdown retrieves the timelogs breakdown report for a
// project — ported from Ballerina getTimeLogBreakdown +
// getTimeLogBreakdownDetails (modules/operations/operations.bal).
// projectID must already be validated by the caller (SanitizeQueryValue)
// before being passed here, since it is concatenated into a sysparm_query
// string below, exactly mirroring the Ballerina original.
//
// This intentionally re-resolves the project's own fields via a direct
// Table API call rather than depending on another domain's
// GetProjectByID, to keep this file self-contained — see this file's
// package comment / the migration's file-ownership convention.
func (c *Client) GetTimeLogBreakdown(ctx context.Context, projectID string) (TimeLogBreakdownDetails, error) {
	projectRaw, err := c.TableQuery(ctx, "customer_project", url.Values{
		"sysparm_query":  {"number=" + projectID},
		"sysparm_fields": {"number,sys_id,short_description,u_project_key,u_remaining_query_hours,u_wso2_closure_state,u_project_type.u_name,u_total_query_hour"},
		"sysparm_limit":  {"1"},
	})
	if err != nil {
		return TimeLogBreakdownDetails{}, err
	}
	var projectData snProjectLookup
	if err := json.Unmarshal(projectRaw, &projectData); err != nil {
		return TimeLogBreakdownDetails{}, err
	}
	if len(projectData.Result) == 0 {
		return TimeLogBreakdownDetails{}, ErrProjectNotFound
	}
	project := projectData.Result[0]

	caseRaw, err := c.TableQuery(ctx, "sn_customerservice_case", url.Values{
		"sysparm_query":  {BuildEncodedQuery("project.number="+projectID, "ORDERBYDESCsys_updated_on")},
		"sysparm_fields": {"number,sys_id,u_wso2_case_id,short_description,u_case_type.u_name,priority,state,opened_by.name,opened_at"},
	})
	if err != nil {
		return TimeLogBreakdownDetails{}, err
	}
	var caseList snCaseDetailsList
	if err := json.Unmarshal(caseRaw, &caseList); err != nil {
		return TimeLogBreakdownDetails{}, err
	}

	result := TimeLogBreakdownDetails{
		ProjectName:         project.ShortDescription,
		ProjectKey:          project.ProjectKey,
		ProjectType:         project.ProjectType,
		RemainingQueryHours: project.RemainingQueryHours,
		TotalQueryHours:     project.TotalQueryHours,
		Cases:               make([]TimeLogBreakdownCase, 0, len(caseList.Result)),
	}

	for _, item := range caseList.Result {
		timeCardRaw, err := c.TableQuery(ctx, "time_card", url.Values{
			"sysparm_query":  {"task.number=" + item.Number},
			"sysparm_fields": {"u_is_billable,sys_created_on,sys_created_by,total,sys_updated_by,sys_updated_on,state"},
		})
		if err != nil {
			return TimeLogBreakdownDetails{}, err
		}
		var timeCardData snTimeCardData
		if err := json.Unmarshal(timeCardRaw, &timeCardData); err != nil {
			return TimeLogBreakdownDetails{}, err
		}

		caseResult := TimeLogBreakdownCase{
			CaseNumber:       item.Number,
			CaseID:           item.WSO2CaseID,
			CaseType:         item.CaseTypeName,
			State:            caseStateFromSNMap[item.State],
			ShortDescription: item.ShortDesc,
			Priority:         casePriorityMap[item.Priority],
			TimeCards:        make([]TimeCardDetails, 0, len(timeCardData.Result)),
		}

		totalMinutes := 0
		consumedMinutes := 0
		for _, tc := range timeCardData.Result {
			total, convErr := strconv.Atoi(tc.Total)
			totalStr := "0"
			if convErr == nil {
				totalStr = formatTime(total)
				totalMinutes += total
				if tc.IsBillable == "true" && tc.State == "Approved" {
					consumedMinutes += total
				}
			}
			caseResult.TimeCards = append(caseResult.TimeCards, TimeCardDetails{
				Total: totalStr, CreatedOn: tc.CreatedOn, CreatedBy: tc.CreatedBy,
				UpdatedOn: tc.UpdatedOn, UpdatedBy: tc.UpdatedBy, IsBillable: tc.IsBillable, State: tc.State,
			})
		}
		caseResult.TotalHours = formatTime(totalMinutes)
		caseResult.ConsumedQueryHours = formatTime(consumedMinutes)

		result.Cases = append(result.Cases, caseResult)
	}

	return result, nil
}

// ErrProjectNotFound is returned when a project lookup by number/ID finds
// no matching ServiceNow record.
var ErrProjectNotFound = &notFoundError{"no project found for the given ID"}
