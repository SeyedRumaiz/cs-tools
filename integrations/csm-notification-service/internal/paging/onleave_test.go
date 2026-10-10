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

package paging

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

// onLeave: skip (the default) leaves out someone on leave who holds a rung;
// call rings them -- after anyone available on the same tier.

// CRE: responders come from membership, which knows nothing of leave, so the
// rung is filtered by who is away on the incident's IST date.
func TestOnLeave_CREResponders(t *testing.T) {
	stub := &stubScheduleReader{
		members: []teamMember{
			member("vega", "zara@example.com", "engineer", "T1"),
			member("vega", "mina@example.com", "engineer", "T2"),
		},
		away: map[string]bool{"zara@example.com": true},
	}
	at := time.Date(2026, 10, 8, 20, 0, 0, 0, time.UTC) // 01:30 IST on the 9th
	rc := RoutingContext{Shift: ShiftLK, AssignedCRETeam: "vega", At: at}

	got, err := testResolver(stub).WithAlertDuty(nil, 0).Resolve(context.Background(), Level0, rc)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(emails(got), []string{"mina@example.com"}) {
		t.Errorf("skip: LEVEL_0 = %v; want the T1 on leave left out", emails(got))
	}
	if stub.gotAwayDay != "2026-10-09" {
		t.Errorf("leave read for %q; want the incident's date in IST, 2026-10-09", stub.gotAwayDay)
	}

	rc.CallOnLeave = true
	got, _ = testResolver(stub).WithAlertDuty(nil, 0).Resolve(context.Background(), Level0, rc)
	if !slices.Equal(emails(got), []string{"zara@example.com", "mina@example.com"}) {
		t.Errorf("call: LEVEL_0 = %v; want both, the one on leave included", emails(got))
	}

	// A failing leave read never empties the rung: it is rung as it is.
	stub.awayErr = errors.New("entity-service down")
	rc.CallOnLeave = false
	got, err = testResolver(stub).WithAlertDuty(nil, 0).Resolve(context.Background(), Level0, rc)
	if err != nil || len(got) != 2 {
		t.Errorf("leave read failing: %v, %v; want both, no error", emails(got), err)
	}
}

// SRE: a tier whose only holder is on leave reaches nobody under skip and
// reaches them under call; with someone available on the tier too, call still
// picks the available one.
func TestOnLeave_SRETier(t *testing.T) {
	away := held("away-l2", "apollo", "SRE_TZ1", "L2")
	away.OnLeave = true
	rc := RoutingContext{Ladder: LadderSRE, AssignedCRETeam: "apollo", At: testClock}

	// Apollo's L2 today is a-l2; another Apollo L2 is on leave.
	stub := morningRota()
	stub.onDuty = append(stub.onDuty, away)
	for _, callOnLeave := range []bool{false, true} {
		rc.CallOnLeave = callOnLeave
		got, err := sreResolver(stub).Resolve(context.Background(), Level1, rc)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(emails(got), []string{"a-l2@example.com"}) {
			t.Errorf("callOnLeave=%v: L2 = %v; want the available a-l2", callOnLeave, emails(got))
		}
		if stub.gotIncludeOnLeave != callOnLeave {
			t.Errorf("callOnLeave=%v asked the on-duty read includeOnLeave=%v", callOnLeave, stub.gotIncludeOnLeave)
		}
	}

	// Nobody else holds L2: skip reaches nobody, call rings the one on leave.
	only := &stubScheduleReader{onDuty: []onDutyAssignment{away}}
	rc.CallOnLeave = false
	if got, _ := sreResolver(only).Resolve(context.Background(), Level1, rc); len(got) != 0 {
		t.Errorf("skip: L2 = %v; want nobody", emails(got))
	}
	rc.CallOnLeave = true
	if got, _ := sreResolver(only).Resolve(context.Background(), Level1, rc); !slices.Equal(emails(got), []string{"away-l2@example.com"}) {
		t.Errorf("call: L2 = %v; want the holder on leave", emails(got))
	}
}

// SME: the ladder's L3 is on leave. skip climbs past L3; call rings them.
func TestOnLeave_SMELadder(t *testing.T) {
	for _, c := range []struct {
		onLeave OnLeave
		want    []string
	}{
		{OnLeaveSkip, []string{"s-a", "s-l2"}},
		{OnLeaveCall, []string{"s-a", "s-l2", "s-l3"}},
	} {
		t.Run(string(c.onLeave), func(t *testing.T) {
			reader := smeTieredReader()
			for i := range reader.onDuty {
				if reader.onDuty[i].Engineer.Name == "s-l3" {
					reader.onDuty[i].OnLeave = true
				}
			}
			cfg := smeOn
			cfg.OnLeave = c.onLeave
			smeChat := &fakeChat{}
			e := smeEngine(reader, newMemStore(), &fakeNotes{}, &fakeChat{}, smeChat, cfg)
			raise(t, e, handoff("Apollo", "asgardeo", "asgardeo", testClock))
			for _, at := range []time.Duration{5 * time.Minute, 10 * time.Minute} {
				if err := e.Tick(context.Background(), testClock.Add(at)); err != nil {
					t.Fatal(err)
				}
			}
			var got []string
			for _, p := range smeChat.posted {
				got = append(got, p.RecipientName)
			}
			if !slices.Equal(got, c.want) {
				t.Errorf("SME cards = %v; want %v", got, c.want)
			}
		})
	}
}

// The file: onLeave per ladder, skip when absent, anything else refused.
func TestConfig_OnLeave(t *testing.T) {
	cfg, err := loadYAML(t, "cre: {onLeave: call}\nsre: {onLeave: SKIP}\nsme: {enabled: true}\n")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.CRE.OnLeave.Calls() || cfg.SRE.OnLeave.Calls() || cfg.SRE.OnLeave != OnLeaveSkip ||
		cfg.SME.OnLeave.Calls() || cfg.SME.OnLeave != OnLeaveSkip {
		t.Errorf("onLeave = cre %q, sre %q, sme %q", cfg.CRE.OnLeave, cfg.SRE.OnLeave, cfg.SME.OnLeave)
	}
	for _, body := range []string{"cre: {onLeave: page}\n", "sme: {onLeave: always}\n"} {
		if _, err := loadYAML(t, body); err == nil {
			t.Errorf("%q loaded; want an error", body)
		}
	}
}
