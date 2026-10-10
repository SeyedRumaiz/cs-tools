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
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/apierror"
	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/scim"
)

type fakeReadinessEntity struct {
	body    string
	err     error
	gotDays int
}

func (f *fakeReadinessEntity) GetPagingReadiness(_ context.Context, days int) ([]byte, error) {
	f.gotDays = days
	if f.err != nil {
		return nil, f.err
	}
	return []byte(f.body), nil
}

// fakeSCIM answers SearchUser from a table: a number, "" for none, an error,
// or absent for a person SCIM does not know. It counts calls and the highest
// number of lookups in flight at once.
type fakeSCIM struct {
	phones map[string]string
	fail   map[string]bool
	delay  time.Duration

	calls    atomic.Int32
	inFlight atomic.Int32
	maxSeen  atomic.Int32
	mu       sync.Mutex
	asked    []string
}

func (f *fakeSCIM) SearchUser(_ context.Context, email string) (*scim.UserInfo, error) {
	f.calls.Add(1)
	n := f.inFlight.Add(1)
	defer f.inFlight.Add(-1)
	for {
		m := f.maxSeen.Load()
		if n <= m || f.maxSeen.CompareAndSwap(m, n) {
			break
		}
	}
	f.mu.Lock()
	f.asked = append(f.asked, email)
	f.mu.Unlock()
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	if f.fail[email] {
		return nil, errors.New("scim: 503")
	}
	phone, ok := f.phones[email]
	if !ok {
		return nil, nil
	}
	if phone == "" {
		return &scim.UserInfo{}, nil
	}
	return &scim.UserInfo{PhoneNumber: &phone}, nil
}

const readinessBody = `{
  "generatedAt": "2026-10-08T03:00:00Z", "from": "2026-10-08", "to": "2026-10-14",
  "chains": [
    {"chain": "CRE", "label": "CRE", "ready": true, "gaps": [],
     "people": [
       {"userId": "u-ann", "email": "ann@example.com", "name": "Ann Lee", "role": "1st responder"},
       {"userId": "u-bob", "email": "Bob@Example.com", "name": "Bob Ray", "role": "Team lead"},
       {"userId": "u-bob", "email": "bob@example.com", "name": "Bob Ray", "role": "CRE head"}
     ]},
    {"chain": "SRE_SAAS", "label": "SaaS SRE", "ready": false,
     "gaps": [{"code": "NO_L1", "severity": "error", "message": "No L1 in TZ2 on 2026-10-10", "fix": "rota", "date": "2026-10-10", "zoneCode": "TZ2", "tier": "L1"}],
     "people": [
       {"userId": "u-bob", "email": "bob@example.com", "name": "Bob Ray", "role": "L2"},
       {"userId": "u-cat", "email": "cat@example.com", "name": "Cat Kim", "role": "L1"}
     ]},
    {"chain": "SME", "label": "SME", "ready": true, "gaps": [], "people": []}
  ]
}`

func getReadiness(t *testing.T, h *PagingReadinessHandler, query string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	h.GetPagingReadiness(w, withUser(httptest.NewRequest(http.MethodGet, "/team-schedule/paging-readiness"+query, nil)))
	return w
}

func chainOf(t *testing.T, r PagingReadiness, code string) PagingReadinessChain {
	t.Helper()
	for _, c := range r.Chains {
		if c.Chain == code {
			return c
		}
	}
	t.Fatalf("chain %s missing from %+v", code, r.Chains)
	return PagingReadinessChain{}
}

func gapCodes(c PagingReadinessChain) []string {
	out := make([]string, 0, len(c.Gaps))
	for _, g := range c.Gaps {
		out = append(out, g.Code)
	}
	return out
}

func TestPagingReadiness_RequiresAUser(t *testing.T) {
	h := NewPagingReadinessHandler(&fakeReadinessEntity{body: readinessBody}, NewPagingPhoneChecker(&fakeSCIM{}))
	w := httptest.NewRecorder()
	h.GetPagingReadiness(w, httptest.NewRequest(http.MethodGet, "/team-schedule/paging-readiness", nil))
	assertStatus(t, w, http.StatusUnauthorized)
}

func TestPagingReadiness_Days(t *testing.T) {
	for _, c := range []struct {
		query    string
		wantCode int
		wantDays int
	}{
		{"", http.StatusOK, 7},
		{"?days=14", http.StatusOK, 14},
		{"?days=0", http.StatusBadRequest, 0},
		{"?days=32", http.StatusBadRequest, 0},
		{"?days=seven", http.StatusBadRequest, 0},
	} {
		t.Run(c.query, func(t *testing.T) {
			ent := &fakeReadinessEntity{body: `{"chains":[]}`}
			w := getReadiness(t, NewPagingReadinessHandler(ent, NewPagingPhoneChecker(&fakeSCIM{})), c.query)
			assertStatus(t, w, c.wantCode)
			if ent.gotDays != c.wantDays {
				t.Fatalf("days forwarded = %d, want %d", ent.gotDays, c.wantDays)
			}
		})
	}
}

func TestPagingReadiness_UpstreamFailureIsGeneric(t *testing.T) {
	ent := &fakeReadinessEntity{err: &apierror.Error{StatusCode: http.StatusInternalServerError, Body: `{"message":"pq: boom"}`}}
	w := getReadiness(t, NewPagingReadinessHandler(ent, NewPagingPhoneChecker(&fakeSCIM{})), "")
	assertStatus(t, w, http.StatusInternalServerError)
	if strings.Contains(w.Body.String(), "pq") {
		t.Fatalf("upstream detail leaked: %s", w.Body.String())
	}
}

// Everyone with a number: nothing added, readiness untouched, people dropped.
func TestPagingReadiness_EveryoneHasAPhone(t *testing.T) {
	sc := &fakeSCIM{phones: map[string]string{
		"ann@example.com": "+94 77 000 0001", "bob@example.com": "+94 77 000 0002", "cat@example.com": "+94 77 000 0003",
	}}
	w := getReadiness(t, NewPagingReadinessHandler(&fakeReadinessEntity{body: readinessBody}, NewPagingPhoneChecker(sc)), "")
	assertStatus(t, w, http.StatusOK)
	if strings.Contains(w.Body.String(), "people") || strings.Contains(w.Body.String(), "example.com") {
		t.Fatalf("people reached the browser: %s", w.Body.String())
	}
	got := decodeJSON[PagingReadiness](t, w)
	if c := chainOf(t, got, "CRE"); !c.Ready || len(c.Gaps) != 0 {
		t.Fatalf("CRE = %+v, want ready with no gaps", c)
	}
	if c := chainOf(t, got, "SRE_SAAS"); c.Ready || fmt.Sprint(gapCodes(c)) != "[NO_L1]" {
		t.Fatalf("SRE_SAAS = %+v, want its own gap kept and not ready", c)
	}
	if c := chainOf(t, got, "SME"); !c.Ready || c.Gaps == nil {
		t.Fatalf("SME = %+v, want ready with an empty (not null) gaps list", c)
	}
	// Bob is on two chains under two spellings: one lookup.
	if n := sc.calls.Load(); n != 3 {
		t.Fatalf("SCIM lookups = %d, want 3 (one per distinct person)", n)
	}
}

// A person with no number is an error on every chain they are on, once per
// chain, naming them and their roles; a person SCIM does not know counts too.
func TestPagingReadiness_NoPhoneIsAGapOnEveryChain(t *testing.T) {
	sc := &fakeSCIM{phones: map[string]string{"ann@example.com": "+94 77 000 0001", "bob@example.com": ""}}
	got := decodeJSON[PagingReadiness](t, getReadiness(t, NewPagingReadinessHandler(&fakeReadinessEntity{body: readinessBody}, NewPagingPhoneChecker(sc)), ""))

	cre := chainOf(t, got, "CRE")
	if cre.Ready || fmt.Sprint(gapCodes(cre)) != "[NO_PHONE]" {
		t.Fatalf("CRE = %+v, want one NO_PHONE and not ready", cre)
	}
	g := cre.Gaps[0]
	if g.Severity != "error" || g.Fix != "profile" ||
		g.Message != "Bob Ray (Team lead, CRE head) has no mobile number on their profile and no paging number" || g.UserID != "u-bob" {
		t.Fatalf("CRE gap = %+v", g)
	}

	saas := chainOf(t, got, "SRE_SAAS")
	if fmt.Sprint(gapCodes(saas)) != "[NO_L1 NO_PHONE NO_PHONE]" {
		t.Fatalf("SRE_SAAS gaps = %v, want its own then Bob and Cat", gapCodes(saas))
	}
	if saas.Gaps[2].Message != "Cat Kim (L1) has no mobile number on their profile and no paging number" {
		t.Fatalf("unknown-to-SCIM gap = %+v", saas.Gaps[2])
	}
}

// One person's lookup failing does not fail the read: their check is skipped
// and each chain they are on carries one warning, without changing readiness.
func TestPagingReadiness_ALookupFailureIsAWarning(t *testing.T) {
	sc := &fakeSCIM{
		phones: map[string]string{"ann@example.com": "+94 77 000 0001", "cat@example.com": "+94 77 000 0003"},
		fail:   map[string]bool{"bob@example.com": true},
	}
	w := getReadiness(t, NewPagingReadinessHandler(&fakeReadinessEntity{body: readinessBody}, NewPagingPhoneChecker(sc)), "")
	assertStatus(t, w, http.StatusOK)
	got := decodeJSON[PagingReadiness](t, w)

	cre := chainOf(t, got, "CRE")
	if !cre.Ready || fmt.Sprint(gapCodes(cre)) != "[PHONE_CHECK_UNAVAILABLE]" || cre.Gaps[0].Severity != "warning" {
		t.Fatalf("CRE = %+v, want still ready with one warning", cre)
	}
	if c := chainOf(t, got, "SRE_SAAS"); fmt.Sprint(gapCodes(c)) != "[NO_L1 PHONE_CHECK_UNAVAILABLE]" {
		t.Fatalf("SRE_SAAS gaps = %v", gapCodes(c))
	}
	if c := chainOf(t, got, "SME"); len(c.Gaps) != 0 {
		t.Fatalf("SME = %+v, want untouched", c)
	}
}

// Answers are cached for ten minutes; a failure is not cached, so the next
// read asks again.
func TestPagingReadiness_CachesAnswersButNotFailures(t *testing.T) {
	sc := &fakeSCIM{
		phones: map[string]string{"ann@example.com": "+1", "cat@example.com": "+3"},
		fail:   map[string]bool{"bob@example.com": true},
	}
	phones := NewPagingPhoneChecker(sc)
	h := NewPagingReadinessHandler(&fakeReadinessEntity{body: readinessBody}, phones)
	clock := time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)
	phones.now = func() time.Time { return clock }

	getReadiness(t, h, "")
	if n := sc.calls.Load(); n != 3 {
		t.Fatalf("first read: %d lookups, want 3", n)
	}
	getReadiness(t, h, "")
	if n := sc.calls.Load(); n != 4 {
		t.Fatalf("second read: %d lookups in all, want 4 (only the failed one again)", n)
	}
	clock = clock.Add(phoneCacheTTL + time.Second)
	getReadiness(t, h, "")
	if n := sc.calls.Load(); n != 7 {
		t.Fatalf("after expiry: %d lookups in all, want 7", n)
	}
}

// No more than eight lookups run at once, however many people there are.
func TestPagingReadiness_BoundsConcurrency(t *testing.T) {
	var people []string
	phones := map[string]string{}
	for i := 0; i < 30; i++ {
		e := fmt.Sprintf("p%02d@example.com", i)
		phones[e] = "+1"
		people = append(people, fmt.Sprintf(`{"email":%q,"name":"P %d","role":"L1"}`, e, i))
	}
	body := `{"chains":[{"chain":"SRE_IAAS","label":"IaaS SRE","ready":true,"gaps":[],"people":[` + strings.Join(people, ",") + `]}]}`
	sc := &fakeSCIM{phones: phones, delay: 5 * time.Millisecond}

	got := decodeJSON[PagingReadiness](t, getReadiness(t, NewPagingReadinessHandler(&fakeReadinessEntity{body: body}, NewPagingPhoneChecker(sc)), ""))
	if n := sc.calls.Load(); n != 30 {
		t.Fatalf("lookups = %d, want 30", n)
	}
	if m := sc.maxSeen.Load(); m > phoneLookupConcurrency {
		t.Fatalf("%d lookups ran at once, want at most %d", m, phoneLookupConcurrency)
	}
	if c := chainOf(t, got, "SRE_IAAS"); !c.Ready || len(c.Gaps) != 0 {
		t.Fatalf("SRE_IAAS = %+v", c)
	}
}

// The paging number is the fallback: a profile number wins; a paging number
// counts once its test call completed; untested is a warning, a failed test
// and no number at all are errors. Each gap names the person by userId.
func TestPagingReadiness_PagingNumberRules(t *testing.T) {
	person := func(id, email string, has bool, status string) string {
		st := ""
		if status != "" {
			st = fmt.Sprintf(`,"pagingPhoneLastTestStatus":%q`, status)
		}
		return fmt.Sprintf(`{"userId":%q,"email":%q,"name":%q,"role":"L1","hasPagingPhone":%t%s}`, id, email, id, has, st)
	}
	body := `{"chains":[{"chain":"SRE_IAAS","label":"IaaS SRE","ready":true,"gaps":[],"people":[` + strings.Join([]string{
		person("profile", "profile@example.com", true, "failed"),
		person("tested", "tested@example.com", true, "completed"),
		person("never", "never@example.com", true, ""),
		person("pending", "pending@example.com", true, "pending"),
		person("noanswer", "noanswer@example.com", true, "no-answer"),
		person("busy", "busy@example.com", true, "busy"),
		person("none", "none@example.com", false, ""),
	}, ",") + `]}]}`
	sc := &fakeSCIM{phones: map[string]string{"profile@example.com": "+1"}}
	got := decodeJSON[PagingReadiness](t, getReadiness(t, NewPagingReadinessHandler(&fakeReadinessEntity{body: body}, NewPagingPhoneChecker(sc)), ""))

	c := chainOf(t, got, "SRE_IAAS")
	if c.Ready {
		t.Fatal("a failed test and no number are errors: the chain is not ready")
	}
	want := map[string][2]string{
		"never":    {"PHONE_UNTESTED", "warning"},
		"pending":  {"PHONE_UNTESTED", "warning"},
		"noanswer": {"PHONE_TEST_FAILED", "error"},
		"busy":     {"PHONE_TEST_FAILED", "error"},
		"none":     {"NO_PHONE", "error"},
	}
	if len(c.Gaps) != len(want) {
		t.Fatalf("gaps = %+v, want one each for %v", c.Gaps, want)
	}
	for _, g := range c.Gaps {
		w, ok := want[g.UserID]
		if !ok || g.Code != w[0] || g.Severity != w[1] || g.Fix != "profile" {
			t.Fatalf("gap %+v, want %v for %q", g, w, g.UserID)
		}
	}
	if !strings.Contains(fmt.Sprint(c.Gaps), "test call failed (no-answer)") {
		t.Fatalf("a failed test should name its status: %+v", c.Gaps)
	}
}

// Untested numbers alone are warnings: the chain stays ready.
func TestPagingReadiness_UntestedIsOnlyAWarning(t *testing.T) {
	body := `{"chains":[{"chain":"SME","label":"SME","ready":true,"gaps":[],"people":[{"userId":"u1","email":"a@example.com","name":"A","role":"L1","hasPagingPhone":true}]}]}`
	got := decodeJSON[PagingReadiness](t, getReadiness(t, NewPagingReadinessHandler(&fakeReadinessEntity{body: body}, NewPagingPhoneChecker(&fakeSCIM{})), ""))
	c := chainOf(t, got, "SME")
	if !c.Ready || fmt.Sprint(gapCodes(c)) != "[PHONE_UNTESTED]" {
		t.Fatalf("SME = %+v, want ready with one PHONE_UNTESTED warning", c)
	}
}

// A failed profile check is settled by a tested paging number; without one
// the person is unchecked, as before.
func TestPagingReadiness_TestedPagingNumberCoversAFailedProfileCheck(t *testing.T) {
	body := `{"chains":[{"chain":"CRE","label":"CRE","ready":true,"gaps":[],"people":[
	  {"userId":"u1","email":"a@example.com","name":"A","role":"L1","hasPagingPhone":true,"pagingPhoneLastTestStatus":"completed"},
	  {"userId":"u2","email":"b@example.com","name":"B","role":"L1","hasPagingPhone":false}]}]}`
	sc := &fakeSCIM{fail: map[string]bool{"a@example.com": true, "b@example.com": true}}
	got := decodeJSON[PagingReadiness](t, getReadiness(t, NewPagingReadinessHandler(&fakeReadinessEntity{body: body}, NewPagingPhoneChecker(sc)), ""))
	c := chainOf(t, got, "CRE")
	if !c.Ready || fmt.Sprint(gapCodes(c)) != "[PHONE_CHECK_UNAVAILABLE]" || c.Gaps[0].Message != "Could not check 1 person's mobile number; try again shortly" {
		t.Fatalf("CRE = %+v, want ready with one warning for B only", c)
	}
}

// Phone gaps are ordered by the tier that first calls the person, errors
// first, and only the 2nd / 3rd responders' numbers are optional (a warning
// that leaves the chain ready).
func TestPagingReadiness_PhoneGapsFollowThePagingTiers(t *testing.T) {
	body := `{"generatedAt":"x","from":"2026-10-08","to":"2026-10-14","chains":[{"chain":"CRE","label":"CRE chain","ready":true,"gaps":[],"people":[
	 {"userId":"u-head","email":"head@example.com","name":"Hana Head","role":"CS head","pagingTier":5},
	 {"userId":"u-t2","email":"t2@example.com","name":"Tia Two","role":"T2 responder · Vega","pagingTier":1,"phoneOptional":true},
	 {"userId":"u-lead","email":"lead@example.com","name":"Leo Lead","role":"Team lead · Vega","pagingTier":2},
	 {"userId":"u-t1","email":"t1@example.com","name":"Una One","role":"T1 responder · Vega","pagingTier":1}]}]}`
	got := decodeJSON[PagingReadiness](t, getReadiness(t, NewPagingReadinessHandler(&fakeReadinessEntity{body: body}, NewPagingPhoneChecker(&fakeSCIM{})), ""))
	cre := chainOf(t, got, "CRE")

	var order []string
	for _, g := range cre.Gaps {
		order = append(order, fmt.Sprintf("%s/%s/%d", g.UserID, g.Severity, g.PagingTier))
	}
	if want := "[u-t1/error/1 u-lead/error/2 u-head/error/5 u-t2/warning/1]"; fmt.Sprint(order) != want {
		t.Fatalf("order = %v, want %s", order, want)
	}
	if cre.Ready {
		t.Error("ready with a 1st responder, a Team lead and a head missing numbers")
	}
	if m := cre.Gaps[0].Message; m != "Tier 1 · Una One (T1 responder · Vega) has no mobile number on their profile and no paging number" {
		t.Errorf("1st responder message %q", m)
	}
	if m := cre.Gaps[3].Message; !strings.HasPrefix(m, "Tier 1 · Tia Two") || !strings.HasSuffix(m, "(optional: the 1st responder is called with them)") {
		t.Errorf("optional responder message %q", m)
	}

	// Only optional numbers missing: warnings, and the chain stays ready.
	only := `{"generatedAt":"x","from":"2026-10-08","to":"2026-10-14","chains":[{"chain":"CRE","label":"CRE chain","ready":true,"gaps":[],"people":[
	 {"userId":"u-t3","email":"t3@example.com","name":"Ted Three","role":"T3 responder · Vega","pagingTier":1,"phoneOptional":true}]}]}`
	got = decodeJSON[PagingReadiness](t, getReadiness(t, NewPagingReadinessHandler(&fakeReadinessEntity{body: only}, NewPagingPhoneChecker(&fakeSCIM{})), ""))
	if c := chainOf(t, got, "CRE"); !c.Ready || c.Gaps[0].Severity != "warning" {
		t.Errorf("optional-only chain = %+v, want ready with a warning", c)
	}

	// The same person in two positions: the earliest tier wins, and the number
	// is required because one of the positions requires it.
	twice := `{"generatedAt":"x","from":"2026-10-08","to":"2026-10-14","chains":[{"chain":"CRE","label":"CRE chain","ready":true,"gaps":[],"people":[
	 {"userId":"u-x","email":"x@example.com","name":"Xi","role":"CRE head","pagingTier":4},
	 {"userId":"u-x","email":"x@example.com","name":"Xi","role":"T2 responder · Vega","pagingTier":1,"phoneOptional":true}]}]}`
	got = decodeJSON[PagingReadiness](t, getReadiness(t, NewPagingReadinessHandler(&fakeReadinessEntity{body: twice}, NewPagingPhoneChecker(&fakeSCIM{})), ""))
	if g := chainOf(t, got, "CRE").Gaps[0]; g.Severity != "error" || g.PagingTier != 1 {
		t.Errorf("merged person gap = %+v, want error at tier 1", g)
	}
}

// entity-service answers first, from "user".phone: someone it says has a
// profile number is never looked up in SCIM. Someone it says has none is
// still looked up while the SCIM fallback is on, and is not once it is off --
// so a directory outage no longer leaves anyone unchecked.
func TestPagingReadiness_ProfileNumberFromEntityService(t *testing.T) {
	body := `{"chains":[{"chain":"CRE","label":"CRE","ready":true,"gaps":[],"people":[
	  {"userId":"u1","email":"db@example.com","name":"Db","role":"L1","hasPagingPhone":false,"hasProfilePhone":true},
	  {"userId":"u2","email":"asg@example.com","name":"Asg","role":"L1","hasPagingPhone":false,"hasProfilePhone":false},
	  {"userId":"u3","email":"old@example.com","name":"Old","role":"L1","hasPagingPhone":false}]}]}`

	t.Run("fallback on: SCIM answers for the people entity-service has no number for", func(t *testing.T) {
		sc := &fakeSCIM{phones: map[string]string{"asg@example.com": "+94770000002", "old@example.com": "+94770000003"}}
		got := decodeJSON[PagingReadiness](t, getReadiness(t, NewPagingReadinessHandler(&fakeReadinessEntity{body: body}, NewPagingPhoneChecker(sc)), ""))
		if c := chainOf(t, got, "CRE"); !c.Ready || len(c.Gaps) != 0 {
			t.Fatalf("CRE = %+v, want ready with no gaps", c)
		}
		sc.mu.Lock()
		defer sc.mu.Unlock()
		if strings.Join(sorted(sc.asked), ",") != "asg@example.com,old@example.com" {
			t.Errorf("SCIM asked about %v; want only the two entity-service has no number for", sc.asked)
		}
	})

	t.Run("fallback off: entity-service's answer stands, an older one's silence still asks SCIM", func(t *testing.T) {
		sc := &fakeSCIM{fail: map[string]bool{"asg@example.com": true}, phones: map[string]string{"old@example.com": "+94770000003"}}
		phones := NewPagingPhoneChecker(sc).WithSCIMFallback(false)
		got := decodeJSON[PagingReadiness](t, getReadiness(t, NewPagingReadinessHandler(&fakeReadinessEntity{body: body}, phones), ""))
		c := chainOf(t, got, "CRE")
		if c.Ready || fmt.Sprint(gapCodes(c)) != "[NO_PHONE]" || c.Gaps[0].UserID != "u2" {
			t.Fatalf("CRE = %+v, want one NO_PHONE for Asg and no PHONE_CHECK_UNAVAILABLE", c)
		}
		sc.mu.Lock()
		defer sc.mu.Unlock()
		if strings.Join(sc.asked, ",") != "old@example.com" {
			t.Errorf("SCIM asked about %v; want only the person entity-service did not report on", sc.asked)
		}
	})
}

func sorted(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}
