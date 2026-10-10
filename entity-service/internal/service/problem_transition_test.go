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
	"strings"
	"testing"
	"time"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/apierror"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/repository"
)

func strp(s string) *string { return &s }

// problemDetailStub answers the pre-move requirement check and the
// post-write re-read: a problem that already has an assignee and fix notes,
// so every move's requirements are met.
func problemDetailStub(context.Context, string) (domain.ProblemDetail, error) {
	id, notes := testDeploymentUUID, "rotate logs"
	return domain.ProblemDetail{ID: &id, AssignedTo: &domain.EntityRef{ID: testUUID, Name: "Jane"}, FixNotes: &notes}, nil
}

func userCtxProblem(t *testing.T) context.Context {
	return contextWithUserIDToken(fakeJWTWithEmail(t, "jane.doe@example.com"))
}

// The table is ServiceNow's ProblemUtils._PROBLEM_TRANSITIONS (101 New, 102
// Assess, 103 RCA, 104 Fix in Progress, 106 Resolved, 107 Closed), in
// problem_state_enum labels.
func TestProblemTransitions_MatchServiceNow(t *testing.T) {
	want := map[string][2]string{
		"assess":  {"NEW", "ASSESS"},
		"confirm": {"ASSESS", "ROOT_CAUSE_ANALYSIS"},
		"fix":     {"ROOT_CAUSE_ANALYSIS", "FIX_IN_PROGRESS"},
		"resolve": {"FIX_IN_PROGRESS", "RESOLVED"},
		"close":   {"RESOLVED", "CLOSED"},
	}
	if len(problemTransitions) != len(want) {
		t.Fatalf("transitions = %d, want %d", len(problemTransitions), len(want))
	}
	for name, ft := range want {
		got, ok := problemTransitions[name]
		if !ok || got.Name != name || got.From != ft[0] || got.To != ft[1] {
			t.Errorf("%s = %+v, want %s -> %s", name, got, ft[0], ft[1])
		}
	}
}

// DATA_SOURCE=postgres: the move and its fields go to Postgres in one call,
// with the from-state enforced; there is no ServiceNow to call.
func TestUpdateProblem_PlainPostgres_TransitionEnforcesFromState(t *testing.T) {
	var gotT repository.ProblemTransition
	var gotEnforce bool
	var gotReq domain.UpdateProblemRequest
	repo := &stubProblemRepo{
		applyProblemTransition: func(_ context.Context, req domain.UpdateProblemRequest, tr repository.ProblemTransition, enforce bool, actor string) (time.Time, error) {
			gotReq, gotT, gotEnforce = req, tr, enforce
			if actor != "jane.doe@example.com" {
				t.Errorf("actor = %q", actor)
			}
			return time.Now(), nil
		},
		getProblem: problemDetailStub,
	}
	svc := NewProblemService(repo)
	if _, err := svc.UpdateProblem(userCtxProblem(t), domain.UpdateProblemRequest{
		ID: testDeploymentUUID, Transition: strp(" fix "), CauseNotes: strp("disk full"), FixNotes: strp("rotate logs"),
	}); err != nil {
		t.Fatalf("UpdateProblem: %v", err)
	}
	if gotT != problemTransitions["fix"] || !gotEnforce {
		t.Errorf("transition %+v enforce %v, want fix with the from-state enforced", gotT, gotEnforce)
	}
	if strOrEmpty(gotReq.CauseNotes) != "disk full" || strOrEmpty(gotReq.FixNotes) != "rotate logs" {
		t.Errorf("fields did not ride along with the move: %+v", gotReq)
	}
}

// DATA_SOURCE=postgres: plain fields (now including the assignment group)
// are written; there is no mirror to dispatch.
func TestUpdateProblem_PlainPostgres_FieldsIncludingAssignmentGroup(t *testing.T) {
	var got domain.UpdateProblemRequest
	repo := &stubProblemRepo{
		updateProblemFields: func(_ context.Context, req domain.UpdateProblemRequest, _ string) (time.Time, error) {
			got = req
			return time.Now(), nil
		},
		getProblem: problemDetailStub,
	}
	if _, err := NewProblemService(repo).UpdateProblem(userCtxProblem(t), domain.UpdateProblemRequest{
		ID: testDeploymentUUID, AssignmentGroupID: strp(testUUID),
	}); err != nil {
		t.Fatalf("UpdateProblem: %v", err)
	}
	if strOrEmpty(got.AssignmentGroupID) != testUUID {
		t.Errorf("assignmentGroupId = %v, want %s", got.AssignmentGroupID, testUUID)
	}
}

// Dual-write: ServiceNow first, with the whole request; only then Postgres,
// with the from-state NOT enforced (ServiceNow is the authority and Postgres
// may lag it).
func TestUpdateProblem_DualWrite_TransitionGoesToServiceNowFirst(t *testing.T) {
	var order []string
	mirror := &stubMirrorProblemService{
		updateProblem: func(_ context.Context, req domain.UpdateProblemRequest) (domain.UpdateProblemResponse, error) {
			order = append(order, "servicenow")
			if strOrEmpty(req.Transition) != "resolve" || strOrEmpty(req.FixNotes) != "patched" {
				t.Errorf("ServiceNow got %+v, want the transition and its fields", req)
			}
			return domain.UpdateProblemResponse{}, nil
		},
	}
	repo := &stubProblemRepo{
		applyProblemTransition: func(_ context.Context, _ domain.UpdateProblemRequest, tr repository.ProblemTransition, enforce bool, _ string) (time.Time, error) {
			order = append(order, "postgres")
			if tr != problemTransitions["resolve"] || enforce {
				t.Errorf("Postgres got %+v enforce=%v, want resolve without the from-state check", tr, enforce)
			}
			return time.Now(), nil
		},
		getProblem: problemDetailStub,
	}
	svc := NewProblemServiceWithSNMirror(repo, mirror, NewSNWritebackDispatcher(&recordingSNWritebackFailures{}))
	if _, err := svc.UpdateProblem(userCtxProblem(t), domain.UpdateProblemRequest{
		ID: testDeploymentUUID, Transition: strp("resolve"), FixNotes: strp("patched"),
	}); err != nil {
		t.Fatalf("UpdateProblem: %v", err)
	}
	if len(order) != 2 || order[0] != "servicenow" || order[1] != "postgres" {
		t.Errorf("order = %v, want [servicenow postgres]", order)
	}
}

// Dual-write: a ServiceNow refusal (e.g. its 409 when its own business rule
// reverts the move) comes back as-is and Postgres is never written, so the
// two never disagree about the state. The stub repo panics if called.
func TestUpdateProblem_DualWrite_ServiceNowRefusalLeavesPostgres(t *testing.T) {
	refusal := &apierror.ConflictError{Msg: "State transition rejected"}
	mirror := &stubMirrorProblemService{
		updateProblem: func(context.Context, domain.UpdateProblemRequest) (domain.UpdateProblemResponse, error) {
			return domain.UpdateProblemResponse{}, refusal
		},
	}
	svc := NewProblemServiceWithSNMirror(&stubProblemRepo{getProblem: problemDetailStub}, mirror, NewSNWritebackDispatcher(&recordingSNWritebackFailures{}))
	_, err := svc.UpdateProblem(userCtxProblem(t), domain.UpdateProblemRequest{ID: testDeploymentUUID, Transition: strp("confirm")})
	if !errors.Is(err, refusal) {
		t.Errorf("err = %v, want ServiceNow's refusal unchanged", err)
	}
}

// Dual-write: ServiceNow moved but Postgres failed -- surfaced, not hidden.
func TestUpdateProblem_DualWrite_PostgresFailureAfterServiceNowIsReturned(t *testing.T) {
	boom := errors.New("connection reset")
	mirror := &stubMirrorProblemService{
		updateProblem: func(context.Context, domain.UpdateProblemRequest) (domain.UpdateProblemResponse, error) {
			return domain.UpdateProblemResponse{}, nil
		},
	}
	repo := &stubProblemRepo{
		applyProblemTransition: func(context.Context, domain.UpdateProblemRequest, repository.ProblemTransition, bool, string) (time.Time, error) {
			return time.Time{}, boom
		},
		getProblem: problemDetailStub,
	}
	svc := NewProblemServiceWithSNMirror(repo, mirror, NewSNWritebackDispatcher(&recordingSNWritebackFailures{}))
	if _, err := svc.UpdateProblem(userCtxProblem(t), domain.UpdateProblemRequest{ID: testDeploymentUUID, Transition: strp("assess")}); !errors.Is(err, boom) {
		t.Errorf("err = %v, want the Postgres failure", err)
	}
}

// Dual-write: a fields-only request with an assignment group still goes
// Postgres first, and the async mirror carries the group to ServiceNow.
func TestUpdateProblem_DualWrite_FieldsMirrorTheAssignmentGroup(t *testing.T) {
	mirrored := make(chan domain.UpdateProblemRequest, 1)
	mirror := &stubMirrorProblemService{
		updateProblem: func(_ context.Context, req domain.UpdateProblemRequest) (domain.UpdateProblemResponse, error) {
			mirrored <- req
			return domain.UpdateProblemResponse{}, nil
		},
	}
	repo := &stubProblemRepo{
		updateProblemFields: func(context.Context, domain.UpdateProblemRequest, string) (time.Time, error) { return time.Now(), nil },
		getProblem:          problemDetailStub,
	}
	svc := NewProblemServiceWithSNMirror(repo, mirror, NewSNWritebackDispatcher(&recordingSNWritebackFailures{}))
	if _, err := svc.UpdateProblem(userCtxProblem(t), domain.UpdateProblemRequest{ID: testDeploymentUUID, AssignmentGroupID: strp(testUUID)}); err != nil {
		t.Fatalf("UpdateProblem: %v", err)
	}
	select {
	case req := <-mirrored:
		if strOrEmpty(req.AssignmentGroupID) != testUUID || req.Transition != nil {
			t.Errorf("mirror got %+v, want the group and no transition", req)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the assignment group was never mirrored to ServiceNow")
	}
}

// An unknown transition is refused before anything is called, in both modes.
func TestUpdateProblem_UnknownTransitionRejected(t *testing.T) {
	for name, svc := range map[string]ProblemService{
		"postgres":   NewProblemService(&stubProblemRepo{}),
		"dual-write": NewProblemServiceWithSNMirror(&stubProblemRepo{}, &stubMirrorProblemService{}, NewSNWritebackDispatcher(&recordingSNWritebackFailures{})),
	} {
		_, err := svc.UpdateProblem(userCtxProblem(t), domain.UpdateProblemRequest{ID: testDeploymentUUID, Transition: strp("reopen")})
		var ve *apierror.ValidationError
		if !errors.As(err, &ve) {
			t.Errorf("%s: err = %T %v, want a ValidationError", name, err, err)
		}
	}
}

// ServiceNow's state model wants an assignee for Assess and fix notes for
// Resolved (discovery script 61). A move missing one is a 400 before
// ServiceNow or Postgres is touched, in both modes -- the stub repo and
// mirror panic if either is reached.
func TestUpdateProblem_TransitionRequirements(t *testing.T) {
	bare := func(context.Context, string) (domain.ProblemDetail, error) {
		id := testDeploymentUUID
		return domain.ProblemDetail{ID: &id}, nil
	}
	for name, svc := range map[string]ProblemService{
		"postgres":   NewProblemService(&stubProblemRepo{getProblem: bare}),
		"dual-write": NewProblemServiceWithSNMirror(&stubProblemRepo{getProblem: bare}, &stubMirrorProblemService{}, NewSNWritebackDispatcher(&recordingSNWritebackFailures{})),
	} {
		for _, c := range []struct {
			req  domain.UpdateProblemRequest
			want string
		}{
			{domain.UpdateProblemRequest{ID: testDeploymentUUID, Transition: strp("assess")}, "assess needs an assignee"},
			{domain.UpdateProblemRequest{ID: testDeploymentUUID, Transition: strp("resolve")}, "resolve needs fix notes"},
			{domain.UpdateProblemRequest{ID: testDeploymentUUID, Transition: strp("resolve"), FixNotes: strp(" ")}, "resolve needs fix notes"},
		} {
			_, err := svc.UpdateProblem(userCtxProblem(t), c.req)
			var ve *apierror.ValidationError
			if !errors.As(err, &ve) || !strings.Contains(ve.Msg, c.want) {
				t.Errorf("%s %s: err = %v, want a ValidationError %q", name, *c.req.Transition, err, c.want)
			}
		}
	}
}

// A requirement met by the request itself passes: the assignee or fix notes
// ride along with the move, as in ServiceNow's own Assess / Resolve dialogs.
func TestUpdateProblem_RequirementsMetByTheRequest(t *testing.T) {
	bare := func(context.Context, string) (domain.ProblemDetail, error) {
		id := testDeploymentUUID
		return domain.ProblemDetail{ID: &id}, nil
	}
	var moved []string
	repo := &stubProblemRepo{
		getProblem: bare,
		applyProblemTransition: func(_ context.Context, _ domain.UpdateProblemRequest, tr repository.ProblemTransition, _ bool, _ string) (time.Time, error) {
			moved = append(moved, tr.Name)
			return time.Now(), nil
		},
	}
	svc := NewProblemService(repo)
	for _, req := range []domain.UpdateProblemRequest{
		{ID: testDeploymentUUID, Transition: strp("assess"), AssignedToID: strp(testUUID)},
		{ID: testDeploymentUUID, Transition: strp("resolve"), FixNotes: strp("patched the gateway")},
		{ID: testDeploymentUUID, Transition: strp("confirm")},
	} {
		if _, err := svc.UpdateProblem(userCtxProblem(t), req); err != nil {
			t.Errorf("%s: %v", *req.Transition, err)
		}
	}
	if strings.Join(moved, ",") != "assess,resolve,confirm" {
		t.Errorf("moved = %v", moved)
	}
}

// The portal sends targetResolutionDate as ServiceNow's "YYYY-MM-DD
// HH:mm:ss" (UTC) -- the format this API documents. Postgres must get the
// same instant as RFC3339, and the ServiceNow mirror its own format back.
func TestUpdateProblem_TargetDateAcceptsThePortalFormat(t *testing.T) {
	for in, want := range map[string][2]string{
		"2026-10-06 00:00:00":       {"2026-10-06T00:00:00Z", "2026-10-06 00:00:00"},
		"2026-10-06T05:30:00+05:30": {"2026-10-06T00:00:00Z", "2026-10-06 00:00:00"},
	} {
		var toPG string
		mirrored := make(chan domain.UpdateProblemRequest, 1)
		repo := &stubProblemRepo{
			updateProblemFields: func(_ context.Context, req domain.UpdateProblemRequest, _ string) (time.Time, error) {
				toPG = strOrEmpty(req.TargetResolutionDate)
				return time.Now(), nil
			},
			getProblem: problemDetailStub,
		}
		mirror := &stubMirrorProblemService{
			updateProblem: func(_ context.Context, req domain.UpdateProblemRequest) (domain.UpdateProblemResponse, error) {
				mirrored <- req
				return domain.UpdateProblemResponse{}, nil
			},
		}
		svc := NewProblemServiceWithSNMirror(repo, mirror, NewSNWritebackDispatcher(&recordingSNWritebackFailures{}))
		if _, err := svc.UpdateProblem(userCtxProblem(t), domain.UpdateProblemRequest{ID: testDeploymentUUID, TargetResolutionDate: strp(in)}); err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if toPG != want[0] {
			t.Errorf("%s: Postgres got %q, want %q", in, toPG, want[0])
		}
		select {
		case req := <-mirrored:
			if strOrEmpty(req.TargetResolutionDate) != want[1] {
				t.Errorf("%s: ServiceNow got %q, want %q", in, strOrEmpty(req.TargetResolutionDate), want[1])
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%s: never mirrored", in)
		}
	}
}

// A transition carrying a date sends ServiceNow its format too.
func TestUpdateProblem_DualWriteTransitionSendsServiceNowDateFormat(t *testing.T) {
	var toSN string
	mirror := &stubMirrorProblemService{
		updateProblem: func(_ context.Context, req domain.UpdateProblemRequest) (domain.UpdateProblemResponse, error) {
			toSN = strOrEmpty(req.TargetResolutionDate)
			return domain.UpdateProblemResponse{}, nil
		},
	}
	repo := &stubProblemRepo{
		getProblem: problemDetailStub,
		applyProblemTransition: func(context.Context, domain.UpdateProblemRequest, repository.ProblemTransition, bool, string) (time.Time, error) {
			return time.Now(), nil
		},
	}
	svc := NewProblemServiceWithSNMirror(repo, mirror, NewSNWritebackDispatcher(&recordingSNWritebackFailures{}))
	if _, err := svc.UpdateProblem(userCtxProblem(t), domain.UpdateProblemRequest{
		ID: testDeploymentUUID, Transition: strp("confirm"), TargetResolutionDate: strp("2026-10-06 08:15:00"),
	}); err != nil {
		t.Fatalf("UpdateProblem: %v", err)
	}
	if toSN != "2026-10-06 08:15:00" {
		t.Errorf("ServiceNow got %q", toSN)
	}
}

// A blank value in the request is what the save would write, so it must not
// pass on the strength of what the problem already has: resolve with
// fixNotes "" would clear the stored notes and resolve without any.
func TestUpdateProblem_BlankRequestValueOverridesStoredOne(t *testing.T) {
	svc := NewProblemService(&stubProblemRepo{getProblem: problemDetailStub}) // has an assignee and fix notes
	_, err := svc.UpdateProblem(userCtxProblem(t), domain.UpdateProblemRequest{
		ID: testDeploymentUUID, Transition: strp("resolve"), FixNotes: strp(""),
	})
	var ve *apierror.ValidationError
	if !errors.As(err, &ve) || !strings.Contains(ve.Msg, "resolve needs fix notes") {
		t.Errorf("err = %v, want the fix-notes ValidationError", err)
	}
}
