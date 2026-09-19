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

package router

import (
	"context"
	"errors"
	"testing"
)

func TestSetMaxConcurrentChats_UpdatesExistingEngineer(t *testing.T) {
	r, pool := newTestRouter(t)
	userID := testUserID(t, r, pool, "capacity-set")

	if err := r.SetMaxConcurrentChats(context.Background(), userID, 5); err != nil {
		t.Fatalf("SetMaxConcurrentChats: %v", err)
	}

	detail, err := r.GetPresence(context.Background(), userID)
	if err != nil {
		t.Fatalf("GetPresence: %v", err)
	}
	if detail.MaxConcurrentChats != 5 {
		t.Errorf("expected MaxConcurrentChats=5 after SetMaxConcurrentChats, got %d", detail.MaxConcurrentChats)
	}
}

func TestSetMaxConcurrentChats_CreatesRowOnFirstContact(t *testing.T) {
	r, pool := newTestRouter(t)
	ctx := context.Background()
	userID := "router-test-capacity-first-contact-nonexistent"
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM cs_engineer_status WHERE user_id = $1`, userID)
	})

	// Unlike testUserID's fixtures, this engineer has never called
	// SetPresence -- SetMaxConcurrentChats must still work, same
	// first-contact behavior as ensureAndLockEngineer.
	if err := r.SetMaxConcurrentChats(ctx, userID, 3); err != nil {
		t.Fatalf("SetMaxConcurrentChats on unseen engineer: %v", err)
	}

	detail, err := r.GetPresence(ctx, userID)
	if err != nil {
		t.Fatalf("GetPresence: %v", err)
	}
	if detail.MaxConcurrentChats != 3 {
		t.Errorf("expected MaxConcurrentChats=3, got %d", detail.MaxConcurrentChats)
	}
	if detail.ChatStatus != StatusOffline {
		t.Errorf("expected a first-contact engineer to still default to OFFLINE, got %s", detail.ChatStatus)
	}
}

func TestSetMaxConcurrentChats_RejectsOutOfRange(t *testing.T) {
	r, pool := newTestRouter(t)
	userID := testUserID(t, r, pool, "capacity-range")

	for _, n := range []int{0, -1, 21, 100} {
		if err := r.SetMaxConcurrentChats(context.Background(), userID, n); !errors.Is(err, ErrInvalidCapacity) {
			t.Errorf("SetMaxConcurrentChats(%d): expected ErrInvalidCapacity, got %v", n, err)
		}
	}

	// A rejected call must not have partially applied -- capacity stays at
	// whatever it was (1, from testUserID's own first-contact default).
	detail, err := r.GetPresence(context.Background(), userID)
	if err != nil {
		t.Fatalf("GetPresence: %v", err)
	}
	if detail.MaxConcurrentChats != 1 {
		t.Errorf("expected a rejected SetMaxConcurrentChats to leave capacity untouched (1), got %d", detail.MaxConcurrentChats)
	}
}

func TestSetMaxConcurrentChats_LoweringDoesNotDropExistingCases(t *testing.T) {
	r, pool := newTestRouter(t)
	userID := testUserID(t, r, pool, "capacity-lower")
	setMaxConcurrent(t, pool, userID, 3)
	if _, err := r.SetPresence(context.Background(), userID, StatusAvailable); err != nil {
		t.Fatalf("SetPresence(AVAILABLE): %v", err)
	}

	caseID := testCaseID(t, pool, "capacity-lower-case")
	assignFixture(t, r, pool, userID, caseID)

	// Lower the cap below the current active count (1 active, capacity
	// dropped to... well, minimum is 1, so use this to prove a lowered-but-
	// still-sufficient cap leaves the case alone, and that future capacity
	// checks use the new value.)
	if err := r.SetMaxConcurrentChats(context.Background(), userID, 1); err != nil {
		t.Fatalf("SetMaxConcurrentChats: %v", err)
	}

	row := getConversationRow(t, pool, caseID)
	if row.AssigneeID == nil || *row.AssigneeID != userID {
		t.Errorf("expected lowering capacity to leave the already-held case assigned, got %+v", row)
	}

	// Now at capacity (1 active, cap 1) -- a second case must queue rather
	// than land on this engineer too.
	secondID := testCaseID(t, pool, "capacity-lower-second")
	secondCI := conversationFixture(t, r, pool, secondID)
	escResult, err := r.Escalate(context.Background(), secondCI)
	if err != nil {
		t.Fatalf("Escalate: %v", err)
	}
	if escResult.EngineerUserID == userID {
		t.Errorf("expected the lowered cap to actually take effect (engineer at capacity), but got assigned a second case: %+v", escResult)
	}
}
