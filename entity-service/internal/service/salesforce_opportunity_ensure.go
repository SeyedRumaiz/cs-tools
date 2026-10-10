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

package service

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/apierror"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/repository"
)

// ensureOpportunity returns the sf_opportunity.id of the opportunity with
// this Salesforce Id, for a child ingest (linked opportunity, invoice, line
// item) that needs its parent row — the ServiceNow script's _initOpportunity
// step. A missing opportunity is ingested inline through the ordinary
// Opportunity branch (which ensures its account in turn), with the duplicate
// guard off, and looked up again. The child families run under the
// Opportunity flag, so the branch is on whenever they are; should it be off,
// the answer is a NotFoundError and the child event fails and is redelivered.
//
// A missing account under the inline Opportunity ingest surfaces as
// EnsureAccount's "account not found ..." NotFoundError, which the child
// records in its FAILED ledger row for the delayed-retry job.
func (s *salesforceEventService) ensureOpportunity(ctx context.Context, lookup repository.SalesforceOpportunityLookup, sfID string) (string, error) {
	sfID = salesforceID18(sfID)
	if sfID == "" {
		return "", &apierror.ValidationError{Msg: "opportunity sfId is required"}
	}
	id, err := lookup.LookupOpportunityIDBySfID(ctx, sfID)
	if err != nil {
		return "", err
	}
	if id != nil {
		return *id, nil
	}
	if !s.opportunity.enabled() {
		return "", &apierror.NotFoundError{Msg: fmt.Sprintf("opportunity not found for sfId %q", sfID)}
	}
	slog.InfoContext(ctx, "salesforce: parent opportunity not in CSM yet, ingesting it first", "opportunitySfId", sfID)
	// The duplicate guard is off here, as in EnsureAccount: the row is known
	// to be missing, so a ledger row saying this version was written is
	// stale (a cascade or an out-of-band delete) and must not stop the write.
	if err := s.ingestOpportunity(ctx, sfID, domain.SalesforceEventUpdated, false); err != nil {
		return "", err
	}
	id, err = lookup.LookupOpportunityIDBySfID(ctx, sfID)
	if err != nil {
		return "", err
	}
	if id == nil {
		return "", fmt.Errorf("salesforce: opportunity %s was ingested but cannot be read back by sf_id", sfID)
	}
	return *id, nil
}
