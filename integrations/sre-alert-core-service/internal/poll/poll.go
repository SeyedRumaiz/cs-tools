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

// Package poll claims alerts with SELECT ... FOR UPDATE SKIP LOCKED and folds them through a continuous fingerprint-sharded pipeline; every replica runs its own Poller, no leader election.
package poll

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"hash/fnv"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"alert-core-service/internal/engine"
	"alert-core-service/internal/model"
	"alert-core-service/internal/store"
)

// Settings tunes a Poller's cadence, claim size, and concurrency.
type Settings struct {
	// Interval is the backstop cadence; a ping (POST /alertz) normally wakes the poller sooner.
	Interval time.Duration
	// Concurrency is the number of fingerprint-sharded workers; one fingerprint always lands on one worker.
	Concurrency int
	// MaxBatch caps the seed rows of one claim; siblings sharing a seed's fingerprint can add as many again.
	MaxBatch int
	// ClaimTTL bounds how long a claimed-but-unfinished alert is held before any replica may reclaim it.
	ClaimTTL time.Duration
	// DeliverySweepInterval is the longest delivery waits when nothing triggers it, which bounds retry latency.
	DeliverySweepInterval time.Duration
}

// AlertStore is the claim queue; *store.AlertRepo implements it.
type AlertStore interface {
	Claim(ctx context.Context, owner string, limit int, claimTTL time.Duration) ([]store.ClaimedAlert, error)
	MarkProcessed(ctx context.Context, ids []string) error
	Release(ctx context.Context, ids []string) error
}

// Engine is what the poller needs from *engine.Engine.
type Engine interface {
	Normalize(alert model.Alert) model.Alert
	HandleGroup(ctx context.Context, fp string, items []engine.Item) error
	DeliverDue(ctx context.Context)
}

const (
	// ackBatch and ackEvery bound how long finished ids wait before one MarkProcessed/Release statement covers them.
	ackBatch = 500
	ackEvery = 20 * time.Millisecond
	// ackTimeout bounds a final acknowledgement written after shutdown cancelled the poller's context.
	ackTimeout = 5 * time.Second
)

// identity names this replica for the claimed_by column; purely diagnostic.
func identity() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "pod"
	}
	var b [4]byte
	_, _ = rand.Read(b[:])
	return host + "-" + hex.EncodeToString(b[:])
}

type group struct {
	fp    string
	items []engine.Item
}

type ack struct {
	done  []string
	retry []string
}

// Poller claims and folds alerts for one replica.
type Poller struct {
	logger   *slog.Logger
	alerts   AlertStore
	engine   Engine
	identity string
	settings Settings
	// capacity is the most alerts in flight at once; a claim tops up to it.
	capacity int
	inflight atomic.Int64
	wake     chan struct{}
	space    chan struct{}
	deliver  chan struct{}
	shards   []chan group
	acks     chan ack
}

// New returns a Poller that claims from alerts and folds through e.
func New(logger *slog.Logger, alerts AlertStore, e Engine, settings Settings) *Poller {
	capacity := 2 * settings.MaxBatch
	p := &Poller{
		logger:   logger,
		alerts:   alerts,
		engine:   e,
		identity: identity(),
		settings: settings,
		capacity: capacity,
		wake:     make(chan struct{}, 1),
		space:    make(chan struct{}, 1),
		deliver:  make(chan struct{}, 1),
		shards:   make([]chan group, settings.Concurrency),
		acks:     make(chan ack, settings.Concurrency),
	}
	for i := range p.shards {
		// A claim adds at most 2 x MaxBatch groups and capacity caps everything in flight, so sends never block.
		p.shards[i] = make(chan group, 2*capacity)
	}
	return p
}

func signal(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

// Wake nudges the poller to claim now; a ping burst collapses into one claim.
func (p *Poller) Wake() { signal(p.wake) }

// Run claims until ctx ends, then releases unstarted work, acknowledges what finished, and returns.
func (p *Poller) Run(ctx context.Context) {
	var workers sync.WaitGroup
	for _, shard := range p.shards {
		workers.Add(1)
		go func() {
			defer workers.Done()
			p.work(ctx, shard)
		}()
	}
	ackerDone := make(chan struct{})
	go func() {
		defer close(ackerDone)
		p.ackLoop(ctx)
	}()
	deliveryDone := make(chan struct{})
	go func() {
		defer close(deliveryDone)
		p.deliveryLoop(ctx)
	}()

	p.claimLoop(ctx)

	for _, shard := range p.shards {
		close(shard)
	}
	workers.Wait()
	close(p.acks)
	<-ackerDone
	<-deliveryDone
}

// claimLoop tops the pipeline up to capacity whenever it is woken, ticks, or frees space.
func (p *Poller) claimLoop(ctx context.Context) {
	ticker := time.NewTicker(p.settings.Interval)
	defer ticker.Stop()
	for {
		p.fill(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-p.wake:
		case <-p.space:
		}
	}
}

// fill claims back-to-back while at least half a batch of room is free and the queue keeps returning work.
func (p *Poller) fill(ctx context.Context) {
	for ctx.Err() == nil {
		free := p.capacity - int(p.inflight.Load())
		limit := min(p.settings.MaxBatch, free/2)
		if limit < max(p.settings.MaxBatch/4, 1) {
			return // wait for the acker to free space instead of issuing tiny claims.
		}
		claimed, err := p.alerts.Claim(ctx, p.identity, limit, p.settings.ClaimTTL)
		if err != nil {
			if ctx.Err() == nil {
				p.logger.Error("failed to claim alerts", "error", err)
			}
			return
		}
		if len(claimed) == 0 {
			return
		}
		p.inflight.Add(int64(len(claimed)))
		p.dispatch(claimed)
		if len(claimed) < limit {
			return
		}
	}
}

// dispatch decodes claimed rows, groups them by fingerprint and sends each group to its shard; undecodable rows are acknowledged as done.
func (p *Poller) dispatch(claimed []store.ClaimedAlert) {
	groups := map[string]*group{}
	var order []*group
	var bad []string
	for _, c := range claimed {
		var a model.Alert
		if err := json.Unmarshal(c.Alert, &a); err != nil {
			p.logger.Error("alert unprocessable, marking done without processing", "alert_id", c.ID, "error", err)
			bad = append(bad, c.ID)
			continue
		}
		a = p.engine.Normalize(a)
		a.ReceivedAt = c.ReceivedAt
		fp := model.Fingerprint(a.Source, a.Service, a.MetricName, a.Environment, a.UniqueIdentifier)
		g, ok := groups[fp]
		if !ok {
			g = &group{fp: fp}
			groups[fp] = g
			order = append(order, g)
		}
		g.items = append(g.items, engine.Item{ID: c.ID, Alert: a})
	}
	if len(bad) > 0 {
		p.acks <- ack{done: bad}
	}
	for _, g := range order {
		p.shards[shard(g.fp, len(p.shards))] <- *g
	}
}

// work folds this shard's groups in order; after shutdown it releases what it didn't start.
func (p *Poller) work(ctx context.Context, shard chan group) {
	for g := range shard {
		ids := make([]string, len(g.items))
		for i, it := range g.items {
			ids[i] = it.ID
		}
		if ctx.Err() != nil || p.engine.HandleGroup(ctx, g.fp, g.items) != nil {
			p.acks <- ack{retry: ids}
			continue
		}
		p.acks <- ack{done: ids}
	}
}

// ackLoop batches finished ids into one MarkProcessed and one Release per flush, then frees pipeline space and triggers delivery.
func (p *Poller) ackLoop(ctx context.Context) {
	ticker := time.NewTicker(ackEvery)
	defer ticker.Stop()
	var done, retry []string
	flush := func() {
		if len(done) == 0 && len(retry) == 0 {
			return
		}
		actx, cancel := context.WithTimeout(context.WithoutCancel(ctx), ackTimeout)
		if len(done) > 0 {
			// A failed mark only delays the ids until their claim expires; reprocessing them is a no-op.
			if err := p.alerts.MarkProcessed(actx, done); err != nil {
				p.logger.Error("failed to mark alerts processed; they will be reclaimed after claim_ttl", "alerts", len(done), "error", err)
			}
		}
		if len(retry) > 0 {
			if err := p.alerts.Release(actx, retry); err != nil {
				p.logger.Error("failed to release alert claims; they will be reclaimed after claim_ttl", "alerts", len(retry), "error", err)
			}
		}
		cancel()
		p.inflight.Add(-int64(len(done) + len(retry)))
		if len(done) > 0 {
			signal(p.deliver)
		}
		done, retry = done[:0], retry[:0]
		signal(p.space)
	}
	for {
		select {
		case a, ok := <-p.acks:
			if !ok {
				flush()
				return
			}
			done = append(done, a.done...)
			retry = append(retry, a.retry...)
			if len(done)+len(retry) >= ackBatch {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

// deliveryLoop runs DeliverDue after folds and at least every DeliverySweepInterval, so CSM/Chat latency never slows claiming.
func (p *Poller) deliveryLoop(ctx context.Context) {
	ticker := time.NewTicker(p.settings.DeliverySweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-p.deliver:
		}
		p.engine.DeliverDue(ctx)
	}
}

// shard maps a fingerprint to a worker index via FNV-1a so an incident's alerts land on the same worker.
func shard(fingerprint string, workers int) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(fingerprint))
	return int(h.Sum32() % uint32(workers))
}
