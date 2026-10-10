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

package service

import (
	"testing"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
)

func sampleMappings() []domain.ProductRepoMapping {
	return []domain.ProductRepoMapping{
		{ProductName: "Acme Server", GithubLabel: "server"},
		{ProductName: "Acme Server Analytics", GithubLabel: "server-analytics"},
		{ProductName: "Acme Gateway", Abbreviation: strPtr("acmegw"), GithubLabel: "gateway"},
		{ProductName: "Kit", GithubLabel: "kit"},
		{ProductName: "Kite", GithubLabel: "kite"},
		{ProductName: "Acme Portal (Legacy)", GithubLabel: "portal"},
		{ProductName: "Acme Portal", GithubLabel: "portal-exact"},
		{ProductName: "WSO2 Developer Platform (Choreo)", GithubLabel: "choreo"},
	}
}

func TestMatchProductRepo(t *testing.T) {
	rows := sampleMappings()
	tests := []struct {
		name    string
		query   string
		want    string
		wantHit bool
	}{
		{name: "exact product name", query: "Acme Server", want: "server", wantHit: true},
		{name: "exact abbreviation", query: "acmegw", want: "gateway", wantHit: true},
		{name: "versioned name matches the product prefix", query: "Acme Server 6.0.0", want: "server", wantHit: true},
		{name: "longer prefix wins over the shorter one", query: "Acme Server Analytics 1.2.0", want: "server-analytics", wantHit: true},
		{name: "a name is not matched by a shorter name it starts with", query: "Kite", want: "kite", wantHit: true},
		{name: "short name is not a prefix of the catalogue name", query: "Server", wantHit: false},
		{name: "unknown product", query: "Not A Product", wantHit: false},
		{name: "blank", query: "  ", wantHit: false},
		{name: "exact stored-name row wins over an earlier row's canonical match, regardless of order", query: "Acme Portal", want: "portal-exact", wantHit: true},
		{name: "reported bug: Choreo's parenthetical-suffixed mapping matches the real catalogue name", query: "WSO2 Developer Platform", want: "choreo", wantHit: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := MatchProductRepo(rows, tt.query)
			if ok != tt.wantHit {
				t.Fatalf("hit=%v want %v", ok, tt.wantHit)
			}
			if ok && got.GithubLabel != tt.want {
				t.Fatalf("label=%q want %q", got.GithubLabel, tt.want)
			}
		})
	}
}
