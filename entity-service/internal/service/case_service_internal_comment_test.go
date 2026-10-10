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
	"testing"
	"time"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
)

func callerWithToken(t *testing.T) context.Context {
	t.Helper()
	return contextWithUserIDToken(fakeJWTWithEmail(t, "jane.doe@example.com"))
}

// CreateCaseComment is the caller-identity operation; CreateInternalCaseComment
// is the same call with the row written as the system. Each must reach its own
// repository method, and only that one: using the system method for a request
// comment would let an external caller write a WORK_NOTE, and using the
// ordinary one for the internal note would lose it (migration 0191).
func TestCaseService_InternalAndOrdinaryCommentUseTheirOwnRepositoryMethod(t *testing.T) {
	const caseID = "55555555-5555-5555-5555-555555555555"
	req := domain.CreateCaseCommentRequest{CaseID: caseID, Type: domain.CommentTypeWorkNote, Content: "note"}

	cases := []struct {
		name   string
		call   func(*caseService) (domain.CreateCaseCommentResponse, error)
		wantAs string // which repository method must be used
	}{
		{"ordinary", func(s *caseService) (domain.CreateCaseCommentResponse, error) {
			return s.CreateCaseComment(callerWithToken(t), req)
		}, "ordinary"},
		{"internal", func(s *caseService) (domain.CreateCaseCommentResponse, error) {
			return s.CreateInternalCaseComment(callerWithToken(t), req)
		}, "system"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var used []string
			ok := func(label string) func(context.Context, domain.CreateCaseCommentRequest, *time.Time) (domain.CaseComment, error) {
				return func(_ context.Context, r domain.CreateCaseCommentRequest, createdOn *time.Time) (domain.CaseComment, error) {
					used = append(used, label)
					if createdOn != nil {
						t.Errorf("a new comment must be stamped by the database, got createdOn %v", createdOn)
					}
					if r.CaseID != caseID || r.Type != domain.CommentTypeWorkNote {
						t.Errorf("unexpected request %+v", r)
					}
					if r.CreatedBy != "jane.doe@example.com" {
						t.Errorf("author must be the caller resolved from the token, got %q", r.CreatedBy)
					}
					return domain.CaseComment{ID: "c-1", Type: r.Type, Content: r.Content}, nil
				}
			}
			svc := &caseService{
				repo: &stubCaseRepo{
					createCaseComment:         ok("ordinary"),
					createCaseCommentAsSystem: ok("system"),
				},
				userRepo: actorUserRepo(t),
			}

			if _, err := tc.call(svc); err != nil {
				t.Fatalf("create comment: %v", err)
			}
			if len(used) != 1 || used[0] != tc.wantAs {
				t.Fatalf("expected exactly the %q repository method, got %v", tc.wantAs, used)
			}
		})
	}
}

// Both operations resolve the author from the caller's token the same way, so
// a missing token is refused by both: the internal operation is not a way
// around identification.
func TestCaseService_InternalCommentStillRequiresACaller(t *testing.T) {
	svc := &caseService{repo: &stubCaseRepo{}, userRepo: actorUserRepo(t)}
	req := domain.CreateCaseCommentRequest{CaseID: "55555555-5555-5555-5555-555555555555", Type: domain.CommentTypeWorkNote, Content: "note"}

	if _, err := svc.CreateInternalCaseComment(context.Background(), req); err == nil {
		t.Fatal("an internal comment without an x-user-id-token must be refused")
	}
}
