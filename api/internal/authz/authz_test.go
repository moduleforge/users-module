package authz_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/moduleforge/core-api/entity"
	"github.com/moduleforge/core-api/opctx"
	"github.com/moduleforge/users-module/api/internal/authz"
	db "github.com/moduleforge/users-module/model/db"
)

// stubQuerier is a minimal db.Querier that only supports GetUserAccountByAccountHolder.
// All other methods panic to catch unexpected calls.
type stubQuerier struct {
	// accountsByEntityID maps entity_id (account_holder) to UserAccount.
	accountsByEntityID map[int64]db.UserAccount
}

func newStubQuerier() *stubQuerier {
	return &stubQuerier{accountsByEntityID: make(map[int64]db.UserAccount)}
}

func (q *stubQuerier) seed(entityID int64, isAdmin bool) db.UserAccount {
	ua := db.UserAccount{
		ID:            entityID * 10, // arbitrary non-zero ID
		Uuid:          uuid.New(),
		AccountHolder: entityID,
		Email:         "user@example.com",
		IsAdmin:       isAdmin,
	}
	q.accountsByEntityID[entityID] = ua
	return ua
}

func (q *stubQuerier) GetUserAccountByAccountHolder(_ context.Context, accountHolder int64) (db.UserAccount, error) {
	if ua, ok := q.accountsByEntityID[accountHolder]; ok {
		return ua, nil
	}
	return db.UserAccount{}, pgx.ErrNoRows
}

// --- Implement the rest of db.Querier so stubQuerier satisfies the interface ---

func (q *stubQuerier) ArchiveApp(_ context.Context, _ int64) error { panic("not implemented") }
func (q *stubQuerier) AssignUserAccountToApp(_ context.Context, _ db.AssignUserAccountToAppParams) error {
	panic("not implemented")
}
func (q *stubQuerier) ClearSetupTokenHash(_ context.Context) error { panic("not implemented") }
func (q *stubQuerier) ConsumeEmailCode(_ context.Context, _ int64) error {
	panic("not implemented")
}
func (q *stubQuerier) ConsumePasswordReset(_ context.Context, _ int64) error {
	panic("not implemented")
}
func (q *stubQuerier) CreateApp(_ context.Context, _ db.CreateAppParams) (db.App, error) {
	panic("not implemented")
}
func (q *stubQuerier) CreateEmailCode(_ context.Context, _ db.CreateEmailCodeParams) (db.EmailCode, error) {
	panic("not implemented")
}
func (q *stubQuerier) CreatePasswordReset(_ context.Context, _ db.CreatePasswordResetParams) (db.PasswordReset, error) {
	panic("not implemented")
}
func (q *stubQuerier) CreateUserAccount(_ context.Context, _ db.CreateUserAccountParams) (db.UserAccount, error) {
	panic("not implemented")
}
func (q *stubQuerier) DeleteAuthLocal(_ context.Context, _ int64) error { panic("not implemented") }
func (q *stubQuerier) DeleteOIDCProvider(_ context.Context, _ string) (int64, error) {
	panic("not implemented")
}
func (q *stubQuerier) GetActiveEmailCode(_ context.Context, _ db.GetActiveEmailCodeParams) (db.EmailCode, error) {
	panic("not implemented")
}
func (q *stubQuerier) GetActivePasswordReset(_ context.Context, _ string) (db.PasswordReset, error) {
	panic("not implemented")
}
func (q *stubQuerier) GetAppBySlug(_ context.Context, _ string) (db.App, error) {
	panic("not implemented")
}
func (q *stubQuerier) GetAppByUUID(_ context.Context, _ uuid.UUID) (db.App, error) {
	panic("not implemented")
}
func (q *stubQuerier) GetAuthLocal(_ context.Context, _ int64) (db.AuthLocal, error) {
	panic("not implemented")
}
func (q *stubQuerier) GetOIDCConfig(_ context.Context) (db.OidcConfig, error) {
	panic("not implemented")
}
func (q *stubQuerier) GetOIDCProvider(_ context.Context, _ string) (db.OidcProvider, error) {
	panic("not implemented")
}
func (q *stubQuerier) GetUserAccountByAuth(_ context.Context, _ db.GetUserAccountByAuthParams) (db.UserAccount, error) {
	panic("not implemented")
}
func (q *stubQuerier) GetUserAccountByEmail(_ context.Context, _ string) (db.UserAccount, error) {
	panic("not implemented")
}
func (q *stubQuerier) GetUserAccountByID(_ context.Context, _ int64) (db.UserAccount, error) {
	panic("not implemented")
}
func (q *stubQuerier) GetUserAccountByUUID(_ context.Context, _ uuid.UUID) (db.UserAccount, error) {
	panic("not implemented")
}
func (q *stubQuerier) ListAppUserAccounts(_ context.Context, _ int64) ([]db.AppsUserAccount, error) {
	panic("not implemented")
}
func (q *stubQuerier) ListApps(_ context.Context) ([]db.App, error) { panic("not implemented") }
func (q *stubQuerier) ListOIDCProviders(_ context.Context) ([]db.OidcProvider, error) {
	panic("not implemented")
}
func (q *stubQuerier) ListUserAccountApps(_ context.Context, _ int64) ([]db.AppsUserAccount, error) {
	panic("not implemented")
}
func (q *stubQuerier) RemoveUserAccountFromApp(_ context.Context, _ db.RemoveUserAccountFromAppParams) error {
	panic("not implemented")
}
func (q *stubQuerier) SearchUserAccounts(_ context.Context, _ db.SearchUserAccountsParams) ([]db.UserAccount, error) {
	panic("not implemented")
}
func (q *stubQuerier) SetAdmin(_ context.Context, _ db.SetAdminParams) error {
	panic("not implemented")
}
func (q *stubQuerier) SetAppUserAccountRoles(_ context.Context, _ db.SetAppUserAccountRolesParams) error {
	panic("not implemented")
}
func (q *stubQuerier) SetDefaultApp(_ context.Context, _ db.SetDefaultAppParams) error {
	panic("not implemented")
}
func (q *stubQuerier) SetOIDCProviderEnabled(_ context.Context, _ db.SetOIDCProviderEnabledParams) error {
	panic("not implemented")
}
func (q *stubQuerier) SetSetupTokenHash(_ context.Context, _ pgtype.Text) error {
	panic("not implemented")
}
func (q *stubQuerier) UpdateApp(_ context.Context, _ db.UpdateAppParams) error {
	panic("not implemented")
}
func (q *stubQuerier) UpdateOIDCConfig(_ context.Context, _ bool) error {
	panic("not implemented")
}
func (q *stubQuerier) UpdateUserAccount(_ context.Context, _ db.UpdateUserAccountParams) error {
	panic("not implemented")
}
func (q *stubQuerier) UpsertAuthLocal(_ context.Context, _ db.UpsertAuthLocalParams) error {
	panic("not implemented")
}
func (q *stubQuerier) UpsertOIDCProvider(_ context.Context, _ db.UpsertOIDCProviderParams) (db.OidcProvider, error) {
	panic("not implemented")
}

// Compile-time: stubQuerier satisfies db.Querier.
var _ db.Querier = (*stubQuerier)(nil)

// --- helpers ---

func ptr[T any](v T) *T { return &v }

// ctxWithActor returns a context with actor entity ID set.
func ctxWithActor(entityID int64) context.Context {
	return opctx.WithActor(context.Background(), entityID)
}

// ctxWithAssumedActor returns a context with both actor and assumed actor set.
func ctxWithAssumedActor(actorID, assumedID int64) context.Context {
	ctx := opctx.WithActor(context.Background(), actorID)
	return opctx.WithAssumedActor(ctx, assumedID)
}

// stubEntity is a minimal entity.Entity used in tests.
type stubEntity struct {
	resource string
	id       *int64
}

func (s stubEntity) Resource() string { return s.resource }
func (s stubEntity) EntityID() *int64 { return s.id }

// --- tests ---

// TestAuthorize_NoActor verifies that an unauthenticated context returns ErrUnauthenticated.
func TestAuthorize_NoActor(t *testing.T) {
	q := newStubQuerier()
	az := authz.New(q)

	err := az.Authorize(context.Background(), "read", stubEntity{resource: "user_account", id: ptr(int64(1))})
	if !errors.Is(err, authz.ErrUnauthenticated) {
		t.Errorf("expected ErrUnauthenticated, got: %v", err)
	}
}

// TestAuthorize_Admin_AllowsAnything verifies that an admin can perform any operation.
func TestAuthorize_Admin_AllowsAnything(t *testing.T) {
	q := newStubQuerier()
	q.seed(1, true) // entity_id=1 is admin
	az := authz.New(q)

	ctx := ctxWithActor(1)

	tests := []struct {
		action string
		target entity.Entity
	}{
		{"read", stubEntity{"user_account", ptr(int64(99))}},   // other user
		{"create", stubEntity{"user_account", nil}},             // pre-create (no ID)
		{"list", stubEntity{"user_account", nil}},               // list
		{"delete", stubEntity{"user_account", ptr(int64(42))}},  // any entity
		{"update", stubEntity{"user_account", ptr(int64(1))}},   // own entity
	}

	for _, tc := range tests {
		t.Run(tc.action, func(t *testing.T) {
			if err := az.Authorize(ctx, tc.action, tc.target); err != nil {
				t.Errorf("admin should be allowed for action=%q: got %v", tc.action, err)
			}
		})
	}
}

// TestAuthorize_NonAdmin_OwnData verifies that a non-admin can access their own entity.
func TestAuthorize_NonAdmin_OwnData(t *testing.T) {
	q := newStubQuerier()
	q.seed(7, false) // entity_id=7 is not admin
	az := authz.New(q)

	ctx := ctxWithActor(7)
	target := stubEntity{"user_account", ptr(int64(7))} // own entity

	if err := az.Authorize(ctx, "read", target); err != nil {
		t.Errorf("non-admin should be allowed to read own data: got %v", err)
	}
	if err := az.Authorize(ctx, "update", target); err != nil {
		t.Errorf("non-admin should be allowed to update own data: got %v", err)
	}
}

// TestAuthorize_NonAdmin_OtherUser verifies that a non-admin cannot access another user's data.
func TestAuthorize_NonAdmin_OtherUser(t *testing.T) {
	q := newStubQuerier()
	q.seed(7, false) // entity_id=7 is not admin
	az := authz.New(q)

	ctx := ctxWithActor(7)
	target := stubEntity{"user_account", ptr(int64(99))} // other user

	err := az.Authorize(ctx, "read", target)
	if !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("expected ErrForbidden for accessing other user's data, got: %v", err)
	}
}

// TestAuthorize_NonAdmin_CreateDenied verifies that a non-admin cannot create resources.
func TestAuthorize_NonAdmin_CreateDenied(t *testing.T) {
	q := newStubQuerier()
	q.seed(7, false)
	az := authz.New(q)

	ctx := ctxWithActor(7)
	target := stubEntity{"user_account", nil} // no entity ID: pre-create

	err := az.Authorize(ctx, "create", target)
	if !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("expected ErrForbidden for non-admin create, got: %v", err)
	}
}

// TestAuthorize_NonAdmin_ListDenied verifies that a non-admin cannot list resources.
func TestAuthorize_NonAdmin_ListDenied(t *testing.T) {
	q := newStubQuerier()
	q.seed(7, false)
	az := authz.New(q)

	ctx := ctxWithActor(7)
	target := stubEntity{"user_account", nil} // no entity ID: list stub

	err := az.Authorize(ctx, "list", target)
	if !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("expected ErrForbidden for non-admin list, got: %v", err)
	}
}

// TestAuthorize_AssumedActor_PolicyApplied verifies that when an admin assumes another user's
// identity, the assumed user's permissions (not the admin's) apply.
func TestAuthorize_AssumedActor_PolicyApplied(t *testing.T) {
	q := newStubQuerier()
	q.seed(1, true)  // entity_id=1 is admin (the real actor)
	q.seed(50, false) // entity_id=50 is not admin (the assumed user)
	az := authz.New(q)

	// Admin (entity 1) is assuming non-admin user (entity 50).
	ctx := ctxWithAssumedActor(1, 50)

	// Assumed user can access their own data.
	ownTarget := stubEntity{"user_account", ptr(int64(50))}
	if err := az.Authorize(ctx, "read", ownTarget); err != nil {
		t.Errorf("assumed user should be allowed to read own data: got %v", err)
	}

	// Assumed user CANNOT access someone else's data (policy is the assumed user's policy).
	otherTarget := stubEntity{"user_account", ptr(int64(99))}
	err := az.Authorize(ctx, "read", otherTarget)
	if !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("assumed user should be forbidden from accessing other's data: got %v", err)
	}

	// Assumed user CANNOT create (admin-only operation for non-admins).
	noIDTarget := stubEntity{"user_account", nil}
	err = az.Authorize(ctx, "create", noIDTarget)
	if !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("assumed user (non-admin) should be forbidden from create: got %v", err)
	}
}

// TestAuthorize_DBError_PropagatesAsInternalError verifies that a DB lookup failure
// is returned as-is (not wrapped as ErrForbidden or ErrUnauthenticated).
func TestAuthorize_DBError_PropagatesAsInternalError(t *testing.T) {
	q := newStubQuerier()
	// Do NOT seed entity 99 — GetUserAccountByAccountHolder will return pgx.ErrNoRows.
	az := authz.New(q)

	ctx := ctxWithActor(99)
	target := stubEntity{"user_account", ptr(int64(99))}

	err := az.Authorize(ctx, "read", target)
	if err == nil {
		t.Error("expected error for missing user account, got nil")
	}
	// The error should NOT be ErrForbidden or ErrUnauthenticated — it's an internal fault.
	if errors.Is(err, authz.ErrForbidden) {
		t.Error("DB lookup error should not surface as ErrForbidden")
	}
	if errors.Is(err, authz.ErrUnauthenticated) {
		t.Error("DB lookup error should not surface as ErrUnauthenticated")
	}
}

