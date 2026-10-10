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
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/middleware"
)

const (
	// defaultReadinessDays is how far ahead the Case Paging readiness strip
	// looks when the caller does not say.
	defaultReadinessDays = 7
	// maxReadinessDays bounds the window, so one read cannot ask entity-service
	// for an arbitrarily long horizon.
	maxReadinessDays = 31

	gapCodeNoPhone                = "NO_PHONE"
	gapCodePhoneUntested          = "PHONE_UNTESTED"
	gapCodePhoneTestFailed        = "PHONE_TEST_FAILED"
	gapCodePhoneCheckUnavailable  = "PHONE_CHECK_UNAVAILABLE"
	gapSeverityError              = "error"
	gapSeverityWarning            = "warning"
	gapFixProfile                 = "profile"
	gapFixData                    = "data"
	errMsgPagingReadinessFallback = "Failed to check the paging chains."

	// A paging number's last test call, as entity-service reports it.
	pagingTestCompleted = "completed"
	pagingTestPending   = "pending"
)

// entityPagingReadinessClient is the one entity-service read the readiness
// handler makes.
type entityPagingReadinessClient interface {
	GetPagingReadiness(ctx context.Context, days int) ([]byte, error)
}

// PagingReadiness is GET /team-schedule/paging-readiness's response: per Case
// Paging chain, whether it has someone to page on every day of the window,
// and what is missing where it does not. entity-service answers the rota,
// responder and paging-number questions; the portal adds the profile phone
// check, which only SCIM can answer, and drops the people list it needed for it.
type PagingReadiness struct {
	GeneratedAt string                 `json:"generatedAt"`
	From        string                 `json:"from"`
	To          string                 `json:"to"`
	Chains      []PagingReadinessChain `json:"chains"`
}

// PagingReadinessChain is one chain's readiness.
type PagingReadinessChain struct {
	Chain string               `json:"chain"`
	Label string               `json:"label"`
	Ready bool                 `json:"ready"`
	Gaps  []PagingReadinessGap `json:"gaps"`
	// People is who the chain would page over the window. Read from
	// entity-service for the phone check and never sent to the browser.
	People []pagingReadinessPerson `json:"people,omitempty"`
}

// PagingReadinessGap is one thing standing between a chain and being ready.
type PagingReadinessGap struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Fix      string `json:"fix"`
	TeamKey  string `json:"teamKey,omitempty"`
	Date     string `json:"date,omitempty"`
	ZoneCode string `json:"zoneCode,omitempty"`
	Tier     string `json:"tier,omitempty"`
	// ShiftCode is the rota window the gap is in, where it is in one.
	ShiftCode string `json:"shiftCode,omitempty"`
	// UserID is the person a phone gap is about, so the page can act on it.
	UserID string `json:"userId,omitempty"`
	// PagingTier is, on a phone gap, the Case Paging tier at which the chain
	// first calls the person (1 = first), so the page can list the numbers
	// in the order they would be needed.
	PagingTier int `json:"pagingTier,omitempty"`
}

type pagingReadinessPerson struct {
	UserID                    string `json:"userId"`
	Email                     string `json:"email"`
	Name                      string `json:"name"`
	Role                      string `json:"role"`
	HasPagingPhone            bool   `json:"hasPagingPhone"`
	PagingPhoneLastTestStatus string `json:"pagingPhoneLastTestStatus,omitempty"`
	PagingTier                int    `json:"pagingTier,omitempty"`
	PhoneOptional             bool   `json:"phoneOptional,omitempty"`
	// HasProfilePhone is entity-service's answer from "user".phone; absent
	// from an entity-service that predates it, which leaves the answer to SCIM.
	HasProfilePhone *bool `json:"hasProfilePhone,omitempty"`
}

// PagingReadinessHandler serves the Case Paging tab's readiness strip.
type PagingReadinessHandler struct {
	entity entityPagingReadinessClient
	phones *PagingPhoneChecker
}

// NewPagingReadinessHandler creates a PagingReadinessHandler.
func NewPagingReadinessHandler(entity entityPagingReadinessClient, phones *PagingPhoneChecker) *PagingReadinessHandler {
	return &PagingReadinessHandler{entity: entity, phones: phones}
}

// GetPagingReadiness handles GET /team-schedule/paging-readiness?days=N.
func (h *PagingReadinessHandler) GetPagingReadiness(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserInfoFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, ErrMsgUnauthorized)
		return
	}

	days := defaultReadinessDays
	if raw := r.URL.Query().Get("days"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > maxReadinessDays {
			writeError(w, http.StatusBadRequest, ErrMsgBadRequest)
			return
		}
		days = n
	}

	raw, err := h.entity.GetPagingReadiness(r.Context(), days)
	if err != nil {
		slog.ErrorContext(r.Context(), "entity GetPagingReadiness failed", "err", err)
		mapUpstreamErrorGeneric(w, err, errMsgPagingReadinessFallback)
		return
	}

	var readiness PagingReadiness
	if err := json.Unmarshal(raw, &readiness); err != nil {
		slog.ErrorContext(r.Context(), "decode paging readiness failed", "err", err)
		writeError(w, http.StatusBadGateway, errMsgPagingReadinessFallback)
		return
	}

	h.addPhoneGaps(r.Context(), &readiness)
	writeJSONValue(w, http.StatusOK, readiness)
}

// phoneVerdict is what one person's numbers mean for paging them.
type phoneVerdict int

const (
	phoneOK phoneVerdict = iota
	phoneUnchecked
	phoneUntested
	phoneTestFailed
	phoneNone
)

// verdictFor decides one person: their profile number first, a paging number
// only as the fallback -- and a paging number counts once a test call to it
// has completed. A profile check that failed is decided by the paging number
// where that settles it, and is otherwise left unchecked.
func verdictFor(p pagingReadinessPerson, hasProfile, profileFailed bool) phoneVerdict {
	if !profileFailed && hasProfile {
		return phoneOK
	}
	if p.HasPagingPhone && p.PagingPhoneLastTestStatus == pagingTestCompleted {
		return phoneOK
	}
	if profileFailed {
		return phoneUnchecked
	}
	if !p.HasPagingPhone {
		return phoneNone
	}
	if p.PagingPhoneLastTestStatus == "" || p.PagingPhoneLastTestStatus == pagingTestPending {
		return phoneUntested
	}
	return phoneTestFailed
}

// addPhoneGaps adds a phone gap to every chain for each person on it who
// cannot be called -- NO_PHONE, PHONE_TEST_FAILED (errors, which make the
// chain not ready) or PHONE_UNTESTED (a warning) -- and removes the people
// lists. A person whose number is optional (the 2nd and 3rd responders,
// called together with the 1st) gets a warning instead of an error. A chain's
// phone gaps are listed by the tier that first calls the person, errors
// before warnings, so the most urgent numbers come first. A person whose profile could not be checked, and whose paging number
// does not settle it, is skipped, and each chain they are on carries one
// PHONE_CHECK_UNAVAILABLE warning instead.
func (h *PagingReadinessHandler) addPhoneGaps(ctx context.Context, readiness *PagingReadiness) {
	entityAnswer := make(map[string]bool)
	for _, c := range readiness.Chains {
		for _, p := range c.People {
			if e := normalizeEmail(p.Email); e != "" && p.HasProfilePhone != nil {
				entityAnswer[e] = entityAnswer[e] || *p.HasProfilePhone
			}
		}
	}
	hasPhone, failed := h.phones.profilePhones(ctx, distinctEmails(readiness.Chains), entityAnswer)
	if len(failed) > 0 {
		slog.WarnContext(ctx, "paging readiness: phone check unavailable for some people", "count", len(failed))
	}

	for i := range readiness.Chains {
		c := &readiness.Chains[i]
		if c.Gaps == nil {
			c.Gaps = []PagingReadinessGap{}
		}
		unchecked := 0
		var phoneGaps []PagingReadinessGap
		for _, p := range peopleByEmail(c.People) {
			key := normalizeEmail(p.Email)
			gap := PagingReadinessGap{Fix: gapFixProfile, UserID: p.UserID, PagingTier: p.PagingTier}
			missing := gapSeverityError
			if p.PhoneOptional {
				missing = gapSeverityWarning
			}
			switch verdictFor(p, hasPhone[key], failed[key]) {
			case phoneOK:
				continue
			case phoneUnchecked:
				unchecked++
				continue
			case phoneNone:
				gap.Code, gap.Severity = gapCodeNoPhone, missing
				gap.Message = fmt.Sprintf("%s%s has no mobile number on their profile and no paging number%s",
					tierPrefix(p), personLabel(p), optionalSuffix(p))
			case phoneUntested:
				gap.Code, gap.Severity = gapCodePhoneUntested, gapSeverityWarning
				gap.Message = fmt.Sprintf("%s%s has a paging number that has not been tested", tierPrefix(p), personLabel(p))
			case phoneTestFailed:
				gap.Code, gap.Severity = gapCodePhoneTestFailed, missing
				gap.Message = fmt.Sprintf("%s%s has a paging number whose test call failed (%s)%s",
					tierPrefix(p), personLabel(p), p.PagingPhoneLastTestStatus, optionalSuffix(p))
			}
			phoneGaps = append(phoneGaps, gap)
			if gap.Severity == gapSeverityError {
				c.Ready = false
			}
		}
		sortPhoneGaps(phoneGaps)
		c.Gaps = append(c.Gaps, phoneGaps...)
		if unchecked > 0 {
			c.Gaps = append(c.Gaps, PagingReadinessGap{
				Code:     gapCodePhoneCheckUnavailable,
				Severity: gapSeverityWarning,
				Fix:      gapFixData,
				Message:  phoneUncheckedMessage(unchecked),
			})
		}
		c.People = nil
	}
}

// distinctEmails is every person's email across the chains, normalised and
// each once, in first-seen order.
func distinctEmails(chains []PagingReadinessChain) []string {
	seen := make(map[string]bool)
	var out []string
	for _, c := range chains {
		for _, p := range c.People {
			e := normalizeEmail(p.Email)
			if e == "" || seen[e] {
				continue
			}
			seen[e] = true
			out = append(out, e)
		}
	}
	return out
}

// peopleByEmail is a chain's people with one entry per email, in first-seen
// order. Somebody on a chain twice -- a responder who is also a lead -- is
// one gap, with both roles named, the earliest tier that calls them, and a
// number that is optional only if it is optional for every position.
func peopleByEmail(people []pagingReadinessPerson) []pagingReadinessPerson {
	index := make(map[string]int)
	var out []pagingReadinessPerson
	for _, p := range people {
		e := normalizeEmail(p.Email)
		if e == "" {
			continue
		}
		if i, ok := index[e]; ok {
			if p.PagingTier > 0 && (out[i].PagingTier == 0 || p.PagingTier < out[i].PagingTier) {
				out[i].PagingTier = p.PagingTier
			}
			out[i].PhoneOptional = out[i].PhoneOptional && p.PhoneOptional
			if p.Role != "" && !containsRole(out[i].Role, p.Role) {
				if out[i].Role == "" {
					out[i].Role = p.Role
				} else {
					out[i].Role += ", " + p.Role
				}
			}
			continue
		}
		index[e] = len(out)
		out = append(out, p)
	}
	return out
}

func containsRole(list, role string) bool {
	for _, r := range strings.Split(list, ", ") {
		if r == role {
			return true
		}
	}
	return false
}

// personLabel is "Name (role)", the name falling back to the email.
func personLabel(p pagingReadinessPerson) string {
	name := strings.TrimSpace(p.Name)
	if name == "" {
		name = strings.TrimSpace(p.Email)
	}
	if p.Role == "" {
		return name
	}
	return fmt.Sprintf("%s (%s)", name, p.Role)
}

func phoneUncheckedMessage(n int) string {
	if n == 1 {
		return "Could not check 1 person's mobile number; try again shortly"
	}
	return fmt.Sprintf("Could not check %d people's mobile numbers; try again shortly", n)
}

// tierPrefix is "Tier N · " for a person whose paging tier is known.
func tierPrefix(p pagingReadinessPerson) string {
	if p.PagingTier <= 0 {
		return ""
	}
	return fmt.Sprintf("Tier %d · ", p.PagingTier)
}

// optionalSuffix says why a missing number is only a warning.
func optionalSuffix(p pagingReadinessPerson) string {
	if !p.PhoneOptional {
		return ""
	}
	return " (optional: the 1st responder is called with them)"
}

// sortPhoneGaps orders phone gaps errors first, then by the tier that first
// calls the person (unknown tiers last), keeping the people order otherwise.
func sortPhoneGaps(gaps []PagingReadinessGap) {
	rank := func(g PagingReadinessGap) (int, int) {
		severity := 1
		if g.Severity == gapSeverityError {
			severity = 0
		}
		tier := g.PagingTier
		if tier <= 0 {
			tier = math.MaxInt
		}
		return severity, tier
	}
	sort.SliceStable(gaps, func(i, j int) bool {
		si, ti := rank(gaps[i])
		sj, tj := rank(gaps[j])
		if si != sj {
			return si < sj
		}
		return ti < tj
	})
}
