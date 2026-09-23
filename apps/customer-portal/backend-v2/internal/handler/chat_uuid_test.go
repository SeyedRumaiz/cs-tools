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

package handler

import "testing"

// TestNewLiveChatCaseID_MatchesUUIDFormat guards the exact contract
// HandleEscalate/HandleCreateCase depend on: every value this generator
// produces must pass uuidRe (response.go), the same validation
// req.ConversationID and other UUID path/body fields go through elsewhere
// in this backend.
func TestNewLiveChatCaseID_MatchesUUIDFormat(t *testing.T) {
	for i := 0; i < 100; i++ {
		id := newLiveChatCaseID()
		if !uuidRe.MatchString(id) {
			t.Fatalf("newLiveChatCaseID produced a non-UUID-shaped value: %q", id)
		}
	}
}

// TestNewLiveChatCaseID_SetsVersionAndVariantBits pins the RFC 4122 §4.4
// bits this generator is responsible for setting by hand (no library
// enforces them for us -- see newLiveChatCaseID's own doc comment on why
// this is hand-rolled): the version nibble must read '4', and the variant
// nibble must be one of 8/9/a/b.
func TestNewLiveChatCaseID_SetsVersionAndVariantBits(t *testing.T) {
	id := newLiveChatCaseID()
	// 8-4-4-4-12: the version nibble is the first character of the third
	// group, the variant nibble is the first character of the fourth group.
	versionNibble := id[14]
	variantNibble := id[19]
	if versionNibble != '4' {
		t.Fatalf("expected version nibble '4', got %q in %q", versionNibble, id)
	}
	switch variantNibble {
	case '8', '9', 'a', 'b':
	default:
		t.Fatalf("expected variant nibble in [89ab], got %q in %q", variantNibble, id)
	}
}

// TestNewLiveChatCaseID_DoesNotRepeat is a basic collision smoke test --
// not a proof of global uniqueness, but enough to catch a broken/constant
// randomness source outright (e.g. an accidentally-zeroed buffer).
func TestNewLiveChatCaseID_DoesNotRepeat(t *testing.T) {
	seen := make(map[string]bool, 1000)
	for i := 0; i < 1000; i++ {
		id := newLiveChatCaseID()
		if seen[id] {
			t.Fatalf("newLiveChatCaseID produced a repeat: %q", id)
		}
		seen[id] = true
	}
}
