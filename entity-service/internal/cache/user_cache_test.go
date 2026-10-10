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
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/config"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
)

const (
	testUserID = "3f1e8d6a-3b4c-4d5e-8f90-123456789abc"
	testEmail  = "Jane.Doe@Example.com"
)

func newTestUserCache(t *testing.T) (*UserCache, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb, err := NewRedisClient(&config.Config{RedisAddr: mr.Addr()})
	if err != nil {
		t.Fatalf("NewRedisClient: %v", err)
	}
	t.Cleanup(func() { _ = rdb.Close() })
	return NewUserCache(rdb, 10*time.Minute), mr
}

func TestNewRedisClient(t *testing.T) {
	t.Run("unconfigured returns no client", func(t *testing.T) {
		rdb, err := NewRedisClient(&config.Config{})
		if rdb != nil || err != nil {
			t.Errorf("NewRedisClient = %v, %v; want nil, nil", rdb, err)
		}
	})
	t.Run("rediss URL turns on TLS and wins over REDIS_ADDR", func(t *testing.T) {
		rdb, err := NewRedisClient(&config.Config{
			RedisURL:  "rediss://:pa%2Bss%3D@cache.example.net:10000",
			RedisAddr: "localhost:6379",
		})
		if err != nil {
			t.Fatalf("NewRedisClient: %v", err)
		}
		defer rdb.Close()
		opts := rdb.Options()
		if opts.Addr != "cache.example.net:10000" || opts.TLSConfig == nil || opts.Password != "pa+ss=" {
			t.Errorf("Addr = %q, TLS = %v, Password decoded = %v; want the URL's host, TLS on, %q",
				opts.Addr, opts.TLSConfig != nil, opts.Password == "pa+ss=", "pa+ss=")
		}
	})
	t.Run("unparseable URL error does not leak the password", func(t *testing.T) {
		_, err := NewRedisClient(&config.Config{RedisURL: "rediss://:s3cr3t@cache.example.net:10000/not-a-db"})
		if err == nil {
			t.Fatal("NewRedisClient = nil error, want one for a non-numeric db")
		}
		if strings.Contains(err.Error(), "s3cr3t") {
			t.Errorf("error leaks the password: %v", err)
		}
	})
}

func TestUserCache_UserDetailRoundTrip(t *testing.T) {
	c, mr := newTestUserCache(t)
	ctx := context.Background()

	if _, ok := c.GetUserDetail(ctx, testUserID); ok {
		t.Fatal("GetUserDetail hit on an empty cache")
	}
	want := domain.UserDetail{ID: testUserID, Email: testEmail, Roles: []string{"internal"}, Active: true}
	c.SetUserDetail(ctx, want)

	got, ok := c.GetUserDetail(ctx, strings.ToUpper(testUserID))
	if !ok {
		t.Fatal("GetUserDetail missed after SetUserDetail (the id lookup must be case-insensitive)")
	}
	if got.ID != want.ID || got.Email != want.Email || len(got.Roles) != 1 || !got.Active {
		t.Errorf("GetUserDetail = %+v, want %+v", got, want)
	}
	if ttl := mr.TTL(userDetailKey(testUserID)); ttl != 10*time.Minute {
		t.Errorf("detail TTL = %v, want 10m", ttl)
	}
	if !mr.Exists(userEmailKey(testEmail)) {
		t.Error("SetUserDetail did not write the email index, so an email-only invalidation could miss it")
	}
}

func TestUserCache_MeRoundTrip(t *testing.T) {
	c, _ := newTestUserCache(t)
	ctx := context.Background()

	c.SetMe(ctx, testEmail, domain.GetUserMeResponse{ID: testUserID, Email: testEmail})
	got, ok := c.GetMe(ctx, strings.ToLower(testEmail))
	if !ok || got.ID != testUserID {
		t.Fatalf("GetMe = %+v, %v; want a hit for the same email in another case", got, ok)
	}
	if _, ok := c.GetMe(ctx, "someone.else@example.com"); ok {
		t.Error("GetMe hit for a different email")
	}
}

// TestUserCache_GetMeIgnoresAnEntryForAnotherEmail: after a user's address
// changes, the old address's index entry can still point at their id. A
// "me" entry carrying the new address must not be served to the old one.
func TestUserCache_GetMeIgnoresAnEntryForAnotherEmail(t *testing.T) {
	c, mr := newTestUserCache(t)
	ctx := context.Background()

	c.SetMe(ctx, "new@example.com", domain.GetUserMeResponse{ID: testUserID, Email: "new@example.com"})
	if err := mr.Set(userEmailKey("old@example.com"), testUserID); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.GetMe(ctx, "old@example.com"); ok {
		t.Error("GetMe served the profile of new@example.com to old@example.com")
	}
}

func TestUserCache_InvalidateUser(t *testing.T) {
	ctx := context.Background()
	seed := func(c *UserCache) {
		c.SetUserDetail(ctx, domain.UserDetail{ID: testUserID, Email: testEmail})
		c.SetMe(ctx, testEmail, domain.GetUserMeResponse{ID: testUserID, Email: testEmail})
	}
	tests := []struct {
		name          string
		userID, email string
	}{
		{name: "by id and email", userID: testUserID, email: testEmail},
		{name: "by email only, id found through the index", email: strings.ToUpper(testEmail)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, mr := newTestUserCache(t)
			seed(c)
			c.InvalidateUser(ctx, tt.userID, tt.email)
			for _, key := range []string{userDetailKey(testUserID), userMeKey(testUserID), userEmailKey(testEmail)} {
				if mr.Exists(key) {
					t.Errorf("%s survived InvalidateUser", key)
				}
			}
		})
	}

	t.Run("by id only leaves the index, which then resolves to a miss", func(t *testing.T) {
		c, mr := newTestUserCache(t)
		seed(c)
		c.InvalidateUser(ctx, testUserID, "")
		if mr.Exists(userDetailKey(testUserID)) || mr.Exists(userMeKey(testUserID)) {
			t.Error("detail or me entry survived InvalidateUser by id")
		}
		if _, ok := c.GetMe(ctx, testEmail); ok {
			t.Error("GetMe hit after the user's me entry was invalidated")
		}
	})

	t.Run("nothing to invalidate is a no-op", func(t *testing.T) {
		c, _ := newTestUserCache(t)
		c.InvalidateUser(ctx, "", "")
	})
}

// TestUserCache_FailsOpen: with Redis gone every read is a miss and every
// write a no-op -- nothing panics, nothing blocks past the timeout.
func TestUserCache_FailsOpen(t *testing.T) {
	c, mr := newTestUserCache(t)
	ctx := context.Background()
	c.SetUserDetail(ctx, domain.UserDetail{ID: testUserID, Email: testEmail})
	mr.Close()

	start := time.Now()
	if _, ok := c.GetUserDetail(ctx, testUserID); ok {
		t.Error("GetUserDetail hit with Redis down")
	}
	if _, ok := c.GetMe(ctx, testEmail); ok {
		t.Error("GetMe hit with Redis down")
	}
	c.SetUserDetail(ctx, domain.UserDetail{ID: testUserID})
	c.SetMe(ctx, testEmail, domain.GetUserMeResponse{ID: testUserID, Email: testEmail})
	c.InvalidateUser(ctx, testUserID, testEmail)
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("five operations against a dead Redis took %v; the timeouts are not bounding them", elapsed)
	}
}

func TestUserCache_IgnoresUndecodableEntry(t *testing.T) {
	c, mr := newTestUserCache(t)
	if err := mr.Set(userDetailKey(testUserID), "{not json"); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.GetUserDetail(context.Background(), testUserID); ok {
		t.Error("GetUserDetail hit on a corrupt entry")
	}
}

func TestUserCache_KeysCarryNoEmail(t *testing.T) {
	key := userEmailKey(testEmail)
	if strings.Contains(strings.ToLower(key), "example") || strings.Contains(key, "@") {
		t.Errorf("email index key %q carries the address", key)
	}
	if userEmailKey("  "+strings.ToUpper(testEmail)+" ") != key {
		t.Error("email index key is not case- and whitespace-insensitive")
	}
	if !strings.HasPrefix(key, userKeyPrefix) || !strings.HasPrefix(userDetailKey(testUserID), "entity:v1:") {
		t.Error("keys are not namespaced under entity:v1:")
	}
}
