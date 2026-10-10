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

package cloudstatus

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeDecisionClient struct {
	sweepRes SweepResult
	sweepErr error

	pending    []PendingWebhook
	pendingErr error

	reports   []deliveryCall
	reportErr error
}

type deliveryCall struct {
	id        string
	delivered bool
	errMsg    string
}

func (f *fakeDecisionClient) Sweep(context.Context) (SweepResult, error) {
	return f.sweepRes, f.sweepErr
}
func (f *fakeDecisionClient) Pending(context.Context) ([]PendingWebhook, error) {
	return f.pending, f.pendingErr
}
func (f *fakeDecisionClient) RecordDelivery(_ context.Context, id string, delivered bool, errMsg string) error {
	f.reports = append(f.reports, deliveryCall{id, delivered, errMsg})
	return f.reportErr
}

type fakePoster struct {
	known map[string]bool
	fail  map[string]error
	posts []string // "cloud|event|timestamp"
}

func (f *fakePoster) Knows(cloud string) bool { return f.known[cloud] }
func (f *fakePoster) Post(_ context.Context, cloud, event, timestamp string) error {
	f.posts = append(f.posts, cloud+"|"+event+"|"+timestamp)
	return f.fail[cloud]
}

// TestDeliverDue_PostsAndReports is the ordinary path.
func TestDeliverDue_PostsAndReports(t *testing.T) {
	client := &fakeDecisionClient{pending: []PendingWebhook{
		{ID: "w1", Number: "OUT001", Cloud: "choreo", Event: "OUTAGE_BEGIN", WireEvent: "outage_begin", Timestamp: "2026-09-28T10:00:00Z"},
	}}
	hook := &fakePoster{known: map[string]bool{"choreo": true}}

	if err := DeliverDue(client, hook)(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hook.posts) != 1 || hook.posts[0] != "choreo|outage_begin|2026-09-28T10:00:00Z" {
		t.Errorf("posts: %v", hook.posts)
	}
	if len(client.reports) != 1 || !client.reports[0].delivered {
		t.Errorf("reports: %+v", client.reports)
	}
}

// TestDeliverDue_SweepFailureStillDelivers covers the deliberate decision not
// to abandon the tick: webhooks already recorded are still owed.
func TestDeliverDue_SweepFailureStillDelivers(t *testing.T) {
	client := &fakeDecisionClient{
		sweepErr: errors.New("database is unhappy"),
		pending: []PendingWebhook{
			{ID: "w1", Cloud: "asgardeo", Event: "OUTAGE_END", WireEvent: "outage_end", Timestamp: "2026-09-28T12:00:00Z"},
		},
	}
	hook := &fakePoster{known: map[string]bool{"asgardeo": true}}

	err := DeliverDue(client, hook)(context.Background())
	if err == nil {
		t.Fatal("the sweep failure must still be reported")
	}
	if !strings.Contains(err.Error(), "sweep") {
		t.Errorf("error should name the sweep: %v", err)
	}
	if len(hook.posts) != 1 {
		t.Errorf("the already-recorded webhook must still be delivered, posts: %v", hook.posts)
	}
	if len(client.reports) != 1 || !client.reports[0].delivered {
		t.Errorf("reports: %+v", client.reports)
	}
}

// TestDeliverDue_OneDashboardDownDoesNotStopAnother is the divergence from
// ServiceNow, where one failing action abandoned the whole execution.
func TestDeliverDue_OneDashboardDownDoesNotStopAnother(t *testing.T) {
	client := &fakeDecisionClient{pending: []PendingWebhook{
		{ID: "w1", Cloud: "asgardeo", Event: "OUTAGE_BEGIN", WireEvent: "outage_begin"},
		{ID: "w2", Cloud: "choreo", Event: "OUTAGE_BEGIN", WireEvent: "outage_begin"},
	}}
	hook := &fakePoster{
		known: map[string]bool{"asgardeo": true, "choreo": true},
		fail:  map[string]error{"asgardeo": errors.New("503")},
	}

	err := DeliverDue(client, hook)(context.Background())
	if err == nil {
		t.Fatal("the failed post must be reported")
	}
	if len(hook.posts) != 2 {
		t.Errorf("both dashboards must be attempted, posts: %v", hook.posts)
	}
	var failed, ok bool
	for _, r := range client.reports {
		if r.id == "w1" && !r.delivered {
			failed = true
		}
		if r.id == "w2" && r.delivered {
			ok = true
		}
	}
	if !failed || !ok {
		t.Errorf("reports: %+v", client.reports)
	}
}

// TestDeliverDue_UnconfiguredCloudIsReportedNotSilentlySkipped is Defect 1's
// counterpart in this component. ServiceNow posted to a URL of "undefined";
// this must produce a visible, attempt-counted failure instead.
func TestDeliverDue_UnconfiguredCloudIsReportedNotSilentlySkipped(t *testing.T) {
	client := &fakeDecisionClient{pending: []PendingWebhook{
		{ID: "w1", Number: "OUT009", Cloud: "choreo-eu", Event: "OUTAGE_BEGIN", WireEvent: "outage_begin"},
	}}
	hook := &fakePoster{known: map[string]bool{"choreo": true}}

	err := DeliverDue(client, hook)(context.Background())
	if err == nil {
		t.Fatal("an unroutable cloud must be reported")
	}
	if len(hook.posts) != 0 {
		t.Errorf("nothing should be posted for an unconfigured cloud, posts: %v", hook.posts)
	}
	if len(client.reports) != 1 || client.reports[0].delivered {
		t.Fatalf("it must be recorded as a failed attempt: %+v", client.reports)
	}
	if !strings.Contains(client.reports[0].errMsg, "choreo-eu") {
		t.Errorf("the recorded error must name the cloud: %q", client.reports[0].errMsg)
	}
}

// TestDeliverDue_DeliveredButUnreportedIsRetried pins the chosen side of that
// race: a duplicate event is better than a lost one.
func TestDeliverDue_DeliveredButUnreportedIsRetried(t *testing.T) {
	client := &fakeDecisionClient{
		pending:   []PendingWebhook{{ID: "w1", Cloud: "choreo", Event: "OUTAGE_BEGIN", WireEvent: "outage_begin"}},
		reportErr: errors.New("entity-service unreachable"),
	}
	hook := &fakePoster{known: map[string]bool{"choreo": true}}

	err := DeliverDue(client, hook)(context.Background())
	if err == nil {
		t.Fatal("a failed report must surface; otherwise the re-send is a surprise")
	}
	if len(hook.posts) != 1 {
		t.Errorf("posts: %v", hook.posts)
	}
}

// TestDeliverDue_NothingPendingIsQuiet is the steady state.
func TestDeliverDue_NothingPendingIsQuiet(t *testing.T) {
	client := &fakeDecisionClient{sweepRes: SweepResult{Scanned: 12}}
	hook := &fakePoster{}

	if err := DeliverDue(client, hook)(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hook.posts) != 0 || len(client.reports) != 0 {
		t.Errorf("a quiet tick must do nothing")
	}
}

// TestDeliverDue_PostsTheWireEventNotTheEnumName is the regression guard for a
// bug a live cross-service run caught and every fixture-based test missed.
//
// entity-service names the transition OUTAGE_BEGIN; the dashboard expects
// "outage_begin". The delivering task must post the second. It missed for a
// while because the stubs here already held the lowercase form in Event, so
// the absent translation looked like a working one.
func TestDeliverDue_PostsTheWireEventNotTheEnumName(t *testing.T) {
	client := &fakeDecisionClient{pending: []PendingWebhook{
		{ID: "w1", Cloud: "choreo", Event: "OUTAGE_BEGIN", WireEvent: "outage_begin"},
	}}
	hook := &fakePoster{known: map[string]bool{"choreo": true}}

	if err := DeliverDue(client, hook)(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hook.posts) != 1 {
		t.Fatalf("posts: %v", hook.posts)
	}
	// posts are recorded as "cloud|event|timestamp"
	if got := hook.posts[0]; got != "choreo|outage_begin|" {
		t.Errorf("posted %q, want the wire literal outage_begin, never the enum name", got)
	}
}

// TestDeliverDue_MissingWireEventIsNotPosted guards the version-skew case: an
// entity-service too old to send wireEvent must not cause an empty event to go
// out, which a dashboard would accept and ignore.
func TestDeliverDue_MissingWireEventIsNotPosted(t *testing.T) {
	client := &fakeDecisionClient{pending: []PendingWebhook{
		{ID: "w1", Cloud: "choreo", Event: "OUTAGE_BEGIN", WireEvent: ""},
	}}
	hook := &fakePoster{known: map[string]bool{"choreo": true}}

	err := DeliverDue(client, hook)(context.Background())
	if err == nil {
		t.Fatal("a missing wire value must surface as an error")
	}
	if len(hook.posts) != 0 {
		t.Errorf("nothing must be posted, got %v", hook.posts)
	}
}
