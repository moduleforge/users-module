package auth

import "context"

// Principal holds the normalized identity extracted from a verified JWT's claims.
// It represents who the token says the bearer is, before any database lookup.
type Principal struct {
	Subject         string   // OIDC sub
	Issuer          string   // OIDC iss
	Email           string
	Roles           []string // normalized, lowercased
	AssumedUserUUID string   // non-empty for assume-identity JWTs; UUID of the assumed user
}

// UserContext is the fully resolved in-process identity, populated after the
// Principal has been matched against the user_accounts table.
type UserContext struct {
	UserAccountID int64
	UserUUID      string
	EntityID      int64
	Email         string
	IsAdmin       bool             // user_accounts.is_admin OR principal has admin role
	AssumedUser   *AssumedUserInfo // non-nil while admin is assuming another user
	AppID         *int64           // resolved app context
	AppRoles      []string
}

// AssumedUserInfo carries the identity of the user an admin is currently impersonating.
type AssumedUserInfo struct {
	UserAccountID int64
	UserUUID      string
	EntityID      int64
	Email         string
}

type contextKey int

const userContextKey contextKey = iota

// WithUserContext stores a UserContext on the provided context.
func WithUserContext(ctx context.Context, uc *UserContext) context.Context {
	return context.WithValue(ctx, userContextKey, uc)
}

// FromContext retrieves the UserContext stored on ctx, if any.
func FromContext(ctx context.Context) (*UserContext, bool) {
	uc, ok := ctx.Value(userContextKey).(*UserContext)
	return uc, ok
}

// MustFromContext retrieves the UserContext stored on ctx and panics if it is absent.
// Use only in handlers that are guaranteed to run behind authentication middleware.
func MustFromContext(ctx context.Context) *UserContext {
	uc, ok := FromContext(ctx)
	if !ok {
		panic("auth: UserContext not on context")
	}
	return uc
}
