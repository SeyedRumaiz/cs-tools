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

package assistant

import (
	"fmt"
	"strings"
	"time"
)

// Providers is each tenant's assistant, falling back to the bridge-wide
// default. A nil Provider means the tenant has none.
type Providers struct {
	def      Provider
	byTenant map[string]Provider
}

// For returns tenantSlug's assistant, or nil.
func (p Providers) For(tenantSlug string) Provider {
	if provider, ok := p.byTenant[tenantSlug]; ok {
		return provider
	}
	return p.def
}

// Describe names each tenant's provider, for the startup log.
func (p Providers) Describe(tenantSlugs []string) map[string]string {
	out := map[string]string{}
	for _, slug := range tenantSlugs {
		out[slug] = kindOf(p.For(slug))
	}
	return out
}

func kindOf(p Provider) string {
	switch p.(type) {
	case nil:
		return "off"
	case *Novera:
		return "novera"
	case *HTTP:
		return "http"
	case Mock:
		return "mock"
	default:
		return fmt.Sprintf("%T", p)
	}
}

// FromEnv reads the assistant settings. ASSISTANT_PROVIDER is the default
// and TENANT_<SLUG>_ASSISTANT_PROVIDER a tenant's own choice, each one of:
//
//	novera  the Novera support assistant, from the bridge-wide NOVERA_*
//	        settings; the default when NOVERA_WS_BASE_URL is set
//	http    a product's own AI service: <prefix>HTTP_URL, and for auth
//	        <prefix>HTTP_TOKEN or <prefix>HTTP_TOKEN_URL, _CLIENT_ID,
//	        _CLIENT_SECRET and _SCOPES
//	mock    a stand-in for local development
//	off     no assistant
//
// where <prefix> is ASSISTANT_ or TENANT_<SLUG>_ASSISTANT_.
// ASSISTANT_DISABLED_TENANTS lists tenants that have none. tenants maps
// each tenant slug to its <SLUG> form.
func FromEnv(getenv func(string) string, tenants map[string]string) (Providers, error) {
	var novera *Novera
	build := func(kind, prefix string) (Provider, error) {
		switch strings.ToLower(strings.TrimSpace(kind)) {
		case "novera":
			if getenv("NOVERA_WS_BASE_URL") == "" || getenv("NOVERA_TOKEN_URL") == "" {
				return nil, fmt.Errorf("novera needs NOVERA_WS_BASE_URL and NOVERA_TOKEN_URL")
			}
			if novera == nil {
				novera = NewNovera(NoveraConfig{
					WSBaseURL:    getenv("NOVERA_WS_BASE_URL"),
					TokenURL:     getenv("NOVERA_TOKEN_URL"),
					ClientID:     getenv("NOVERA_CLIENT_ID"),
					ClientSecret: getenv("NOVERA_CLIENT_SECRET"),
					Scopes:       splitList(getenv("NOVERA_SCOPES")),
				})
			}
			return novera, nil
		case "http":
			url := getenv(prefix + "HTTP_URL")
			if url == "" {
				return nil, fmt.Errorf("http needs %sHTTP_URL", prefix)
			}
			return NewHTTP(HTTPConfig{
				URL:          url,
				Token:        getenv(prefix + "HTTP_TOKEN"),
				TokenURL:     getenv(prefix + "HTTP_TOKEN_URL"),
				ClientID:     getenv(prefix + "HTTP_CLIENT_ID"),
				ClientSecret: getenv(prefix + "HTTP_CLIENT_SECRET"),
				Scopes:       splitList(getenv(prefix + "HTTP_SCOPES")),
			}), nil
		case "mock":
			return Mock{Delay: 40 * time.Millisecond}, nil
		case "", "off":
			return nil, nil
		default:
			return nil, fmt.Errorf("unknown assistant provider %q", kind)
		}
	}

	defaultKind := getenv("ASSISTANT_PROVIDER")
	if defaultKind == "" && getenv("NOVERA_WS_BASE_URL") != "" {
		defaultKind = "novera"
	}
	def, err := build(defaultKind, "ASSISTANT_")
	if err != nil {
		return Providers{}, fmt.Errorf("ASSISTANT_PROVIDER: %w", err)
	}

	byTenant := map[string]Provider{}
	for slug, envSlug := range tenants {
		prefix := "TENANT_" + envSlug + "_ASSISTANT_"
		kind := getenv(prefix + "PROVIDER")
		if kind == "" {
			continue
		}
		provider, err := build(kind, prefix)
		if err != nil {
			return Providers{}, fmt.Errorf("%sPROVIDER: %w", prefix, err)
		}
		byTenant[slug] = provider
	}
	for _, slug := range splitList(getenv("ASSISTANT_DISABLED_TENANTS")) {
		byTenant[slug] = nil
	}
	return Providers{def: def, byTenant: byTenant}, nil
}

func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
