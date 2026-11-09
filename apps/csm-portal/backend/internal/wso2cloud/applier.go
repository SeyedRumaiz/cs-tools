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
// Cloud itself. The WSO2 Cloud API is not available yet, so the only
// appliers today are NotConnected (refuses every approval) and DryRun
// (records the approval without changing anything, for local and test
// stacks). A real client takes the Applier's place once the API exists.
package wso2cloud

import (
	"context"
	"encoding/json"
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
	TargetID    string `json:"targetId"`
	TargetLabel string `json:"targetLabel"`
	// Tier and Limits are set for an upgrade only.
	Tier   *string         `json:"tier"`
	Limits json.RawMessage `json:"limits"`
}

// Applier makes the change an approved request asks for in WSO2 Cloud.
type Applier interface {
	Apply(ctx context.Context, req Request) error
}

// ErrNotConnected is returned when this deployment has no WSO2 Cloud API to
// apply a change through.
var ErrNotConnected = errors.New("WSO2 Cloud is not connected")

// NotConnected refuses every approval: nothing can be applied, so nothing may
// be recorded as approved. This is the default.
type NotConnected struct{}

// Apply always returns ErrNotConnected.
func (NotConnected) Apply(context.Context, Request) error { return ErrNotConnected }

// DryRun records an approval without changing anything in WSO2 Cloud. For
// local and test stacks only: an approval it lets through says a change was
// made that was not.
type DryRun struct{}

// Apply logs the change it would have made and returns nil.
func (DryRun) Apply(ctx context.Context, req Request) error {
	slog.WarnContext(ctx, "wso2 cloud: dry run, change not applied", "requestId", req.ID, "number", req.Number, "type", req.Type)
	return nil
}

// ApplierForMode returns the Applier WSO2_CLOUD_APPLY_MODE names: empty for
// NotConnected, "dry_run" for DryRun. Any other value is an error, so a typo
// fails startup instead of quietly refusing (or allowing) approvals.
func ApplierForMode(raw string) (Applier, error) {
	switch mode := strings.TrimSpace(raw); mode {
	case "":
		return NotConnected{}, nil
	case "dry_run":
		return DryRun{}, nil
	default:
		return nil, fmt.Errorf("unknown WSO2_CLOUD_APPLY_MODE %q: leave it empty or set it to dry_run", mode)
	}
}
