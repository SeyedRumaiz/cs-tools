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
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

package repository

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// inTxReturning runs fn inside one transaction against db, committing on
// success and rolling back otherwise -- the plain-*pgxpool.Pool equivalent
// of Scoped's InTxReturning (scoped.go), for a resource with no row-level
// security (so there is no caller identity to stamp as session GUCs before
// the transaction starts).
func inTxReturning[T any](ctx context.Context, db *pgxpool.Pool, fn func(tx pgx.Tx) (T, error)) (T, error) {
	var zero T
	tx, err := db.Begin(ctx)
	if err != nil {
		return zero, fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op once Commit has succeeded

	result, err := fn(tx)
	if err != nil {
		return zero, err
	}
	if err := tx.Commit(ctx); err != nil {
		return zero, fmt.Errorf("commit transaction: %w", err)
	}
	return result, nil
}

// This package already has the two interfaces a read helper needs to run
// either inside an existing transaction or directly against the pool --
// rowQuerier (outage_repo.go, QueryRow) and rowsQuerier (case_repo.go,
// Query), both satisfied structurally by pgx.Tx and *pgxpool.Pool alike.
// Reused here rather than redeclared under a third name.
