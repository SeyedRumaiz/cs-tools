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

package dto

import "testing"

// caseIssueTypeRef/caseEngagementTypeRef used to echo the raw input label
// verbatim (Label: *label) even on a recognised enum, unlike caseStatusRef's
// own already-correct displayLabelOr lookup -- so a Postgres-mode caller,
// whose raw label is the UPPER_SNAKE enum value, saw that raw value on every
// case card's issue-type/engagement-type chip instead of a display label.
// ServiceNow-mode callers never noticed, since SN happens to send labels
// close enough to Title Case already.

func TestCaseIssueTypeRef_ResolvesDisplayLabelForPostgresEnum(t *testing.T) {
	got := caseIssueTypeRef(strPtr("PERFORMANCE_DEGRADATION"))
	if got == nil || got.Label != "Performance Degradation" {
		t.Fatalf("caseIssueTypeRef(PERFORMANCE_DEGRADATION) = %+v, want Label \"Performance Degradation\"", got)
	}
	if got.ID != caseIssueTypeIDs["performance_degradation"] {
		t.Errorf("caseIssueTypeRef id = %q, want %q", got.ID, caseIssueTypeIDs["performance_degradation"])
	}
}

func TestCaseIssueTypeRef_UnrecognisedLabelPassesThrough(t *testing.T) {
	got := caseIssueTypeRef(strPtr("Something Unmapped"))
	if got == nil || got.Label != "Something Unmapped" || got.ID != "" {
		t.Fatalf("caseIssueTypeRef(Something Unmapped) = %+v, want {ID:\"\" Label:\"Something Unmapped\"}", got)
	}
}

func TestCaseEngagementTypeRef_ResolvesDisplayLabelForPostgresEnum(t *testing.T) {
	got := caseEngagementTypeRef(strPtr("NEW_FEATURE_IMPROVEMENT"))
	if got == nil || got.Label != "New Feature Improvement" {
		t.Fatalf("caseEngagementTypeRef(NEW_FEATURE_IMPROVEMENT) = %+v, want Label \"New Feature Improvement\"", got)
	}
	if got.ID != caseEngagementTypeIDs["new_feature_improvement"] {
		t.Errorf("caseEngagementTypeRef id = %q, want %q", got.ID, caseEngagementTypeIDs["new_feature_improvement"])
	}
}

func TestCaseEngagementTypeRef_ResolvesDisplayLabelForDomainEnum(t *testing.T) {
	// entity-service's own domain.EngagementType spelling (lowercase), the
	// form the case-search response actually carries.
	got := caseEngagementTypeRef(strPtr("new_feature_improvement"))
	if got == nil || got.Label != "New Feature Improvement" {
		t.Fatalf("caseEngagementTypeRef(new_feature_improvement) = %+v, want Label \"New Feature Improvement\"", got)
	}
}

func TestCaseEngagementTypeRef_UnrecognisedLabelPassesThrough(t *testing.T) {
	got := caseEngagementTypeRef(strPtr("Something Unmapped"))
	if got == nil || got.Label != "Something Unmapped" || got.ID != "" {
		t.Fatalf("caseEngagementTypeRef(Something Unmapped) = %+v, want {ID:\"\" Label:\"Something Unmapped\"}", got)
	}
}
