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
	"regexp"
	"strings"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/apierror"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
	"github.com/wso2-open-operations/cs-tools/entity-service/internal/repository"
)

// trailingParenRE matches a trailing "(...)" annotation on a product name,
// e.g. the " (Choreo)" in "WSO2 Developer Platform (Choreo)".
var trailingParenRE = regexp.MustCompile(`\s*\([^()]*\)\s*$`)

// canonicalProductName strips one trailing parenthetical annotation from
// name. Some product_repo_mapping rows carry a human disambiguation suffix
// that was never part of the real product catalogue name (e.g. the mapping
// is "WSO2 Developer Platform (Choreo)" but real cases carry the product as
// plain "WSO2 Developer Platform") -- every rule in MatchProductRepo below
// fails against a row like that since the stored name is longer than, not a
// prefix of, the real one. Returning this as a second candidate name lets
// such a row match the real catalogue name without changing matching for
// any row whose name has no such suffix.
func canonicalProductName(name string) string {
	return strings.TrimSpace(trailingParenRE.ReplaceAllString(name, ""))
}

// ProductRepoMappingService resolves a case product name to a GitHub repo.
type ProductRepoMappingService struct {
	repo *repository.ProductRepoMappingRepository
}

// NewProductRepoMappingService returns a service bound to repo.
func NewProductRepoMappingService(repo *repository.ProductRepoMappingRepository) *ProductRepoMappingService {
	return &ProductRepoMappingService{repo: repo}
}

// Find returns the mapping for name.
// An exact product name wins, then an exact abbreviation, then the longest
// product name that is a prefix of name followed by a space (a version).
func (s *ProductRepoMappingService) Find(ctx context.Context, name string) (domain.ProductRepoMapping, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return domain.ProductRepoMapping{}, &apierror.ValidationError{Msg: "Product name is required."}
	}
	rows, err := s.repo.ListActive(ctx)
	if err != nil {
		return domain.ProductRepoMapping{}, err
	}
	found, ok := MatchProductRepo(rows, name)
	if !ok {
		return domain.ProductRepoMapping{}, &apierror.NotFoundError{Msg: "No GitHub repository is mapped for this product."}
	}
	return found, nil
}

// MatchProductRepo applies the lookup rules to an already-loaded catalogue.
func MatchProductRepo(rows []domain.ProductRepoMapping, name string) (domain.ProductRepoMapping, bool) {
	query := strings.TrimSpace(name)
	if query == "" {
		return domain.ProductRepoMapping{}, false
	}
	folded := strings.ToLower(query)

	// candidateNames returns the row's stored name plus its canonical
	// (parenthetical-stripped) form when the two differ. Used by the prefix
	// pass below, where "longest candidate wins" is already order-independent.
	candidateNames := func(row domain.ProductRepoMapping) []string {
		product := strings.TrimSpace(row.ProductName)
		names := []string{product}
		if canon := canonicalProductName(product); canon != "" && !strings.EqualFold(canon, product) {
			names = append(names, canon)
		}
		return names
	}

	// Exact stored name wins first, across every row, before any row's
	// canonical name is even considered -- checking both forms row-by-row
	// would let an earlier row's canonical match shadow a later row's own
	// exact stored-name match.
	for _, row := range rows {
		if strings.EqualFold(strings.TrimSpace(row.ProductName), query) {
			return row, true
		}
	}
	for _, row := range rows {
		product := strings.TrimSpace(row.ProductName)
		if canon := canonicalProductName(product); canon != "" && !strings.EqualFold(canon, product) && strings.EqualFold(canon, query) {
			return row, true
		}
	}
	for _, row := range rows {
		if row.Abbreviation != nil && strings.EqualFold(strings.TrimSpace(*row.Abbreviation), query) {
			return row, true
		}
	}

	var best domain.ProductRepoMapping
	bestLen := -1
	for _, row := range rows {
		for _, candidate := range candidateNames(row) {
			prefix := strings.ToLower(candidate) + " "
			if strings.HasPrefix(folded, prefix) && len(candidate) > bestLen {
				best = row
				bestLen = len(candidate)
			}
		}
	}
	if bestLen < 0 {
		return domain.ProductRepoMapping{}, false
	}
	return best, true
}
