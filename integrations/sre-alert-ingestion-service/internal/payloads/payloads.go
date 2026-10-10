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

// Package payloads keeps raw webhook bodies in memory and writes them to raw_alerts in one statement per flush, so storing every request costs one insert per interval instead of one per webhook.
package payloads

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgconn"
)

// Inserter writes one flush; *postgres.Store implements it.
type Inserter interface {
	InsertPayloads(ctx context.Context, receivedAt []time.Time, payloads []string) error
}

// Config tunes how long bodies wait in memory and how much memory they may take.
type Config struct {
	// FlushInterval is the longest a body waits before it is written.
	FlushInterval time.Duration
	// MaxBytes caps buffered bodies; reaching half of it flushes early, and a body that would pass it is dropped.
	MaxBytes int64
	// FlushTimeout bounds one insert.
	FlushTimeout time.Duration
}

// Buffer collects bodies from request handlers and flushes them from a single goroutine.
type Buffer struct {
	logger *slog.Logger
	ins    Inserter
	cfg    Config

	mu      sync.Mutex
	at      []time.Time
	bodies  []string
	bytes   int64
	dropped int64

	// flushMu keeps the periodic flush and the shutdown flush from inserting the same rows twice.
	flushMu sync.Mutex
	early   chan struct{}
	// runCtx bounds Run's periodic flushes; Close cancels it so an in-flight insert hands its rows to the final flush.
	runCtx    context.Context
	cancelRun context.CancelFunc
	stopped   chan struct{}
}

// New returns a Buffer; call Run in a goroutine and Close on shutdown.
func New(logger *slog.Logger, ins Inserter, cfg Config) *Buffer {
	runCtx, cancelRun := context.WithCancel(context.Background())
	return &Buffer{
		logger:    logger,
		ins:       ins,
		cfg:       cfg,
		early:     make(chan struct{}, 1),
		runCtx:    runCtx,
		cancelRun: cancelRun,
		stopped:   make(chan struct{}),
	}
}

// Add buffers body as received at at; it never blocks on the database.
func (b *Buffer) Add(at time.Time, body []byte) {
	payload := asJSON(body)
	size := int64(len(payload))

	b.mu.Lock()
	if b.bytes+size > b.cfg.MaxBytes {
		b.dropped++
		b.mu.Unlock()
		b.flushSoon()
		return
	}
	b.at = append(b.at, at)
	b.bodies = append(b.bodies, payload)
	b.bytes += size
	high := b.bytes >= b.cfg.MaxBytes/2
	b.mu.Unlock()

	if high {
		b.flushSoon()
	}
}

func (b *Buffer) flushSoon() {
	select {
	case b.early <- struct{}{}:
	default:
	}
}

// Run flushes every FlushInterval, or sooner when the buffer passes half of MaxBytes, until Close.
func (b *Buffer) Run() {
	defer close(b.stopped)
	ticker := time.NewTicker(b.cfg.FlushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-b.runCtx.Done():
			return
		case <-ticker.C:
		case <-b.early:
		}
		ctx, cancel := context.WithTimeout(b.runCtx, b.cfg.FlushTimeout)
		b.flush(ctx)
		cancel()
	}
}

// Close stops Run, cancelling any in-flight periodic insert so its rows rejoin the buffer, then writes everything within ctx and FlushTimeout.
func (b *Buffer) Close(ctx context.Context) {
	b.cancelRun()
	select {
	case <-b.stopped:
	case <-ctx.Done():
	}
	ctx, cancel := context.WithTimeout(ctx, b.cfg.FlushTimeout)
	defer cancel()
	b.flush(ctx)
}

// flush writes everything buffered in one insert; a failed insert keeps the rows for the next flush unless the database rejected their data.
func (b *Buffer) flush(ctx context.Context) {
	b.flushMu.Lock()
	defer b.flushMu.Unlock()

	b.mu.Lock()
	at, bodies, size, dropped := b.at, b.bodies, b.bytes, b.dropped
	b.at, b.bodies, b.bytes, b.dropped = nil, nil, 0, 0
	b.mu.Unlock()

	if dropped > 0 {
		b.logger.Warn("payload buffer full; raw bodies were not stored", "dropped", dropped, "max_bytes", b.cfg.MaxBytes)
	}
	if len(bodies) == 0 {
		return
	}

	start := time.Now()
	err := b.ins.InsertPayloads(ctx, at, bodies)
	if err == nil {
		b.logger.Info("payloads written", "payloads", len(bodies), "bytes", size, "write_ms", time.Since(start).Milliseconds())
		return
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && strings.HasPrefix(pgErr.Code, "22") {
		b.logger.Error("payloads rejected by the database; discarding this batch", "payloads", len(bodies), "error", err)
		return
	}
	b.logger.Error("payload insert failed; keeping them for the next flush", "payloads", len(bodies), "error", err)

	b.mu.Lock()
	b.at = append(at, b.at...)
	b.bodies = append(bodies, b.bodies...)
	b.bytes += size
	b.mu.Unlock()
}

// asJSON returns body unchanged when it is JSON jsonb accepts, and otherwise stores it as a JSON string so one odd body can't fail a whole flush.
func asJSON(body []byte) string {
	if json.Valid(body) && !strings.Contains(string(body), `\u0000`) {
		return string(body)
	}
	// jsonb rejects NUL, so it is replaced along with invalid UTF-8 before quoting.
	text := strings.ReplaceAll(strings.ToValidUTF8(string(body), string(utf8.RuneError)), "\x00", string(utf8.RuneError))
	quoted, _ := json.Marshal(text)
	return string(quoted)
}
