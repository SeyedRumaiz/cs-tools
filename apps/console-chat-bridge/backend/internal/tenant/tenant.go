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

// Package tenant resolves a request's /v1/{tenant}/... path segment to that
// tenant's IdP validation config, allowed origins, and routing defaults.
// The legacy /support/chats path never consults this package.
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
	// ValidationType is "introspection" (default, validates an Opaque
	// token via RFC 7662) or "jwks" (verifies a JWT's signature against
	// JWKSURI — see tokenvalidator.JWKSValidator).
	ValidationType string
	// Issuer is this tenant's trusted token issuer, and the base used to
	// derive the introspection endpoint when IntrospectionURL is blank.
	Issuer string
	// IntrospectionURL overrides the derived introspection endpoint --
	// required for an IdP (e.g. Asgardeo) whose introspection endpoint
	// doesn't live under Issuer+"/oauth2/introspect".
	IntrospectionURL string
	// UserinfoURL overrides the derived OIDC UserInfo endpoint used by the
	// userinfo fallback (see introspect.Validator.ValidateBearer) --
	// required when it doesn't live under Issuer+"/oauth2/userinfo".
	UserinfoURL string
	// JWKSURI/Audience configure a "jwks" tenant — unused for
	// "introspection".
	JWKSURI  string
	Audience string
	// ClientID authenticates this bridge to the tenant's introspection
	// endpoint via HTTP Basic auth (RFC 7662 §2.1); the matching secret is
	// never stored here — see BuildTable's own doc comment.
	ClientID string
	// InsecureSkipVerify disables TLS certificate verification for calls
	// to this tenant's IdP — LOCAL DEVELOPMENT ONLY, mirrors
	// introspect.Config.InsecureSkipVerify's own doc comment.
	InsecureSkipVerify bool
	// AllowedOrigins is this tenant's CORS allow-list, used by
	// middleware.CORS once a tenant is resolved.
	AllowedOrigins []string
	// RoutingSource, Channel, and ProjectID tag every case this tenant
	// escalates, becoming CaseInfo.Source/Channel/ProjectID for
	// chat-routing-service (ProjectID scopes the duplicate-open-chat
	// check). RoutingSource defaults to BuildTable's defaultRoutingSource
	// when blank.
	RoutingSource string
	Channel       string
	ProjectID     string
	// SCIM* configure this tenant's SCIM-based canonical-Subject resolver
	// (see internal/scim and tokenvalidator.IntrospectionValidator.
	// WithSCIMResolver), used instead of the UserInfo fallback whenever
	// introspection alone doesn't carry a Subject. SCIMClientID blank (the
	// default) means SCIM is not configured; ResolveSubjectViaUserinfo is
	// used instead. Must be a separate, least-privilege client-credentials
	// registration from ClientID, scoped only to SCIM2 read access; its
	// secret is resolved the same way ClientID's is (see BuildTable).
	// SCIMBaseURL/SCIMTokenURL default to Issuer and
	// Issuer+"/oauth2/token" when blank.
	SCIMBaseURL  string
	SCIMTokenURL string
	SCIMClientID string
	SCIMScopes   []string
}

// Tenant pairs a Config with the TokenValidator built from it, constructed
// once at startup (BuildTable) rather than per-request.
type Tenant struct {
	Config
	Validator tokenvalidator.TokenValidator
}

// Table looks tenants up by Slug — the resolved value of a /v1/{tenant}/...
// path segment.
type Table map[string]*Tenant

// Lookup returns slug's Tenant, or ok=false if no such tenant is
// configured.
func (t Table) Lookup(slug string) (*Tenant, bool) {
	tt, ok := t[slug]
	return tt, ok
}

// SecretLookup resolves a tenant's IdP client secret, keyed by Slug. The
// registry itself never carries a secret — see BuildTable. An interface
// rather than a hardcoded env lookup so tests can supply a fake.
type SecretLookup func(slug string) string

// registryFieldCountBase/Userinfo/SCIM are the valid field counts for one
// TENANT_REGISTRY row (see BuildTable). SCIM's four fields are all-or-
// nothing, so a row is never 14-16 fields.
const (
	registryFieldCountBase     = 12
	registryFieldCountUserinfo = 13
	registryFieldCountSCIM     = 17
)

// LegacyConfig carries /support/chats' pre-multi-tenant env vars, reused to
// synthesize the "identity-console" fallback tenant when TENANT_REGISTRY is
// unset (see BuildTable). ClientSecret is passed directly rather than via
// SecretLookup, since this fallback path has no TENANT_<SLUG>_CLIENT_SECRET
// to look up.
type LegacyConfig struct {
	IssuerBaseURL      string
	ClientID           string
	ClientSecret       string
	InsecureSkipVerify bool
	AllowedOrigins     []string
	// SCIM* configure the SCIM-based canonical-Subject resolver (see
	// internal/scim), used instead of the UserInfo fallback whenever
	// introspection alone doesn't carry a Subject. SCIMClientID blank
	// means SCIM is not configured. Must be a separate, least-privilege
	// client-credentials registration from ClientID/ClientSecret.
	// SCIMBaseURL/SCIMTokenURL default to IssuerBaseURL and
	// IssuerBaseURL+"/oauth2/token" when blank.
	SCIMBaseURL      string
	SCIMTokenURL     string
	SCIMClientID     string
	SCIMClientSecret string
	SCIMScopes       []string
}

// legacySlug is the fallback tenant's Slug — reachable at
// /v1/identity-console/... once BuildTable synthesizes it.
const legacySlug = "identity-console"

// legacyRoutingSource/legacyChannel/legacyProjectID match the legacy
// /support/chats constants exactly (see internal/handler/chats.go), so a
// case raised through /v1/identity-console/... routes identically.
const (
	legacyRoutingSource = "asgardeo"
	legacyChannel       = "ask-ai"
	legacyProjectID     = "console-ask-ai"
)

// BuildTable parses TENANT_REGISTRY into a Table, one TokenValidator per
// row. If raw is blank, it synthesizes a single "identity-console" row from
// the legacy pre-multi-tenant env vars.
//
// Each row is "|"-separated fields, in order:
//
//	slug|validationType|issuer|introspectionURL|jwksURI|audience|clientID|insecureSkipVerify|allowedOrigins|routingSource|channel|projectID|userinfoURL|scimClientID|scimBaseURL|scimTokenURL|scimScopes
//
// A row has 12, 13, or 17 fields: userinfoURL is optional (12 vs 13), and
// the four SCIM fields are optional as one atomic block (13 vs 17).
// allowedOrigins and scimScopes are themselves ","-separated.
// validationType defaults to "introspection" ("jwks" is the alternative,
// case-insensitive). insecureSkipVerify is true only for the literal
// string "true". Blank routingSource inherits defaultRoutingSource.
//
// Client secrets are never stored in the registry: each row's secrets are
// resolved via secretLookup(slug) and scimSecretLookup(slug), which default
// to TENANT_<SLUG>_CLIENT_SECRET and TENANT_<SLUG>_SCIM_CLIENT_SECRET.
func BuildTable(raw, defaultRoutingSource string, secretLookup, scimSecretLookup SecretLookup, legacy LegacyConfig) (Table, error) {
	if secretLookup == nil {
		secretLookup = EnvSecretLookup
	}
	if scimSecretLookup == nil {
		scimSecretLookup = EnvSCIMSecretLookup
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
		n := len(fields)
		if n != registryFieldCountBase && n != registryFieldCountUserinfo && n != registryFieldCountSCIM {
			return nil, fmt.Errorf("tenant: TENANT_REGISTRY row %d: expected %d, %d, or %d fields, got %d", i+1, registryFieldCountBase, registryFieldCountUserinfo, registryFieldCountSCIM, n)
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

		allowedOrigins := splitCommaList(fields[8])
		routingSource := fields[9]
		if routingSource == "" {
			routingSource = defaultRoutingSource
		}
		var userinfoURL string
		if n >= registryFieldCountUserinfo {
			userinfoURL = fields[12]
		}
		var scimClientID, scimBaseURL, scimTokenURL string
		var scimScopes []string
		if n == registryFieldCountSCIM {
			scimClientID = fields[13]
			scimBaseURL = fields[14]
			scimTokenURL = fields[15]
			scimScopes = splitCommaList(fields[16])
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
			SCIMBaseURL:        scimBaseURL,
			SCIMTokenURL:       scimTokenURL,
			SCIMClientID:       scimClientID,
			SCIMScopes:         scimScopes,
		}

		validator, err := buildValidator(cfg, secretLookup(slug), scimSecretLookup(slug))
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

// splitCommaList splits a "," separated field into its trimmed, non-empty
// parts (nil if s has none) — shared by allowedOrigins and scimScopes,
// the registry's two nested comma-delimited fields.
func splitCommaList(s string) []string {
	var out []string
	if s == "" {
		return out
	}
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// EnvSecretLookup is BuildTable's default SecretLookup: reads
// TENANT_<SLUG_UPPER_SNAKE>_CLIENT_SECRET from the process environment,
// upper-casing slug and replacing every character outside [A-Z0-9] with
// "_" to form a valid env var name (e.g. slug "acme-corp" ->
// TENANT_ACME_CORP_CLIENT_SECRET).
func EnvSecretLookup(slug string) string {
	return os.Getenv("TENANT_" + envSlug(slug) + "_CLIENT_SECRET")
}

// EnvSCIMSecretLookup is BuildTable's default scimSecretLookup: reads
// TENANT_<SLUG_UPPER_SNAKE>_SCIM_CLIENT_SECRET from the process
// environment — the SCIM-client counterpart to EnvSecretLookup, kept as a
// separate env var since it's a separate, least-privilege credential.
func EnvSCIMSecretLookup(slug string) string {
	return os.Getenv("TENANT_" + envSlug(slug) + "_SCIM_CLIENT_SECRET")
}

// EnvSlug is slug in the upper snake case used in per-tenant environment
// variable names (acme-corp becomes ACME_CORP).
func EnvSlug(slug string) string {
	return envSlug(slug)
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

// buildValidator constructs cfg's TokenValidator. secret authenticates
// introspection; scimSecret authenticates the SCIM resolver's
// client-credentials grant when cfg.SCIMClientID configures one. Both are
// passed in rather than read from cfg, since Config never stores secrets.
func buildValidator(cfg Config, secret, scimSecret string) (tokenvalidator.TokenValidator, error) {
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
		iv := tokenvalidator.NewIntrospectionValidator(v)
		if cfg.SCIMClientID != "" {
			scimBaseURL := cfg.SCIMBaseURL
			if scimBaseURL == "" {
				scimBaseURL = cfg.Issuer
			}
			scimTokenURL := cfg.SCIMTokenURL
			if scimTokenURL == "" {
				scimTokenURL = strings.TrimRight(cfg.Issuer, "/") + "/oauth2/token"
			}
			iv = iv.WithSCIMResolver(scim.NewClient(scim.Config{
				BaseURL:            scimBaseURL,
				TokenURL:           scimTokenURL,
				ClientID:           cfg.SCIMClientID,
				ClientSecret:       scimSecret,
				Scopes:             cfg.SCIMScopes,
				InsecureSkipVerify: cfg.InsecureSkipVerify,
			}))
		}
		return iv, nil
	case "jwks":
		return tokenvalidator.NewJWKSValidator(context.Background(), tokenvalidator.JWKSValidatorConfig{
			JWKSURI:            cfg.JWKSURI,
			Issuer:             cfg.Issuer,
			Audience:           cfg.Audience,
			InsecureSkipVerify: cfg.InsecureSkipVerify,
		})
	default:
		return nil, fmt.Errorf("unknown validationType %q (want \"introspection\" or \"jwks\")", cfg.ValidationType)
	}
}

// buildLegacyTenant synthesizes the identity-console fallback tenant from
// legacy, building its validator through the same buildValidator every
// TENANT_REGISTRY row uses.
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
		SCIMBaseURL:        legacy.SCIMBaseURL,
		SCIMTokenURL:       legacy.SCIMTokenURL,
		SCIMClientID:       legacy.SCIMClientID,
		SCIMScopes:         legacy.SCIMScopes,
	}
	validator, err := buildValidator(cfg, legacy.ClientSecret, legacy.SCIMClientSecret)
	if err != nil {
		return nil, err
	}
	return &Tenant{Config: cfg, Validator: validator}, nil
}

type contextKey string

const tenantContextKey contextKey = "tenant"

// WithTenant returns a context carrying t — set by internal/middleware's
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
