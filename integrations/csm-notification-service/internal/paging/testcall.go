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
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/wso2-open-operations/cs-tools/integrations/csm-notification-service/internal/events"
	"github.com/wso2-open-operations/cs-tools/integrations/csm-notification-service/internal/notifications"
)

// Paging-number test calls.
//
// A lead or admin stores a paging-only number for somebody in CSM and presses
// "Test call"; entity-service publishes paging.test_call_requested. This
// places ONE short call to that number, waits, asks Twilio how the call
// ended, and PUTs the outcome back (completed, no-answer, busy or failed),
// which the portal shows next to the number.
//
// The event is acknowledged once the call is placed; the wait runs in a
// goroutine bound to the server's context. KNOWN GAP (accepted for phase 1):
// a restart during the wait loses it, and the number's status stays "pending"
// until somebody tests it again.

// Test-call statuses, as entity-service's test-result endpoint spells them.
const (
	TestCallCompleted = "completed"
	TestCallNoAnswer  = "no-answer"
	TestCallBusy      = "busy"
	TestCallFailed    = "failed"
)

// testCallClaimTTL is how long one person's test call holds their slot.
// entity-service refuses repeats as well; this covers a redelivery.
const testCallClaimTTL = 2 * time.Minute

// testCallTwilio is the slice of notifications.TwilioClient a test call uses.
type testCallTwilio interface {
	MakeCall(ctx context.Context, to, message string) (notifications.Call, error)
	GetCall(ctx context.Context, callSID string) (notifications.Call, error)
}

// testResultWriter records the outcome; EntityClient implements it.
type testResultWriter interface {
	PutPagingTestResult(ctx context.Context, userID, status string, testedAt time.Time) error
}

// testCallGuard is the per-person dedup; Store implements it.
type testCallGuard interface {
	ClaimTestCall(ctx context.Context, userID string, ttl time.Duration) (bool, error)
}

// TestCaller places paging-number test calls; see the top of this file.
type TestCaller struct {
	calls   testCallTwilio
	results testResultWriter
	guard   testCallGuard
	cfg     TestCallConfig
	// sending is CALL_SENDING_ENABLED: off, a test call is reported failed
	// rather than placed.
	sending bool
	// base outlives a record: the status wait and the report run on it.
	base context.Context

	// FirstCheck is how long after placing the call Twilio is first asked
	// how it went; PollEvery how often after that while it is still queued,
	// ringing or in progress; GiveUpAfter when to stop asking. Fields so
	// tests can shorten them.
	FirstCheck, PollEvery, GiveUpAfter time.Duration
	// ReportAttempts and ReportBackoff bound the PUT of the outcome.
	ReportAttempts int
	ReportBackoff  time.Duration

	wg sync.WaitGroup
	// now stamps testedAt; time.Now unless a test substitutes one.
	now func() time.Time
}

// NewTestCaller constructs a TestCaller whose waits run on base (the server's
// context).
func NewTestCaller(base context.Context, calls *notifications.TwilioClient, results *EntityClient, store *Store, cfg TestCallConfig, sending bool) *TestCaller {
	return newTestCaller(base, calls, results, store, cfg, sending)
}

func newTestCaller(base context.Context, calls testCallTwilio, results testResultWriter, guard testCallGuard, cfg TestCallConfig, sending bool) *TestCaller {
	return &TestCaller{
		calls: calls, results: results, guard: guard, cfg: cfg, sending: sending, base: base,
		FirstCheck: time.Minute, PollEvery: 20 * time.Second, GiveUpAfter: 3 * time.Minute,
		ReportAttempts: 3, ReportBackoff: 2 * time.Second,
		now: time.Now,
	}
}

// testCallMessage is what the call says.
func testCallMessage(name string) string {
	if name = strings.TrimSpace(name); name == "" {
		name = "you"
	}
	return fmt.Sprintf("This is a Case Paging test call for %s. Nothing is needed from you. Goodbye.", name)
}

// HandleTestCall places one test call for a press. An error is returned only
// when the dedup itself cannot be read, which a retry may fix; every other
// outcome is reported to entity-service and acknowledged.
func (t *TestCaller) HandleTestCall(ctx context.Context, p events.PagingTestCallRequestedPayload) error {
	log := slog.With("userId", p.UserID)
	claimed, err := t.guard.ClaimTestCall(ctx, p.UserID, testCallClaimTTL)
	if err != nil {
		return fmt.Errorf("paging: claim test call for %s: %w", p.UserID, err)
	}
	if !claimed {
		log.InfoContext(ctx, "paging: test call ignored; one for this person ran in the last two minutes")
		return nil
	}

	refuse := func(why string) error {
		log.WarnContext(ctx, "paging: test call not placed; reporting failed", "reason", why)
		t.reportAsync(p.UserID, TestCallFailed)
		return nil
	}
	switch {
	case !t.cfg.On():
		return refuse("testCall.enabled is false")
	case !t.sending:
		return refuse("CALL_SENDING_ENABLED=false")
	case !e164.MatchString(strings.TrimSpace(p.Phone)):
		return refuse("the number is not E.164")
	case !t.cfg.Dialable(strings.TrimSpace(p.Phone)):
		return refuse("the number is not in testCall.allowedNumbers")
	}

	call, err := t.calls.MakeCall(ctx, strings.TrimSpace(p.Phone), testCallMessage(p.Name))
	if err != nil {
		// Never logged with the error text: a provider rejection can echo the
		// number back.
		log.WarnContext(ctx, "paging: test call could not be placed; reporting failed",
			"to", maskPhone(p.Phone), "reason", permanentReason(err))
		t.reportAsync(p.UserID, TestCallFailed)
		return nil
	}
	log.InfoContext(ctx, "paging: test call placed", "to", maskPhone(p.Phone), "callSid", call.SID, "status", call.Status)

	t.wg.Add(1)
	go func() {
		defer t.wg.Done()
		status, ok := t.await(call)
		if !ok {
			// The server is shutting down; the status stays pending.
			return
		}
		slog.Info("paging: test call finished", "userId", p.UserID, "callSid", call.SID, "result", status)
		t.report(p.UserID, status)
	}()
	return nil
}

// await waits for the call to end and maps Twilio's status to a result. It
// reports false when the server's context ends first.
//
//	completed                     completed
//	no-answer                     no-answer
//	busy                          busy
//	failed, canceled              failed
//	queued, ringing, in-progress  ask again; past GiveUpAfter, in-progress
//	                              is completed and anything else no-answer
//
// A failed status read counts as "still going" and is asked again.
func (t *TestCaller) await(call notifications.Call) (string, bool) {
	last := call.Status
	waited := t.FirstCheck
	if !t.sleep(t.FirstCheck) {
		return "", false
	}
	for {
		got, err := t.calls.GetCall(t.base, call.SID)
		if err != nil {
			slog.Warn("paging: could not read a test call's status; asking again", "callSid", call.SID, "err", err)
		} else {
			last = got.Status
			switch strings.ToLower(got.Status) {
			case "completed":
				return TestCallCompleted, true
			case "no-answer":
				return TestCallNoAnswer, true
			case "busy":
				return TestCallBusy, true
			case "failed", "canceled":
				return TestCallFailed, true
			}
		}
		if waited+t.PollEvery > t.GiveUpAfter {
			break
		}
		if !t.sleep(t.PollEvery) {
			return "", false
		}
		waited += t.PollEvery
	}
	if strings.EqualFold(last, "in-progress") {
		return TestCallCompleted, true
	}
	return TestCallNoAnswer, true
}

// sleep waits d on the server's context, reporting false if it ends first.
func (t *TestCaller) sleep(d time.Duration) bool {
	if d <= 0 {
		return t.base.Err() == nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-t.base.Done():
		return false
	}
}

// reportAsync reports off the consumer's goroutine.
func (t *TestCaller) reportAsync(userID, status string) {
	t.wg.Add(1)
	go func() {
		defer t.wg.Done()
		t.report(userID, status)
	}()
}

// report PUTs the outcome to entity-service, best effort: a few attempts, then
// logged and given up, never an error anyone retries.
func (t *TestCaller) report(userID, status string) {
	at := t.now()
	attempts := t.ReportAttempts
	if attempts < 1 {
		attempts = 1
	}
	var err error
	for i := 0; i < attempts; i++ {
		if i > 0 && !t.sleep(t.ReportBackoff) {
			break
		}
		if err = t.results.PutPagingTestResult(t.base, userID, status, at); err == nil {
			return
		}
	}
	slog.Error("paging: could not record a test call's result; it stays pending",
		"userId", userID, "result", status, "err", err)
}

// Wait blocks until every status wait and report started so far has ended.
// For tests and an orderly shutdown.
func (t *TestCaller) Wait() { t.wg.Wait() }
