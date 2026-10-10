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

package store

import (
	"context"
	"log/slog"
	"time"

	"alert-core-service/internal/pglock"
)

// purgeChunk bounds each DELETE so retention never holds long row locks or a large transaction.
const purgeChunk = 5000

// Retention deletes processed alerts, raw webhook bodies and settled incidents older than their TTLs; raw_alerts shares AlertTTL.
type Retention struct {
	Logger      *slog.Logger
	Alerts      *AlertRepo
	Incidents   *IncidentRepo
	Locker      *pglock.Locker
	Interval    time.Duration
	AlertTTL    time.Duration
	IncidentTTL time.Duration
}

// Run purges every Interval until ctx ends; a try-lock keeps it to one replica at a time.
func (r *Retention) Run(ctx context.Context) {
	ticker := time.NewTicker(r.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.purgeOnce(ctx)
		}
	}
}

func (r *Retention) purgeOnce(ctx context.Context) {
	unlock, ok, err := r.Locker.TryLock(ctx, "retention")
	if err != nil {
		r.Logger.Warn("retention: lock failed", "error", err)
		return
	}
	if !ok {
		return
	}
	defer unlock()
	alerts := r.purge(ctx, "alerts", time.Now().Add(-r.AlertTTL), r.Alerts.Purge)
	raw := r.purge(ctx, "raw_alerts", time.Now().Add(-r.AlertTTL), r.Alerts.PurgeRaw)
	incidents := r.purge(ctx, "incidents", time.Now().Add(-r.IncidentTTL), r.Incidents.Purge)
	if alerts > 0 || raw > 0 || incidents > 0 {
		r.Logger.Info("retention purged old rows", "alerts", alerts, "raw_alerts", raw, "incidents", incidents)
	}
}

func (r *Retention) purge(ctx context.Context, table string, cutoff time.Time, del func(context.Context, time.Time, int) (int64, error)) int64 {
	var total int64
	for ctx.Err() == nil {
		n, err := del(ctx, cutoff, purgeChunk)
		if err != nil {
			r.Logger.Warn("retention: purge failed", "table", table, "error", err)
			return total
		}
		total += n
		if n < purgeChunk {
			return total
		}
	}
	return total
}
