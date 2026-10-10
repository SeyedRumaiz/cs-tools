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
	"fmt"
	"strings"
	"time"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/repository"
)

// Agreed service time from a mirrored cmn_schedule.
//
// *** THIS IS DELIBERATELY NOT A GLIDESCHEDULE PORT. ***
// GlideSchedule supports excluded spans, child schedules, span-level
// timezones and several recurrence vocabularies. Reimplementing all of that
// from the outside, with no instance to check against, would produce
// something that looks right and is wrong in cases nobody enumerated — and
// the output is a percentage on a customer-facing status page, where "wrong
// but plausible" is the worst possible failure.
//
// So this supports exactly the shapes the live data actually uses, and
// REFUSES anything else. A subject whose schedule it cannot model fails,
// gets logged, and keeps its previous row — which is visibly stale rather
// than quietly incorrect.
//
// What the live data is: all seven commitments on the instance reference one
// schedule, the stock "24 x 7". That degenerates to AlwaysOn, and is
// recognised as such below rather than being evaluated span by span.

// SpanSchedule covers the intervals its spans describe.
type SpanSchedule struct {
	// windows are the daily covered ranges as wall-clock offsets from local
	// midnight. A 24x7 schedule is one window of [0, 24h).
	windows []dayWindow
	// loc is the zone the spans' wall-clock times are in. Every day walk and
	// window boundary is built in it, whatever zone the caller's times carry.
	loc *time.Location
}

type dayWindow struct {
	start time.Duration // from local midnight
	end   time.Duration
}

// NewSpanSchedule builds a schedule from mirrored spans, or reports that the
// spans describe something it will not guess at.
func NewSpanSchedule(spans []repository.ScheduleSpanRow, loc *time.Location) (AvailabilitySchedule, error) {
	if loc == nil {
		loc = time.UTC
	}
	// No spans at all is an unrestricted schedule, which is what
	// ServiceNow does with an empty cmn_schedule.
	if len(spans) == 0 {
		return AlwaysOn{}, nil
	}

	var windows []dayWindow
	for _, sp := range spans {
		// *** AN EXCLUSION CHANGES THE ANSWER AND IS NOT MODELLED. ***
		// GlideSchedule subtracts excluded spans from the covered time.
		// Treating one as ordinary coverage would OVERSTATE agreed service
		// time, which understates downtime as a fraction and publishes an
		// availability figure that is too high.
		if isExclusion(sp) {
			return nil, fmt.Errorf("availability: schedule has an exclusion span (%q/%q), which is not modelled",
				sp.SpanType, sp.ShowAs)
		}

		repeat := strings.ToLower(strings.TrimSpace(sp.RepeatType))
		if repeat != "daily" && repeat != "" {
			// Weekly, monthly, yearly and the rest change which DAYS are
			// covered, not just which hours. Refused for the same reason.
			return nil, fmt.Errorf("availability: schedule repeat type %q is not modelled", sp.RepeatType)
		}
		if sp.StartOn == nil || sp.EndOn == nil {
			return nil, fmt.Errorf("availability: schedule span has no start or end")
		}

		start := sinceMidnight(*sp.StartOn)
		end := sinceMidnight(*sp.EndOn)
		// A span ending at exactly midnight is stored as the next day's
		// 00:00 and means "to the end of the day", not "zero length".
		if end == 0 && sp.EndOn.After(*sp.StartOn) {
			end = 24 * time.Hour
		}
		if end <= start {
			return nil, fmt.Errorf("availability: schedule span crosses midnight, which is not modelled")
		}
		windows = append(windows, dayWindow{start: start, end: end})
	}

	// A single window covering the whole day IS 24x7 — the case every
	// commitment on the instance actually uses. Collapsing it means the
	// common path runs exact arithmetic instead of a day-by-day walk.
	if len(windows) == 1 && windows[0].start == 0 && windows[0].end == 24*time.Hour {
		return AlwaysOn{}, nil
	}

	return &SpanSchedule{windows: windows, loc: loc}, nil
}

// Covered implements AvailabilitySchedule by walking the period a day at a
// time and intersecting each day with every window.
func (s *SpanSchedule) Covered(begin, end time.Time) time.Duration {
	if !end.After(begin) {
		return 0
	}

	var total time.Duration
	// Walk days in the schedule's own zone. The caller's times may carry any
	// location (pgx returns timestamptz in the process zone, usually UTC), so
	// anchoring on begin.Location() would start the walk at the wrong midnight.
	local := begin.In(s.loc)
	day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, s.loc)

	for day.Before(end) {
		for _, w := range s.windows {
			// Wall-clock boundaries via time.Date, not midnight plus a
			// duration: on a daylight-saving change day midnight+9h is 08:00
			// or 10:00 local, not 09:00.
			ws := wallClock(day, w.start, s.loc)
			we := wallClock(day, w.end, s.loc)
			if ws.Before(begin) {
				ws = begin
			}
			if we.After(end) {
				we = end
			}
			if we.After(ws) {
				total += we.Sub(ws)
			}
		}
		// AddDate rather than Add(24h): across a DST transition a day is not
		// 24 hours, and stepping by a fixed 24h would drift the window.
		day = time.Date(day.Year(), day.Month(), day.Day()+1, 0, 0, 0, 0, s.loc)
	}
	return total
}

// wallClock is the instant at which the clock in loc reads `offset` past the
// start of day's date. An offset of 24h is the next day's midnight (time.Date
// normalises it).
func wallClock(day time.Time, offset time.Duration, loc *time.Location) time.Time {
	h := int(offset / time.Hour)
	m := int((offset % time.Hour) / time.Minute)
	sec := int((offset % time.Minute) / time.Second)
	return time.Date(day.Year(), day.Month(), day.Day(), h, m, sec, 0, loc)
}

func sinceMidnight(t time.Time) time.Duration {
	return time.Duration(t.Hour())*time.Hour +
		time.Duration(t.Minute())*time.Minute +
		time.Duration(t.Second())*time.Second
}

// isExclusion reports whether a span removes time rather than adding it.
// ServiceNow spells this two ways depending on the release, so both the span
// type and the show_as value are checked.
func isExclusion(sp repository.ScheduleSpanRow) bool {
	t := strings.ToLower(sp.SpanType)
	a := strings.ToLower(sp.ShowAs)
	return strings.Contains(t, "exclude") || strings.Contains(a, "exclude") ||
		strings.Contains(t, "exception") || strings.Contains(a, "free")
}
