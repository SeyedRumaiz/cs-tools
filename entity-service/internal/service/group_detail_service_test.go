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
	"testing"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/apierror"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
)

type stubGroupDetailRepo struct {
	detail domain.GroupDetail
	err    error
	calls  int
	gotID  string
}

func (s *stubGroupDetailRepo) GetGroupDetail(_ context.Context, id string) (domain.GroupDetail, error) {
	s.calls++
	s.gotID = id
	return s.detail, s.err
}

const testGroupID = "22222222-2222-4222-8222-222222222222"

func TestGroupDetailService_ReturnsTheGroupForAnInternalCaller(t *testing.T) {
	email := "cab@example.test"
	repo := &stubGroupDetailRepo{detail: domain.GroupDetail{
		ID: testGroupID, Name: "CAB Approval", Email: &email,
		Members: []domain.GroupMember{{ID: "u1", Name: "A"}, {ID: "u2", Name: "B"}}, Total: 2,
	}}
	got, err := NewGroupDetailService(repo, alwaysUnrestrictedAccess{}).GetGroupDetail(context.Background(), testGroupID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name != "CAB Approval" || got.Total != 2 || len(got.Members) != 2 || repo.gotID != testGroupID {
		t.Fatalf("got %+v (repo asked for %q)", got, repo.gotID)
	}
}

// The members are staff, so an external (customer) caller is refused before
// the repository is reached -- and before the id is even parsed, so a
// malformed id gets the same 403 and reveals nothing.
func TestGroupDetailService_RefusesANonInternalCaller(t *testing.T) {
	repo := &stubGroupDetailRepo{}
	svc := NewGroupDetailService(repo, restrictedAccess{})
	for _, id := range []string{testGroupID, "not-a-uuid"} {
		_, err := svc.GetGroupDetail(context.Background(), id)
		var fe *apierror.ForbiddenError
		if !errors.As(err, &fe) {
			t.Fatalf("id %q: err = %v, want a ForbiddenError", id, err)
		}
	}
	if repo.calls != 0 {
		t.Fatalf("repository reached %d times for a non-internal caller", repo.calls)
	}
}

func TestGroupDetailService_PropagatesAnUnresolvableIdentity(t *testing.T) {
	repo := &stubGroupDetailRepo{}
	want := &apierror.UnauthorizedError{Msg: "no verified identity"}
	_, err := NewGroupDetailService(repo, erroringAccess{err: want}).GetGroupDetail(context.Background(), testGroupID)
	var ue *apierror.UnauthorizedError
	if !errors.As(err, &ue) {
		t.Fatalf("err = %v, want the UnauthorizedError from ResolveScope", err)
	}
	if repo.calls != 0 {
		t.Fatal("repository must not be reached without an identity")
	}
}

func TestGroupDetailService_RejectsAMalformedId(t *testing.T) {
	repo := &stubGroupDetailRepo{}
	_, err := NewGroupDetailService(repo, alwaysUnrestrictedAccess{}).GetGroupDetail(context.Background(), "not-a-uuid")
	var ve *apierror.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("err = %v, want a ValidationError", err)
	}
	if repo.calls != 0 {
		t.Fatal("repository must not be reached for a malformed id")
	}
}

func TestGroupDetailService_UnknownGroupIsNotFound(t *testing.T) {
	repo := &stubGroupDetailRepo{err: &apierror.NotFoundError{Msg: "group not found"}}
	_, err := NewGroupDetailService(repo, alwaysUnrestrictedAccess{}).GetGroupDetail(context.Background(), testGroupID)
	var nf *apierror.NotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("err = %v, want a NotFoundError", err)
	}
}

func TestGroupDetailService_GroupWithNoMembersIsNotAnError(t *testing.T) {
	repo := &stubGroupDetailRepo{detail: domain.GroupDetail{ID: testGroupID, Name: "Empty", Members: []domain.GroupMember{}}}
	got, err := NewGroupDetailService(repo, alwaysUnrestrictedAccess{}).GetGroupDetail(context.Background(), testGroupID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Total != 0 || got.Members == nil {
		t.Fatalf("got %+v, want an empty non-nil member list", got)
	}
}
