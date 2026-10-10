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

// SalesforceEntityOpportunityLineItem is the OpportunityLineItem envelope
// entity name (Sales Entity calls the object a "subscription line item").
const SalesforceEntityOpportunityLineItem = "OpportunityLineItem"

// SalesforceIngestEntityOpportunityLineItem is the salesforce_ingest_state.entity
// value of the standalone line item ingest (table sf_opportunity_product). The
// line items the Opportunity event writes as a set record no ledger row of
// their own; the opportunity's row covers them.
const SalesforceIngestEntityOpportunityLineItem = "opportunity_line_item"
