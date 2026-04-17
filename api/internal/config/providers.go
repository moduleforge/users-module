// Package config: OIDC provider registry.
//
// Providers are declared in the environment using the
// `AUTH_PROVIDER_{ID}_{FIELD}` convention. A provider is considered enabled
// iff its CLIENT_ID is set. Well-known providers (google, microsoft) have
// built-in defaults for issuer, claim style, and display name so that a
// deployment only needs to set the client_id and client_secret.
package config

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// Provider describes an OIDC identity provider as configured via the env.
// Fields that are safe to expose to browsers (ID, DisplayName) are returned
// by the public /v1/auth/providers endpoint; everything else stays server-side.
type Provider struct {
	ID           string
	DisplayName  string
	IssuerURL    string
	ClientID     string
	ClientSecret string
	ClaimStyle   string
	Scopes       []string
	// MultiTenantIssuer marks providers whose discovery documents return a
	// literal "{tenantid}" placeholder in the `issuer` field (Microsoft's
	// /common, /organizations, /consumers endpoints). For these we skip
	// go-oidc's strict issuer check at verification time and validate the
	// real tenant-specific issuer ourselves after the id_token is decoded.
	MultiTenantIssuer bool
}

// ProviderRegistry maps provider IDs to fully-resolved Provider entries.
// Only enabled providers (those with a ClientID set) appear in the registry.
type ProviderRegistry map[string]Provider

// IDs returns the sorted list of provider IDs in the registry.
func (r ProviderRegistry) IDs() []string {
	ids := make([]string, 0, len(r))
	for id := range r {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// providerDefaults holds the built-in defaults applied when a deployment
// only supplies CLIENT_ID / CLIENT_SECRET for a well-known provider.
type providerDefaults struct {
	IssuerURL   string
	ClaimStyle  string
	DisplayName string
}

// wellKnownProviders is the registry of built-in defaults. Add entries here
// rather than scattering provider-specific knowledge through the codebase.
var wellKnownProviders = map[string]providerDefaults{
	"google": {
		IssuerURL:   "https://accounts.google.com",
		ClaimStyle:  "google",
		DisplayName: "Google",
	},
	"microsoft": {
		IssuerURL:   "https://login.microsoftonline.com/common/v2.0",
		ClaimStyle:  "microsoft",
		DisplayName: "Microsoft",
	},
}

// defaultScopes is the OIDC scope set we request when a provider does not
// override SCOPES in the environment. "openid" is required to get an id_token
// from a standards-compliant OP; "email" and "profile" are needed to populate
// Principal.Email and the user's display name.
var defaultScopes = []string{"openid", "email", "profile"}

// microsoftMultiTenantIssuers is the exact set of Microsoft v2.0 discovery
// URLs whose `issuer` field in the discovery document is a literal
// "{tenantid}" placeholder. Matches are exact (not substring) so that a
// deployment pinning a specific tenant URL (e.g.
// https://login.microsoftonline.com/<my-tenant-uuid>/v2.0) is treated as a
// normal single-tenant provider with strict issuer checking enabled.
var microsoftMultiTenantIssuers = map[string]struct{}{
	"https://login.microsoftonline.com/common/v2.0":        {},
	"https://login.microsoftonline.com/organizations/v2.0": {},
	"https://login.microsoftonline.com/consumers/v2.0":     {},
}

// isMultiTenantIssuer reports whether an issuer URL is one of the known
// Microsoft multi-tenant discovery endpoints.
func isMultiTenantIssuer(issuerURL string) bool {
	_, ok := microsoftMultiTenantIssuers[issuerURL]
	return ok
}

// LoadProviders builds a ProviderRegistry by scanning the process environment.
//
// Candidate IDs come from AUTH_PROVIDERS (comma list) if set; otherwise the
// registry auto-discovers IDs by scanning env keys matching
// AUTH_PROVIDER_{ID}_CLIENT_ID. Missing CLIENT_SECRET on an enabled provider
// is a startup error; unknown provider IDs without an explicit issuer or
// claim_style are also an error. Returns (nil, nil) when no providers are
// configured — that's the "intentionally disabled" state.
func LoadProviders() (ProviderRegistry, error) {
	candidates := candidateProviderIDs()
	if len(candidates) == 0 {
		return ProviderRegistry{}, nil
	}

	registry := make(ProviderRegistry, len(candidates))
	var problems []string

	for _, id := range candidates {
		provider, err := loadProvider(id)
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		if provider == nil {
			// Candidate ID was enumerated but has no CLIENT_ID — skip silently.
			// This lets AUTH_PROVIDERS list a provider that's toggled off by
			// not setting its client_id.
			continue
		}
		registry[id] = *provider
	}

	if len(problems) > 0 {
		return nil, fmt.Errorf("auth providers: %s", strings.Join(problems, "; "))
	}

	return registry, nil
}

// candidateProviderIDs returns the set of provider IDs to try to load.
// Honors AUTH_PROVIDERS if set; otherwise auto-discovers from env keys.
func candidateProviderIDs() []string {
	if raw := os.Getenv("AUTH_PROVIDERS"); raw != "" {
		seen := make(map[string]bool)
		var ids []string
		for _, tok := range strings.Split(raw, ",") {
			tok = strings.TrimSpace(strings.ToLower(tok))
			if tok == "" || seen[tok] {
				continue
			}
			seen[tok] = true
			ids = append(ids, tok)
		}
		return ids
	}

	// Auto-discover by scanning env for AUTH_PROVIDER_{ID}_CLIENT_ID.
	const prefix = "AUTH_PROVIDER_"
	const suffix = "_CLIENT_ID"
	seen := make(map[string]bool)
	var ids []string
	for _, kv := range os.Environ() {
		eq := strings.IndexByte(kv, '=')
		if eq <= 0 {
			continue
		}
		key := kv[:eq]
		if !strings.HasPrefix(key, prefix) || !strings.HasSuffix(key, suffix) {
			continue
		}
		inner := strings.TrimSuffix(strings.TrimPrefix(key, prefix), suffix)
		if inner == "" {
			continue
		}
		id := strings.ToLower(inner)
		if seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// loadProvider reads a single provider from the environment. Returns
// (nil, nil) if the provider is not enabled (no CLIENT_ID); (nil, err) if
// the provider is enabled but misconfigured.
func loadProvider(id string) (*Provider, error) {
	envKey := func(field string) string {
		return "AUTH_PROVIDER_" + strings.ToUpper(id) + "_" + field
	}

	clientID := os.Getenv(envKey("CLIENT_ID"))
	if clientID == "" {
		return nil, nil
	}

	clientSecret := os.Getenv(envKey("CLIENT_SECRET"))
	if clientSecret == "" {
		return nil, fmt.Errorf("%s is set but %s is missing", envKey("CLIENT_ID"), envKey("CLIENT_SECRET"))
	}

	defaults := wellKnownProviders[id]

	issuerURL := firstNonEmpty(os.Getenv(envKey("ISSUER_URL")), defaults.IssuerURL)
	claimStyle := firstNonEmpty(os.Getenv(envKey("CLAIM_STYLE")), defaults.ClaimStyle)
	displayName := firstNonEmpty(os.Getenv(envKey("DISPLAY_NAME")), defaults.DisplayName, titleCase(id))

	if issuerURL == "" {
		return nil, fmt.Errorf("provider %q: %s is required (no built-in default)", id, envKey("ISSUER_URL"))
	}
	if claimStyle == "" {
		return nil, fmt.Errorf("provider %q: %s is required (no built-in default)", id, envKey("CLAIM_STYLE"))
	}

	scopes := parseScopes(os.Getenv(envKey("SCOPES")))
	if len(scopes) == 0 {
		scopes = append(scopes, defaultScopes...)
	}

	return &Provider{
		ID:                id,
		DisplayName:       displayName,
		IssuerURL:         issuerURL,
		ClientID:          clientID,
		ClientSecret:      clientSecret,
		ClaimStyle:        claimStyle,
		Scopes:            scopes,
		MultiTenantIssuer: isMultiTenantIssuer(issuerURL),
	}, nil
}

// parseScopes splits a comma-separated scope string into a trimmed slice.
// Whitespace-only entries are dropped.
func parseScopes(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// firstNonEmpty returns the first argument that is not the empty string.
// Returns "" if all arguments are empty.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// titleCase uppercases the first byte of id and leaves the rest alone, so
// "authelia" becomes "Authelia" (not ALL-CAPS like strings.ToTitle would
// produce, and without pulling in golang.org/x/text for a one-liner).
// Provider IDs are ASCII-only by construction (AUTH_PROVIDER_{ID} is an env
// var name), so byte-level handling is safe here.
func titleCase(id string) string {
	if id == "" {
		return ""
	}
	return strings.ToUpper(id[:1]) + id[1:]
}
