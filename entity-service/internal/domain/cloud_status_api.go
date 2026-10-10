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

package domain

import (
	"encoding/json"
	"math"
	"math/big"
	"strconv"
	"strings"
)

// The public cloud status dashboard's read API.
//
// These types are the Postgres-backed replacement for five ServiceNow Scripted
// REST APIs that wso2-enterprise/uptime-dashboard calls today. Their shapes are
// NOT ours to choose: the dashboard's frontend already renders them, so the
// JSON here reproduces what those scripts emit, field name for field name.
// Where a name reads oddly for Go (display_name, subgroups) that is why.
//
// The scripts live in that repo under servicenow/scripted-rest-api/ and were
// read directly rather than inferred from responses.

// CloudStatusMonitorGroup is one named group within a region, e.g. "Login" or
// "Development", holding the individual monitors shown beneath it.
type CloudStatusMonitorGroup struct {
	DisplayName string                       `json:"display_name"`
	Subgroups   []CloudStatusMonitorSubgroup `json:"subgroups"`
}

// CloudStatusMonitorSubgroup is a single monitored component as the dashboard
// renders it: a row with a status light, an uptime figure and, during an
// incident, a message.
type CloudStatusMonitorSubgroup struct {
	// Description is a POINTER so an absent one serialises as null, not "".
	// ServiceNow emits null, and the frontend keys a tooltip icon off this
	// field -- sending an empty string where it has always received null is
	// the kind of difference that renders.
	Description *string `json:"description"`
	DisplayName string  `json:"display_name"`

	// Status is the dashboard's integer scale, not the Postgres enum:
	// 0 Operational · 1 Maintenance · 2 Degraded · 3 Outage. The mapping is
	// fixed by the frontend's own constants.js and by the flow that writes it.
	Status int `json:"status"`

	Availability []CloudStatusAvailability `json:"availability"`

	// Message is the short description of an ONGOING outage affecting this
	// component, and only when that outage's type agrees with the status the
	// monitor currently shows. Empty otherwise -- see the repository for why
	// that agreement check is not redundant.
	Message string `json:"message"`
}

// CloudStatusAvailability is one uptime figure for one window.
//
// Availability is a STRING, not a number, because the ServiceNow script emits
// it as one and the frontend renders it verbatim. Sending 100 where the
// dashboard has always received "100" is the kind of difference that survives
// every test and changes what a customer sees.
type CloudStatusAvailability struct {
	Availability string `json:"availability"`
	Duration     string `json:"duration"`
}

// CloudStatusIncident is one past or ongoing outage as the incident history
// renders it.
type CloudStatusIncident struct {
	ID               string `json:"id"`
	Begin            string `json:"begin"`
	End              string `json:"end"`
	Type             string `json:"type"`
	Status           string `json:"status"`
	ShortDescription string `json:"short_description"`

	// Expanded drives a UI accordion and is always false on the wire. Kept
	// because the frontend reads it; dropping it would be a frontend change.
	Expanded bool `json:"expanded"`
}

// CloudStatusIncidentMonth is one calendar month's incidents.
type CloudStatusIncidentMonth struct {
	Incidents []CloudStatusIncident `json:"incidents"`
}

// CloudStatusMonitorsResponse is keyed by region -- "cp - us", "eu - dp - eu"
// and so on -- exactly as the dashboard groups its columns.
type CloudStatusMonitorsResponse map[string][]CloudStatusMonitorGroup

// CloudStatusIncidentsResponse is keyed "YYYY-M", e.g. "2026-9". Not
// zero-padded, because the script builds the key by string concatenation and
// the frontend matches on it.
type CloudStatusIncidentsResponse map[string]CloudStatusIncidentMonth

// cloudStatusDashboardStatus maps the stored enum to the dashboard's integer.
//
// MAJOR_OUTAGE has no integer here on purpose: the dashboard's own constants
// define 0-3 only, and the flow that writes these values never produces 4. A
// monitor somehow holding MAJOR_OUTAGE degrades to 3, the most severe the
// dashboard can render, rather than to 0.
var cloudStatusDashboardStatus = map[CloudMonitorStatus]int{
	CloudMonitorStatusOperational:   0,
	CloudMonitorStatusMaintenance:   1,
	CloudMonitorStatusDegraded:      2,
	CloudMonitorStatusPartialOutage: 3,
	CloudMonitorStatusMajorOutage:   3,
}

// DashboardStatus converts a stored monitor status to the dashboard's integer.
// An unknown value becomes 0, matching the script's behaviour on an empty
// u_status.
func DashboardStatus(s CloudMonitorStatus) int {
	return cloudStatusDashboardStatus[s]
}

// outageTypeForStatus is the inverse agreement check the monitors script
// performs before attaching an outage's message to a component.
//
// The script only shows the message when the ongoing outage's TYPE matches the
// status the monitor is currently displaying: planned with 1, degradation with
// 2, outage with 3. That looks redundant -- the flow sets the status from the
// type in the first place -- and is not: the two are written by different
// paths at different times, so a monitor can be showing Degraded while the
// outage that caused it has since been retyped. The check suppresses a message
// that would contradict the light next to it.
var outageTypeForStatus = map[int]string{
	1: "PLANNED",
	2: "DEGRADATION",
	3: "OUTAGE",
}

// MessageAgreesWithStatus reports whether an ongoing outage's type matches the
// status a monitor is showing.
func MessageAgreesWithStatus(status int, outageType string) bool {
	want, ok := outageTypeForStatus[status]
	return ok && want == outageType
}

// ── /availabilities ────────────────────────────────────────────────────

// AvailabilityFigure is one weighted uptime figure for one window.
//
// *** THE WIRE TYPE DIFFERS BY CLOUD, AND THAT IS NOT A MISTAKE. *** In the
// ServiceNow script every calculateX ends
//
//	return parseFloat(running_count).toFixed(precision)   // a STRING
//
// while the unweighted `calculate` ends
//
//	return parseFloat( (...).toFixed(precision) )         // a NUMBER
//
// so the live API really does emit "100.000" for six clouds and 100 for
// asgardeo. Confirmed against it on 2026-09-29. Typing this float64 would
// quietly restyle six clouds' payloads, so the distinction is carried here.
type AvailabilityFigure struct {
	// Value is the already-rounded decimal text, e.g. "100.000" or "99.822".
	Value string
	// Number emits Value as a bare JSON number instead of a string.
	Number bool
}

// MarshalJSON writes the figure as ServiceNow would.
func (f AvailabilityFigure) MarshalJSON() ([]byte, error) {
	if f.Number {
		// Already a valid JSON number; emitting it raw preserves the exact
		// digits, which json.Marshal of a float64 would not.
		return []byte(f.Value), nil
	}
	return json.Marshal(f.Value)
}

// CloudAvailabilityWindow is one {availability, duration} pair.
type CloudAvailabilityWindow struct {
	Availability AvailabilityFigure `json:"availability"`
	Duration     string             `json:"duration"`
}

// CloudAvailabilitiesResponse is keyed by region -- "cp", "us - dp" -- each
// carrying exactly four windows in the script's own order.
type CloudAvailabilitiesResponse map[string][]CloudAvailabilityWindow

// AvailabilityWindows are the four windows, in the order the script emits
// them. The labels are rendered verbatim by the frontend.
var AvailabilityWindows = []struct {
	Enum  string
	Label string
}{
	{"LAST_7_DAYS", "Last 7 days"},
	{"LAST_30_DAYS", "Last 30 days"},
	{"LAST_90_DAYS", "Last 90 days"},
	{"LAST_12_MONTHS", "Last 12 months"},
}

// AvailabilityPrecision is the script's hardcoded `var precision = 3`.
const AvailabilityPrecision = 3

// JSToFixed formats x exactly as JavaScript's Number.prototype.toFixed does.
//
// Go's strconv rounds halves to even; ECMA-262 §21.1.3.3 says "let n be an
// integer for which n / 10^f - x is as close to zero as possible; if there
// are two such n, pick the LARGER n" -- half away from zero for positives.
// The two disagree only on an exact tie, which is rare here and would show up
// as a published uptime one thousandth out. big.Rat makes the comparison
// exact rather than approximately right.
func JSToFixed(x float64, prec int) string {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		// The script would emit "NaN"; callers guard before reaching here.
		return strconv.FormatFloat(x, 'f', prec, 64)
	}
	neg := math.Signbit(x)
	r := new(big.Rat).SetFloat64(math.Abs(x))

	pow := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(prec)), nil)
	scaled := new(big.Rat).Mul(r, new(big.Rat).SetInt(pow))
	// n = floor(scaled + 1/2), which resolves ties upward.
	scaled.Add(scaled, big.NewRat(1, 2))
	n := new(big.Int).Quo(scaled.Num(), scaled.Denom())

	digits := n.String()
	if prec > 0 {
		for len(digits) <= prec {
			digits = "0" + digits
		}
		digits = digits[:len(digits)-prec] + "." + digits[len(digits)-prec:]
	}
	if neg && n.Sign() != 0 {
		digits = "-" + digits
	}
	return digits
}

// TrimJSNumber renders a fixed-decimal string the way JSON.stringify renders
// the number parseFloat() would produce from it: trailing zeros gone, and a
// bare integer where the fraction vanishes entirely.
//
// This is what makes asgardeo's 100.000 arrive as 100 rather than "100.000".
func TrimJSNumber(fixed string) string {
	if !strings.Contains(fixed, ".") {
		return fixed
	}
	fixed = strings.TrimRight(fixed, "0")
	return strings.TrimSuffix(fixed, ".")
}

// ── /history ───────────────────────────────────────────────────────────

// AvailabilityHistoryPoint is one day's uptime.
//
// Availability is a plain number here for EVERY cloud -- the history script
// uses parseFloat and never toFixed. Two endpoints, two conventions, both
// published; see the discovery notes.
type AvailabilityHistoryPoint struct {
	Availability float64 `json:"availability"`
	Date         string  `json:"date"`
}

// AvailabilityHistorySubgroup is one monitored component's 90-day history.
type AvailabilityHistorySubgroup struct {
	DisplayName string                     `json:"display_name"`
	History     []AvailabilityHistoryPoint `json:"history"`
}

// AvailabilityHistoryGroup is one named group within a region.
type AvailabilityHistoryGroup struct {
	DisplayName string                        `json:"display_name"`
	Subgroups   []AvailabilityHistorySubgroup `json:"subgroups"`
}

// CloudAvailabilityHistoryResponse is keyed by region, as the dashboard
// groups its columns.
type CloudAvailabilityHistoryResponse map[string][]AvailabilityHistoryGroup

// AvailabilityHistoryDays is the script's own cap: uniqueAvaialbility.slice(-90).
const AvailabilityHistoryDays = 90

// ── /incident/{id} ─────────────────────────────────────────────────────

// CloudStatusIncidentComment is one entry from an outage's external
// communications journal.
type CloudStatusIncidentComment struct {
	Comment   string `json:"comment"`
	CreatedOn string `json:"created_on"`
}

// CloudStatusIncidentAttachment is one PDF attached to the outage. The
// script filters to application/pdf and emits nothing else.
type CloudStatusIncidentAttachment struct {
	Name string `json:"name"`
	ID   string `json:"id"`
}

// CloudStatusIncidentDetail is the full detail view of one outage.
//
// Note the type labels differ from the enum and from plain title case:
// degradation renders as "Degraded" and planned as "Maintenance". See
// incidentTypeLabel.
type CloudStatusIncidentDetail struct {
	ID               string                          `json:"id"`
	Begin            string                          `json:"begin"`
	End              string                          `json:"end"`
	Type             string                          `json:"type"`
	Status           string                          `json:"status"`
	ShortDescription string                          `json:"short_description"`
	Comments         []CloudStatusIncidentComment    `json:"comments"`
	Attachments      []CloudStatusIncidentAttachment `json:"attachments"`
}

// CloudStatusIncidentAttachmentsOnly is what the endpoint returns when the
// outage exists but its incident does not qualify.
//
// *** THIS ODD SHAPE IS THE SOURCE'S, AND IT IS THE COMMON CASE. *** The
// ServiceNow script builds its payload from an incident lookup that is gated
// on `stateNOT IN 1,3,8`, then assigns attachments onto whatever came back.
// When the lookup matches nothing the payload is an empty object carrying
// only attachments -- no id, no begin, no type. That happens whenever the
// outage has no linked incident at all, which on the current data is 449 of
// 637 outages, and again for the 17 whose incident is still New.
//
// Verified against the live API: an asgardeo outage with no work item
// returns exactly {"attachments":[]}.
type CloudStatusIncidentAttachmentsOnly struct {
	Attachments []CloudStatusIncidentAttachment `json:"attachments"`
}

// incidentDetailTypeLabel is the DETAIL endpoint's own type mapping. It
// agrees with the list endpoint's, and both are reproduced separately
// because nothing guarantees they stay in step.
//
// IncidentDetailStatus renders the two states the detail view shows.
func IncidentDetailStatus(end string) string {
	if end != "" {
		return "Resolved"
	}
	return "In Progress"
}

// ── outage ids on the wire ─────────────────────────────────────────────

// SysID renders a Postgres UUID the way the dashboard has always seen it:
// 32 hex characters with no dashes.
//
// *** THIS IS NOT COSMETIC. *** ServiceNow sys_ids have no dashes, and the
// frontend puts this value straight into a URL — `/incidents/${id}` — and
// matches it between the incident list and the detail page. Emitting the
// dashed form changes every incident link on the site and breaks any
// bookmarked one. Confirmed against the live API, which returns
// "7af3a9683bab839091404c6aa5e45a10".
func SysID(uuid string) string {
	return strings.ReplaceAll(uuid, "-", "")
}

// LooksLikeUUID reports whether s has the 8-4-4-4-12 hex shape Postgres will
// accept as a uuid.
//
// Checked in Go rather than left to the database because the alternative is
// letting a malformed public URL become "invalid input syntax for type uuid",
// which surfaces as a 500 and an internal error log. A caller cannot tell a
// bad id from a broken service, and a scanner walking the URL space fills the
// logs with neither.
func LooksLikeUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
				return false
			}
		}
	}
	return true
}

// UUIDFromSysID is the inverse, for ids arriving from the dashboard.
//
// Accepts either form: a 32-hex sys_id gets the 8-4-4-4-12 dashes put back,
// and anything else is returned unchanged for Postgres to accept or reject.
// Being liberal here means a link created before the cutover still resolves
// after it.
func UUIDFromSysID(id string) string {
	id = strings.TrimSpace(id)
	if len(id) != 32 {
		return id
	}
	for _, r := range id {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return id
		}
	}
	return id[0:8] + "-" + id[8:12] + "-" + id[12:16] + "-" + id[16:20] + "-" + id[20:]
}
