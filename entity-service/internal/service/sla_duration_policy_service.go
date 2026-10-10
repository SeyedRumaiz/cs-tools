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
	"context"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/repository"
)

type slaDurationPolicyService struct {
	repo   repository.ReferenceDataRepository
	access AccessService
}

// NewSLADurationPolicyService constructs an SLADurationPolicyService backed
// by the given repository. access gates every call to internal callers --
// same reasoning as NewSLAStatusService's own doc comment: this is reference
// data for a backend service (csm-notification-service, fetched once at
// startup), not something any portal caller has a legitimate use for.
func NewSLADurationPolicyService(repo repository.ReferenceDataRepository, access AccessService) SLADurationPolicyService {
	return &slaDurationPolicyService{repo: repo, access: access}
}

// ListSLADurationPolicy implements SLADurationPolicyService.
func (s *slaDurationPolicyService) ListSLADurationPolicy(ctx context.Context) (domain.SLADurationPolicyResponse, error) {
	if err := RequireInternalCaller(ctx, s.access, "sla duration policy is only available to internal services"); err != nil {
		return domain.SLADurationPolicyResponse{}, err
	}
	rows, err := s.repo.ListSLADurationPolicy(ctx)
	if err != nil {
		return domain.SLADurationPolicyResponse{}, err
	}
	policies := make([]domain.SLADurationPolicyItem, 0, len(rows))
	for _, r := range rows {
		policies = append(policies, domain.SLADurationPolicyItem{
			Severity:        r.Severity,
			ClockType:       r.ClockType,
			DurationSeconds: r.DurationSeconds,
		})
	}
	return domain.SLADurationPolicyResponse{Policies: policies}, nil
}
