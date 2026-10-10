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

// Package risk is an entity-service-backed client for customer-health/
// risk-tracking: project health review state, risk open/close history, and
// the action items and comments attached to each risk. Until this was
// rewired, this package talked directly to a standalone MySQL database
// (apps/csm-portal/backend's own former modules/risk-equivalent); that
// database has been migrated into entity-service's own Postgres (migration
// 0219 in that repo), and this package's job is now purely to call
// entity-service's REST endpoints and reshape the result, not to run SQL of
// its own. Every exported type/method signature here is unchanged or
// minimally adjusted (see types.go's own doc comment on ID types) from the
// MySQL-backed version, so internal/handler/customer_health*.go needed no
// restructuring beyond the id-type change.
package risk

import (
	"regexp"
	"strings"

	"github.com/wso2-open-operations/cs-tools/apps/csm-portal/backend/internal/entity"
)

// Client wraps entity-service's customer-health endpoints.
type Client struct {
	entity *entity.CustomerEntityClient
}

// NewClient constructs a Client backed by the given entity-service client.
// Unlike the old MySQL-backed constructor, this never fails or makes a
// network call of its own -- entityClient is already a live, shared
// dependency constructed (and health-checked implicitly by every other
// route that uses it) elsewhere in main.go.
func NewClient(entityClient *entity.CustomerEntityClient) *Client {
	return &Client{entity: entityClient}
}

// hexSysIDRe matches a 32-character lowercase/uppercase hex upstream sys_id
// with no dashes -- the shape every projectSysId/accountSysId this package
// receives from the portal frontend has.
var hexSysIDRe = regexp.MustCompile(`^[0-9a-fA-F]{32}$`)

// sysIDToUUID reformats a 32-character upstream sys_id into the dashed UUID
// syntax entity-service's Postgres actually stores it as -- confirmed in
// entity-service's own CLAUDE.md: "The services and groups are SN sys_ids as
// Postgres UUIDs", and true of every upstream-sourced row in that schema,
// project and account included. A value that isn't a well-formed 32-hex
// sys_id is returned unchanged (entity-service's own UUID validation will
// reject it with a clear 400 rather than this silently mangling it).
func sysIDToUUID(sysID string) string {
	if !hexSysIDRe.MatchString(sysID) {
		return sysID
	}
	return sysID[0:8] + "-" + sysID[8:12] + "-" + sysID[12:16] + "-" + sysID[16:20] + "-" + sysID[20:32]
}

// uuidToSysID is sysIDToUUID's inverse: strips the dashes out of a UUID to
// recover the original 32-character sys_id shape the portal frontend (and
// every other customer-health response field) expects. A value with no
// dashes is returned unchanged.
func uuidToSysID(uuid string) string {
	return strings.ReplaceAll(uuid, "-", "")
}
