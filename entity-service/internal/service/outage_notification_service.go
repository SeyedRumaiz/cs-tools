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
	"log/slog"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/apierror"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/repository"
)

// defaultOutageSweepLimit caps one sweep. Small on purpose: the estate holds a
// few hundred outages and only those opted into notification are ever
// considered, so a sweep that needed a large limit would mean something is
// wrong rather than busy.
const defaultOutageSweepLimit = 100

type outageNotificationService struct {
	repo   repository.OutageNotificationRepository
	access AccessService
}

// NewOutageNotificationService constructs an OutageNotificationService.
func NewOutageNotificationService(repo repository.OutageNotificationRepository, access AccessService) OutageNotificationService {
	return &outageNotificationService{repo: repo, access: access}
}

// Sweep evaluates every outage awaiting internal-stakeholder notification and
// returns the emails that should go out.
//
// *** IT DECIDES AND RECORDS; IT DOES NOT SEND. *** The caller
// (operations/csm-scheduled-tasks) renders and delivers, the same division
// every report task in this platform uses: this service owns what is true
// about the estate, the scheduled task owns who hears about it.
//
// A SWEEP RATHER THAN A RECORD TRIGGER, for the reason the query-hour port
// gives: nothing in this stack writes `outage`. csm-sync-service mirrors it in
// from ServiceNow, so there is no local write to react to and no event to
// consume. Polling is not a compromise here, it is the only accurate model.
//
// Recording happens BEFORE the caller has sent anything, and that is
// deliberate. The alternative — send, then record — duplicates the email
// whenever the recording fails, and a duplicate outage notice to the whole
// internal audience is worse than a missed one. This way a crash between the
// two loses an email; the sweep does not retry it, because the phase has
// moved. Chosen knowingly: at-most-once beats at-least-once for outage mail.
func (s *outageNotificationService) Sweep(ctx context.Context, limit int) (domain.OutageNotificationSweepResponse, error) {
	if err := s.requireInternalCaller(ctx); err != nil {
		return domain.OutageNotificationSweepResponse{}, err
	}
	release, locked, err := s.repo.TryLockSweep(ctx)
	if err != nil {
		return domain.OutageNotificationSweepResponse{}, err
	}
	if !locked {
		// Another sweep holds the lock and is already sending what is owed.
		// Reporting nothing is correct: there is nothing for THIS caller to
		// send, and evaluating again would only race it.
		return domain.OutageNotificationSweepResponse{Decisions: []domain.OutageNotificationDecision{}}, nil
	}
	defer release()

	if limit <= 0 || limit > 1000 {
		limit = defaultOutageSweepLimit
	}

	outages, err := s.repo.PendingOutages(ctx, limit)
	if err != nil {
		return domain.OutageNotificationSweepResponse{}, err
	}

	out := domain.OutageNotificationSweepResponse{
		Evaluated: len(outages),
		Decisions: []domain.OutageNotificationDecision{},
	}

	for _, o := range outages {
		d := decideOutageNotification(o)
		if d.Kind == domain.OutageNotificationNone {
			// Reached when ServiceNow already resolved an outage this service
			// has never seen. Nothing to send, but a row is still written so
			// the outage leaves the pending set instead of being re-evaluated
			// on every sweep forever.
			if o.State == nil {
				if err := s.repo.RecordSent(ctx, o.OutageID, domain.OutageNotificationNone,
					o.SyncedPhase, true); err != nil { // seeded: this phase is ServiceNow's, not ours
					s.recordError(&out, o.OutageID, err)
				}
			}
			continue
		}

		// seeded is true only when this is the outage's first row AND the
		// phase it is being written with came from ServiceNow rather than
		// from an email we sent.
		seeded := o.State == nil && o.SyncedPhase != domain.OutageNotificationPhaseNone
		if err := s.repo.RecordSent(ctx, o.OutageID, d.Kind, phaseAfter(d.Kind), seeded); err != nil {
			// Recorded nothing, so send nothing: the decision is dropped from
			// the response rather than handed to a caller that would email
			// without a trace of having done so.
			s.recordError(&out, o.OutageID, err)
			continue
		}
		out.Decisions = append(out.Decisions, d)
	}

	if len(out.Decisions) > 0 {
		slog.InfoContext(ctx, "outage internal-notification sweep",
			"evaluated", out.Evaluated, "toSend", len(out.Decisions), "failed", len(out.Errors))
	}
	return out, nil
}

func (s *outageNotificationService) recordError(out *domain.OutageNotificationSweepResponse, outageID string, err error) {
	if out.Errors == nil {
		out.Errors = map[string]string{}
	}
	out.Errors[outageID] = err.Error()
	slog.Error("outage notification: recording send failed; no email will be reported for this outage",
		"outageId", outageID, "error", err)
}

// State returns what has been sent for one outage.
func (s *outageNotificationService) State(ctx context.Context, outageID string) (domain.OutageNotificationState, error) {
	if err := s.requireInternalCaller(ctx); err != nil {
		return domain.OutageNotificationState{}, err
	}
	if err := validateUUIDs("outageId", []string{outageID}); err != nil {
		return domain.OutageNotificationState{}, err
	}
	return s.repo.State(ctx, outageID)
}

// requireInternalCaller gates both operations. Neither has a project or
// account to scope to — a sweep is estate-wide by definition, and an outage's
// notification history is operational data with no customer-facing subset —
// so the answer for a scoped caller is refusal rather than a filtered view.
func (s *outageNotificationService) requireInternalCaller(ctx context.Context) error {
	scope, err := s.access.ResolveScope(ctx)
	if err != nil {
		return err
	}
	if !scope.Unrestricted {
		return &apierror.ForbiddenError{Msg: "outage notification is only available to internal services"}
	}
	return nil
}
