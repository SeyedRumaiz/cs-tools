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
	"sort"
	"time"
)

// Overlap resolution, ported from AvailabilityOutageProcessor.
//
// *** THIS FILE EXISTS BECAUSE THE FIRST VERSION OF THIS PORT WAS WRONG. ***
// AvailabilityCalculatorV2 calls processOutages() BEFORE anything else
// touches the outages, and the first implementation skipped it on the
// assumption that the accumulator was the whole story. It is not, and the
// two things this step does are both load-bearing:
//
//  1. *** OVERLAPPING OUTAGES ARE MERGED, NOT SUMMED. *** Two outages
//     10:00-12:00 and 11:00-13:00 are three hours of downtime, not four.
//     Summing them overstates downtime, and it does so exactly when a
//     service is worst affected — several overlapping incidents is what a
//     bad day looks like.
//
//  2. *** A PLANNED OUTAGE CUTS INTO A REAL ONE. *** This is the correction
//     that matters most. An earlier commit message, PR description and spec
//     all claimed "a planned outage contributes exactly zero and changes
//     nothing". That is true of the accumulator in isolation and FALSE of
//     the system: processOutages subtracts planned windows from overlapping
//     outages first — splitting one in two if the planned window sits in
//     the middle — and only then discards the planned entries. So planned
//     maintenance really does excuse downtime that happens inside it.
//
// ServiceNow implements this as a single pass with index juggling,
// insertions and deletions mid-loop. Reproduced here as explicit interval
// arithmetic instead: same result, and the index arithmetic in the original
// is where its own comments admit uncertainty ("CANT HAPPEN SINCE...").

// processAvailabilityOutages resolves overlaps and applies planned windows,
// returning disjoint OUTAGE intervals ordered by start.
//
// begin and end bound the period; outages are trimmed to it first, matching
// _modifyOutageBoundaries. A zero-length result is dropped.
func processAvailabilityOutages(outages []AvailabilityOutage, begin, end time.Time) []AvailabilityOutage {
	var real, planned []AvailabilityOutage

	for _, o := range outages {
		o = trimToPeriod(o, begin, end)
		if !o.End.After(o.Begin) {
			continue
		}
		switch o.Type {
		case OutageTypeOutage:
			real = append(real, o)
		case OutageTypePlanned:
			planned = append(planned, o)
		}
		// Anything else -- DEGRADATION above all -- is dropped here rather
		// than carried and ignored later. ServiceNow never fetches it in
		// the first place.
	}

	// Merge each type into maximal disjoint windows.
	//
	// Merging `real` is LOAD-BEARING -- it is the difference between three
	// hours and four for two overlapping two-hour outages.
	//
	// Merging `planned` is NOT, and the comment here used to claim it was.
	// A mutation that removed it survived the whole suite, which is the
	// honest test of the claim: subtracting [11,13) and then [13,15) from
	// [10,16) gives the same answer as subtracting the merged [11,15),
	// because interval subtraction composes. ServiceNow needs its
	// _mergeLongestPossiblePlannedOutage pass because it is juggling array
	// indices mid-loop, not because the arithmetic requires it. Kept
	// anyway: it mirrors the source, it is cheap, and it keeps the
	// subtraction loop short.
	planned = mergeIntervals(planned)
	real = mergeIntervals(real)

	// Subtract the planned windows. This is the split/trim/delete the
	// original spells out as four cases; as interval arithmetic it is one.
	for _, p := range planned {
		real = subtractInterval(real, p)
	}

	return real
}

// mergeIntervals coalesces overlapping and touching intervals of one type.
//
// Touching counts as overlapping: ServiceNow's test is
// `nextOutage.begin <= currentOutage.end`, so an outage ending at 12:00 and
// one starting at 12:00 become a single interval rather than two. That
// matters for the COUNT as much as the duration — two becomes one, and
// MTBF and MTRS divide by it.
func mergeIntervals(in []AvailabilityOutage) []AvailabilityOutage {
	if len(in) < 2 {
		return in
	}
	sorted := make([]AvailabilityOutage, len(in))
	copy(sorted, in)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Begin.Before(sorted[j].Begin) })

	out := []AvailabilityOutage{sorted[0]}
	for _, cur := range sorted[1:] {
		last := &out[len(out)-1]
		if !cur.Begin.After(last.End) {
			if cur.End.After(last.End) {
				last.End = cur.End
			}
			continue
		}
		out = append(out, cur)
	}
	return out
}

// subtractInterval removes a planned window from a set of outage intervals.
func subtractInterval(in []AvailabilityOutage, cut AvailabilityOutage) []AvailabilityOutage {
	var out []AvailabilityOutage
	for _, o := range in {
		// No overlap: kept whole.
		if !cut.Begin.Before(o.End) || !o.Begin.Before(cut.End) {
			out = append(out, o)
			continue
		}
		// Case 4 / case 1 lower half: a piece survives before the window.
		if o.Begin.Before(cut.Begin) {
			out = append(out, AvailabilityOutage{Begin: o.Begin, End: cut.Begin, Type: o.Type})
		}
		// Case 3 / case 1 upper half: a piece survives after it.
		if cut.End.Before(o.End) {
			out = append(out, AvailabilityOutage{Begin: cut.End, End: o.End, Type: o.Type})
		}
		// Case 2 -- wholly inside the planned window -- adds neither, which
		// is the deletion.
	}
	return out
}
