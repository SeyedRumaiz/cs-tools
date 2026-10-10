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

// Availability arithmetic, ported from ServiceNow's AvailabilityCalculatorV2.
//
// *** THE TARGET IS v2's INTENT, NOT v2's CODE. ***
// `com.snc.availability.v2` is false on both instances, so every stored row
// was written by the LEGACY calculator. The brief was to use the latest and
// best approach available, which means v2's design where it is sound and a
// correction where it is not — not bug-for-bug fidelity.
//
// Four places where this deliberately does better than v2, each because
// copying v2 would produce a worse number or no number at all:
//
//  1. *** v2's processOutages CRASHES ON TWO OR MORE OUTAGES. *** Its guard
//     reads `if (nextOutageIndex > outages.length)` where it needs `>=`, so
//     the last iteration dereferences outages[length] — undefined — and
//     throws. calculate() swallows it, and the subject gets NO rows at all.
//     Reproducing that would publish nothing for exactly the services that
//     had the most incidents. (It is also the likeliest reason neither
//     instance has switched v2 on.)
//
//  2. LAST_90_DAYS is not a v2 period type, and both dashboard endpoints
//     publish a "Last 90 days" figure. Emitted — see availability_segments.
//
//  3. Rolling windows run their full length. v1 spans N-1 days under
//     PRB1304264; "last 30 days" should cover 30.
//
//  4. absolute_availability is guarded against a zero-length period. v2
//     guards the scheduled arm and not this one, yielding NaN.
//
// MTBF over a period with no failures stays 0, which is v2's answer. v1
// substitutes a denominator of 1 and reports the whole period, turning an
// undefined quantity into a number that reads like a measurement. Nothing
// consumes the column; 0 is the honest sentinel.
//
// Because the stored rows are v1 output, they are not a line-by-line
// baseline — but they agree with this on everything that matters, which
// availability_golden_test demonstrates over 136 real cases.
//
// Everything below was read out of the V2 source rather than the product
// docs, because the docs describe behaviour the code does not have. The
// three places that matter most:
//
//  1. *** A PLANNED OUTAGE CONTRIBUTES EXACTLY ZERO. *** It is fetched by
//     the query and passed through segmentation, then skipped by a
//     `type === 'outage'` test in the accumulator. It does NOT reduce agreed
//     service time, which is what the field names and the docs both suggest.
//     AST shrinks only via a separate `maintenance_window` commitment folded
//     into the schedule — of which there are currently none configured.
//
//  2. *** AST IS MEASURED, NOT COMPUTED. *** ServiceNow does not ask the
//     schedule how long it covers. It builds a synthetic outage spanning the
//     whole period and runs it through the same accumulator with the
//     schedule applied. Reproduced the same way here: it is the only thing
//     that guarantees AST and the downtime it is compared against round
//     identically.
//
//  3. *** COUNT AND DURATION MOVE TOGETHER. *** `if (duration > 0)
//     totalOutages++` — so an outage lying entirely outside the schedule
//     scores zero seconds and is not counted either. Counting it would
//     change MTBF and MTRS, which divide by it.
package service

import (
	"time"
)

// Outage type values, as ServiceNow stores them on cmdb_ci_outage.type.
//
// DEGRADATION is deliberately absent. It is a real value on the instance —
// 115 of 635 outages carry it — and it is excluded TWICE over by V2: the
// query asks only for outage/planned, and the accumulator then counts only
// outage. There is no path by which a degradation affects a percentage, so
// a constant for it here would imply an option the engine does not have.
const (
	OutageTypeOutage  = "outage"
	OutageTypePlanned = "planned"
)

// AvailabilityOutage is one outage as the calculator sees it: an interval and
// a type, and nothing else. The caller is responsible for having applied V2's
// selection rules before handing them over (see AvailabilityInputs.Outages).
type AvailabilityOutage struct {
	Begin time.Time
	End   time.Time
	Type  string
}

// AvailabilitySchedule reports how much of an interval a commitment's
// schedule covers. Implemented by scheduleSpans for a real cmn_schedule, and
// by AlwaysOn for the 24x7 case.
//
// Modelled as an interface because every commitment on the instance today
// points at the stock "24 x 7" schedule, so the only implementation
// exercised in production is the trivial one — and a port that hard-coded
// 24x7 on that basis would be silently wrong the first time somebody
// configured a business-hours commitment.
type AvailabilitySchedule interface {
	// Covered returns how much of [begin, end) the schedule includes.
	Covered(begin, end time.Time) time.Duration
}

// AlwaysOn is a 24x7 schedule: every second counts.
//
// This is what all seven commitments on the instance resolve to today — they
// reference the stock "24 x 7" schedule record, which is why every stored
// row has ast exactly 24h. Note the distinction from a NIL schedule, which
// V2 also treats as unrestricted: both give the same answer, and both are
// represented by this type so the rest of the code never nil-checks.
type AlwaysOn struct{}

// Covered implements AvailabilitySchedule.
func (AlwaysOn) Covered(begin, end time.Time) time.Duration {
	if !end.After(begin) {
		return 0
	}
	return end.Sub(begin)
}

// AvailabilityInputs is one (subject, commitment, period) calculation.
type AvailabilityInputs struct {
	// Begin and End bound the period. Half-open: [Begin, End).
	Begin time.Time
	End   time.Time

	// Outages overlapping the period, ALREADY filtered to the types V2
	// selects (outage and planned) and to those with both a begin and an
	// end. An outage still open has no end and is excluded by V2 outright —
	// not counted up to "now", not counted partially. 70 of the 635 outages
	// on the instance are in that state, some open since 2021.
	Outages []AvailabilityOutage

	// Schedule is the commitment's schedule. Nil means unrestricted, which
	// is what V2 does when service_commitment.schedule is empty.
	Schedule AvailabilitySchedule

	// TargetPercent is service_commitment.availability — the field is called
	// `availability`, NOT `availability_target`. All seven commitments on the
	// instance carry 100, which makes AllowedDowntime zero and
	// CommitmentMet false the instant any downtime is recorded.
	TargetPercent float64
}

// AvailabilityResult mirrors the columns of one service_availability row.
type AvailabilityResult struct {
	AbsoluteDowntime  time.Duration
	ScheduledDowntime time.Duration

	// ScheduledTotal is AST — agreed service time.
	ScheduledTotal time.Duration

	AbsoluteAvailability  float64
	ScheduledAvailability float64

	AbsoluteCount  int
	ScheduledCount int

	MTBF time.Duration
	MTRS time.Duration

	AllowedDowntime time.Duration
	CommitmentMet   bool
}

// CalculateAvailability is the port of
// AvailabilityCalculatorV2._generateAvailabilityValuesFor.
func CalculateAvailability(in AvailabilityInputs) AvailabilityResult {
	schedule := in.Schedule
	if schedule == nil {
		schedule = AlwaysOn{}
	}

	// *** THE PROCESSOR RUNS FIRST, AND SKIPPING IT WAS THIS PORT'S WORST
	// BUG. *** V2 calls processOutages() before segmentation, and it does
	// two things the accumulator cannot: it MERGES overlapping outages
	// rather than summing them, and it SUBTRACTS planned windows from the
	// real outages they overlap. Without it, two overlapping two-hour
	// outages read as four hours of downtime instead of three, and planned
	// maintenance excuses nothing.
	//
	// What comes back is already trimmed to the period, already disjoint,
	// and contains OUTAGE entries only.
	outages := processAvailabilityOutages(in.Outages, in.Begin, in.End)

	absDown, absCount := accumulateOutages(outages, nil, in.Begin, in.End)
	schedDown, schedCount := accumulateOutages(outages, schedule, in.Begin, in.End)

	// The period's own length, measured through the same accumulator. See
	// point 2 in the package comment: this is how V2 derives both totals,
	// and reproducing it any other way risks the two disagreeing at the
	// boundary.
	wholePeriod := []AvailabilityOutage{{Begin: in.Begin, End: in.End, Type: OutageTypeOutage}}
	absTotal, _ := accumulateOutages(wholePeriod, nil, time.Time{}, time.Time{})
	schedTotal, _ := accumulateOutages(wholePeriod, schedule, time.Time{}, time.Time{})

	res := AvailabilityResult{
		AbsoluteDowntime:  absDown,
		ScheduledDowntime: schedDown,
		ScheduledTotal:    schedTotal,
		AbsoluteCount:     absCount,
		ScheduledCount:    schedCount,
	}

	// *** THE GUARD IS ON THE SCHEDULED SIDE ONLY, AND THAT ASYMMETRY IS
	// ServiceNow's, NOT A TRANSCRIPTION SLIP. *** V2 writes
	//     var scheduledAvailabilityTotal = 100;
	//     if (changedScheduledOutageDataDuration != 0) ...
	// and has no equivalent for the absolute one, which yields NaN on a
	// zero-length period. A zero-length period cannot occur for any
	// registered segment type, so rather than propagate a NaN this returns
	// a zeroed result — the one deliberate divergence in this file, and it
	// is a divergence from a bug.
	if absTotal <= 0 {
		return res
	}

	res.AbsoluteAvailability = 100 * float64(absTotal-absDown) / float64(absTotal)

	res.ScheduledAvailability = 100
	if schedTotal > 0 {
		res.ScheduledAvailability = 100 * float64(schedTotal-schedDown) / float64(schedTotal)
	}

	// Mean Time Between Failures and Mean Time to Restore Service. Both
	// divide by the SCHEDULED count — not the absolute one — and both stay
	// zero when nothing was counted, exactly as V2 leaves them.
	if schedCount > 0 {
		res.MTBF = (schedTotal - schedDown) / time.Duration(schedCount)
		res.MTRS = schedDown / time.Duration(schedCount)
	}

	// allowedDowntime = scheduledTotal * ((100 - target) / 100)
	res.AllowedDowntime = time.Duration(float64(schedTotal) * ((100 - in.TargetPercent) / 100))
	res.CommitmentMet = res.ScheduledDowntime <= res.AllowedDowntime

	return res
}

// accumulateOutages is the port of AvailabilityCalculatorV2._addOutages.
//
// A zero begin/end disables trimming, which is how V2 measures the period
// itself (it calls _addOutages with no boundaries at all).
func accumulateOutages(
	outages []AvailabilityOutage,
	schedule AvailabilitySchedule,
	begin, end time.Time,
) (time.Duration, int) {
	var total time.Duration
	var count int

	for _, o := range outages {
		o = trimToPeriod(o, begin, end)

		// *** ONLY `outage` CONTRIBUTES. *** A planned outage reaches this
		// line and leaves with duration zero. See point 1 in the package
		// comment — this single condition is the whole of ServiceNow's
		// planned-outage handling, and it is the opposite of what the
		// product documentation describes.
		var d time.Duration
		if o.Type == OutageTypeOutage && o.End.After(o.Begin) {
			if schedule != nil {
				d = schedule.Covered(o.Begin, o.End)
			} else {
				d = o.End.Sub(o.Begin)
			}
		}

		// Counted only when it contributed time. An outage wholly outside
		// the schedule scores zero and is not a failure for MTBF purposes.
		if d > 0 {
			count++
		}
		total += d
	}

	return total, count
}

// trimToPeriod clips an outage to the period so an outage spanning two days
// contributes its overlap to each day separately rather than its whole length
// to both.
func trimToPeriod(o AvailabilityOutage, begin, end time.Time) AvailabilityOutage {
	if begin.IsZero() || end.IsZero() {
		return o
	}
	if o.Begin.Before(begin) {
		o.Begin = begin
	}
	if o.End.After(end) {
		o.End = end
	}
	return o
}
