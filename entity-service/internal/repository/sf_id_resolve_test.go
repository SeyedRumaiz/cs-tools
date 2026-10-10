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
	"bytes"
	"context"
	"log/slog"
	"regexp"
	"strings"
	"testing"
)

// captureWarnings routes slog to a buffer for the test.
func captureWarnings(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func TestWarnDuplicateSfID_OnlyForMoreThanOneRow(t *testing.T) {
	buf := captureWarnings(t)
	warnDuplicateSfID(context.Background(), "account", "001A", "row-1", 1)
	warnDuplicateSfID(context.Background(), "account", " ", "row-1", 5)
	if buf.Len() != 0 {
		t.Fatalf("logged %q for a single row or a blank sf_id", buf.String())
	}
	warnDuplicateSfID(context.Background(), "account", "001A", "row-1", 3)
	if got := buf.String(); !strings.Contains(got, "level=WARN") || !strings.Contains(got, "rows=3") ||
		!strings.Contains(got, "table=account") || !strings.Contains(got, "chosenId=row-1") {
		t.Errorf("warning = %q", got)
	}
}

// One write logs once, from the same statement that resolves the row.
func TestUpdateOneBySfID(t *testing.T) {
	buf := captureWarnings(t)
	q := &updateTagQuerier{update: updateSfInvoiceQuery, rows: 2}
	id, rows, err := updateOneBySfID(context.Background(), q, updateSfInvoiceQuery, "sf_invoice", "a0I1")
	if err != nil || id != "row" || rows != 2 || len(q.statements) != 1 {
		t.Fatalf("id=%q rows=%d err=%v statements=%d", id, rows, err, len(q.statements))
	}
	if strings.Count(buf.String(), "level=WARN") != 1 {
		t.Errorf("warnings = %q, want one", buf.String())
	}
	q = &updateTagQuerier{update: updateSfInvoiceQuery}
	if id, rows, err := updateOneBySfID(context.Background(), q, updateSfInvoiceQuery, "sf_invoice", "a0I1"); err != nil || id != "" || rows != 0 {
		t.Errorf("no row: id=%q rows=%d err=%v", id, rows, err)
	}
}

func TestResolveIDBySfID_NoRowIsNil(t *testing.T) {
	q := &updateTagQuerier{update: resolveAccountBySfIDQuery}
	if id, err := resolveIDBySfID(context.Background(), q, resolveAccountBySfIDQuery, "account", "001A"); err != nil || id != nil {
		t.Errorf("id=%v err=%v, want nil, nil", id, err)
	}
	q.rows = 1
	if id, err := resolveIDBySfID(context.Background(), q, resolveAccountBySfIDQuery, "account", "001A"); err != nil || id == nil || *id != "row" {
		t.Errorf("id=%v err=%v, want row", id, err)
	}
}

// Every ingest UPDATE writes the one resolved row (by primary key), not every copy.
func TestIngestUpdates_WriteOneRowByID(t *testing.T) {
	byID := regexp.MustCompile(`(?m)^\s*WHERE [a-z_]+\.id = t\.id\s*$`)
	for name, sql := range map[string]string{
		"account":     updateAccountBySfIDQuery,
		"opportunity": updateSfOpportunityQuery,
		"line item":   updateSfOpportunityProductQuery,
		"invoice":     updateSfInvoiceQuery,
		"link":        updateSfOpportunityLinkQuery,
	} {
		if !byID.MatchString(sql) || !strings.Contains(sql, "count(*) OVER () AS n") || !strings.Contains(sql, "LIMIT 1) t") {
			t.Errorf("%s update does not target one resolved row:\n%s", name, sql)
		}
	}
}

// The resolve orderings put a referenced copy first, then the oldest, then the lowest id.
func TestResolveOrders_ReferencedFirst(t *testing.T) {
	for name, tc := range map[string]struct {
		order    string
		children []string
		tail     string
	}{
		"account":     {accountReferencedOrder, []string{"work_item", "account_contact", "project", "sf_opportunity"}, ") DESC, a.created_on, a.id"},
		"project":     {projectReferencedOrder, []string{"work_item", "project_contact", "sf_opportunity_link"}, ") DESC, p.created_on, p.id"},
		"opportunity": {opportunityReferencedOrder, []string{"sf_invoice", "sf_opportunity_link", "sf_opportunity_product"}, ") DESC, o.created_on, o.id"},
	} {
		for _, c := range tc.children {
			if !strings.Contains(tc.order, "FROM "+c+" ") {
				t.Errorf("%s order ignores %s", name, c)
			}
		}
		if !strings.HasSuffix(tc.order, tc.tail) {
			t.Errorf("%s order does not end with %q", name, tc.tail)
		}
	}
	for _, sql := range []string{resolveAccountBySfIDQuery, resolveProjectBySfIDQuery, resolveOpportunityBySfIDQuery, resolveSalesforceProjectQuery} {
		if !strings.Contains(sql, "OVER ()") || !strings.Contains(sql, "LIMIT 1") {
			t.Errorf("resolve query lacks the copy count or LIMIT 1:\n%s", sql)
		}
	}
}
