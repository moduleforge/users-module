package auth

import "fmt"

// genericMapper is a configurable mapper suitable for any OIDC provider whose
// claim layout can be described with dot-separated paths.
//
// opts.EmailPath and opts.RolesPath must be set; both accept dot-notation like
// "realm_access.roles" or simply "email".
type genericMapper struct {
	opts MapperOptions
}

func (m *genericMapper) Map(rawClaims map[string]any) (Principal, error) {
	sub, iss, err := extractRequired(rawClaims)
	if err != nil {
		return Principal{}, err
	}

	if m.opts.EmailPath == "" {
		return Principal{}, fmt.Errorf("auth: generic mapper requires opts.EmailPath")
	}
	if m.opts.RolesPath == "" {
		return Principal{}, fmt.Errorf("auth: generic mapper requires opts.RolesPath")
	}

	email := getStringByPath(rawClaims, m.opts.EmailPath)
	roles := lowercaseAll(getStringSliceByPath(rawClaims, m.opts.RolesPath))

	// Extract assumed_user_uuid if present (set by IssueAssumeJWT).
	assumedUserUUID, _ := rawClaims["assumed_user_uuid"].(string)

	return Principal{
		Subject:         sub,
		Issuer:          iss,
		Email:           email,
		Roles:           roles,
		AssumedUserUUID: assumedUserUUID,
	}, nil
}
