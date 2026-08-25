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
	"context"
	"fmt"

	"github.com/wso2-open-operations/cs-tools/apps/customer-portal/backend-v2/internal/usermanagement"
)

// mockContactsClient is a no-op contactsClient used only when
// USER_MANAGEMENT_LOCAL_MOCK=true. The real project-contact onboarding
// service is keyed on a project's Salesforce ID and knows nothing about
// projects seeded directly into local Postgres, so calling it in that setup
// always fails. Reads return an empty result instead of erroring; writes
// are rejected with a clear message rather than pretending to succeed.
type mockContactsClient struct{}

// NewMockContactsClient returns a contactsClient that never calls the real
// project-contact onboarding service. Local development only.
func NewMockContactsClient() contactsClient {
	return mockContactsClient{}
}

func (mockContactsClient) GetProjectContacts(ctx context.Context, projectID string) ([]usermanagement.Contact, error) {
	return []usermanagement.Contact{}, nil
}

func (mockContactsClient) CreateProjectContact(ctx context.Context, projectID string, req usermanagement.OnBoardContactPayload) (usermanagement.Membership, error) {
	return usermanagement.Membership{}, fmt.Errorf("contact management is disabled locally (USER_MANAGEMENT_LOCAL_MOCK=true)")
}

func (mockContactsClient) RemoveProjectContact(ctx context.Context, projectID, contactEmail, adminEmail string) (usermanagement.Membership, error) {
	return usermanagement.Membership{}, fmt.Errorf("contact management is disabled locally (USER_MANAGEMENT_LOCAL_MOCK=true)")
}

func (mockContactsClient) UpdateMembershipRole(ctx context.Context, projectID, contactEmail string, req usermanagement.MembershipRolePayload) (usermanagement.Membership, error) {
	return usermanagement.Membership{}, fmt.Errorf("contact management is disabled locally (USER_MANAGEMENT_LOCAL_MOCK=true)")
}

func (mockContactsClient) ValidateProjectContact(ctx context.Context, req usermanagement.ValidationPayload) (*usermanagement.Contact, bool, error) {
	return nil, false, fmt.Errorf("contact management is disabled locally (USER_MANAGEMENT_LOCAL_MOCK=true)")
}
