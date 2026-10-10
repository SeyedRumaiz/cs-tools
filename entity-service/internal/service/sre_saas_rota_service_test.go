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
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/apierror"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/auth"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/repository"
)

type fakeRotaWriter struct {
	calls int
	got   repository.RotaMonthWrite
	// owned is what GeneratedRows answers: the rows the generator wrote.
	owned      repository.GeneratedRowIDs
	ownerAsked string
}

func (f *fakeRotaWriter) GeneratedRows(_ context.Context, owner string, _ []string, _, _ string) (repository.GeneratedRowIDs, error) {
	f.ownerAsked = owner
	out := repository.GeneratedRowIDs{Assignments: map[string]bool{}, Absences: map[string]bool{}}
	for id := range f.owned.Assignments {
		out.Assignments[id] = true
	}
	for id := range f.owned.Absences {
		out.Absences[id] = true
	}
	return out, nil
}

func (f *fakeRotaWriter) ReplaceGeneratedMonth(_ context.Context, w repository.RotaMonthWrite) (repository.RotaMonthWriteResult, error) {
	f.calls++
	f.got = w
	return repository.RotaMonthWriteResult{Written: len(w.Turns), LieuWritten: len(w.Lieu), Replaced: 7}, nil
}

// saasTestCatalogue is the SaaS rota as the catalogue serves it: its shifts,
// the lieu kind, and two teams on SRE_SAAS (plus a CRE team that is not).
func saasTestCatalogue() domain.ScheduleCatalogue {
	rota := RotaSRESaaS
	cat := domain.ScheduleCatalogue{
		AbsenceKinds: []domain.ScheduleAbsenceKind{{Code: "LIEU_LEAVE"}, {Code: "ANNUAL_LEAVE"}},
	}
	for code, w := range saasTestWindows {
		cat.Shifts = append(cat.Shifts, domain.ScheduleShift{Code: code, StartMinute: w[0], EndMinute: w[1]})
	}
	for _, spec := range []struct {
		key string
		n   int
	}{{"apollo", 14}, {"artemis", 12}} {
		team := domain.ScheduleTeam{Key: spec.key, Name: spec.key, Family: "SRE", RotaCode: &rota}
		for _, m := range saasTestTeam(spec.key, spec.n) {
			role := "engineer"
			if m.IsLead {
				role = "lead"
			}
			team.Members = append(team.Members, domain.ScheduleTeamMember{
				ScheduleEngineer: domain.ScheduleEngineer{UserID: m.UserID, Name: m.Name, Email: m.Email},
				Role:             role,
			})
		}
		cat.Teams = append(cat.Teams, team)
	}
	cat.Teams = append(cat.Teams, domain.ScheduleTeam{Key: "castor", Family: "CRE",
		Members: []domain.ScheduleTeamMember{{ScheduleEngineer: domain.ScheduleEngineer{UserID: "castor-01"}, Role: "lead"}}})
	return cat
}

// saasTestNightRows marks apollo-12..14 on TZ3 regular hours every weekday
// of the read window, as a lead would on the roster.
func saasTestNightRows(from, to time.Time) []domain.ScheduleAssignment {
	var out []domain.ScheduleAssignment
	for _, id := range saasTestNight {
		for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
			if saasDow(d) < 5 {
				out = append(out, domain.ScheduleAssignment{
					Engineer: domain.ScheduleEngineer{UserID: id}, TeamKey: "apollo",
					ShiftCode: saasShiftTZ3Regular, RotaDate: saasISO(d), Source: "MANUAL",
				})
			}
		}
	}
	return out
}

func newTestRotaService(repo *fakeScheduleRepo, writer *fakeRotaWriter, now time.Time) *rotaGenerateService {
	repo.catalogue = saasTestCatalogue()
	return &rotaGenerateService{schedule: repo, writer: writer, access: alwaysUnrestrictedAccess{}, now: func() time.Time { return now }}
}

var saasTestNow = time.Date(2026, time.October, 9, 10, 0, 0, 0, time.UTC)

func generateReq(month string) domain.GenerateRotaMonthRequest {
	return domain.GenerateRotaMonthRequest{RotaCode: "SRE_SAAS", Month: month}
}

func TestGenerateMonth_WhoMayGenerate(t *testing.T) {
	cases := []struct {
		name     string
		ctx      context.Context
		ledTeams []string
		admin    []string
		want     string // "" = allowed
	}{
		{"a lead of apollo", leadCtx("apollo.lead@example.com"), []string{"apollo"}, nil, ""},
		{"a lead of artemis", leadCtx("artemis.lead@example.com"), []string{"artemis"}, nil, ""},
		{"an SRE rota admin", leadCtx("admin@example.com"), []string{}, []string{"apollo", "artemis"}, ""},
		{"a lead of a CRE team", leadCtx("castor.lead@example.com"), []string{"castor"}, nil, "forbidden"},
		{"an engineer who leads nothing", leadCtx("apollo.eng01@example.com"), []string{}, nil, "forbidden"},
		{"a service credential", auth.WithIdentity(context.Background(), auth.Identity{Validated: true, ClientID: "svc"}), []string{"apollo"}, nil, "forbidden"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeScheduleRepo{ledTeams: tc.ledTeams, adminTeams: tc.admin}
			svc := newTestRotaService(repo, &fakeRotaWriter{}, saasTestNow)
			req := generateReq("2026-11")
			req.DryRun = true
			_, err := svc.GenerateMonth(tc.ctx, req)
			var forbidden *apierror.ForbiddenError
			switch tc.want {
			case "":
				if err != nil {
					t.Fatalf("want allowed, got %v", err)
				}
			case "forbidden":
				if !errors.As(err, &forbidden) {
					t.Fatalf("want 403, got %v", err)
				}
			}
		})
	}
}

func TestGenerateMonth_RefusesWhatItCannotDo(t *testing.T) {
	cases := []struct {
		name string
		edit func(*domain.GenerateRotaMonthRequest)
		want any
	}{
		{"another rota", func(r *domain.GenerateRotaMonthRequest) { r.RotaCode = "SRE_IAAS" }, &apierror.NotFoundError{}},
		{"a bad month", func(r *domain.GenerateRotaMonthRequest) { r.Month = "November" }, &apierror.ValidationError{}},
		{"a month that is over", func(r *domain.GenerateRotaMonthRequest) { r.Month = "2026-09" }, &apierror.ValidationError{}},
		{"from in the past", func(r *domain.GenerateRotaMonthRequest) { r.Month = "2026-10"; f := "2026-10-01"; r.From = &f }, &apierror.ValidationError{}},
		{"from outside the month", func(r *domain.GenerateRotaMonthRequest) { f := "2026-12-01"; r.From = &f }, &apierror.ValidationError{}},
		{"a TZ3 crew for a team not on the rota", func(r *domain.GenerateRotaMonthRequest) {
			r.NightCrew = map[string][]string{"castor": {"castor-01"}}
		}, &apierror.ValidationError{}},
		{"a TZ3 crew with someone from another team", func(r *domain.GenerateRotaMonthRequest) {
			r.NightCrew = map[string][]string{"apollo": {"apollo-01", "artemis-01"}}
		}, &apierror.ValidationError{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeScheduleRepo{ledTeams: []string{"apollo"}}
			svc := newTestRotaService(repo, &fakeRotaWriter{}, saasTestNow)
			req := generateReq("2026-11")
			tc.edit(&req)
			_, err := svc.GenerateMonth(leadCtx("apollo.lead@example.com"), req)
			if err == nil || fmt.Sprintf("%T", err) != fmt.Sprintf("%T", tc.want) {
				t.Fatalf("want %T, got %v", tc.want, err)
			}
		})
	}
}

func TestGenerateMonth_DryRunWritesNothing(t *testing.T) {
	repo := &fakeScheduleRepo{ledTeams: []string{"apollo"}}
	repo.assignments = saasTestNightRows(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 12, 5, 0, 0, 0, 0, time.UTC))
	writer := &fakeRotaWriter{}
	svc := newTestRotaService(repo, writer, saasTestNow)
	req := generateReq("2026-11")
	req.DryRun = true
	res, err := svc.GenerateMonth(leadCtx("apollo.lead@example.com"), req)
	if err != nil {
		t.Fatal(err)
	}
	if writer.calls != 0 {
		t.Fatal("a dry run wrote to the rota")
	}
	if !res.DryRun || res.Summary.Planned == 0 || len(res.Turns) != res.Summary.Planned-res.Summary.KeptByHand || res.Summary.Written != 0 {
		t.Fatalf("unexpected dry-run summary %+v", res.Summary)
	}
	if res.From != "2026-11-01" || res.Month != "2026-11" {
		t.Fatalf("month %s from %s", res.Month, res.From)
	}
	for _, turn := range res.Turns {
		if turn.Name == "" {
			t.Fatalf("turn has no name for the preview: %+v", turn)
		}
	}
	// It read the whole window the chain needs, every team's absences, and
	// only the SaaS teams' turns.
	if repo.gotAssignmentReq.From != "2026-10-05" || repo.gotAssignmentReq.To != "2026-12-03" {
		t.Fatalf("read window %s..%s", repo.gotAssignmentReq.From, repo.gotAssignmentReq.To)
	}
	if strings.Join(repo.gotAssignmentReq.TeamKeys, ",") != "apollo,artemis" || repo.gotAbsenceReq.TeamKeys != nil {
		t.Fatalf("read teams %v / %v", repo.gotAssignmentReq.TeamKeys, repo.gotAbsenceReq.TeamKeys)
	}
}

func TestGenerateMonth_TheCurrentMonthStartsToday(t *testing.T) {
	repo := &fakeScheduleRepo{ledTeams: []string{"apollo"}}
	svc := newTestRotaService(repo, &fakeRotaWriter{}, saasTestNow)
	req := generateReq("2026-10")
	req.DryRun = true
	res, err := svc.GenerateMonth(leadCtx("apollo.lead@example.com"), req)
	if err != nil {
		t.Fatal(err)
	}
	if res.From != "2026-10-09" {
		t.Fatalf("from = %s, want today", res.From)
	}
	for _, turn := range res.Turns {
		if turn.RotaDate < "2026-10-09" {
			t.Fatalf("turn written in the past: %+v", turn)
		}
	}
}

func TestGenerateMonth_AGeneratedMonthNeedsRegenerate(t *testing.T) {
	tier := "L1"
	generated := domain.ScheduleAssignment{
		ID:       "gen-1",
		Engineer: domain.ScheduleEngineer{UserID: "apollo-01"}, TeamKey: "apollo",
		ShiftCode: saasShiftTZ1L1, Tier: &tier, RotaDate: "2026-11-03", Source: "GENERATED",
	}
	repo := &fakeScheduleRepo{ledTeams: []string{"apollo"}, assignments: []domain.ScheduleAssignment{generated}}
	writer := &fakeRotaWriter{owned: repository.GeneratedRowIDs{Assignments: map[string]bool{"gen-1": true}}}
	svc := newTestRotaService(repo, writer, saasTestNow)

	_, err := svc.GenerateMonth(leadCtx("apollo.lead@example.com"), generateReq("2026-11"))
	var conflict *apierror.ConflictError
	if !errors.As(err, &conflict) || writer.calls != 0 {
		t.Fatalf("want a 409 and nothing written, got %v (writes %d)", err, writer.calls)
	}

	req := generateReq("2026-11")
	req.Regenerate = true
	f := "2026-11-16"
	req.From = &f
	res, err := svc.GenerateMonth(leadCtx("apollo.lead@example.com"), req)
	if err != nil {
		t.Fatal(err)
	}
	w := writer.got
	if writer.calls != 1 || w.From != "2026-11-16" || w.To != "2026-11-30" || w.ActorEmail != "apollo.lead@example.com" {
		t.Fatalf("write bounds %s..%s by %s", w.From, w.To, w.ActorEmail)
	}
	if strings.Join(w.TeamKeys, ",") != "apollo,artemis" {
		t.Fatalf("teams %v", w.TeamKeys)
	}
	if want := repository.RotaGeneratorOwner("SRE_SAAS"); w.Owner != want || writer.ownerAsked != want {
		t.Fatalf("owner written %q, asked %q, want %q", w.Owner, writer.ownerAsked, want)
	}
	// Regular hours are in its replace only because it writes SUP itself; the
	// owner keeps a lead's own marks out of it (checked against Postgres in
	// schedule_generate_repo_integration_test.go).
	if !strings.Contains(strings.Join(w.ShiftCodes, ","), saasShiftTZ3Regular) {
		t.Fatalf("its own SUP must be replaced too: %v", w.ShiftCodes)
	}
	for _, turn := range w.Turns {
		if turn.RotaDate < "2026-11-16" {
			t.Fatalf("turn before from: %+v", turn)
		}
	}
	// It owns the lieu of every weekend it writes a day of, and no other.
	wantNotes := []string{"2026-11-21", "2026-11-28"}
	if len(w.LieuNotes) != len(wantNotes) {
		t.Fatalf("lieu notes %v", w.LieuNotes)
	}
	for i, sat := range wantNotes {
		if w.LieuNotes[i] != saasLieuNote(sat) {
			t.Fatalf("lieu notes %v", w.LieuNotes)
		}
	}
	if res.Summary.Replaced != 7 || res.Summary.Written != len(w.Turns) {
		t.Fatalf("summary %+v", res.Summary)
	}
}

// A GENERATED row the generator did not write -- the local seed, a rota
// import -- is somebody else's turn: it does not make the month "already
// generated", and it is kept like a turn set by hand.
func TestGenerateMonth_GeneratedRowsItDidNotWriteAreNotItsOwn(t *testing.T) {
	tier := "L1"
	imported := domain.ScheduleAssignment{
		ID:       "import-1",
		Engineer: domain.ScheduleEngineer{UserID: "apollo-05"}, TeamKey: "apollo",
		ShiftCode: saasShiftTZ1L1, Tier: &tier, RotaDate: "2026-11-04", Source: "GENERATED",
	}
	repo := &fakeScheduleRepo{ledTeams: []string{"apollo"}, assignments: []domain.ScheduleAssignment{imported}}
	writer := &fakeRotaWriter{}
	svc := newTestRotaService(repo, writer, saasTestNow)
	res, err := svc.GenerateMonth(leadCtx("apollo.lead@example.com"), generateReq("2026-11"))
	if err != nil {
		t.Fatalf("an imported GENERATED row must not make the month already generated: %v", err)
	}
	if res.AlreadyGenerated || res.Summary.KeptByHand == 0 {
		t.Fatalf("alreadyGenerated %v, keptByHand %d", res.AlreadyGenerated, res.Summary.KeptByHand)
	}
	for _, turn := range writer.got.Turns {
		if turn.RotaDate == "2026-11-04" && turn.TeamKey == "apollo" && turn.ShiftCode == saasShiftTZ1L1 {
			t.Fatalf("generated over the imported turn: %+v", turn)
		}
	}
}

// No SUP is written over a day somebody else set for that person, and the
// generator's own earlier TZ3 SUP is not read as a lead marking them on nights.
func TestGenerateMonth_SUPLeavesDaysSetByOthersAndIsNotAMark(t *testing.T) {
	handSUP := domain.ScheduleAssignment{
		ID:       "hand-sup",
		Engineer: domain.ScheduleEngineer{UserID: "apollo-05"}, TeamKey: "apollo",
		ShiftCode: saasShiftTZ2Regular, RotaDate: "2026-11-04", Source: "MANUAL",
	}
	var ownTZ3 []domain.ScheduleAssignment
	owned := map[string]bool{}
	for d := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC); d.Before(time.Date(2026, 12, 5, 0, 0, 0, 0, time.UTC)); d = d.AddDate(0, 0, 1) {
		if saasDow(d) >= 5 {
			continue
		}
		id := "own-tz3-" + saasISO(d)
		owned[id] = true
		ownTZ3 = append(ownTZ3, domain.ScheduleAssignment{
			ID: id, Engineer: domain.ScheduleEngineer{UserID: "artemis-07"}, TeamKey: "artemis",
			ShiftCode: saasShiftTZ3Regular, RotaDate: saasISO(d), Source: "GENERATED",
		})
	}
	repo := &fakeScheduleRepo{ledTeams: []string{"apollo"}, assignments: append([]domain.ScheduleAssignment{handSUP}, ownTZ3...)}
	writer := &fakeRotaWriter{owned: repository.GeneratedRowIDs{Assignments: owned}}
	svc := newTestRotaService(repo, writer, saasTestNow)
	req := generateReq("2026-11")
	req.Regenerate = true
	if _, err := svc.GenerateMonth(leadCtx("apollo.lead@example.com"), req); err != nil {
		t.Fatal(err)
	}
	nights := 0
	for _, turn := range writer.got.Turns {
		if turn.UserID == "apollo-05" && turn.RotaDate == "2026-11-04" && turn.Tier == "" {
			t.Fatalf("a SUP written over the day a lead set: %+v", turn)
		}
		if turn.UserID == "artemis-07" && turn.ShiftCode == saasShiftTZ3 {
			nights++
		}
	}
	// Read as a mark, artemis-07 would be the team's only night person all month.
	if nights > 10 {
		t.Fatalf("its own TZ3 SUP was read as a night mark: artemis-07 has %d night turns", nights)
	}
}

func TestGenerateMonth_SlotsFilledByHandAreLeftAlone(t *testing.T) {
	tier := "L1"
	byHand := domain.ScheduleAssignment{
		Engineer: domain.ScheduleEngineer{UserID: "apollo-05"}, TeamKey: "apollo",
		ShiftCode: saasShiftTZ1L1, Tier: &tier, RotaDate: "2026-11-04", Source: "MANUAL",
	}
	repo := &fakeScheduleRepo{ledTeams: []string{"apollo"}, assignments: []domain.ScheduleAssignment{byHand}}
	writer := &fakeRotaWriter{}
	svc := newTestRotaService(repo, writer, saasTestNow)
	res, err := svc.GenerateMonth(leadCtx("apollo.lead@example.com"), generateReq("2026-11"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Summary.KeptByHand == 0 {
		t.Fatal("the hand-filled slot was not counted as kept")
	}
	for _, turn := range writer.got.Turns {
		if turn.RotaDate == "2026-11-04" && turn.TeamKey == "apollo" && turn.ShiftCode == saasShiftTZ1L1 {
			t.Fatalf("generated a second TZ1 L1 over the lead's: %+v", turn)
		}
		if turn.RotaDate == "2026-11-04" && turn.UserID == "apollo-05" &&
			(turn.ShiftCode == saasShiftTZ1 || turn.ShiftCode == saasShiftTZ1L1) {
			t.Fatalf("gave apollo-05 an overlapping turn: %+v", turn)
		}
	}
}

// One person works one zone a day: somebody a lead put on TZ1 by hand is
// given nothing else that day -- no TZ2 or TZ3 turn, no L3, no SUP -- and the
// slots they would have taken go to others.
func TestGenerateMonth_ADaySetByHandIsThePersonsOnlyZone(t *testing.T) {
	tier := "L1"
	byHand := domain.ScheduleAssignment{
		ID: "hand-tz1", Engineer: domain.ScheduleEngineer{UserID: "apollo-05"}, TeamKey: "apollo",
		ShiftCode: saasShiftTZ1L1, Tier: &tier, RotaDate: "2026-11-04", Source: "MANUAL",
	}
	repo := &fakeScheduleRepo{ledTeams: []string{"apollo"}, assignments: []domain.ScheduleAssignment{byHand}}
	writer := &fakeRotaWriter{}
	svc := newTestRotaService(repo, writer, saasTestNow)
	if _, err := svc.GenerateMonth(leadCtx("apollo.lead@example.com"), generateReq("2026-11")); err != nil {
		t.Fatal(err)
	}
	for _, turn := range writer.got.Turns {
		if turn.UserID == "apollo-05" && turn.RotaDate == "2026-11-04" {
			t.Fatalf("given %+v on a day already set by hand", turn)
		}
	}
	for _, zone := range []string{saasShiftTZ2, saasShiftTZ3} {
		n := 0
		for _, turn := range writer.got.Turns {
			if turn.TeamKey == "apollo" && turn.RotaDate == "2026-11-04" && turn.ShiftCode == zone && turn.Tier == saasTierL3 {
				n++
			}
		}
		if n != 1 {
			t.Fatalf("apollo %s L3 on 2026-11-04: want one, got %d", zone, n)
		}
	}
}

func TestGenerateMonth_TheChosenTZ3CrewReachesTheGenerator(t *testing.T) {
	repo := &fakeScheduleRepo{ledTeams: []string{"apollo"}}
	writer := &fakeRotaWriter{}
	svc := newTestRotaService(repo, writer, saasTestNow)
	req := generateReq("2026-11")
	req.NightCrew = map[string][]string{"apollo": {"apollo-02", "apollo-05", " apollo-05 ", "apollo-08"}}
	if _, err := svc.GenerateMonth(leadCtx("apollo.lead@example.com"), req); err != nil {
		t.Fatal(err)
	}
	crew := map[string]bool{"apollo-02": true, "apollo-05": true, "apollo-08": true}
	for _, turn := range writer.got.Turns {
		if turn.TeamKey == "apollo" && turn.ShiftCode == saasShiftTZ3 && turn.RotaDate == "2026-11-04" && !crew[turn.UserID] {
			t.Fatalf("apollo TZ3 on 4 Nov went outside the chosen crew: %+v", turn)
		}
	}
}

func TestGenerateMonth_ItsOwnEarlierLieuIsNotAvailability(t *testing.T) {
	// An earlier run's lieu for the weekend of 7 November, on someone who would
	// otherwise work that Monday: regenerating must not read it as leave.
	note := saasLieuNote("2026-11-07")
	ends := "2026-11-10"
	own := domain.ScheduleAbsence{
		ID:       "lieu-own",
		Engineer: domain.ScheduleEngineer{UserID: "artemis-03"}, TeamKey: "artemis",
		KindCode: "LIEU_LEAVE", StartsOn: "2026-11-09", EndsOn: &ends, Note: &note,
	}
	other := own
	other.ID = "lieu-other"
	otherNote := "booked by the lead"
	other.Note = &otherNote
	// Lieu a lead booked by hand with the very note the generator writes is
	// still the lead's: only the generator's own rows are set aside.
	sameNote := own
	sameNote.ID = "lieu-by-hand"

	plan := func(abs ...domain.ScheduleAbsence) map[string]bool {
		repo := &fakeScheduleRepo{ledTeams: []string{"artemis"}, absences: abs}
		writer := &fakeRotaWriter{owned: repository.GeneratedRowIDs{Absences: map[string]bool{"lieu-own": true}}}
		svc := newTestRotaService(repo, writer, saasTestNow)
		req := generateReq("2026-11")
		req.DryRun = true
		res, err := svc.GenerateMonth(leadCtx("artemis.lead@example.com"), req)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]bool{}
		for _, turn := range res.Turns {
			if turn.UserID == "artemis-03" {
				out[turn.RotaDate] = true
			}
		}
		return out
	}
	// Leave a lead booked is respected; the generator's own is not leave.
	if days := plan(other); days["2026-11-09"] || days["2026-11-10"] {
		t.Fatalf("worked through leave a lead booked: %v", days)
	}
	if days := plan(sameNote); days["2026-11-09"] || days["2026-11-10"] {
		t.Fatalf("worked through lieu a lead booked with the generator's note: %v", days)
	}
	// The generator's own lieu is ignored: the month is the one it would be with
	// no absence at all.
	if fmt.Sprint(plan(own)) != fmt.Sprint(plan()) {
		t.Fatalf("the generator's own earlier lieu changed the month: %v vs %v", plan(own), plan())
	}
}
