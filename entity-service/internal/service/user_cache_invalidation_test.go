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
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/salesentity"
)

// The writers of user, role and membership data outside UserService must
// drop the affected user's cached profile once their write has committed, and
// only then: a failed write changed nothing, so it invalidates nothing.

func TestUserCacheInvalidation_MembershipIngest(t *testing.T) {
	t.Run("an ingested membership invalidates its user", func(t *testing.T) {
		h := newIngestHarness(sampleProjectContact("INVITED", "Portal user"), sampleContact(), false)
		if err := h.svc.HandleEvent(context.Background(), membershipEvent("CREATED", "Project_Contact__c")); err != nil {
			t.Fatal(err)
		}
		want := []domain.AffectedUser{{ID: "user-1", Email: h.repo.upserts[0].Email}}
		if !reflect.DeepEqual(h.cache.invalidated, want) {
			t.Errorf("invalidated = %+v, want %+v", h.cache.invalidated, want)
		}
	})

	t.Run("a failed upsert invalidates nothing", func(t *testing.T) {
		h := newIngestHarness(sampleProjectContact("INVITED", "Portal user"), sampleContact(), false)
		h.repo.upsertErr = errors.New("connection reset")
		if err := h.svc.HandleEvent(context.Background(), membershipEvent("CREATED", "Project_Contact__c")); err == nil {
			t.Fatal("HandleEvent = nil, want the upsert error")
		}
		if len(h.cache.invalidated) != 0 {
			t.Errorf("invalidated = %+v, want none", h.cache.invalidated)
		}
	})

	t.Run("a DELETED membership invalidates every affected user", func(t *testing.T) {
		h := newIngestHarness(sampleProjectContact("INVITED", "Portal user"), sampleContact(), false)
		h.repo.deactivateAffected = []domain.AffectedUser{{ID: "user-1", Email: "jane@acme.com"}}
		if err := h.svc.HandleEvent(context.Background(), membershipEvent("DELETED", "Project_Contact__c")); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(h.cache.invalidated, h.repo.deactivateAffected) {
			t.Errorf("invalidated = %+v, want %+v", h.cache.invalidated, h.repo.deactivateAffected)
		}
	})

	t.Run("no cache configured is fine", func(t *testing.T) {
		h := newIngestHarness(sampleProjectContact("INVITED", "Portal user"), sampleContact(), false)
		unwired := NewSalesforceEventServiceWithMembershipIngest(h.accounts, &stubSalesEntityClient{},
			SalesforceIngestSupport{Accounts: h.accounts, States: h.states},
			MembershipIngest{Memberships: h.repo, Steps: h.steps, SalesEntity: h.se, Contacts: h.contacts})
		if err := unwired.HandleEvent(context.Background(), membershipEvent("CREATED", "Project_Contact__c")); err != nil {
			t.Fatalf("HandleEvent without a UserCache: %v", err)
		}
	})
}

func TestUserCacheInvalidation_ContactWriter(t *testing.T) {
	t.Run("a written contact invalidates its user", func(t *testing.T) {
		h := newIngestHarness(sampleProjectContact("INVITED"), sampleWriterContact(), false)
		if err := h.svc.HandleEvent(context.Background(), contactEvent("UPDATED")); err != nil {
			t.Fatal(err)
		}
		want := []domain.AffectedUser{{ID: "user-1", Email: "jane@acme.com"}}
		if !reflect.DeepEqual(h.cache.invalidated, want) {
			t.Errorf("invalidated = %+v, want %+v", h.cache.invalidated, want)
		}
	})

	t.Run("a failed contact write invalidates nothing", func(t *testing.T) {
		h := newIngestHarness(sampleProjectContact("INVITED"), sampleWriterContact(), false)
		h.contacts.upsertErr = errors.New("connection reset")
		if err := h.svc.HandleEvent(context.Background(), contactEvent("UPDATED")); err == nil {
			t.Fatal("HandleEvent = nil, want the upsert error")
		}
		if len(h.cache.invalidated) != 0 {
			t.Errorf("invalidated = %+v, want none", h.cache.invalidated)
		}
	})

	t.Run("a DELETED contact invalidates every deactivated user", func(t *testing.T) {
		h := newIngestHarness(sampleProjectContact("REGISTERED"), sampleWriterContact(), false)
		h.contacts.deactivateAffected = []domain.AffectedUser{
			{ID: "user-1", Email: "jane@acme.com"},
			{ID: "user-2", Email: "jane.old@acme.com"},
		}
		if err := h.svc.HandleEvent(context.Background(), contactEvent("DELETED")); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(h.cache.invalidated, h.contacts.deactivateAffected) {
			t.Errorf("invalidated = %+v, want %+v", h.cache.invalidated, h.contacts.deactivateAffected)
		}
	})
}

func TestUserCacheInvalidation_PortalMembershipWrites(t *testing.T) {
	const writeUserID = "9a2e8d6a-3b4c-4d5e-8f90-123456789abc"
	want := []domain.AffectedUser{{ID: writeUserID, Email: writeEmail}}
	existing := func() *domain.ProjectMembershipRow {
		return &domain.ProjectMembershipRow{
			ProjectContactID: "pc-1", MembershipSfID: writeMembershipID, ContactSfID: writeContactSfID,
			Email: writeEmail, State: domain.MembershipStateRegistered,
			ProjectGroups: []string{projectGroupGeneralAccess},
		}
	}

	t.Run("invite", func(t *testing.T) {
		h := newInternalWriteHarness(t)
		if _, err := h.svc.Invite(context.Background(), writeProjectID, inviteReq("portal user")); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(h.cache.invalidated, want) {
			t.Errorf("invalidated = %+v, want %+v", h.cache.invalidated, want)
		}
	})

	t.Run("update roles", func(t *testing.T) {
		h := newInternalWriteHarness(t)
		h.repo.existing = existing()
		h.se.contact = existingSalesforceContact()
		h.se.membership = &salesentity.ProjectContact{ID: writeMembershipID, Roles: []string{"Portal user"}}
		if _, err := h.svc.UpdateRoles(context.Background(), writeProjectID, writeEmail,
			domain.UpdateProjectMembershipRolesRequest{Roles: []string{"Portal user", "Admin"}}); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(h.cache.invalidated, want) {
			t.Errorf("invalidated = %+v, want %+v", h.cache.invalidated, want)
		}
	})

	t.Run("deactivate", func(t *testing.T) {
		h := newInternalWriteHarness(t)
		h.repo.existing = existing()
		h.se.contact = existingSalesforceContact()
		h.se.membership = &salesentity.ProjectContact{ID: writeMembershipID}
		if err := h.svc.Deactivate(context.Background(), writeProjectID, writeEmail); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(h.cache.invalidated, want) {
			t.Errorf("invalidated = %+v, want %+v", h.cache.invalidated, want)
		}
	})

	t.Run("a commit that failed after Salesforce invalidates nothing", func(t *testing.T) {
		h := newInternalWriteHarness(t)
		h.repo.commitErr = true
		if _, err := h.svc.Invite(context.Background(), writeProjectID, inviteReq("portal user")); err == nil {
			t.Fatal("Invite = nil, want the commit failure")
		}
		if len(h.cache.invalidated) != 0 {
			t.Errorf("invalidated = %+v, want none", h.cache.invalidated)
		}
	})
}
