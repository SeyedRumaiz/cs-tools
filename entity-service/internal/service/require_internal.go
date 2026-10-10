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

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/apierror"
)

// RequireInternalCaller is the single "internal callers only" check: it
// resolves the caller's AccessScope and returns a ForbiddenError carrying
// forbiddenMsg unless the scope is Unrestricted (an allow-listed internal
// client, or a user token whose active user rows are all INTERNAL). An
// identity that cannot be resolved is returned as ResolveScope reports it
// (Unauthorized / ServiceUnavailable), never downgraded to Forbidden.
//
// Every service-level requireInternalCaller and the route-level internalOnly
// middleware (internal/server) delegate here, so the rule lives in one place.
func RequireInternalCaller(ctx context.Context, access AccessService, forbiddenMsg string) error {
	scope, err := access.ResolveScope(ctx)
	if err != nil {
		return err
	}
	if !scope.Unrestricted {
		return &apierror.ForbiddenError{Msg: forbiddenMsg}
	}
	return nil
}

// RequireProjectAccess is the "this caller may act on this project" check as a
// reusable primitive for route-level guards: internal callers pass for any
// project, an external caller only for a project they are registered on (a
// project they are not on is reported as NotFound, never confirming it exists),
// and a malformed id is a ValidationError. It delegates to authorizeProject, the
// same check the project, case and stats services already make.
func RequireProjectAccess(ctx context.Context, access AccessService, projectID string) error {
	_, err := authorizeProject(ctx, access, projectID)
	return err
}
