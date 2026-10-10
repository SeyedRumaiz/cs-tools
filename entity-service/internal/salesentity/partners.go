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

package salesentity

import (
	"context"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/apierror"
)

// CustomerPartner is one partner account of a customer, as listed by
// POST /customer-search with includePartners: a Salesforce Partner row from
// the customer (AccountFromId) to another account whose Role is not
// 'Client', distinct by partner account.
type CustomerPartner struct {
	ID   string  `json:"id"`
	Name *string `json:"name"`
}

type customerPartnersRequest struct {
	IDs             []string `json:"ids"`
	IsRealTime      bool     `json:"isRealTime"`
	IncludePartners bool     `json:"includePartners"`
	Limit           int      `json:"limit"`
}

// customerWithPartners is the slice of a customer-search row this read
// needs. Partners is a pointer so "partners": [] (no partners) is told apart
// from a missing or null key (a Sales Entity build without includePartners):
// the caller replaces the stored set only on the former.
type customerWithPartners struct {
	ID       string             `json:"id"`
	Partners *[]CustomerPartner `json:"partners"`
}

// GetCustomerPartners lists the partner accounts of one customer via
// POST /customer-search {ids: [id], isRealTime: true, includePartners: true}
// (Sales Entity rejects includePartners without isRealTime). It returns the
// customer's Id as Sales Entity spells it (the 18-character form) with the
// partners.
//
// An empty result is a ServiceUnavailableError so the caller can retry, as
// for GetCustomer. A response whose partners key is missing or null is a
// ServiceUnavailableError too, never an empty list: that is an older Sales
// Entity build, and treating it as "no partners" would delete every stored
// partner link.
func (c *Client) GetCustomerPartners(ctx context.Context, id string) (string, []CustomerPartner, error) {
	var rows []customerWithPartners
	body := customerPartnersRequest{IDs: []string{id}, IsRealTime: true, IncludePartners: true, Limit: 1}
	if err := c.searchWithRetry(ctx, customerSearchPath, body, "customer", &rows); err != nil {
		return "", nil, err
	}
	if len(rows) == 0 {
		return "", nil, &apierror.ServiceUnavailableError{Msg: "salesentity: customer not found"}
	}
	for _, row := range rows {
		if !salesforceIDEqual(row.ID, id) {
			continue
		}
		if row.Partners == nil {
			return "", nil, &apierror.ServiceUnavailableError{Msg: "salesentity: customer-search did not return partners (includePartners unsupported)"}
		}
		return row.ID, *row.Partners, nil
	}
	return "", nil, &apierror.ServiceUnavailableError{Msg: "salesentity: customer-search returned an unexpected customer"}
}
