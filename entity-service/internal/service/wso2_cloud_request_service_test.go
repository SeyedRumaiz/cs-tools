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
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/apierror"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/auth"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
)

const testWCRID = "0d2f0b2e-8f6b-4f0a-9d5c-5b1f2a3c4d5e"

// fakeWCRRepo records what reaches the repository.
type fakeWCRRepo struct {
	created     []domain.CreateWSO2CloudRequestRequest
	createdBy   string
	stored      *domain.WSO2CloudRequest
	resolveArgs []string
	resolveNil  bool
	deleteOK    bool
	searched    *domain.SearchWSO2CloudRequestsRequest
}

func (f *fakeWCRRepo) Create(_ context.Context, by string, req domain.CreateWSO2CloudRequestRequest) (domain.WSO2CloudRequest, error) {
	f.created = append(f.created, req)
	f.createdBy = by
	return domain.WSO2CloudRequest{ID: testWCRID, Type: req.Type, RequestedBy: by, Status: domain.WSO2CloudRequestPending}, nil
}

func (f *fakeWCRRepo) Get(context.Context, string) (*domain.WSO2CloudRequest, error) {
	return f.stored, nil
}

func (f *fakeWCRRepo) Search(_ context.Context, req domain.SearchWSO2CloudRequestsRequest) ([]domain.WSO2CloudRequest, int, error) {
	f.searched = &req
	return []domain.WSO2CloudRequest{}, 0, nil
}

func (f *fakeWCRRepo) Resolve(_ context.Context, id string, status domain.WSO2CloudRequestStatus, by string, _ *string) (*domain.WSO2CloudRequest, error) {
	f.resolveArgs = []string{id, string(status), by}
	if f.resolveNil {
		return nil, nil
	}
	out := *f.stored
	out.Status = status
	out.ResolvedBy = &by
	return &out, nil
}

func (f *fakeWCRRepo) Delete(context.Context, string) (bool, error) { return f.deleteOK, nil }

func wcrUserCtx(email string) context.Context {
	return auth.WithIdentity(context.Background(), auth.Identity{Validated: true, UserEmail: email})
}



func tierp(t domain.WSO2CloudTier) *domain.WSO2CloudTier { return &t }

func TestWSO2CloudRequestService_CreateValidation(t *testing.T) {
	cases := []struct {
		name    string
		req     domain.CreateWSO2CloudRequestRequest
		wantErr string
	}{
		{"unknown type", domain.CreateWSO2CloudRequestRequest{Type: "rename_user", TargetID: "u1", TargetLabel: "a@b.c"}, "not a valid WSO2 Cloud request type"},
		{"no target id", domain.CreateWSO2CloudRequestRequest{Type: domain.WSO2CloudRequestUnlockUser, TargetID: "  ", TargetLabel: "a@b.c"}, "targetId is required"},
		{"no target label", domain.CreateWSO2CloudRequestRequest{Type: domain.WSO2CloudRequestUnlockUser, TargetID: "u1"}, "targetLabel is required"},
		{"lock needs a reason", domain.CreateWSO2CloudRequestRequest{Type: domain.WSO2CloudRequestLockUser, TargetID: "u1", TargetLabel: "a@b.c", Reason: strp("   ")}, "reason is required for a lock user request"},
		{"disable needs a reason", domain.CreateWSO2CloudRequestRequest{Type: domain.WSO2CloudRequestDisableUser, TargetID: "u1", TargetLabel: "a@b.c"}, "reason is required for a disable user request"},
		{"upgrade needs a tier", domain.CreateWSO2CloudRequestRequest{Type: domain.WSO2CloudRequestUpgradeOrganization, TargetID: "o1", TargetLabel: "acme"}, "tier is required"},
		{"unknown tier", domain.CreateWSO2CloudRequestRequest{Type: domain.WSO2CloudRequestUpgradeOrganization, TargetID: "o1", TargetLabel: "acme", Tier: tierp("gold")}, "not a valid WSO2 Cloud tier"},
		{"limits must be an object", domain.CreateWSO2CloudRequestRequest{Type: domain.WSO2CloudRequestUpgradeOrganization, TargetID: "o1", TargetLabel: "acme", Tier: tierp("enterprise"), Limits: json.RawMessage(`[1,2]`)}, "limits must be a JSON object"},
		{"tier only on an upgrade", domain.CreateWSO2CloudRequestRequest{Type: domain.WSO2CloudRequestDeleteOrganization, TargetID: "o1", TargetLabel: "acme", Tier: tierp("free")}, "only accepted on an upgrade_organization request"},
		{"long reason", domain.CreateWSO2CloudRequestRequest{Type: domain.WSO2CloudRequestLockUser, TargetID: "u1", TargetLabel: "a@b.c", Reason: strp(strings.Repeat("x", 2001))}, "reason must be at most 2000 characters"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeWCRRepo{}
			svc := NewWSO2CloudRequestService(repo, internalAccess{})
			_, err := svc.Create(wcrUserCtx("engineer@wso2.com"), tc.req)
			var ve *apierror.ValidationError
			if !errors.As(err, &ve) || !strings.Contains(ve.Msg, tc.wantErr) {
				t.Fatalf("got %v, want a ValidationError containing %q", err, tc.wantErr)
			}
			if len(repo.created) != 0 {
				t.Fatalf("a refused request reached the repository")
			}
		})
	}
}

func TestWSO2CloudRequestService_CreateNormalisesAndUsesTheToken(t *testing.T) {
	repo := &fakeWCRRepo{}
	svc := NewWSO2CloudRequestService(repo, internalAccess{})
	_, err := svc.Create(wcrUserCtx("engineer@wso2.com"), domain.CreateWSO2CloudRequestRequest{
		Type: domain.WSO2CloudRequestUpgradeOrganization, TargetID: " o1 ", TargetLabel: " acme ",
		Reason: strp("  "), Tier: tierp("enterprise"), Limits: json.RawMessage(` {"monthlyActiveUsers": 50000} `),
		PreviousLimits: json.RawMessage(`null`),
	})
	if err != nil {
		t.Fatal(err)
	}
	got := repo.created[0]
	if got.TargetID != "o1" || got.TargetLabel != "acme" || got.Reason != nil {
		t.Fatalf("not normalised: %+v", got)
	}
	if string(got.Limits) != `{"monthlyActiveUsers": 50000}` || got.PreviousLimits != nil {
		t.Fatalf("limits: %q / %q", got.Limits, got.PreviousLimits)
	}
	if repo.createdBy != "engineer@wso2.com" {
		t.Fatalf("requested by %q, want the token's email", repo.createdBy)
	}
}

func TestWSO2CloudRequestService_InternalCallersOnly(t *testing.T) {
	repo := &fakeWCRRepo{}
	svc := NewWSO2CloudRequestService(repo, customerAccess{})
	_, err := svc.Create(wcrUserCtx("customer@example.com"), domain.CreateWSO2CloudRequestRequest{Type: domain.WSO2CloudRequestUnlockUser, TargetID: "u1", TargetLabel: "a@b.c"})
	var fe *apierror.ForbiddenError
	if !errors.As(err, &fe) {
		t.Fatalf("got %v, want ForbiddenError", err)
	}
}

func TestWSO2CloudRequestService_CreateNeedsAPerson(t *testing.T) {
	svc := NewWSO2CloudRequestService(&fakeWCRRepo{}, internalAccess{})
	ctx := auth.WithIdentity(context.Background(), auth.Identity{Validated: true, ClientID: "m2m"})
	_, err := svc.Create(ctx, domain.CreateWSO2CloudRequestRequest{Type: domain.WSO2CloudRequestUnlockUser, TargetID: "u1", TargetLabel: "a@b.c"})
	var ue *apierror.UnauthorizedError
	if !errors.As(err, &ue) {
		t.Fatalf("got %v, want UnauthorizedError", err)
	}
}

func TestWSO2CloudRequestService_Resolve(t *testing.T) {
	pending := func() *domain.WSO2CloudRequest {
		return &domain.WSO2CloudRequest{ID: testWCRID, RequestedBy: "Requester@wso2.com", Status: domain.WSO2CloudRequestPending}
	}
	t.Run("reject needs a remark", func(t *testing.T) {
		svc := NewWSO2CloudRequestService(&fakeWCRRepo{stored: pending()}, internalAccess{})
		_, err := svc.Resolve(wcrUserCtx("approver@wso2.com"), testWCRID, domain.ResolveWSO2CloudRequestRequest{Status: domain.WSO2CloudRequestRejected, Remark: strp(" ")})
		var ve *apierror.ValidationError
		if !errors.As(err, &ve) || !strings.Contains(ve.Msg, "remark is required") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("status must be a decision", func(t *testing.T) {
		svc := NewWSO2CloudRequestService(&fakeWCRRepo{stored: pending()}, internalAccess{})
		_, err := svc.Resolve(wcrUserCtx("approver@wso2.com"), testWCRID, domain.ResolveWSO2CloudRequestRequest{Status: domain.WSO2CloudRequestPending})
		var ve *apierror.ValidationError
		if !errors.As(err, &ve) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("the requester cannot resolve their own request", func(t *testing.T) {
		repo := &fakeWCRRepo{stored: pending()}
		svc := NewWSO2CloudRequestService(repo, internalAccess{})
		_, err := svc.Resolve(wcrUserCtx("requester@wso2.com"), testWCRID, domain.ResolveWSO2CloudRequestRequest{Status: domain.WSO2CloudRequestApproved})
		var fe *apierror.ForbiddenError
		if !errors.As(err, &fe) {
			t.Fatalf("got %v, want ForbiddenError", err)
		}
		if repo.resolveArgs != nil {
			t.Fatalf("a refused resolve reached the repository")
		}
	})
	t.Run("already resolved", func(t *testing.T) {
		stored := pending()
		stored.Status = domain.WSO2CloudRequestApproved
		svc := NewWSO2CloudRequestService(&fakeWCRRepo{stored: stored}, internalAccess{})
		_, err := svc.Resolve(wcrUserCtx("approver@wso2.com"), testWCRID, domain.ResolveWSO2CloudRequestRequest{Status: domain.WSO2CloudRequestApproved})
		var ce *apierror.ConflictError
		if !errors.As(err, &ce) {
			t.Fatalf("got %v, want ConflictError", err)
		}
	})
	t.Run("resolved by someone else in between", func(t *testing.T) {
		svc := NewWSO2CloudRequestService(&fakeWCRRepo{stored: pending(), resolveNil: true}, internalAccess{})
		_, err := svc.Resolve(wcrUserCtx("approver@wso2.com"), testWCRID, domain.ResolveWSO2CloudRequestRequest{Status: domain.WSO2CloudRequestApproved})
		var ce *apierror.ConflictError
		if !errors.As(err, &ce) {
			t.Fatalf("got %v, want ConflictError", err)
		}
	})
	t.Run("approve", func(t *testing.T) {
		repo := &fakeWCRRepo{stored: pending()}
		svc := NewWSO2CloudRequestService(repo, internalAccess{})
		out, err := svc.Resolve(wcrUserCtx("approver@wso2.com"), testWCRID, domain.ResolveWSO2CloudRequestRequest{Status: domain.WSO2CloudRequestApproved})
		if err != nil {
			t.Fatal(err)
		}
		if out.Status != domain.WSO2CloudRequestApproved || repo.resolveArgs[2] != "approver@wso2.com" {
			t.Fatalf("got %+v / %v", out, repo.resolveArgs)
		}
	})
	t.Run("unknown request", func(t *testing.T) {
		svc := NewWSO2CloudRequestService(&fakeWCRRepo{}, internalAccess{})
		_, err := svc.Resolve(wcrUserCtx("approver@wso2.com"), testWCRID, domain.ResolveWSO2CloudRequestRequest{Status: domain.WSO2CloudRequestApproved})
		var ne *apierror.NotFoundError
		if !errors.As(err, &ne) {
			t.Fatalf("got %v, want NotFoundError", err)
		}
	})
}

func TestWSO2CloudRequestService_Delete(t *testing.T) {
	t.Run("only a pending request", func(t *testing.T) {
		stored := &domain.WSO2CloudRequest{ID: testWCRID, Status: domain.WSO2CloudRequestRejected}
		svc := NewWSO2CloudRequestService(&fakeWCRRepo{stored: stored}, internalAccess{})
		err := svc.Delete(wcrUserCtx("a@wso2.com"), testWCRID)
		var ce *apierror.ConflictError
		if !errors.As(err, &ce) {
			t.Fatalf("got %v, want ConflictError", err)
		}
	})
	t.Run("unknown", func(t *testing.T) {
		svc := NewWSO2CloudRequestService(&fakeWCRRepo{}, internalAccess{})
		var ne *apierror.NotFoundError
		if err := svc.Delete(wcrUserCtx("a@wso2.com"), testWCRID); !errors.As(err, &ne) {
			t.Fatalf("got %v, want NotFoundError", err)
		}
	})
	t.Run("bad id", func(t *testing.T) {
		svc := NewWSO2CloudRequestService(&fakeWCRRepo{}, internalAccess{})
		var ve *apierror.ValidationError
		if err := svc.Delete(wcrUserCtx("a@wso2.com"), "nope"); !errors.As(err, &ve) {
			t.Fatalf("got %v, want ValidationError", err)
		}
	})
}

func TestWSO2CloudRequestService_SearchValidatesFilters(t *testing.T) {
	repo := &fakeWCRRepo{}
	svc := NewWSO2CloudRequestService(repo, internalAccess{})
	_, err := svc.Search(wcrUserCtx("a@wso2.com"), domain.SearchWSO2CloudRequestsRequest{
		Filters: domain.SearchWSO2CloudRequestsFilters{Statuses: []domain.WSO2CloudRequestStatus{"cancelled"}},
	})
	var ve *apierror.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("got %v, want ValidationError", err)
	}
	out, err := svc.Search(wcrUserCtx("a@wso2.com"), domain.SearchWSO2CloudRequestsRequest{
		Filters: domain.SearchWSO2CloudRequestsFilters{TargetID: strp("  ")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if repo.searched.Filters.TargetID != nil || out.Limit != defaultLimit {
		t.Fatalf("blank target id kept or no default limit: %+v / %+v", repo.searched.Filters, out)
	}
}
