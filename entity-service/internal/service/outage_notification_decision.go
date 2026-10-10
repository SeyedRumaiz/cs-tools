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
	"time"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
)

// decideOutageNotification reproduces the three branches of ServiceNow's
// `Internal Stakeholders Email Notification - Outage Communication`, in order.
//
// The original is If / Else If / Else If, so AT MOST ONE ARM RUNS per
// evaluation and the order decides which. That is not incidental — it is what
// produces the behaviour below, and a port that evaluated all three
// independently would send two emails where ServiceNow sends one.
//
//	1  If      phase is None                         -> Declared
//	4  Else If End is not empty AND phase is not Resolved -> Resolved
//	7  Else If phase is Declared AND End is empty     -> Update
//
// Two guards are load-bearing. `phase is not Resolved` on arm 4 is what stops
// the resolution email re-sending on every later change; `End is empty` on
// arm 7 is what stops update emails continuing after the outage is over.
//
// THE ORDERING HAS A CONSEQUENCE WORTH KNOWING. An outage that is already over
// when notification is switched on still gets a "declared" email first,
// because arm 1 wins on that evaluation and arm 4 cannot also run. ServiceNow
// then needs a SECOND record update before it will ever say "resolved", and if
// none arrives the outage sits at Declared forever and the resolution email
// never goes out.
//
// This port is a sweep rather than a record trigger, so it comes back and
// notices. That is a deliberate divergence: it fixes a real gap rather than
// faithfully reproducing a silence. Both emails are still sent, in the same
// order, one evaluation apart.
func decideOutageNotification(o domain.OutageForNotification) domain.OutageNotificationDecision {
	d := domain.OutageNotificationDecision{OutageID: o.OutageID, Number: o.Number}

	phase := domain.OutageNotificationPhaseNone
	if o.State != nil {
		phase = o.State.Phase
	} else if o.SyncedPhase != "" {
		// Never evaluated here, but ServiceNow has. Seed from its phase so a
		// cutover does not re-announce outages it already announced.
		phase = o.SyncedPhase
	}

	ended := o.EndOn != nil

	switch {
	case phase == domain.OutageNotificationPhaseNone:
		d.Kind = domain.OutageNotificationDeclared
		d.Reason = "Declaration (The First Email)"

	case ended && phase != domain.OutageNotificationPhaseResolved:
		d.Kind = domain.OutageNotificationResolved
		d.Reason = "Resolution (The Final Email)"

	case phase == domain.OutageNotificationPhaseDeclared && !ended:
		// *** ONCE PER CHANGE, NOT ONCE PER SWEEP. *** ServiceNow ran this
		// flow "For each unique change" on the outage record, so this arm was
		// reached only when the outage had actually been edited. A sweep has
		// no trigger to inherit: it re-reads every declared, unended outage on
		// every tick, so without this guard each one mails the whole internal
		// list again — twelve times an hour at the default */5 schedule, for
		// as long as the outage stays open.
		//
		// The comparison is against the last thing we SAID, not the last time
		// we looked: last_update_on when we have sent an update, otherwise
		// declared_on. An outage edited between the declaration and the first
		// update therefore still earns one.
		if !outageChangedSince(o, lastSpokeAt(o)) {
			d.Kind = domain.OutageNotificationNone
			d.Reason = "no change since the last notice"
			return d
		}
		d.Kind = domain.OutageNotificationUpdate
		d.Reason = "Update (Everything In-Between)"

	default:
		// Resolved and nothing left to say. ServiceNow reaches this by having
		// no matching arm; here it is explicit.
		d.Kind = domain.OutageNotificationNone
		d.Reason = "no branch matched"
		return d
	}

	d.Subject, d.Body = renderOutageNotification(d.Kind, o)
	return d
}

// renderOutageNotification produces the subject and body for one email.
//
// *** THIS IS A FAITHFUL PORT OF A STUB, AND THAT IS DELIBERATE. ***
// The ServiceNow flow's three bodies are fixed sentences carrying no outage
// detail at all — no message, no service, no window:
//
//	"Outage {number} declared."   "Outage {number} resolved."
//	"Outage {number} update."
//
// Its own description claims it "sends templated emails to CI owners, logs the
// content to the Activity Stream, and resets the input field for the next
// update". It does none of those things. The description describes an intended
// design; the flow is a skeleton of it.
//
// Enriching the body here would be inventing content that cannot be checked
// against the original, and would make the parallel run impossible to compare.
// The wording is reproduced exactly; making these emails useful is a product
// decision, tracked separately from the port.
func renderOutageNotification(kind domain.OutageNotificationKind, o domain.OutageForNotification) (subject, body string) {
	word := map[domain.OutageNotificationKind]string{
		domain.OutageNotificationDeclared: "Declared",
		domain.OutageNotificationResolved: "Resolved",
		domain.OutageNotificationUpdate:   "Update",
	}[kind]

	sentence := map[domain.OutageNotificationKind]string{
		domain.OutageNotificationDeclared: "declared",
		domain.OutageNotificationResolved: "resolved",
		domain.OutageNotificationUpdate:   "update",
	}[kind]

	return fmt.Sprintf("[Outage] %s - %s", o.Number, word),
		fmt.Sprintf("Outage %s %s.", o.Number, sentence)
}

// phaseAfter returns the phase the outage is in once an email of this kind
// has been sent.
//
// The update arm does not ADVANCE the phase — that is what lets one outage
// emit many update emails, and it is the original's behaviour — but it still
// has a well-defined resulting phase, because the arm only runs when the
// phase is already Declared. Returning it rather than "no change" matters:
// the first time an outage reaches the update arm there may be no stored row
// yet (its phase was seeded from ServiceNow), and writing "unchanged" into a
// fresh row would store NONE and re-declare the outage on the next sweep.
// Observed exactly that, against a real database, before this returned a
// phase for every arm.
func phaseAfter(kind domain.OutageNotificationKind) domain.OutageNotificationPhase {
	switch kind {
	case domain.OutageNotificationResolved:
		return domain.OutageNotificationPhaseResolved
	default:
		// Declared, and Update — which only runs from Declared and leaves it
		// there.
		return domain.OutageNotificationPhaseDeclared
	}
}

// lastSpokeAt is the instant of the most recent thing this service said about
// an outage: the last update if there has been one, otherwise the
// declaration. Nil when it has said nothing, which cannot happen on the
// Update arm — that arm requires phase DECLARED — but is handled rather than
// assumed, because a state row seeded from ServiceNow carries a phase without
// necessarily carrying our timestamps.
func lastSpokeAt(o domain.OutageForNotification) *time.Time {
	if o.State == nil {
		return nil
	}
	if o.State.LastUpdateOn != nil {
		return o.State.LastUpdateOn
	}
	return o.State.DeclaredOn
}

// outageChangedSince reports whether the outage row was modified after since.
//
// Both unknowns are deliberately resolved toward SENDING. A missing
// updated_on (the column is nullable in the mirror) or a state row with no
// timestamps of its own — the seeded-from-ServiceNow case — would otherwise
// silence the update arm permanently for that outage, and a notifier that
// goes quiet is far harder to notice than one that repeats.
func outageChangedSince(o domain.OutageForNotification, since *time.Time) bool {
	if o.UpdatedOn == nil || since == nil {
		return true
	}
	return o.UpdatedOn.After(*since)
}
