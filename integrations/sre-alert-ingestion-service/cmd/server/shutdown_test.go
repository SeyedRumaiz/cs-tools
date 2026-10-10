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

package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"sre-alert-ingestion-service/internal/transport/auth"
	"sre-alert-ingestion-service/internal/transport/server"
)

// blockingPipeline holds each request until release is closed.
type blockingPipeline struct {
	entered chan struct{}
	release chan struct{}
	events  *eventLog
}

func (p *blockingPipeline) Ingest(ctx context.Context, req server.Request) server.Result {
	p.entered <- struct{}{}
	<-p.release
	p.events.add("request done")
	return server.Result{Status: http.StatusCreated, AltIDs: []string{"ALT000000001"}}
}

type eventLog struct {
	mu     sync.Mutex
	events []string
}

func (e *eventLog) add(s string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, s)
}

func (e *eventLog) all() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.events...)
}

type fakeAlloc struct {
	events  *eventLog
	onClose func(ctx context.Context)
}

func (a fakeAlloc) Close(ctx context.Context) error {
	a.events.add("allocator closed")
	if a.onClose != nil {
		a.onClose(ctx)
	}
	return nil
}

// startInFlight serves srv and returns once one POST is blocked inside pipe.
func startInFlight(t *testing.T, pipe *blockingPipeline) (*server.Server, *http.Server, chan int) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := server.New(server.Options{
		Logger: logger, Auth: auth.None{}, Pipeline: pipe, Sources: []string{"aws"},
		MaxBodyBytes: 1 << 20, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second,
	})
	httpSrv := srv.HTTPServer("")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go httpSrv.Serve(ln)

	status := make(chan int, 1)
	go func() {
		resp, err := http.Post("http://"+ln.Addr().String()+server.SourceRoutePrefix+"aws", "application/json", strings.NewReader(`{}`))
		if err != nil {
			status <- 0
			return
		}
		resp.Body.Close()
		status <- resp.StatusCode
	}()
	<-pipe.entered
	return srv, httpSrv, status
}

func TestShutdown_DrainsInFlightRequestBeforeClosingAllocator(t *testing.T) {
	events := &eventLog{}
	pipe := &blockingPipeline{entered: make(chan struct{}, 1), release: make(chan struct{}), events: events}
	srv, httpSrv, status := startInFlight(t, pipe)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan struct{})
	b := budget{DrainDelay: 10 * time.Millisecond, RequestWait: 2 * time.Second, AllocatorDrain: time.Second}
	go func() {
		shutdown(ctx, logger, srv, httpSrv, fakeAlloc{events: events}, fakeRaw{events: events}, b, func(context.Context) { events.add("after") })
		close(done)
	}()

	// While the request is still in flight, /healthz must already say 503.
	deadline := time.Now().Add(2 * time.Second)
	for {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
		if rec.Code == http.StatusServiceUnavailable {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("/healthz = %d during drain, want 503", rec.Code)
		}
		time.Sleep(5 * time.Millisecond)
	}
	select {
	case <-done:
		t.Fatal("shutdown finished while a request was still in flight")
	case <-time.After(50 * time.Millisecond):
	}

	close(pipe.release)
	if got := <-status; got != http.StatusCreated {
		t.Errorf("in-flight request status = %d, want 201", got)
	}
	<-done
	want := []string{"request done", "allocator closed", "payloads closed", "after"}
	if got := events.all(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("shutdown order = %v, want %v", got, want)
	}
}

func TestShutdown_StuckRequestKeepsAllocatorBudget(t *testing.T) {
	events := &eventLog{}
	pipe := &blockingPipeline{entered: make(chan struct{}, 1), release: make(chan struct{}), events: events}
	srv, httpSrv, _ := startInFlight(t, pipe)
	defer close(pipe.release)

	var left time.Duration
	var closeErr error
	alloc := fakeAlloc{events: events, onClose: func(ctx context.Context) {
		d, _ := ctx.Deadline()
		left, closeErr = time.Until(d), ctx.Err()
	}}
	b := budget{DrainDelay: 10 * time.Millisecond, RequestWait: 100 * time.Millisecond, AllocatorDrain: 300 * time.Millisecond}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	shutdown(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), srv, httpSrv, alloc, nil, b)

	if closeErr != nil || left < 250*time.Millisecond {
		t.Errorf("allocator got %v left (err %v), want its full %v", left, closeErr, b.AllocatorDrain)
	}
}

type fakeRaw struct {
	events  *eventLog
	onClose func(ctx context.Context)
}

func (f fakeRaw) Close(ctx context.Context) {
	if f.onClose != nil {
		f.onClose(ctx)
	}
	if f.events != nil {
		f.events.add("payloads closed")
	}
}

// TestShutdown_PayloadsGetTheirOwnBudget: even when the grace context has run out, the final raw body insert still gets its full payload_drain.
func TestShutdown_PayloadsGetTheirOwnBudget(t *testing.T) {
	events := &eventLog{}
	pipe := &blockingPipeline{entered: make(chan struct{}, 1), release: make(chan struct{}), events: events}
	srv, httpSrv, _ := startInFlight(t, pipe)
	defer close(pipe.release)

	var left time.Duration
	var rawErr error
	raw := fakeRaw{onClose: func(ctx context.Context) {
		d, _ := ctx.Deadline()
		left, rawErr = time.Until(d), ctx.Err()
	}}
	b := budget{DrainDelay: 10 * time.Millisecond, RequestWait: 100 * time.Millisecond, AllocatorDrain: 50 * time.Millisecond, PayloadDrain: 300 * time.Millisecond}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	shutdown(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), srv, httpSrv, fakeAlloc{events: events}, raw, b)

	if rawErr != nil || left < 250*time.Millisecond {
		t.Errorf("payloads got %v left (err %v), want their full %v", left, rawErr, b.PayloadDrain)
	}
}
