package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/moduleforge/users-module/api/internal/config"
)

func TestValidateReturnPath(t *testing.T) {
	okCases := map[string]string{
		"empty defaults to root": "",
		"simple path":            "/profile",
		"path with query":        "/profile?tab=security",
		"nested":                 "/orgs/foo/users",
	}

	for name, input := range okCases {
		t.Run("accept/"+name, func(t *testing.T) {
			got, err := validateReturnPath(input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			// Empty normalizes to "/".
			if input == "" && got != "/" {
				t.Errorf("empty input should normalize to %q, got %q", "/", got)
			}
			if input != "" && got != input {
				t.Errorf("got %q, want %q", got, input)
			}
		})
	}

	rejectCases := map[string]string{
		"absolute http":     "http://evil.com/",
		"absolute https":    "https://evil.com/path",
		"protocol-relative": "//evil.com/path",
		"javascript scheme": "javascript:alert(1)",
		"no leading slash":  "profile",
		"scheme no slashes": "data:text/plain,hi",
	}

	for name, input := range rejectCases {
		t.Run("reject/"+name, func(t *testing.T) {
			if _, err := validateReturnPath(input); err == nil {
				t.Errorf("expected error for %q, got nil", input)
			}
		})
	}
}

// TestOAuth_EndToEnd walks through AuthorizeURL → token exchange → ID-token
// verification → Principal mapping, using a fully-local fake OIDC provider.
// A correctly-signed id_token with the expected issuer/audience/nonce passes
// all checks; this pins down that our wiring matches the protocol.
func TestOAuth_EndToEnd(t *testing.T) {
	// 1. Generate a throwaway RSA key used to sign id_tokens.
	signingKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	const keyID = "test-key-1"

	// 2. Stand up a fake OIDC provider. Shared state between discovery and
	// token handlers is captured in closures; the issuer URL needed for the
	// discovery document has to match the server's final URL, so we wire it
	// up after the test server is started.
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	clientID := "test-client-id"
	clientSecret := "test-client-secret"
	expectedCode := "test-auth-code"
	expectedSubject := "google-sub-123"
	expectedEmail := "user@example.com"

	// Shared across handlers.
	var lastNonce string

	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		cfg := map[string]any{
			"issuer":                                srv.URL,
			"authorization_endpoint":                srv.URL + "/authorize",
			"token_endpoint":                        srv.URL + "/token",
			"jwks_uri":                              srv.URL + "/jwks",
			"response_types_supported":              []string{"code"},
			"subject_types_supported":               []string{"public"},
			"id_token_signing_alg_values_supported": []string{"RS256"},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(cfg)
	})

	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		n := base64.RawURLEncoding.EncodeToString(signingKey.N.Bytes())
		e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(signingKey.E)).Bytes())
		jwks := map[string]any{
			"keys": []map[string]any{
				{
					"kty": "RSA",
					"alg": "RS256",
					"use": "sig",
					"kid": keyID,
					"n":   n,
					"e":   e,
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(jwks)
	})

	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if r.Form.Get("code") != expectedCode {
			http.Error(w, "bad code", 400)
			return
		}
		if r.Form.Get("client_id") != clientID {
			http.Error(w, "bad client", 400)
			return
		}

		idToken, err := signIDToken(signingKey, keyID, jwt.MapClaims{
			"iss":   srv.URL,
			"aud":   clientID,
			"sub":   expectedSubject,
			"email": expectedEmail,
			"nonce": lastNonce,
			"iat":   time.Now().Unix(),
			"exp":   time.Now().Add(time.Hour).Unix(),
		})
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		resp := map[string]any{
			"access_token": "test-access-token",
			"id_token":     idToken,
			"token_type":   "Bearer",
			"expires_in":   3600,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	// 3. Build a Config pointing at the fake provider.
	cfg := &config.Config{
		Auth: config.AuthConfig{
			AdminRole:            "admin",
			FrontendReturnURL:    "http://gui.test/auth/oidc/return",
			OAuthRedirectBaseURL: "http://api.test",
		},
		LocalAuth: config.LocalAuthConfig{
			JWTSecret: "test-jwt-secret-for-state-signer",
		},
		Providers: config.ProviderRegistry{
			"google": config.Provider{
				ID:           "google",
				DisplayName:  "Google",
				IssuerURL:    srv.URL,
				ClientID:     clientID,
				ClientSecret: clientSecret,
				ClaimStyle:   "google",
				Scopes:       []string{"openid", "email", "profile"},
			},
		},
	}

	oauth, err := NewOAuth(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewOAuth: %v", err)
	}

	// 4. Drive AuthorizeURL → capture the nonce and state for the mock token
	//    endpoint to echo back.
	authURL, stateToken, err := oauth.AuthorizeURL("google", "/profile")
	if err != nil {
		t.Fatalf("AuthorizeURL: %v", err)
	}
	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("parse authURL: %v", err)
	}
	if parsed.Query().Get("state") != stateToken {
		t.Errorf("authURL state = %q, want %q", parsed.Query().Get("state"), stateToken)
	}
	lastNonce = parsed.Query().Get("nonce")
	if lastNonce == "" {
		t.Fatal("authURL did not include a nonce")
	}

	// 5. Exchange with a matching state cookie.
	principal, payload, err := oauth.Exchange(context.Background(), "google", expectedCode, stateToken, stateToken)
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}

	if principal.Subject != expectedSubject {
		t.Errorf("Subject = %q, want %q", principal.Subject, expectedSubject)
	}
	if principal.Issuer != srv.URL {
		t.Errorf("Issuer = %q, want %q", principal.Issuer, srv.URL)
	}
	if principal.Email != expectedEmail {
		t.Errorf("Email = %q, want %q", principal.Email, expectedEmail)
	}
	if payload.ReturnPath != "/profile" {
		t.Errorf("payload.ReturnPath = %q, want /profile", payload.ReturnPath)
	}
}

func TestOAuth_Exchange_StateCookieMismatch(t *testing.T) {
	oauth := newOAuthForStateOnlyTest(t)

	// Generate a valid state token for one return path.
	authURL, state, err := oauth.AuthorizeURL("google", "/profile")
	if err != nil {
		t.Fatalf("AuthorizeURL: %v", err)
	}
	_ = authURL

	_, _, err = oauth.Exchange(context.Background(), "google", "code", state, "different-cookie")
	if err == nil {
		t.Fatal("expected state mismatch error, got nil")
	}
	if !errors.Is(err, ErrStateValidation) {
		t.Errorf("expected ErrStateValidation, got %v", err)
	}
}

func TestOAuth_Exchange_MissingState(t *testing.T) {
	oauth := newOAuthForStateOnlyTest(t)

	_, _, err := oauth.Exchange(context.Background(), "google", "code", "", "")
	if err == nil {
		t.Fatal("expected error for missing state")
	}
	if !errors.Is(err, ErrStateValidation) {
		t.Errorf("expected ErrStateValidation, got %v", err)
	}
}

func TestOAuth_AuthorizeURL_UnknownProvider(t *testing.T) {
	oauth := newOAuthForStateOnlyTest(t)
	_, _, err := oauth.AuthorizeURL("unknown", "/")
	if err == nil {
		t.Fatal("expected ErrUnknownProvider")
	}
}

// TestOAuth_Exchange_IDTokenTampering covers the three ways the IdP (or an
// attacker in the middle) might return an id_token that violates the
// integrity contract. Each variant should cause Exchange to return an error
// and our code must never accept such a token.
func TestOAuth_Exchange_IDTokenTampering(t *testing.T) {
	type tampering struct {
		name       string
		mutator    func(claims jwt.MapClaims, serverURL, clientID string)
		wantSubstr string
	}
	cases := []tampering{
		{
			name: "nonce mismatch",
			mutator: func(claims jwt.MapClaims, _, _ string) {
				claims["nonce"] = "not-the-nonce-we-asked-for"
			},
			// The go-oidc library checks the nonce hook first if set; we
			// check it ourselves after Verify returns. Either way we expect
			// an error — the message isn't load-bearing.
			wantSubstr: "nonce",
		},
		{
			name: "aud mismatch",
			mutator: func(claims jwt.MapClaims, _, _ string) {
				claims["aud"] = "some-other-client-id"
			},
			wantSubstr: "aud",
		},
		{
			name: "iss mismatch",
			mutator: func(claims jwt.MapClaims, _, _ string) {
				claims["iss"] = "https://evil.example.com"
			},
			wantSubstr: "iss",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			h := newFakeOIDCHarness(t, tc.mutator)

			oauth, err := NewOAuth(context.Background(), h.cfg)
			if err != nil {
				t.Fatalf("NewOAuth: %v", err)
			}

			authURL, stateToken, err := oauth.AuthorizeURL("google", "/profile")
			if err != nil {
				t.Fatalf("AuthorizeURL: %v", err)
			}
			parsed, err := url.Parse(authURL)
			if err != nil {
				t.Fatalf("parse authURL: %v", err)
			}
			h.setNonce(parsed.Query().Get("nonce"))

			_, _, err = oauth.Exchange(context.Background(), "google", h.expectedCode, stateToken, stateToken)
			if err == nil {
				t.Fatalf("expected error for %s, got nil", tc.name)
			}
			// Sanity: the error should plausibly mention the offending claim.
			// Don't overfit to a specific message — just confirm rejection.
			t.Logf("%s: %v", tc.name, err)
		})
	}
}

// fakeOIDCHarness encapsulates the fake-provider scaffolding used by the
// end-to-end and tampering tests. Callers pass a mutator that can rewrite the
// id_token claims right before they are signed; the happy-path test uses a
// no-op mutator.
type fakeOIDCHarness struct {
	cfg          *config.Config
	expectedCode string
	setNonce     func(string)
}

func newFakeOIDCHarness(t *testing.T, mutate func(jwt.MapClaims, string, string)) *fakeOIDCHarness {
	t.Helper()

	signingKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	const keyID = "test-key-1"

	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	clientID := "test-client-id"
	clientSecret := "test-client-secret"
	expectedCode := "test-auth-code"
	expectedSubject := "google-sub-123"
	expectedEmail := "user@example.com"

	var nonce string
	setNonce := func(n string) { nonce = n }

	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		cfg := map[string]any{
			"issuer":                                srv.URL,
			"authorization_endpoint":                srv.URL + "/authorize",
			"token_endpoint":                        srv.URL + "/token",
			"jwks_uri":                              srv.URL + "/jwks",
			"response_types_supported":              []string{"code"},
			"subject_types_supported":               []string{"public"},
			"id_token_signing_alg_values_supported": []string{"RS256"},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(cfg)
	})

	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		n := base64.RawURLEncoding.EncodeToString(signingKey.N.Bytes())
		e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(signingKey.E)).Bytes())
		jwks := map[string]any{
			"keys": []map[string]any{
				{"kty": "RSA", "alg": "RS256", "use": "sig", "kid": keyID, "n": n, "e": e},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(jwks)
	})

	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if r.Form.Get("code") != expectedCode {
			http.Error(w, "bad code", 400)
			return
		}
		claims := jwt.MapClaims{
			"iss":   srv.URL,
			"aud":   clientID,
			"sub":   expectedSubject,
			"email": expectedEmail,
			"nonce": nonce,
			"iat":   time.Now().Unix(),
			"exp":   time.Now().Add(time.Hour).Unix(),
		}
		if mutate != nil {
			mutate(claims, srv.URL, clientID)
		}
		idToken, err := signIDToken(signingKey, keyID, claims)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		resp := map[string]any{
			"access_token": "test-access-token",
			"id_token":     idToken,
			"token_type":   "Bearer",
			"expires_in":   3600,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	cfg := &config.Config{
		Auth: config.AuthConfig{
			AdminRole:            "admin",
			FrontendReturnURL:    "http://gui.test/auth/oidc/return",
			OAuthRedirectBaseURL: "http://api.test",
		},
		LocalAuth: config.LocalAuthConfig{
			JWTSecret: "test-jwt-secret-for-state-signer",
		},
		Providers: config.ProviderRegistry{
			"google": config.Provider{
				ID:           "google",
				DisplayName:  "Google",
				IssuerURL:    srv.URL,
				ClientID:     clientID,
				ClientSecret: clientSecret,
				ClaimStyle:   "google",
				Scopes:       []string{"openid", "email", "profile"},
			},
		},
	}

	return &fakeOIDCHarness{
		cfg:          cfg,
		expectedCode: expectedCode,
		setNonce:     setNonce,
	}
}

// newOAuthForStateOnlyTest builds an OAuth with a single bogus provider whose
// issuer points nowhere — enough for state/cookie validation to run but not
// enough to execute a real token exchange. Tests that need a working exchange
// use the fake server setup in TestOAuth_EndToEnd instead.
func newOAuthForStateOnlyTest(t *testing.T) *OAuth {
	t.Helper()

	// Stand up a stub discovery endpoint so NewOAuth doesn't fail.
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w,
			`{"issuer":%q,"authorization_endpoint":%q,"token_endpoint":%q,"jwks_uri":%q,"response_types_supported":["code"],"subject_types_supported":["public"],"id_token_signing_alg_values_supported":["RS256"]}`,
			srv.URL, srv.URL+"/authorize", srv.URL+"/token", srv.URL+"/jwks")
	})

	cfg := &config.Config{
		Auth: config.AuthConfig{
			AdminRole:            "admin",
			FrontendReturnURL:    "http://gui.test/return",
			OAuthRedirectBaseURL: "http://api.test",
		},
		LocalAuth: config.LocalAuthConfig{JWTSecret: "test-secret"},
		Providers: config.ProviderRegistry{
			"google": config.Provider{
				ID:           "google",
				DisplayName:  "Google",
				IssuerURL:    srv.URL,
				ClientID:     "test-client",
				ClientSecret: "test-secret",
				ClaimStyle:   "google",
				Scopes:       []string{"openid", "email", "profile"},
			},
		},
	}

	oauth, err := NewOAuth(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewOAuth: %v", err)
	}
	return oauth
}

// signIDToken produces an RS256-signed JWT with the given claims. The kid
// header matches what the /jwks endpoint publishes so the verifier accepts it.
func signIDToken(key *rsa.PrivateKey, kid string, claims jwt.MapClaims) (string, error) {
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = kid
	return tok.SignedString(key)
}

// TestNewOAuth_PartialInitFailure pins down the phase-9.8 contract: one bad
// provider must not take down the whole registry. NewOAuth returns nil error,
// the good provider lands in EnabledProviders(), the bad one is listed only
// by AllProviders() with InitOK=false, and Exchange on the bad one returns
// ErrProviderNotAvailable so the handler can 404 it.
func TestNewOAuth_PartialInitFailure(t *testing.T) {
	// Stand up a real discovery endpoint for the "good" provider. The "bad"
	// provider points at a URL that will fail discovery (the path exists but
	// returns 404), so oidc.NewProvider errors during init.
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w,
			`{"issuer":%q,"authorization_endpoint":%q,"token_endpoint":%q,"jwks_uri":%q,"response_types_supported":["code"],"subject_types_supported":["public"],"id_token_signing_alg_values_supported":["RS256"]}`,
			srv.URL, srv.URL+"/authorize", srv.URL+"/token", srv.URL+"/jwks")
	})

	cfg := &config.Config{
		Auth: config.AuthConfig{
			AdminRole:            "admin",
			FrontendReturnURL:    "http://gui.test/return",
			OAuthRedirectBaseURL: "http://api.test",
		},
		LocalAuth: config.LocalAuthConfig{JWTSecret: "test-secret"},
		Providers: config.ProviderRegistry{
			"google": config.Provider{
				ID:           "google",
				DisplayName:  "Google",
				IssuerURL:    srv.URL,
				ClientID:     "good-client",
				ClientSecret: "good-secret",
				ClaimStyle:   "google",
				Scopes:       []string{"openid", "email", "profile"},
			},
			"microsoft": config.Provider{
				ID:           "microsoft",
				DisplayName:  "Microsoft",
				IssuerURL:    srv.URL + "/nonexistent-issuer",
				ClientID:     "bad-client",
				ClientSecret: "bad-secret",
				ClaimStyle:   "microsoft",
				Scopes:       []string{"openid", "email", "profile"},
			},
		},
	}

	oauth, err := NewOAuth(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewOAuth should not fail on per-provider init: %v", err)
	}
	if oauth == nil {
		t.Fatal("NewOAuth returned nil OAuth")
	}

	enabled := oauth.EnabledProviders()
	if len(enabled) != 1 {
		t.Fatalf("EnabledProviders() len = %d, want 1; got %v", len(enabled), enabled)
	}
	if enabled[0].ID != "google" {
		t.Errorf("EnabledProviders()[0].ID = %q, want google", enabled[0].ID)
	}

	all := oauth.AllProviders()
	if len(all) != 2 {
		t.Fatalf("AllProviders() len = %d, want 2", len(all))
	}
	// Sorted by ID → google, microsoft.
	if all[0].ID != "google" || !all[0].InitOK {
		t.Errorf("AllProviders()[0] = %+v, want google InitOK=true", all[0])
	}
	if all[1].ID != "microsoft" || all[1].InitOK {
		t.Errorf("AllProviders()[1] = %+v, want microsoft InitOK=false", all[1])
	}
	if all[1].Err == nil {
		t.Error("bad provider should carry a non-nil Err")
	}

	if got, want := oauth.Status(), StatusOK; got != want {
		t.Errorf("Status() = %q, want %q", got, want)
	}

	// Exchange on the bad provider must surface ErrProviderNotAvailable so the
	// handler can respond 404 — never a confusing "cookie mismatch" 400 or a
	// real token-endpoint call.
	_, _, err = oauth.Exchange(context.Background(), "microsoft", "code", "state", "state")
	if err == nil {
		t.Fatal("Exchange on bad provider: expected error, got nil")
	}
	if !errors.Is(err, ErrProviderNotAvailable) {
		t.Errorf("Exchange error = %v, want errors.Is ErrProviderNotAvailable", err)
	}

	// AuthorizeURL likewise should refuse to build an auth URL for the bad
	// provider — otherwise we'd redirect the browser to a broken flow.
	_, _, err = oauth.AuthorizeURL("microsoft", "/")
	if err == nil {
		t.Fatal("AuthorizeURL on bad provider: expected error, got nil")
	}
	if !errors.Is(err, ErrProviderNotAvailable) {
		t.Errorf("AuthorizeURL error = %v, want errors.Is ErrProviderNotAvailable", err)
	}
}

// TestOAuth_Status covers the three oauth-only states NewOAuth can put the
// registry in. The fourth (empty_no_consent) requires NO_OIDC_ACCOUNTS=1 in
// env; we exercise it with t.Setenv.
func TestOAuth_Status(t *testing.T) {
	baseCfg := func() *config.Config {
		return &config.Config{
			Auth: config.AuthConfig{
				AdminRole:            "admin",
				FrontendReturnURL:    "http://gui.test/return",
				OAuthRedirectBaseURL: "http://api.test",
			},
			LocalAuth: config.LocalAuthConfig{JWTSecret: "test-secret"},
			Providers: config.ProviderRegistry{},
		}
	}

	t.Run("no_env_no_flag when empty and flag unset", func(t *testing.T) {
		t.Setenv("NO_OIDC_ACCOUNTS", "")
		oauth, err := NewOAuth(context.Background(), baseCfg())
		if err != nil {
			t.Fatalf("NewOAuth: %v", err)
		}
		if got, want := oauth.Status(), StatusNoEnvNoFlag; got != want {
			t.Errorf("Status() = %q, want %q", got, want)
		}
	})

	t.Run("empty_no_consent when empty and flag set", func(t *testing.T) {
		t.Setenv("NO_OIDC_ACCOUNTS", "1")
		oauth, err := NewOAuth(context.Background(), baseCfg())
		if err != nil {
			t.Fatalf("NewOAuth: %v", err)
		}
		if got, want := oauth.Status(), StatusEmptyNoConsent; got != want {
			t.Errorf("Status() = %q, want %q", got, want)
		}
	})

	t.Run("init_failed when every provider fails", func(t *testing.T) {
		t.Setenv("NO_OIDC_ACCOUNTS", "")
		cfg := baseCfg()
		cfg.Providers = config.ProviderRegistry{
			"bogus": config.Provider{
				ID:           "bogus",
				DisplayName:  "Bogus",
				IssuerURL:    "http://127.0.0.1:1/definitely-not-listening",
				ClientID:     "c",
				ClientSecret: "s",
				ClaimStyle:   "generic",
				Scopes:       []string{"openid"},
			},
		}
		oauth, err := NewOAuth(context.Background(), cfg)
		if err != nil {
			t.Fatalf("NewOAuth: %v", err)
		}
		if got, want := oauth.Status(), StatusInitFailed; got != want {
			t.Errorf("Status() = %q, want %q", got, want)
		}
	})
}

// TestIsValidMicrosoftIssuer pins down the tenant-issuer regex used to
// replace go-oidc's strict issuer check for multi-tenant Microsoft providers.
// The regex is load-bearing security: it's the only thing standing between a
// spoofed id_token's `iss` claim and a successful login once SkipIssuerCheck
// is enabled on the verifier. Exercise the shape carefully.
func TestIsValidMicrosoftIssuer(t *testing.T) {
	accept := []string{
		// Canonical Azure tenant UUID.
		"https://login.microsoftonline.com/11111111-2222-3333-4444-555555555555/v2.0",
		// Personal-MSA tenant — matches the same UUID shape, no special case.
		"https://login.microsoftonline.com/9188040d-6c67-4c5b-b112-36a304b66dad/v2.0",
	}
	for _, iss := range accept {
		iss := iss
		t.Run("accept/"+iss, func(t *testing.T) {
			if !isValidMicrosoftIssuer(iss) {
				t.Errorf("expected %q to be accepted", iss)
			}
		})
	}

	reject := map[string]string{
		"empty":                   "",
		"http (not https)":        "http://login.microsoftonline.com/11111111-2222-3333-4444-555555555555/v2.0",
		"uppercase hex in tenant": "https://login.microsoftonline.com/11111111-2222-3333-4444-55555555555F/v2.0",
		"trailing slash":          "https://login.microsoftonline.com/11111111-2222-3333-4444-555555555555/v2.0/",
		"missing /v2.0":           "https://login.microsoftonline.com/11111111-2222-3333-4444-555555555555",
		"v1.0 endpoint":           "https://login.microsoftonline.com/11111111-2222-3333-4444-555555555555/v1.0",
		"literal placeholder":     "https://login.microsoftonline.com/{tenantid}/v2.0",
		"common alias":            "https://login.microsoftonline.com/common/v2.0",
		"non-uuid tenant":         "https://login.microsoftonline.com/notauuid/v2.0",
		"wrong host":              "https://evil.example.com/11111111-2222-3333-4444-555555555555/v2.0",
		"extra path":              "https://login.microsoftonline.com/11111111-2222-3333-4444-555555555555/v2.0/extra",
		"too-short tenant":        "https://login.microsoftonline.com/11111111-2222-3333-4444-5555555555/v2.0",
		"too-long tenant":         "https://login.microsoftonline.com/11111111-2222-3333-4444-5555555555555/v2.0",
	}
	for name, iss := range reject {
		name, iss := name, iss
		t.Run("reject/"+name, func(t *testing.T) {
			if isValidMicrosoftIssuer(iss) {
				t.Errorf("expected %q to be rejected", iss)
			}
		})
	}
}

// newFakeMultiTenantHarness stands up a fake OIDC provider whose discovery
// document advertises Microsoft's literal "{tenantid}" placeholder as its
// `issuer` and whose id_token is signed with a caller-supplied `iss` claim.
// This mirrors real Azure behavior: discovery says one thing, the token says
// another, and our job is to accept the combination only when the token's
// issuer matches the tenant-UUID pattern. Kept separate from
// newFakeOIDCHarness so the existing 9.8 tests aren't perturbed.
type fakeMultiTenantHarness struct {
	cfg          *config.Config
	expectedCode string
	setNonce     func(string)
}

func newFakeMultiTenantHarness(t *testing.T, tokenIssuer string) *fakeMultiTenantHarness {
	t.Helper()

	signingKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	const keyID = "test-key-1"

	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	clientID := "test-client-id"
	clientSecret := "test-client-secret"
	expectedCode := "test-auth-code"
	expectedSubject := "ms-sub-123"
	expectedEmail := "user@example.com"

	var nonce string
	setNonce := func(n string) { nonce = n }

	// Discovery: all endpoints point at srv, but `issuer` is the literal
	// Microsoft placeholder string. Everything else is standard.
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		cfg := map[string]any{
			"issuer":                                "https://login.microsoftonline.com/{tenantid}/v2.0",
			"authorization_endpoint":                srv.URL + "/authorize",
			"token_endpoint":                        srv.URL + "/token",
			"jwks_uri":                              srv.URL + "/jwks",
			"response_types_supported":              []string{"code"},
			"subject_types_supported":               []string{"public"},
			"id_token_signing_alg_values_supported": []string{"RS256"},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(cfg)
	})

	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		n := base64.RawURLEncoding.EncodeToString(signingKey.N.Bytes())
		e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(signingKey.E)).Bytes())
		jwks := map[string]any{
			"keys": []map[string]any{
				{"kty": "RSA", "alg": "RS256", "use": "sig", "kid": keyID, "n": n, "e": e},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(jwks)
	})

	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if r.Form.Get("code") != expectedCode {
			http.Error(w, "bad code", 400)
			return
		}
		claims := jwt.MapClaims{
			"iss":   tokenIssuer,
			"aud":   clientID,
			"sub":   expectedSubject,
			"email": expectedEmail,
			"nonce": nonce,
			"iat":   time.Now().Unix(),
			"exp":   time.Now().Add(time.Hour).Unix(),
		}
		idToken, err := signIDToken(signingKey, keyID, claims)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		resp := map[string]any{
			"access_token": "test-access-token",
			"id_token":     idToken,
			"token_type":   "Bearer",
			"expires_in":   3600,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	// IssuerURL points at the test server so discovery actually works; the
	// MultiTenantIssuer flag is set directly (LoadProviders would set it from
	// the real Microsoft URL, but we can't dial Microsoft from a unit test).
	// ClaimStyle "microsoft" exercises the mapper path we actually care about.
	cfg := &config.Config{
		Auth: config.AuthConfig{
			AdminRole:            "admin",
			FrontendReturnURL:    "http://gui.test/auth/oidc/return",
			OAuthRedirectBaseURL: "http://api.test",
		},
		LocalAuth: config.LocalAuthConfig{
			JWTSecret: "test-jwt-secret-for-state-signer",
		},
		Providers: config.ProviderRegistry{
			"microsoft": config.Provider{
				ID:                "microsoft",
				DisplayName:       "Microsoft",
				IssuerURL:         srv.URL,
				ClientID:          clientID,
				ClientSecret:      clientSecret,
				ClaimStyle:        "microsoft",
				Scopes:            []string{"openid", "email", "profile"},
				MultiTenantIssuer: true,
			},
		},
	}

	return &fakeMultiTenantHarness{
		cfg:          cfg,
		expectedCode: expectedCode,
		setNonce:     setNonce,
	}
}

// runMultiTenantExchange is shared scaffolding for the three multi-tenant
// exchange tests: it drives AuthorizeURL → Exchange and returns whatever
// Exchange produced, so each test can assert success or a specific failure
// without duplicating setup.
func runMultiTenantExchange(t *testing.T, h *fakeMultiTenantHarness) (Principal, StatePayload, error) {
	t.Helper()

	oauth, err := NewOAuth(context.Background(), h.cfg)
	if err != nil {
		t.Fatalf("NewOAuth: %v", err)
	}

	authURL, stateToken, err := oauth.AuthorizeURL("microsoft", "/profile")
	if err != nil {
		t.Fatalf("AuthorizeURL: %v", err)
	}
	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("parse authURL: %v", err)
	}
	h.setNonce(parsed.Query().Get("nonce"))

	return oauth.Exchange(context.Background(), "microsoft", h.expectedCode, stateToken, stateToken)
}

// TestOAuth_MultiTenant_HappyPath verifies that a multi-tenant Microsoft
// provider (discovery returns the "{tenantid}" placeholder, id_token carries
// a tenant-UUID issuer) completes the full Exchange flow and yields a
// Principal. This pins down the intended happy path end-to-end.
func TestOAuth_MultiTenant_HappyPath(t *testing.T) {
	tokenIssuer := "https://login.microsoftonline.com/9188040d-6c67-4c5b-b112-36a304b66dad/v2.0"
	h := newFakeMultiTenantHarness(t, tokenIssuer)

	principal, payload, err := runMultiTenantExchange(t, h)
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if principal.Issuer != tokenIssuer {
		t.Errorf("Principal.Issuer = %q, want %q", principal.Issuer, tokenIssuer)
	}
	if principal.Subject != "ms-sub-123" {
		t.Errorf("Principal.Subject = %q, want %q", principal.Subject, "ms-sub-123")
	}
	if principal.Email != "user@example.com" {
		t.Errorf("Principal.Email = %q, want %q", principal.Email, "user@example.com")
	}
	if payload.ReturnPath != "/profile" {
		t.Errorf("payload.ReturnPath = %q, want /profile", payload.ReturnPath)
	}
}

// TestOAuth_MultiTenant_EvilHostRejected ensures an id_token whose issuer is
// a completely different host (classic spoof vector once SkipIssuerCheck is
// enabled) is rejected by the post-verify tenant-issuer check.
func TestOAuth_MultiTenant_EvilHostRejected(t *testing.T) {
	h := newFakeMultiTenantHarness(t, "https://evil.example.com/aaa/v2.0")

	_, _, err := runMultiTenantExchange(t, h)
	if err == nil {
		t.Fatal("expected error for non-Microsoft issuer, got nil")
	}
	if !strings.Contains(err.Error(), "microsoft issuer not accepted") {
		t.Errorf("error should mention issuer rejection, got: %v", err)
	}
}

// TestOAuth_MultiTenant_NonUUIDTenantRejected ensures that even same-host
// issuers are rejected when the tenant segment isn't a UUID. This is the
// subtler spoof case: an attacker who controls any MS-hosted endpoint (or who
// can forge the literal-"common"-in-iss case) must not slip past.
func TestOAuth_MultiTenant_NonUUIDTenantRejected(t *testing.T) {
	h := newFakeMultiTenantHarness(t, "https://login.microsoftonline.com/notauuid/v2.0")

	_, _, err := runMultiTenantExchange(t, h)
	if err == nil {
		t.Fatal("expected error for non-UUID tenant issuer, got nil")
	}
	if !strings.Contains(err.Error(), "microsoft issuer not accepted") {
		t.Errorf("error should mention issuer rejection, got: %v", err)
	}
}
