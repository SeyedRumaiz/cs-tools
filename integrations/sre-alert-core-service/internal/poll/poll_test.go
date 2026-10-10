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

package poll

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"alert-core-service/internal/engine"
	"alert-core-service/internal/model"
	"alert-core-service/internal/store"
)

// fakeAlerts serves queued rows to Claim and records acknowledgements.
type fakeAlerts struct {
	mu        sync.Mutex
	queue     []store.ClaimedAlert
	processed []string
	released  []string
	markCalls int
	claims    int
}

func (f *fakeAlerts) Claim(_ context.Context, _ string, limit int, _ time.Duration) ([]store.ClaimedAlert, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.claims++
	n := min(limit, len(f.queue))
	out := f.queue[:n]
	f.queue = f.queue[n:]
	return out, nil
}

func (f *fakeAlerts) MarkProcessed(_ context.Context, ids []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.markCalls++
	f.processed = append(f.processed, ids...)
	return nil
}

func (f *fakeAlerts) Release(_ context.Context, ids []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.released = append(f.released, ids...)
	return nil
}

func (f *fakeAlerts) counts() (processed, released, markCalls int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.processed), len(f.released), f.markCalls
}

// fakeEngine records each group it folds and fails fingerprints listed in fail.
type fakeEngine struct {
	mu       sync.Mutex
	groups   map[string][][]string
	fail     map[string]bool
	block    chan struct{}
	started  chan struct{}
	delivers atomic.Int32
}

func (f *fakeEngine) Normalize(a model.Alert) model.Alert { return a }

func (f *fakeEngine) HandleGroup(_ context.Context, fp string, items []engine.Item) error {
	if f.started != nil {
		select {
		case f.started <- struct{}{}:
		default:
		}
	}
	if f.block != nil {
		<-f.block
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	ids := make([]string, len(items))
	for i, it := range items {
		ids[i] = it.ID
	}
	f.groups[fp] = append(f.groups[fp], ids)
	if f.fail[fp] {
		return errors.New("fold failed")
	}
	return nil
}

func (f *fakeEngine) DeliverDue(context.Context) { f.delivers.Add(1) }

func row(id, uid string) store.ClaimedAlert {
	return store.ClaimedAlert{ID: id, Source: "datadog", ReceivedAt: time.Now(),
		Alert: []byte(`{"service":"svc","metric_name":"m","severity":"Critical","source":"datadog","unique_identifier":"` + uid + `"}`)}
}

func fp(uid string) string { return model.Fingerprint("datadog", "svc", "m", "", uid) }

func settings() Settings {
	return Settings{Interval: time.Hour, Concurrency: 4, MaxBatch: 100, ClaimTTL: time.Minute, DeliverySweepInterval: time.Hour}
}

func newPoller(alerts *fakeAlerts, eng *fakeEngine, s Settings) *Poller {
	return New(slog.New(slog.NewTextHandler(io.Discard, nil)), alerts, eng, s)
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(time.Millisecond)
	}
}

func run(p *Poller) (stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		p.Run(ctx)
	}()
	return func() {
		cancel()
		<-done
	}
}

func TestRun_GroupsByFingerprintAndBatchesAcks(t *testing.T) {
	alerts := &fakeAlerts{}
	for i := range 90 {
		alerts.queue = append(alerts.queue, row(fmt.Sprintf("ALT%03d", i), fmt.Sprintf("u%d", i%3)))
	}
	eng := &fakeEngine{groups: map[string][][]string{}}
	stop := run(newPoller(alerts, eng, settings()))
	waitFor(t, func() bool { p, _, _ := alerts.counts(); return p == 90 })
	stop()

	for _, uid := range []string{"u0", "u1", "u2"} {
		groups := eng.groups[fp(uid)]
		if len(groups) != 1 || len(groups[0]) != 30 {
			t.Fatalf("fingerprint %s groups = %v, want one group of 30", uid, groups)
		}
	}
	if _, _, marks := alerts.counts(); marks > 3 {
		t.Errorf("MarkProcessed calls = %d, want the 90 acks batched into a few statements", marks)
	}
	if eng.delivers.Load() == 0 {
		t.Error("delivery was never triggered after folding")
	}
}

func TestRun_FailedGroupIsReleasedAndBadJSONIsDone(t *testing.T) {
	alerts := &fakeAlerts{queue: []store.ClaimedAlert{row("ALT1", "ok"), row("ALT2", "bad"), {ID: "ALT3", Alert: []byte("not json")}}}
	eng := &fakeEngine{groups: map[string][][]string{}, fail: map[string]bool{fp("bad"): true}}
	stop := run(newPoller(alerts, eng, settings()))
	waitFor(t, func() bool { p, r, _ := alerts.counts(); return p == 2 && r == 1 })
	stop()

	slices.Sort(alerts.processed)
	if !slices.Equal(alerts.processed, []string{"ALT1", "ALT3"}) || !slices.Equal(alerts.released, []string{"ALT2"}) {
		t.Fatalf("processed = %v released = %v", alerts.processed, alerts.released)
	}
}

func TestRun_ClaimsStayWithinCapacity(t *testing.T) {
	alerts := &fakeAlerts{}
	for i := range 1000 {
		alerts.queue = append(alerts.queue, row(fmt.Sprintf("ALT%04d", i), fmt.Sprintf("u%d", i)))
	}
	eng := &fakeEngine{groups: map[string][][]string{}, block: make(chan struct{}), started: make(chan struct{}, 1)}
	s := settings()
	p := newPoller(alerts, eng, s)
	stop := run(p)
	<-eng.started
	time.Sleep(50 * time.Millisecond)
	if in := p.inflight.Load(); in > int64(p.capacity) {
		t.Fatalf("inflight = %d, want at most capacity %d while workers are blocked", in, p.capacity)
	}
	close(eng.block)
	waitFor(t, func() bool { n, _, _ := alerts.counts(); return n == 1000 })
	stop()
}

func TestRun_ShutdownReleasesUnstartedWork(t *testing.T) {
	alerts := &fakeAlerts{}
	for i := range 50 {
		alerts.queue = append(alerts.queue, row(fmt.Sprintf("ALT%02d", i), "same"))
	}
	eng := &fakeEngine{groups: map[string][][]string{}, block: make(chan struct{}), started: make(chan struct{}, 1)}
	s := settings()
	s.MaxBatch = 20
	p := newPoller(alerts, eng, s)
	stop := run(p)
	<-eng.started

	stopped := make(chan struct{})
	go func() { stop(); close(stopped) }()
	time.Sleep(20 * time.Millisecond)
	close(eng.block)
	<-stopped

	processed, released, _ := alerts.counts()
	if p.inflight.Load() != 0 || processed+released != 50-len(alerts.queue) {
		t.Fatalf("inflight = %d processed = %d released = %d unclaimed = %d, want every claimed alert acknowledged", p.inflight.Load(), processed, released, len(alerts.queue))
	}
	if released == 0 {
		t.Fatal("want groups queued behind the blocked one released on shutdown")
	}
}

func TestShard_StableAndInRange(t *testing.T) {
	for _, f := range []string{"a", "b", fp("x")} {
		s := shard(f, 7)
		if s < 0 || s >= 7 || s != shard(f, 7) {
			t.Fatalf("shard(%q) = %d, want a stable index in [0,7)", f, s)
		}
	}
}
