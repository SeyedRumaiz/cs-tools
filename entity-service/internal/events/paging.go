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

// Case Paging events.
//
// These go on the main shared topic (EVENT_HUB_TOPIC), not the incident
// topic: a test call belongs to no incident. The structs are duplicated in
// csm-notification-service by hand (separate Go modules), which decodes them
// strictly; keep the two in step and add a field there first.
package events

const (
	// TypePagingTestCallRequested: someone asked for a test call to a
	// person's paging-only phone number, from the Case Paging tab. The
	// consumer places the call and reports the outcome back
	// (PUT /team-schedule/paging-contacts/{userId}/test-result).
	TypePagingTestCallRequested Type = "paging.test_call_requested"
)

// PagingTestCallRequestedPayload is TypePagingTestCallRequested's payload.
// The envelope's EntityID is UserID.
type PagingTestCallRequestedPayload struct {
	UserID string `json:"userId"`
	Email  string `json:"email"`
	Name   string `json:"name"`
	// Phone is the paging-only number to call, E.164.
	Phone string `json:"phone"`
	// RequestedBy is the email of who asked for the test.
	RequestedBy string `json:"requestedBy"`
	// RequestedAt is when, RFC3339.
	RequestedAt string `json:"requestedAt"`
}
