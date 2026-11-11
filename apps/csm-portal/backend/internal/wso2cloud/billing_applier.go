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
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
)

// billingClient is the part of BillingClient the applier calls.
type billingClient interface {
	CreateBillingAccount(ctx context.Context, orgID, csmRequest string) (string, error)
	ChangePlan(ctx context.Context, orgID, subscriptionID string, change PlanChange) error
}

// BillingApplier applies an approved upgrade through WSO2 Cloud's billing
// admin API: it creates a wire-transfer billing account for the organization
// (unless an earlier attempt already did), records it on the request, then
// moves the subscription to the Enterprise plan on that account. Confirming
// the payment when the wire arrives is not open to clients yet, so it is not
// done here. Every other request type returns ErrNotAvailable.
type BillingApplier struct {
	Client billingClient
}

// Apply runs the upgrade. A plan change is safe to repeat, and the billing
// account is reused once recorded, so an approval that failed part-way can be
// retried.
func (a BillingApplier) Apply(ctx context.Context, req Request, rec Recorder) error {
	if req.Type != "upgrade_organization" {
		return ErrNotAvailable
	}
	subscriptionID, planCode := deref(req.SubscriptionID), deref(req.PlanCode)
	if subscriptionID == "" || planCode == "" {
		return errors.New("the upgrade request names no subscription or plan")
	}
	accountID := deref(req.BillingAccountID)
	if accountID == "" {
		created, err := a.Client.CreateBillingAccount(ctx, req.TargetID, req.Number)
		if err != nil {
			return fmt.Errorf("create billing account: %w", err)
		}
		if err := rec.RecordBillingAccount(ctx, created); err != nil {
			// The account exists in WSO2 Cloud but the request does not know
			// it: a retry would create another. Logged with its id so a person
			// can put it right.
			slog.ErrorContext(ctx, "wso2 cloud: billing account created but not recorded on the request",
				"requestId", req.ID, "number", req.Number, "billingAccountId", created, "err", err)
			return fmt.Errorf("record billing account: %w", err)
		}
		accountID = created
	}
	change := PlanChange{
		PlanCode:         planCode,
		BillingAccountID: accountID,
		BillingPeriod:    deref(req.BillingPeriod),
		NotifyCustomer:   req.NotifyCustomer,
		Notes:            planChangeNotes(req),
	}
	if err := a.Client.ChangePlan(ctx, req.TargetID, subscriptionID, change); err != nil {
		return fmt.Errorf("change plan: %w", err)
	}
	return nil
}

// planChangeNotes names the CSM request, so the change can be traced in WSO2
// Cloud's audit log, followed by the requester's reason.
func planChangeNotes(req Request) string {
	notes := "CSM portal request " + req.Number
	if reason := deref(req.Reason); reason != "" {
		notes += ": " + reason
	}
	return notes
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return strings.TrimSpace(*s)
}
