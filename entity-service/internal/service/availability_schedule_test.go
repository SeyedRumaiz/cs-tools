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

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/repository"
)

func spanAt(h1, m1, h2, m2 int, repeat string) repository.ScheduleSpanRow {
	s := time.Date(2010, 1, 1, h1, m1, 0, 0, time.UTC)
	e := time.Date(2010, 1, 1, h2, m2, 0, 0, time.UTC)
	if h2 == 24 {
		e = time.Date(2010, 1, 2, 0, 0, 0, 0, time.UTC)
	}
	return repository.ScheduleSpanRow{StartOn: &s, EndOn: &e, RepeatType: repeat}
}

// The only schedule any commitment on the instance actually uses. It must
// collapse to AlwaysOn, both because that is exact and because the
// day-walking path is pointless for it.
func TestNewSpanSchedule_TwentyFourSevenCollapsesToAlwaysOn(t *testing.T) {
	got, err := NewSpanSchedule([]repository.ScheduleSpanRow{spanAt(0, 0, 24, 0, "daily")}, time.UTC)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := got.(AlwaysOn); !ok {
		t.Fatalf("got %T, want AlwaysOn — a full-day daily span IS 24x7", got)
	}
}

func TestNewSpanSchedule_NoSpansIsUnrestricted(t *testing.T) {
	got, err := NewSpanSchedule(nil, time.UTC)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := got.(AlwaysOn); !ok {
		t.Fatalf("got %T, want AlwaysOn", got)
	}
}

// *** THE REFUSALS ARE THE POINT OF THIS FILE. ***
// Each of these shapes would produce a plausible but wrong agreed service
// time, and a wrong AST publishes a wrong uptime percentage on a
// customer-facing page. Failing the subject leaves its previous row in
// place — visibly stale beats quietly incorrect.
func TestNewSpanSchedule_RefusesWhatItCannotModel(t *testing.T) {
	excl := spanAt(9, 0, 17, 0, "daily")
	excl.SpanType = "exclude"

	free := spanAt(9, 0, 17, 0, "daily")
	free.ShowAs = "Free"

	noEnd := spanAt(9, 0, 17, 0, "daily")
	noEnd.EndOn = nil

	cases := map[string]repository.ScheduleSpanRow{
		"an exclusion span would overstate AST":          excl,
		"a 'free' span is an exclusion by another name":  free,
		"a weekly repeat changes which days are covered": spanAt(9, 0, 17, 0, "weekly"),
		"a span with no end cannot be measured":          noEnd,
		"a span crossing midnight is not modelled":       spanAt(22, 0, 6, 0, "daily"),
	}

	for name, span := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := NewSpanSchedule([]repository.ScheduleSpanRow{span}, time.UTC); err == nil {
				t.Fatalf("accepted a span it cannot model; it would publish a wrong percentage")
			}
		})
	}
}

func TestSpanSchedule_BusinessHoursCoverage(t *testing.T) {
	sched, err := NewSpanSchedule([]repository.ScheduleSpanRow{spanAt(9, 0, 17, 0, "daily")}, time.UTC)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// A single whole day: 09:00-17:00 is eight hours.
	begin := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	end := begin.AddDate(0, 0, 1)
	if got := sched.Covered(begin, end); got != 8*time.Hour {
		t.Errorf("one day = %v, want 8h", got)
	}

	// Three days running: the window must repeat, not apply once.
	if got := sched.Covered(begin, begin.AddDate(0, 0, 3)); got != 24*time.Hour {
		t.Errorf("three days = %v, want 24h (8h x 3)", got)
	}

	// A partial overlap: 10:00-12:00 is inside the window entirely.
	if got := sched.Covered(begin.Add(10*time.Hour), begin.Add(12*time.Hour)); got != 2*time.Hour {
		t.Errorf("10:00-12:00 = %v, want 2h", got)
	}

	// Entirely outside the window.
	if got := sched.Covered(begin.Add(2*time.Hour), begin.Add(4*time.Hour)); got != 0 {
		t.Errorf("02:00-04:00 = %v, want 0", got)
	}

	// Straddling the window edge: 16:00-18:00 contributes only the first hour.
	if got := sched.Covered(begin.Add(16*time.Hour), begin.Add(18*time.Hour)); got != time.Hour {
		t.Errorf("16:00-18:00 = %v, want 1h", got)
	}
}

func TestSpanSchedule_TwoWindowsInOneDay(t *testing.T) {
	sched, err := NewSpanSchedule([]repository.ScheduleSpanRow{
		spanAt(9, 0, 12, 0, "daily"),
		spanAt(13, 0, 17, 0, "daily"),
	}, time.UTC)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	begin := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	if got := sched.Covered(begin, begin.AddDate(0, 0, 1)); got != 7*time.Hour {
		t.Errorf("split day = %v, want 7h (3h + 4h, lunch excluded)", got)
	}
}

// Stepping a day at a time must use AddDate, not Add(24h): across a DST
// transition a local day is 23 or 25 hours, and a fixed 24h step drifts the
// window off the clock by an hour for the rest of the period.
func TestSpanSchedule_SurvivesADSTTransition(t *testing.T) {
	loc, err := time.LoadLocation("Europe/London")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	sched, err := NewSpanSchedule([]repository.ScheduleSpanRow{spanAt(9, 0, 17, 0, "daily")}, time.UTC)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 25 October 2026 is the autumn clock change in the UK.
	begin := time.Date(2026, 10, 24, 0, 0, 0, 0, loc)
	end := time.Date(2026, 10, 27, 0, 0, 0, 0, loc)

	// Three local days, each with an 09:00-17:00 window, regardless of the
	// fact that one of them is 25 hours long.
	if got := sched.Covered(begin, end); got != 24*time.Hour {
		t.Errorf("three days across a DST change = %v, want 24h", got)
	}
}

// Windows are wall-clock in the schedule's zone. On a 25-hour day (London,
// clocks back on 2026-10-25) a 09:00-17:00 window must still sit at 09:00-17:00
// local -- midnight plus nine hours would put it at 08:00-16:00.
func TestSpanSchedule_WindowsStayOnWallClockAcrossDST(t *testing.T) {
	london, err := time.LoadLocation("Europe/London")
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	sched, err := NewSpanSchedule([]repository.ScheduleSpanRow{spanAt(9, 0, 17, 0, "daily")}, london)
	if err != nil {
		t.Fatal(err)
	}
	at := func(h int) time.Time { return time.Date(2026, 10, 25, h, 0, 0, 0, london) }
	if got := sched.Covered(at(8), at(9)); got != 0 {
		t.Errorf("08:00-09:00 local on the DST day covered %v, want 0", got)
	}
	if got := sched.Covered(at(16), at(17)); got != time.Hour {
		t.Errorf("16:00-17:00 local on the DST day covered %v, want 1h", got)
	}
	if got := sched.Covered(at(0), time.Date(2026, 10, 26, 0, 0, 0, 0, london)); got != 8*time.Hour {
		t.Errorf("whole DST day covered %v, want 8h", got)
	}
}

// The day walk is anchored in the schedule's zone, not in whatever zone the
// caller's times carry: the same instants passed in UTC or in Colombo give the
// same answer.
func TestSpanSchedule_AnchorsInScheduleZoneNotCallerZone(t *testing.T) {
	colombo, err := time.LoadLocation("Asia/Colombo")
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	sched, err := NewSpanSchedule([]repository.ScheduleSpanRow{spanAt(9, 0, 17, 0, "daily")}, colombo)
	if err != nil {
		t.Fatal(err)
	}
	// 09:00-10:00 in Colombo is inside the window; 03:30-04:30 UTC is the
	// same hour. A walk anchored on the arguments' (UTC) midnight would put the
	// window at 14:30-22:30 Colombo and count this hour as uncovered.
	nine := time.Date(2026, 10, 1, 9, 0, 0, 0, colombo)
	inColombo := sched.Covered(nine, nine.Add(time.Hour))
	inUTC := sched.Covered(nine.UTC(), nine.Add(time.Hour).UTC())
	if inColombo != time.Hour || inUTC != time.Hour {
		t.Errorf("09:00-10:00 Colombo covered %v (Colombo-zoned args) / %v (UTC-zoned args), want 1h both", inColombo, inUTC)
	}
}
