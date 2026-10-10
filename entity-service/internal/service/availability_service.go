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
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/repository"
)

// The availability sweep: the Go replacement for ServiceNow's nightly
// "Calculate Availability" job.
//
// *** THIS IS THE PRODUCER HALF, AND IT IS THE HALF THAT WAS MISSING. ***
// The Cloud Status Dashboard's three endpoints are already ported and read
// service_availability out of Postgres. Nothing in Postgres WRITES it:
// csm-sync-service mirrors the rows from ServiceNow, so at cutover the
// dashboard's uptime figures would simply stop advancing — with no error
// anywhere, because reading a table nobody updates looks exactly like
// reading a table where nothing happened.

// AvailabilitySweepResult reports what one run did.
type AvailabilitySweepResult struct {
	Subjects int `json:"subjects"`
	Rows     int `json:"rows"`
	Failed   int `json:"failed"`
}

// AvailabilityService recomputes availability for every committed subject.
type AvailabilityService interface {
	Sweep(ctx context.Context, now time.Time) (AvailabilitySweepResult, error)
}

// DefaultAvailabilityTimezone is the zone period boundaries resolve in.
//
// *** NOT THE COMMITMENT'S TIMEZONE, AND THAT IS MEASURED, NOT ASSUMED. ***
// Every commitment on both instances records GMT, and an earlier version of
// this service passed that straight to the segment builder. Checked against
// the real stored periods, that reproduces ZERO of production's 68 boundaries
// and 27 of dev's 68. Asia/Colombo reproduces 68 of 68 on production.
//
// The reason is in the ServiceNow source. v1 — which is what both instances
// actually run — applies the commitment timezone ONLY to the GlideSchedule
// (`answer.setTimeZone(tz)`); its period boundaries come from
// gs.beginningOfDay() and friends, which resolve in the SYSTEM zone,
// glide.sys.default.tz = Asia/Colombo. Only v2 sets the session zone from
// the commitment, and v2 has never been switched on.
//
// So honouring commitment.timezone would silently re-date four years of
// history by five and a half hours, move outages between days on the
// dashboard's daily chart, and stop the natural-key upsert matching any
// mirrored row. The zone is therefore explicit configuration with the
// observed production default, rather than inherited from a field nothing
// has ever honoured.
const DefaultAvailabilityTimezone = "Asia/Colombo"

type availabilityService struct {
	repo repository.AvailabilityRepository
	loc  *time.Location
}

// NewAvailabilityService constructs the sweep. An empty timezone uses
// DefaultAvailabilityTimezone; an unknown one is a startup error rather than
// a silent fallback, because the wrong zone mis-dates every row it writes.
func NewAvailabilityService(repo repository.AvailabilityRepository, timezone string) (AvailabilityService, error) {
	if timezone == "" {
		timezone = DefaultAvailabilityTimezone
	}
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, fmt.Errorf("availability: unknown timezone %q: %w", timezone, err)
	}
	return &availabilityService{repo: repo, loc: loc}, nil
}

// Sweep recomputes every period for every subject.
//
// One subject failing does not abort the run. ServiceNow's calculator
// swallows per-subject errors too (`catch (e) { gs.info(...) }`), and the
// reasoning holds here for a different reason: this writes the numbers a
// customer-facing status page reads, and one offering with bad data must not
// freeze every other offering's uptime at yesterday's value.
func (s *availabilityService) Sweep(ctx context.Context, now time.Time) (AvailabilitySweepResult, error) {
	subjects, err := s.repo.Subjects(ctx)
	if err != nil {
		return AvailabilitySweepResult{}, fmt.Errorf("availability: load subjects: %w", err)
	}

	var res AvailabilitySweepResult
	res.Subjects = len(subjects)

	for _, subject := range subjects {
		written, err := s.sweepSubject(ctx, subject, now)
		if err != nil {
			res.Failed++
			// The commitment id, never the offering name: this log line
			// goes to a shared sink and an offering name is close enough to
			// customer-identifying to keep out of it.
			slog.ErrorContext(ctx, "availability: subject failed",
				"commitment", subject.ServiceCommitmentID, "err", err)
			continue
		}
		res.Rows += written
	}
	return res, nil
}

func (s *availabilityService) sweepSubject(
	ctx context.Context, subject repository.AvailabilitySubject, now time.Time,
) (int, error) {
	subjectID := subject.SubjectID()
	if subjectID == "" {
		// Neither an offering nor a CI. ServiceNow's "Only Offering or CI"
		// rule should prevent it, and digiops-cs migration 0124 adds a CHECK
		// that enforces it — but the mirror can be mid-backfill, so this
		// reports rather than panics on a nil deref.
		return 0, fmt.Errorf("commitment %s has neither a service offering nor a CI", subject.ServiceCommitmentID)
	}
	if subject.ServiceOfferingID == nil {
		// *** A CI-ONLY SUBJECT CANNOT BE STORED, SO IT FAILS. ***
		// ServiceNow lets a commitment hold a CI instead of an offering, but
		// service_availability (migration 0084) has no cmdb_ci column: a row
		// written for it would carry no subject at all, and the natural-key
		// and rolling deletes could not tell two such subjects apart. None
		// exists today -- all 146 staging links are offerings, and
		// cmdb_ci_id is empty on every one -- so this only fires if one
		// appears, and then visibly, in the sweep's failed count.
		return 0, fmt.Errorf("commitment %s is held by a CI (%s), not a service offering: service_availability has no CI column to store it",
			subject.ServiceCommitmentID, subjectID)
	}

	schedule, err := s.scheduleFor(ctx, subject)
	if err != nil {
		return 0, err
	}

	// s.loc, NOT the commitment's zone — see DefaultAvailabilityTimezone.
	segments := append(AvailabilitySegmentsFor(now, s.loc), PreviousFixedSegments(now, s.loc)...)

	// Outages are fetched ONCE over the widest window and reused, rather
	// than queried per segment. Twelve segments per subject (the eight
	// containing now plus the four just-closed fixed periods) times ~146
	// subjects is 1,752 queries a run against a table the dashboard is also
	// reading; one query per subject is 146. The calculator trims to each
	// segment itself, so the result is identical.
	widest := segments[0]
	for _, seg := range segments {
		if seg.Begin.Before(widest.Begin) {
			widest.Begin = seg.Begin
		}
		if seg.End.After(widest.End) {
			widest.End = seg.End
		}
	}
	outageRows, err := s.repo.OutagesFor(ctx, subjectID, widest.Begin, widest.End)
	if err != nil {
		return 0, err
	}
	outages := make([]AvailabilityOutage, 0, len(outageRows))
	for _, o := range outageRows {
		outages = append(outages, AvailabilityOutage{
			Begin: o.Begin, End: o.End, Type: normaliseOutageType(o.Type),
		})
	}

	var fixed, rolling []repository.ComputedAvailabilityRow
	var rollingTypes []string

	for _, seg := range segments {
		result := CalculateAvailability(AvailabilityInputs{
			Begin: seg.Begin, End: seg.End,
			Outages:       outages,
			Schedule:      schedule,
			TargetPercent: subject.TargetPercent,
		})

		row := repository.ComputedAvailabilityRow{
			ServiceOfferingID:   subject.ServiceOfferingID,
			CmdbCiID:            subject.CmdbCiID,
			ServiceCommitmentID: subject.ServiceCommitmentID,
			Type:                seg.Type,
			Begin:               seg.Begin,
			End:                 seg.End,
			TimeZone:            subject.Timezone,
			AbsoluteDowntime:    result.AbsoluteDowntime,
			ScheduledDowntime:   result.ScheduledDowntime,
			ScheduledTotal:      result.ScheduledTotal,
			AbsoluteAvail:       roundAvailability(result.AbsoluteAvailability),
			ScheduledAvail:      roundAvailability(result.ScheduledAvailability),
			AbsoluteCount:       result.AbsoluteCount,
			ScheduledCount:      result.ScheduledCount,
			MTBF:                result.MTBF,
			MTRS:                result.MTRS,
			AllowedDowntime:     result.AllowedDowntime,
			CommitmentMet:       result.CommitmentMet,
		}

		if seg.Rolling {
			rolling = append(rolling, row)
			rollingTypes = append(rollingTypes, seg.Type)
			continue
		}
		fixed = append(fixed, row)
	}

	if err := s.repo.UpsertFixed(ctx, fixed); err != nil {
		return 0, err
	}
	if err := s.repo.ReplaceRolling(ctx, subjectID, subject.ServiceCommitmentID, rollingTypes, rolling); err != nil {
		return len(fixed), err
	}
	return len(fixed) + len(rolling), nil
}

// normaliseOutageType maps the Postgres enum onto the lowercase values the
// calculator compares against.
//
// *** THE TWO VOCABULARIES ARE NOT THE SAME SIZE. *** Postgres has
// DEGRADATION | OUTAGE | PLANNED; ServiceNow's availability engine knows
// only outage and planned. DEGRADATION maps to neither on purpose — it falls
// through to a value the accumulator ignores, which is exactly what
// ServiceNow does with it. 115 of the instance's 635 outages are
// degradations and none of them has ever moved a percentage.
func normaliseOutageType(t string) string {
	switch t {
	case "OUTAGE", "outage":
		return OutageTypeOutage
	case "PLANNED", "planned":
		return OutageTypePlanned
	default:
		return t
	}
}

func (s *availabilityService) scheduleFor(
	ctx context.Context, subject repository.AvailabilitySubject,
) (AvailabilitySchedule, error) {
	// No schedule means unrestricted. ServiceNow treats a null schedule and
	// a 24x7 one identically, and every commitment on the instance today
	// points at the stock "24 x 7" record — which is why every stored row
	// has an AST of exactly 24 hours.
	if subject.ScheduleID == nil {
		return AlwaysOn{}, nil
	}
	if subject.ScheduleName != nil && isTwentyFourSeven(*subject.ScheduleName) {
		return AlwaysOn{}, nil
	}

	// *** ANY OTHER SCHEDULE FAILS THE SUBJECT, ON PURPOSE. ***
	// Its spans cannot be read: cmn_schedule_span.yaml syncs only CSM rota
	// span types, so a commitment schedule's spans never reach schedule_span,
	// and the repeat/show-as columns NewSpanSchedule needs are not mirrored
	// either. Computing such a subject as 24x7 would publish a wrong figure
	// with no error anywhere; failing it shows up in the sweep's `failed`
	// count and names the schedule. NewSpanSchedule is ready for the day
	// those spans are synced.
	name := "(unknown)"
	if subject.ScheduleName != nil {
		name = *subject.ScheduleName
	}
	return nil, fmt.Errorf("availability: commitment %s is measured against schedule %q, which is not modelled: "+
		"only \"24 x 7\" is supported until commitment schedules' spans are synced", subject.ServiceCommitmentID, name)
}

// isTwentyFourSeven reports whether a schedule name is ServiceNow's stock
// "24 x 7" schedule, tolerating case and spacing ("24x7", "24 X 7").
func isTwentyFourSeven(name string) bool {
	return strings.ReplaceAll(strings.ToLower(name), " ", "") == "24x7"
}

// availabilityStoredDecimals is the scale ServiceNow stores availability
// percentages at: every downtime row in staging's copy of service_availability
// has at most five decimals (99.99946, 98.92976, 99.9954), whatever the
// commitment's precision says. /monitors passes the stored value straight
// through, so an unrounded 99.99945987654321 would reach the status page.
const availabilityStoredDecimals = 5

// roundAvailability rounds half away from zero to availabilityStoredDecimals.
// NaN and the infinities pass through untouched, so the calculator's own
// guards still see them.
func roundAvailability(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return v
	}
	p := math.Pow(10, availabilityStoredDecimals)
	return math.Round(v*p) / p
}
