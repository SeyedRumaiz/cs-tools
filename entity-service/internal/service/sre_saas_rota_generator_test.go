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
	"reflect"
	"strings"
	"testing"
	"time"
)

// The windows the SaaS shifts are seeded with (migration 0154), in minutes.
var saasTestWindows = map[string][2]int{
	saasShiftTZ1L1:      {360, 810},
	saasShiftTZ1:        {360, 810},
	saasShiftTZ2L1:      {810, 1260},
	saasShiftTZ2:        {810, 1260},
	saasShiftTZ3:        {1260, 1800},
	saasShiftWeekend:    {360, 1260},
	saasShiftTZ3Regular: {1260, 1800},
	saasShiftTZ1Regular: {360, 900},
	saasShiftTZ2Regular: {720, 1260},
}

// saasTestTeam is a made-up team: a lead plus n engineers, all on days.
func saasTestTeam(team string, n int) []saasRotaMember {
	out := []saasRotaMember{{UserID: team + "-lead", Name: team + " Lead", Email: team + ".lead@example.com", TeamKey: team, IsLead: true}}
	for i := 1; i <= n; i++ {
		out = append(out, saasRotaMember{
			UserID:  fmt.Sprintf("%s-%02d", team, i),
			Name:    fmt.Sprintf("%s Engineer %d", team, i),
			Email:   fmt.Sprintf("%s.eng%02d@example.com", team, i),
			TeamKey: team,
		})
	}
	return out
}

// saasTestInput is two teams sized like the real ones -- apollo a lead and 14
// engineers, artemis a lead and 12 -- with three of apollo's engineers on TZ3
// regular hours every weekday of the month and the weeks either side of it.
func saasTestInput(month time.Time) saasRotaInput {
	members := append(saasTestTeam("apollo", 14), saasTestTeam("artemis", 12)...)
	night := map[string]map[string]bool{}
	start := saasMondayOf(month).AddDate(0, 0, -7*(saasRotaHorizonWeeks+1))
	end := month.AddDate(0, 2, 0)
	for _, id := range saasTestNight {
		night[id] = map[string]bool{}
		for d := start; d.Before(end); d = d.AddDate(0, 0, 1) {
			if saasDow(d) < 5 {
				night[id][saasISO(d)] = true
			}
		}
	}
	return saasRotaInput{
		Month:       month,
		Members:     members,
		Unavailable: map[string]map[string]bool{},
		NightOn:     night,
		Windows:     saasTestWindows,
	}
}

// saasTestNight are the engineers on TZ3 regular hours.
var saasTestNight = []string{"apollo-12", "apollo-13", "apollo-14"}

func saasTestIsNight(id string) bool {
	for _, n := range saasTestNight {
		if n == id {
			return true
		}
	}
	return false
}

func saasMonth(y int, m time.Month) time.Time { return time.Date(y, m, 1, 0, 0, 0, 0, time.UTC) }

func saasTurnsBy(plan saasRotaPlan, keep func(saasPlannedTurn) bool) []saasPlannedTurn {
	var out []saasPlannedTurn
	for _, t := range plan.Turns {
		if keep(t) {
			out = append(out, t)
		}
	}
	return out
}

func saasDaysOf(month time.Time) []time.Time {
	var out []time.Time
	for d := month; d.Month() == month.Month(); d = d.AddDate(0, 0, 1) {
		out = append(out, d)
	}
	return out
}

func TestSRESaaSRota_EveryWeekdaySlotIsFilled(t *testing.T) {
	month := saasMonth(2026, time.November)
	plan := generateSRESaaSRota(saasTestInput(month))

	for _, d := range saasDaysOf(month) {
		if saasDow(d) >= 5 {
			continue
		}
		iso := saasISO(d)
		for _, team := range []string{"apollo", "artemis"} {
			for _, slot := range []struct{ shift, tier string }{
				{saasShiftTZ1L1, saasTierL1}, {saasShiftTZ1, saasTierL2},
				{saasShiftTZ2L1, saasTierL1}, {saasShiftTZ2, saasTierL2},
			} {
				got := saasTurnsBy(plan, func(x saasPlannedTurn) bool {
					return x.RotaDate == iso && x.TeamKey == team && x.ShiftCode == slot.shift && x.Tier == slot.tier
				})
				if len(got) != 1 {
					t.Fatalf("%s %s %s %s: want exactly one turn, got %v", iso, team, slot.shift, slot.tier, got)
				}
			}
		}
		for _, team := range []string{"apollo", "artemis"} {
			for _, tier := range []string{saasTierL1, saasTierL2} {
				got := saasTurnsBy(plan, func(x saasPlannedTurn) bool {
					return x.RotaDate == iso && x.TeamKey == team && x.ShiftCode == saasShiftTZ3 && x.Tier == tier
				})
				if len(got) != 1 {
					t.Fatalf("%s %s TZ3 %s: want exactly one turn, got %v", iso, team, tier, got)
				}
				if team == "apollo" && !saasTestIsNight(got[0].UserID) {
					t.Fatalf("%s apollo TZ3 %s went to %s, who is not marked on nights", iso, tier, got[0].UserID)
				}
			}
		}
	}
}

// What the leads asked for: every team has L1, L2 and L3 on every zone every
// day -- TZ1, TZ2 and TZ3 on a weekday, the weekend window and TZ3 on a
// weekend -- whether its TZ3 hours are marked (apollo) or not (artemis).
func TestSRESaaSRota_EveryTeamHasL1L2L3OnEveryZoneEveryDay(t *testing.T) {
	month := saasMonth(2026, time.November)
	plan := generateSRESaaSRota(saasTestInput(month))
	for _, d := range saasDaysOf(month) {
		iso := saasISO(d)
		zones := map[string][]string{
			"TZ1": {saasShiftTZ1L1, saasShiftTZ1}, "TZ2": {saasShiftTZ2L1, saasShiftTZ2}, "TZ3": {saasShiftTZ3},
		}
		if saasDow(d) >= 5 {
			zones = map[string][]string{"TZ1+2": {saasShiftWeekend}, "TZ3": {saasShiftTZ3}}
		}
		for _, team := range []string{"apollo", "artemis"} {
			for zone, codes := range zones {
				for _, tier := range []string{saasTierL1, saasTierL2, saasTierL3} {
					got := saasTurnsBy(plan, func(x saasPlannedTurn) bool {
						if x.RotaDate != iso || x.TeamKey != team || x.Tier != tier {
							return false
						}
						for _, c := range codes {
							if x.ShiftCode == c {
								return true
							}
						}
						return false
					})
					if len(got) != 1 {
						t.Fatalf("%s %s %s %s: want exactly one, got %v", iso, team, zone, tier, got)
					}
				}
			}
		}
	}
	if len(plan.Warnings) != 0 {
		t.Fatalf("no slot should be empty: %v", plan.Warnings)
	}
}

func TestSRESaaSRota_NobodyHoldsTwoTurnsAtOnce(t *testing.T) {
	plan := generateSRESaaSRota(saasTestInput(saasMonth(2026, time.November)))
	byKey := map[string][]saasPlannedTurn{}
	for _, x := range plan.Turns {
		k := x.UserID + "|" + x.RotaDate
		byKey[k] = append(byKey[k], x)
	}
	for k, turns := range byKey {
		for i := range turns {
			for j := i + 1; j < len(turns); j++ {
				if turns[i].Tier == "" || turns[j].Tier == "" {
					continue // regular hours (SUP) sit under a turn; only turns may not overlap
				}
				a, b := saasTestWindows[turns[i].ShiftCode], saasTestWindows[turns[j].ShiftCode]
				if a[0] < b[1] && b[0] < a[1] {
					t.Fatalf("%s holds overlapping turns %v and %v", k, turns[i], turns[j])
				}
			}
		}
	}
}

func TestSRESaaSRota_NightPeopleNeverWorkDays(t *testing.T) {
	plan := generateSRESaaSRota(saasTestInput(saasMonth(2026, time.November)))
	for _, x := range plan.Turns {
		if x.TeamKey != "apollo" || x.Tier == "" { // artemis's nights rotate; SUP is not a turn
			continue
		}
		night := saasTestIsNight(x.UserID)
		if night && x.ShiftCode != saasShiftTZ3 {
			t.Fatalf("night person given a day turn: %v", x)
		}
		if !night && x.ShiftCode == saasShiftTZ3 && x.Tier != saasTierL3 {
			t.Fatalf("day person given a night turn: %v", x)
		}
	}
}

func TestSRESaaSRota_LeaveIsRespected(t *testing.T) {
	month := saasMonth(2026, time.November)
	in := saasTestInput(month)
	in.Unavailable["apollo-01"] = map[string]bool{}
	for d := month; d.Day() <= 14; d = d.AddDate(0, 0, 1) {
		in.Unavailable["apollo-01"][saasISO(d)] = true
	}
	in.Unavailable["artemis-lead"] = map[string]bool{"2026-11-04": true}
	plan := generateSRESaaSRota(in)

	for _, x := range plan.Turns {
		if in.Unavailable[x.UserID][x.RotaDate] {
			t.Fatalf("turn given on a day away: %v", x)
		}
	}
	// With the lead away, a teammate takes the TZ1 L3 instead of it going empty.
	got := saasTurnsBy(plan, func(x saasPlannedTurn) bool {
		return x.RotaDate == "2026-11-04" && x.TeamKey == "artemis" && x.ShiftCode == saasShiftTZ1 && x.Tier == saasTierL3
	})
	if len(got) != 1 || got[0].UserID == "artemis-lead" {
		t.Fatalf("artemis TZ1 L3 on the lead's day away: want one teammate, got %v (warnings %v)", got, plan.Warnings)
	}
}

// The script's rule: a CR serves one week and goes back to the L1 rotation,
// so (with enough people free) nobody is L2 two weeks running.
func TestSRESaaSRota_NobodyIsCRTwoWeeksRunning(t *testing.T) {
	month := saasMonth(2026, time.November)
	plan := generateSRESaaSRota(saasTestInput(month))
	l2ByWeek := map[string]map[int]map[string]bool{}
	for _, x := range plan.Turns {
		if x.Tier != saasTierL2 || (x.ShiftCode != saasShiftTZ1 && x.ShiftCode != saasShiftTZ2) {
			continue
		}
		d, _ := time.Parse("2006-01-02", x.RotaDate)
		w := saasWeek(d)
		if l2ByWeek[x.TeamKey] == nil {
			l2ByWeek[x.TeamKey] = map[int]map[string]bool{}
		}
		if l2ByWeek[x.TeamKey][w] == nil {
			l2ByWeek[x.TeamKey][w] = map[string]bool{}
		}
		l2ByWeek[x.TeamKey][w][x.UserID] = true
	}
	for team, weeks := range l2ByWeek {
		for w, people := range weeks {
			if len(people) > 4 {
				t.Fatalf("%s week %d: L2 should come from the week's CR pairs, got %d people", team, w, len(people))
			}
			for p := range people {
				if weeks[w-1][p] {
					t.Fatalf("%s: %s is L2 in weeks %d and %d", team, p, w-1, w)
				}
			}
		}
	}
}

func TestSRESaaSRota_WeekendOnCallEarnsMondayAndTuesday(t *testing.T) {
	month := saasMonth(2026, time.November)
	plan := generateSRESaaSRota(saasTestInput(month))
	for _, d := range saasDaysOf(month) {
		if saasDow(d) != 5 { // Saturday
			continue
		}
		sat, sun := saasISO(d), saasISO(d.AddDate(0, 0, 1))
		for _, team := range []string{"apollo", "artemis"} {
			onSat := saasTurnsBy(plan, func(x saasPlannedTurn) bool {
				return x.RotaDate == sat && x.TeamKey == team && x.ShiftCode == saasShiftWeekend && x.Tier == saasTierL1
			})
			if len(onSat) != 1 {
				t.Fatalf("%s %s: want one weekend on-call, got %v", sat, team, onSat)
			}
			oncall := onSat[0].UserID
			if d.AddDate(0, 0, 1).Month() == month.Month() {
				onSun := saasTurnsBy(plan, func(x saasPlannedTurn) bool {
					return x.RotaDate == sun && x.TeamKey == team && x.ShiftCode == saasShiftWeekend && x.Tier == saasTierL1
				})
				if len(onSun) != 1 || onSun[0].UserID != oncall {
					t.Fatalf("%s %s: Sunday's on-call %v is not Saturday's %s", sun, team, onSun, oncall)
				}
			}
			mon, tue := saasISO(d.AddDate(0, 0, 2)), saasISO(d.AddDate(0, 0, 3))
			if !hasLieu(plan, oncall, mon, tue) {
				t.Fatalf("%s's weekend on-call %s got no lieu leave on %s-%s: %v", team, oncall, mon, tue, plan.Lieu)
			}
			for _, x := range plan.Turns {
				if x.UserID == oncall && (x.RotaDate == mon || x.RotaDate == tue) {
					t.Fatalf("%s works on their lieu day: %v", oncall, x)
				}
			}
		}
	}
}

// One person works one zone a day: every turn anyone holds that day, L1-L3,
// is on one zone (the weekend window counts as one), and their SUP is on that
// zone too. The lead is L3 on TZ1 -- the weekend window at a weekend -- and
// TZ2 and TZ3 have an L3 of their own, never the lead.
func TestSRESaaSRota_OnePersonWorksOneZoneADay(t *testing.T) {
	month := saasMonth(2026, time.November)
	plan := generateSRESaaSRota(saasTestInput(month))
	zoneOf := map[string]string{
		saasShiftTZ1L1: "TZ1", saasShiftTZ1: "TZ1", saasShiftTZ1Regular: "TZ1",
		saasShiftTZ2L1: "TZ2", saasShiftTZ2: "TZ2", saasShiftTZ2Regular: "TZ2",
		saasShiftTZ3: "TZ3", saasShiftTZ3Regular: "TZ3", saasShiftWeekend: "TZ1+2",
	}
	zones := map[string]map[string]bool{}
	for _, x := range plan.Turns {
		k := x.UserID + " " + x.RotaDate
		if zones[k] == nil {
			zones[k] = map[string]bool{}
		}
		zones[k][zoneOf[x.ShiftCode]] = true
	}
	for k, z := range zones {
		if len(z) != 1 {
			t.Fatalf("%s works more than one zone: %v", k, z)
		}
	}
	for _, d := range saasDaysOf(month) {
		iso := saasISO(d)
		first := saasShiftTZ1
		if saasDow(d) >= 5 {
			first = saasShiftWeekend
		}
		for _, team := range []string{"apollo", "artemis"} {
			lead := saasTurnsBy(plan, func(x saasPlannedTurn) bool {
				return x.UserID == team+"-lead" && x.RotaDate == iso && x.Tier == saasTierL3
			})
			if len(lead) != 1 || lead[0].ShiftCode != first {
				t.Fatalf("%s %s lead: want L3 on %s only, got %v", iso, team, first, lead)
			}
		}
	}
}

// With a normal team, leads are never drawn for L1, L2 or the weekend: their
// L3 is never lost, so no "L3 is empty" warning is raised.
func TestSRESaaSRota_LeadsTakeNothingButL3WhileOthersAreFree(t *testing.T) {
	plan := generateSRESaaSRota(saasTestInput(saasMonth(2026, time.November)))
	for _, x := range plan.Turns {
		if strings.HasSuffix(x.UserID, "-lead") && x.Tier != saasTierL3 && x.Tier != "" {
			t.Fatalf("a lead was given %v while others were free", x)
		}
	}
	if hasWarning(plan, "", "L3 is empty") {
		t.Fatalf("no L3 should be lost: %v", plan.Warnings)
	}
}

// A slot is left empty only when nobody on the team could take it: every
// engineer counts unless marked away, on nights that week, or already on a day
// turn that day -- not only the zone's own half.
func TestSRESaaSRota_NoSlotIsLeftEmptyWhileSomeoneIsFree(t *testing.T) {
	month := saasMonth(2026, time.November)
	in := saasTestInput(month)
	// Most of apollo away for a week, so a half runs out.
	for i := 1; i <= 8; i++ {
		id := fmt.Sprintf("apollo-%02d", i)
		in.Unavailable[id] = map[string]bool{}
		for d := saasMonth(2026, time.November).AddDate(0, 0, 15); d.Day() <= 22; d = d.AddDate(0, 0, 1) {
			in.Unavailable[id][saasISO(d)] = true
		}
	}
	plan := generateSRESaaSRota(in)
	dayShifts := map[string]bool{saasShiftTZ1L1: true, saasShiftTZ1: true, saasShiftTZ2L1: true, saasShiftTZ2: true}
	for _, w := range plan.Warnings {
		if w.Date == "" || !strings.Contains(w.Message, "nobody is free") ||
			!(strings.Contains(w.Message, "TZ1") || strings.Contains(w.Message, "TZ2")) {
			continue
		}
		team := strings.SplitN(w.Message, " ", 2)[0]
		held := map[string]bool{}
		for _, x := range plan.Turns {
			if x.RotaDate == w.Date && (dayShifts[x.ShiftCode] || x.ShiftCode == saasShiftTZ3) && x.Tier != saasTierL3 {
				held[x.UserID] = true
			}
		}
		for _, m := range in.Members {
			if m.TeamKey != team || held[m.UserID] || in.Unavailable[m.UserID][w.Date] || saasTestIsNight(m.UserID) {
				continue
			}
			t.Fatalf("%s: %q, but %s was free", w.Date, w.Message, m.UserID)
		}
	}
	// And the week really was short: the slots were filled from the team.
	for _, d := range saasDaysOf(month) {
		iso := saasISO(d)
		if saasDow(d) >= 5 || iso < "2026-11-16" || iso > "2026-11-20" {
			continue
		}
		for _, sh := range []string{saasShiftTZ1L1, saasShiftTZ2L1} {
			if len(saasTurnsBy(plan, func(x saasPlannedTurn) bool {
				return x.RotaDate == iso && x.TeamKey == "apollo" && x.ShiftCode == sh
			})) != 1 {
				t.Fatalf("%s apollo %s not filled", iso, sh)
			}
		}
	}
}

// TZ3 hours marked for only part of a month: the weeks after the marks stop
// still get night people, from the everyone rotation.
func TestSRESaaSRota_WeeksWithNoTZ3HoursMarkedStillGetNights(t *testing.T) {
	month := saasMonth(2026, time.November)
	in := saasTestInput(month)
	for _, days := range in.NightOn {
		for iso := range days {
			if iso >= "2026-11-16" {
				delete(days, iso)
			}
		}
	}
	plan := generateSRESaaSRota(in)
	for _, d := range saasDaysOf(month) {
		if saasDow(d) >= 5 {
			continue
		}
		iso := saasISO(d)
		for _, tier := range []string{saasTierL1, saasTierL2} {
			got := saasTurnsBy(plan, func(x saasPlannedTurn) bool {
				return x.RotaDate == iso && x.TeamKey == "apollo" && x.ShiftCode == saasShiftTZ3 && x.Tier == tier
			})
			if len(got) != 1 {
				t.Fatalf("%s apollo TZ3 %s: want one turn, got %v", iso, tier, got)
			}
			if iso < "2026-11-16" && !saasTestIsNight(got[0].UserID) {
				t.Fatalf("%s: a marked week's nights went to %s, who is not marked", iso, got[0].UserID)
			}
		}
	}
}

// TZ3 hours marked for only part of a week: the nights after the marks stop
// are still covered, by people not on a day turn that day.
func TestSRESaaSRota_NightsAfterMarksStopMidWeekAreCovered(t *testing.T) {
	month := saasMonth(2026, time.November)
	in := saasTestInput(month)
	for _, days := range in.NightOn {
		for iso := range days {
			if iso >= "2026-11-19" { // marks end on Wednesday 18th
				delete(days, iso)
			}
		}
	}
	plan := generateSRESaaSRota(in)
	for _, iso := range []string{"2026-11-19", "2026-11-20"} {
		for _, tier := range []string{saasTierL1, saasTierL2} {
			got := saasTurnsBy(plan, func(x saasPlannedTurn) bool {
				return x.RotaDate == iso && x.TeamKey == "apollo" && x.ShiftCode == saasShiftTZ3 && x.Tier == tier
			})
			if len(got) != 1 {
				t.Fatalf("%s apollo TZ3 %s: want one turn, got %v (warnings %v)", iso, tier, got, plan.Warnings)
			}
			for _, x := range plan.Turns {
				if x.UserID == got[0].UserID && x.RotaDate == iso && x.ShiftCode != saasShiftTZ3 && x.Tier != saasTierL3 && x.Tier != "" {
					t.Fatalf("%s: %s is on nights and on a day turn %v", iso, x.UserID, x)
				}
			}
		}
	}
}

// SUP on every working weekday: everyone not away holds exactly one SUP, the
// regular hours of the one zone they work that day, beside any L1-L3 they
// hold -- a turn's own zone for whoever holds a turn, otherwise night people
// TZ3, leads TZ1 and everyone else the half they are in that week. Never
// more than one zone, and none on a weekend.
func TestSRESaaSRota_EveryoneWorkingHoldsOneSUPOfTheirZone(t *testing.T) {
	month := saasMonth(2026, time.November)
	in := saasTestInput(month)
	in.Unavailable["apollo-02"] = map[string]bool{"2026-11-04": true}
	plan := generateSRESaaSRota(in)
	regular := map[string]bool{saasShiftTZ1Regular: true, saasShiftTZ2Regular: true, saasShiftTZ3Regular: true}
	supFor := map[string]string{
		saasShiftTZ1L1: saasShiftTZ1Regular, saasShiftTZ1: saasShiftTZ1Regular,
		saasShiftTZ2L1: saasShiftTZ2Regular, saasShiftTZ2: saasShiftTZ2Regular,
		saasShiftTZ3: saasShiftTZ3Regular,
	}
	away := func(user, iso string) bool {
		if in.Unavailable[user][iso] {
			return true
		}
		for _, l := range plan.Lieu { // lieu leave after a weekend on-call is time off too
			if l.UserID == user && iso >= l.From && iso <= l.To {
				return true
			}
		}
		return false
	}
	withTurn := 0
	for _, d := range saasDaysOf(month) {
		iso := saasISO(d)
		for _, m := range in.Members {
			var turns, sups []saasPlannedTurn
			for _, x := range plan.Turns {
				if x.UserID != m.UserID || x.RotaDate != iso {
					continue
				}
				if regular[x.ShiftCode] {
					sups = append(sups, x)
				} else {
					turns = append(turns, x)
				}
			}
			if saasDow(d) >= 5 {
				if len(sups) != 0 {
					t.Fatalf("%s %s: SUP on a weekend %v", iso, m.UserID, sups)
				}
				continue
			}
			if away(m.UserID, iso) {
				if len(sups)+len(turns) != 0 {
					t.Fatalf("%s %s is away but has %v %v", iso, m.UserID, turns, sups)
				}
				continue
			}
			if len(sups) != 1 {
				t.Fatalf("%s %s: want one SUP, got %v (turns %v)", iso, m.UserID, sups, turns)
			}
			code := sups[0].ShiftCode
			if len(turns) > 0 {
				withTurn++
				if want := supFor[turns[0].ShiftCode]; code != want {
					t.Fatalf("%s %s holds %s but SUP is %s, want %s", iso, m.UserID, turns[0].ShiftCode, code, want)
				}
				continue
			}
			switch {
			case m.IsLead && code != saasShiftTZ1Regular:
				t.Fatalf("%s lead %s SUP is %s, want TZ1", iso, m.UserID, code)
			case saasTestIsNight(m.UserID) && code != saasShiftTZ3Regular:
				t.Fatalf("%s night person %s SUP is %s, want TZ3", iso, m.UserID, code)
			case !m.IsLead && m.TeamKey == "apollo" && !saasTestIsNight(m.UserID) && code == saasShiftTZ3Regular:
				t.Fatalf("%s day person %s given TZ3 SUP", iso, m.UserID)
			}
		}
	}
	if withTurn == 0 {
		t.Fatal("nobody on a turn was checked")
	}
}

// Regular hours someone else set keep the person to that zone: on a day an
// engineer's TZ2 SUP was set by a lead, the generator gives them nothing on
// TZ1 or TZ3 -- and every zone still has its L1, L2 and L3.
func TestSRESaaSRota_RegularHoursSetByOthersKeepThePersonToThatZone(t *testing.T) {
	month := saasMonth(2026, time.November)
	in := saasTestInput(month)
	in.Pinned = map[string]map[string]string{}
	var people []string
	for _, m := range in.Members {
		if m.TeamKey == "apollo" && !m.IsLead {
			people = append(people, m.UserID)
		}
	}
	for d := month; d.Month() == month.Month(); d = d.AddDate(0, 0, 1) {
		if saasDow(d) >= 5 {
			continue
		}
		for _, p := range people {
			if in.Pinned[p] == nil {
				in.Pinned[p] = map[string]string{}
			}
			in.Pinned[p][saasISO(d)] = "TZ2"
		}
	}
	plan := generateSRESaaSRota(in)
	zoneOf := map[string]string{saasShiftTZ1L1: "TZ1", saasShiftTZ1: "TZ1", saasShiftTZ1Regular: "TZ1",
		saasShiftTZ2L1: "TZ2", saasShiftTZ2: "TZ2", saasShiftTZ2Regular: "TZ2", saasShiftTZ3: "TZ3", saasShiftTZ3Regular: "TZ3"}
	for _, x := range plan.Turns {
		if z := in.Pinned[x.UserID][x.RotaDate]; z != "" && zoneOf[x.ShiftCode] != z {
			t.Fatalf("%s pinned to %s but given %v", x.UserID, z, x)
		}
	}
	// Apollo's engineers are all kept to TZ2 every weekday, so its TZ1 and TZ3
	// turns can only be its lead's -- one of them -- and the rest is warned about.
	if !hasWarning(plan, "", "apollo") {
		t.Fatalf("expected apollo's other zones to be warned about, got %v", plan.Warnings)
	}
}

// A lead chooses three people for a team's TZ3: every TZ3 turn of that team
// -- L1, L2 and L3, weekdays and weekends -- is theirs while they are free,
// each of them takes every tier in turn, and they work no other zone. Only a
// night the crew is short (lieu after a weekend on-call) is filled from the
// rest of the team. The other team keeps its usual nights.
func TestSRESaaSRota_AChosenTZ3CrewTakesEveryTierInTurn(t *testing.T) {
	month := saasMonth(2026, time.November)
	in := saasTestInput(month)
	crew := []string{"apollo-03", "apollo-07", "apollo-10"}
	in.NightCrew = map[string][]string{"apollo": crew}
	plan := generateSRESaaSRota(in)
	isCrew := map[string]bool{}
	for _, p := range crew {
		isCrew[p] = true
	}
	lieu := func(user, iso string) bool {
		for _, l := range plan.Lieu {
			if l.UserID == user && iso >= l.From && iso <= l.To {
				return true
			}
		}
		return false
	}
	tiers := map[string]map[string]int{}
	for _, d := range saasDaysOf(month) {
		iso := saasISO(d)
		allFree := true
		for _, p := range crew {
			if lieu(p, iso) {
				allFree = false
			}
		}
		for _, tier := range []string{saasTierL1, saasTierL2, saasTierL3} {
			got := saasTurnsBy(plan, func(x saasPlannedTurn) bool {
				return x.RotaDate == iso && x.TeamKey == "apollo" && x.ShiftCode == saasShiftTZ3 && x.Tier == tier
			})
			if len(got) != 1 {
				t.Fatalf("%s apollo TZ3 %s: want one, got %v", iso, tier, got)
			}
			if allFree && !isCrew[got[0].UserID] {
				t.Fatalf("%s apollo TZ3 %s went to %s, not the chosen crew", iso, tier, got[0].UserID)
			}
			if isCrew[got[0].UserID] {
				if tiers[got[0].UserID] == nil {
					tiers[got[0].UserID] = map[string]int{}
				}
				tiers[got[0].UserID][tier]++
			}
		}
	}
	for _, p := range crew {
		for _, tier := range []string{saasTierL1, saasTierL2, saasTierL3} {
			if tiers[p][tier] == 0 {
				t.Fatalf("%s never took TZ3 %s: %v", p, tier, tiers[p])
			}
		}
	}
	for _, x := range plan.Turns {
		if isCrew[x.UserID] && x.ShiftCode != saasShiftTZ3 && x.ShiftCode != saasShiftTZ3Regular {
			t.Fatalf("crew member given another zone: %v", x)
		}
	}
	// Artemis chose nobody, so its nights are its usual rotation.
	if got := saasTurnsBy(plan, func(x saasPlannedTurn) bool { return x.TeamKey == "artemis" && x.ShiftCode == saasShiftTZ3 }); len(got) == 0 {
		t.Fatal("artemis has no nights")
	}
}

// Lieu leave is only the days the on-call is not already away: with leave on
// the Tuesday after their weekend, the lieu is the Monday alone -- written as
// a span that overlaps nothing, so the write cannot drop it.
func TestSRESaaSRota_LieuSkipsOnlyTheDayAlreadyAway(t *testing.T) {
	month := saasMonth(2026, time.November)
	plan := generateSRESaaSRota(saasTestInput(month))
	var l saasPlannedLieu
	for _, x := range plan.Lieu {
		if x.From != x.To && x.Weekend >= "2026-11-07" {
			l = x
			break
		}
	}
	if l.UserID == "" {
		t.Fatalf("no two-day lieu to test with: %v", plan.Lieu)
	}
	in := saasTestInput(month)
	in.Unavailable[l.UserID] = map[string]bool{l.To: true}
	again := generateSRESaaSRota(in)
	for _, x := range again.Lieu {
		if x.UserID == l.UserID && x.Weekend == l.Weekend {
			if x.From != l.From || x.To != l.From {
				t.Fatalf("lieu for %s after %s: want %s only, got %s to %s", l.UserID, l.Weekend, l.From, x.From, x.To)
			}
			return
		}
	}
	t.Fatalf("the weekend's lieu went missing for %s: %v", l.UserID, again.Lieu)
}

// A weekend that starts in the month before (Saturday 31 October, Sunday 1
// November): November's run writes the Sunday, so it writes that weekend's
// lieu too -- the on-call is kept off the Monday and Tuesday either way.
func TestSRESaaSRota_AWeekendStartingLastMonthStillRecordsItsLieu(t *testing.T) {
	plan := generateSRESaaSRota(saasTestInput(saasMonth(2026, time.November)))
	sunday := saasTurnsBy(plan, func(x saasPlannedTurn) bool {
		return x.RotaDate == "2026-11-01" && x.ShiftCode == saasShiftWeekend && x.Tier == saasTierL1
	})
	if len(sunday) == 0 {
		t.Fatal("no weekend on-call on Sunday 1 November")
	}
	for _, on := range sunday {
		found := false
		for _, l := range plan.Lieu {
			if l.UserID == on.UserID && l.Weekend == "2026-10-31" && l.From == "2026-11-02" {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s was on call on 1 November but has no lieu recorded: %v", on.UserID, plan.Lieu)
		}
	}
}

func TestSRESaaSRota_IsTheSameEveryTime(t *testing.T) {
	month := saasMonth(2026, time.December)
	a := generateSRESaaSRota(saasTestInput(month))
	b := generateSRESaaSRota(saasTestInput(month))
	if !reflect.DeepEqual(a, b) {
		t.Fatal("two runs over the same input gave different months")
	}
}

// A regeneration from a day must give exactly the turns a full run gives from
// that day on, and none before it.
func TestSRESaaSRota_RegeneratingFromADayMatchesTheFullMonth(t *testing.T) {
	month := saasMonth(2026, time.November)
	full := generateSRESaaSRota(saasTestInput(month))
	in := saasTestInput(month)
	in.From = time.Date(2026, time.November, 18, 0, 0, 0, 0, time.UTC)
	part := generateSRESaaSRota(in)

	var want []saasPlannedTurn
	for _, x := range full.Turns {
		if x.RotaDate >= "2026-11-18" {
			want = append(want, x)
		}
	}
	if !reflect.DeepEqual(want, part.Turns) {
		t.Fatalf("regenerating from the 18th differs from the full month's turns from the 18th")
	}
}

func TestSRESaaSRota_OnlyTheMonthIsWritten(t *testing.T) {
	month := saasMonth(2026, time.November)
	plan := generateSRESaaSRota(saasTestInput(month))
	for _, x := range plan.Turns {
		if !strings.HasPrefix(x.RotaDate, "2026-11-") {
			t.Fatalf("turn outside the month: %v", x)
		}
	}
}

// With no TZ3 regular hours marked, every engineer takes a turn on nights:
// each week one engineer from each team, never a lead, and that person works
// no day turn that week.
func TestSRESaaSRota_NoTZ3HoursMarkedPutsEveryoneOnTheNightRotation(t *testing.T) {
	month := saasMonth(2026, time.November)
	in := saasTestInput(month)
	in.NightOn = map[string]map[string]bool{}
	plan := generateSRESaaSRota(in)

	if len(plan.Warnings) != 0 {
		t.Fatalf("no warning expected, got %v", plan.Warnings)
	}
	type key struct {
		team string
		wi   int
	}
	nightOf := map[key]map[string]bool{} // team, week -> its night people
	nightPeople := map[string]map[string]bool{"apollo": {}, "artemis": {}}
	for _, d := range saasDaysOf(month) {
		if saasDow(d) >= 5 {
			continue
		}
		iso, wi := saasISO(d), saasWeek(d)
		for _, team := range []string{"apollo", "artemis"} {
			for _, tier := range []string{saasTierL1, saasTierL2} {
				got := saasTurnsBy(plan, func(x saasPlannedTurn) bool {
					return x.RotaDate == iso && x.TeamKey == team && x.ShiftCode == saasShiftTZ3 && x.Tier == tier
				})
				if len(got) != 1 {
					t.Fatalf("%s %s TZ3 %s: want exactly one turn, got %v", iso, team, tier, got)
				}
				p := got[0].UserID
				if strings.HasSuffix(p, "-lead") || !strings.HasPrefix(p, team+"-") {
					t.Fatalf("%s %s TZ3 %s went to %s", iso, team, tier, p)
				}
				k := key{team, wi}
				if nightOf[k] == nil {
					nightOf[k] = map[string]bool{}
				}
				nightOf[k][p] = true
				nightPeople[team][p] = true
			}
		}
	}
	for k, people := range nightOf {
		if len(people) != 2 {
			t.Fatalf("%s week %d: want a night pair, got %v", k.team, k.wi, people)
		}
		for _, x := range plan.Turns {
			if people[x.UserID] && saasWeek(saasMustParse(x.RotaDate)) == k.wi &&
				saasDow(saasMustParse(x.RotaDate)) < 5 && x.ShiftCode != saasShiftTZ3 && x.Tier != "" {
				t.Fatalf("%s week %d: night person %s also given a day turn %v", k.team, k.wi, x.UserID, x)
			}
		}
	}
	for team, people := range nightPeople {
		if len(people) < 6 {
			t.Fatalf("%s: nights should rotate through the team, only %v took them", team, people)
		}
	}
}

// A lead keeps somebody off nights by marking them away (leave, RnD, ENG).
func TestSRESaaSRota_SomeoneMarkedAwayIsNotPutOnNights(t *testing.T) {
	month := saasMonth(2026, time.November)
	in := saasTestInput(month)
	in.NightOn = map[string]map[string]bool{}
	plain := generateSRESaaSRota(in)
	var target string
	for _, x := range plain.Turns {
		if x.ShiftCode == saasShiftTZ3 && x.Tier == saasTierL1 {
			target = x.UserID
			break
		}
	}
	if target == "" {
		t.Fatal("expected somebody on nights")
	}
	in.Unavailable[target] = map[string]bool{}
	start := saasMondayOf(month).AddDate(0, 0, -7*(saasRotaHorizonWeeks+1))
	for d := start; d.Before(month.AddDate(0, 1, 0)); d = d.AddDate(0, 0, 1) {
		in.Unavailable[target][saasISO(d)] = true
	}
	plan := generateSRESaaSRota(in)
	for _, x := range plan.Turns {
		if x.UserID == target {
			t.Fatalf("%s is marked away all month but got %v", target, x)
		}
	}
}

func saasMustParse(iso string) time.Time {
	d, err := time.Parse("2006-01-02", iso)
	if err != nil {
		panic(err)
	}
	return d
}

func TestSRESaaSRota_ATeamWithNoLeadIsWarnedAbout(t *testing.T) {
	in := saasTestInput(saasMonth(2026, time.November))
	var members []saasRotaMember
	for _, m := range in.Members {
		if m.UserID != "artemis-lead" {
			members = append(members, m)
		}
	}
	in.Members = members
	plan := generateSRESaaSRota(in)
	if !hasWarning(plan, "", "team artemis has no lead") {
		t.Fatalf("expected a no-lead warning, got %v", plan.Warnings)
	}
	// Its L3 is still covered, from its engineers.
	l3 := saasTurnsBy(plan, func(x saasPlannedTurn) bool {
		return x.TeamKey == "artemis" && x.Tier == saasTierL3 && x.RotaDate == "2026-11-03"
	})
	if len(l3) != 3 {
		t.Fatalf("artemis with no lead: want an L3 on TZ1, TZ2 and TZ3, got %v", l3)
	}
}

func TestSRESaaSRota_SomeoneAwayAllWeekIsNotThatWeeksCR(t *testing.T) {
	month := saasMonth(2026, time.November)
	in := saasTestInput(month)
	// Away Monday 9 to Friday 13 November.
	in.Unavailable["artemis-02"] = map[string]bool{}
	for d := 9; d <= 13; d++ {
		in.Unavailable["artemis-02"][fmt.Sprintf("2026-11-%02d", d)] = true
	}
	plan := generateSRESaaSRota(in)
	for _, x := range plan.Turns {
		if x.UserID == "artemis-02" && x.RotaDate >= "2026-11-09" && x.RotaDate <= "2026-11-13" {
			t.Fatalf("given a turn in a week away: %v", x)
		}
	}
}

func TestSaasWeek(t *testing.T) {
	cases := map[string]int{
		"2024-01-01": 0, "2024-01-07": 0, "2024-01-08": 1,
		"2026-11-02": 148, "2026-11-08": 148,
	}
	for iso, want := range cases {
		d, _ := time.Parse("2006-01-02", iso)
		if got := saasWeek(d); got != want {
			t.Fatalf("saasWeek(%s) = %d, want %d", iso, got, want)
		}
	}
}

func TestSaasRotate(t *testing.T) {
	a := []string{"a", "b", "c"}
	for n, want := range map[int][]string{0: {"a", "b", "c"}, 1: {"b", "c", "a"}, 5: {"c", "a", "b"}, -1: {"c", "a", "b"}} {
		if got := saasRotate(a, n); !reflect.DeepEqual(got, want) {
			t.Fatalf("saasRotate(%d) = %v, want %v", n, got, want)
		}
	}
	if saasRotate(nil, 3) != nil {
		t.Fatal("rotating nothing should give nothing")
	}
}

func hasWarning(plan saasRotaPlan, iso, contains string) bool {
	for _, w := range plan.Warnings {
		if (iso == "" || w.Date == iso) && strings.Contains(w.Message, contains) {
			return true
		}
	}
	return false
}

func hasLieu(plan saasRotaPlan, user, from, to string) bool {
	for _, l := range plan.Lieu {
		if l.UserID == user && l.From <= from && l.To >= to {
			return true
		}
	}
	return false
}
