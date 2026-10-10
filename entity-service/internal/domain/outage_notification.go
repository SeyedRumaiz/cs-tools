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

import "time"

// OutageNotificationPhase is how far the internal-stakeholder notification
// has progressed for one outage. It mirrors ServiceNow's
// u_internal_notification_phase choice list exactly, because the port has to
// be able to seed from it at cutover.
type OutageNotificationPhase string

const (
	// OutageNotificationPhaseNone means no email has gone out yet. A real
	// state, not an absence — it is what 634 of 635 outages currently hold.
	OutageNotificationPhaseNone OutageNotificationPhase = "NONE"
	// OutageNotificationPhaseDeclared means the declaration email has been
	// sent. Transient in practice: an outage passes through it on its way to
	// resolved, and no outage on the source instance is sitting here.
	OutageNotificationPhaseDeclared OutageNotificationPhase = "DECLARED"
	// OutageNotificationPhaseResolved is terminal. Nothing further is sent.
	OutageNotificationPhaseResolved OutageNotificationPhase = "RESOLVED"
)

// OutageNotificationKind is which of the three emails an evaluation selected.
type OutageNotificationKind string

const (
	OutageNotificationNone     OutageNotificationKind = ""
	OutageNotificationDeclared OutageNotificationKind = "DECLARED"
	OutageNotificationResolved OutageNotificationKind = "RESOLVED"
	OutageNotificationUpdate   OutageNotificationKind = "UPDATE"
)

// OutageNotificationState is this service's own record of what it has sent
// for one outage.
//
// Deliberately separate from the `outage` table, which is sync output: see
// migration 000085 for why writing back into a mirrored table would be
// overwritten by the next sync run.
type OutageNotificationState struct {
	OutageID     string                  `json:"outageId"`
	Phase        OutageNotificationPhase `json:"phase"`
	DeclaredOn   *time.Time              `json:"declaredOn,omitempty"`
	ResolvedOn   *time.Time              `json:"resolvedOn,omitempty"`
	LastUpdateOn *time.Time              `json:"lastUpdateOn,omitempty"`
	UpdateCount  int                     `json:"updateCount"`
	// SeededFromSync is true when Phase came from ServiceNow's mirrored value
	// rather than from an email this service sent. It is the difference
	// between "we announced this" and "ServiceNow announced this before
	// cutover", which is the first question anyone asks about a declared
	// outage with no matching send.
	SeededFromSync bool `json:"seededFromSync"`
}

// OutageForNotification is one outage as the notifier needs to see it: the
// fields the three branch conditions read, plus what the email renders.
type OutageForNotification struct {
	OutageID string     `json:"outageId"`
	Number   string     `json:"number"`
	Name     string     `json:"name"`
	Message  string     `json:"message"`
	Type     string     `json:"type"`
	StartOn  *time.Time `json:"startOn,omitempty"`
	// EndOn being non-nil is the entire resolution signal. ServiceNow has no
	// separate state field on an outage — "closing an outage is done by
	// setting End", as entity-service's own OutageService already documents.
	EndOn *time.Time `json:"endOn,omitempty"`
	// UpdatedOn is the outage row's own last-modified instant, and it is what
	// makes the Update arm fire once per change rather than once per sweep.
	//
	// ServiceNow ran this flow "For each unique change" on the record, so its
	// update branch was reached only when the outage was actually edited. A
	// sweep has no such trigger: it re-reads every declared, unended outage on
	// every tick, and without this the whole internal list is mailed again on
	// each one. At the default */5 that is twelve identical emails an hour,
	// for as long as the outage stays open.
	UpdatedOn *time.Time `json:"updatedOn,omitempty"`
	// SyncedPhase is outage.internal_notification_phase, mirrored from
	// ServiceNow. READ ONLY, and only to seed a first state row.
	SyncedPhase OutageNotificationPhase `json:"syncedPhase"`
	// ServiceName and ServiceOfferingName are display values for the email.
	ServiceName         string `json:"serviceName,omitempty"`
	ServiceOfferingName string `json:"serviceOfferingName,omitempty"`
	// State is what this service has already sent, nil when it has never
	// evaluated this outage.
	State *OutageNotificationState `json:"state,omitempty"`
}

// OutageNotificationDecision is one outage's evaluation: which email to send,
// if any, and why.
type OutageNotificationDecision struct {
	OutageID string                 `json:"outageId"`
	Number   string                 `json:"number"`
	Kind     OutageNotificationKind `json:"kind"`
	// Reason names the branch that selected Kind, in the ServiceNow flow's own
	// vocabulary, so a log line can be matched against the original.
	Reason string `json:"reason"`
	// Subject and Body are rendered here rather than by the caller so the
	// wording lives in one place and can be tested against the original.
	Subject string `json:"subject,omitempty"`
	Body    string `json:"body,omitempty"`
}

// OutageNotificationSweepResponse is the result of one sweep.
type OutageNotificationSweepResponse struct {
	Evaluated int                          `json:"evaluated"`
	Decisions []OutageNotificationDecision `json:"decisions"`
	// Errors is keyed by outage id. A failure on one outage does not abandon
	// the rest of the sweep.
	Errors map[string]string `json:"errors,omitempty"`
}
