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
	"math/rand"
	"testing"
	"time"
)

// Differential testing against a brute-force oracle.
//
// *** THIS EXISTS TO CLOSE A GAP THE GOLDEN DATA CANNOT. ***
// Not one of the 136 real ServiceNow cases contains a planned outage —
// measured, not assumed: deleting the planned-subtraction step leaves every
// one of them passing. So the behaviour I got WRONG on the first attempt,
// and corrected only after reading AvailabilityOutageProcessor, is covered
// by nothing but tests I wrote from reading that same source. Tests written
// from a reading cannot catch a misreading.
//
// Waiting for ServiceNow to grow a planned outage is not the only option.
// The semantics are simple enough to state independently:
//
//	a second is DOWN if some outage-type interval covers it
//	AND no planned-type interval covers it.
//
// markSecondBySecond below does exactly that, with no intervals, no
// merging, no sorting — a flat array of booleans. It is obviously correct
// in a way the interval arithmetic is not, and it shares no code with it.
// Agreement across thousands of random cases is strong evidence that the
// clever implementation is right.
//
// *** AND IT IS WHY THE PORT DOES NOT COPY v2 EXACTLY. ***
// ServiceNow's own processOutages throws on any subject with two or more
// outages in a period: its guard reads
//
//	if (nextOutageIndex > outages.length)   // needs >=
//
// so on the last iteration it dereferences outages[length], which is
// undefined. calculate() swallows the exception, and the subject silently
// gets NO availability rows. Reproducing that faithfully would mean
// publishing nothing for exactly the subjects that had the most incidents.
// The port implements v2's INTENT; this test is what stands in for the
// behaviour of a v2 that worked.

// markSecondBySecond is the oracle: a flat per-second simulation.
func markSecondBySecond(outages []AvailabilityOutage, begin, end time.Time) (down time.Duration, intervals int) {
	total := int(end.Sub(begin) / time.Second)
	if total <= 0 {
		return 0, 0
	}
	marked := make([]bool, total)

	for _, o := range outages {
		if o.Type != OutageTypeOutage {
			continue
		}
		for s := clampIndex(o.Begin, begin, total); s < clampIndex(o.End, begin, total); s++ {
			marked[s] = true
		}
	}
	// Planned windows erase, and they erase AFTER every outage is laid
	// down — the order matters, and getting it backwards is the mistake
	// this oracle exists to rule out.
	for _, o := range outages {
		if o.Type != OutageTypePlanned {
			continue
		}
		for s := clampIndex(o.Begin, begin, total); s < clampIndex(o.End, begin, total); s++ {
			marked[s] = false
		}
	}

	var run bool
	for _, m := range marked {
		if m {
			down += time.Second
			if !run {
				intervals++
			}
		}
		run = m
	}
	return down, intervals
}

func clampIndex(t, begin time.Time, total int) int {
	s := int(t.Sub(begin) / time.Second)
	if s < 0 {
		return 0
	}
	if s > total {
		return total
	}
	return s
}

func TestCalculateAvailability_MatchesBruteForceOracle(t *testing.T) {
	begin := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	const periodSecs = 6 * 3600 // six hours, so the oracle stays cheap
	end := begin.Add(periodSecs * time.Second)

	rng := rand.New(rand.NewSource(20261002))

	for iteration := 0; iteration < 3000; iteration++ {
		n := rng.Intn(6) // 0..5 outages, including the empty case
		outages := make([]AvailabilityOutage, 0, n)
		for i := 0; i < n; i++ {
			// Deliberately allowed to start before and end after the
			// period, so trimming is exercised alongside everything else.
			start := rng.Intn(periodSecs+1200) - 600
			length := 1 + rng.Intn(3600)
			typ := OutageTypeOutage
			// Roughly one in three planned: frequent enough that overlaps,
			// nesting and abutment all occur many times across 3000 runs.
			if rng.Intn(3) == 0 {
				typ = OutageTypePlanned
			}
			outages = append(outages, AvailabilityOutage{
				Begin: begin.Add(time.Duration(start) * time.Second),
				End:   begin.Add(time.Duration(start+length) * time.Second),
				Type:  typ,
			})
		}

		wantDown, wantCount := markSecondBySecond(outages, begin, end)
		got := CalculateAvailability(AvailabilityInputs{
			Begin: begin, End: end, Outages: outages, TargetPercent: 100,
		})

		if got.AbsoluteDowntime != wantDown {
			t.Fatalf("iteration %d: downtime = %v, brute force says %v\noutages: %s",
				iteration, got.AbsoluteDowntime, wantDown, describeOutages(outages, begin))
		}
		if got.AbsoluteCount != wantCount {
			t.Fatalf("iteration %d: count = %d, brute force says %d\noutages: %s",
				iteration, got.AbsoluteCount, wantCount, describeOutages(outages, begin))
		}
	}
}

// The same, restricted to cases that actually contain a planned outage
// overlapping a real one — the specific shape the golden data has none of.
// Separated so a failure names the behaviour rather than an iteration
// number, and so the count of such cases is asserted rather than hoped for.
func TestCalculateAvailability_PlannedOverlapMatchesOracle(t *testing.T) {
	begin := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	const periodSecs = 4 * 3600
	end := begin.Add(periodSecs * time.Second)
	rng := rand.New(rand.NewSource(7))

	overlaps := 0
	for iteration := 0; iteration < 2000; iteration++ {
		realStart := rng.Intn(periodSecs - 600)
		realLen := 600 + rng.Intn(3600)
		// A planned window placed to land inside, across, or beside it.
		plannedStart := realStart - 900 + rng.Intn(realLen+1800)
		plannedLen := 60 + rng.Intn(2400)

		outages := []AvailabilityOutage{
			{Begin: begin.Add(time.Duration(realStart) * time.Second),
				End:  begin.Add(time.Duration(realStart+realLen) * time.Second),
				Type: OutageTypeOutage},
			{Begin: begin.Add(time.Duration(plannedStart) * time.Second),
				End:  begin.Add(time.Duration(plannedStart+plannedLen) * time.Second),
				Type: OutageTypePlanned},
		}
		if plannedStart < realStart+realLen && realStart < plannedStart+plannedLen {
			overlaps++
		}

		wantDown, wantCount := markSecondBySecond(outages, begin, end)
		got := CalculateAvailability(AvailabilityInputs{
			Begin: begin, End: end, Outages: outages, TargetPercent: 100,
		})
		if got.AbsoluteDowntime != wantDown || got.AbsoluteCount != wantCount {
			t.Fatalf("iteration %d: got %v/%d, brute force says %v/%d\noutages: %s",
				iteration, got.AbsoluteDowntime, got.AbsoluteCount,
				wantDown, wantCount, describeOutages(outages, begin))
		}
	}

	// Without this the test could pass having generated only disjoint
	// pairs, which is exactly the hole in the golden data it is here to
	// fill.
	if overlaps < 1000 {
		t.Fatalf("only %d of 2000 cases actually overlapped; the generator is not "+
			"producing the shape this test exists for", overlaps)
	}
	t.Logf("%d of 2000 cases had a planned window overlapping a real outage", overlaps)
}

func describeOutages(outages []AvailabilityOutage, begin time.Time) string {
	s := ""
	for _, o := range outages {
		s += "\n  " + o.Type + " +" +
			(time.Duration(o.Begin.Sub(begin))).String() + " .. +" +
			(time.Duration(o.End.Sub(begin))).String()
	}
	return s
}
