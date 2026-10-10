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
	"testing"

	"github.com/wso2-open-operations/cs-tools/integrations/csm-notification-service/internal/eventbus"
	"github.com/wso2-open-operations/cs-tools/integrations/csm-notification-service/internal/events"
)

type fakeTestCaller struct {
	got []events.PagingTestCallRequestedPayload
}

func (f *fakeTestCaller) HandleTestCall(_ context.Context, p events.PagingTestCallRequestedPayload) error {
	f.got = append(f.got, p)
	return nil
}

var testCallRecord = eventbus.Record{Value: []byte(`{"type":"paging.test_call_requested","entityId":"u-1","payload":` +
	`{"userId":"u-1","email":"ana@wso2.com","name":"Ana","phone":"+94771234567","requestedBy":"lead@wso2.com","requestedAt":"2026-10-08T10:00:00Z"}}`)}

// paging.test_call_requested reaches the tester; with none configured it is
// acknowledged, not retried.
func TestDispatcher_Handle_PagingTestCall(t *testing.T) {
	tc := &fakeTestCaller{}
	d := newTestDispatcher(&mockEmailSender{}, &mockGoogleChatSender{}, &mockCallSender{}).WithPagingTestCalls(tc)
	if err := d.Handle(context.Background(), testCallRecord); err != nil {
		t.Fatal(err)
	}
	if len(tc.got) != 1 || tc.got[0].UserID != "u-1" || tc.got[0].Phone != "+94771234567" {
		t.Fatalf("tester got %+v", tc.got)
	}
	if err := newTestDispatcher(&mockEmailSender{}, &mockGoogleChatSender{}, &mockCallSender{}).Handle(context.Background(), testCallRecord); err != nil {
		t.Errorf("without a tester: %v; want the event acknowledged", err)
	}
}
