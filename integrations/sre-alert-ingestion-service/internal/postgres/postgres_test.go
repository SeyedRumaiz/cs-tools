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

//go:build integration

package postgres

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"testing"
	"time"
)

// TestInsertBatch_IdempotentWithDatabaseClock runs with -tags integration against a throwaway database created from PG*: a retried batch is a no-op and created_at comes from the server.
func TestInsertBatch_IdempotentWithDatabaseClock(t *testing.T) {
	cfg, err := ConfigFromEnv()
	if err != nil {
		t.Fatalf("ConfigFromEnv: %v", err)
	}
	admin, err := Connect(cfg, 10*time.Second, 5*time.Second)
	if err != nil {
		t.Fatalf("connect to %s: %v", cfg.Database, err)
	}
	defer admin.Close()

	dbName := fmt.Sprintf("ingestion_test_%d", rand.Int63())
	if _, err := admin.Exec(context.Background(), "CREATE DATABASE "+dbName); err != nil {
		t.Fatalf("create database %s: %v", dbName, err)
	}
	defer func() {
		_, _ = admin.Exec(context.Background(), "DROP DATABASE "+dbName)
	}()

	testCfg := cfg
	testCfg.Database = dbName
	pool, err := Connect(testCfg, 10*time.Second, 5*time.Second)
	if err != nil {
		t.Fatalf("connect to %s: %v", dbName, err)
	}
	defer pool.Close()
	schema, err := os.ReadFile("../../schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), string(schema)); err != nil {
		t.Fatalf("apply schema.sql: %v", err)
	}
	store := NewStore(pool, 5*time.Second, 5*time.Second)
	ctx := context.Background()

	seqs, err := store.ClaimIDs(ctx, 3)
	if err != nil || len(seqs) != 3 {
		t.Fatalf("ClaimIDs = %v, %v", seqs, err)
	}
	rows := make([]InsertRow, len(seqs))
	for i, seq := range seqs {
		rows[i] = InsertRow{ID: FormatID(seq), Source: "aws", Alert: []byte(fmt.Sprintf(`{"service":"s%d"}`, i)), Fingerprint: fmt.Sprintf("fp%d", i)}
	}
	before := time.Now().Add(-time.Minute)
	if err := store.InsertBatch(ctx, rows); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	if err := store.InsertBatch(ctx, rows); err != nil {
		t.Fatalf("retrying the same batch must be a no-op, got %v", err)
	}

	var count int
	var oldest time.Time
	if err := pool.QueryRow(ctx, `SELECT count(*), min(created_at) FROM alerts WHERE fingerprint LIKE 'fp%'`).Scan(&count, &oldest); err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Errorf("rows = %d, want 3", count)
	}
	if oldest.Before(before) {
		t.Errorf("created_at = %v, want the database clock", oldest)
	}

	received := time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)
	payloads := []string{`{"rule_id":"a8f2","severity":"1"}`, `"not json"`}
	if err := store.InsertPayloads(ctx, []time.Time{received, received.Add(time.Second)}, payloads); err != nil {
		t.Fatalf("InsertPayloads: %v", err)
	}
	var stored int
	var ruleID string
	var at time.Time
	if err := pool.QueryRow(ctx, `SELECT count(*), max(payload->>'rule_id'), min(received_at) FROM raw_alerts`).Scan(&stored, &ruleID, &at); err != nil {
		t.Fatal(err)
	}
	if stored != 2 || ruleID != "a8f2" || !at.Equal(received) {
		t.Errorf("raw_alerts: rows=%d rule_id=%q received_at=%v, want 2, a8f2, %v", stored, ruleID, at, received)
	}
}
