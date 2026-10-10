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

package main

import (
	"testing"

	"github.com/wso2-open-operations/cs-tools/integrations/csm-notification-service/internal/paging"
)

func TestEscalationStartProblem(t *testing.T) {
	cases := []struct {
		name                                             string
		rosterInvalid, rosterEmpty, teamSchedule, entity bool
		wantStart                                        bool
	}{
		// The fix: the Team Schedule is enough on its own.
		{"team schedule, no roster", false, true, true, true, true},
		{"team schedule and a roster", false, false, true, true, true},
		// Team Schedule asked for but unreachable: the roster is the fallback.
		{"team schedule without entity-service, roster set", false, false, true, false, true},
		{"team schedule without entity-service, no roster", false, true, true, false, false},
		// The roster mode is unchanged.
		{"roster", false, false, false, false, true},
		{"no roster", false, true, false, true, false},
		// A roster that does not parse is always a stop, in either mode.
		{"invalid roster, team schedule", true, false, true, true, false},
		{"invalid roster", true, false, false, false, false},
	}
	for _, c := range cases {
		problem := escalationStartProblem(c.rosterInvalid, c.rosterEmpty, c.teamSchedule, c.entity)
		if (problem == "") != c.wantStart {
			t.Errorf("%s: problem = %q, want start = %v", c.name, problem, c.wantStart)
		}
	}
}

// With no configuration file, the SRE ladder runs only on the Team Schedule:
// the roster resolver ignores which ladder asks, so an SRE engine on it would
// call the same roster people a second time.
func TestLoadEscalationConfig_NoFileRunsSREOnlyOnTheTeamSchedule(t *testing.T) {
	t.Setenv("INCIDENT_ESCALATION_CONFIG", "")
	t.Setenv("INCIDENT_ESCALATION_ENABLED", "")
	for resolver, want := range map[string]bool{"": false, "roster": false, "team-schedule": true} {
		t.Setenv("INCIDENT_ESCALATION_RESOLVER", resolver)
		cfg, err := loadEscalationConfig(paging.ChannelCall)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.SRE.Enabled != want || !cfg.CRE.Enabled {
			t.Errorf("resolver %q: SRE enabled=%v CRE enabled=%v, want SRE %v and CRE on", resolver, cfg.SRE.Enabled, cfg.CRE.Enabled, want)
		}
	}
}
