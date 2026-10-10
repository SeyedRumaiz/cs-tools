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

// defaultOutageCommSweepLimit caps one sweep. Only a handful of outages are
// ever opted in at once, so this is a guard against a runaway query rather
// than a real page size.
const defaultOutageCommSweepLimit = 100

// outageCommStatusSent is what ServiceNow writes into u_outage_status on
// every log row it creates — the literal string "sent", on both arms.
const outageCommStatusSent = "sent"

type outageCommunicationService struct {
	repo   repository.OutageCommunicationRepository
	access AccessService
}

// NewOutageCommunicationService wires the service.
func NewOutageCommunicationService(repo repository.OutageCommunicationRepository, access AccessService) OutageCommunicationService {
	return &outageCommunicationService{repo: repo, access: access}
}

// Sweep evaluates every opted-in outage and returns the emails owed.
//
// *** IT RECORDS BEFORE IT RETURNS, AND THAT ORDERING IS THE DESIGN. ***
// The communication-log row is this port's idempotency guard, so writing it
// first means a crash between the sweep and the send loses that email
// rather than repeating it. The opposite ordering — send, then record —
// turns every crash into a duplicate.
//
// This matches the internal-stakeholder notifier's choice in the same
// codebase, for the same reason: these mails go to a standing group, and a
// duplicate announcement to that group is more damaging than a gap the
// on-call will notice anyway. One model across both ports is worth more
// than optimising each separately.
//
// A row that fails to record is dropped from the response rather than
// handed to a caller that would then email with no trace of having done so.
func (s *outageCommunicationService) Sweep(ctx context.Context, limit int) (domain.OutageCommunicationSweepResponse, error) {
	if err := s.requireInternalCaller(ctx); err != nil {
		return domain.OutageCommunicationSweepResponse{}, err
	}
	release, locked, err := s.repo.TryLockSweep(ctx)
	if err != nil {
		return domain.OutageCommunicationSweepResponse{}, err
	}
	if !locked {
		// Another sweep holds the lock and is already sending what is owed.
		// Reporting nothing is correct: there is nothing for THIS caller to
		// send, and evaluating again would only race it.
		return domain.OutageCommunicationSweepResponse{Decisions: []domain.OutageCommunicationDecision{}}, nil
	}
	defer release()

	if limit <= 0 || limit > 1000 {
		limit = defaultOutageCommSweepLimit
	}

	outages, err := s.repo.PendingOutages(ctx, limit)
	if err != nil {
		return domain.OutageCommunicationSweepResponse{}, err
	}

	out := domain.OutageCommunicationSweepResponse{
		Evaluated: len(outages),
		Decisions: []domain.OutageCommunicationDecision{},
	}

	for _, o := range outages {
		d := decideOutageCommunication(o)
		if d.Kind == domain.OutageCommunicationNone {
			continue
		}

		// The type literal is ServiceNow's, verbatim. The log's generated
		// email_type_norm column collapses "Declare"/"Declared", so writing
		// the long form here is safe either way — but writing what
		// ServiceNow writes keeps the two systems' rows indistinguishable,
		// which matters while both are live.
		if err := s.repo.Record(ctx, domain.RecordOutageCommunicationRequest{
			OutageNumber: o.Number,
			EmailType:    emailTypeFor(d.Kind),
			OutageStatus: outageCommStatusSent,
			Subject:      d.Subject,
			Recipients:   "", // filled by the sender; see the note on Recipients below
			EmailContent: d.Body,
			MainContent:  mainContentFor(d.Kind),
		}); err != nil {
			slog.ErrorContext(ctx, "outage communication: could not record; dropping this decision",
				"outageId", o.OutageID, "number", o.Number, "kind", d.Kind, "err", err)
			continue
		}
		out.Decisions = append(out.Decisions, d)
	}

	return out, nil
}

// Log returns what has been said about one outage, newest first.
func (s *outageCommunicationService) Log(ctx context.Context, number string) ([]domain.OutageCommunicationLogEntry, error) {
	if err := s.requireInternalCaller(ctx); err != nil {
		return nil, err
	}
	if number == "" {
		return nil, &apierror.ValidationError{Msg: "outage number is required"}
	}
	return s.repo.LogForOutage(ctx, number)
}

// emailTypeFor maps a decision to the literal ServiceNow writes.
//
// "Resolve", not "Resolved" — the flow's step 13 uses the short form for
// the resolution and the LONG form ("Declared") for the declaration. The
// asymmetry is ServiceNow's, not a typo here.
func emailTypeFor(k domain.OutageCommunicationKind) string {
	switch k {
	case domain.OutageCommunicationDeclared:
		return "Declared"
	case domain.OutageCommunicationResolved:
		return "Resolve"
	default:
		return ""
	}
}

// mainContentFor is the one-line summary ServiceNow stores alongside the
// full body, taken verbatim from steps 6 and 13.
func mainContentFor(k domain.OutageCommunicationKind) string {
	switch k {
	case domain.OutageCommunicationDeclared:
		return "We want to inform you of an outage currently affecting our services."
	case domain.OutageCommunicationResolved:
		return "We're pleased to inform you that the outage has been fully resolved."
	default:
		return ""
	}
}

// requireInternalCaller refuses scoped callers outright.
//
// The communication log holds full email bodies and recipient lists for
// internal announcements. There is no customer-facing subset of it, so a
// scoped caller gets a refusal rather than a filtered view — the same
// stance the internal-stakeholder notifier takes.
func (s *outageCommunicationService) requireInternalCaller(ctx context.Context) error {
	scope, err := s.access.ResolveScope(ctx)
	if err != nil {
		return err
	}
	if !scope.Unrestricted {
		return &apierror.ForbiddenError{Msg: "outage communication is only available to internal services"}
	}
	return nil
}
