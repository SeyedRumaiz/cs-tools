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

package payloads

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

type fakeInserter struct {
	mu    sync.Mutex
	calls [][]string
	err   error
}

func (f *fakeInserter) InsertPayloads(_ context.Context, at []time.Time, payloads []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(at) != len(payloads) {
		panic("received_at and payloads differ in length")
	}
	f.calls = append(f.calls, payloads)
	return f.err
}

func (f *fakeInserter) snapshot() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]string(nil), f.calls...)
}

func newBuffer(ins Inserter, maxBytes int64) *Buffer {
	return New(slog.New(slog.NewTextHandler(io.Discard, nil)), ins, Config{
		FlushInterval: time.Hour, MaxBytes: maxBytes, FlushTimeout: time.Second,
	})
}

// TestFlush_OneInsertForAllBuffered: every buffered body goes out in a single insert, in arrival order.
func TestFlush_OneInsertForAllBuffered(t *testing.T) {
	ins := &fakeInserter{}
	b := newBuffer(ins, 1<<20)
	b.Add(time.Now(), []byte(`{"a":1}`))
	b.Add(time.Now(), []byte(`{"b":2}`))
	b.Add(time.Now(), []byte(`{"c":3}`))
	b.flush(context.Background())

	calls := ins.snapshot()
	if len(calls) != 1 || len(calls[0]) != 3 || calls[0][0] != `{"a":1}` || calls[0][2] != `{"c":3}` {
		t.Fatalf("inserts = %v, want one insert of 3 in order", calls)
	}
	b.flush(context.Background())
	if len(ins.snapshot()) != 1 {
		t.Error("an empty buffer must not insert")
	}
}

// TestFlush_KeepsRowsOnTransientFailure: a connection-type failure keeps the rows for the next flush.
func TestFlush_KeepsRowsOnTransientFailure(t *testing.T) {
	ins := &fakeInserter{err: errors.New("connection refused")}
	b := newBuffer(ins, 1<<20)
	b.Add(time.Now(), []byte(`{"a":1}`))
	b.flush(context.Background())

	ins.err = nil
	b.Add(time.Now(), []byte(`{"b":2}`))
	b.flush(context.Background())
	calls := ins.snapshot()
	if len(calls) != 2 || len(calls[1]) != 2 || calls[1][0] != `{"a":1}` || calls[1][1] != `{"b":2}` {
		t.Fatalf("inserts = %v, want the failed row retried first", calls)
	}
}

// TestFlush_DiscardsDataErrors: a batch the database rejects as bad data is not retried forever.
func TestFlush_DiscardsDataErrors(t *testing.T) {
	ins := &fakeInserter{err: &pgconn.PgError{Code: "22P02"}}
	b := newBuffer(ins, 1<<20)
	b.Add(time.Now(), []byte(`{"a":1}`))
	b.flush(context.Background())
	ins.err = nil
	b.flush(context.Background())
	if calls := ins.snapshot(); len(calls) != 1 {
		t.Fatalf("inserts = %d, want the rejected batch dropped", len(calls))
	}
}

// TestAdd_DropsPastMaxBytesAndFlushesEarly: half of MaxBytes triggers an early flush, and a body past MaxBytes is dropped.
func TestAdd_DropsPastMaxBytesAndFlushesEarly(t *testing.T) {
	ins := &fakeInserter{}
	b := newBuffer(ins, 20)
	b.Add(time.Now(), []byte(`{"a":"123456"}`)) // 14 bytes, past half
	select {
	case <-b.early:
	default:
		t.Error("passing half of max_bytes should request an early flush")
	}
	b.Add(time.Now(), []byte(`{"b":"123456"}`)) // would reach 28, dropped
	b.flush(context.Background())
	if calls := ins.snapshot(); len(calls) != 1 || len(calls[0]) != 1 {
		t.Fatalf("inserts = %v, want only the first body", calls)
	}
}

// TestClose_FlushesRemaining: shutdown writes what is still buffered.
func TestClose_FlushesRemaining(t *testing.T) {
	ins := &fakeInserter{}
	b := newBuffer(ins, 1<<20)
	go b.Run()
	b.Add(time.Now(), []byte(`{"a":1}`))
	b.Close(context.Background())
	if calls := ins.snapshot(); len(calls) != 1 || len(calls[0]) != 1 {
		t.Fatalf("inserts = %v, want the buffered body written on Close", calls)
	}
}

// TestAsJSON: JSON is kept as sent, anything else becomes a JSON string jsonb accepts.
func TestAsJSON(t *testing.T) {
	cases := map[string]struct{ in, want string }{
		"object":       {`{"rule_id":"a8f2"}`, `{"rule_id":"a8f2"}`},
		"plain text":   {"not json", `"not json"`},
		"nul escape":   {`{"a":"x\u0000y"}`, `"{\"a\":\"x\\u0000y\"}"`},
		"raw nul byte": {"a\x00b", "\"a�b\""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := asJSON([]byte(tc.in))
			if got != tc.want {
				t.Errorf("asJSON(%q) = %s, want %s", tc.in, got, tc.want)
			}
			if !json.Valid([]byte(got)) {
				t.Errorf("asJSON(%q) is not valid JSON", tc.in)
			}
		})
	}
}

// blockingInserter holds its first insert until ctx ends, as a slow database would, then accepts every later one.
type blockingInserter struct {
	fakeInserter
	entered chan struct{}
	first   sync.Once
}

func (b *blockingInserter) InsertPayloads(ctx context.Context, at []time.Time, payloads []string) error {
	blocked := false
	b.first.Do(func() { blocked = true })
	if blocked {
		close(b.entered)
		<-ctx.Done()
		return ctx.Err()
	}
	return b.fakeInserter.InsertPayloads(ctx, at, payloads)
}

// TestClose_CancelsInFlightFlushAndKeepsItsRows: Close doesn't wait out a slow periodic insert; its rows are written by the final flush.
func TestClose_CancelsInFlightFlushAndKeepsItsRows(t *testing.T) {
	ins := &blockingInserter{entered: make(chan struct{})}
	b := New(slog.New(slog.NewTextHandler(io.Discard, nil)), ins, Config{
		FlushInterval: time.Hour, MaxBytes: 1 << 20, FlushTimeout: time.Minute,
	})
	go b.Run()
	b.Add(time.Now(), []byte(`{"a":1}`))
	b.flushSoon()
	<-ins.entered

	start := time.Now()
	b.Close(context.Background())
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("Close took %v; it should cancel the in-flight insert instead of waiting out FlushTimeout", took)
	}
	if calls := ins.snapshot(); len(calls) != 1 || len(calls[0]) != 1 || calls[0][0] != `{"a":1}` {
		t.Fatalf("inserts after Close = %v, want the cancelled row written once", calls)
	}
}

// TestClose_AppliesFlushTimeout: the final insert gets a deadline even when the caller's ctx has none.
func TestClose_AppliesFlushTimeout(t *testing.T) {
	var deadline time.Time
	var ok bool
	ins := insertFunc(func(ctx context.Context) { deadline, ok = ctx.Deadline() })
	b := New(slog.New(slog.NewTextHandler(io.Discard, nil)), ins, Config{
		FlushInterval: time.Hour, MaxBytes: 1 << 20, FlushTimeout: 2 * time.Second,
	})
	go b.Run()
	b.Add(time.Now(), []byte(`{"a":1}`))
	b.Close(context.Background())
	if !ok || time.Until(deadline) > 2*time.Second {
		t.Fatalf("final insert deadline = %v (set %v), want within FlushTimeout", deadline, ok)
	}
}

type insertFunc func(ctx context.Context)

func (f insertFunc) InsertPayloads(ctx context.Context, _ []time.Time, _ []string) error {
	f(ctx)
	return nil
}
