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
	"slices"
	"strings"
)

// KnownRegions are the WSO2 Cloud regions, as codes. Salesforce's
// Organization record uses the same codes. Each region has a billing admin API
// of its own, with its own client.
var KnownRegions = []string{"us", "eu"}

// ErrRegionRequired is returned when a call names no region and this
// deployment has more than one.
var ErrRegionRequired = errors.New("a WSO2 Cloud region is required")

// RegionError is a region this deployment has no billing API for.
type RegionError struct {
	Region  string
	Regions []string
}

func (e *RegionError) Error() string {
	return fmt.Sprintf("WSO2 Cloud region %q is not available here (regions: %s)", e.Region, strings.Join(e.Regions, ", "))
}

// PickRegion returns the region a call goes to: the one named, which must be
// one of regions, or, when none is named, the only region there is.
func PickRegion(region string, regions []string) (string, error) {
	region = strings.ToLower(strings.TrimSpace(region))
	if region == "" {
		if len(regions) == 1 {
			return regions[0], nil
		}
		return "", ErrRegionRequired
	}
	if !slices.Contains(regions, region) {
		return "", &RegionError{Region: region, Regions: regions}
	}
	return region, nil
}

// regionalBilling reaches WSO2 Cloud's billing admin API in every region this
// deployment has, each through its own client.
type regionalBilling struct {
	regions []string
	clients map[string]*BillingClient
}

// Apply runs an approved upgrade through the billing API of the request's
// region. Other request types have no API yet, whatever their region.
func (b regionalBilling) Apply(ctx context.Context, req Request, rec Recorder) error {
	if req.Type != "upgrade_organization" {
		return ErrNotAvailable
	}
	region, err := PickRegion(deref(req.Region), b.regions)
	if err != nil {
		return err
	}
	return BillingApplier{Client: b.clients[region]}.Apply(ctx, req, rec)
}

// ListSubscriptions lists an organization's subscriptions in a region.
func (b regionalBilling) ListSubscriptions(ctx context.Context, region, orgID string) ([]Subscription, error) {
	r, err := PickRegion(region, b.regions)
	if err != nil {
		return nil, err
	}
	return b.clients[r].ListSubscriptions(ctx, orgID)
}

// parseRegions reads WSO2_CLOUD_REGIONS: region codes separated by commas or
// spaces, each one of KnownRegions, listed once.
func parseRegions(raw string) ([]string, error) {
	var regions []string
	for _, r := range strings.Fields(strings.ReplaceAll(raw, ",", " ")) {
		r = strings.ToLower(r)
		if !slices.Contains(KnownRegions, r) {
			return nil, fmt.Errorf("WSO2_CLOUD_REGIONS: unknown region %q (known: %s)", r, strings.Join(KnownRegions, ", "))
		}
		if !slices.Contains(regions, r) {
			regions = append(regions, r)
		}
	}
	if len(regions) == 0 {
		return nil, fmt.Errorf("WSO2_CLOUD_APPLY_MODE=%s needs WSO2_CLOUD_REGIONS (for example: us, or us,eu)", ModeBillingAPI)
	}
	return regions, nil
}

// regionBillingConfig reads one region's billing settings,
// WSO2_CLOUD_<REGION>_BILLING_BASE_URL and the rest.
func regionBillingConfig(getenv func(string) string, region string) (BillingConfig, error) {
	prefix := "WSO2_CLOUD_" + strings.ToUpper(region) + "_"
	get := func(name string) string { return strings.TrimSpace(getenv(prefix + name)) }
	cfg := BillingConfig{
		BaseURL:      get("BILLING_BASE_URL"),
		TokenURL:     get("TOKEN_URL"),
		ClientID:     get("CLIENT_ID"),
		ClientSecret: get("CLIENT_SECRET"),
		Scopes:       strings.Fields(strings.ReplaceAll(getenv(prefix+"SCOPES"), ",", " ")),
		Audience:     get("TOKEN_AUDIENCE"),
	}
	var missing []string
	for _, f := range []struct{ name, value string }{
		{"BILLING_BASE_URL", cfg.BaseURL}, {"TOKEN_URL", cfg.TokenURL}, {"CLIENT_ID", cfg.ClientID}, {"CLIENT_SECRET", cfg.ClientSecret},
	} {
		if f.value == "" {
			missing = append(missing, prefix+f.name)
		}
	}
	if len(missing) > 0 {
		return BillingConfig{}, fmt.Errorf("WSO2_CLOUD_APPLY_MODE=%s needs %s", ModeBillingAPI, strings.Join(missing, ", "))
	}
	return cfg, nil
}
