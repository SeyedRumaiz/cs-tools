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
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
)

const cachedTestUserID = "3f1e8d6a-3b4c-4d5e-8f90-123456789abc"

// fakeUserCache is an in-memory UserCache that records invalidations. down
// makes every read miss, as an unreachable Redis does.
type fakeUserCache struct {
	details     map[string]domain.UserDetail
	mes         map[string]domain.GetUserMeResponse
	down        bool
	invalidated []domain.AffectedUser
}

func newFakeUserCache() *fakeUserCache {
	return &fakeUserCache{details: map[string]domain.UserDetail{}, mes: map[string]domain.GetUserMeResponse{}}
}

func (f *fakeUserCache) GetUserDetail(_ context.Context, id string) (domain.UserDetail, bool) {
	if f.down {
		return domain.UserDetail{}, false
	}
	d, ok := f.details[id]
	return d, ok
}

func (f *fakeUserCache) SetUserDetail(_ context.Context, d domain.UserDetail) {
	if !f.down {
		f.details[d.ID] = d
	}
}

func (f *fakeUserCache) GetMe(_ context.Context, email string) (domain.GetUserMeResponse, bool) {
	if f.down {
		return domain.GetUserMeResponse{}, false
	}
	me, ok := f.mes[strings.ToLower(email)]
	return me, ok
}

func (f *fakeUserCache) SetMe(_ context.Context, email string, me domain.GetUserMeResponse) {
	if !f.down {
		f.mes[strings.ToLower(email)] = me
	}
}

func (f *fakeUserCache) InvalidateUser(_ context.Context, userID, email string) {
	f.invalidated = append(f.invalidated, domain.AffectedUser{ID: userID, Email: email})
}

// countingUserService counts calls to the inner service, so a test can tell
// a cache hit (no call) from a miss.
type countingUserService struct {
	UserService
	getUserCalls int
	getMeCalls   int
	getUser      func(id string) (domain.UserDetail, error)
	getMe        func() (domain.GetUserMeResponse, error)
	patchMe      func() (domain.PatchUserMeResponse, error)
	createUser   func(req domain.CreateUserRequest) (domain.User, error)
}

func (s *countingUserService) GetUser(_ context.Context, id string) (domain.UserDetail, error) {
	s.getUserCalls++
	return s.getUser(id)
}

func (s *countingUserService) GetMe(context.Context) (domain.GetUserMeResponse, error) {
	s.getMeCalls++
	return s.getMe()
}

func (s *countingUserService) PatchMe(context.Context, domain.PatchUserMeRequest) (domain.PatchUserMeResponse, error) {
	return s.patchMe()
}

func (s *countingUserService) CreateUser(_ context.Context, req domain.CreateUserRequest) (domain.User, error) {
	return s.createUser(req)
}

func TestCachedUserService_GetUser_MissFillsThenHitSkipsInner(t *testing.T) {
	inner := &countingUserService{getUser: func(id string) (domain.UserDetail, error) {
		return domain.UserDetail{ID: id, Email: "jane@example.com"}, nil
	}}
	cache := newFakeUserCache()
	svc := NewCachedUserService(inner, cache)

	for i := 0; i < 2; i++ {
		got, err := svc.GetUser(context.Background(), cachedTestUserID)
		if err != nil || got.ID != cachedTestUserID {
			t.Fatalf("GetUser #%d = %+v, %v", i+1, got, err)
		}
	}
	if inner.getUserCalls != 1 {
		t.Errorf("inner GetUser called %d times, want 1 (the second call must be a cache hit)", inner.getUserCalls)
	}
}

func TestCachedUserService_GetUser_ErrorsAreNotCached(t *testing.T) {
	inner := &countingUserService{getUser: func(string) (domain.UserDetail, error) {
		return domain.UserDetail{}, &apierror.NotFoundError{Msg: "user not found"}
	}}
	cache := newFakeUserCache()
	svc := NewCachedUserService(inner, cache)

	for i := 0; i < 2; i++ {
		if _, err := svc.GetUser(context.Background(), cachedTestUserID); err == nil {
			t.Fatal("GetUser = nil error, want the inner NotFoundError")
		}
	}
	if inner.getUserCalls != 2 || len(cache.details) != 0 {
		t.Errorf("inner calls = %d, cached = %d; a NotFoundError must reach the inner service every time and never be cached",
			inner.getUserCalls, len(cache.details))
	}
}

func TestCachedUserService_GetUser_MalformedIDNeverReachesTheCache(t *testing.T) {
	inner := &countingUserService{}
	svc := NewCachedUserService(inner, newFakeUserCache())
	_, err := svc.GetUser(context.Background(), "not-a-uuid")
	var ve *apierror.ValidationError
	if !errors.As(err, &ve) || inner.getUserCalls != 0 {
		t.Errorf("GetUser = %v (inner calls %d); want a ValidationError before any lookup", err, inner.getUserCalls)
	}
}

func TestCachedUserService_GetUser_CacheDownFallsBackToInner(t *testing.T) {
	inner := &countingUserService{getUser: func(id string) (domain.UserDetail, error) {
		return domain.UserDetail{ID: id}, nil
	}}
	cache := newFakeUserCache()
	cache.down = true
	svc := NewCachedUserService(inner, cache)
	for i := 0; i < 2; i++ {
		if _, err := svc.GetUser(context.Background(), cachedTestUserID); err != nil {
			t.Fatalf("GetUser with the cache down = %v, want the inner result", err)
		}
	}
	if inner.getUserCalls != 2 {
		t.Errorf("inner GetUser called %d times, want 2", inner.getUserCalls)
	}
}

func TestCachedUserService_GetMe_KeyedByCallerEmail(t *testing.T) {
	inner := &countingUserService{getMe: func() (domain.GetUserMeResponse, error) {
		return domain.GetUserMeResponse{ID: cachedTestUserID, Email: "jane.doe@example.com"}, nil
	}}
	cache := newFakeUserCache()
	svc := NewCachedUserService(inner, cache)
	ctx := contextWithUserIDToken(fakeJWTWithEmail(t, "jane.doe@example.com"))

	for i := 0; i < 2; i++ {
		got, err := svc.GetMe(ctx)
		if err != nil || got.ID != cachedTestUserID {
			t.Fatalf("GetMe #%d = %+v, %v", i+1, got, err)
		}
	}
	if inner.getMeCalls != 1 {
		t.Errorf("inner GetMe called %d times, want 1", inner.getMeCalls)
	}

	other := contextWithUserIDToken(fakeJWTWithEmail(t, "someone.else@example.com"))
	if _, err := svc.GetMe(other); err != nil {
		t.Fatal(err)
	}
	if inner.getMeCalls != 2 {
		t.Error("another caller was served the first caller's cached profile")
	}
}

func TestCachedUserService_GetMe_NoTokenDelegatesForTheError(t *testing.T) {
	inner := &countingUserService{getMe: func() (domain.GetUserMeResponse, error) {
		return domain.GetUserMeResponse{}, &apierror.UnauthorizedError{Msg: "x-user-id-token header is required"}
	}}
	cache := newFakeUserCache()
	svc := NewCachedUserService(inner, cache)
	_, err := svc.GetMe(contextWithUserIDToken(""))
	var ue *apierror.UnauthorizedError
	if !errors.As(err, &ue) || len(cache.mes) != 0 {
		t.Errorf("GetMe = %v, cached = %d; want the inner UnauthorizedError and nothing cached", err, len(cache.mes))
	}
}

func TestCachedUserService_PatchMe_InvalidatesTheCaller(t *testing.T) {
	inner := &countingUserService{patchMe: func() (domain.PatchUserMeResponse, error) {
		return domain.PatchUserMeResponse{User: domain.PatchUserMeUpdated{ID: cachedTestUserID}}, nil
	}}
	cache := newFakeUserCache()
	svc := NewCachedUserService(inner, cache)
	ctx := contextWithUserIDToken(fakeJWTWithEmail(t, "jane.doe@example.com"))

	if _, err := svc.PatchMe(ctx, domain.PatchUserMeRequest{TimeZone: "Asia/Colombo"}); err != nil {
		t.Fatal(err)
	}
	want := []domain.AffectedUser{{ID: cachedTestUserID, Email: "jane.doe@example.com"}}
	if len(cache.invalidated) != 1 || cache.invalidated[0] != want[0] {
		t.Errorf("invalidated = %+v, want %+v", cache.invalidated, want)
	}
}

func TestCachedUserService_FailedWritesDoNotInvalidate(t *testing.T) {
	failure := &apierror.ValidationError{Msg: "bad"}
	inner := &countingUserService{
		patchMe:    func() (domain.PatchUserMeResponse, error) { return domain.PatchUserMeResponse{}, failure },
		createUser: func(domain.CreateUserRequest) (domain.User, error) { return domain.User{}, failure },
	}
	cache := newFakeUserCache()
	svc := NewCachedUserService(inner, cache)
	ctx := contextWithUserIDToken(fakeJWTWithEmail(t, "jane.doe@example.com"))

	_, _ = svc.PatchMe(ctx, domain.PatchUserMeRequest{TimeZone: "x"})
	_, _ = svc.CreateUser(ctx, domain.CreateUserRequest{Email: "new@example.com"})
	if len(cache.invalidated) != 0 {
		t.Errorf("invalidated = %+v after two failed writes, want none", cache.invalidated)
	}
}

func TestCachedUserService_CreateUser_InvalidatesTheNewUser(t *testing.T) {
	inner := &countingUserService{createUser: func(req domain.CreateUserRequest) (domain.User, error) {
		return domain.User{ID: cachedTestUserID, Email: strings.ToLower(req.Email)}, nil
	}}
	cache := newFakeUserCache()
	svc := NewCachedUserService(inner, cache)

	if _, err := svc.CreateUser(context.Background(), domain.CreateUserRequest{Email: "New.User@Example.com"}); err != nil {
		t.Fatal(err)
	}
	want := domain.AffectedUser{ID: cachedTestUserID, Email: "new.user@example.com"}
	if len(cache.invalidated) != 1 || cache.invalidated[0] != want {
		t.Errorf("invalidated = %+v, want [%+v]", cache.invalidated, want)
	}
}

func TestInvalidateUser_NilInvalidatorIsANoOp(t *testing.T) {
	invalidateUser(context.Background(), nil, cachedTestUserID, "jane@example.com")
}
