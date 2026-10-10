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
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
)

// AnnouncementRegistryRepository reads every announcement case matching a
// search in ONE query, for the CSM announcement registry.
//
// The registry groups every matching announcement into batches client-side,
// so it needs the whole matching set, not a page. Reading it through
// CaseRepository.SearchCases meant one request per 50 rows (that search caps a
// page at 50), each repeating the same full scan, sort and COUNT: about 100
// queries per registry load for ~5,000 announcements. Under row-level
// security that is slow, because work_item's policy runs before the type
// filter and enum equality is not leakproof, so the (type, updated_on) index
// cannot be used and every page scans the whole table.
type AnnouncementRegistryRepository interface {
	// SearchAnnouncementCases returns every case matching req, newest-updated
	// first, carrying only the fields the registry reads. It returns
	// ErrTooManyRegistryRows when more than maxRows match, rather than a
	// silently truncated list.
	SearchAnnouncementCases(ctx context.Context, req domain.SearchCasesRequest, scope SearchScope, maxRows int) ([]domain.SearchCaseView, error)
}

// ErrTooManyRegistryRows is returned by SearchAnnouncementCases when the
// result would exceed maxRows.
var ErrTooManyRegistryRows = errors.New("too many matching announcements")

type announcementRegistryRepo struct {
	db *Scoped
}

// NewAnnouncementRegistryRepository returns an AnnouncementRegistryRepository
// backed by the Scoped pool, so the caller's identity is stamped on the query
// exactly as for every other protected read.
func NewAnnouncementRegistryRepository(db *Scoped) AnnouncementRegistryRepository {
	return &announcementRegistryRepo{db: db}
}

// SearchAnnouncementCases implements AnnouncementRegistryRepository.
//
// It reuses buildCaseSearchWhere and caseSearchJoins, so filters mean exactly
// what they mean in /cases/search, and the same ORDER BY as the registry used
// before (updatedOn descending, id as tie-break). The select list is cut down
// to what the registry reads, so Postgres drops the joins nothing references.
func (r *announcementRegistryRepo) SearchAnnouncementCases(ctx context.Context, req domain.SearchCasesRequest, scope SearchScope, maxRows int) ([]domain.SearchCaseView, error) {
	// Every row below is labelled an announcement, so refuse a request that
	// does not filter to announcements only (the service forces this; a future
	// caller that skips the service must not get other case types mislabelled).
	if len(req.Parsed.Types) != 1 || !strings.EqualFold(req.Parsed.Types[0], "announcement") {
		return nil, fmt.Errorf("announcement registry search requires a type filter of exactly announcement, got %v", req.Parsed.Types)
	}
	// Same explicit identity stamp as caseRepo.SearchCases.
	ctx = WithCallerIdentity(ctx, scope)
	where, args, argIdx, err := buildCaseSearchWhere(req, scope)
	if err != nil {
		return nil, err
	}

	query := fmt.Sprintf(
		`SELECT wi.id, wi.number, wi.wso2_id, wi.subject, `+caseLikeStateColumn+`,
		        wi.created_on, wi.updated_on, wi.created_by,
		        p.id, p.name
		 FROM work_item wi %s %s
		 ORDER BY wi.updated_on DESC NULLS LAST, wi.id
		 LIMIT $%d`,
		caseSearchJoins, where, argIdx,
	)
	args = append(args, maxRows+1)

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query announcement registry cases: %w", err)
	}
	defer rows.Close()

	out := make([]domain.SearchCaseView, 0, 256)
	for rows.Next() {
		var (
			cv                   domain.SearchCaseView
			internalID           *string
			subject              string
			state                *string
			createdAt, updatedAt time.Time
			creatorEmail         string
			projID, projName     *string
		)
		if err := rows.Scan(&cv.ID, &cv.Number, &internalID, &subject, &state,
			&createdAt, &updatedAt, &creatorEmail, &projID, &projName); err != nil {
			return nil, fmt.Errorf("scan announcement registry case: %w", err)
		}
		cv.InternalID = stringOrEmpty(internalID)
		cv.Type = "announcement"
		cv.Subject = &subject
		if state != nil {
			lower := strings.ToLower(*state)
			cv.State = &lower
		}
		cv.CreatedOn = createdAt.UTC().Format(time.RFC3339)
		cv.UpdatedOn = updatedAt.UTC().Format(time.RFC3339)
		// Same as SearchCases: the projection carries only the creator's
		// email, so the reference keeps a null id and empty name.
		cv.CreatedBy = domain.NewUserReference("", creatorEmail, "")
		if projID != nil {
			cv.Project = &domain.EntityRef{ID: *projID, Name: stringOrEmpty(projName)}
		}
		out = append(out, cv)
		if len(out) > maxRows {
			return nil, ErrTooManyRegistryRows
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate announcement registry cases: %w", err)
	}
	return out, nil
}
