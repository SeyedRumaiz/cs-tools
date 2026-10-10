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

package repository_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/repository"
)

// GET /change-requests/{id}/approvals carries each stage's assignment group, and
// opening that group (GET /groups/{id}) lists the people the stage was
// provisioned from. Same harness and DSN gate as the other
// TestChangeRequestFlowIntegration_* tests.

func approvalGroupIDs(members []domain.GroupMember) []string {
	out := make([]string, 0, len(members))
	for _, m := range members {
		out = append(out, strings.ToLower(m.ID))
	}
	sort.Strings(out)
	return out
}

func TestChangeRequestFlowIntegration_ApprovalsCarryTheAssignmentGroup(t *testing.T) {
	f := newCustomerGroupFlow(t)
	// A customer who is a member of the assigned group: never provisioned as an
	// approver (pools are INTERNAL-only), so not listed on the group page either.
	seedExternalGroupMembers(t, f.scoped, crFlowGroupID, crFlowExternalID)
	id := f.createWithProject(domain.ChangeRequestTypeNormal, sp(crScopeProjectA), true, false)
	f.driveToCustomerApproval(id)

	view := f.approvalsAs(id, crScopeUserA1)
	if len(view.Approvals) != 3 {
		t.Fatalf("approvals = %+v, want Peer, CAB and Customer Approval stages", view.Approvals)
	}

	// Peer Approval: the change's assigned group.
	peer := view.Approvals[0]
	if peer.AssignmentGroup == nil || peer.AssignmentGroup.ID != crFlowGroupID || peer.AssignmentGroup.Name != "CR Flow Assigned Group" {
		t.Fatalf("peer stage assignmentGroup = %+v, want {%s, CR Flow Assigned Group}", peer.AssignmentGroup, crFlowGroupID)
	}
	// CAB Approval: the CAB Approval group, by its fixed id.
	cab := view.Approvals[1]
	if cab.AssignmentGroup == nil || cab.AssignmentGroup.ID != crCABGroupID || cab.AssignmentGroup.Name != "CAB Approval" {
		t.Fatalf("CAB stage assignmentGroup = %+v, want {%s, CAB Approval}", cab.AssignmentGroup, crCABGroupID)
	}
	// approverName still carries the display name, unchanged.
	if peer.ApproverName != "CR Flow Assigned Group" || cab.ApproverName != "CAB Approval" {
		t.Fatalf("approverName = %q / %q", peer.ApproverName, cab.ApproverName)
	}
	// Customer Approval: the project's registered contacts, no group.
	cust := view.Approvals[2]
	if cust.Stage != stageCustApproval || cust.AssignmentGroup != nil || cust.ApproverName != "Customer Group" {
		t.Fatalf("customer stage = %+v, want %q with no assignmentGroup and approverName Customer Group", cust, stageCustApproval)
	}

	// Opening the CAB group lists exactly the people provisioned on that stage.
	detail := repository.NewGroupDetailRepository(f.pool)
	cabGroup, err := detail.GetGroupDetail(f.sys, cab.AssignmentGroup.ID)
	if err != nil {
		t.Fatalf("GetGroupDetail(CAB): %v", err)
	}
	if cabGroup.Name != "CAB Approval" || cabGroup.Total != 2 {
		t.Fatalf("CAB group = %+v, want CAB Approval with its 2 seeded members", cabGroup)
	}
	want := []string{strings.ToLower(crCABMemberUserID1), strings.ToLower(crCABMemberUserID2)}
	sort.Strings(want)
	if got := approvalGroupIDs(cabGroup.Members); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("CAB group members = %v, want the stage's approvers %v", got, want)
	}
	var cabApprovers []string
	for _, ap := range cab.Approvers {
		cabApprovers = append(cabApprovers, strings.ToLower(ap.ID))
	}
	sort.Strings(cabApprovers)
	if strings.Join(cabApprovers, ",") != strings.Join(want, ",") {
		t.Fatalf("CAB stage approvers = %v, want %v", cabApprovers, want)
	}

	// Opening the peer stage's assigned group lists everyone it was provisioned
	// from (creator and outsider included -- the creator is provisioned
	// cancelled).
	peerGroup, err := detail.GetGroupDetail(f.sys, peer.AssignmentGroup.ID)
	if err != nil {
		t.Fatalf("GetGroupDetail(peer group): %v", err)
	}
	shown := map[string]bool{}
	for _, m := range peerGroup.Members {
		shown[strings.ToLower(m.ID)] = true
	}
	for _, ap := range peer.Approvers {
		if !shown[strings.ToLower(ap.ID)] {
			t.Fatalf("peer approver %s (%s) is provisioned from the group but missing from the group page %v", ap.Name, ap.ID, shown)
		}
	}
	// The page lists who the stage can be provisioned from, which is not
	// everyone in the group: the customer belongs to the group but is not an
	// active internal user, so the pool skipped them and the page does not
	// offer them either.
	if shown[strings.ToLower(crFlowExternalID)] {
		t.Fatalf("the customer is not eligible for an internal stage and must not be listed on its group page: %v", shown)
	}
	for _, ap := range peer.Approvers {
		if strings.EqualFold(ap.ID, crFlowExternalID) {
			t.Fatalf("the customer was provisioned as a peer approver: %+v", peer.Approvers)
		}
	}
}
