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
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5"
)

// sf_id is not unique (migration 0095), so writes and lookups resolve ONE row: the copy with
// the most kinds of child first, then oldest, then lowest id (CLAUDE.md, "One row per sf_id").

// accountReferencedOrder ranks account rows a by how many kinds of child point at them.
const accountReferencedOrder = `(EXISTS (SELECT 1 FROM work_item w WHERE w.account_id = a.id)::int
		+ EXISTS (SELECT 1 FROM account_contact ac WHERE ac.account_id = a.id)::int
		+ EXISTS (SELECT 1 FROM project p WHERE p.account_id = a.id)::int
		+ EXISTS (SELECT 1 FROM sf_opportunity o WHERE o.account_id = a.id)::int) DESC, a.created_on, a.id`

// projectReferencedOrder ranks project rows p by how many kinds of child point at them.
const projectReferencedOrder = `(EXISTS (SELECT 1 FROM work_item w WHERE w.project_id = p.id)::int
		+ EXISTS (SELECT 1 FROM project_contact pc WHERE pc.project_id = p.id)::int
		+ EXISTS (SELECT 1 FROM sf_opportunity_link l WHERE l.project_id = p.id)::int) DESC, p.created_on, p.id`

// opportunityReferencedOrder ranks sf_opportunity rows o by how many kinds of child point at them.
const opportunityReferencedOrder = `(EXISTS (SELECT 1 FROM sf_invoice i WHERE i.opportunity_id = o.id)::int
		+ EXISTS (SELECT 1 FROM sf_opportunity_link l WHERE l.opportunity_id = o.id)::int
		+ EXISTS (SELECT 1 FROM sf_opportunity_product li WHERE li.opportunity_id = o.id)::int) DESC, o.created_on, o.id`

// resolveAccountBySfIDQuery is the account row an sf_id stands for.
const resolveAccountBySfIDQuery = `
	SELECT a.id::text, count(*) OVER ()
	FROM account a
	WHERE a.sf_id = $1
	ORDER BY ` + accountReferencedOrder + `
	LIMIT 1`

// resolveProjectBySfIDQuery is the project row an sf_id stands for.
const resolveProjectBySfIDQuery = `
	SELECT p.id::text, count(*) OVER ()
	FROM project p
	WHERE p.sf_id = $1
	ORDER BY ` + projectReferencedOrder + `
	LIMIT 1`

// resolveOpportunityBySfIDQuery is the sf_opportunity row an sf_id stands for.
const resolveOpportunityBySfIDQuery = `
	SELECT o.id::text, count(*) OVER ()
	FROM sf_opportunity o
	WHERE o.sf_id = $1
	ORDER BY ` + opportunityReferencedOrder + `
	LIMIT 1`

// warnDuplicateSfID logs when rows (count(*) OVER () of a resolve) shows more
// than one row carrying sfID.
func warnDuplicateSfID(ctx context.Context, table, sfID, chosenID string, rows int64) {
	if rows > 1 && strings.TrimSpace(sfID) != "" {
		slog.WarnContext(ctx, "salesforce: more than one row carries this Salesforce id; using one",
			"table", table, "sfId", sfID, "rows", rows, "chosenId", chosenID)
	}
}

// resolveIDBySfID runs a resolve query above: the chosen id, or nil when no
// row carries sfID.
func resolveIDBySfID(ctx context.Context, q querier, query, table, sfID string) (*string, error) {
	var id string
	var rows int64
	err := q.QueryRow(ctx, query, sfID).Scan(&id, &rows)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("resolve %s by sf_id: %w", table, err)
	}
	warnDuplicateSfID(ctx, table, sfID, id, rows)
	return &id, nil
}

// updateOneBySfID runs an UPDATE ... FROM (resolve) ... RETURNING id, rows: the
// updated id and how many rows carry sfID (0 when none, and nothing updated).
func updateOneBySfID(ctx context.Context, q querier, query, table, sfID string, args ...any) (id string, rows int64, err error) {
	err = q.QueryRow(ctx, query, args...).Scan(&id, &rows)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", 0, nil
	}
	if err != nil {
		return "", 0, err
	}
	warnDuplicateSfID(ctx, table, sfID, id, rows)
	return id, rows, nil
}
