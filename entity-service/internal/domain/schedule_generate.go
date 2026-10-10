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

package domain

// GenerateRotaMonthRequest asks for one month of a rota to be worked out from
// the availability leads have marked, and (unless DryRun) written.
type GenerateRotaMonthRequest struct {
	// RotaCode is taken from the path: only SRE_SAAS can be generated today.
	RotaCode string `json:"-"`
	// Month is YYYY-MM. The current month or a later one.
	Month string `json:"month"`
	// DryRun works the month out and returns it without writing anything.
	DryRun bool `json:"dryRun,omitempty"`
	// Regenerate replaces the turns an earlier generation wrote, from From on.
	// Without it, a month that already has generated turns is refused.
	Regenerate bool `json:"regenerate,omitempty"`
	// From is the first day (YYYY-MM-DD) turns are written for. Defaults to
	// the first of the month, or today when the month has started. A day in
	// the past is refused: the rota that was worked is history.
	From *string `json:"from,omitempty"`
	// NightCrew is team key -> user ids: the people a lead chose to work that
	// team's TZ3 for the month. Their TZ3 L1, L2 and L3 go round them night by
	// night, and they work no other zone. A team left out keeps the usual
	// nights (TZ3 hours marked, or the rotation). Each id must be a member of
	// that team.
	NightCrew map[string][]string `json:"nightCrew,omitempty"`
}

// GeneratedRotaTurn is one turn of the month, named for the preview.
type GeneratedRotaTurn struct {
	UserID    string `json:"userId"`
	Name      string `json:"name"`
	TeamKey   string `json:"teamKey"`
	ShiftCode string `json:"shiftCode"`
	Tier      string `json:"tier"`
	RotaDate  string `json:"rotaDate"`
}

// GeneratedLieuLeave is the lieu leave a weekend on-call earns.
type GeneratedLieuLeave struct {
	UserID   string `json:"userId"`
	Name     string `json:"name"`
	TeamKey  string `json:"teamKey"`
	StartsOn string `json:"startsOn"`
	EndsOn   string `json:"endsOn"`
}

// RotaGenerationWarning is a slot nobody could fill, or a gap for a lead to
// look at. Date is absent for a warning about the whole month.
type RotaGenerationWarning struct {
	Date    *string `json:"date,omitempty"`
	Message string  `json:"message"`
}

// RotaGenerationSummary counts what the generation did, or would do.
type RotaGenerationSummary struct {
	// Planned is every turn the month should hold from From on.
	Planned int `json:"planned"`
	// KeptByHand is planned turns not written because a lead already filled
	// that slot, or the person already holds a turn then, by hand.
	KeptByHand int `json:"keptByHand"`
	// Written is turns written. Equal to Planned - KeptByHand on a real run,
	// unless the rota changed between the read and the write; 0 on a dry run.
	Written int `json:"written"`
	// Replaced is earlier generated turns removed by a regeneration.
	Replaced int `json:"replaced"`
	// LieuPlanned and LieuWritten are the lieu leave spans, likewise.
	LieuPlanned int `json:"lieuPlanned"`
	LieuWritten int `json:"lieuWritten"`
}

// GenerateRotaMonthResponse is the month: what was (or would be) written.
type GenerateRotaMonthResponse struct {
	RotaCode string `json:"rotaCode"`
	Month    string `json:"month"`
	From     string `json:"from"`
	DryRun   bool   `json:"dryRun"`
	// AlreadyGenerated is true when the month already holds generated turns:
	// a real run then needs regenerate.
	AlreadyGenerated bool                    `json:"alreadyGenerated"`
	Summary          RotaGenerationSummary   `json:"summary"`
	Turns            []GeneratedRotaTurn     `json:"turns"`
	LieuLeave        []GeneratedLieuLeave    `json:"lieuLeave"`
	Warnings         []RotaGenerationWarning `json:"warnings"`
}
