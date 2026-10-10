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
	"strings"
	"testing"
)

func TestViewerProjectHint(t *testing.T) {
	if got := viewerProjectHint("wi", SearchScope{Unrestricted: true}); got != "" {
		t.Fatalf("internal caller must get no hint (it would match nothing), got %q", got)
	}
	got := viewerProjectHint("wi", SearchScope{ViewerEmail: "c@example.com"})
	if !strings.Contains(got, "wi.project_id = ANY(") || !strings.Contains(got, "app.viewer_project_ids") {
		t.Fatalf("external caller must get the project predicate, got %q", got)
	}
	if !strings.HasPrefix(got, " AND ") {
		t.Fatalf("hint must be appendable to an existing WHERE clause, got %q", got)
	}
}
