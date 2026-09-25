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
	"github.com/jackc/pgx/v5/pgconn"
)

// ErrDuplicateOpenChat is returned by CreateWorkItem when c.CustomerEmail +
// c.ProjectID already has another, different-caseId chat_conversation row
// with session_ended_at IS NULL -- i.e. that customer already has a live
// chat in progress for this project. Enforced two ways: the plain SELECT
// check inside CreateWorkItem below (a fast, friendly rejection for the
// common case) and, as a race-safe backstop for two concurrent escalations
// arriving at once, the partial unique index added by migration
// 000021_split_case_and_conversation_identity
// (uq_chat_conversation_open_customer_project) -- a unique-violation on
// that index is translated to this same error (see the pgconn.PgError
// check below), so a caller never has to tell the two enforcement paths
// apart.
//
// Never returned for a retried CreateWorkItem call for the SAME caseId --
// that is idempotency (see the early case_id lookup below), not a
// duplicate.
var ErrDuplicateOpenChat = errors.New("this customer already has an open live chat for this project")

// uqOpenCustomerProjectConstraint is the partial unique index's name --
// see migration 000021_split_case_and_conversation_identity.
const uqOpenCustomerProjectConstraint = "uq_chat_conversation_open_customer_project"

// isUniqueViolation reports whether err is a Postgres unique_violation
// (SQLSTATE 23505) on the named constraint/index.
func isUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == constraint
}

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
	// ConversationID is the stable Novera AI-chat conversation this case
	// came from -- see migration 000021_split_case_and_conversation_identity
	// and CaseInfo.ConversationID's own doc comment. Distinct from CaseID
	// since a single conversation can produce more than one case over its
	// lifetime (one per escalation).
	ConversationID string `json:"conversationId"`
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
// in state OPEN, unassigned) for a brand-new escalation, plus its prior
// AI-chatbot messages and first comment if c.Message is non-empty. Also
// stores c itself as chat_conversation.case_info -- the durable display
// blob (subject, customer email/name, message) GetPresence/DebugState read
// back for as long as this conversation is held by an engineer, including
// after Accept (unlike chat_queue's own case_info, which Accept deletes).
// That blob's own copy of PriorMessages is never trusted on read, though --
// see commentsForCase, which every read path uses instead, so the
// chat_routing.comment rows inserted below are the durable source of truth.
//
// Called once per case, BEFORE Router.Escalate -- unlike the single-case
// model this replaced, Escalate's own assignment now writes
// chat_conversation.assignee_id directly (see assignCaseToEngineer), so
// this row must already exist by the time Escalate runs. This mirrors how
// the real entity-service case this stand-in mimics already exists before
// csm-portal/backend's HandleEscalate is even called.
//
// Idempotent by case_id: escalation retries (a network hiccup, backend-v2
// or csm-portal/backend re-sending the same request) are expected, and
// must not duplicate the prior-message transcript inserted below. If
// chat_conversation already has a row for c.CaseID, this returns nil
// immediately without touching work_item/chat_conversation/comment at all
// -- never by deleting and reinserting anything. The existence check and
// every insert below share one transaction, so a genuinely concurrent
// double call still can't duplicate anything: whichever call loses the
// race gets a unique-constraint error on the chat_conversation insert
// (case_id has a UNIQUE INDEX) and its whole transaction rolls back.
//
// Also enforces at most one non-ended live chat per (c.CustomerEmail,
// c.ProjectID): a brand-new caseId (different from any existing row's)
// for a customer/project that already has an open (session_ended_at IS
// NULL) chat_conversation row is rejected with ErrDuplicateOpenChat rather
// than creating a second, competing escalation -- see that error's own doc
// comment for the two layers this is enforced at. A caseId that IS an
// existing row's own (the idempotent-retry case above) never reaches this
// check at all, and a customer whose previous chat has since ended or
// converted (session_ended_at set) is never blocked by it.
func (r *Router) CreateWorkItem(ctx context.Context, c CaseInfo) error {
	caseInfoJSON, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshal case info: %w", err)
	}

	return r.withTx(ctx, func(tx pgx.Tx) error {
		var existingWorkItemID string
		err := tx.QueryRow(ctx, `
			SELECT work_item_id FROM chat_conversation WHERE case_id = $1
		`, c.CaseID).Scan(&existingWorkItemID)
		switch {
		case err == nil:
			// Already created by an earlier call -- idempotent no-op,
			// including PriorMessages.
			return nil
		case !errors.Is(err, pgx.ErrNoRows):
			return fmt.Errorf("check existing chat_conversation: %w", err)
		}

		// Application-level duplicate-open-chat check -- see this method's
		// own doc comment and ErrDuplicateOpenChat. c.CustomerEmail/
		// c.ProjectID are required for this to mean anything; a request
		// missing either is let through here (nothing to compare against)
		// but is exactly the kind of malformed record the migration's
		// COALESCE-to-'' indexing exists to still catch at the database
		// layer as a defensive backstop.
		if c.CustomerEmail != "" && c.ProjectID != "" {
			var exists bool
			if err := tx.QueryRow(ctx, `
				SELECT EXISTS (
					SELECT 1 FROM chat_conversation
					WHERE case_id != $1
					  AND session_ended_at IS NULL
					  AND case_info ->> 'customerEmail' = $2
					  AND case_info ->> 'projectId' = $3
				)
			`, c.CaseID, c.CustomerEmail, c.ProjectID).Scan(&exists); err != nil {
				return fmt.Errorf("check duplicate open chat: %w", err)
			}
			if exists {
				return fmt.Errorf("%w: customerEmail=%s projectId=%s", ErrDuplicateOpenChat, c.CustomerEmail, c.ProjectID)
			}
		}

		var workItemID string
		if err := tx.QueryRow(ctx, `
			INSERT INTO work_item (creator_id, subject) VALUES ($1, $2)
			RETURNING id
		`, c.CustomerEmail, c.Subject).Scan(&workItemID); err != nil {
			return fmt.Errorf("insert work_item: %w", err)
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO chat_conversation (work_item_id, case_id, conversation_id, case_info)
			VALUES ($1, $2, $3, $4::jsonb)
		`, workItemID, c.CaseID, c.ConversationID, caseInfoJSON); err != nil {
			if isUniqueViolation(err, uqOpenCustomerProjectConstraint) {
				return fmt.Errorf("%w: customerEmail=%s projectId=%s", ErrDuplicateOpenChat, c.CustomerEmail, c.ProjectID)
			}
			return fmt.Errorf("insert chat_conversation: %w", err)
		}

		// Prior AI-chatbot (Novera) messages, inserted first and each with
		// its own real created_at (parsed from CaseInfo.PriorMessages,
		// falling back to now() for an empty/malformed timestamp rather
		// than failing the whole escalation over it) so the transcript --
		// ordered by created_at everywhere it's read back (DebugWorkItem,
		// commentsForCase, AddComment) -- reads in the order these
		// actually happened, ahead of the triggering message below (which
		// keeps the column's default now()). Role maps to created_by the
		// same way commentsForCase maps it back: Assistant ->
		// priorMessageAssistantAuthor, Customer -> the customer's own email
		// (already on hand as c.CustomerEmail, matching AddComment's own
		// convention for a live customer message).
		for _, m := range c.PriorMessages {
			createdBy := c.CustomerEmail
			if m.Role == PriorMessageRoleAssistant {
				createdBy = priorMessageAssistantAuthor
			}
			createdAt := time.Now()
			if t, parseErr := time.Parse(time.RFC3339, m.CreatedAt); parseErr == nil {
				createdAt = t
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO comment (work_item_id, content, created_by, created_at)
				VALUES ($1, $2, $3, $4)
			`, workItemID, m.Content, createdBy, createdAt); err != nil {
				return fmt.Errorf("insert prior comment: %w", err)
			}
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
//
// Also rejects a comment on a case whose session_ended_at is already set
// (ErrConversationEnded), instead of silently inserting it -- this used to
// succeed unconditionally, which was the direct cause of a live bug: after
// an engineer clicked "End session", the case vanished from their own
// view, but the customer's page kept accepting and "relaying" messages
// with no error at all (see csm-portal/backend's HandleCustomerMessage,
// which previously treated this call as pure best-effort and always
// reported success regardless of the outcome here).
func (r *Router) AddComment(ctx context.Context, caseID, authorEmail, content string) error {
	return r.withTx(ctx, func(tx pgx.Tx) error {
		var (
			workItemID     string
			sessionEndedAt *time.Time
		)
		err := tx.QueryRow(ctx, `
			SELECT work_item_id, session_ended_at FROM chat_conversation WHERE case_id = $1
		`, caseID).Scan(&workItemID, &sessionEndedAt)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return fmt.Errorf("add comment: no chat_conversation for case %s", caseID)
		case err != nil:
			return fmt.Errorf("add comment: look up work item: %w", err)
		}
		if sessionEndedAt != nil {
			return fmt.Errorf("%w: case_id=%s", ErrConversationEnded, caseID)
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
// already has. PriorMessages is NOT read from that stored blob -- it's
// replaced with a fresh read of chat_routing.comment (see commentsForCase),
// so what a caller gets here always matches what's actually durably
// persisted, including anything AddComment has added since.
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
	priorMessages, err := commentsForCase(ctx, r.db, caseID)
	if err != nil {
		return CaseInfo{}, fmt.Errorf("get case info: %w", err)
	}
	c.PriorMessages = priorMessages
	return c, nil
}

// commentsForCase loads caseID's chat_routing.comment rows as PriorMessage,
// oldest first -- the durable source of truth for what a case's
// PriorMessages should show to an engineer, resolved via
// chat_conversation.work_item_id. Used by every path that returns a
// CaseInfo/CaseStatus destined for csm-portal/backend's assignedCaseEvent
// or GetPresence rehydration (GetCaseInfo above, and claimOldestWaiting/
// lockPendingConversation/engineerCases in state.go), instead of trusting
// whatever CreateWorkItem/Escalate originally snapshotted into the
// case_info JSONB blob -- see CaseInfo.PriorMessages's own doc comment.
// Also reads back case_info for its own customerEmail, needed to tell a
// live engineer reply apart from the customer's own live message -- see
// commentsForWorkItem's own doc comment on why that distinction matters.
func commentsForCase(ctx context.Context, q pgxQuerier, caseID string) ([]PriorMessage, error) {
	var (
		workItemID   string
		caseInfoJSON []byte
	)
	err := q.QueryRow(ctx, `SELECT work_item_id, case_info FROM chat_conversation WHERE case_id = $1`, caseID).Scan(&workItemID, &caseInfoJSON)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("comments for case: resolve work item: %w", err)
	}
	var customerEmail string
	if caseInfoJSON != nil {
		var c CaseInfo
		if err := json.Unmarshal(caseInfoJSON, &c); err == nil {
			customerEmail = c.CustomerEmail
		}
	}
	return commentsForWorkItem(ctx, q, workItemID, customerEmail)
}

// commentsForWorkItem is commentsForCase's query, factored out for callers
// that already have workItemID (and customerEmail, off the same decoded
// CaseInfo) on hand -- currently just engineerCases in state.go.
//
// created_by maps back to Role the same way CreateWorkItem maps Role to
// created_by on insert, PLUS a case CreateWorkItem never has to handle:
// priorMessageAssistantAuthor -> Assistant; customerEmail (when known and
// it matches) -> Customer; anything else -> Engineer. That third case is
// exactly AddComment's other caller, HandleEngineerMessage in csm-portal/
// backend -- once a case is accepted, the live conversation (customer
// messages AND engineer replies) is appended to this SAME comment table via
// AddComment, so this read path has to tell those two apart even though
// CreateWorkItem's own write path never needs to. A row is only ever
// classified Engineer when customerEmail is non-empty and genuinely
// doesn't match -- an empty/unresolved customerEmail (shouldn't normally
// happen, but see CreateWorkItem's own defensive comments on a malformed
// record) falls back to Customer rather than mislabeling everything as
// Engineer.
func commentsForWorkItem(ctx context.Context, q pgxQuerier, workItemID, customerEmail string) ([]PriorMessage, error) {
	rows, err := q.Query(ctx, `
		SELECT content, created_by, created_at FROM comment
		WHERE work_item_id = $1 ORDER BY created_at ASC
	`, workItemID)
	if err != nil {
		return nil, fmt.Errorf("comments for work item: query: %w", err)
	}
	defer rows.Close()

	var msgs []PriorMessage
	for rows.Next() {
		var (
			content, createdBy string
			createdAt          time.Time
		)
		if err := rows.Scan(&content, &createdBy, &createdAt); err != nil {
			return nil, fmt.Errorf("comments for work item: scan: %w", err)
		}
		role := PriorMessageRoleCustomer
		switch {
		case createdBy == priorMessageAssistantAuthor:
			role = PriorMessageRoleAssistant
		case customerEmail != "" && createdBy != customerEmail:
			role = PriorMessageRoleEngineer
		}
		msgs = append(msgs, PriorMessage{
			Role: role, Content: content, CreatedAt: createdAt.Format(time.RFC3339),
		})
	}
	return msgs, rows.Err()
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
		       c.work_item_id, c.case_id, c.conversation_id, c.assignee_id, c.state
		FROM chat_conversation c
		JOIN work_item w ON w.id = c.work_item_id
		WHERE c.case_id = $1
	`, caseID).Scan(
		&detail.WorkItem.ID, &detail.WorkItem.CreatorID, &detail.WorkItem.Subject, &detail.WorkItem.WorkItemNumber,
		&detail.Conversation.WorkItemID, &detail.Conversation.CaseID, &detail.Conversation.ConversationID, &assigneeID, &detail.Conversation.State,
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
