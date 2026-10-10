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

// These run the invoice and standalone line-item writes against a live
// PostgreSQL instance: parameter types, the by-sf_id update, the ledger row
// in the same transaction and the delete. Skipped unless
// ENTITY_TEST_DATABASE_URL is set (see
// account_repo_salesforce_integration_test.go for the setup).

const (
	ociOppRow     = "5fb00000-0000-4000-8000-000000000001"
	ociOppSfID    = "006OCITEST0000001A"
	ociInvoiceSfI = "a0IOCITEST0000001A"
	ociLineSfID   = "00kOCITEST0000001A"
)

func newOpportunityChildIntegrationPool(t *testing.T) *pgxpool.Pool {
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
			`DELETE FROM sf_invoice WHERE sf_id = '` + ociInvoiceSfI + `'`,
			`DELETE FROM sf_opportunity_product WHERE line_item_sf_id = '` + ociLineSfID + `'`,
			`DELETE FROM sf_opportunity WHERE id = '` + ociOppRow + `'`,
			`DELETE FROM salesforce_ingest_state WHERE sf_id LIKE '%OCITEST%'`,
		} {
			if _, err := pool.Exec(ctx, stmt); err != nil {
				t.Fatalf("clean: %v", err)
			}
		}
	}
	clean()
	t.Cleanup(clean)
	if _, err := pool.Exec(ctx, `INSERT INTO sf_opportunity (id, created_on, updated_on, created_by, updated_by, sf_id, name)
		VALUES ($1, now(), now(), 't', 't', $2, 'OCI Opp')`, ociOppRow, ociOppSfID); err != nil {
		t.Fatalf("seed opportunity: %v", err)
	}
	return pool
}

func ociState(entity, sfID, eventType string) domain.UpsertSalesforceIngestStateRequest {
	return domain.UpsertSalesforceIngestStateRequest{Entity: entity, SfID: sfID, EventModifiedOn: time.Now().UTC(),
		EventType: eventType, Status: domain.SalesforceIngestSucceeded}
}

func TestSfInvoiceIntegration_UpsertAndDelete(t *testing.T) {
	pool := newOpportunityChildIntegrationPool(t)
	ctx := context.Background()
	repo := NewSalesforceInvoiceRepository(pool)
	opp := ociOppRow
	amount := 1500.25
	due := time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)
	row := domain.SalesforceInvoiceUpsert{
		SfID: ociInvoiceSfI, Name: ociPtr("2508"), Description: ociPtr("CSM Sync Test"), Classification: ociPtr("Subscription"),
		OpportunityID: &opp, InvoicedAmount: &amount, InvoicedDueDate: &due, OriginalInvoiceDueDate: &due,
	}
	created, err := repo.UpsertFromSalesforce(ctx, row, ociState(domain.SalesforceIngestEntityInvoice, ociInvoiceSfI, "CREATED"))
	if err != nil || !created {
		t.Fatalf("first upsert created=%v err=%v", created, err)
	}
	row.Description = ociPtr("CSM Sync Test (edited)")
	row.OpportunityID = nil
	created, err = repo.UpsertFromSalesforce(ctx, row, ociState(domain.SalesforceIngestEntityInvoice, ociInvoiceSfI, "UPDATED"))
	if err != nil || created {
		t.Fatalf("second upsert created=%v err=%v, want an update", created, err)
	}
	var n int
	var desc string
	var gotAmount float64
	var oppID *string
	if err := pool.QueryRow(ctx, `SELECT count(*) OVER (), description, invoiced_amount::float8, opportunity_id::text FROM sf_invoice WHERE sf_id = $1`, ociInvoiceSfI).
		Scan(&n, &desc, &gotAmount, &oppID); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if n != 1 || desc != "CSM Sync Test (edited)" || gotAmount != 1500.25 || oppID != nil {
		t.Errorf("rows=%d description=%q amount=%v opportunity=%v", n, desc, gotAmount, oppID)
	}
	deleted, err := repo.DeleteBySfID(ctx, ociInvoiceSfI, ociState(domain.SalesforceIngestEntityInvoice, ociInvoiceSfI, "DELETED"))
	if err != nil || deleted != 1 {
		t.Fatalf("delete n=%d err=%v", deleted, err)
	}
	var eventType string
	if err := pool.QueryRow(ctx, `SELECT event_type FROM salesforce_ingest_state WHERE entity = 'invoice' AND sf_id = $1`, ociInvoiceSfI).Scan(&eventType); err != nil || eventType != "DELETED" {
		t.Errorf("ledger event_type = %q err = %v", eventType, err)
	}
}

// The standalone line item write leaves the two ServiceNow-side columns
// alone and deletes by line_item_sf_id.
func TestSfOpportunityLineItemIntegration_UpsertKeepsServiceNowColumnsAndDeletes(t *testing.T) {
	pool := newOpportunityChildIntegrationPool(t)
	ctx := context.Background()
	repo := NewSalesforceOpportunityLineItemRepository(pool)
	qty, price := 3.0, 1200.5
	li := domain.SalesforceOpportunityLineItemUpsert{LineItemSfID: ociLineSfID, Name: ociPtr("Line"), ProductName: ociPtr("Development Support - 40 hours"),
		Quantity: &qty, TotalPrice: &price}
	state := func(et string) domain.UpsertSalesforceIngestStateRequest {
		return ociState(domain.SalesforceIngestEntityOpportunityLineItem, ociLineSfID, et)
	}
	if created, err := repo.UpsertFromSalesforce(ctx, ociOppSfID, ociOppRow, li, state("CREATED")); err != nil || !created {
		t.Fatalf("insert created=%v err=%v", created, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE sf_opportunity_product SET development_support_hours = 40, engagement_code = 'ENG-7' WHERE line_item_sf_id = $1`, ociLineSfID); err != nil {
		t.Fatalf("set ServiceNow columns: %v", err)
	}
	qty = 5
	if created, err := repo.UpsertFromSalesforce(ctx, ociOppSfID, ociOppRow, li, state("UPDATED")); err != nil || created {
		t.Fatalf("update created=%v err=%v", created, err)
	}
	var n int
	var gotQty float64
	var hours *float64
	var eng *string
	if err := pool.QueryRow(ctx, `SELECT count(*) OVER (), quantity::float8, development_support_hours::float8, engagement_code
		FROM sf_opportunity_product WHERE line_item_sf_id = $1 AND opportunity_id = $2`, ociLineSfID, ociOppRow).Scan(&n, &gotQty, &hours, &eng); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if n != 1 || gotQty != 5 || hours == nil || *hours != 40 || eng == nil || *eng != "ENG-7" {
		t.Errorf("rows=%d quantity=%v hours=%v engagement=%v", n, gotQty, hours, eng)
	}
	if deleted, err := repo.DeleteByLineItemSfID(ctx, ociLineSfID, state("DELETED")); err != nil || deleted != 1 {
		t.Fatalf("delete n=%d err=%v", deleted, err)
	}
	if deleted, err := repo.DeleteByLineItemSfID(ctx, ociLineSfID, state("DELETED")); err != nil || deleted != 0 {
		t.Fatalf("second delete n=%d err=%v, want 0", deleted, err)
	}
}

func ociPtr(s string) *string { return &s }
