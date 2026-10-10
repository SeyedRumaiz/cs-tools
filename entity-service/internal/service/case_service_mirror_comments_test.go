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
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/repository"
)

// mirrorSNCaseService is the ServiceNow-side CaseService whose comment search
// returns the comments ServiceNow generated for a just-created case.
type mirrorSNCaseService struct {
	CaseService
	comments []domain.CaseComment
}

func (m *mirrorSNCaseService) SearchCaseComments(_ context.Context, _ domain.SearchCaseCommentsRequest) (domain.SearchCaseCommentsResponse, error) {
	return domain.SearchCaseCommentsResponse{Comments: m.comments}, nil
}

// ServiceNow can generate WORK_NOTE comments for a new case, and the customer
// who created the case is the caller here. A WORK_NOTE is refused for an
// external identity (migration 0191), so the mirror must write through the
// repository's system-identity operation. If it used the ordinary create,
// ServiceNow-originated work notes would be silently lost (the failure is
// logged and does not fail the case creation). The service layer must not
// stamp an identity itself: the context it passes down is the caller's.
func TestMirrorInitialSNComments_WritesThroughTheSystemOperation(t *testing.T) {
	const caseID = "44444444-4444-4444-4444-444444444444"
	created := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	older := time.Date(2026, 9, 30, 8, 0, 0, 0, time.UTC)
	type call struct {
		ctx       context.Context
		req       domain.CreateCaseCommentRequest
		createdOn *time.Time
	}
	var calls []call

	repo := &stubCaseRepo{
		createCaseCommentAsSystem: func(ctx context.Context, req domain.CreateCaseCommentRequest, createdOn *time.Time) (domain.CaseComment, error) {
			calls = append(calls, call{ctx, req, createdOn})
			return domain.CaseComment{}, nil
		},
		// the ordinary create must not be used for the mirror
		createCaseComment: func(context.Context, domain.CreateCaseCommentRequest, *time.Time) (domain.CaseComment, error) {
			t.Fatal("the mirror must not use the caller-identity create")
			return domain.CaseComment{}, nil
		},
	}
	svc := &caseService{
		repo: repo,
		snMirror: &mirrorSNCaseService{comments: []domain.CaseComment{
			{Type: domain.CommentTypeWorkNote, Content: "auto-generated note", CreatedOn: created},
			{Type: domain.CommentTypeActivity, Content: "audit trail", CreatedOn: created},
			{Type: domain.CommentTypeComment, Content: "title and description", CreatedOn: older},
		}},
	}

	svc.mirrorInitialSNComments(externalCustomerContext(), caseID)

	if len(calls) != 2 {
		t.Fatalf("expected the work note and the comment to be mirrored (the audit entry is skipped), got %d writes", len(calls))
	}
	if calls[0].req.Type != domain.CommentTypeWorkNote || calls[0].req.CaseID != caseID {
		t.Fatalf("expected the WORK_NOTE to be written to case %s, got %+v", caseID, calls[0].req)
	}
	// ServiceNow's own timestamps are kept, not replaced by "now"
	if calls[0].createdOn == nil || !calls[0].createdOn.Equal(created) {
		t.Fatalf("work note must keep ServiceNow's CreatedOn %v, got %v", created, calls[0].createdOn)
	}
	if calls[1].createdOn == nil || !calls[1].createdOn.Equal(older) {
		t.Fatalf("comment must keep ServiceNow's CreatedOn %v, got %v", older, calls[1].createdOn)
	}
	// the service layer hands the caller's context straight down; the
	// repository is what stamps the system identity
	for i, c := range calls {
		id, ok := repository.CallerIdentityFromContext(c.ctx)
		if !ok || id.Unrestricted || id.ViewerEmail != "customer@test.local" {
			t.Fatalf("call %d: service layer must not change the caller's identity, got %+v (ok=%v)", i, id, ok)
		}
	}
}
