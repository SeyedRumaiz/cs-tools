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

package handler

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/scim"
)

const (
	// phoneLookupConcurrency bounds the SCIM lookups one read makes at a time.
	phoneLookupConcurrency = 8
	// phoneCacheTTL is how long a person's "has a mobile number on their
	// profile" answer is reused. A number added to a profile shows within
	// this long.
	phoneCacheTTL = 10 * time.Minute
)

// phoneLookupClient is the SCIM lookup used to see whether a person has a
// mobile number on their Asgardeo profile.
type phoneLookupClient interface {
	SearchUser(ctx context.Context, email string) (*scim.UserInfo, error)
}

// phoneCacheEntry is one person's cached answer.
type phoneCacheEntry struct {
	hasPhone bool
	expires  time.Time
}

// PagingPhoneChecker answers "does this person have a mobile number on their
// profile" for the Case Paging reads. entity-service answers first, from the
// number the portal stores on the person ("user".phone); SCIM is asked only
// for the people it does not answer for, with one cache and one concurrency
// bound shared by every read that asks -- the readiness strip and the paging
// chain ask about the same people.
type PagingPhoneChecker struct {
	scim phoneLookupClient
	// scimFallback is whether someone entity-service reports with no profile
	// number is still looked up in SCIM. On until "user".phone has been
	// filled from Asgardeo for everyone; then SCIM is only asked about people
	// an older entity-service does not report on at all.
	scimFallback bool
	now          func() time.Time

	mu    sync.Mutex
	cache map[string]phoneCacheEntry
}

// NewPagingPhoneChecker creates a PagingPhoneChecker backed by SCIM.
func NewPagingPhoneChecker(scimClient phoneLookupClient) *PagingPhoneChecker {
	return &PagingPhoneChecker{
		scim:         scimClient,
		scimFallback: true,
		now:          time.Now,
		cache:        make(map[string]phoneCacheEntry),
	}
}

// WithSCIMFallback turns the SCIM fallback on or off (on by default); see
// PagingPhoneChecker.scimFallback.
func (p *PagingPhoneChecker) WithSCIMFallback(on bool) *PagingPhoneChecker {
	p.scimFallback = on
	return p
}

// profilePhones answers "has a mobile number on their profile" for each email
// (normalised by the caller). entityAnswer is what entity-service said, by
// email; someone it said has one is not asked again. SCIM is asked about
// anyone it said has none, while the fallback is on, and about anyone it said
// nothing about. failed is the SCIM lookups that failed, as in lookup.
func (p *PagingPhoneChecker) profilePhones(ctx context.Context, emails []string, entityAnswer map[string]bool) (hasPhone, failed map[string]bool) {
	var ask []string
	for _, e := range emails {
		has, answered := entityAnswer[e]
		if has || (answered && (p == nil || !p.scimFallback)) {
			continue
		}
		ask = append(ask, e)
	}
	if p != nil && p.scim != nil && len(ask) > 0 {
		hasPhone, failed = p.lookup(ctx, ask)
	} else {
		hasPhone, failed = map[string]bool{}, map[string]bool{}
	}
	for e, has := range entityAnswer {
		if has {
			hasPhone[e] = true
		}
	}
	return hasPhone, failed
}

// lookup answers "has a mobile number on their profile" for each email
// (normalised by the caller), from the cache where it can and from SCIM, at
// most phoneLookupConcurrency at a time, where it cannot. A lookup that fails
// is reported in failed and never cached. A person SCIM does not know has no
// profile, and so no number.
func (p *PagingPhoneChecker) lookup(ctx context.Context, emails []string) (hasPhone, failed map[string]bool) {
	hasPhone = make(map[string]bool, len(emails))
	failed = make(map[string]bool)

	now := p.now()
	var toFetch []string
	p.mu.Lock()
	for _, e := range emails {
		if entry, ok := p.cache[e]; ok && now.Before(entry.expires) {
			hasPhone[e] = entry.hasPhone
			continue
		}
		toFetch = append(toFetch, e)
	}
	p.mu.Unlock()

	if len(toFetch) == 0 {
		return hasPhone, failed
	}

	var (
		wg  sync.WaitGroup
		mu  sync.Mutex
		sem = make(chan struct{}, phoneLookupConcurrency)
	)
	for _, email := range toFetch {
		wg.Add(1)
		sem <- struct{}{}
		go func(email string) {
			defer wg.Done()
			defer func() { <-sem }()

			info, err := p.scim.SearchUser(ctx, email)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failed[email] = true
				return
			}
			hasPhone[email] = info != nil && info.PhoneNumber != nil && strings.TrimSpace(*info.PhoneNumber) != ""
		}(email)
	}
	wg.Wait()

	expires := p.now().Add(phoneCacheTTL)
	p.mu.Lock()
	for _, e := range toFetch {
		if failed[e] {
			continue
		}
		p.cache[e] = phoneCacheEntry{hasPhone: hasPhone[e], expires: expires}
	}
	// Drop what has expired, so the cache holds at most the people seen in
	// the last TTL.
	for e, entry := range p.cache {
		if !now.Before(entry.expires) {
			delete(p.cache, e)
		}
	}
	p.mu.Unlock()

	return hasPhone, failed
}
