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

package corewake

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestWake_PostsToAlertsCore(t *testing.T) {
	var calls atomic.Int64
	var method string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		calls.Add(1)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	c := New(discard(), srv.URL+"/alertz", "", time.Second)
	c.Wake()
	c.Wait(context.Background())
	if calls.Load() != 1 || method != http.MethodPost {
		t.Errorf("calls = %d, method = %s; want one POST", calls.Load(), method)
	}
}

func TestWake_OneInFlightAndCoalescesTheRest(t *testing.T) {
	var calls, inFlight, maxInFlight atomic.Int64
	gate := make(chan struct{})
	entered := make(chan struct{}, 10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := inFlight.Add(1)
		if n > maxInFlight.Load() {
			maxInFlight.Store(n)
		}
		calls.Add(1)
		entered <- struct{}{}
		<-gate
		inFlight.Add(-1)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	c := New(discard(), srv.URL, "", 5*time.Second)
	c.Wake()
	<-entered // first call is in flight
	for range 5 {
		c.Wake() // all coalesce into one follow-up
	}
	close(gate)
	c.Wait(context.Background())

	if got := calls.Load(); got != 2 {
		t.Errorf("calls = %d, want 2 (the in-flight one + one coalesced follow-up)", got)
	}
	if maxInFlight.Load() != 1 {
		t.Errorf("max in flight = %d, want 1", maxInFlight.Load())
	}
}

func TestWake_EmptyURLIsNoOp(t *testing.T) {
	c := New(discard(), "", "", time.Second)
	c.Wake()
	c.Wait(context.Background()) // must not hang
}

func TestWake_ErrorsAreOnlyLogged(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := New(discard(), srv.URL, "", time.Second)
	c.Wake()
	c.Wait(context.Background())

	unreachable := New(discard(), "http://127.0.0.1:1", "", 200*time.Millisecond)
	unreachable.Wake()
	unreachable.Wait(context.Background())
}

// authHeader records the Authorization header of the one wake call made to srv.
func authHeader(t *testing.T, tlsServer bool, token string) (string, bool) {
	t.Helper()
	var got string
	var seen bool
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, seen = r.Header.Get("Authorization"), r.Header.Get("Authorization") != ""
		w.WriteHeader(http.StatusAccepted)
	})
	srv := httptest.NewServer(h)
	if tlsServer {
		srv.Close()
		srv = httptest.NewTLSServer(h)
	}
	defer srv.Close()

	c := New(discard(), srv.URL, token, time.Second)
	c.http.Transport = srv.Client().Transport // trust the test cert, keep the redirect policy
	c.Wake()
	c.Wait(context.Background())
	return got, seen
}

func TestWake_SendsTokenOverHTTPS(t *testing.T) {
	got, _ := authHeader(t, true, "wake-token")
	if want := "Bearer wake-token"; got != want {
		t.Errorf("Authorization = %q, want %q", got, want)
	}
}

// Over plain http the secret would cross the network in cleartext, so it is withheld.
func TestWake_WithholdsCredentialOverHTTP(t *testing.T) {
	if _, seen := authHeader(t, false, "wake-token"); seen {
		t.Error("credential sent over plain http")
	}
}

func TestWake_NoCredentialSendsNoHeader(t *testing.T) {
	if _, seen := authHeader(t, true, ""); seen {
		t.Error("no credential configured, but an Authorization header was sent")
	}
}

// CodeRabbit: an https endpoint redirecting to http on the same host must not get the
// credential replayed in cleartext. Go would forward Authorization on that redirect.
func TestWake_DoesNotFollowRedirectsWithCredential(t *testing.T) {
	var leaked atomic.Bool
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked.Store(true)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer plain.Close()

	var logs strings.Builder
	secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL+"/alertz", http.StatusFound)
	}))
	defer secure.Close()

	c := New(slog.New(slog.NewTextHandler(&logs, nil)), secure.URL+"/alertz", "wake-token", time.Second)
	c.http.Transport = secure.Client().Transport
	c.Wake()
	c.Wait(context.Background())

	if leaked.Load() {
		t.Fatal("redirect was followed to plain http, so the credential could cross in cleartext")
	}
	if !strings.Contains(logs.String(), "status=302") {
		t.Errorf("the 3xx should be logged as an unexpected status, logs: %s", logs.String())
	}
}
