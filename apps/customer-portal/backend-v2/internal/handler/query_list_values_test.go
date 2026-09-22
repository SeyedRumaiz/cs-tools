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

import (
	"net/url"
	"reflect"
	"testing"
)

// The microapp joins list parameters with commas while the webapp repeats
// them. Before this split, a comma-joined value reached entity-service as one
// item and came back "caseTypes contains invalid value: a,b,c".
func TestQueryListValues(t *testing.T) {
	for _, tt := range []struct {
		name string
		raw  string
		want []string
	}{
		{"comma joined, as the microapp sends", "?caseTypes=default_case,security_report_analysis,engagement",
			[]string{"default_case", "security_report_analysis", "engagement"}},
		{"repeated, as the webapp sends", "?caseTypes=default_case&caseTypes=engagement",
			[]string{"default_case", "engagement"}},
		{"mixed", "?caseTypes=a,b&caseTypes=c", []string{"a", "b", "c"}},
		{"spaces and a trailing comma", "?caseTypes=a, b,", []string{"a", "b"}},
		{"absent", "", nil},
		{"present but empty", "?caseTypes=", nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			u, err := url.Parse("http://x/p" + tt.raw)
			if err != nil {
				t.Fatal(err)
			}
			got := queryListValues(u.Query()["caseTypes"])
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %#v, want %#v", got, tt.want)
			}
		})
	}
}
