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

// Package handler is declared in user_handler.go.
package handler

import (
	"encoding/json"
	"net/http"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/service"
)

// ProductRepoMappingHandler serves GET /products/github-repo.
type ProductRepoMappingHandler struct {
	svc *service.ProductRepoMappingService
}

// NewProductRepoMappingHandler returns a handler bound to svc.
func NewProductRepoMappingHandler(svc *service.ProductRepoMappingService) *ProductRepoMappingHandler {
	return &ProductRepoMappingHandler{svc: svc}
}

// Get handles GET /products/github-repo?name=
func (h *ProductRepoMappingHandler) Get(w http.ResponseWriter, r *http.Request) {
	mapping, err := h.svc.Find(r.Context(), r.URL.Query().Get("name"))
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(mapping)
}
