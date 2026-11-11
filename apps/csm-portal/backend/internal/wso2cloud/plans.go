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

package wso2cloud

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Plan is an Enterprise plan an upgrade can move a subscription to.
type Plan struct {
	Code  string `json:"code"`
	Label string `json:"label"`
}

// Plans lists the Enterprise plans per product, from
// WSO2_CLOUD_ENTERPRISE_PLANS. WSO2 Cloud has no endpoint that lists plans, so
// the deployment names them; the plan code must be an active Enterprise plan
// of the subscription's product or WSO2 Cloud refuses the plan change.
type Plans map[string][]Plan

// ParsePlans reads WSO2_CLOUD_ENTERPRISE_PLANS, a JSON object of product to
// plans, e.g. {"identity":[{"code":"identity-enterprise","label":"Enterprise"}]}.
// Empty means no plans (no upgrade can be raised). A label defaults to the
// code. A malformed value, a blank product or code, or a code listed twice for
// one product is an error, so it fails startup.
func ParsePlans(raw string) (Plans, error) {
	out := Plans{}
	if strings.TrimSpace(raw) == "" {
		return out, nil
	}
	var parsed map[string][]Plan
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil, fmt.Errorf("WSO2_CLOUD_ENTERPRISE_PLANS must be a JSON object of product to [{code, label}]: %w", err)
	}
	for product, plans := range parsed {
		product = strings.TrimSpace(product)
		if product == "" {
			return nil, fmt.Errorf("WSO2_CLOUD_ENTERPRISE_PLANS has a blank product")
		}
		seen := map[string]bool{}
		for _, p := range plans {
			code := strings.TrimSpace(p.Code)
			if code == "" {
				return nil, fmt.Errorf("WSO2_CLOUD_ENTERPRISE_PLANS: product %q has a plan with no code", product)
			}
			if seen[code] {
				return nil, fmt.Errorf("WSO2_CLOUD_ENTERPRISE_PLANS: product %q lists plan %q twice", product, code)
			}
			seen[code] = true
			label := strings.TrimSpace(p.Label)
			if label == "" {
				label = code
			}
			out[product] = append(out[product], Plan{Code: code, Label: label})
		}
	}
	return out, nil
}

// Has reports whether code is a configured plan of product.
func (p Plans) Has(product, code string) bool {
	for _, plan := range p[strings.TrimSpace(product)] {
		if plan.Code == strings.TrimSpace(code) {
			return true
		}
	}
	return false
}
