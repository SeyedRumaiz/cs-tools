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

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/repository"
)

const workNoteTestCaseID = "33333333-3333-3333-3333-333333333333"

// recordingEscalations is an EscalationService whose CreateEscalation records
// the context it was called with and returns a canned escalation.
type recordingEscalations struct {
	EscalationService
	createCtx context.Context
}

func (r *recordingEscalations) CreateEscalation(ctx context.Context, _ domain.CreateEscalationRequest) (domain.CreateEscalationResponse, error) {
	r.createCtx = ctx
	return domain.CreateEscalationResponse{Escalation: domain.CreatedEscalation{ID: "esc-1"}}, nil
}

// recordingCaseService is a CaseService that records which comment operation
// was used, the context it got, and the request. CreateCaseComment is the
// caller-identity operation and must not be used for the escalation note.
type recordingCaseService struct {
	CaseService
	internalCtx     context.Context
	internalComment domain.CreateCaseCommentRequest
	internalCalls   int
	ordinaryCalls   int
	err             error
}

func (r *recordingCaseService) CreateInternalCaseComment(ctx context.Context, req domain.CreateCaseCommentRequest) (domain.CreateCaseCommentResponse, error) {
	r.internalCalls++
	r.internalCtx = ctx
	r.internalComment = req
	return domain.CreateCaseCommentResponse{}, r.err
}

func (r *recordingCaseService) CreateCaseComment(context.Context, domain.CreateCaseCommentRequest) (domain.CreateCaseCommentResponse, error) {
	r.ordinaryCalls++
	return domain.CreateCaseCommentResponse{}, nil
}

func externalCustomerContext() context.Context {
	return repository.WithCallerIdentity(context.Background(), repository.SearchScope{
		ProjectIDs:  []string{"22222222-2222-2222-2222-222222222222"},
		ViewerEmail: "customer@test.local",
	})
}

// A customer can escalate their own case. The case work note recorded after
// the escalation is a WORK_NOTE comment, which an external caller can neither
// read nor write (migration 0191), so it goes through the internal comment
// operation (which writes it as the system below the service boundary). The
// escalation itself keeps the caller's identity, and this layer hands the
// caller's context down unchanged for both: it stamps no identity of its own.
func TestCreateCaseEscalation_WorkNoteUsesInternalOperationEscalationKeepsCaller(t *testing.T) {
	escalations := &recordingEscalations{}
	caseSvc := &recordingCaseService{}
	svc := NewCaseEscalationService(escalations, caseSvc)

	if _, err := svc.CreateCaseEscalation(externalCustomerContext(), workNoteTestCaseID, nil, nil); err != nil {
		t.Fatalf("CreateCaseEscalation: %v", err)
	}

	// the escalation was created with the customer's own identity, unchanged
	got, ok := repository.CallerIdentityFromContext(escalations.createCtx)
	if !ok || got.Unrestricted || got.ViewerEmail != "customer@test.local" {
		t.Fatalf("escalation must keep the caller's identity, got %+v (ok=%v)", got, ok)
	}

	// the work note is a WORK_NOTE on the same case, via the internal operation
	if caseSvc.internalCalls != 1 || caseSvc.ordinaryCalls != 0 {
		t.Fatalf("expected exactly one internal comment and no ordinary one, got internal=%d ordinary=%d", caseSvc.internalCalls, caseSvc.ordinaryCalls)
	}
	if caseSvc.internalComment.Type != domain.CommentTypeWorkNote || caseSvc.internalComment.CaseID != workNoteTestCaseID {
		t.Fatalf("expected a WORK_NOTE on case %s, got %+v", workNoteTestCaseID, caseSvc.internalComment)
	}
	// no identity change in the service layer
	note, ok := repository.CallerIdentityFromContext(caseSvc.internalCtx)
	if !ok || note.Unrestricted || note.ViewerEmail != "customer@test.local" {
		t.Fatalf("the escalation service must not stamp an identity itself, got %+v (ok=%v)", note, ok)
	}
}

// The note is bookkeeping: if writing it fails, the escalation the customer
// asked for has already happened and must still be reported as successful.
func TestCreateCaseEscalation_WorkNoteFailureDoesNotFailEscalation(t *testing.T) {
	caseSvc := &recordingCaseService{err: errors.New("boom")}
	svc := NewCaseEscalationService(&recordingEscalations{}, caseSvc)

	got, err := svc.CreateCaseEscalation(externalCustomerContext(), workNoteTestCaseID, nil, nil)
	if err != nil {
		t.Fatalf("a failed work note must not fail the escalation: %v", err)
	}
	if got.ID != "esc-1" {
		t.Fatalf("expected the created escalation back, got %+v", got)
	}
}
