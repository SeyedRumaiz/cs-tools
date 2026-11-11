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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
)

// SubscriptionLister lists an organization's product subscriptions, so an
// upgrade can name the one it moves.
type SubscriptionLister interface {
	ListSubscriptions(ctx context.Context, orgID string) ([]Subscription, error)
}

// notConnectedSubscriptions answers every lookup with ErrNotConnected.
type notConnectedSubscriptions struct{}

func (notConnectedSubscriptions) ListSubscriptions(context.Context, string) ([]Subscription, error) {
	return nil, ErrNotConnected
}

// dryRunSubscriptions gives every organization the same two sample
// subscriptions, so the upgrade form can be tried on a local stack. Never in a
// real deployment: these subscriptions do not exist.
type dryRunSubscriptions struct{}

func (dryRunSubscriptions) ListSubscriptions(ctx context.Context, orgID string) ([]Subscription, error) {
	slog.WarnContext(ctx, "wso2 cloud: dry run, sample subscriptions returned", "orgId", orgID)
	return []Subscription{
		{ID: "dry-run-" + orgID + "-identity", Product: "identity", PlanCode: "identity-payg", Status: "active"},
		{ID: "dry-run-" + orgID + "-integration", Product: "integration", PlanCode: "integration-free", Status: "trial", TrialEnd: "2027-01-31T00:00:00Z"},
	}, nil
}

// subscriptionWire is one subscription as the billing admin API returns it.
// The guide names subscription_id, plan_code and trial_end; id, product_code
// and a nested plan are accepted too, because the full schema
// (billing-admin-api.v1.yaml) was not available when this was written. When it
// is, tighten this to the real shape.
type subscriptionWire struct {
	ID             string `json:"id"`
	SubscriptionID string `json:"subscription_id"`
	Product        string `json:"product"`
	ProductCode    string `json:"product_code"`
	PlanCode       string `json:"plan_code"`
	Plan           *struct {
		Code string `json:"code"`
	} `json:"plan"`
	Status   string `json:"status"`
	TrialEnd string `json:"trial_end"`
}

// parseSubscriptions reads a subscription list that is either a bare array or
// an object holding it under data, subscriptions or items.
func parseSubscriptions(raw json.RawMessage) ([]Subscription, error) {
	raw = bytes.TrimSpace(raw)
	var rows []subscriptionWire
	if len(raw) > 0 && raw[0] == '[' {
		if err := json.Unmarshal(raw, &rows); err != nil {
			return nil, fmt.Errorf("decode subscriptions: %w", err)
		}
	} else {
		var wrapped map[string]json.RawMessage
		if err := json.Unmarshal(raw, &wrapped); err != nil {
			return nil, fmt.Errorf("decode subscriptions: %w", err)
		}
		for _, key := range []string{"data", "subscriptions", "items"} {
			if list, ok := wrapped[key]; ok {
				if err := json.Unmarshal(list, &rows); err != nil {
					return nil, fmt.Errorf("decode subscriptions.%s: %w", key, err)
				}
				break
			}
		}
	}
	out := make([]Subscription, 0, len(rows))
	for _, r := range rows {
		s := Subscription{ID: r.SubscriptionID, Product: r.Product, PlanCode: r.PlanCode, Status: r.Status, TrialEnd: r.TrialEnd}
		if s.ID == "" {
			s.ID = r.ID
		}
		if s.Product == "" {
			s.Product = r.ProductCode
		}
		if s.PlanCode == "" && r.Plan != nil {
			s.PlanCode = r.Plan.Code
		}
		if s.ID == "" {
			continue // nothing an upgrade could name
		}
		out = append(out, s)
	}
	return out, nil
}
