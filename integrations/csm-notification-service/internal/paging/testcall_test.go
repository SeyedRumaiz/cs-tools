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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wso2-open-operations/cs-tools/integrations/csm-notification-service/internal/apierror"
	"github.com/wso2-open-operations/cs-tools/integrations/csm-notification-service/internal/events"
	"github.com/wso2-open-operations/cs-tools/integrations/csm-notification-service/internal/notifications"
)

// fakeTestTwilio places calls and answers status reads from a script.
type fakeTestTwilio struct {
	mu       sync.Mutex
	placed   []string
	messages []string
	makeErr  error
	statuses []string // returned in order; the last one repeats
	reads    int
}

func (f *fakeTestTwilio) MakeCall(_ context.Context, to, message string) (notifications.Call, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.makeErr != nil {
		return notifications.Call{}, f.makeErr
	}
	f.placed = append(f.placed, to)
	f.messages = append(f.messages, message)
	return notifications.Call{SID: "CAtest", Status: "queued"}, nil
}

func (f *fakeTestTwilio) GetCall(_ context.Context, sid string) (notifications.Call, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	i := f.reads
	if i >= len(f.statuses) {
		i = len(f.statuses) - 1
	}
	f.reads++
	return notifications.Call{SID: sid, Status: f.statuses[i]}, nil
}

type reportedResult struct{ userID, status string }

type fakeResults struct {
	mu      sync.Mutex
	got     []reportedResult
	failFor int // the first failFor PUTs fail
	puts    int
}

func (f *fakeResults) PutPagingTestResult(_ context.Context, userID, status string, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.puts++
	if f.puts <= f.failFor {
		return errors.New("entity-service unavailable")
	}
	f.got = append(f.got, reportedResult{userID, status})
	return nil
}

type fakeGuard struct{ held map[string]bool }

func (g *fakeGuard) ClaimTestCall(_ context.Context, userID string, _ time.Duration) (bool, error) {
	if g.held == nil {
		g.held = map[string]bool{}
	}
	if g.held[userID] {
		return false, nil
	}
	g.held[userID] = true
	return true, nil
}

// fastTestCaller is a TestCaller whose waits take microseconds.
func fastTestCaller(tw *fakeTestTwilio, res *fakeResults, cfg TestCallConfig) *TestCaller {
	tc := newTestCaller(context.Background(), tw, res, &fakeGuard{}, cfg, true)
	tc.FirstCheck, tc.PollEvery, tc.GiveUpAfter = time.Millisecond, time.Millisecond, 9*time.Millisecond
	tc.ReportBackoff = time.Microsecond
	return tc
}

func testCallFor(phone string) events.PagingTestCallRequestedPayload {
	return events.PagingTestCallRequestedPayload{UserID: "u-1", Email: "ana@wso2.com", Name: "Ana", Phone: phone,
		RequestedBy: "lead@wso2.com", RequestedAt: "2026-10-08T10:00:00Z"}
}

func runTestCall(t *testing.T, tc *TestCaller, p events.PagingTestCallRequestedPayload) {
	t.Helper()
	if err := tc.HandleTestCall(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	tc.Wait()
}

func TestTestCall_StatusMapping(t *testing.T) {
	for _, c := range []struct {
		name     string
		statuses []string
		want     string
	}{
		{"answered", []string{"completed"}, TestCallCompleted},
		{"no answer", []string{"no-answer"}, TestCallNoAnswer},
		{"busy", []string{"busy"}, TestCallBusy},
		{"canceled", []string{"canceled"}, TestCallFailed},
		{"polled until it ends", []string{"queued", "ringing", "in-progress", "completed"}, TestCallCompleted},
		{"still talking when we give up", []string{"ringing", "in-progress"}, TestCallCompleted},
		{"still ringing when we give up", []string{"ringing"}, TestCallNoAnswer},
	} {
		t.Run(c.name, func(t *testing.T) {
			tw, res := &fakeTestTwilio{statuses: c.statuses}, &fakeResults{failFor: 1}
			runTestCall(t, fastTestCaller(tw, res, TestCallConfig{}), testCallFor("+94771234567"))
			if len(tw.placed) != 1 || tw.placed[0] != "+94771234567" {
				t.Fatalf("calls = %v; want one to the paging number", tw.placed)
			}
			if !strings.Contains(tw.messages[0], "Case Paging test call for Ana") {
				t.Errorf("message = %q", tw.messages[0])
			}
			// The first PUT fails; the retry lands.
			if len(res.got) != 1 || res.got[0] != (reportedResult{"u-1", c.want}) {
				t.Fatalf("reported %v; want %s", res.got, c.want)
			}
		})
	}
}

// Polling stops at GiveUpAfter: a first check, then one read per PollEvery.
func TestTestCall_PollsUntilGiveUp(t *testing.T) {
	tw, res := &fakeTestTwilio{statuses: []string{"ringing"}}, &fakeResults{}
	tc := fastTestCaller(tw, res, TestCallConfig{})
	tc.FirstCheck, tc.PollEvery, tc.GiveUpAfter = 6*time.Millisecond, 2*time.Millisecond, 18*time.Millisecond
	runTestCall(t, tc, testCallFor("+94771234567"))
	if tw.reads != 7 {
		t.Errorf("%d status reads; want 7 (at 6, 8, ... 18)", tw.reads)
	}
}

// Disabled, not E.164 or not allowed: no call, reported failed.
func TestTestCall_RefusedIsReportedFailed(t *testing.T) {
	off := false
	for name, c := range map[string]struct {
		cfg   TestCallConfig
		phone string
	}{
		"disabled":    {TestCallConfig{Enabled: &off}, "+94771234567"},
		"not allowed": {TestCallConfig{AllowedNumbers: []string{"+94770000000"}}, "+94771234567"},
		"not E.164":   {TestCallConfig{}, "0771234567"},
	} {
		t.Run(name, func(t *testing.T) {
			tw, res := &fakeTestTwilio{statuses: []string{"completed"}}, &fakeResults{}
			runTestCall(t, fastTestCaller(tw, res, c.cfg), testCallFor(c.phone))
			if len(tw.placed) != 0 || len(res.got) != 1 || res.got[0].status != TestCallFailed {
				t.Errorf("calls=%v reported=%v; want no call and failed", tw.placed, res.got)
			}
		})
	}
	t.Run("sending disabled", func(t *testing.T) {
		tw, res := &fakeTestTwilio{statuses: []string{"completed"}}, &fakeResults{}
		tc := fastTestCaller(tw, res, TestCallConfig{})
		tc.sending = false
		runTestCall(t, tc, testCallFor("+94771234567"))
		if len(tw.placed) != 0 || len(res.got) != 1 || res.got[0].status != TestCallFailed {
			t.Errorf("calls=%v reported=%v", tw.placed, res.got)
		}
	})
	t.Run("rejected by the provider", func(t *testing.T) {
		tw := &fakeTestTwilio{makeErr: &apierror.Error{StatusCode: 400, Body: `{"code":21211}`}}
		res := &fakeResults{}
		runTestCall(t, fastTestCaller(tw, res, TestCallConfig{}), testCallFor("+94771234567"))
		if len(res.got) != 1 || res.got[0].status != TestCallFailed {
			t.Errorf("reported %v; want failed", res.got)
		}
	})
}

// A second request for the same person within two minutes places nothing.
func TestTestCall_Deduplicated(t *testing.T) {
	tw, res := &fakeTestTwilio{statuses: []string{"completed"}}, &fakeResults{}
	tc := fastTestCaller(tw, res, TestCallConfig{})
	runTestCall(t, tc, testCallFor("+94771234567"))
	runTestCall(t, tc, testCallFor("+94771234567"))
	if len(tw.placed) != 1 || len(res.got) != 1 {
		t.Errorf("calls=%v reported=%v; want one of each", tw.placed, res.got)
	}
}

// A shutdown during the wait leaves the status pending: nothing is reported.
func TestTestCall_ShutdownDuringTheWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	tw, res := &fakeTestTwilio{statuses: []string{"completed"}}, &fakeResults{}
	tc := newTestCaller(ctx, tw, res, &fakeGuard{}, TestCallConfig{}, true)
	if err := tc.HandleTestCall(context.Background(), testCallFor("+94771234567")); err != nil {
		t.Fatal(err)
	}
	cancel()
	tc.Wait()
	if len(tw.placed) != 1 || len(res.got) != 0 {
		t.Errorf("calls=%v reported=%v; want the call placed and nothing reported", tw.placed, res.got)
	}
}

// The testCall section: absent is on, any number; strict keys.
func TestConfig_TestCallSection(t *testing.T) {
	cfg, err := loadYAML(t, "enabled: true\n")
	if err != nil || !cfg.TestCall.On() || !cfg.TestCall.Dialable("+94771234567") {
		t.Fatalf("absent testCall = %+v, %v; want on, any number", cfg.TestCall, err)
	}
	cfg, err = loadYAML(t, "testCall:\n  enabled: false\n  allowedNumbers: [\"+94770000000\"]\n")
	if err != nil || cfg.TestCall.On() || cfg.TestCall.Dialable("+94771234567") {
		t.Fatalf("testCall = %+v, %v", cfg.TestCall, err)
	}
	if _, err := loadYAML(t, "testCall:\n  maxCalls: 1\n"); err == nil {
		t.Error("an unknown testCall key loaded")
	}
}
