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
// KIND, either express or implied. See the License for the
// specific language governing permissions and limitations
// under the License.

package service

import (
	"errors"
	"testing"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/apierror"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
)

func TestSearchProjects_ValidationMessagesNameTheValue(t *testing.T) {
	cases := []struct {
		name string
		req  domain.SearchProjectsRequest
		want string
	}{
		{"excludeClosureStates lower-case", domain.SearchProjectsRequest{ExcludeClosureStates: []string{"Open", "suspended"}},
			`excludeClosureStates: "suspended" is not a valid closure state; use one of Open, Restricted, Suspended`},
		{"closureStatus", domain.SearchProjectsRequest{ClosureStatus: "Closed"},
			`closureStatus: "Closed" is not a valid closure state; use one of Open, Restricted, Suspended`},
		{"sortBy", domain.SearchProjectsRequest{SortBy: "name"},
			`sortBy: "name" is not a valid sort field; use endDate`},
		{"sortOrder", domain.SearchProjectsRequest{SortOrder: "DESC"},
			`sortOrder: "DESC" is not a valid sort order; use one of asc, desc`},
		{"endDateFrom", domain.SearchProjectsRequest{EndDateFrom: "2026/01/01"},
			`endDateFrom: "2026/01/01" is not a valid date; use yyyy-MM-dd`},
		{"endDateTo", domain.SearchProjectsRequest{EndDateTo: "31-12-2026"},
			`endDateTo: "31-12-2026" is not a valid date; use yyyy-MM-dd`},
		{"excludeSubscriptionTypes", domain.SearchProjectsRequest{ExcludeSubscriptionTypes: []domain.SubscriptionType{"Cloud Support"}},
			`excludeSubscriptionTypes: "Cloud Support" is not a valid subscription type; use one of cloud_evaluation_support, cloud_support, development_support, evaluation_subscription, internal, managed_cloud_subscription, platformer_subscription, professional_services, subscription`},
		{"arrTodayGte unsupported", domain.SearchProjectsRequest{ArrTodayGte: "100000"},
			`arrTodayGte is not supported on this data source yet: account ARR is not stored; remove the filter`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &recordingProjectRepo{}
			_, err := NewProjectService(repo, alwaysUnrestrictedAccess{}).SearchProjects(t.Context(), tc.req)
			var ve *apierror.ValidationError
			if !errors.As(err, &ve) || ve.Msg != tc.want {
				t.Fatalf("err = %v, want 400 %q", err, tc.want)
			}
			if repo.called {
				t.Fatal("repository must not be called for an invalid request")
			}
		})
	}
}

func TestSearchProjects_NewFiltersReachRepository(t *testing.T) {
	req := domain.SearchProjectsRequest{
		EndDateFrom: "2026-01-01", EndDateTo: "2026-12-31",
		OnboardingStatus: []string{"Completed", "In-Progress"}, SubRegion: "APAC",
	}
	repo := &recordingProjectRepo{}
	if _, err := NewProjectService(repo, alwaysUnrestrictedAccess{}).SearchProjects(t.Context(), req); err != nil {
		t.Fatalf("err = %v", err)
	}
	got := repo.gotReq
	if got.EndDateFrom != req.EndDateFrom || got.EndDateTo != req.EndDateTo || got.SubRegion != req.SubRegion ||
		len(got.OnboardingStatus) != 2 {
		t.Fatalf("repo got %+v, want filters unchanged", got)
	}
}
