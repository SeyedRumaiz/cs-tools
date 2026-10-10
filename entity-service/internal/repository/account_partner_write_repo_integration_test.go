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
// KIND, either express or implied. See the License for the
// specific language governing permissions and limitations
// under the License.

package repository

import (
	"context"
	"os"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
)

// These run the partner refresh's set replace against a live PostgreSQL
// instance and read the result back through AccountPartnerRepository, the
// invitation validator's reader, so the writer is held to the reader's
// contract. Skipped unless ENTITY_TEST_DATABASE_URL is set (see
// account_repo_salesforce_integration_test.go for the setup).

// Fixture accounts, distinct from every other integration file's.
var apiAccounts = map[string]string{
	"customer": "5fa00000-0000-4000-8000-000000000001",
	"p1":       "5fa00000-0000-4000-8000-000000000002",
	"p2":       "5fa00000-0000-4000-8000-000000000003",
	"p3":       "5fa00000-0000-4000-8000-000000000004",
	"other":    "5fa00000-0000-4000-8000-000000000005",
}

func apiSfID(key string) string { return "001APITEST" + key }

func newAccountPartnerIntegrationPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("ENTITY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("ENTITY_TEST_DATABASE_URL is not set; skipping the live-database tests")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	ids := make([]string, 0, len(apiAccounts))
	for _, id := range apiAccounts {
		ids = append(ids, id)
	}
	clean := func() {
		// account_relationship rows cascade with their accounts.
		if _, err := pool.Exec(ctx, `DELETE FROM account WHERE id = ANY($1::uuid[])`, ids); err != nil {
			t.Fatalf("clean accounts: %v", err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM salesforce_ingest_state WHERE entity = $1 AND sf_id = $2`,
			domain.SalesforceIngestEntityAccountPartners, apiSfID("customer")); err != nil {
			t.Fatalf("clean ledger: %v", err)
		}
	}
	clean()
	t.Cleanup(clean)
	for key, id := range apiAccounts {
		if _, err := pool.Exec(ctx, `INSERT INTO account (id, created_on, updated_on, created_by, updated_by, name, number, sf_id)
			VALUES ($1, now(), now(), 't', 't', $2, $3, $3)`, id, "API "+key, apiSfID(key)); err != nil {
			t.Fatalf("seed account %s: %v", key, err)
		}
	}
	return pool
}

func partnersOf(t *testing.T, pool *pgxpool.Pool) []string {
	t.Helper()
	got, err := NewAccountPartnerRepository(pool).PartnerAccountSfIDs(context.Background(), apiAccounts["customer"])
	if err != nil {
		t.Fatalf("PartnerAccountSfIDs: %v", err)
	}
	sort.Strings(got)
	return got
}

func TestAccountPartnerIntegration_SetReplaceAndReaderContract(t *testing.T) {
	pool := newAccountPartnerIntegrationPool(t)
	ctx := context.Background()
	repo := NewAccountPartnerWriteRepository(pool)
	customer := apiAccounts["customer"]

	// Rows the refresh must never touch: a different label on the customer,
	// and a csm-sync style partner link carrying a ServiceNow type sys_id.
	if _, err := pool.Exec(ctx, `INSERT INTO account_relationship (id, created_on, updated_on, created_by, updated_by,
			from_account_id, to_account_id, relationship_type_id, relationship_label, reverse_relationship_label, is_reverse_relationship)
		VALUES (gen_random_uuid(), now(), now(), 'csm-sync', 'csm-sync', $1, $2, 'abc123', 'Is Reseller Of', 'Is Resold By', false),
		       (gen_random_uuid(), now(), now(), 'csm-sync', 'csm-sync', $3, $2, 'def456', 'Is Partner Of', 'Is Customer Of', false)`,
		apiAccounts["other"], customer, apiAccounts["p1"]); err != nil {
		t.Fatalf("seed relationships: %v", err)
	}

	replace := func(keys ...string) (int, int) {
		t.Helper()
		ids := []string{}
		for _, k := range keys {
			ids = append(ids, apiAccounts[k])
		}
		state := domain.UpsertSalesforceIngestStateRequest{
			Entity: domain.SalesforceIngestEntityAccountPartners, SfID: apiSfID("customer"),
			EventModifiedOn: time.Now().UTC(), EventType: domain.SalesforceEventUpdated, Status: domain.SalesforceIngestSucceeded,
		}
		added, removed, err := repo.ReplacePartners(ctx, apiSfID("customer"), customer, ids, state)
		if err != nil {
			t.Fatalf("ReplacePartners(%v): %v", keys, err)
		}
		return added, removed
	}
	count := func(where string, args ...any) int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM account_relationship WHERE `+where, args...).Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
		return n
	}

	// {p1, p2}: p1's forward row exists (csm-sync), so p1 gains only its
	// reverse row and p2 gains both.
	if added, removed := replace("p1", "p2"); added != 3 || removed != 0 {
		t.Errorf("first replace added=%d removed=%d, want 3/0", added, removed)
	}
	if got, want := partnersOf(t, pool), []string{apiSfID("p1"), apiSfID("p2")}; !reflect.DeepEqual(got, want) {
		t.Errorf("partners = %v, want %v", got, want)
	}
	if n := count(`from_account_id = $1 AND to_account_id = $2 AND relationship_label = 'Is Customer Of'
		AND reverse_relationship_label = 'Is Partner Of' AND is_reverse_relationship`, customer, apiAccounts["p2"]); n != 1 {
		t.Errorf("reverse rows for p2 = %d, want 1", n)
	}
	if n := count(`from_account_id = $1 AND to_account_id = $2 AND relationship_label = 'Is Partner Of'
		AND reverse_relationship_label = 'Is Customer Of' AND NOT is_reverse_relationship AND created_by = 'salesforce-sync'`, apiAccounts["p2"], customer); n != 1 {
		t.Errorf("forward rows for p2 = %d, want 1", n)
	}

	// A replay changes nothing.
	if added, removed := replace("p1", "p2"); added != 0 || removed != 0 {
		t.Errorf("replay added=%d removed=%d, want 0/0", added, removed)
	}

	// {p2, p3}: p1's forward (the csm-sync row) and reverse rows go, p3 arrives.
	if added, removed := replace("p2", "p3"); added != 2 || removed != 2 {
		t.Errorf("second replace added=%d removed=%d, want 2/2", added, removed)
	}
	if got, want := partnersOf(t, pool), []string{apiSfID("p2"), apiSfID("p3")}; !reflect.DeepEqual(got, want) {
		t.Errorf("partners = %v, want %v", got, want)
	}

	// The reader tolerates either direction: with only reverse rows left for
	// p2 (its forward row removed by hand), p2 is still a partner.
	if _, err := pool.Exec(ctx, `DELETE FROM account_relationship WHERE from_account_id = $1 AND to_account_id = $2`, apiAccounts["p2"], customer); err != nil {
		t.Fatalf("drop forward row: %v", err)
	}
	if got := partnersOf(t, pool); !reflect.DeepEqual(got, []string{apiSfID("p2"), apiSfID("p3")}) {
		t.Errorf("partners with a reverse-only link = %v", got)
	}

	// {}: every partner row of the customer goes; the other label stays.
	replace()
	if got := partnersOf(t, pool); len(got) != 0 {
		t.Errorf("partners after empty replace = %v, want none", got)
	}
	if n := count(`to_account_id = $1 AND relationship_label = 'Is Reseller Of'`, customer); n != 1 {
		t.Errorf("unrelated label rows = %d, want 1 (untouched)", n)
	}

	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM salesforce_ingest_state WHERE entity = $1 AND sf_id = $2`,
		domain.SalesforceIngestEntityAccountPartners, apiSfID("customer")).Scan(&status); err != nil || status != "SUCCEEDED" {
		t.Errorf("ledger status = %q err = %v, want SUCCEEDED", status, err)
	}
}
