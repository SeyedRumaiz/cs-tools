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

// Package store owns the alerts claim queue and the incidents_processed and incident_notes tables.
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AlertRepo claims unprocessed rows from the alerts table and marks them done.
type AlertRepo struct {
	pool *pgxpool.Pool
}

// NewAlertRepo wraps pool for claim-queue access to the alerts table.
func NewAlertRepo(pool *pgxpool.Pool) *AlertRepo {
	return &AlertRepo{pool: pool}
}

// ClaimedAlert is one claimed row; Alert is the raw jsonb payload and ReceivedAt is alerts.created_at.
type ClaimedAlert struct {
	ID         string
	Source     string
	Alert      []byte
	ReceivedAt time.Time
}

// claimQuery seeds from the oldest claimable ids, then pulls up to the same number of claimable siblings sharing a seed's fingerprint, so one incident's alerts tend to land on one replica; SKIP LOCKED keeps replicas from ever claiming the same row.
const claimQuery = `
WITH seed AS (
	SELECT id, fingerprint FROM alerts
	WHERE processed_at IS NULL AND (claimed_until IS NULL OR claimed_until < now())
	ORDER BY id
	LIMIT $1
	FOR UPDATE SKIP LOCKED
), siblings AS (
	SELECT a.id FROM alerts a
	WHERE a.fingerprint IN (SELECT fingerprint FROM seed WHERE fingerprint IS NOT NULL)
	  AND a.processed_at IS NULL AND (a.claimed_until IS NULL OR a.claimed_until < now())
	  AND a.id NOT IN (SELECT id FROM seed)
	ORDER BY a.id
	LIMIT $1
	FOR UPDATE SKIP LOCKED
)
UPDATE alerts SET claimed_by = $2, claimed_until = now() + $3::interval
WHERE id IN (SELECT id FROM seed UNION ALL SELECT id FROM siblings)
RETURNING id, source, alert, created_at`

// Claim reserves up to 2 x limit rows for owner until claimTTL elapses on the database clock, so a crashed replica's claims get reclaimed.
func (r *AlertRepo) Claim(ctx context.Context, owner string, limit int, claimTTL time.Duration) ([]ClaimedAlert, error) {
	rows, err := r.pool.Query(ctx, claimQuery, limit, owner, claimTTL)
	if err != nil {
		return nil, fmt.Errorf("claim batch: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (ClaimedAlert, error) {
		var c ClaimedAlert
		err := row.Scan(&c.ID, &c.Source, &c.Alert, &c.ReceivedAt)
		return c, err
	})
	if err != nil {
		return nil, fmt.Errorf("claim batch: %w", err)
	}
	return out, nil
}

// MarkProcessed stamps every id done in one statement so no replica claims them again.
func (r *AlertRepo) MarkProcessed(ctx context.Context, ids []string) error {
	if _, err := r.pool.Exec(ctx, `UPDATE alerts SET processed_at = now() WHERE id = ANY($1)`, ids); err != nil {
		return fmt.Errorf("mark %d alerts processed: %w", len(ids), err)
	}
	return nil
}

// Release clears claims so the ids are retried next cycle instead of waiting out the claim TTL.
func (r *AlertRepo) Release(ctx context.Context, ids []string) error {
	if _, err := r.pool.Exec(ctx, `UPDATE alerts SET claimed_by = NULL, claimed_until = NULL WHERE id = ANY($1) AND processed_at IS NULL`, ids); err != nil {
		return fmt.Errorf("release %d alert claims: %w", len(ids), err)
	}
	return nil
}

// PurgeRaw deletes up to limit raw_alerts rows received before cutoff, oldest first.
func (r *AlertRepo) PurgeRaw(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
	tag, err := r.pool.Exec(ctx, `DELETE FROM raw_alerts WHERE id IN (
		SELECT id FROM raw_alerts WHERE received_at < $1 ORDER BY received_at LIMIT $2)`, cutoff, limit)
	if err != nil {
		return 0, fmt.Errorf("purge raw alerts: %w", err)
	}
	return tag.RowsAffected(), nil
}

// Purge deletes up to limit alerts processed before cutoff, oldest ids first.
func (r *AlertRepo) Purge(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
	tag, err := r.pool.Exec(ctx, `DELETE FROM alerts WHERE id IN (
		SELECT id FROM alerts WHERE processed_at < $1 ORDER BY id LIMIT $2)`, cutoff, limit)
	if err != nil {
		return 0, fmt.Errorf("purge alerts: %w", err)
	}
	return tag.RowsAffected(), nil
}
