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
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
)

// partnerQuerier records replaceAccountPartners' statements; every Exec
// affects one row unless failOn names the statement.
type partnerQuerier struct {
	statements []string
	args       [][]any
	failOn     string
}

func (q *partnerQuerier) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	q.statements = append(q.statements, sql)
	q.args = append(q.args, args)
	if q.failOn != "" && sql == q.failOn {
		return pgconn.CommandTag{}, errors.New("boom")
	}
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}

func (q *partnerQuerier) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, fmt.Errorf("unexpected Query")
}

func (q *partnerQuerier) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	q.statements = append(q.statements, sql)
	q.args = append(q.args, args)
	return oppRow{}
}

func partnerState() domain.UpsertSalesforceIngestStateRequest {
	return domain.UpsertSalesforceIngestStateRequest{
		Entity: domain.SalesforceIngestEntityAccountPartners, SfID: "001C", EventModifiedOn: time.Now().UTC(),
		EventType: domain.SalesforceEventUpdated, Status: domain.SalesforceIngestSucceeded,
	}
}

// One lock on the customer, the four set statements in order, then the
// ledger row — all on the one transaction.
func TestReplaceAccountPartners_StatementOrder(t *testing.T) {
	q := &partnerQuerier{}
	added, removed, err := replaceAccountPartners(context.Background(), q, "001C", "cust-row", nil, partnerState())
	if err != nil {
		t.Fatalf("replaceAccountPartners: %v", err)
	}
	if added != 2 || removed != 2 {
		t.Errorf("added=%d removed=%d, want 2/2 from the fake's one row per statement", added, removed)
	}
	want := []string{"pg_advisory_xact_lock", insertForwardPartnerLinksQuery, insertReversePartnerLinksQuery,
		deleteStaleForwardPartnerLinksQuery, deleteStaleReversePartnerLinksQuery, "salesforce_ingest_state"}
	if len(q.statements) != len(want) {
		t.Fatalf("statements = %d, want %d", len(q.statements), len(want))
	}
	for i, w := range want {
		if !strings.Contains(q.statements[i], w) {
			t.Errorf("statement %d = %.60q, want it to contain %.40q", i, q.statements[i], w)
		}
	}
	if q.args[0][0] != "account-partners:001C" {
		t.Errorf("lock key = %v", q.args[0][0])
	}
	// A nil set is sent as an empty array, so the deletes remove every
	// partner row rather than comparing against NULL.
	if ids, ok := q.args[3][1].([]string); !ok || ids == nil {
		t.Errorf("delete partner ids = %#v, want an empty non-nil slice", q.args[3][1])
	}
}

// Every statement is pinned to the partner labels, so no other relationship
// of the customer can be inserted or deleted.
func TestReplaceAccountPartners_OnlyPartnerLabels(t *testing.T) {
	for name, sql := range map[string]string{
		"insert forward": insertForwardPartnerLinksQuery,
		"insert reverse": insertReversePartnerLinksQuery,
		"delete forward": deleteStaleForwardPartnerLinksQuery,
		"delete reverse": deleteStaleReversePartnerLinksQuery,
	} {
		if !strings.Contains(sql, "relationship_label = $") || !strings.Contains(sql, "is_reverse_relationship = ") {
			t.Errorf("%s does not pin the label and direction:\n%s", name, sql)
		}
	}
	q := &partnerQuerier{}
	if _, _, err := replaceAccountPartners(context.Background(), q, "001C", "cust-row", []string{"p"}, partnerState()); err != nil {
		t.Fatal(err)
	}
	if q.args[1][3] != relationshipLabelPartnerOf || q.args[1][4] != relationshipLabelCustomerOf ||
		q.args[3][2] != relationshipLabelPartnerOf || q.args[4][2] != relationshipLabelCustomerOf {
		t.Errorf("label arguments = %v %v %v", q.args[1], q.args[3], q.args[4])
	}
}

func TestReplaceAccountPartners_FailureStopsBeforeTheLedger(t *testing.T) {
	q := &partnerQuerier{failOn: deleteStaleForwardPartnerLinksQuery}
	if _, _, err := replaceAccountPartners(context.Background(), q, "001C", "cust-row", []string{"p"}, partnerState()); err == nil {
		t.Fatal("err = nil, want the delete failure")
	}
	for _, s := range q.statements {
		if strings.Contains(s, "salesforce_ingest_state") {
			t.Error("ledger written after a failed statement")
		}
	}
}
