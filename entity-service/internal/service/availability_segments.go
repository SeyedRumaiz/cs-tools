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

import "time"

// Which periods one run computes, ported from AvailabilitySegmentProcessor.
//
// Two shapes, and the difference decides how they are written:
//
//   - FIXED (daily, weekly, monthly, annually) — a calendar period. Written
//     once and then updated in place, because yesterday's Tuesday is still
//     yesterday's Tuesday tomorrow.
//   - ROLLING (last 7/30/90 days, last 12 months) — a window ending now.
//     *** DELETED AND REWRITTEN every run. *** Yesterday's "last 30 days" is
//     not a period anyone wants to keep; it is a stale answer to today's
//     question. A port that upserts these the way it upserts a fixed period
//     accumulates one row per day per subject forever.
//
// *** LAST_90_DAYS IS NOT IN SERVICENOW v2, AND IS EMITTED ANYWAY. ***
// v2's segment processor registers seven types and that is not one of them;
// v1 writes it via _lastDays(90). But BOTH live dashboard endpoints read it —
// /monitors queries LAST_30_DAYS and LAST_90_DAYS, /availabilities queries
// four types including LAST_90_DAYS — and both render a "Last 90 days" figure
// on the customer-facing status page. Adhering to v2 unchanged would remove
// that number silently: no rows, no error, just a gap. It is computed by the
// same calculator over the same window shape, so emitting it is an addition
// rather than a divergence.
//
// LAST_1_DAYS is the mirror image: v1 writes it, the instance holds 146 rows
// of it, and NOTHING reads it. Deliberately not emitted.
const (
	AvailabilityTypeDaily        = "DAILY"
	AvailabilityTypeWeekly       = "WEEKLY"
	AvailabilityTypeMonthly      = "MONTHLY"
	AvailabilityTypeAnnually     = "ANNUALLY"
	AvailabilityTypeLast7Days    = "LAST_7_DAYS"
	AvailabilityTypeLast30Days   = "LAST_30_DAYS"
	AvailabilityTypeLast90Days   = "LAST_90_DAYS"
	AvailabilityTypeLast12Months = "LAST_12_MONTHS"
)

// AvailabilitySegment is one period to compute and store.
type AvailabilitySegment struct {
	Type string
	// Begin and End are half-open: [Begin, End).
	Begin time.Time
	End   time.Time
	// Rolling marks a window that must be deleted and rewritten rather than
	// updated in place.
	Rolling bool
}

// AvailabilitySegmentsFor returns every period a run at `now` must compute,
// with all boundaries resolved in loc.
//
// *** THE LOCATION IS NOT COSMETIC. *** ServiceNow sets the session timezone
// for the duration of the calculation and resolves every boundary in it, so
// "the beginning of today" is midnight in the commitment's zone, not UTC.
// The stored history proves the consequence: daily rows start at 00:00:00 in
// 2022 and at 18:30:00 in 2026, and 18:30Z is midnight in Asia/Colombo. Pass
// the zone the commitment specifies; the rows will not line up otherwise.
func AvailabilitySegmentsFor(now time.Time, loc *time.Location) []AvailabilitySegment {
	if loc == nil {
		loc = time.UTC
	}
	n := now.In(loc)

	startOfToday := time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, loc)
	// Every rolling window ends here, and every one of them is measured
	// BACKWARDS from here rather than from the start of today — which is how
	// ServiceNow makes the window include the whole of today.
	startOfTomorrow := startOfToday.AddDate(0, 0, 1)

	// Fixed periods: [start of the period containing now, start of the next).
	// ServiceNow writes endOfSegment() plus one second, which is the same
	// instant; expressed directly here because a half-open interval is what
	// the arithmetic actually wants and "23:59:59 plus a second" invites
	// somebody to store the 23:59:59.
	startOfWeek := startOfToday.AddDate(0, 0, -weekdayOffset(startOfToday))
	startOfMonth := time.Date(n.Year(), n.Month(), 1, 0, 0, 0, 0, loc)
	startOfYear := time.Date(n.Year(), 1, 1, 0, 0, 0, 0, loc)

	return []AvailabilitySegment{
		{Type: AvailabilityTypeDaily, Begin: startOfToday, End: startOfTomorrow},
		{Type: AvailabilityTypeWeekly, Begin: startOfWeek, End: startOfWeek.AddDate(0, 0, 7)},
		{Type: AvailabilityTypeMonthly, Begin: startOfMonth, End: startOfMonth.AddDate(0, 1, 0)},
		{Type: AvailabilityTypeAnnually, Begin: startOfYear, End: startOfYear.AddDate(1, 0, 0)},

		// *** THESE ARE FULL N-DAY WINDOWS, AND v1's WERE NOT. ***
		// The legacy summarizer computes last-N as
		//     beginningOfDay(start) - (N-2) days
		// under PRB1304264, so its "last 30 days" actually spans 29. v2
		// subtracts N from the beginning of tomorrow and gets 30. So the
		// stored rows will NOT match a v2 recomputation even for an
		// unchanged input — one more reason the existing table is not a
		// baseline to diff against.
		{Type: AvailabilityTypeLast7Days, Begin: startOfTomorrow.AddDate(0, 0, -7), End: startOfTomorrow, Rolling: true},
		{Type: AvailabilityTypeLast30Days, Begin: startOfTomorrow.AddDate(0, 0, -30), End: startOfTomorrow, Rolling: true},
		{Type: AvailabilityTypeLast90Days, Begin: startOfTomorrow.AddDate(0, 0, -90), End: startOfTomorrow, Rolling: true},
		{Type: AvailabilityTypeLast12Months, Begin: startOfTomorrow.AddDate(0, -12, 0), End: startOfTomorrow, Rolling: true},
	}
}

// weekdayOffset returns how many days back the start of the week is.
//
// *** MONDAY, NOT SUNDAY, AND THE DATA IS WHAT SETTLES IT. ***
// gs.beginningOfWeek follows the glide.ui.week_starts_on property, so it
// cannot be read off the API name — and the first draft of this file assumed
// Sunday, which would have shifted every weekly row by a day. The stored
// history says otherwise, at both ends of its four-year range:
//
//	oldest weekly row  2022-10-31 00:00:00Z  -> Monday
//	newest weekly row  2026-09-20 18:30:00Z  -> Monday 00:00 in Asia/Colombo
//
// Two boundaries, four years apart, under two different timezone
// conventions, both landing on Monday local midnight.
//
// Go puts Sunday at 0, so a Monday-based week needs the rotation below:
// Monday -> 0, Tuesday -> 1, ... Sunday -> 6.
// PreviousFixedSegments are the daily, weekly, monthly and annual periods
// that ended most recently before now: yesterday, last week, last month and
// last year.
//
// *** WITHOUT THESE A FIXED ROW FREEZES AT WHATEVER THE SWEEP SAW. ***
// AvailabilitySegmentsFor only yields the periods containing now, so a daily
// sweep writes today's row once, part-way through the day, and never touches
// it again: an outage later that day never reaches it, and the same is true
// of the last day of every week, month and year. ServiceNow does not have
// this hole. Measured on staging's copy of service_availability, its fixed
// rows are last written a median 15.5h after the period ENDS -- exactly the
// next 10:00 UTC run -- so each run finalises the period that just closed as
// well as starting the current one. Recomputing these every run is a
// superset of that (idempotent: rows are keyed on their own start), and also
// picks up an outage edited after the period closed.
func PreviousFixedSegments(now time.Time, loc *time.Location) []AvailabilitySegment {
	if loc == nil {
		loc = time.UTC
	}
	n := now.In(loc)
	startOfToday := time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, loc)
	startOfWeek := startOfToday.AddDate(0, 0, -weekdayOffset(startOfToday))
	startOfMonth := time.Date(n.Year(), n.Month(), 1, 0, 0, 0, 0, loc)
	startOfYear := time.Date(n.Year(), 1, 1, 0, 0, 0, 0, loc)
	return []AvailabilitySegment{
		{Type: AvailabilityTypeDaily, Begin: startOfToday.AddDate(0, 0, -1), End: startOfToday},
		{Type: AvailabilityTypeWeekly, Begin: startOfWeek.AddDate(0, 0, -7), End: startOfWeek},
		{Type: AvailabilityTypeMonthly, Begin: startOfMonth.AddDate(0, -1, 0), End: startOfMonth},
		{Type: AvailabilityTypeAnnually, Begin: startOfYear.AddDate(-1, 0, 0), End: startOfYear},
	}
}

func weekdayOffset(t time.Time) int {
	return (int(t.Weekday()) + 6) % 7
}
