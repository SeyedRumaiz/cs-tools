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
	"testing"
)

func TestPickRegion(t *testing.T) {
	both := []string{"us", "eu"}
	for _, tc := range []struct {
		region  string
		regions []string
		want    string
		err     error
	}{
		{"eu", both, "eu", nil},
		{" EU ", both, "eu", nil},
		{"", []string{"us"}, "us", nil},
		{"", both, "", ErrRegionRequired},
		{"eu", []string{"us"}, "", &RegionError{}},
		{"apac", both, "", &RegionError{}},
	} {
		got, err := PickRegion(tc.region, tc.regions)
		switch tc.err.(type) {
		case nil:
			if err != nil || got != tc.want {
				t.Errorf("PickRegion(%q, %v) = %q, %v; want %q", tc.region, tc.regions, got, err, tc.want)
			}
		case *RegionError:
			var re *RegionError
			if !errors.As(err, &re) {
				t.Errorf("PickRegion(%q, %v) = %v; want a RegionError", tc.region, tc.regions, err)
			}
		default:
			if !errors.Is(err, tc.err) {
				t.Errorf("PickRegion(%q, %v) = %v; want %v", tc.region, tc.regions, err, tc.err)
			}
		}
	}
}

// twoRegions is a regionalBilling over a fake billing API per region.
func twoRegions(t *testing.T) (regionalBilling, map[string]*fakeBilling) {
	fakes := map[string]*fakeBilling{"us": newFakeBilling(t), "eu": newFakeBilling(t)}
	b := regionalBilling{regions: []string{"us", "eu"}, clients: map[string]*BillingClient{}}
	for r, f := range fakes {
		b.clients[r] = f.client(t)
	}
	return b, fakes
}

func TestRegionalBilling_AppliesThroughTheRequestsRegion(t *testing.T) {
	b, fakes := twoRegions(t)
	req := upgrade()
	req.Region = strp("eu")
	if err := b.Apply(context.Background(), req, &recordedAccounts{}); err != nil {
		t.Fatal(err)
	}
	if len(fakes["eu"].planChanges) != 1 || len(fakes["us"].planChanges) != 0 || fakes["us"].created != 0 {
		t.Fatalf("eu plan changes %d, us plan changes %d, us accounts %d; want only eu", len(fakes["eu"].planChanges), len(fakes["us"].planChanges), fakes["us"].created)
	}
}

func TestRegionalBilling_RequestWithoutRegion(t *testing.T) {
	b, fakes := twoRegions(t)
	if err := b.Apply(context.Background(), upgrade(), &recordedAccounts{}); !errors.Is(err, ErrRegionRequired) {
		t.Fatalf("two regions, no region: %v; want ErrRegionRequired", err)
	}
	if fakes["us"].created+fakes["eu"].created != 0 {
		t.Fatal("WSO2 Cloud was called for a request with no region")
	}
	one := regionalBilling{regions: []string{"us"}, clients: map[string]*BillingClient{"us": b.clients["us"]}}
	if err := one.Apply(context.Background(), upgrade(), &recordedAccounts{}); err != nil {
		t.Fatalf("one region, no region: %v; want it applied there", err)
	}
	if len(fakes["us"].planChanges) != 1 {
		t.Fatal("the only region was not used")
	}
}

func TestRegionalBilling_OtherTypesAreNotAvailable(t *testing.T) {
	b, _ := twoRegions(t)
	if err := b.Apply(context.Background(), Request{Type: "delete_user"}, nil); !errors.Is(err, ErrNotAvailable) {
		t.Fatalf("delete_user: %v; want ErrNotAvailable", err)
	}
}

func TestRegionalBilling_ListsSubscriptionsInTheRegion(t *testing.T) {
	b, fakes := twoRegions(t)
	fakes["us"].subsBody = `[{"subscription_id":"sub-us","product":"identity","plan_code":"identity-payg","status":"active"}]`
	fakes["eu"].subsBody = `[{"subscription_id":"sub-eu","product":"identity","plan_code":"identity-payg","status":"active"}]`
	subs, err := b.ListSubscriptions(context.Background(), "eu", "org-1")
	if err != nil || len(subs) != 1 || subs[0].ID != "sub-eu" {
		t.Fatalf("eu subscriptions = %+v, %v", subs, err)
	}
	if _, err := b.ListSubscriptions(context.Background(), "", "org-1"); !errors.Is(err, ErrRegionRequired) {
		t.Fatalf("no region: %v; want ErrRegionRequired", err)
	}
}
