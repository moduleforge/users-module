package auth

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/moduleforge/core-api/opctx"
	"github.com/moduleforge/users-module/api/internal/server"
)

// Sentinel errors from AuthenticateRequest. Callers use errors.Is to
// distinguish "no session" (treat as not-authenticated, possibly fall
// through to an alternate auth path) from genuine internal faults.
var (
	// ErrNoAuthHeader is returned when the request has no Authorization
	// header or the header isn't in "Bearer <token>" form.
	ErrNoAuthHeader = errors.New("auth: no bearer token")
	// ErrInvalidToken is returned when the verifier rejects the token
	// (signature, expiry, issuer, audience, or parse failure).
	ErrInvalidToken = errors.New("auth: invalid or expired token")
)

// AuthenticateRequest runs the Bearer-extract → verify → claim-map →
// user-resolve pipeline on r. It does not touch the response writer;
// callers decide how to map errors to HTTP status codes.
//
// Error classification:
//   - ErrNoAuthHeader: missing or non-Bearer Authorization header.
//   - ErrInvalidToken: verifier rejected the token.
//   - ErrUserGone:     resolver reports the user no longer exists.
//   - any other error: mapper/resolver internal fault (treat as 500).
//
// This is shared by RequireAuth (which maps the errors to HTTP
// responses) and the onboarding AdminChecker (which maps them to
// "fall through to setup-token" vs "500").
func AuthenticateRequest(r *http.Request, verifier *Verifier, mapper ClaimMapper, resolver *UserResolver) (*UserContext, error) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return nil, ErrNoAuthHeader
	}
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return nil, ErrNoAuthHeader
	}
	rawToken := strings.TrimPrefix(authHeader, "Bearer ")

	claims, err := verifier.Verify(r.Context(), rawToken)
	if err != nil {
		return nil, ErrInvalidToken
	}

	principal, err := mapper.Map(claims)
	if err != nil {
		return nil, fmt.Errorf("claim map: %w", err)
	}

	uc, err := resolver.Resolve(r.Context(), principal)
	if err != nil {
		return nil, err // may wrap ErrUserGone, or be an internal fault
	}
	return uc, nil
}

// RequireAuth returns middleware that validates the Authorization header,
// maps claims to a Principal, resolves/creates the user, and stores
// *UserContext on the request context.
func RequireAuth(verifier *Verifier, mapper ClaimMapper, resolver *UserResolver) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			uc, err := AuthenticateRequest(r, verifier, mapper, resolver)
			if err != nil {
				switch {
				case errors.Is(err, ErrNoAuthHeader):
					// Preserve the pre-9.10a distinction between missing
					// header and malformed header for any callers that
					// assert on the message.
					if r.Header.Get("Authorization") == "" {
						server.Error(w, http.StatusUnauthorized, "unauthorized", "missing Authorization header")
					} else {
						server.Error(w, http.StatusUnauthorized, "unauthorized", "invalid Authorization header format")
					}
				case errors.Is(err, ErrInvalidToken):
					server.Error(w, http.StatusUnauthorized, "unauthorized", "invalid or expired token")
				case errors.Is(err, ErrUserGone):
					server.Error(w, http.StatusUnauthorized, "unauthorized", "user no longer exists")
				default:
					// Classify mapper vs resolver so ops can tell them
					// apart; the helper doesn't differentiate but the
					// error message does.
					if strings.HasPrefix(err.Error(), "claim map:") {
						slog.ErrorContext(r.Context(), "claim mapper error", "error", err)
						server.Error(w, http.StatusInternalServerError, "internal_error", "failed to process authentication claims")
					} else {
						slog.ErrorContext(r.Context(), "user resolve error", "error", err)
						server.Error(w, http.StatusInternalServerError, "internal_error", "failed to resolve user")
					}
				}
				return
			}
			// Populate opctx values so downstream service code and the
			// Authorizer can read actor identity via opctx.ActorEntityID.
			// WithUserContext preserves the richer UserContext for handler-level
			// code that needs fields beyond what opctx exposes (email, roles, etc.).
			ctx := WithUserContext(r.Context(), uc)
			ctx = opctx.WithActor(ctx, uc.EntityID)
			if uc.AssumedUser != nil {
				ctx = opctx.WithAssumedActor(ctx, uc.AssumedUser.EntityID)
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireAdmin checks that the current user is an admin. 403 otherwise.
func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uc, ok := FromContext(r.Context())
		if !ok || !uc.IsAdmin {
			server.Error(w, http.StatusForbidden, "forbidden", "admin access required")
			return
		}
		next.ServeHTTP(w, r)
	})
}
