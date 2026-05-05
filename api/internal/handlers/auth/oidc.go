package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"

	localauth "github.com/moduleforge/users-module/api/internal/auth"
	"github.com/moduleforge/users-module/api/internal/config"
	"github.com/moduleforge/users-module/api/internal/server"
	db "github.com/moduleforge/users-module/model/db"
)

// normalizeProviderID lowercases the provider URL param so the lookup matches
// the registry keys (which are lowercased at load time). Without this,
// /v1/auth/oidc/Google/start would 404.
func normalizeProviderID(r *http.Request) string {
	return strings.ToLower(chi.URLParam(r, "provider"))
}

// stateCookieName is the name of the cookie that carries the signed state
// token between /start and /callback.
const stateCookieName = "oidc_state"

// stateCookiePath scopes the cookie to the OIDC callback route tree so it
// isn't broadcast on unrelated requests.
const stateCookiePath = "/v1/auth/oidc/"

// stateCookieMaxAge mirrors the TTL baked into the state token itself.
const stateCookieMaxAge = 300

// userResolver is the interface the OIDC handler uses to look up or create a
// user from a verified OIDC principal. Defined here (at the point of use) so
// tests can inject a stub without importing the concrete resolver type.
type userResolver interface {
	Resolve(ctx context.Context, p localauth.Principal) (*localauth.UserContext, error)
}

// OIDCHandler serves the provider discovery endpoint and the authorization
// code start/callback round trip. It holds its own copies of the resolver
// and the OAuth orchestrator so the main router wiring stays simple.
type OIDCHandler struct {
	queries  *db.Queries
	oauth    *localauth.OAuth
	resolver userResolver
	cfg      *config.Config
}

// NewOIDCHandler wires up the handler with everything it needs. All fields
// must be non-nil (except oauth, which may be a shell with zero providers).
func NewOIDCHandler(queries *db.Queries, oauth *localauth.OAuth, resolver userResolver, cfg *config.Config) *OIDCHandler {
	return &OIDCHandler{
		queries:  queries,
		oauth:    oauth,
		resolver: resolver,
		cfg:      cfg,
	}
}

// providerEntry is the browser-safe view of a Provider — only public fields.
type providerEntry struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}

// ListProviders handles GET /v1/auth/providers. It is unauthenticated so the
// login page can render provider buttons before the user has a session.
// Only successfully-initialized providers are returned; a provider whose
// discovery document failed at boot is silently omitted so the GUI only
// offers buttons that actually work. Never include client_secret, issuer_url,
// or scopes in the response.
func (h *OIDCHandler) ListProviders(w http.ResponseWriter, r *http.Request) {
	enabled := h.oauth.EnabledProviders()
	out := make([]providerEntry, 0, len(enabled))
	for _, s := range enabled {
		out = append(out, providerEntry{ID: s.Provider.ID, DisplayName: s.Provider.DisplayName})
	}
	server.JSON(w, http.StatusOK, out)
}

// Start handles GET /v1/auth/oidc/{provider}/start. It validates the provider
// id and return path, writes the signed state cookie, and 302s the browser
// to the provider's authorization URL.
func (h *OIDCHandler) Start(w http.ResponseWriter, r *http.Request) {
	providerID := normalizeProviderID(r)

	returnPath := r.URL.Query().Get("return")
	// ?mode=test is the "Test configuration" affordance from the Edit
	// modal — the browser runs the full authorize/callback loop but
	// the callback skips resolver + JWT so the admin's session is
	// untouched. Any other mode value is ignored (fall through to
	// normal login), matching the "be generous in what you accept"
	// spirit of OAuth start.
	testMode := r.URL.Query().Get("mode") == "test"
	authURL, state, err := h.oauth.AuthorizeURL(providerID, returnPath, testMode)
	if err != nil {
		// Unknown IDs and init-failed providers both surface as 404 — an
		// operator-observable failure (slog.Warn at boot) that the user has
		// no agency to fix mid-request.
		if errors.Is(err, localauth.ErrUnknownProvider) || errors.Is(err, localauth.ErrProviderNotAvailable) {
			server.Error(w, http.StatusNotFound, "not_found", "unknown provider")
			return
		}
		slog.WarnContext(r.Context(), "oidc start: bad request", "error", err, "provider", providerID)
		server.Error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	http.SetCookie(w, h.newStateCookie(state, stateCookieMaxAge, r))
	http.Redirect(w, r, authURL, http.StatusFound)
}

// Callback handles GET /v1/auth/oidc/{provider}/callback. It trades the
// authorization code for an id_token, resolves the user, mints a local JWT,
// writes an audit row, and 302s to the GUI return page with the JWT in the
// URL fragment so it never hits any server log.
func (h *OIDCHandler) Callback(w http.ResponseWriter, r *http.Request) {
	providerID := normalizeProviderID(r)

	// Clear the state cookie regardless of outcome — one-shot usage.
	h.clearStateCookie(w, r)

	// Short-circuit on an unknown or init-failed provider so the browser
	// sees a 404 rather than a confusing "missing state cookie" 400 that it
	// cannot remediate. Enabled providers fall through to the full flow
	// below; init-failed providers are indistinguishable from unknown at
	// the wire.
	if !h.oauth.ProviderAvailable(providerID) {
		server.Error(w, http.StatusNotFound, "not_found", "unknown provider")
		return
	}

	q := r.URL.Query()
	if providerErr := q.Get("error"); providerErr != "" {
		h.redirectToFrontendError(w, r, providerErr)
		return
	}

	code := q.Get("code")
	rawState := q.Get("state")
	if code == "" || rawState == "" {
		server.Error(w, http.StatusBadRequest, "bad_request", "missing code or state")
		return
	}

	cookie, err := r.Cookie(stateCookieName)
	if err != nil {
		server.Error(w, http.StatusBadRequest, "bad_request", "missing state cookie")
		return
	}

	principal, statePayload, err := h.oauth.Exchange(r.Context(), providerID, code, rawState, cookie.Value)
	if err != nil {
		slog.WarnContext(r.Context(), "oidc callback: exchange failed", "error", err, "provider", providerID)
		// Unknown ID or init-failed provider → 404, same policy as /start.
		if errors.Is(err, localauth.ErrUnknownProvider) || errors.Is(err, localauth.ErrProviderNotAvailable) {
			server.Error(w, http.StatusNotFound, "not_found", "unknown provider")
			return
		}
		// State/cookie problems are client-fixable → 400. Everything downstream
		// (token endpoint, id_token verify) reports a generic error via redirect
		// so the GUI can surface it and the operator can inspect logs.
		if errors.Is(err, localauth.ErrStateValidation) {
			server.Error(w, http.StatusBadRequest, "bad_request", "invalid or expired state")
			return
		}
		// Test mode: surface the exchange/verify error directly on the
		// /oidc-config banner instead of the generic "authentication_failed"
		// redirect, since diagnosing the IdP config is the entire point.
		if statePayload.TestMode {
			h.redirectToTestResult(w, r, providerID, "", "", "", err.Error())
			return
		}
		h.redirectToFrontendError(w, r, "authentication_failed")
		return
	}

	// Test path: id_token verified cleanly. Skip resolver + JWT
	// (no session mutation) and bounce back to /oidc-config with the
	// verified identity details so the admin can confirm what the IdP
	// asserted.
	if statePayload.TestMode {
		slog.InfoContext(r.Context(), "oidc test succeeded",
			"provider", providerID,
			"email", principal.Email,
			"subject", principal.Subject,
		)
		h.redirectToTestResult(w, r, providerID, principal.Email, principal.Subject, principal.Issuer, "")
		return
	}

	if principal.Email == "" {
		// Without an email we can't link to or create a user record. This is
		// almost always a scope misconfiguration on the IdP side.
		slog.WarnContext(r.Context(), "oidc callback: principal missing email", "provider", providerID)
		h.redirectToFrontendError(w, r, "missing_email")
		return
	}

	uc, err := h.resolver.Resolve(r.Context(), principal)
	if err != nil {
		if errors.Is(err, localauth.ErrUserGone) {
			slog.WarnContext(r.Context(), "oidc callback: user gone", "error", err, "provider", providerID)
			h.redirectToFrontendError(w, r, "authentication_failed")
			return
		}
		slog.ErrorContext(r.Context(), "oidc callback: resolve user internal error", "error", err, "provider", providerID)
		server.Error(w, http.StatusInternalServerError, "internal_error", "user resolution failed")
		return
	}

	ua, err := h.queries.GetUserAccountByID(r.Context(), uc.UserAccountID)
	if err != nil {
		slog.ErrorContext(r.Context(), "oidc callback: reload user account", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "user account reload failed")
		return
	}

	token, err := localauth.IssueLocalJWT(ua, uc.IsAdmin, h.cfg.LocalAuth.JWTSecret, h.cfg.LocalAuth.LocalIssuer)
	if err != nil {
		slog.ErrorContext(r.Context(), "oidc callback: issue jwt", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "token issuance failed")
		return
	}

	slog.InfoContext(r.Context(), "oidc login succeeded",
		"provider", providerID,
		"user_account_uuid", ua.Uuid.String(),
	)

	redirectURL := h.buildSuccessRedirect(token, statePayload.ReturnPath)
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

// buildSuccessRedirect composes the GUI return URL with the JWT and return
// path in the URL fragment so they never appear in server logs.
func (h *OIDCHandler) buildSuccessRedirect(token, returnPath string) string {
	base := h.oauth.FrontendReturnURL
	frag := url.Values{}
	frag.Set("token", token)
	frag.Set("return", returnPath)
	// Using url.Values encoding with '#' instead of '?' because the spec
	// wants these in the fragment.
	return base + "#" + frag.Encode()
}

// redirectToFrontendError 302s to the frontend return URL with a query
// parameter describing a generic error code. We intentionally do not leak
// internal errors; operators can correlate via slog.
func (h *OIDCHandler) redirectToFrontendError(w http.ResponseWriter, r *http.Request, code string) {
	u, err := url.Parse(h.oauth.FrontendReturnURL)
	if err != nil {
		// Fall back to a plain error response if the configured URL is bad.
		server.Error(w, http.StatusInternalServerError, "internal_error", "authentication failed")
		return
	}
	q := u.Query()
	q.Set("error", code)
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

// redirectToTestResult 302s back to /oidc-config (same origin as the
// configured FrontendReturnURL) with the test outcome as query params.
// Unlike the real-login path we send result fields in the QUERY string,
// not the fragment — there's no secret to hide and the GUI reads them
// via useSearchParams. testErr is the human-readable error when the
// test failed; pass "" on success.
func (h *OIDCHandler) redirectToTestResult(
	w http.ResponseWriter, r *http.Request,
	providerID, email, subject, issuer, testErr string,
) {
	u, err := url.Parse(h.oauth.FrontendReturnURL)
	if err != nil {
		server.Error(w, http.StatusInternalServerError, "internal_error", "test configuration failed")
		return
	}
	// FrontendReturnURL points at /auth/oidc/return; the test banner
	// lives on /oidc-config (same origin) so swap the path.
	u.Path = "/oidc-config"
	u.Fragment = ""
	q := url.Values{}
	if testErr != "" {
		q.Set("test_result", "fail")
		q.Set("test_error", testErr)
	} else {
		q.Set("test_result", "ok")
		q.Set("test_email", email)
		q.Set("test_sub", subject)
		q.Set("test_issuer", issuer)
	}
	q.Set("test_provider", providerID)
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

// newStateCookie builds the oidc_state cookie with the security attributes
// described in the spec. "Secure" is set iff the inbound request came in
// over HTTPS or through a proxy that flagged it (X-Forwarded-Proto=https).
func (h *OIDCHandler) newStateCookie(value string, maxAge int, r *http.Request) *http.Cookie {
	return &http.Cookie{
		Name:     stateCookieName,
		Value:    value,
		Path:     stateCookiePath,
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   requestIsHTTPS(r),
		SameSite: http.SameSiteLaxMode,
	}
}

// clearStateCookie tombstones the state cookie by setting MaxAge<0.
func (h *OIDCHandler) clearStateCookie(w http.ResponseWriter, r *http.Request) {
	c := h.newStateCookie("", -1, r)
	http.SetCookie(w, c)
}

// requestIsHTTPS decides whether to set the Secure cookie flag based on the
// inbound request. Handles the common reverse-proxy case via X-Forwarded-Proto.
func requestIsHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if xf := r.Header.Get("X-Forwarded-Proto"); xf != "" {
		return xf == "https"
	}
	return false
}

