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

import "testing"

// The guard is dropped only for an explicitly Unrestricted caller. Anything
// else, including the zero SearchScope a forgotten identity would produce,
// must keep it.
func TestAnnouncementLeakGuardFor(t *testing.T) {
	cases := []struct {
		name  string
		scope SearchScope
		want  string
	}{
		{"internal caller skips the guard", SearchScope{Unrestricted: true}, "TRUE"},
		{"customer keeps the guard", SearchScope{ProjectIDs: []string{"p1"}, ViewerEmail: "a@b.c"}, announcementVisibilityLeakGuard},
		{"customer with no projects keeps the guard", SearchScope{ViewerEmail: "a@b.c"}, announcementVisibilityLeakGuard},
		{"zero scope fails closed", SearchScope{}, announcementVisibilityLeakGuard},
	}
	for _, tc := range cases {
		if got := announcementLeakGuardFor(tc.scope); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}
