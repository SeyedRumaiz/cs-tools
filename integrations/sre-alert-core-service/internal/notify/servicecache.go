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

package notify

import (
	"sync"
	"time"
)

// serviceCache only caches successful resolutions; caching a zero-result or error risks a stale wrong answer.
type serviceCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	entries map[string]serviceCacheEntry
}

type serviceCacheEntry struct {
	svc       resolvedService
	expiresAt time.Time
}

func newServiceCache(ttl time.Duration) *serviceCache {
	return &serviceCache{ttl: ttl, entries: make(map[string]serviceCacheEntry)}
}

func (c *serviceCache) get(label string, now time.Time) (resolvedService, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[label]
	if !ok || now.After(e.expiresAt) {
		return resolvedService{}, false
	}
	return e.svc, true
}

func (c *serviceCache) set(label string, svc resolvedService, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[label] = serviceCacheEntry{svc: svc, expiresAt: now.Add(c.ttl)}
}
