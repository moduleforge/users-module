package auth

import (
	"context"
	"testing"
)

// baseClaims returns the mandatory OIDC claims shared by all test cases.
func baseClaims() map[string]any {
	return map[string]any{
		"sub": "user-123",
		"iss": "https://issuer.example.com",
	}
}

// merge returns a shallow copy of base with the extra key/value pairs applied.
func merge(base map[string]any, extra map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(extra))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

// containsRole reports whether roles contains the target role.
func containsRole(roles []string, target string) bool {
	for _, r := range roles {
		if r == target {
			return true
		}
	}
	return false
}

func TestNewClaimMapper_UnknownStyle(t *testing.T) {
	_, err := NewClaimMapper("unknown-provider", MapperOptions{})
	if err == nil {
		t.Fatal("expected error for unknown style, got nil")
	}
}

// TestClaimMapper is the main table-driven test covering all providers.
func TestClaimMapper(t *testing.T) {
	type testCase struct {
		name      string
		style     string
		opts      MapperOptions
		claims    map[string]any
		wantEmail string
		wantAdmin bool // whether "admin" role should be present
		wantError bool
	}

	tests := []testCase{
		// ------------------------------------------------------------------ google
		{
			name:      "google/basic",
			style:     "google",
			claims:    merge(baseClaims(), map[string]any{"email": "user@gmail.com"}),
			wantEmail: "user@gmail.com",
			wantAdmin: false,
		},
		{
			name:      "google/no-email",
			style:     "google",
			claims:    baseClaims(),
			wantEmail: "",
			wantAdmin: false,
		},
		{
			name:      "google/missing-sub",
			style:     "google",
			claims:    map[string]any{"iss": "https://accounts.google.com", "email": "x@gmail.com"},
			wantError: true,
		},
		{
			name:      "google/missing-iss",
			style:     "google",
			claims:    map[string]any{"sub": "abc", "email": "x@gmail.com"},
			wantError: true,
		},

		// --------------------------------------------------------------- microsoft
		{
			name:      "microsoft/email-claim",
			style:     "microsoft",
			claims:    merge(baseClaims(), map[string]any{"email": "user@corp.com", "roles": []any{"admin", "Editor"}}),
			wantEmail: "user@corp.com",
			wantAdmin: true,
		},
		{
			name:      "microsoft/preferred-username-fallback",
			style:     "microsoft",
			claims:    merge(baseClaims(), map[string]any{"preferred_username": "user@corp.com"}),
			wantEmail: "user@corp.com",
			wantAdmin: false,
		},
		{
			name:  "microsoft/wids-global-admin",
			style: "microsoft",
			claims: merge(baseClaims(), map[string]any{
				"email": "ga@corp.com",
				"wids":  []any{"62e90394-69f5-4237-9190-012177145e10"},
			}),
			wantEmail: "ga@corp.com",
			wantAdmin: true,
		},
		{
			name:  "microsoft/wids-unknown-guid",
			style: "microsoft",
			claims: merge(baseClaims(), map[string]any{
				"email": "regular@corp.com",
				"wids":  []any{"00000000-0000-0000-0000-000000000000"},
			}),
			wantEmail: "regular@corp.com",
			wantAdmin: false,
		},
		{
			name:      "microsoft/missing-sub",
			style:     "microsoft",
			claims:    map[string]any{"iss": "https://login.microsoftonline.com/tenant/v2.0"},
			wantError: true,
		},

		// --------------------------------------------------------------- authelia
		{
			name:      "authelia/admin-group",
			style:     "authelia",
			claims:    merge(baseClaims(), map[string]any{"email": "u@example.com", "groups": []any{"admin", "users"}}),
			wantEmail: "u@example.com",
			wantAdmin: true,
		},
		{
			name:      "authelia/no-admin-group",
			style:     "authelia",
			claims:    merge(baseClaims(), map[string]any{"email": "u@example.com", "groups": []any{"users"}}),
			wantEmail: "u@example.com",
			wantAdmin: false,
		},
		{
			name:      "authelia/custom-admin-role",
			style:     "authelia",
			opts:      MapperOptions{AdminRole: "superuser"},
			claims:    merge(baseClaims(), map[string]any{"email": "u@example.com", "groups": []any{"superuser"}}),
			wantEmail: "u@example.com",
			wantAdmin: true, // "admin" check here uses the custom role name
		},
		{
			name:      "authelia/missing-iss",
			style:     "authelia",
			claims:    map[string]any{"sub": "abc"},
			wantError: true,
		},

		// --------------------------------------------------------------- keycloak
		{
			name:  "keycloak/realm-admin",
			style: "keycloak",
			claims: merge(baseClaims(), map[string]any{
				"email":        "user@realm.com",
				"realm_access": map[string]any{"roles": []any{"admin", "offline_access"}},
			}),
			wantEmail: "user@realm.com",
			wantAdmin: true,
		},
		{
			name:  "keycloak/no-admin-role",
			style: "keycloak",
			claims: merge(baseClaims(), map[string]any{
				"email":        "user@realm.com",
				"realm_access": map[string]any{"roles": []any{"offline_access"}},
			}),
			wantEmail: "user@realm.com",
			wantAdmin: false,
		},
		{
			name:      "keycloak/no-realm-access",
			style:     "keycloak",
			claims:    merge(baseClaims(), map[string]any{"email": "user@realm.com"}),
			wantEmail: "user@realm.com",
			wantAdmin: false,
		},
		{
			name:      "keycloak/missing-sub",
			style:     "keycloak",
			claims:    map[string]any{"iss": "https://keycloak.example.com/realms/myrealm"},
			wantError: true,
		},

		// --------------------------------------------------------------- cognito
		{
			name:      "cognito/admin-group",
			style:     "cognito",
			claims:    merge(baseClaims(), map[string]any{"email": "u@aws.com", "cognito:groups": []any{"admin", "readers"}}),
			wantEmail: "u@aws.com",
			wantAdmin: true,
		},
		{
			name:      "cognito/no-groups",
			style:     "cognito",
			claims:    merge(baseClaims(), map[string]any{"email": "u@aws.com"}),
			wantEmail: "u@aws.com",
			wantAdmin: false,
		},
		{
			name:      "cognito/missing-iss",
			style:     "cognito",
			claims:    map[string]any{"sub": "abc"},
			wantError: true,
		},

		// ------------------------------------------------------------------ auth0
		{
			name:  "auth0/namespaced-roles",
			style: "auth0",
			opts:  MapperOptions{RolesNamespace: "https://myapp.example.com"},
			claims: merge(baseClaims(), map[string]any{
				"email":                           "u@example.com",
				"https://myapp.example.com/roles": []any{"admin", "editor"},
			}),
			wantEmail: "u@example.com",
			wantAdmin: true,
		},
		{
			name:  "auth0/auto-detect-namespace",
			style: "auth0",
			claims: merge(baseClaims(), map[string]any{
				"email":                    "u@example.com",
				"https://someapp.io/roles": []any{"admin"},
			}),
			wantEmail: "u@example.com",
			wantAdmin: true,
		},
		{
			name:      "auth0/plain-roles-fallback",
			style:     "auth0",
			claims:    merge(baseClaims(), map[string]any{"email": "u@example.com", "roles": []any{"admin"}}),
			wantEmail: "u@example.com",
			wantAdmin: true,
		},
		{
			name:      "auth0/no-roles",
			style:     "auth0",
			claims:    merge(baseClaims(), map[string]any{"email": "u@example.com"}),
			wantEmail: "u@example.com",
			wantAdmin: false,
		},
		{
			name:      "auth0/missing-sub",
			style:     "auth0",
			claims:    map[string]any{"iss": "https://example.auth0.com/"},
			wantError: true,
		},

		// --------------------------------------------------------------- firebase
		{
			name:      "firebase/roles-array",
			style:     "firebase",
			claims:    merge(baseClaims(), map[string]any{"email": "u@fb.com", "roles": []any{"admin", "viewer"}}),
			wantEmail: "u@fb.com",
			wantAdmin: true,
		},
		{
			name:      "firebase/boolean-admin",
			style:     "firebase",
			claims:    merge(baseClaims(), map[string]any{"email": "u@fb.com", "admin": true}),
			wantEmail: "u@fb.com",
			wantAdmin: true,
		},
		{
			name:      "firebase/boolean-admin-false",
			style:     "firebase",
			claims:    merge(baseClaims(), map[string]any{"email": "u@fb.com", "admin": false}),
			wantEmail: "u@fb.com",
			wantAdmin: false,
		},
		{
			name:      "firebase/no-roles",
			style:     "firebase",
			claims:    merge(baseClaims(), map[string]any{"email": "u@fb.com"}),
			wantEmail: "u@fb.com",
			wantAdmin: false,
		},
		{
			name:      "firebase/missing-iss",
			style:     "firebase",
			claims:    map[string]any{"sub": "abc"},
			wantError: true,
		},

		// --------------------------------------------------------------- generic
		{
			name:  "generic/nested-roles",
			style: "generic",
			opts:  MapperOptions{EmailPath: "email", RolesPath: "realm_access.roles"},
			claims: merge(baseClaims(), map[string]any{
				"email":        "u@generic.com",
				"realm_access": map[string]any{"roles": []any{"admin", "user"}},
			}),
			wantEmail: "u@generic.com",
			wantAdmin: true,
		},
		{
			name:  "generic/flat-roles",
			style: "generic",
			opts:  MapperOptions{EmailPath: "email", RolesPath: "roles"},
			claims: merge(baseClaims(), map[string]any{
				"email": "u@generic.com",
				"roles": []any{"viewer"},
			}),
			wantEmail: "u@generic.com",
			wantAdmin: false,
		},
		{
			name:      "generic/missing-email-path",
			style:     "generic",
			opts:      MapperOptions{RolesPath: "roles"},
			claims:    baseClaims(),
			wantError: true,
		},
		{
			name:      "generic/missing-roles-path",
			style:     "generic",
			opts:      MapperOptions{EmailPath: "email"},
			claims:    baseClaims(),
			wantError: true,
		},
		{
			name:      "generic/missing-sub",
			style:     "generic",
			opts:      MapperOptions{EmailPath: "email", RolesPath: "roles"},
			claims:    map[string]any{"iss": "https://idp.example.com"},
			wantError: true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			mapper, err := NewClaimMapper(tc.style, tc.opts)
			if err != nil {
				t.Fatalf("NewClaimMapper(%q) returned unexpected error: %v", tc.style, err)
			}

			p, err := mapper.Map(tc.claims)
			if tc.wantError {
				if err == nil {
					t.Fatalf("Map() expected error, got nil; principal: %+v", p)
				}
				return
			}
			if err != nil {
				t.Fatalf("Map() unexpected error: %v", err)
			}

			if p.Subject == "" {
				t.Error("Principal.Subject must not be empty")
			}
			if p.Issuer == "" {
				t.Error("Principal.Issuer must not be empty")
			}
			if p.Email != tc.wantEmail {
				t.Errorf("Principal.Email = %q, want %q", p.Email, tc.wantEmail)
			}

			// For authelia custom-admin-role case, the "admin" role name is overridden.
			adminRole := "admin"
			if tc.opts.AdminRole != "" {
				adminRole = tc.opts.AdminRole
			}

			gotAdmin := containsRole(p.Roles, adminRole)
			if gotAdmin != tc.wantAdmin {
				t.Errorf("admin role present = %v, want %v (roles: %v)", gotAdmin, tc.wantAdmin, p.Roles)
			}

			// All roles must be lowercase.
			for _, r := range p.Roles {
				for _, c := range r {
					if c >= 'A' && c <= 'Z' {
						t.Errorf("role %q is not fully lowercased", r)
						break
					}
				}
			}
		})
	}
}

// TestContextRoundTrip verifies the principal context helpers.
func TestContextRoundTrip(t *testing.T) {
	uc := &UserContext{
		UserAccountID: 42,
		UserUUID:      "uuid-abc",
		Email:         "user@example.com",
		IsAdmin:       true,
	}

	ctx := WithUserContext(context.Background(), uc)

	got, ok := FromContext(ctx)
	if !ok {
		t.Fatal("FromContext returned ok=false, want true")
	}
	if got != uc {
		t.Errorf("FromContext returned different pointer: got %p, want %p", got, uc)
	}
}

func TestMustFromContext_Present(t *testing.T) {
	uc := &UserContext{UserAccountID: 1}
	ctx := WithUserContext(context.Background(), uc)
	got := MustFromContext(ctx)
	if got != uc {
		t.Error("MustFromContext returned wrong pointer")
	}
}

func TestMustFromContext_Absent(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("MustFromContext should panic when UserContext is absent")
		}
	}()
	MustFromContext(context.Background())
}

func TestFromContext_Empty(t *testing.T) {
	_, ok := FromContext(context.Background())
	if ok {
		t.Error("FromContext on empty context should return ok=false")
	}
}
