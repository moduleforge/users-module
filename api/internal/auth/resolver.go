package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	coredb "github.com/moduleforge/core-model/db"
	db "github.com/moduleforge/users-module/model/db"
)

// ErrUserGone is returned by UserResolver.Resolve when a locally-issued JWT
// references a user account that no longer exists in the user_accounts table
// (e.g., the account was deleted after the token was minted). Handlers should
// translate this into 401 Unauthorized — the signature is valid but the identity
// is no longer valid.
var ErrUserGone = errors.New("auth: user no longer exists")

// uuidLookupFn is the slot used by the local-issuer fast path. Extracted as
// a field so tests can substitute a stub without needing a running Postgres.
// Nil is valid — the fast path short-circuits and falls back to the OIDC
// path in that case (useful for pre-Phase 9 fallback semantics in tests).
type uuidLookupFn func(ctx context.Context, u uuid.UUID) (db.UserAccount, error)

// UserResolver resolves a Principal to a *UserContext. For OIDC principals it
// auto-creates the user account on first sight; for locally-issued JWTs
// (matching LocalIssuer) it takes a fast path that simply loads by UUID.
type UserResolver struct {
	pool        *pgxpool.Pool
	queries     *db.Queries
	coreQ       *coredb.Queries
	adminRole   string
	localIssuer string
	uuidLookup  uuidLookupFn
}

// NewUserResolver creates a resolver. localIssuer is the value written into
// the "iss" claim by IssueLocalJWT — when Resolve sees a Principal with this
// issuer, it skips the OIDC auto-create path and looks up the user account by UUID.
func NewUserResolver(pool *pgxpool.Pool, queries *db.Queries, coreQ *coredb.Queries, adminRole, localIssuer string) *UserResolver {
	if adminRole == "" {
		adminRole = "admin"
	}
	r := &UserResolver{
		pool:        pool,
		queries:     queries,
		coreQ:       coreQ,
		adminRole:   adminRole,
		localIssuer: localIssuer,
	}
	if queries != nil {
		r.uuidLookup = queries.GetUserAccountByUUID
	}
	return r
}

// Resolve looks up or creates the user account associated with the given Principal.
// On first-ever user, sets is_admin = true (root bootstrap).
func (r *UserResolver) Resolve(ctx context.Context, p Principal) (*UserContext, error) {
	// Local-issuer fast path. A principal minted by IssueLocalJWT carries
	// the user account's own UUID in `sub`; the HS256 signature has already
	// proven authenticity, so we only need to hydrate the DB row. No
	// auto-create, no email-link attempt. A missing UUID means the account
	// was deleted between token issue and use — 401, not 500.
	if r.localIssuer != "" && p.Issuer == r.localIssuer && r.uuidLookup != nil {
		parsed, err := uuid.Parse(p.Subject)
		if err != nil {
			return nil, fmt.Errorf("auth: local jwt sub is not a uuid: %w", err)
		}
		ua, err := r.uuidLookup(ctx, parsed)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, ErrUserGone
			}
			return nil, fmt.Errorf("auth: lookup local user account by uuid: %w", err)
		}
		return r.buildUserContext(ua, p), nil
	}

	// Try to find existing user account by auth credentials.
	if p.Issuer != "" && p.Subject != "" {
		ua, err := r.queries.GetUserAccountByAuth(ctx, db.GetUserAccountByAuthParams{
			AuthIssuer: pgtype.Text{String: p.Issuer, Valid: true},
			AuthID:     pgtype.Text{String: p.Subject, Valid: true},
		})
		if err == nil {
			return r.buildUserContext(ua, p), nil
		}
		if err != pgx.ErrNoRows {
			return nil, fmt.Errorf("auth: lookup by auth: %w", err)
		}
	}

	// Try by email if available.
	if p.Email != "" {
		ua, err := r.queries.GetUserAccountByEmail(ctx, p.Email)
		if err == nil {
			// Found by email — link auth credentials if not already set.
			if p.Issuer != "" && p.Subject != "" {
				_ = r.queries.UpdateUserAccount(ctx, db.UpdateUserAccountParams{
					ID:              ua.ID,
					Email:           ua.Email,
					EmailVerifiedAt: ua.EmailVerifiedAt,
					AuthIssuer:      pgtype.Text{String: p.Issuer, Valid: true},
					AuthID:          pgtype.Text{String: p.Subject, Valid: true},
				})
			}
			return r.buildUserContext(ua, p), nil
		}
		if err != pgx.ErrNoRows {
			return nil, fmt.Errorf("auth: lookup by email: %w", err)
		}
	}

	// New user — auto-create within a transaction.
	ua, err := r.autoCreate(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("auth: auto-create user account: %w", err)
	}

	return r.buildUserContext(ua, p), nil
}

// autoCreate creates a new entity → legal_entity → natural_person → user_account chain.
func (r *UserResolver) autoCreate(ctx context.Context, p Principal) (db.UserAccount, error) {
	var ua db.UserAccount

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return ua, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	qtx := r.queries.WithTx(tx)
	coreQtx := r.coreQ.WithTx(tx)

	// Check if this is the first user account (root bootstrap).
	var userAccountCount int64
	err = tx.QueryRow(ctx, "SELECT count(*) FROM user_accounts").Scan(&userAccountCount)
	if err != nil {
		return ua, fmt.Errorf("count user_accounts: %w", err)
	}
	isFirstUser := userAccountCount == 0

	// Resolve the natural_person type ID from the types registry.
	npType, err := coreQtx.GetTypeBySlug(ctx, "natural_person")
	if err != nil {
		return ua, fmt.Errorf("resolve natural_person type: %w", err)
	}

	// Create entity.
	entity, err := coreQtx.CreateEntity(ctx, npType.ID)
	if err != nil {
		return ua, fmt.Errorf("create entity: %w", err)
	}

	// Create legal entity (pure FK anchor — no kind/display_name).
	_, err = coreQtx.CreateLegalEntity(ctx, entity.ID)
	if err != nil {
		return ua, fmt.Errorf("create legal entity: %w", err)
	}

	// Derive given_name from email local-part for auto-created accounts.
	givenName := p.Email
	if idx := strings.Index(p.Email, "@"); idx > 0 {
		givenName = p.Email[:idx]
	}

	// Create natural person.
	_, err = coreQtx.CreateNaturalPerson(ctx, coredb.CreateNaturalPersonParams{
		EntityID:   entity.ID,
		GivenName:  pgtype.Text{String: givenName, Valid: true},
		FamilyName: pgtype.Text{},
	})
	if err != nil {
		return ua, fmt.Errorf("create natural person: %w", err)
	}

	// Create user account. account_holder references legal_entities(entity_id),
	// so entity.ID is valid here because we just created the legal_entity row.
	var authIssuer, authID pgtype.Text
	if p.Issuer != "" {
		authIssuer = pgtype.Text{String: p.Issuer, Valid: true}
	}
	if p.Subject != "" {
		authID = pgtype.Text{String: p.Subject, Valid: true}
	}

	ua, err = qtx.CreateUserAccount(ctx, db.CreateUserAccountParams{
		AccountHolder: entity.ID,
		Email:         p.Email,
		IsAdmin:       isFirstUser,
		AuthIssuer:    authIssuer,
		AuthID:        authID,
	})
	if err != nil {
		return ua, fmt.Errorf("create user account: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return ua, fmt.Errorf("commit: %w", err)
	}

	if isFirstUser {
		slog.InfoContext(ctx, "first user account created with admin privileges",
			"email", p.Email,
			"user_account_uuid", ua.Uuid.String(),
		)
	}

	return ua, nil
}

func (r *UserResolver) buildUserContext(ua db.UserAccount, p Principal) *UserContext {
	// Admin if DB flag is set OR principal has admin role.
	isAdmin := ua.IsAdmin
	if !isAdmin {
		for _, role := range p.Roles {
			if role == r.adminRole {
				isAdmin = true
				break
			}
		}
	}

	uc := &UserContext{
		UserAccountID: ua.ID,
		UserUUID:      ua.Uuid.String(),
		EntityID:      ua.AccountHolder,
		Email:         ua.Email,
		IsAdmin:       isAdmin,
	}

	if ua.DefaultAppID.Valid {
		appID := ua.DefaultAppID.Int64
		uc.AppID = &appID
	}

	return uc
}
