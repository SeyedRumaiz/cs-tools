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

// Package validate holds tiny, dependency-free validation helpers shared
// across layers that would otherwise each keep their own copy of the same
// pattern, with no mechanism to notice the two drifting apart.
package validate

import "regexp"

// UUIDPattern matches a well-formed UUID (any version/variant), e.g.
// "11111111-1111-1111-1111-111111111111". The one definition every package
// that needs UUID-shaped validation imports (internal/service's
// request-body id validation, internal/config's escalation group id
// validation, ...) instead of each keeping its own copy that has to be kept
// in sync by hand.
var UUIDPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// IsUUID reports whether s is a well-formed UUID string.
func IsUUID(s string) bool {
	return UUIDPattern.MatchString(s)
}
