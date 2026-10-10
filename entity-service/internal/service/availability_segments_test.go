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

import (
	"testing"
	"time"
)

func segmentsByType(t *testing.T, now time.Time, loc *time.Location) map[string]AvailabilitySegment {
	t.Helper()
	out := map[string]AvailabilitySegment{}
	for _, s := range AvailabilitySegmentsFor(now, loc) {
		out[s.Type] = s
	}
	return out
}

// *** THE WEEK STARTS ON MONDAY, AND THIS TEST IS WHY THAT IS NOT A GUESS. ***
// gs.beginningOfWeek follows a ServiceNow property, so the API name proves
// nothing; the first draft of the segment builder assumed Sunday and would
// have shifted every weekly row by a day. The two dates below are the oldest
// and newest weekly rows actually stored on the instance, and both land on
// Monday local midnight.
func TestAvailabilitySegments_WeekStartsMonday(t *testing.T) {
	// Thursday 2026-10-01 -> the week began Monday the 28th.
	segs := segmentsByType(t, time.Date(2026, 10, 1, 13, 0, 0, 0, time.UTC), time.UTC)
	w := segs[AvailabilityTypeWeekly]

	if got := w.Begin.Weekday(); got != time.Monday {
		t.Fatalf("weekly segment begins on %v, want Monday", got)
	}
	want := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	if !w.Begin.Equal(want) {
		t.Errorf("weekly begin = %v, want %v", w.Begin, want)
	}
	if !w.End.Equal(want.AddDate(0, 0, 7)) {
		t.Errorf("weekly end = %v, want %v", w.End, want.AddDate(0, 0, 7))
	}
}

// A Sunday is the case a Sunday-based implementation gets wrong by six days
// rather than one, so it is worth its own assertion.
func TestAvailabilitySegments_WeekOnASunday(t *testing.T) {
	segs := segmentsByType(t, time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC), time.UTC)
	w := segs[AvailabilityTypeWeekly]
	want := time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC) // the Monday before
	if !w.Begin.Equal(want) {
		t.Fatalf("weekly begin on a Sunday = %v, want %v (the preceding Monday)", w.Begin, want)
	}
}

// Rolling windows are measured back from the START OF TOMORROW, which is how
// ServiceNow makes them include the whole of today. Measuring from the start
// of today instead gives a window one day short and ending in the past.
func TestAvailabilitySegments_RollingWindowsIncludeAllOfToday(t *testing.T) {
	now := time.Date(2026, 10, 1, 13, 45, 0, 0, time.UTC)
	tomorrow := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	segs := segmentsByType(t, now, time.UTC)

	cases := []struct {
		typ   string
		begin time.Time
	}{
		{AvailabilityTypeLast7Days, tomorrow.AddDate(0, 0, -7)},
		{AvailabilityTypeLast30Days, tomorrow.AddDate(0, 0, -30)},
		{AvailabilityTypeLast90Days, tomorrow.AddDate(0, 0, -90)},
		{AvailabilityTypeLast12Months, tomorrow.AddDate(0, -12, 0)},
	}
	for _, c := range cases {
		s, ok := segs[c.typ]
		if !ok {
			t.Errorf("%s was not emitted", c.typ)
			continue
		}
		if !s.End.Equal(tomorrow) {
			t.Errorf("%s ends %v, want start of tomorrow %v", c.typ, s.End, tomorrow)
		}
		if !s.Begin.Equal(c.begin) {
			t.Errorf("%s begins %v, want %v", c.typ, s.Begin, c.begin)
		}
		if !s.Rolling {
			t.Errorf("%s is not marked rolling; it would be upserted and accumulate a row a day", c.typ)
		}
	}
}

// *** v2's LAST_30_DAYS IS 30 DAYS. v1's WAS 29. ***
// The legacy summarizer subtracts (N-2) days from the beginning of today
// under PRB1304264. Asserting the full span here is what stops somebody
// "fixing" this to match the stored rows, which were written by v1.
func TestAvailabilitySegments_RollingWindowsAreFullLength(t *testing.T) {
	segs := segmentsByType(t, time.Date(2026, 10, 1, 13, 0, 0, 0, time.UTC), time.UTC)
	for typ, wantDays := range map[string]int{
		AvailabilityTypeLast7Days:  7,
		AvailabilityTypeLast30Days: 30,
		AvailabilityTypeLast90Days: 90,
	} {
		s := segs[typ]
		if got := int(s.End.Sub(s.Begin).Hours() / 24); got != wantDays {
			t.Errorf("%s spans %d days, want %d (v1's off-by-one is not the target)", typ, got, wantDays)
		}
	}
}

// LAST_90_DAYS is not a v2 type, and both live dashboard endpoints read it.
// Dropping it would remove the "Last 90 days" figure from the status page
// silently — no rows, no error.
func TestAvailabilitySegments_Last90DaysIsEmittedAndLast1DaysIsNot(t *testing.T) {
	segs := segmentsByType(t, time.Date(2026, 10, 1, 13, 0, 0, 0, time.UTC), time.UTC)

	if _, ok := segs[AvailabilityTypeLast90Days]; !ok {
		t.Error("LAST_90_DAYS not emitted; /monitors and /availabilities both read it")
	}
	if _, ok := segs["LAST_1_DAYS"]; ok {
		t.Error("LAST_1_DAYS emitted; nothing reads it and v2 does not define it")
	}
}

// The zone is the commitment's, not UTC. This is the boundary shift visible
// in the stored history: daily rows start 00:00:00 in 2022 and 18:30:00 in
// 2026, and 18:30Z is midnight in Asia/Colombo.
func TestAvailabilitySegments_BoundariesResolveInTheGivenZone(t *testing.T) {
	colombo, err := time.LoadLocation("Asia/Colombo")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	// 19:00 UTC on 1 Oct is already 00:30 on 2 Oct in Colombo, so "today"
	// differs between the two zones — exactly the case that catches a
	// UTC-hardcoded implementation.
	now := time.Date(2026, 10, 1, 19, 0, 0, 0, time.UTC)

	daily := segmentsByType(t, now, colombo)[AvailabilityTypeDaily]
	if got := daily.Begin.UTC(); !got.Equal(time.Date(2026, 10, 1, 18, 30, 0, 0, time.UTC)) {
		t.Errorf("daily begin = %v UTC, want 2026-10-01 18:30 UTC (midnight in Colombo)", got)
	}

	dailyUTC := segmentsByType(t, now, time.UTC)[AvailabilityTypeDaily]
	if dailyUTC.Begin.Equal(daily.Begin) {
		t.Error("the zone made no difference; boundaries are being resolved in UTC regardless")
	}
}

func TestAvailabilitySegments_FixedPeriodsAreNotRolling(t *testing.T) {
	segs := segmentsByType(t, time.Date(2026, 10, 1, 13, 0, 0, 0, time.UTC), time.UTC)
	for _, typ := range []string{
		AvailabilityTypeDaily, AvailabilityTypeWeekly,
		AvailabilityTypeMonthly, AvailabilityTypeAnnually,
	} {
		if segs[typ].Rolling {
			t.Errorf("%s is marked rolling; it would be deleted and rewritten, losing history", typ)
		}
	}
}

func TestAvailabilitySegments_MonthAndYearBoundaries(t *testing.T) {
	segs := segmentsByType(t, time.Date(2026, 10, 15, 6, 0, 0, 0, time.UTC), time.UTC)

	m := segs[AvailabilityTypeMonthly]
	if !m.Begin.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)) ||
		!m.End.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("monthly = [%v, %v), want all of October", m.Begin, m.End)
	}

	y := segs[AvailabilityTypeAnnually]
	if !y.Begin.Equal(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) ||
		!y.End.Equal(time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("annually = [%v, %v), want all of 2026", y.Begin, y.End)
	}
}

// The just-closed fixed periods, which the sweep rewrites alongside the
// current ones so a period's last hours are never lost.
func TestAvailabilitySegments_PreviousFixedPeriods(t *testing.T) {
	colombo, err := time.LoadLocation("Asia/Colombo")
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	// Thursday 2026-10-01 08:30 Colombo (03:00 UTC, when the sub-cron runs).
	now := time.Date(2026, 10, 1, 3, 0, 0, 0, time.UTC)
	got := map[string]AvailabilitySegment{}
	for _, s := range PreviousFixedSegments(now, colombo) {
		if s.Rolling {
			t.Errorf("%s: a closed fixed period must not be rolling", s.Type)
		}
		got[s.Type] = s
	}
	want := map[string][2]time.Time{
		AvailabilityTypeDaily:    {time.Date(2026, 9, 30, 0, 0, 0, 0, colombo), time.Date(2026, 10, 1, 0, 0, 0, 0, colombo)},
		AvailabilityTypeWeekly:   {time.Date(2026, 9, 21, 0, 0, 0, 0, colombo), time.Date(2026, 9, 28, 0, 0, 0, 0, colombo)},
		AvailabilityTypeMonthly:  {time.Date(2026, 9, 1, 0, 0, 0, 0, colombo), time.Date(2026, 10, 1, 0, 0, 0, 0, colombo)},
		AvailabilityTypeAnnually: {time.Date(2025, 1, 1, 0, 0, 0, 0, colombo), time.Date(2026, 1, 1, 0, 0, 0, 0, colombo)},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d previous segments, want %d", len(got), len(want))
	}
	for typ, w := range want {
		s := got[typ]
		if !s.Begin.Equal(w[0]) || !s.End.Equal(w[1]) {
			t.Errorf("%s = [%v, %v), want [%v, %v)", typ, s.Begin, s.End, w[0], w[1])
		}
	}
	// Each closed period must end exactly where the current one begins:
	// no gap for an outage to fall into, no overlap to count it twice.
	current := segmentsByType(t, now, colombo)
	for typ := range want {
		if !got[typ].End.Equal(current[typ].Begin) {
			t.Errorf("%s: previous ends %v but current begins %v", typ, got[typ].End, current[typ].Begin)
		}
	}
}

func TestRoundAvailability(t *testing.T) {
	cases := map[float64]float64{
		99.99945987654321: 99.99946, // the asgardeo value staging showed unrounded on /monitors
		99.99981995884774: 99.99982,
		98.92975963977676: 98.92976,
		100:               100,
		0:                 0,
	}
	for in, want := range cases {
		if got := roundAvailability(in); got != want {
			t.Errorf("roundAvailability(%v) = %v, want %v", in, got, want)
		}
	}
}
