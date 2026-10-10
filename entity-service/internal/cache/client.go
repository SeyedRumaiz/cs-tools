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

// Package cache holds this service's Redis-backed caches. Redis is never a
// source of truth here: every cached value can be rebuilt from Postgres, so a
// Redis that is down, slow or empty costs latency, never correctness.
package cache

import (
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/config"
)

// The timeouts are deliberately tight. A cache read sits in front of a
// Postgres query that answers in a few milliseconds, so a Redis that takes
// longer than this is worse than no Redis, and the caller falls back to
// Postgres rather than wait.
const (
	dialTimeout = 500 * time.Millisecond
	ioTimeout   = 200 * time.Millisecond
)

// NewRedisClient builds the client for cfg's REDIS_URL, or REDIS_ADDR/
// REDIS_PASSWORD when REDIS_URL is unset — the same precedence as
// integrations/csm-notification-service. It returns nil, nil when neither is
// set. Connecting is lazy: a wrong address surfaces as a logged cache miss on
// first use, not here.
//
// A plain redis.NewClient only supports a non-clustered Redis, or a managed
// one under the "Enterprise" clustering policy; "OSS Cluster" needs a
// cluster-aware client, which this is not.
func NewRedisClient(cfg *config.Config) (*redis.Client, error) {
	var opts *redis.Options
	switch {
	case cfg.RedisURL != "":
		parsed, err := redis.ParseURL(cfg.RedisURL)
		if err != nil {
			// Not wrapped: ParseURL's error can quote the URL, password included.
			return nil, errors.New("invalid REDIS_URL: failed to parse connection string")
		}
		opts = parsed
	case cfg.RedisAddr != "":
		opts = &redis.Options{Addr: cfg.RedisAddr, Password: cfg.RedisPassword}
	default:
		return nil, nil
	}
	opts.DialTimeout = dialTimeout
	opts.ReadTimeout = ioTimeout
	opts.WriteTimeout = ioTimeout
	// Honour the per-call context deadline too, so a cache call can never
	// outlive the request that made it.
	opts.ContextTimeoutEnabled = true
	// One retry at most, and one dial attempt per connection (the default is
	// five, with backoff): each retry is more time added to a request that is
	// about to fall back to Postgres anyway. With Redis down, the pool's own
	// breaker then fails calls fast after PoolSize dial failures and probes in
	// the background until it is back.
	opts.MaxRetries = 1
	opts.DialerRetries = 1
	return redis.NewClient(opts), nil
}
