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

package domain

// SalesforceIngestEntityAccountPartners is the salesforce_ingest_state.entity
// value of the partner-relationship refresh. Its sf_id is the CUSTOMER's
// Salesforce Account Id: one row records the latest refresh of that
// customer's partner set in account_relationship, not one relationship.
const SalesforceIngestEntityAccountPartners = "account_partners"

// SalesforcePartnerRefreshResult reports what one partner refresh did.
type SalesforcePartnerRefreshResult struct {
	// CustomerSfID is the customer's Salesforce Account Id as Sales Entity
	// spells it (18 characters).
	CustomerSfID string `json:"customerSfId"`
	// PartnerSfIDs is the partner set Salesforce holds now, and so the set
	// account_relationship holds for the customer after the refresh.
	PartnerSfIDs []string `json:"partnerSfIds"`
	// Added and Removed count account_relationship rows, forward and
	// reverse together (a new partner is two rows).
	Added   int `json:"added"`
	Removed int `json:"removed"`
	// Disabled is true when CSM_MIGRATION_SALESFORCE_PARTNER_INGEST_ENABLED is
	// off and nothing was read or written.
	Disabled bool `json:"disabled,omitempty"`
}
