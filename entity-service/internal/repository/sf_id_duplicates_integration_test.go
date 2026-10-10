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
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
)

// Two copies share each sf_id: the OLDER copy has no children, the newer one
// does. Skipped unless ENTITY_TEST_DATABASE_URL is set (setup as in account_repo_salesforce_integration_test.go).

const (
	dupAccountSfID  = "001DUPTEST0000001A"
	dupAccountBare  = "5fd00000-0000-4000-8000-000000000001"
	dupAccountRef   = "5fd00000-0000-4000-8000-000000000002"
	dupOppSfID      = "006DUPTEST0000001A"
	dupOppBare      = "5fd00000-0000-4000-8000-000000000011"
	dupOppRef       = "5fd00000-0000-4000-8000-000000000012"
	dupInvoiceSfID  = "a0IDUPTEST0000001A"
	dupInvoiceBare  = "5fd00000-0000-4000-8000-000000000021"
	dupInvoiceRef   = "5fd00000-0000-4000-8000-000000000022"
	dupProjectSfID  = "a0PDUPTEST0000001A"
	dupProjectBare  = "5fd00000-0000-4000-8000-000000000031"
	dupProjectRef   = "5fd00000-0000-4000-8000-000000000032"
	dupLinkID       = "5fd00000-0000-4000-8000-000000000041"
	dupLineItemSfID = "00kDUPTEST0000001A"
)

func newDuplicateSfIDPool(t *testing.T) *pgxpool.Pool {
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
	clean := func() {
		for _, stmt := range []string{
			`DELETE FROM sf_invoice WHERE sf_id = '` + dupInvoiceSfID + `'`,
			`DELETE FROM sf_opportunity_link WHERE id = '` + dupLinkID + `'`,
			`DELETE FROM sf_opportunity WHERE sf_id = '` + dupOppSfID + `'`,
			`DELETE FROM project WHERE sf_id = '` + dupProjectSfID + `'`,
			`DELETE FROM account WHERE sf_id = '` + dupAccountSfID + `'`,
			`DELETE FROM salesforce_ingest_state WHERE sf_id LIKE '%DUPTEST%'`,
		} {
			if _, err := pool.Exec(ctx, stmt); err != nil {
				t.Fatalf("clean: %v", err)
			}
		}
	}
	clean()
	t.Cleanup(clean)
	for _, stmt := range []string{
		`INSERT INTO account (id, created_on, updated_on, created_by, updated_by, name, number, sf_id) VALUES
			('` + dupAccountBare + `', now() - interval '1 day', now(), 't', 't', 'Bare', 'ACC-DUP-1', '` + dupAccountSfID + `'),
			('` + dupAccountRef + `', now(), now(), 't', 't', 'Referenced', 'ACC-DUP-2', '` + dupAccountSfID + `')`,
		`INSERT INTO sf_opportunity (id, created_on, updated_on, created_by, updated_by, sf_id, name, account_id) VALUES
			('` + dupOppBare + `', now() - interval '1 day', now(), 't', 't', '` + dupOppSfID + `', 'Bare', NULL),
			('` + dupOppRef + `', now(), now(), 't', 't', '` + dupOppSfID + `', 'Referenced', '` + dupAccountRef + `')`,
		`INSERT INTO project (id, created_on, updated_on, created_by, updated_by, key, sf_id) VALUES
			('` + dupProjectBare + `', now() - interval '1 day', now(), 't', 't', 'DUPTEST-BARE', '` + dupProjectSfID + `'),
			('` + dupProjectRef + `', now(), now(), 't', 't', 'DUPTEST-REF', '` + dupProjectSfID + `')`,
		`INSERT INTO sf_opportunity_link (id, created_on, updated_on, created_by, updated_by, opportunity_id, project_id) VALUES
			('` + dupLinkID + `', now(), now(), 't', 't', '` + dupOppRef + `', '` + dupProjectRef + `')`,
		`INSERT INTO sf_invoice (id, created_on, updated_on, created_by, updated_by, sf_id, name, opportunity_id) VALUES
			('` + dupInvoiceBare + `', now() - interval '1 day', now(), 't', 't', '` + dupInvoiceSfID + `', 'Bare', NULL),
			('` + dupInvoiceRef + `', now(), now(), 't', 't', '` + dupInvoiceSfID + `', 'Referenced', '` + dupOppRef + `')`,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	return pool
}

func dupState(entity, sfID string) domain.UpsertSalesforceIngestStateRequest {
	return domain.UpsertSalesforceIngestStateRequest{Entity: entity, SfID: sfID, EventModifiedOn: time.Now().UTC(),
		EventType: domain.SalesforceEventUpdated, Status: domain.SalesforceIngestSucceeded}
}

func dupName(t *testing.T, pool *pgxpool.Pool, table, id string) string {
	t.Helper()
	var name string
	if err := pool.QueryRow(context.Background(), `SELECT name FROM `+table+` WHERE id = $1`, id).Scan(&name); err != nil {
		t.Fatalf("read %s %s: %v", table, id, err)
	}
	return name
}

// Only the referenced copy is written, the lookups return it, and the
// opportunity's line items attach to it.
func TestDuplicateSfIDIntegration_WritesOnlyTheReferencedCopy(t *testing.T) {
	pool := newDuplicateSfIDPool(t)
	ctx := context.Background()

	accounts := &accountRepo{db: NewScoped(pool)}
	if err := accounts.UpsertFromSalesforce(ctx, domain.SalesforceAccountUpsert{SfID: dupAccountSfID, Name: "From SF", Number: "ACC-DUP-2"},
		dupState(domain.SalesforceIngestEntityAccount, dupAccountSfID)); err != nil {
		t.Fatalf("account upsert: %v", err)
	}
	if got := dupName(t, pool, "account", dupAccountRef); got != "From SF" {
		t.Errorf("referenced account name = %q, want From SF", got)
	}
	if got := dupName(t, pool, "account", dupAccountBare); got != "Bare" {
		t.Errorf("bare account name = %q, want unchanged", got)
	}
	if id, err := accounts.LookupAccountIDBySfID(ctx, dupAccountSfID); err != nil || id == nil || *id != dupAccountRef {
		t.Errorf("account lookup = %v, %v; want %s", id, err, dupAccountRef)
	}

	name, acct := "From SF", dupAccountRef
	res, err := NewSalesforceOpportunityRepository(pool).UpsertFromSalesforce(ctx, domain.SalesforceOpportunityUpsert{
		SfID: dupOppSfID, Name: &name, AccountID: &acct,
		LineItems: []domain.SalesforceOpportunityLineItemUpsert{{LineItemSfID: dupLineItemSfID, Name: &name}},
	}, dupState(domain.SalesforceIngestEntityOpportunity, dupOppSfID))
	if err != nil || res.Created || res.OpportunityID != dupOppRef {
		t.Fatalf("opportunity upsert = %+v, %v; want an update of %s", res, err, dupOppRef)
	}
	if got := dupName(t, pool, "sf_opportunity", dupOppBare); got != "Bare" {
		t.Errorf("bare opportunity name = %q, want unchanged", got)
	}
	var owner string
	if err := pool.QueryRow(ctx, `SELECT opportunity_id::text FROM sf_opportunity_product WHERE line_item_sf_id = $1`, dupLineItemSfID).Scan(&owner); err != nil || owner != dupOppRef {
		t.Errorf("line item owner = %q, %v; want %s", owner, err, dupOppRef)
	}
	if id, err := NewSalesforceOpportunityLookup(pool).LookupOpportunityIDBySfID(ctx, dupOppSfID); err != nil || id == nil || *id != dupOppRef {
		t.Errorf("opportunity lookup = %v, %v; want %s", id, err, dupOppRef)
	}

	opp := dupOppRef
	if created, err := NewSalesforceInvoiceRepository(pool).UpsertFromSalesforce(ctx, domain.SalesforceInvoiceUpsert{SfID: dupInvoiceSfID, Name: &name, OpportunityID: &opp},
		dupState(domain.SalesforceIngestEntityInvoice, dupInvoiceSfID)); err != nil || created {
		t.Fatalf("invoice upsert created=%v err=%v", created, err)
	}
	if got := dupName(t, pool, "sf_invoice", dupInvoiceRef); got != "From SF" {
		t.Errorf("invoice under the opportunity = %q, want From SF", got)
	}
	if got := dupName(t, pool, "sf_invoice", dupInvoiceBare); got != "Bare" {
		t.Errorf("other invoice copy = %q, want unchanged", got)
	}

	if id, err := NewSalesforceProjectRepository(NewScoped(pool)).LookupProjectIDBySfID(ctx, dupProjectSfID); err != nil || id == nil || *id != dupProjectRef {
		t.Errorf("project lookup = %v, %v; want %s", id, err, dupProjectRef)
	}
}
