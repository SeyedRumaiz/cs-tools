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

// This file is a local stand-in for entity-service's generic work-item
// schema (work_item / chat_conversation / comment), reproduced here so
// this feature's message and engineer-assignment persistence can be built
// and tested before that real schema exists. Once entity-service ships its
// own version, csm-portal/backend's calls should move there instead, and
// this file (plus its migration) should be deleted.
//
// These tables live in this service's own "chat_routing" schema rather
// than entity-service's, so every query below uses plain unqualified table
// names resolved via this service's own search_path. Keeping them here
// also means entity-service's eventual real migration can't collide with
// this stand-in on table names.
package router

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// WorkItem mirrors the interim work_item row.
type WorkItem struct {
	ID             string `json:"id"`
	CreatorID      string `json:"creatorId"`
	Subject        string `json:"subject"`
	WorkItemNumber string `json:"workItemNumber,omitempty"`
}

// ChatConversation mirrors the interim chat_conversation row. CaseID isn't
// part of entity-service's eventual real schema -- it's here because every
// caller of AddComment/Accept looks a conversation up by it.
type ChatConversation struct {
	WorkItemID string `json:"workItemId"`
	CaseID     string `json:"caseId"`
	// AssigneeID is nil until this conversation is assigned to an engineer
	// (see internal/router/state.go's assignCaseToEngineer) -- set at
	// assignment time, not at Accept, so a pending-but-unconfirmed
	// conversation is distinguishable from a still-queued one.
	AssigneeID *string `json:"assigneeId,omitempty"`
	// State is this conversation's lifecycle stage: OPEN until Router.
	// Accept moves it to ACTIVE. The database enum also has RESOLVED/
	// CONVERTED_CHAT/CONVERTED_CASE/ABANDONED/CLOSED, but nothing sets
	// those yet.
	State string `json:"state"`
}

// Comment mirrors one row of the shared comment table.
type Comment struct {
	ID        string `json:"id"`
	Content   string `json:"content"`
	CreatedBy string `json:"createdBy"`
	CreatedAt string `json:"createdAt"`
}

// WorkItemDetail is DebugWorkItem's result.
type WorkItemDetail struct {
	WorkItem     WorkItem         `json:"workItem"`
	Conversation ChatConversation `json:"conversation"`
	// Comments is every message so far, oldest first -- the full transcript.
	Comments []Comment `json:"comments"`
}

// CreateWorkItem creates the work_item + chat_conversation pair (starting
// in state OPEN, unassigned) for a brand-new escalation, plus its first
// comment if c.Message is non-empty. Also stores c itself as
// chat_conversation.case_info -- the durable display blob (subject,
// customer email/name, message) GetPresence/DebugState read back for as
// long as this conversation is held by an engineer, including after
// Accept (unlike chat_queue's own case_info, which Accept deletes).
//
// Called once per case, BEFORE Router.Escalate -- unlike the single-case
// model this replaced, Escalate's own assignment now writes
// chat_conversation.assignee_id directly (see assignCaseToEngineer), so
// this row must already exist by the time Escalate runs. This mirrors how
// the real entity-service case this stand-in mimics already exists before
// csm-portal/backend's HandleEscalate is even called. This is a create,
// not an upsert: calling it twice for the same c.CaseID is a caller bug
// this doesn't try to reconcile.
func (r *Router) CreateWorkItem(ctx context.Context, c CaseInfo) error {
	caseInfoJSON, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshal case info: %w", err)
	}

	return r.withTx(ctx, func(tx pgx.Tx) error {
		var workItemID string
		if err := tx.QueryRow(ctx, `
			INSERT INTO work_item (creator_id, subject) VALUES ($1, $2)
			RETURNING id
		`, c.CustomerEmail, c.Subject).Scan(&workItemID); err != nil {
			return fmt.Errorf("insert work_item: %w", err)
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO chat_conversation (work_item_id, case_id, case_info)
			VALUES ($1, $2, $3::jsonb)
		`, workItemID, c.CaseID, caseInfoJSON); err != nil {
			return fmt.Errorf("insert chat_conversation: %w", err)
		}

		if c.Message != "" {
			if _, err := tx.Exec(ctx, `
				INSERT INTO comment (work_item_id, content, created_by)
				VALUES ($1, $2, $3)
			`, workItemID, c.Message, c.CustomerEmail); err != nil {
				return fmt.Errorf("insert initial comment: %w", err)
			}
		}
		return nil
	})
}

// AddComment appends one message to caseID's transcript, looking up its
// work_item via chat_conversation.case_id. Used for both directions of the
// live chat so the whole transcript ends up in one place. Returns an error
// rather than a silent no-op if caseID has no chat_conversation row, since
// that means CreateWorkItem was never called for it -- worth surfacing
// even though current callers still treat this call as best-effort.
func (r *Router) AddComment(ctx context.Context, caseID, authorEmail, content string) error {
	return r.withTx(ctx, func(tx pgx.Tx) error {
		var workItemID string
		err := tx.QueryRow(ctx, `
			SELECT work_item_id FROM chat_conversation WHERE case_id = $1
		`, caseID).Scan(&workItemID)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return fmt.Errorf("add comment: no chat_conversation for case %s", caseID)
		case err != nil:
			return fmt.Errorf("add comment: look up work item: %w", err)
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO comment (work_item_id, content, created_by) VALUES ($1, $2, $3)
		`, workItemID, content, authorEmail); err != nil {
			return fmt.Errorf("insert comment: %w", err)
		}
		return nil
	})
}

// GetCaseInfo returns caseID's originally-submitted CaseInfo (subject,
// customer email/name, message, projectId) as CreateWorkItem stored it,
// so callers can build a case request without resending data this service
// already has.
func (r *Router) GetCaseInfo(ctx context.Context, caseID string) (CaseInfo, error) {
	var caseInfoJSON []byte
	err := r.db.QueryRow(ctx, `SELECT case_info FROM chat_conversation WHERE case_id = $1`, caseID).Scan(&caseInfoJSON)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return CaseInfo{}, fmt.Errorf("%w: case_id=%s", ErrConversationNotFound, caseID)
	case err != nil:
		return CaseInfo{}, fmt.Errorf("get case info: %w", err)
	}
	var c CaseInfo
	if caseInfoJSON != nil {
		if err := json.Unmarshal(caseInfoJSON, &c); err != nil {
			return CaseInfo{}, fmt.Errorf("get case info: decode: %w", err)
		}
	}
	return c, nil
}

// DebugWorkItem returns caseID's full work item, chat conversation, and
// comment transcript in order -- for verification/debugging only.
func (r *Router) DebugWorkItem(ctx context.Context, caseID string) (WorkItemDetail, error) {
	var (
		detail     WorkItemDetail
		assigneeID *string
	)
	err := r.db.QueryRow(ctx, `
		SELECT w.id, w.creator_id, w.subject, COALESCE(w.work_item_number, ''),
		       c.work_item_id, c.case_id, c.assignee_id, c.state
		FROM chat_conversation c
		JOIN work_item w ON w.id = c.work_item_id
		WHERE c.case_id = $1
	`, caseID).Scan(
		&detail.WorkItem.ID, &detail.WorkItem.CreatorID, &detail.WorkItem.Subject, &detail.WorkItem.WorkItemNumber,
		&detail.Conversation.WorkItemID, &detail.Conversation.CaseID, &assigneeID, &detail.Conversation.State,
	)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return WorkItemDetail{}, fmt.Errorf("no work item for case %s", caseID)
	case err != nil:
		return WorkItemDetail{}, fmt.Errorf("debug work item: %w", err)
	}
	detail.Conversation.AssigneeID = assigneeID

	rows, err := r.db.Query(ctx, `
		SELECT id, content, created_by, created_at FROM comment
		WHERE work_item_id = $1 ORDER BY created_at ASC
	`, detail.WorkItem.ID)
	if err != nil {
		return WorkItemDetail{}, fmt.Errorf("debug work item: query comments: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			c         Comment
			createdAt time.Time
		)
		if err := rows.Scan(&c.ID, &c.Content, &c.CreatedBy, &createdAt); err != nil {
			return WorkItemDetail{}, fmt.Errorf("debug work item: scan comment: %w", err)
		}
		c.CreatedAt = createdAt.Format(time.RFC3339)
		detail.Comments = append(detail.Comments, c)
	}
	return detail, rows.Err()
}
