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

// ErrDuplicateOpenChat is returned by CreateWorkItem when
// c.CustomerEmail/c.ProjectID already has another open (session_ended_at
// IS NULL) chat_conversation row under a different case_id — enforced both
// by a plain SELECT check and, as a race-safe backstop, by the
// uq_chat_conversation_open_customer_project partial unique index. Never
// returned for an idempotent retry of the same case_id.
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
// part of entity-service's eventual real schema — it's here because every
// caller of AddComment/Accept looks a conversation up by it.
type ChatConversation struct {
	WorkItemID string `json:"workItemId"`
	CaseID     string `json:"caseId"`
	// ConversationID is the stable Novera AI-chat conversation this case
	// came from — see migration 000021_split_case_and_conversation_identity
	// and CaseInfo.ConversationID's own doc comment. Distinct from CaseID
	// since a single conversation can produce more than one case over its
	// lifetime (one per escalation).
	ConversationID string `json:"conversationId"`
	// AssigneeID is nil until this conversation is assigned to an engineer
	// (see internal/router/state.go's assignCaseToEngineer) — set at
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
	// Comments is every message so far, oldest first — the full transcript.
	Comments []Comment `json:"comments"`
}

// CreateWorkItem creates the work_item and chat_conversation pair (state
// OPEN, unassigned) for a new escalation, along with c's prior AI-chatbot
// messages and first comment. It stores c as chat_conversation.case_info,
// the durable display blob read by GetPresence/DebugState.
//
// It is idempotent by c.CaseID: if a chat_conversation row already exists
// for that case, it returns nil without modifying anything. Concurrent
// calls for the same case are also safe — the loser gets a unique-
// constraint error and rolls back.
//
// It must be called before Router.Escalate, and returns ErrDuplicateOpenChat
// if c.CustomerEmail/c.ProjectID already has another open chat.
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
			// Already created by an earlier call — idempotent no-op,
			// including PriorMessages.
			return nil
		case !errors.Is(err, pgx.ErrNoRows):
			return fmt.Errorf("check existing chat_conversation: %w", err)
		}

		// Application-level duplicate-open-chat check — see this method's
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
		// commentsForCase, AddComment) — reads in the order these
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

// AddComment appends one message to caseID's transcript (looked up via
// chat_conversation.case_id) and bumps chat_conversation.updated_at so the
// message counts as activity for SweepStaleAcceptedSessions. Returns an
// error if caseID has no chat_conversation row, or ErrConversationEnded if
// its session has already ended, rather than silently no-op'ing.
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

		// Bumps updated_at so this counts as activity for SweepStaleAcceptedSessions.
		if _, err := tx.Exec(ctx, `
			UPDATE chat_conversation SET updated_at = now() WHERE case_id = $1
		`, caseID); err != nil {
			return fmt.Errorf("bump conversation activity: %w", err)
		}
		return nil
	})
}

// GetCaseInfo returns caseID's originally-submitted CaseInfo, as
// CreateWorkItem stored it, with PriorMessages replaced by a fresh read
// from chat_routing.comment (see commentsForCase) so it reflects anything
// AddComment has added since.
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
// oldest first, resolved via chat_conversation.work_item_id. It also reads
// case_info for customerEmail, needed to tell an engineer reply apart from
// the customer's own message (see commentsForWorkItem).
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
// that already have workItemID and customerEmail on hand (engineerCases in
// state.go). created_by maps back to Role: priorMessageAssistantAuthor ->
// Assistant, a match on customerEmail -> Customer, anything else ->
// Engineer (falls back to Customer when customerEmail is empty/unknown).
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
// comment transcript in order — for verification/debugging only.
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

// OpenChat is a customer's chat that has not ended yet.
type OpenChat struct {
	CaseID         string `json:"caseId"`
	ConversationID string `json:"conversationId"`
	// Accepted is true once the assigned engineer has accepted the chat.
	Accepted bool `json:"accepted"`
	// AssigneeID is the engineer the chat is assigned to, if any.
	AssigneeID string `json:"assigneeId,omitempty"`
}

// FindOpenChat returns customerEmail's open chat for projectID, using the
// same key as the duplicate-open-chat check. found is false when there is
// none.
func (r *Router) FindOpenChat(ctx context.Context, customerEmail, projectID string) (chat OpenChat, found bool, err error) {
	var acceptedAt *time.Time
	err = r.db.QueryRow(ctx, `
		SELECT case_id, conversation_id, accepted_at, COALESCE(assignee_id, '')
		FROM chat_conversation
		WHERE session_ended_at IS NULL
		  AND case_info ->> 'customerEmail' = $1
		  AND case_info ->> 'projectId' = $2
		ORDER BY created_at DESC
		LIMIT 1
	`, customerEmail, projectID).Scan(&chat.CaseID, &chat.ConversationID, &acceptedAt, &chat.AssigneeID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return OpenChat{}, false, nil
	case err != nil:
		return OpenChat{}, false, fmt.Errorf("find open chat: %w", err)
	}
	chat.Accepted = acceptedAt != nil
	return chat, true, nil
}

// ActiveChat is an accepted chat that has not ended, with the time its
// engineer last spoke.
type ActiveChat struct {
	CaseID         string `json:"caseId"`
	ConversationID string `json:"conversationId"`
	AssigneeID     string `json:"assigneeId"`
	// EngineerActiveAt is the engineer's latest message, or the accept
	// time if they have not written yet.
	EngineerActiveAt time.Time `json:"engineerActiveAt"`
}

// ActiveChats returns every accepted chat that has not ended. An engineer
// message is any comment written by neither the customer nor the
// assistant.
func (r *Router) ActiveChats(ctx context.Context) ([]ActiveChat, error) {
	rows, err := r.db.Query(ctx, `
		SELECT c.case_id, c.conversation_id, c.assignee_id,
		       GREATEST(c.accepted_at, COALESCE((
		           SELECT max(m.created_at) FROM comment m
		           WHERE m.work_item_id = c.work_item_id
		             AND m.created_by <> COALESCE(c.case_info ->> 'customerEmail', '')
		             AND m.created_by <> $1
		       ), c.accepted_at))
		FROM chat_conversation c
		WHERE c.state = 'ACTIVE' AND c.accepted_at IS NOT NULL
		  AND c.session_ended_at IS NULL AND c.assignee_id IS NOT NULL
		ORDER BY c.accepted_at
	`, priorMessageAssistantAuthor)
	if err != nil {
		return nil, fmt.Errorf("active chats: %w", err)
	}
	defer rows.Close()

	var chats []ActiveChat
	for rows.Next() {
		var c ActiveChat
		if err := rows.Scan(&c.CaseID, &c.ConversationID, &c.AssigneeID, &c.EngineerActiveAt); err != nil {
			return nil, fmt.Errorf("active chats: scan: %w", err)
		}
		chats = append(chats, c)
	}
	return chats, rows.Err()
}
