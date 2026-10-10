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

package dispatch

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/wso2-open-operations/cs-tools/integrations/csm-notification-service/internal/eventbus"
	"github.com/wso2-open-operations/cs-tools/integrations/csm-notification-service/internal/events"
)

type fakeSpecialOpsPager struct {
	mu  sync.Mutex
	ids []string
	got []events.IncidentSpecialOpsAlertPayload
}

func (f *fakeSpecialOpsPager) HandleSpecialOpsAlert(_ context.Context, id string, p events.IncidentSpecialOpsAlertPayload) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ids = append(f.ids, id)
	f.got = append(f.got, p)
	return nil
}

var specialOpsRecord = eventbus.Record{Value: []byte(`{"type":"incident.special_ops_alert","entityId":"INC-1","payload":` +
	`{"incidentId":"INC-1","number":"INC0099001","subject":"Latency","product":"Choreo","teamKey":"choreo-runtime-team",` +
	`"teamLabel":"Choreo Runtime Team","assignmentGroupId":"g-2","previousAssignmentGroupName":"SRE - Apollo",` +
	`"changedOn":"2026-10-08T10:00:00Z","smeTeam":"choreo-sme"}}`)}

// The sre-events path hands the alert to the SME page.
func TestDispatcher_HandleShared_SpecialOpsAlert(t *testing.T) {
	pager := &fakeSpecialOpsPager{}
	d := newTestDispatcher(&mockEmailSender{}, &mockGoogleChatSender{}, &mockCallSender{}).WithSpecialOpsPage(pager)
	if err := d.HandleShared(context.Background(), specialOpsRecord); err != nil {
		t.Fatal(err)
	}
	if len(pager.got) != 1 || pager.ids[0] != "INC-1" || pager.got[0].SMETeam != "choreo-sme" ||
		pager.got[0].PreviousAssignmentGroupName != "SRE - Apollo" {
		t.Fatalf("pager got ids=%v %+v", pager.ids, pager.got)
	}
}

// An alert that arrives before the SRE engine is wired waits for it; with no
// SME page at all it is acknowledged.
func TestDispatcher_SpecialOpsAlert_WaitsForTheWiring(t *testing.T) {
	pager := &fakeSpecialOpsPager{}
	d := newTestDispatcher(&mockEmailSender{}, &mockGoogleChatSender{}, &mockCallSender{}).DeferSpecialOpsPage()
	done := make(chan error, 1)
	go func() { done <- d.Handle(context.Background(), specialOpsRecord) }()
	select {
	case err := <-done:
		t.Fatalf("Handle returned (%v) before the SME page was wired", err)
	case <-time.After(20 * time.Millisecond):
	}
	d.WithSpecialOpsPage(pager)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(pager.got) != 1 {
		t.Fatalf("pager got %d alerts; want 1", len(pager.got))
	}

	none := newTestDispatcher(&mockEmailSender{}, &mockGoogleChatSender{}, &mockCallSender{}).DeferSpecialOpsPage().WithSpecialOpsPage(nil)
	if err := none.Handle(context.Background(), specialOpsRecord); err != nil {
		t.Errorf("with no SME page: %v; want the alert acknowledged", err)
	}
	if err := newTestDispatcher(&mockEmailSender{}, &mockGoogleChatSender{}, &mockCallSender{}).Handle(context.Background(), specialOpsRecord); err != nil {
		t.Errorf("never wired: %v; want the alert acknowledged", err)
	}
}
