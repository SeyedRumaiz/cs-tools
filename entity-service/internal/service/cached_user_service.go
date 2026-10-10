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

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
)

// cachedUserService puts a cache-aside UserCache in front of a UserService:
// GetUser and GetMe read the cache first and fill it on a miss, and the two
// writes (PatchMe, CreateUser) invalidate the user they changed. Everything
// else passes straight through.
//
// Only successful responses are cached. An error, NotFoundError included, is
// returned uncached, so a user created a moment later is found at once.
//
// Caching GetUser by id alone is safe because its response does not depend
// on who is asking: authorization runs in the middleware before the handler,
// on every request, hit or miss. GetMe is keyed by the caller's own email.
type cachedUserService struct {
	UserService
	cache UserCache
}

// NewCachedUserService wraps inner with cache.
func NewCachedUserService(inner UserService, cache UserCache) UserService {
	return &cachedUserService{UserService: inner, cache: cache}
}

// GetUser implements UserService.
func (s *cachedUserService) GetUser(ctx context.Context, id string) (domain.UserDetail, error) {
	// Validate before touching the cache, so a malformed id costs no round
	// trip and still gets the inner service's ValidationError.
	if err := validateUUIDs("id", []string{id}); err != nil {
		return domain.UserDetail{}, err
	}
	if d, ok := s.cache.GetUserDetail(ctx, id); ok {
		return d, nil
	}
	d, err := s.UserService.GetUser(ctx, id)
	if err != nil {
		return domain.UserDetail{}, err
	}
	s.cache.SetUserDetail(ctx, d)
	return d, nil
}

// GetMe implements UserService.
func (s *cachedUserService) GetMe(ctx context.Context) (domain.GetUserMeResponse, error) {
	email, err := callerEmail(ctx)
	if err != nil || email == "" {
		// A missing or undecodable token: the inner service returns the
		// right error for it.
		return s.UserService.GetMe(ctx)
	}
	if me, hit := s.cache.GetMe(ctx, email); hit {
		return me, nil
	}
	me, err := s.UserService.GetMe(ctx)
	if err != nil {
		return domain.GetUserMeResponse{}, err
	}
	s.cache.SetMe(ctx, email, me)
	return me, nil
}

// PatchMe implements UserService.
func (s *cachedUserService) PatchMe(ctx context.Context, req domain.PatchUserMeRequest) (domain.PatchUserMeResponse, error) {
	resp, err := s.UserService.PatchMe(ctx, req)
	if err != nil {
		return resp, err
	}
	// PatchMe resolved the same token successfully, so this cannot fail; an
	// empty email would still invalidate by id.
	email, _ := callerEmail(ctx)
	s.cache.InvalidateUser(ctx, resp.User.ID, email)
	return resp, nil
}

// CreateUser implements UserService.
func (s *cachedUserService) CreateUser(ctx context.Context, req domain.CreateUserRequest) (domain.User, error) {
	u, err := s.UserService.CreateUser(ctx, req)
	if err != nil {
		return u, err
	}
	email := u.Email
	if email == "" {
		email = req.Email
	}
	s.cache.InvalidateUser(ctx, u.ID, email)
	return u, nil
}

// invalidateUser is a nil-safe UserCacheInvalidator call, for the writers
// whose cache dependency is optional (nil when Redis is not configured).
func invalidateUser(ctx context.Context, c UserCacheInvalidator, userID, email string) {
	if c == nil {
		return
	}
	c.InvalidateUser(ctx, userID, email)
}
