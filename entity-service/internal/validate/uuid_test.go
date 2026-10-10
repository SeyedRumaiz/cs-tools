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

package validate

import "testing"

func TestIsUUID(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"well-formed lowercase", "11111111-1111-1111-1111-111111111111", true},
		{"well-formed uppercase", "AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE", true},
		{"nil UUID still matches the shape", "00000000-0000-0000-0000-000000000000", true},
		{"empty string", "", false},
		{"missing hyphens", "11111111111111111111111111111111", false},
		{"too short", "11111111-1111-1111-1111-11111111", false},
		{"not hex", "gggggggg-gggg-gggg-gggg-gggggggggggg", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsUUID(tc.in); got != tc.want {
				t.Errorf("IsUUID(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
