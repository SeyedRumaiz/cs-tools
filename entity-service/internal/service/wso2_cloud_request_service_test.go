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
	// setAccount is what SetBillingAccount returns; setArgs what it was called with.
	setAccount *domain.WSO2CloudRequest
	setArgs    []string
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

func (f *fakeWCRRepo) SetBillingAccount(_ context.Context, id, accountID string) (*domain.WSO2CloudRequest, error) {
	f.setArgs = []string{id, accountID}
	return f.setAccount, nil
}

func wcrUserCtx(email string) context.Context {
	return auth.WithIdentity(context.Background(), auth.Identity{Validated: true, UserEmail: email})
}

func periodp(p domain.WSO2CloudBillingPeriod) *domain.WSO2CloudBillingPeriod { return &p }

// wcrUpgrade is a complete upgrade request; tests change one field at a time.
func wcrUpgrade() domain.CreateWSO2CloudRequestRequest {
	return domain.CreateWSO2CloudRequestRequest{
		Type: domain.WSO2CloudRequestUpgradeOrganization, TargetID: "o1", TargetLabel: "acme",
		Product: strp("identity"), SubscriptionID: strp("sub-1"), PlanCode: strp("enterprise-identity"),
	}
}

func withUpgrade(change func(*domain.CreateWSO2CloudRequestRequest)) domain.CreateWSO2CloudRequestRequest {
	r := wcrUpgrade()
	change(&r)
	return r
}

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
		{"upgrade needs a subscription", withUpgrade(func(r *domain.CreateWSO2CloudRequestRequest) { r.SubscriptionID = strp(" ") }), "subscriptionId is required"},
		{"upgrade needs a plan", withUpgrade(func(r *domain.CreateWSO2CloudRequestRequest) { r.PlanCode = nil }), "planCode is required"},
		{"unknown billing period", withUpgrade(func(r *domain.CreateWSO2CloudRequestRequest) { r.BillingPeriod = periodp("weekly") }), "not a valid billing period"},
		{"plan already held", withUpgrade(func(r *domain.CreateWSO2CloudRequestRequest) { r.PreviousPlanCode = strp("enterprise-identity") }), "already on"},
		{"long plan code", withUpgrade(func(r *domain.CreateWSO2CloudRequestRequest) { r.PlanCode = strp(strings.Repeat("p", 101)) }), "planCode must be at most 100 characters"},
		{"plan only on an upgrade", domain.CreateWSO2CloudRequestRequest{Type: domain.WSO2CloudRequestDeleteOrganization, TargetID: "o1", TargetLabel: "acme", PlanCode: strp("x")}, "only accepted on an upgrade_organization request"},
		{"notify only on an upgrade", domain.CreateWSO2CloudRequestRequest{Type: domain.WSO2CloudRequestDeleteOrganization, TargetID: "o1", TargetLabel: "acme", NotifyCustomer: true}, "only accepted on an upgrade_organization request"},
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
		Reason: strp("  "), Product: strp(" identity "), SubscriptionID: strp(" sub-1 "), PlanCode: strp(" enterprise-identity "),
		PreviousPlanCode: strp(""), BillingPeriod: periodp(domain.WSO2CloudBillingAnnual), NotifyCustomer: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	got := repo.created[0]
	if got.TargetID != "o1" || got.TargetLabel != "acme" || got.Reason != nil {
		t.Fatalf("not normalised: %+v", got)
	}
	if *got.Product != "identity" || *got.SubscriptionID != "sub-1" || *got.PlanCode != "enterprise-identity" || got.PreviousPlanCode != nil {
		t.Fatalf("plan fields: %+v", got)
	}
	if *got.BillingPeriod != domain.WSO2CloudBillingAnnual || !got.NotifyCustomer {
		t.Fatalf("period / notify: %+v", got)
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

func TestWSO2CloudRequestService_SetBillingAccount(t *testing.T) {
	pendingUpgrade := func(account *string) *domain.WSO2CloudRequest {
		return &domain.WSO2CloudRequest{ID: testWCRID, Type: domain.WSO2CloudRequestUpgradeOrganization,
			Status: domain.WSO2CloudRequestPending, BillingAccountID: account}
	}
	t.Run("records a trimmed id", func(t *testing.T) {
		repo := &fakeWCRRepo{setAccount: pendingUpgrade(strp("ba-1"))}
		svc := NewWSO2CloudRequestService(repo, internalAccess{})
		got, err := svc.SetBillingAccount(wcrUserCtx("approver@wso2.com"), testWCRID, domain.SetWSO2CloudBillingAccountRequest{BillingAccountID: " ba-1 "})
		if err != nil || *got.BillingAccountID != "ba-1" {
			t.Fatalf("got %+v, %v", got, err)
		}
		if repo.setArgs[1] != "ba-1" {
			t.Fatalf("repository got %q", repo.setArgs[1])
		}
	})
	t.Run("the same id again is accepted", func(t *testing.T) {
		repo := &fakeWCRRepo{stored: pendingUpgrade(strp("ba-1"))}
		svc := NewWSO2CloudRequestService(repo, internalAccess{})
		if _, err := svc.SetBillingAccount(wcrUserCtx("approver@wso2.com"), testWCRID, domain.SetWSO2CloudBillingAccountRequest{BillingAccountID: "ba-1"}); err != nil {
			t.Fatal(err)
		}
	})
	cases := []struct {
		name   string
		stored *domain.WSO2CloudRequest
		body   string
		check  func(error) bool
	}{
		{"blank id", nil, " ", func(err error) bool { var ve *apierror.ValidationError; return errors.As(err, &ve) }},
		{"not found", nil, "ba-1", func(err error) bool { var ne *apierror.NotFoundError; return errors.As(err, &ne) }},
		{"a different id", pendingUpgrade(strp("ba-0")), "ba-1", func(err error) bool { var ce *apierror.ConflictError; return errors.As(err, &ce) }},
		{"not an upgrade", &domain.WSO2CloudRequest{ID: testWCRID, Type: domain.WSO2CloudRequestLockUser, Status: domain.WSO2CloudRequestPending}, "ba-1",
			func(err error) bool { var ve *apierror.ValidationError; return errors.As(err, &ve) }},
		{"already resolved", &domain.WSO2CloudRequest{ID: testWCRID, Type: domain.WSO2CloudRequestUpgradeOrganization, Status: domain.WSO2CloudRequestRejected}, "ba-1",
			func(err error) bool { var ce *apierror.ConflictError; return errors.As(err, &ce) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := NewWSO2CloudRequestService(&fakeWCRRepo{stored: tc.stored}, internalAccess{})
			_, err := svc.SetBillingAccount(wcrUserCtx("approver@wso2.com"), testWCRID, domain.SetWSO2CloudBillingAccountRequest{BillingAccountID: tc.body})
			if !tc.check(err) {
				t.Fatalf("unexpected error %v", err)
			}
		})
	}
	t.Run("needs a user token", func(t *testing.T) {
		svc := NewWSO2CloudRequestService(&fakeWCRRepo{}, internalAccess{})
		_, err := svc.SetBillingAccount(context.Background(), testWCRID, domain.SetWSO2CloudBillingAccountRequest{BillingAccountID: "ba-1"})
		var ue *apierror.UnauthorizedError
		if !errors.As(err, &ue) {
			t.Fatalf("got %v", err)
		}
	})
}
