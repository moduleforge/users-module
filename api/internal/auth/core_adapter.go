package auth

import (
	"context"

	coreservice "github.com/moduleforge/core-api/service"
)

// CorePrincipalAdapter implements coreservice.PrincipalExtractor by reading
// the users-module UserContext from ctx and mapping it to core's Principal.
// The AssumedUser concept is intentionally omitted — UserID and EntityID in
// UserContext already reflect the effective (possibly assumed) identity as
// populated by RequireAuth middleware.
type CorePrincipalAdapter struct{}

// Compile-time assertion.
var _ coreservice.PrincipalExtractor = CorePrincipalAdapter{}

// FromContext extracts a coreservice.Principal from ctx. Returns (nil, false)
// when no UserContext is present (i.e. unauthenticated request).
func (CorePrincipalAdapter) FromContext(ctx context.Context) (*coreservice.Principal, bool) {
	uc, ok := FromContext(ctx)
	if !ok {
		return nil, false
	}
	return &coreservice.Principal{
		UserID:   uc.UserID,
		EntityID: uc.EntityID,
		IsAdmin:  uc.IsAdmin,
	}, true
}
