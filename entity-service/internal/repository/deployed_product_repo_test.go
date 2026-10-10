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

package repository

import (
	"encoding/json"
	"testing"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/apierror"
)

// TestParseUpdateDeployedProductDescription covers the three wire states
// UpdateDeployedProductRequest.Description (json.RawMessage) must stay
// distinguishable across: absent (not provided), explicit null (clear it),
// and a real value (set it) -- plus the malformed-type rejection that keeps
// a non-string, non-null payload from ever reaching the database.
func TestParseUpdateDeployedProductDescription(t *testing.T) {
	tests := []struct {
		name         string
		raw          json.RawMessage
		wantProvided bool
		wantValue    *string
		wantErr      bool
	}{
		{name: "absent", raw: nil, wantProvided: false, wantValue: nil},
		{name: "empty raw message", raw: json.RawMessage{}, wantProvided: false, wantValue: nil},
		{name: "explicit null clears it", raw: json.RawMessage(`null`), wantProvided: true, wantValue: nil},
		{name: "a real value", raw: json.RawMessage(`"Production API Manager instance"`), wantProvided: true, wantValue: strPtr("Production API Manager instance")},
		{name: "empty string is still a provided value", raw: json.RawMessage(`""`), wantProvided: true, wantValue: strPtr("")},
		{name: "a JSON number is rejected", raw: json.RawMessage(`123`), wantErr: true},
		{name: "a JSON array is rejected", raw: json.RawMessage(`["x"]`), wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			provided, value, err := parseUpdateDeployedProductDescription(tc.raw)
			if tc.wantErr {
				var ve *apierror.ValidationError
				if !errorsAsValidationError(err, &ve) {
					t.Fatalf("expected *apierror.ValidationError, got %T: %v", err, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if provided != tc.wantProvided {
				t.Errorf("provided = %v, want %v", provided, tc.wantProvided)
			}
			if (value == nil) != (tc.wantValue == nil) {
				t.Fatalf("value = %v, want %v", value, tc.wantValue)
			}
			if value != nil && *value != *tc.wantValue {
				t.Errorf("value = %q, want %q", *value, *tc.wantValue)
			}
		})
	}
}

// TestDeployedProductCreateFKField locks in the constraint-name -> field-name
// mapping CreateDeployedProductFromServiceNow's 23503 handling depends on --
// these names come from Postgres' own default "<table>_<column>_fkey"
// naming for migration 0019's unnamed foreign keys, so a schema rename
// would silently break this mapping (falling back to the generic "one or
// more referenced fields" message) without this test to catch it.
func TestDeployedProductCreateFKField(t *testing.T) {
	want := map[string]string{
		"deployed_product_project_id_fkey":    "projectId",
		"deployed_product_deployment_id_fkey": "deploymentId",
		"deployed_product_product_id_fkey":    "productId",
		"deployed_product_version_id_fkey":    "versionId",
	}
	if len(deployedProductCreateFKField) != len(want) {
		t.Fatalf("deployedProductCreateFKField has %d entries, want %d", len(deployedProductCreateFKField), len(want))
	}
	for constraint, field := range want {
		if got := deployedProductCreateFKField[constraint]; got != field {
			t.Errorf("deployedProductCreateFKField[%q] = %q, want %q", constraint, got, field)
		}
	}
}

// TestLowercaseCategory pins the egress casing of a deployed product's
// category: the enum's UPPER labels become the lower-case codes the request
// filter and project metadata use, and NULL stays nil.
func TestLowercaseCategory(t *testing.T) {
	str := func(s string) *string { return &s }
	cases := []struct {
		name string
		in   *string
		want *string
	}{
		{"nil stays nil", nil, nil},
		{"MS", str("MS"), str("ms")},
		{"PC", str("PC"), str("pc")},
		{"PS", str("PS"), str("ps")},
		{"CL", str("CL"), str("cl")},
		{"PDP", str("PDP"), str("pdp")},
		{"already lower", str("ms"), str("ms")},
		{"empty stays empty", str(""), str("")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := lowercaseCategory(tc.in)
			if (got == nil) != (tc.want == nil) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			if got != nil && *got != *tc.want {
				t.Fatalf("got %q, want %q", *got, *tc.want)
			}
		})
	}
}
