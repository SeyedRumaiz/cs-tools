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
// KIND, either express or implied. See the License for the
// specific language governing permissions and limitations
// under the License.

package service

import (
	"context"
	"log/slog"
	"sort"
	"time"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/repository"
)

const (
	// salesforceIngestRetryMaxAttempts caps how many times one record is
	// re-run before the job leaves it alone: every failed re-run bumps the
	// row's retry_count (only this job does), so a parent that never arrives
	// stops costing a Sales Entity round trip after this many ticks (an hour
	// at the 5m default). The row stays FAILED and visible; ingesting the
	// parent resets retry_count (RequeueMissingParentFailures).
	salesforceIngestRetryMaxAttempts = 12
	// salesforceIngestRetryBatchSize bounds one tick's work per source table.
	salesforceIngestRetryBatchSize = 100
	// salesforceIngestRetryTimeout bounds one record's re-run, so a hung
	// Sales Entity call cannot stall the whole batch past the next tick.
	salesforceIngestRetryTimeout = 30 * time.Second
)

// MembershipReingester re-runs the membership ingest for one Salesforce
// Project_Contact__c id as if an UPDATED event had arrived — the
// membership-ingest-enabled SalesforceEventService implements it.
type MembershipReingester interface {
	RetryMembershipIngest(ctx context.Context, membershipSfID string) error
}

// SalesforceIngestRetryWorker periodically re-runs Salesforce ingests that
// FAILED because the record's parent was not in CSM yet. Service Bus
// redelivers a failed envelope five times within seconds and then
// dead-letters it, but today a new project reaches CSM only through the
// ServiceNow sync, up to five minutes later — so a membership invited on a
// brand-new project dead-letters before its project exists. This job is the
// second chance: on every tick it reads the DATABASE onboarding_step rows
// whose last_error says "project not found" / "account not found"
// (repository.OnboardingStepRepository.ListMissingParentFailures), skips
// anything written within the last interval (the parent needs time to
// arrive) or already at the retry cap, and calls
// MembershipReingester.RetryMembershipIngest for each. The re-run is the
// ordinary ingest, so it reads the current record from Sales Entity, writes
// the row, records the step and publishes project_contact.invited when the
// membership moved into an invited state; a failed re-run then counts
// against the cap (RecordRetryAttempt), which redeliveries never touch.
//
// The salesforce_ingest_state ledger (accounts, projects, opportunities) is
// read the same way through EntityRetriers: a FAILED missing-parent row is
// handed to the retrier registered for its entity (routes.go registers
// opportunity and contact); rows of an entity without one are only counted.
//
// Modeled on SLAEngineRecomputeWorker's Run loop — same
// shutdown-by-context-cancellation shape — but the first tick waits one
// interval: a row must be older than the interval to qualify anyway, and a
// rolling restart should not fire a burst of Sales Entity calls.
type SalesforceIngestRetryWorker struct {
	Steps       repository.OnboardingStepRepository
	Memberships MembershipReingester
	// States may be nil when the ledger is not wired; the job then only
	// re-runs memberships.
	States repository.SalesforceIngestStateRepository
	// EntityRetriers re-runs one FAILED ledger row, keyed by
	// salesforce_ingest_state.entity (domain.SalesforceIngestEntityAccount,
	// ...). A family that records into the ledger registers its retrier
	// here; an entity without one is skipped.
	EntityRetriers map[string]func(ctx context.Context, sfID string) error
	Interval       time.Duration
	MaxAttempts    int
	BatchSize      int
}

// NewSalesforceIngestRetryWorker constructs the worker. interval <= 0 falls
// back to five minutes, which is the ServiceNow sync's own cadence — the
// longest a parent should take to arrive.
func NewSalesforceIngestRetryWorker(steps repository.OnboardingStepRepository, memberships MembershipReingester, states repository.SalesforceIngestStateRepository, interval time.Duration) *SalesforceIngestRetryWorker {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	return &SalesforceIngestRetryWorker{
		Steps:          steps,
		Memberships:    memberships,
		States:         states,
		EntityRetriers: map[string]func(ctx context.Context, sfID string) error{},
		Interval:       interval,
		MaxAttempts:    salesforceIngestRetryMaxAttempts,
		BatchSize:      salesforceIngestRetryBatchSize,
	}
}

// Run retries on every tick until ctx is cancelled.
func (w *SalesforceIngestRetryWorker) Run(ctx context.Context) {
	slog.InfoContext(ctx, "salesforce: ingest retry worker started", "interval", w.Interval, "maxAttempts", w.MaxAttempts)
	for {
		select {
		case <-ctx.Done():
			slog.InfoContext(ctx, "salesforce: ingest retry worker stopped")
			return
		case <-time.After(w.Interval):
		}
		w.RunOnce(ctx)
	}
}

// RunOnce performs one tick: memberships first, then the ledger. Errors are
// logged, never returned — a transient database error must not take the
// worker down for the life of the process; the next tick tries again.
func (w *SalesforceIngestRetryWorker) RunOnce(ctx context.Context) {
	w.retryMemberships(ctx)
	w.retryLedger(ctx)
}

func (w *SalesforceIngestRetryWorker) retryMemberships(ctx context.Context) {
	if w.Steps == nil || w.Memberships == nil {
		return
	}
	steps, err := w.Steps.ListMissingParentFailures(ctx, w.Interval, w.MaxAttempts, w.BatchSize)
	if err != nil {
		slog.ErrorContext(ctx, "salesforce: ingest retry: list failed membership steps", "err", err)
		return
	}
	if len(steps) == 0 {
		return
	}
	succeeded := 0
	for _, st := range steps {
		if ctx.Err() != nil {
			return
		}
		slog.InfoContext(ctx, "salesforce: ingest retry: re-running membership",
			"membershipSfId", st.MembershipSfID, "attempt", st.RetryCount+1, "lastError", derefString(st.LastError), "failedOn", st.UpdatedOn)
		if err := w.retryOne(ctx, func(c context.Context) error { return w.Memberships.RetryMembershipIngest(c, st.MembershipSfID) }); err != nil {
			slog.WarnContext(ctx, "salesforce: ingest retry: membership still failing", "membershipSfId", st.MembershipSfID, "err", err)
			// Counts toward the cap and restarts the interval, also when the
			// re-run failed before recording the step (the Sales Entity fetch).
			if _, rerr := w.Steps.RecordRetryAttempt(ctx, st.ID); rerr != nil {
				slog.ErrorContext(ctx, "salesforce: ingest retry: record membership attempt", "membershipSfId", st.MembershipSfID, "err", rerr)
			}
			continue
		}
		succeeded++
		slog.InfoContext(ctx, "salesforce: ingest retry: membership re-ingested", "membershipSfId", st.MembershipSfID)
	}
	slog.InfoContext(ctx, "salesforce: ingest retry: membership pass done", "retried", len(steps), "succeeded", succeeded)
}

func (w *SalesforceIngestRetryWorker) retryLedger(ctx context.Context) {
	if w.States == nil {
		return
	}
	entities := make([]string, 0, len(w.EntityRetriers))
	for entity, retrier := range w.EntityRetriers {
		if retrier != nil {
			entities = append(entities, entity)
		}
	}
	if len(entities) == 0 {
		return
	}
	sort.Strings(entities)
	// Eligibility (a registered retrier, a missing-parent error, under the
	// cap) is applied in the query, before the batch limit, so rows this
	// job would skip can never fill the batch and starve eligible ones.
	rows, err := w.States.ListMissingParentFailures(ctx, entities, w.Interval, w.MaxAttempts, w.BatchSize)
	if err != nil {
		slog.ErrorContext(ctx, "salesforce: ingest retry: list failed ledger rows", "err", err)
		return
	}
	retried, succeeded, skipped := 0, 0, 0
	for _, row := range rows {
		if ctx.Err() != nil {
			return
		}
		retrier := w.EntityRetriers[row.Entity]
		if retrier == nil || row.RetryCount >= w.MaxAttempts || !repository.IsMissingParentError(derefString(row.LastError)) {
			skipped++
			continue
		}
		retried++
		slog.InfoContext(ctx, "salesforce: ingest retry: re-running record",
			"entity", row.Entity, "sfId", row.SfID, "attempt", row.RetryCount+1, "lastError", derefString(row.LastError), "failedOn", row.UpdatedOn)
		if err := w.retryOne(ctx, func(c context.Context) error { return retrier(c, row.SfID) }); err != nil {
			slog.WarnContext(ctx, "salesforce: ingest retry: record still failing", "entity", row.Entity, "sfId", row.SfID, "err", err)
			// Same as for memberships.
			if _, rerr := w.States.RecordRetryAttempt(ctx, row.Entity, row.SfID); rerr != nil {
				slog.ErrorContext(ctx, "salesforce: ingest retry: record ledger attempt", "entity", row.Entity, "sfId", row.SfID, "err", rerr)
			}
			continue
		}
		succeeded++
		slog.InfoContext(ctx, "salesforce: ingest retry: record re-ingested", "entity", row.Entity, "sfId", row.SfID)
	}
	if len(rows) > 0 {
		slog.InfoContext(ctx, "salesforce: ingest retry: ledger pass done", "retried", retried, "succeeded", succeeded, "skipped", skipped)
	}
}

// retryOne runs one re-run under its own timeout.
func (w *SalesforceIngestRetryWorker) retryOne(ctx context.Context, run func(context.Context) error) error {
	runCtx, cancel := context.WithTimeout(ctx, salesforceIngestRetryTimeout)
	defer cancel()
	return run(runCtx)
}

// RetryMembershipIngest implements MembershipReingester: the delayed-retry
// job's re-run of one membership, as if Salesforce had sent UPDATED for it.
// The membership's own LastModifiedDate drives the duplicate guard, and a
// FAILED step never blocks, so the re-run goes through to the upsert.
func (s *salesforceEventService) RetryMembershipIngest(ctx context.Context, membershipSfID string) error {
	if !s.membership.enabled() {
		return errMembershipIngestDisabled
	}
	return s.ingestMembership(ctx, membershipSfID, domain.SalesforceEventUpdated, nil)
}

// requeueChildrenOf resets the retry budget of the FAILED memberships and
// ledger rows waiting for parent, best-effort: the parent write has committed.
func (s *salesforceEventService) requeueChildrenOf(ctx context.Context, parent repository.MissingParent) {
	reqCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if s.membership != nil && s.membership.Steps != nil {
		if n, err := s.membership.Steps.RequeueMissingParentFailures(reqCtx, parent); err != nil {
			slog.ErrorContext(ctx, "salesforce: requeue memberships waiting for parent", "parent", parent.Kind, "sfId", parent.SfID, "err", err)
		} else if n > 0 {
			slog.InfoContext(ctx, "salesforce: requeued memberships waiting for parent", "parent", parent.Kind, "sfId", parent.SfID, "count", n)
		}
	}
	if s.support.States != nil {
		if n, err := s.support.States.RequeueMissingParentFailures(reqCtx, parent); err != nil {
			slog.ErrorContext(ctx, "salesforce: requeue ledger rows waiting for parent", "parent", parent.Kind, "sfId", parent.SfID, "err", err)
		} else if n > 0 {
			slog.InfoContext(ctx, "salesforce: requeued ledger rows waiting for parent", "parent", parent.Kind, "sfId", parent.SfID, "count", n)
		}
	}
}
