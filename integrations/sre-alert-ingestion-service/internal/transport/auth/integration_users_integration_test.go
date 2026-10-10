//go:build integration

// Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com).
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

// Package auth checks source webhooks against alerts-core's integration_users table (AUTH_ENABLED).
package auth

import (
	"context"
	"crypto/pbkdf2"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"sre-alert-ingestion-service/internal/postgres"
)

// TestAuthenticate_ConcurrentBurstOnSmallPool reproduces a cold-cache burst of 50 requests against a 2-connection pool on a throwaway database created from PG*.
func TestAuthenticate_ConcurrentBurstOnSmallPool(t *testing.T) {
	cfg, err := postgres.ConfigFromEnv()
	if err != nil {
		t.Skipf("PG* not set: %v", err)
	}
	ctx := context.Background()
	admin, err := postgres.Connect(cfg, 10*time.Second, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	name := fmt.Sprintf("ingestion_auth_test_%d", rand.Int63())
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = admin.Exec(context.Background(), "DROP DATABASE "+name+" WITH (FORCE)") }()
	cfg.Database = name
	cfg.PoolMaxConns = 2
	pool, err := postgres.Connect(cfg, 10*time.Second, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}

	salt := []byte("0123456789abcdef")
	hash, err := pbkdf2.Key(sha256.New, "test", salt, 10000, keyLen)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `CREATE TABLE integration_users (username text PRIMARY KEY, secret_hash text, salt text,
		iterations int, enabled boolean)`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO integration_users VALUES ('test', $1, $2, 10000, true)`,
		base64.StdEncoding.EncodeToString(hash), base64.StdEncoding.EncodeToString(salt)); err != nil {
		t.Fatal(err)
	}

	// A row with NULL fields must be skipped on its own, not fail the whole load.
	if _, err := pool.Exec(ctx, `INSERT INTO integration_users (username, secret_hash, salt, iterations, enabled) VALUES ('broken', NULL, NULL, 10000, true)`); err != nil {
		t.Fatal(err)
	}

	authn := NewIntegrationUsers(pool, slog.New(slog.NewTextHandler(io.Discard, nil)), UsersConfig{
		QueryTimeout: 2 * time.Second, RefreshInterval: 30 * time.Second, MaxStale: 15 * time.Minute, CacheTTL: time.Minute,
	})
	if err := authn.Refresh(ctx); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	request := func(user, secret string) error {
		r := httptest.NewRequest("POST", "/elasticsearch", nil)
		r.SetBasicAuth(user, secret)
		return authn.Authenticate(r, "elasticsearch")
	}

	var wg sync.WaitGroup
	errs := make(chan error, 50)
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- request("test", "test")
		}()
	}
	wg.Wait()
	close(errs)
	failed := 0
	for err := range errs {
		if err != nil {
			failed++
		}
	}
	if failed != 0 {
		t.Fatalf("%d of 50 concurrent requests with valid credentials failed", failed)
	}

	if err := request("test", "wrong"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("wrong secret = %v, want ErrUnauthorized", err)
	}
	if err := request("nobody", "test"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("unknown user = %v, want ErrUnauthorized", err)
	}
	pool.Close()
	if err := authn.Refresh(ctx); err == nil {
		t.Fatal("Refresh on a closed pool must fail")
	}
	if err := request("test", "test"); err != nil {
		t.Fatalf("a failed refresh must keep the last good copy, got %v", err)
	}
}
