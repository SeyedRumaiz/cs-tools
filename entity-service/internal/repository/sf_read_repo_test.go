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
	"reflect"
	"testing"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
)

func TestBuildSFWhere(t *testing.T) {
	cases := []struct {
		name      string
		filters   []sfFilter
		wantWhere string
		wantArgs  []any
	}{
		{"no filters", []sfFilter{{"l.project_id", ""}}, "", nil},
		{"one filter", []sfFilter{{"o.account_id", "a"}}, " WHERE o.account_id = $1", []any{"a"}},
		{"skips empty, numbers the rest", []sfFilter{{"l.project_id", ""}, {"l.opportunity_id", "b"}}, " WHERE l.opportunity_id = $1", []any{"b"}},
		{"both", []sfFilter{{"l.project_id", "a"}, {"l.opportunity_id", "b"}}, " WHERE l.project_id = $1 AND l.opportunity_id = $2", []any{"a", "b"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			where, args := buildSFWhere(tc.filters...)
			if where != tc.wantWhere || !reflect.DeepEqual(args, tc.wantArgs) {
				t.Errorf("got %q %v, want %q %v", where, args, tc.wantWhere, tc.wantArgs)
			}
		})
	}
}

func TestEntityRef(t *testing.T) {
	id, name, empty := "x", "Acme", ""
	if entityRef(nil, &name) != nil || entityRef(&empty, &name) != nil {
		t.Error("an absent join must give a nil reference")
	}
	if got := entityRef(&id, nil); !reflect.DeepEqual(got, &domain.EntityRef{ID: "x"}) {
		t.Errorf("got %+v", got)
	}
	if got := entityRef(&id, &name); !reflect.DeepEqual(got, &domain.EntityRef{ID: "x", Name: "Acme"}) {
		t.Errorf("got %+v", got)
	}
}
