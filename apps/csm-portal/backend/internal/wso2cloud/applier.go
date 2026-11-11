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

// Package wso2cloud applies an approved WSO2 Cloud request (upgrade or delete
// an organization; lock, unlock, disable, enable or delete a user) in WSO2
// Cloud itself. Three appliers exist: NotConnected (refuses every approval),
// DryRun (records the approval without changing anything, for local and test
// stacks) and BillingApplier, which upgrades an organization through WSO2
// Cloud's billing admin API. The other request types have no WSO2 Cloud API
// yet, so BillingApplier refuses them with ErrNotAvailable.
package wso2cloud

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
)

// Request is the part of a WSO2 Cloud request an Applier needs: what to change
// and on which organization or user. It is decoded from entity-service's
// request record.
type Request struct {
	ID     string `json:"id"`
	Number string `json:"number"`
	// Type is upgrade_organization, delete_organization, lock_user,
	// unlock_user, disable_user, enable_user or delete_user.
	Type string `json:"type"`
	// TargetID is the organization's UUID or the user's SCIM id in WSO2 Cloud.
	TargetID    string  `json:"targetId"`
	TargetLabel string  `json:"targetLabel"`
	Reason      *string `json:"reason"`
	// The upgrade fields: the subscription (one product of the organization)
	// moved to an Enterprise plan billed by wire transfer. Nil for every
	// other type.
	Product        *string `json:"product"`
	SubscriptionID *string `json:"subscriptionId"`
	PlanCode       *string `json:"planCode"`
	BillingPeriod  *string `json:"billingPeriod"`
	NotifyCustomer bool    `json:"notifyCustomer"`
	// BillingAccountID is set once an approval has created the wire billing
	// account, so a retried approval reuses it.
	BillingAccountID *string `json:"billingAccountId"`
}

// Recorder stores what an Applier creates in WSO2 Cloud part-way through,
// so that a failed approval can be retried without creating it again.
type Recorder interface {
	RecordBillingAccount(ctx context.Context, billingAccountID string) error
}

// Applier makes the change an approved request asks for in WSO2 Cloud.
type Applier interface {
	Apply(ctx context.Context, req Request, rec Recorder) error
}

// ErrNotConnected is returned when this deployment has no WSO2 Cloud API to
// apply a change through.
var ErrNotConnected = errors.New("WSO2 Cloud is not connected")

// ErrNotAvailable is returned for a request type WSO2 Cloud offers no API for
// yet (everything but an upgrade).
var ErrNotAvailable = errors.New("WSO2 Cloud has no API for this request type yet")

// NotConnected refuses every approval: nothing can be applied, so nothing may
// be recorded as approved. This is the default.
type NotConnected struct{}

// Apply always returns ErrNotConnected.
func (NotConnected) Apply(context.Context, Request, Recorder) error { return ErrNotConnected }

// DryRun records an approval without changing anything in WSO2 Cloud. For
// local and test stacks only: an approval it lets through says a change was
// made that was not.
type DryRun struct{}

// Apply logs the change it would have made and returns nil.
func (DryRun) Apply(ctx context.Context, req Request, _ Recorder) error {
	slog.WarnContext(ctx, "wso2 cloud: dry run, change not applied", "requestId", req.ID, "number", req.Number, "type", req.Type)
	return nil
}

// Connection is how this deployment reaches WSO2 Cloud: what applies an
// approved request and what lists an organization's subscriptions.
type Connection struct {
	Applier       Applier
	Subscriptions SubscriptionLister
	// Mode is the WSO2_CLOUD_APPLY_MODE value in effect ("" for not connected).
	Mode string
}

// Modes of WSO2_CLOUD_APPLY_MODE.
const (
	ModeNotConnected = ""
	ModeDryRun       = "dry_run"
	ModeBillingAPI   = "billing_api"
)

// FromEnv builds the Connection WSO2_CLOUD_APPLY_MODE names: empty for not
// connected, "dry_run" for DryRun (with sample subscriptions), "billing_api"
// for WSO2 Cloud's billing admin API, which needs WSO2_CLOUD_BILLING_BASE_URL,
// WSO2_CLOUD_TOKEN_URL, WSO2_CLOUD_CLIENT_ID and WSO2_CLOUD_CLIENT_SECRET
// (WSO2_CLOUD_SCOPES and WSO2_CLOUD_TOKEN_AUDIENCE are optional). Any other
// mode, or billing_api without its settings, is an error, so a typo fails
// startup instead of quietly refusing (or allowing) approvals.
func FromEnv(getenv func(string) string) (Connection, error) {
	switch mode := strings.TrimSpace(getenv("WSO2_CLOUD_APPLY_MODE")); mode {
	case ModeNotConnected:
		return Connection{Applier: NotConnected{}, Subscriptions: notConnectedSubscriptions{}, Mode: mode}, nil
	case ModeDryRun:
		return Connection{Applier: DryRun{}, Subscriptions: dryRunSubscriptions{}, Mode: mode}, nil
	case ModeBillingAPI:
		cfg := BillingConfig{
			BaseURL:      strings.TrimSpace(getenv("WSO2_CLOUD_BILLING_BASE_URL")),
			TokenURL:     strings.TrimSpace(getenv("WSO2_CLOUD_TOKEN_URL")),
			ClientID:     strings.TrimSpace(getenv("WSO2_CLOUD_CLIENT_ID")),
			ClientSecret: strings.TrimSpace(getenv("WSO2_CLOUD_CLIENT_SECRET")),
			Scopes:       strings.Fields(strings.ReplaceAll(getenv("WSO2_CLOUD_SCOPES"), ",", " ")),
			Audience:     strings.TrimSpace(getenv("WSO2_CLOUD_TOKEN_AUDIENCE")),
		}
		client, err := NewBillingClient(cfg)
		if err != nil {
			return Connection{}, err
		}
		return Connection{Applier: BillingApplier{Client: client}, Subscriptions: client, Mode: mode}, nil
	default:
		return Connection{}, fmt.Errorf("unknown WSO2_CLOUD_APPLY_MODE %q: leave it empty, or set it to %s or %s", mode, ModeDryRun, ModeBillingAPI)
	}
}
