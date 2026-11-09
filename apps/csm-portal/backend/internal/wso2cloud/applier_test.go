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

func TestApplierForMode(t *testing.T) {
	for _, raw := range []string{"", "  "} {
		a, err := ApplierForMode(raw)
		if err != nil {
			t.Fatalf("ApplierForMode(%q): %v", raw, err)
		}
		if err := a.Apply(context.Background(), Request{}); !errors.Is(err, ErrNotConnected) {
			t.Errorf("ApplierForMode(%q).Apply = %v, want ErrNotConnected", raw, err)
		}
	}
	a, err := ApplierForMode(" dry_run ")
	if err != nil {
		t.Fatalf("ApplierForMode(dry_run): %v", err)
	}
	if err := a.Apply(context.Background(), Request{ID: "x"}); err != nil {
		t.Errorf("dry run Apply = %v, want nil", err)
	}
	for _, raw := range []string{"dry-run", "DRY_RUN", "live", "true"} {
		if _, err := ApplierForMode(raw); err == nil {
			t.Errorf("ApplierForMode(%q) accepted, want an error", raw)
		}
	}
}
