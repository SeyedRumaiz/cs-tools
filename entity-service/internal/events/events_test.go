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

package events

import (
	"encoding/json"
	"strings"
	"testing"
)

// csm-notification-service decodes the resend marker as "isResend"; a
// different name here silently turns every portal resend into an ordinary
// invitation that its duplicate guard then skips.
func TestProjectContactInvitedPayload_ResendWireName(t *testing.T) {
	raw, err := json.Marshal(ProjectContactInvitedPayload{Resend: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"isResend":true`) {
		t.Errorf("payload = %s, want the resend marker as \"isResend\"", raw)
	}

	raw, err = json.Marshal(ProjectContactInvitedPayload{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "esend") {
		t.Errorf("payload = %s, an ordinary invitation must omit the resend marker", raw)
	}
}

// csm-notification-service decodes this payload with DisallowUnknownFields,
// so the wire names are pinned here.
func TestProjectContactRegisteredPayload_WireNames(t *testing.T) {
	raw, err := json.Marshal(ProjectContactRegisteredPayload{IsIntegrationUser: true, EventModifiedOn: "2026-09-18T06:37:07Z"})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	want := []string{"membershipSfId", "contactSfId", "email", "givenName", "familyName", "projectName", "projectKey", "isIntegrationUser", "eventModifiedOn"}
	if len(got) != len(want) {
		t.Errorf("payload = %s, want exactly %v", raw, want)
	}
	for _, k := range want {
		if _, ok := got[k]; !ok {
			t.Errorf("payload = %s, missing %q", raw, k)
		}
	}
}
