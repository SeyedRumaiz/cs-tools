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

// Package tenant turns this bridge from single-tenant (hardcoded to one
// pinned WSO2 IS instance, see internal/introspect's own doc comment) into
// a real multi-tenant gateway: each tenant gets its own IdP validation
// config, allowed browser origins, and routing defaults, selected at
// request time by a path segment (see internal/middleware's tenant-
// resolution middleware) rather than compiled in.
//
// This is purely additive -- the legacy /support/chats path never consults
// this package's Table at all, and keeps using its own hardcoded env vars
// exactly as before (see cmd/server/main.go). Only the new /v1/{tenant}/...
// API resolves a tenant through here.
package tenant

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/wso2-open-operations/cs-tools/apps/console-chat-bridge/backend/internal/introspect"
	"github.com/wso2-open-operations/cs-tools/apps/console-chat-bridge/backend/internal/scim"
	"github.com/wso2-open-operations/cs-tools/apps/console-chat-bridge/backend/internal/tokenvalidator"
)

// Config is one tenant's full configuration: how to validate its bearer
// tokens, which browser origins may call it, and how its escalations should
// be tagged when routed to csm-portal/backend.
type Config struct {
	// Slug identifies this tenant in the URL path (/v1/{slug}/...) and as
	// the TENANT_REGISTRY row's first field. Case-sensitive, matched
	// exactly against the path segment.
	Slug string
	// ValidationType selects how this tenant's bearer tokens are checked:
	// "introspection" (RFC 7662, the only kind actually implemented today
	// -- see tokenvalidator.IntrospectionValidator) or "jwks" (accepted,
	// but tokenvalidator.JWKSValidator always fails -- see that type's own
	// doc comment). Defaults to "introspection" when blank.
	ValidationType string
	// Issuer is this tenant's trusted token issuer -- for introspection,
	// also the base used to derive the introspection endpoint when
	// IntrospectionURL is blank (mirrors introspect.Config.IssuerBaseURL).
	Issuer string
	// IntrospectionURL overrides the derived introspection endpoint --
	// required for an IdP (e.g. Asgardeo) whose introspection endpoint
	// doesn't live under Issuer+"/oauth2/introspect".
	IntrospectionURL string
	// UserinfoURL overrides the derived OIDC UserInfo endpoint used by
	// introspect.Validator's userinfo fallback (see that package's own doc
	// comment on ValidateBearer) -- required for an IdP whose userinfo
	// endpoint doesn't live under Issuer+"/oauth2/userinfo". Optional even
	// for an explicit TENANT_REGISTRY row: left blank, introspect.Validator
	// derives it from Issuer itself, the same way IntrospectionURL's own
	// blank case works.
	UserinfoURL string
	// JWKSURI/Audience configure a "jwks" tenant -- unused for
	// "introspection".
	JWKSURI  string
	Audience string
	// ClientID authenticates this bridge to the tenant's introspection
	// endpoint via HTTP Basic auth (RFC 7662 §2.1); the matching secret is
	// never stored here -- see BuildTable's own doc comment.
	ClientID string
	// InsecureSkipVerify disables TLS certificate verification for calls
	// to this tenant's IdP -- LOCAL DEVELOPMENT ONLY, mirrors
	// introspect.Config.InsecureSkipVerify's own doc comment.
	InsecureSkipVerify bool
	// AllowedOrigins is this tenant's own CORS allow-list -- see
	// internal/middleware.CORS, which reads this instead of the legacy
	// path's single global CORS_ALLOWED_ORIGINS list once a tenant has
	// been resolved.
	AllowedOrigins []string
	// RoutingSource/Channel/ProjectID tag every case this tenant escalates
	// -- RoutingSource becomes chat-routing-service's CaseInfo.Source (and
	// so csm-portal/backend's chatNotifiers routing key -- see that
	// service's own dual "asgardeo"/"console-chat-bridge" registration),
	// Channel becomes CaseInfo.Channel, ProjectID becomes CaseInfo.
	// ProjectID (the duplicate-open-chat scope -- see chat-routing-
	// service's CreateWorkItem doc comment). RoutingSource defaults to
	// BuildTable's defaultRoutingSource when left blank in a registry row.
	RoutingSource string
	Channel       string
	ProjectID     string
}

// Tenant pairs a Config with the TokenValidator built from it, constructed
// once at startup (BuildTable) rather than per-request.
type Tenant struct {
	Config
	Validator tokenvalidator.TokenValidator
}

// Table looks tenants up by Slug -- the resolved value of a /v1/{tenant}/...
// path segment.
type Table map[string]*Tenant

// Lookup returns slug's Tenant, or ok=false if no such tenant is
// configured.
func (t Table) Lookup(slug string) (*Tenant, bool) {
	tt, ok := t[slug]
	return tt, ok
}

// SecretLookup resolves a tenant's IdP client secret, keyed by Slug. The
// registry itself never carries a secret (see BuildTable's own doc
// comment) -- this exists as an interface, rather than a hardcoded env
// lookup, only so tests can supply a fake without touching real process
// environment variables.
type SecretLookup func(slug string) string

// registryFieldCountMin/Max bound how many "|"-delimited fields one
// TENANT_REGISTRY row has -- see BuildTable's own doc comment for the
// order. Max's one extra field (UserinfoURL) is optional so every row
// written before it existed keeps parsing unchanged.
const (
	registryFieldCountMin = 12
	registryFieldCountMax = 13
)

// LegacyConfig carries the pre-multi-tenant env vars /support/chats already
// uses (cmd/server/main.go's KNOWN_ISSUER_BASE_URL/INTROSPECTION_CLIENT_ID/
// INTROSPECTION_CLIENT_SECRET/INTROSPECTION_INSECURE_SKIP_VERIFY/
// CORS_ALLOWED_ORIGINS), reused unchanged to synthesize the "identity-
// console" fallback tenant when TENANT_REGISTRY is unset -- see
// BuildTable's own doc comment. Passing the legacy client secret through
// here (rather than via SecretLookup, which only covers explicit registry
// rows) keeps this fallback path independent of TENANT_<SLUG>_CLIENT_SECRET
// naming.
type LegacyConfig struct {
	IssuerBaseURL      string
	ClientID           string
	ClientSecret       string
	InsecureSkipVerify bool
	AllowedOrigins     []string
	// SCIM* configure this tenant's SCIM-based canonical-Subject resolver
	// (see internal/scim and tokenvalidator.IntrospectionValidator.
	// WithSCIMResolver), used instead of introspect.Validator's UserInfo
	// fallback whenever introspection alone doesn't carry a Subject.
	//
	// SCIMClientID left blank (the default -- these are all optional, and
	// SCIM is never required) means SCIM is not configured: this tenant
	// keeps using ResolveSubjectViaUserinfo exactly as before, unaffected.
	//
	// Deliberately a SEPARATE, least-privilege client-credentials
	// registration from ClientID/ClientSecret above (which only ever does
	// RFC 7662 Basic-auth introspection) -- this one needs a
	// client-credentials-grant token carrying WSO2 IS's SCIM2-view scope,
	// nothing else. SCIMBaseURL/SCIMTokenURL default to IssuerBaseURL and
	// IssuerBaseURL+"/oauth2/token" respectively when left blank, since
	// SCIM2 and the token endpoint both live on the same WSO2 IS instance
	// as introspection for every tenant this applies to today.
	SCIMBaseURL      string
	SCIMTokenURL     string
	SCIMClientID     string
	SCIMClientSecret string
	SCIMScopes       []string
}

// legacySlug is the fallback tenant's Slug -- reachable at
// /v1/identity-console/... once BuildTable synthesizes it.
const legacySlug = "identity-console"

// legacyRoutingSource/legacyChannel/legacyProjectID match this bridge's
// existing /support/chats constants (sourceAsgardeo/channelAskAI/
// consoleProjectID in internal/handler/chats.go) exactly, so a case raised
// through the fallback tenant's /v1/identity-console/... path routes
// identically to one raised through the legacy path.
const (
	legacyRoutingSource = "asgardeo"
	legacyChannel       = "ask-ai"
	legacyProjectID     = "console-ask-ai"
)

// BuildTable parses TENANT_REGISTRY's raw value into a Table, constructing
// one TokenValidator per row -- or, when raw is blank, synthesizes a single
// "identity-console" row from legacy (today's pre-multi-tenant env vars),
// so /v1/identity-console/... works out of the box without requiring a
// separate TENANT_REGISTRY setup on top of what /support/chats already has
// configured.
//
// Row syntax: rows separated by ";", fields by "|", 12 or 13 fields per
// row, in this order:
//
//	slug|validationType|issuer|introspectionURL|jwksURI|audience|clientID|insecureSkipVerify|allowedOrigins|routingSource|channel|projectID|userinfoURL
//
// userinfoURL (field 13) is optional -- a 12-field row (every row written
// before this field existed) parses exactly as before, with UserinfoURL
// left blank so introspect.Validator derives one from issuer itself (see
// Config.UserinfoURL's own doc comment).
//
// allowedOrigins is itself "," separated when it carries more than one
// origin (a nested list inside one "|"-delimited field, the same flat-
// delimited-config convention csm-portal/backend's own CSM_TEAM_REGISTRY
// uses). validationType is "introspection" (default when blank) or "jwks",
// case-insensitive. insecureSkipVerify is the literal string "true" or
// anything else (including blank) for false. Blank routingSource inherits
// defaultRoutingSource (BRIDGE_ROUTING_SOURCE).
//
// Client secrets are never in this registry -- Choreo's config UI and this
// registry's own row-based design are both a poor fit for a value that
// must stay confidential, so each row's secret is instead resolved via
// secretLookup(slug), which defaults to reading
// TENANT_<SLUG_UPPER_SNAKE>_CLIENT_SECRET from the process environment when
// nil.
func BuildTable(raw, defaultRoutingSource string, secretLookup SecretLookup, legacy LegacyConfig) (Table, error) {
	if secretLookup == nil {
		secretLookup = EnvSecretLookup
	}

	if strings.TrimSpace(raw) == "" {
		t, err := buildLegacyTenant(legacy)
		if err != nil {
			return nil, err
		}
		return Table{t.Slug: t}, nil
	}

	table := Table{}
	rows := strings.Split(raw, ";")
	for i, row := range rows {
		row = strings.TrimSpace(row)
		if row == "" {
			continue
		}
		fields := strings.Split(row, "|")
		if len(fields) < registryFieldCountMin || len(fields) > registryFieldCountMax {
			return nil, fmt.Errorf("tenant: TENANT_REGISTRY row %d: expected %d or %d fields, got %d", i+1, registryFieldCountMin, registryFieldCountMax, len(fields))
		}
		for j := range fields {
			fields[j] = strings.TrimSpace(fields[j])
		}

		slug := fields[0]
		if slug == "" {
			return nil, fmt.Errorf("tenant: TENANT_REGISTRY row %d: slug is required", i+1)
		}
		if _, dup := table[slug]; dup {
			return nil, fmt.Errorf("tenant: TENANT_REGISTRY row %d: duplicate slug %q", i+1, slug)
		}

		var allowedOrigins []string
		if fields[8] != "" {
			for _, o := range strings.Split(fields[8], ",") {
				if o = strings.TrimSpace(o); o != "" {
					allowedOrigins = append(allowedOrigins, o)
				}
			}
		}
		routingSource := fields[9]
		if routingSource == "" {
			routingSource = defaultRoutingSource
		}
		var userinfoURL string
		if len(fields) == registryFieldCountMax {
			userinfoURL = fields[12]
		}

		cfg := Config{
			Slug:               slug,
			ValidationType:     strings.ToLower(fields[1]),
			Issuer:             fields[2],
			IntrospectionURL:   fields[3],
			JWKSURI:            fields[4],
			Audience:           fields[5],
			ClientID:           fields[6],
			InsecureSkipVerify: fields[7] == "true",
			AllowedOrigins:     allowedOrigins,
			RoutingSource:      routingSource,
			Channel:            fields[10],
			ProjectID:          fields[11],
			UserinfoURL:        userinfoURL,
		}

		validator, err := buildValidator(cfg, secretLookup(slug))
		if err != nil {
			return nil, fmt.Errorf("tenant: TENANT_REGISTRY row %d (%s): %w", i+1, slug, err)
		}
		table[slug] = &Tenant{Config: cfg, Validator: validator}
	}
	if len(table) == 0 {
		return nil, errors.New("tenant: TENANT_REGISTRY is set but contains no rows")
	}
	return table, nil
}

// EnvSecretLookup is BuildTable's default SecretLookup: reads
// TENANT_<SLUG_UPPER_SNAKE>_CLIENT_SECRET from the process environment,
// upper-casing slug and replacing every character outside [A-Z0-9] with
// "_" to form a valid env var name (e.g. slug "acme-corp" ->
// TENANT_ACME_CORP_CLIENT_SECRET).
func EnvSecretLookup(slug string) string {
	return os.Getenv("TENANT_" + envSlug(slug) + "_CLIENT_SECRET")
}

func envSlug(slug string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r - ('a' - 'A')
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		default:
			return '_'
		}
	}, slug)
}

func buildValidator(cfg Config, secret string) (tokenvalidator.TokenValidator, error) {
	switch cfg.ValidationType {
	case "", "introspection":
		if cfg.Issuer == "" {
			return nil, errors.New("issuer is required for validationType introspection")
		}
		v := introspect.NewValidator(introspect.Config{
			IssuerBaseURL:             cfg.Issuer,
			IntrospectionURL:          cfg.IntrospectionURL,
			UserinfoURL:               cfg.UserinfoURL,
			IntrospectionClientID:     cfg.ClientID,
			IntrospectionClientSecret: secret,
			InsecureSkipVerify:        cfg.InsecureSkipVerify,
		})
		return tokenvalidator.NewIntrospectionValidator(v), nil
	case "jwks":
		return tokenvalidator.NewJWKSValidator(tokenvalidator.JWKSValidatorConfig{
			JWKSURI: cfg.JWKSURI, Issuer: cfg.Issuer, Audience: cfg.Audience,
		}), nil
	default:
		return nil, fmt.Errorf("unknown validationType %q (want \"introspection\" or \"jwks\")", cfg.ValidationType)
	}
}

func buildLegacyTenant(legacy LegacyConfig) (*Tenant, error) {
	if legacy.IssuerBaseURL == "" {
		return nil, errors.New("tenant: TENANT_REGISTRY is unset and KNOWN_ISSUER_BASE_URL is empty -- cannot build the identity-console fallback tenant")
	}
	cfg := Config{
		Slug:               legacySlug,
		ValidationType:     "introspection",
		Issuer:             legacy.IssuerBaseURL,
		IntrospectionURL:   strings.TrimRight(legacy.IssuerBaseURL, "/") + "/oauth2/introspect",
		UserinfoURL:        strings.TrimRight(legacy.IssuerBaseURL, "/") + "/oauth2/userinfo",
		ClientID:           legacy.ClientID,
		InsecureSkipVerify: legacy.InsecureSkipVerify,
		AllowedOrigins:     legacy.AllowedOrigins,
		RoutingSource:      legacyRoutingSource,
		Channel:            legacyChannel,
		ProjectID:          legacyProjectID,
	}
	v := introspect.NewValidator(introspect.Config{
		IssuerBaseURL:             cfg.Issuer,
		IntrospectionURL:          cfg.IntrospectionURL,
		UserinfoURL:               cfg.UserinfoURL,
		IntrospectionClientID:     cfg.ClientID,
		IntrospectionClientSecret: legacy.ClientSecret,
		InsecureSkipVerify:        cfg.InsecureSkipVerify,
	})
	iv := tokenvalidator.NewIntrospectionValidator(v)
	if legacy.SCIMClientID != "" {
		scimBaseURL := legacy.SCIMBaseURL
		if scimBaseURL == "" {
			scimBaseURL = legacy.IssuerBaseURL
		}
		scimTokenURL := legacy.SCIMTokenURL
		if scimTokenURL == "" {
			scimTokenURL = strings.TrimRight(legacy.IssuerBaseURL, "/") + "/oauth2/token"
		}
		iv = iv.WithSCIMResolver(scim.NewClient(scim.Config{
			BaseURL:            scimBaseURL,
			TokenURL:           scimTokenURL,
			ClientID:           legacy.SCIMClientID,
			ClientSecret:       legacy.SCIMClientSecret,
			Scopes:             legacy.SCIMScopes,
			InsecureSkipVerify: legacy.InsecureSkipVerify,
		}))
	}
	return &Tenant{Config: cfg, Validator: iv}, nil
}

type contextKey string

const tenantContextKey contextKey = "tenant"

// WithTenant returns a context carrying t -- set by internal/middleware's
// tenant-resolution middleware after a successful Table.Lookup.
func WithTenant(ctx context.Context, t *Tenant) context.Context {
	return context.WithValue(ctx, tenantContextKey, t)
}

// FromContext retrieves the Tenant WithTenant stored, or ok=false if the
// current request never resolved one (e.g. a legacy /support/chats
// request, which never runs the tenant-resolution middleware at all).
func FromContext(ctx context.Context) (*Tenant, bool) {
	t, ok := ctx.Value(tenantContextKey).(*Tenant)
	return t, ok
}
