// Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com).
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

package csm

import (
	"encoding/json"
	"testing"
)

// entity-service's /services/search hit carries the service's supportGroup; it is where an alert-born
// incident is assigned, so it must survive decoding.
func TestSearchITServicesResponse_DecodesSupportGroup(t *testing.T) {
	raw := `{"services":[
		{"id":"s1","name":"Choreo Control Plane","supportGroup":{"id":"g1","name":"SRE - Apollo"}},
		{"id":"s2","name":"Choreo Control Plane API"}
	],"total":2}`
	var resp searchITServicesResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatal(err)
	}

	svc, ok := matchService(resp.Services, "choreo control plane")
	if !ok || svc.ID != "s1" {
		t.Fatalf("matchService = %+v, %v; want s1 (exact name, not the substring hit)", svc, ok)
	}
	if svc.SupportGroupID() != "g1" {
		t.Errorf("SupportGroupID = %q, want g1", svc.SupportGroupID())
	}
	if (ITService{ID: "s2"}).SupportGroupID() != "" {
		t.Error("a service with no support group must report none")
	}
}
