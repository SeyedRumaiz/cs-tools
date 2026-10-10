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

package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
)

// userKeyPrefix namespaces every key this cache writes. "entity:" keeps them
// apart from other services sharing the instance (csm-notification-service's
// "sla:tier:*"); "v1" is bumped whenever a cached struct's shape changes, so
// entries written by an older build are simply never read again and age out.
const userKeyPrefix = "entity:v1:user:"

// opTimeout bounds one cache operation, which may be two round trips (GetMe,
// InvalidateUser). Past it the caller treats the cache as a miss.
const opTimeout = 250 * time.Millisecond

// warnInterval rate-limits the "Redis is failing" warning: with Redis down,
// every request would otherwise log one.
const warnInterval = 30 * time.Second

// UserCache caches GET /users/{id} and GET /users/me responses in Redis.
//
// Three keys per user:
//
//	entity:v1:user:detail:{id}            domain.UserDetail (GET /users/{id})
//	entity:v1:user:me:{id}                domain.GetUserMeResponse (GET /users/me)
//	entity:v1:user:id-by-email:{sha256}   the user's id, keyed by the hash of
//	                                      their lower-cased email
//
// The email index lets GetMe, which only knows the caller's email, find the
// "me" entry, and lets InvalidateUser clear a user that a writer can only name
// by email. Every write of a detail or "me" entry refreshes the index too, so
// the index never expires before the entries it points at. The email is
// hashed so the key space holds no addresses.
//
// Every method fails open: a Redis error is logged (rate-limited) and treated
// as a miss, or as a no-op for writes. Nothing here ever fails a request.
type UserCache struct {
	rdb      *redis.Client
	ttl      time.Duration
	lastWarn atomic.Int64
}

// NewUserCache constructs a UserCache whose entries expire after ttl.
func NewUserCache(rdb *redis.Client, ttl time.Duration) *UserCache {
	return &UserCache{rdb: rdb, ttl: ttl}
}

func userDetailKey(id string) string {
	return userKeyPrefix + "detail:" + strings.ToLower(strings.TrimSpace(id))
}

func userMeKey(id string) string {
	return userKeyPrefix + "me:" + strings.ToLower(strings.TrimSpace(id))
}

func userEmailKey(email string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(email))))
	return userKeyPrefix + "id-by-email:" + hex.EncodeToString(sum[:])
}

// GetUserDetail returns the cached GET /users/{id} response, if any.
func (c *UserCache) GetUserDetail(ctx context.Context, id string) (domain.UserDetail, bool) {
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	var d domain.UserDetail
	if !c.getJSON(ctx, userDetailKey(id), &d) {
		return domain.UserDetail{}, false
	}
	return d, true
}

// SetUserDetail caches a GET /users/{id} response.
func (c *UserCache) SetUserDetail(ctx context.Context, d domain.UserDetail) {
	if d.ID == "" {
		return
	}
	raw, err := json.Marshal(d)
	if err != nil {
		c.warn(ctx, "user cache: could not encode user detail", err)
		return
	}
	ctx, cancel := writeContext(ctx)
	defer cancel()
	_, err = c.rdb.Pipelined(ctx, func(p redis.Pipeliner) error {
		p.Set(ctx, userDetailKey(d.ID), raw, c.ttl)
		if d.Email != "" {
			p.Set(ctx, userEmailKey(d.Email), d.ID, c.ttl)
		}
		return nil
	})
	if err != nil {
		c.warn(ctx, "user cache: write failed", err)
	}
}

// GetMe returns the cached GET /users/me response for the caller with this
// email, if any. An entry whose email no longer matches (the user's address
// changed since the index was written) is a miss, never another user's
// profile.
func (c *UserCache) GetMe(ctx context.Context, email string) (domain.GetUserMeResponse, bool) {
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	id, err := c.rdb.Get(ctx, userEmailKey(email)).Result()
	if err != nil {
		if !errors.Is(err, redis.Nil) {
			c.warn(ctx, "user cache: read failed, falling back to the database", err)
		}
		return domain.GetUserMeResponse{}, false
	}
	var me domain.GetUserMeResponse
	if !c.getJSON(ctx, userMeKey(id), &me) {
		return domain.GetUserMeResponse{}, false
	}
	if !strings.EqualFold(strings.TrimSpace(me.Email), strings.TrimSpace(email)) {
		return domain.GetUserMeResponse{}, false
	}
	return me, true
}

// SetMe caches a GET /users/me response for the caller with this email.
func (c *UserCache) SetMe(ctx context.Context, email string, me domain.GetUserMeResponse) {
	if me.ID == "" || strings.TrimSpace(email) == "" {
		return
	}
	raw, err := json.Marshal(me)
	if err != nil {
		c.warn(ctx, "user cache: could not encode user profile", err)
		return
	}
	ctx, cancel := writeContext(ctx)
	defer cancel()
	_, err = c.rdb.Pipelined(ctx, func(p redis.Pipeliner) error {
		p.Set(ctx, userMeKey(me.ID), raw, c.ttl)
		p.Set(ctx, userEmailKey(email), me.ID, c.ttl)
		return nil
	})
	if err != nil {
		c.warn(ctx, "user cache: write failed", err)
	}
}

// InvalidateUser deletes every cached entry for the user with this id and/or
// email. Either may be empty; with only an email, the id is found through the
// email index. Call it after the write has COMMITTED: deleting first lets a
// concurrent read re-cache the old row before the commit lands.
//
// A failure is logged and swallowed. The write itself has succeeded, and the
// stale entry still expires after the TTL.
func (c *UserCache) InvalidateUser(ctx context.Context, userID, email string) {
	ctx, cancel := writeContext(ctx)
	defer cancel()

	var keys []string
	ids := []string{}
	if strings.TrimSpace(userID) != "" {
		ids = append(ids, userID)
	}
	if strings.TrimSpace(email) != "" {
		emailKey := userEmailKey(email)
		keys = append(keys, emailKey)
		indexed, err := c.rdb.Get(ctx, emailKey).Result()
		switch {
		case err == nil && indexed != "" && !strings.EqualFold(indexed, userID):
			ids = append(ids, indexed)
		case err != nil && !errors.Is(err, redis.Nil):
			c.warn(ctx, "user cache: could not read the email index during invalidation", err)
		}
	}
	for _, id := range ids {
		keys = append(keys, userDetailKey(id), userMeKey(id))
	}
	if len(keys) == 0 {
		return
	}
	if err := c.rdb.Del(ctx, keys...).Err(); err != nil {
		// Not rate-limited: a missed invalidation serves stale data for up
		// to the TTL, which is worth a line every time.
		slog.ErrorContext(ctx, "user cache: invalidation failed, the entry stays until its TTL expires",
			"userId", userID, "err", err)
	}
}

func (c *UserCache) getJSON(ctx context.Context, key string, out any) bool {
	raw, err := c.rdb.Get(ctx, key).Bytes()
	if err != nil {
		if !errors.Is(err, redis.Nil) {
			c.warn(ctx, "user cache: read failed, falling back to the database", err)
		}
		return false
	}
	if err := json.Unmarshal(raw, out); err != nil {
		c.warn(ctx, "user cache: ignoring an undecodable entry", err)
		return false
	}
	return true
}

// warn logs at most once per warnInterval. Never pass a key or an email:
// keys can be traced back to an address, and log lines must not carry one
// (see user_repo.go's GetUserByEmail).
func (c *UserCache) warn(ctx context.Context, msg string, err error) {
	now := time.Now().UnixNano()
	last := c.lastWarn.Load()
	if now-last < int64(warnInterval) || !c.lastWarn.CompareAndSwap(last, now) {
		return
	}
	slog.WarnContext(ctx, msg, "err", err)
}

// writeContext detaches cache writes from the request's cancellation. They
// run after the response is already decided, and an invalidation in
// particular must not be skipped because the client hung up.
func writeContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), opTimeout)
}
