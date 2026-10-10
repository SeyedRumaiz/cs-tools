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

package server

import (
	"errors"
	"log"
	"net/http"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/apierror"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/service"
)

// projectMemberOnly wraps a handler for a route shaped /projects/{id}/... so it
// only runs for a caller who may act on that project: an internal caller for
// any project, an external caller only for a project they are registered on.
// Anyone else gets 404 (never confirming the project exists); an identity that
// cannot be resolved gets 401/503 exactly as AccessService.ResolveScope reports
// it, and a malformed id gets 400.
//
// This is the authorization boundary for the project-contact reads, which have
// no row-level security and whose services take no caller scope. Unlike
// internalOnly it lets a customer read their own project's contacts, which the
// customer portal's Contacts tab needs.
func projectMemberOnly(access service.AccessService, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err := service.RequireProjectAccess(r.Context(), access, r.PathValue("id"))
		if err != nil {
			var ue *apierror.UnauthorizedError
			var sue *apierror.ServiceUnavailableError
			var fe *apierror.ForbiddenError
			var nfe *apierror.NotFoundError
			var ve *apierror.ValidationError
			switch {
			case errors.As(err, &nfe):
				apierror.WriteJSON(w, http.StatusNotFound, nfe.Msg)
			case errors.As(err, &ve):
				apierror.WriteJSON(w, http.StatusBadRequest, ve.Msg)
			case errors.As(err, &fe):
				apierror.WriteJSON(w, http.StatusForbidden, fe.Msg)
			case errors.As(err, &ue):
				apierror.WriteJSON(w, http.StatusUnauthorized, ue.Msg)
			case errors.As(err, &sue):
				apierror.WriteJSON(w, http.StatusServiceUnavailable, sue.Msg)
			default:
				log.Printf("projectMemberOnly: resolve scope for %s: %v", r.Method, err)
				apierror.WriteJSON(w, http.StatusInternalServerError, "internal server error")
			}
			return
		}
		next(w, r)
	}
}
