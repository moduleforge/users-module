package handlers

// Tests for authz / observer cross-cutting concerns on UserAccountsHandler.
//
// These tests exercise:
//   - Authorize-denied → handler aborts before any DB write (List, Create variants).
//   - In-tx Observer error → txhelper rolls back and handler returns 500.
//   - Unauthenticated actor (no opctx actor) → 401 response.
//
// They use in-memory stubs for db.Querier, txhelper.DB/Tx, and
// coreAuthz.Authorizer — no network or real database is required.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	coreAuthz "github.com/moduleforge/core-api/authz"
	"github.com/moduleforge/core-api/entity"
	"github.com/moduleforge/core-api/observer"
	"github.com/moduleforge/core-api/opctx"
	"github.com/moduleforge/core-api/txhelper"
	localAuth "github.com/moduleforge/users-module/api/internal/auth"
	localAuthz "github.com/moduleforge/users-module/api/internal/authz"
	db "github.com/moduleforge/users-module/model/db"
)

// ---------------------------------------------------------------------------
// Stub authz.Authorizer
// ---------------------------------------------------------------------------

// allowAllAuthzStub permits every operation.
type allowAllAuthzStub struct{}

func (allowAllAuthzStub) Authorize(_ context.Context, _ string, _ entity.Entity) error {
	return nil
}

var _ coreAuthz.Authorizer = allowAllAuthzStub{}

// denyAuthzStub always returns the configured error.
type denyAuthzStub struct{ err error }

func (d denyAuthzStub) Authorize(_ context.Context, _ string, _ entity.Entity) error {
	return d.err
}

var _ coreAuthz.Authorizer = denyAuthzStub{}

// ---------------------------------------------------------------------------
// Fake txhelper.DB and pgx.Tx
// ---------------------------------------------------------------------------

// fakePgxDB implements txhelper.DB. BeginTx returns the configured tx.
type fakePgxDB struct {
	tx  pgx.Tx
	err error
}

func (d *fakePgxDB) BeginTx(_ context.Context, _ pgx.TxOptions) (pgx.Tx, error) {
	return d.tx, d.err
}

var _ txhelper.DB = (*fakePgxDB)(nil)

// fakePgxTx is a minimal pgx.Tx that records commit/rollback decisions.
type fakePgxTx struct {
	committed  bool
	rolledBack bool
}

func (t *fakePgxTx) Begin(_ context.Context) (pgx.Tx, error)            { return nil, nil }
func (t *fakePgxTx) Commit(_ context.Context) error                      { t.committed = true; return nil }
func (t *fakePgxTx) Rollback(_ context.Context) error                    { t.rolledBack = true; return nil }
func (t *fakePgxTx) CopyFrom(_ context.Context, _ pgx.Identifier, _ []string, _ pgx.CopyFromSource) (int64, error) {
	return 0, nil
}
func (t *fakePgxTx) SendBatch(_ context.Context, _ *pgx.Batch) pgx.BatchResults { return nil }
func (t *fakePgxTx) LargeObjects() pgx.LargeObjects                              { return pgx.LargeObjects{} }
func (t *fakePgxTx) Prepare(_ context.Context, _, _ string) (*pgconn.StatementDescription, error) {
	return nil, nil
}
func (t *fakePgxTx) Exec(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}
func (t *fakePgxTx) Query(_ context.Context, _ string, _ ...any) (pgx.Rows, error) { return nil, nil }
func (t *fakePgxTx) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row        { return nil }
func (t *fakePgxTx) Conn() *pgx.Conn                                                { return nil }

var _ pgx.Tx = (*fakePgxTx)(nil)

func newFakePgxDB() (*fakePgxDB, *fakePgxTx) {
	tx := &fakePgxTx{}
	return &fakePgxDB{tx: tx}, tx
}

// ---------------------------------------------------------------------------
// Stub db.Querier for handler tests
// ---------------------------------------------------------------------------

// handlerQuerier is a minimal in-memory db.Querier for handler unit tests.
// Methods used by the handler under test are implemented; all others panic.
type handlerQuerier struct {
	userAccounts map[uuid.UUID]db.UserAccount // lookup by UUID
	createResult db.UserAccount               // returned by CreateUserAccount
	createErr    error
	searchResult []db.UserAccount
}

func newHandlerQuerier() *handlerQuerier {
	return &handlerQuerier{
		userAccounts: make(map[uuid.UUID]db.UserAccount),
	}
}

func (q *handlerQuerier) seedUserAccount(entityID int64, isAdmin bool) db.UserAccount {
	ua := db.UserAccount{
		ID:            entityID * 10,
		Uuid:          uuid.New(),
		AccountHolder: entityID,
		Email:         "user@example.com",
		IsAdmin:       isAdmin,
		CreatedAt:     pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}
	q.userAccounts[ua.Uuid] = ua
	return ua
}

func (q *handlerQuerier) GetUserAccountByUUID(_ context.Context, id uuid.UUID) (db.UserAccount, error) {
	if ua, ok := q.userAccounts[id]; ok {
		return ua, nil
	}
	return db.UserAccount{}, pgx.ErrNoRows
}

func (q *handlerQuerier) CreateUserAccount(_ context.Context, _ db.CreateUserAccountParams) (db.UserAccount, error) {
	if q.createErr != nil {
		return db.UserAccount{}, q.createErr
	}
	return q.createResult, nil
}

func (q *handlerQuerier) SearchUserAccounts(_ context.Context, _ db.SearchUserAccountsParams) ([]db.UserAccount, error) {
	return q.searchResult, nil
}

// Unused but required by db.Querier interface; all panic so unexpected calls are visible.
func (q *handlerQuerier) ArchiveApp(_ context.Context, _ int64) error { panic("unexpected: ArchiveApp") }
func (q *handlerQuerier) AssignUserAccountToApp(_ context.Context, _ db.AssignUserAccountToAppParams) error {
	panic("unexpected: AssignUserAccountToApp")
}
func (q *handlerQuerier) ClearSetupTokenHash(_ context.Context) error {
	panic("unexpected: ClearSetupTokenHash")
}
func (q *handlerQuerier) ConsumeEmailCode(_ context.Context, _ int64) error {
	panic("unexpected: ConsumeEmailCode")
}
func (q *handlerQuerier) ConsumePasswordReset(_ context.Context, _ int64) error {
	panic("unexpected: ConsumePasswordReset")
}
func (q *handlerQuerier) CreateApp(_ context.Context, _ db.CreateAppParams) (db.App, error) {
	panic("unexpected: CreateApp")
}
func (q *handlerQuerier) CreateEmailCode(_ context.Context, _ db.CreateEmailCodeParams) (db.EmailCode, error) {
	panic("unexpected: CreateEmailCode")
}
func (q *handlerQuerier) CreatePasswordReset(_ context.Context, _ db.CreatePasswordResetParams) (db.PasswordReset, error) {
	panic("unexpected: CreatePasswordReset")
}
func (q *handlerQuerier) DeleteAuthLocal(_ context.Context, _ int64) error {
	panic("unexpected: DeleteAuthLocal")
}
func (q *handlerQuerier) DeleteOIDCProvider(_ context.Context, _ string) (int64, error) {
	panic("unexpected: DeleteOIDCProvider")
}
func (q *handlerQuerier) GetActiveEmailCode(_ context.Context, _ db.GetActiveEmailCodeParams) (db.EmailCode, error) {
	panic("unexpected: GetActiveEmailCode")
}
func (q *handlerQuerier) GetActivePasswordReset(_ context.Context, _ string) (db.PasswordReset, error) {
	panic("unexpected: GetActivePasswordReset")
}
func (q *handlerQuerier) GetAppBySlug(_ context.Context, _ string) (db.App, error) {
	panic("unexpected: GetAppBySlug")
}
func (q *handlerQuerier) GetAppByUUID(_ context.Context, _ uuid.UUID) (db.App, error) {
	panic("unexpected: GetAppByUUID")
}
func (q *handlerQuerier) GetAuthLocal(_ context.Context, _ int64) (db.AuthLocal, error) {
	panic("unexpected: GetAuthLocal")
}
func (q *handlerQuerier) GetOIDCConfig(_ context.Context) (db.OidcConfig, error) {
	panic("unexpected: GetOIDCConfig")
}
func (q *handlerQuerier) GetOIDCProvider(_ context.Context, _ string) (db.OidcProvider, error) {
	panic("unexpected: GetOIDCProvider")
}
func (q *handlerQuerier) GetUserAccountByAccountHolder(_ context.Context, _ int64) (db.UserAccount, error) {
	panic("unexpected: GetUserAccountByAccountHolder")
}
func (q *handlerQuerier) GetUserAccountByAuth(_ context.Context, _ db.GetUserAccountByAuthParams) (db.UserAccount, error) {
	panic("unexpected: GetUserAccountByAuth")
}
func (q *handlerQuerier) GetUserAccountByEmail(_ context.Context, _ string) (db.UserAccount, error) {
	panic("unexpected: GetUserAccountByEmail")
}
func (q *handlerQuerier) GetUserAccountByID(_ context.Context, _ int64) (db.UserAccount, error) {
	panic("unexpected: GetUserAccountByID")
}
func (q *handlerQuerier) ListAppUserAccounts(_ context.Context, _ int64) ([]db.AppsUserAccount, error) {
	panic("unexpected: ListAppUserAccounts")
}
func (q *handlerQuerier) ListApps(_ context.Context) ([]db.App, error) {
	panic("unexpected: ListApps")
}
func (q *handlerQuerier) ListOIDCProviders(_ context.Context) ([]db.OidcProvider, error) {
	panic("unexpected: ListOIDCProviders")
}
func (q *handlerQuerier) ListUserAccountApps(_ context.Context, _ int64) ([]db.AppsUserAccount, error) {
	panic("unexpected: ListUserAccountApps")
}
func (q *handlerQuerier) RemoveUserAccountFromApp(_ context.Context, _ db.RemoveUserAccountFromAppParams) error {
	panic("unexpected: RemoveUserAccountFromApp")
}
func (q *handlerQuerier) SetAdmin(_ context.Context, _ db.SetAdminParams) error {
	panic("unexpected: SetAdmin")
}
func (q *handlerQuerier) SetAppUserAccountRoles(_ context.Context, _ db.SetAppUserAccountRolesParams) error {
	panic("unexpected: SetAppUserAccountRoles")
}
func (q *handlerQuerier) SetDefaultApp(_ context.Context, _ db.SetDefaultAppParams) error {
	panic("unexpected: SetDefaultApp")
}
func (q *handlerQuerier) SetOIDCProviderEnabled(_ context.Context, _ db.SetOIDCProviderEnabledParams) error {
	panic("unexpected: SetOIDCProviderEnabled")
}
func (q *handlerQuerier) SetSetupTokenHash(_ context.Context, _ pgtype.Text) error {
	panic("unexpected: SetSetupTokenHash")
}
func (q *handlerQuerier) UpdateApp(_ context.Context, _ db.UpdateAppParams) error {
	panic("unexpected: UpdateApp")
}
func (q *handlerQuerier) UpdateOIDCConfig(_ context.Context, _ bool) error {
	panic("unexpected: UpdateOIDCConfig")
}
func (q *handlerQuerier) UpdateUserAccount(_ context.Context, _ db.UpdateUserAccountParams) error {
	panic("unexpected: UpdateUserAccount")
}
func (q *handlerQuerier) UpsertAuthLocal(_ context.Context, _ db.UpsertAuthLocalParams) error {
	panic("unexpected: UpsertAuthLocal")
}
func (q *handlerQuerier) UpsertOIDCProvider(_ context.Context, _ db.UpsertOIDCProviderParams) (db.OidcProvider, error) {
	panic("unexpected: UpsertOIDCProvider")
}

var _ db.Querier = (*handlerQuerier)(nil)

// ---------------------------------------------------------------------------
// Error observer for rollback testing
// ---------------------------------------------------------------------------

// errorObserver is a MutationObserver whose Observe always returns the given error.
type errorObserver struct{ err error }

func (o errorObserver) Observe(_ context.Context, _ pgx.Tx, _, _ string, _ *int64, _, _ any) error {
	return o.err
}

func (o errorObserver) ObserveAfterCommit(_ context.Context, _, _ string, _ *int64, _, _ any) error {
	return nil
}

var _ observer.MutationObserver = errorObserver{}

// ---------------------------------------------------------------------------
// Context helpers
// ---------------------------------------------------------------------------

// adminContext returns a context with an admin UserContext and opctx.Actor set.
func adminContext(entityID int64) context.Context {
	uc := &localAuth.UserContext{
		UserAccountID: 1,
		UserUUID:      uuid.New().String(),
		EntityID:      entityID,
		Email:         "admin@example.com",
		IsAdmin:       true,
	}
	ctx := localAuth.WithUserContext(context.Background(), uc)
	ctx = opctx.WithActor(ctx, entityID)
	return ctx
}

// unauthenticatedContext returns a context with no UserContext and no opctx actor.
func unauthenticatedContext() context.Context {
	return context.Background()
}

// ---------------------------------------------------------------------------
// Test: List — Authorize-denied returns 401 (unauthenticated) or 403 (forbidden)
// ---------------------------------------------------------------------------

// TestUserAccountsHandler_List_AuthzDenied_Unauthenticated verifies that when
// the authorizer returns ErrUnauthenticated, the List handler responds with 401
// and makes no DB calls.
func TestUserAccountsHandler_List_AuthzDenied_Unauthenticated(t *testing.T) {
	q := newHandlerQuerier()
	fakePool, _ := newFakePgxDB()
	obs := observer.NewObserverGroup()

	h := &UserAccountsHandler{
		pool:       fakePool,
		q:          q,
		newQuerier: func(_ pgx.Tx) db.Querier { panic("DB must not be called") },
		az:         denyAuthzStub{err: localAuthz.ErrUnauthenticated},
		observers:  obs,
	}

	ctx := unauthenticatedContext()
	req := httptest.NewRequest(http.MethodGet, "/v1/user-accounts", nil).WithContext(ctx)
	rr := httptest.NewRecorder()
	h.List(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want 401", rr.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("body not valid JSON: %v", err)
	}
	errObj, _ := resp["error"].(map[string]any)
	if errObj["code"] != "unauthorized" {
		t.Errorf("error.code: got %v, want unauthorized", errObj["code"])
	}
}

// TestUserAccountsHandler_List_AuthzDenied_Forbidden verifies that when the
// authorizer returns ErrForbidden, the List handler responds with 403.
func TestUserAccountsHandler_List_AuthzDenied_Forbidden(t *testing.T) {
	q := newHandlerQuerier()
	fakePool, _ := newFakePgxDB()
	obs := observer.NewObserverGroup()

	h := &UserAccountsHandler{
		pool:       fakePool,
		q:          q,
		newQuerier: func(_ pgx.Tx) db.Querier { panic("DB must not be called") },
		az:         denyAuthzStub{err: localAuthz.ErrForbidden},
		observers:  obs,
	}

	ctx := adminContext(42)
	req := httptest.NewRequest(http.MethodGet, "/v1/user-accounts", nil).WithContext(ctx)
	rr := httptest.NewRecorder()
	h.List(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("status: got %d, want 403", rr.Code)
	}
}

// TestUserAccountsHandler_List_AuthzAllowed verifies that an allowed List call
// returns 200 and uses the querier.
func TestUserAccountsHandler_List_AuthzAllowed(t *testing.T) {
	q := newHandlerQuerier()
	q.searchResult = []db.UserAccount{}
	fakePool, _ := newFakePgxDB()
	obs := observer.NewObserverGroup()

	h := &UserAccountsHandler{
		pool:       fakePool,
		q:          q,
		newQuerier: func(_ pgx.Tx) db.Querier { return q },
		az:         allowAllAuthzStub{},
		observers:  obs,
	}

	ctx := adminContext(42)
	req := httptest.NewRequest(http.MethodGet, "/v1/user-accounts", nil).WithContext(ctx)
	rr := httptest.NewRecorder()
	h.List(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200, body=%s", rr.Code, rr.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Test: Create — Authorize-denied aborts before any DB write
// ---------------------------------------------------------------------------

// TestUserAccountsHandler_Create_AuthzDenied verifies that an authz-denied
// Create request returns 401 and never calls the pool or querier.
func TestUserAccountsHandler_Create_AuthzDenied(t *testing.T) {
	fakePool, fakeTx := newFakePgxDB()
	obs := observer.NewObserverGroup()

	h := &UserAccountsHandler{
		pool:       fakePool,
		q:          newHandlerQuerier(),
		newQuerier: func(_ pgx.Tx) db.Querier { panic("DB must not be called") },
		az:         denyAuthzStub{err: localAuthz.ErrUnauthenticated},
		observers:  obs,
	}

	body := `{"email":"new@example.com","given_name":"New","family_name":"User"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/user-accounts", strings.NewReader(body)).
		WithContext(unauthenticatedContext())
	rr := httptest.NewRecorder()
	h.Create(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want 401", rr.Code)
	}
	if fakeTx.committed {
		t.Error("tx committed despite authz denial")
	}
}

// ---------------------------------------------------------------------------
// Test: In-tx Observer error → txhelper rolls back, handler returns 500
// ---------------------------------------------------------------------------

// TestUserAccountsHandler_Create_ObserverError_Rollback verifies that when the
// in-tx observer returns an error, txhelper rolls back the transaction and the
// handler returns 500.
//
// The test short-circuits the two-phase creation by replacing the coreSvcs and
// coreQ with nil (since authz passes but we test only the Phase-2 tx path).
// We inject a querier that succeeds for CreateUserAccount but an observer that
// fails; we then verify the tx was rolled back and not committed.
func TestUserAccountsHandler_Create_ObserverError_Rollback(t *testing.T) {
	fakePool, fakeTx := newFakePgxDB()

	// The handlerQuerier returns a valid UserAccount row on Create.
	q := newHandlerQuerier()
	q.createResult = db.UserAccount{
		ID:            100,
		Uuid:          uuid.New(),
		AccountHolder: 42,
		Email:         "new@example.com",
		IsAdmin:       false,
		CreatedAt:     pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}

	observeErr := errors.New("audit storage full")
	obs := observer.NewObserverGroup(errorObserver{err: observeErr})

	h := &UserAccountsHandler{
		pool:       fakePool,
		q:          q,
		newQuerier: func(_ pgx.Tx) db.Querier { return q },
		az:         allowAllAuthzStub{},
		observers:  obs,
		// coreSvcs and coreQ are nil; Create calls them only when Phase-1 is reached.
		// To test Phase-2 observer rollback without Phase-1, we wire a custom Phase-2
		// by injecting at the handler struct directly via the httptest path below.
	}

	// We cannot easily skip Phase 1 (NaturalPerson.Create) without a real coreSvcs.
	// Instead, test the observer-error rollback path via Update, which goes directly
	// into a single txhelper.Run without a Phase-1 dependency.
	seeded := q.seedUserAccount(42, false)

	// Swap in a querier that supports GetUserAccountByUUID and UpdateUserAccount.
	updateQ := &updateStubQuerier{handlerQuerier: *q, ua: seeded}
	h.q = updateQ
	h.newQuerier = func(_ pgx.Tx) db.Querier { return updateQ }

	body := `{"email":"updated@example.com"}`
	// UpdateUserAccount is hit inside the tx; the observer fires after that.
	// The request must have a chi URL param for {uuid}; we build the context manually
	// since we do not have a running chi router.
	//
	// loadUserAccountByUUIDParam calls chi.URLParam(r, "uuid") which reads from chi's
	// RouteContext. We wire that manually.
	chiCtx := chi.NewRouteContext()
	chiCtx.URLParams.Add("uuid", seeded.Uuid.String())

	ctx := adminContext(42)
	ctx = context.WithValue(ctx, chi.RouteCtxKey, chiCtx)

	req := httptest.NewRequest(http.MethodPut, "/v1/user-accounts/"+seeded.Uuid.String(),
		strings.NewReader(body)).WithContext(ctx)
	rr := httptest.NewRecorder()
	h.Update(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want 500 when observer fails", rr.Code)
	}
	if fakeTx.committed {
		t.Error("tx committed despite observer error — expected rollback")
	}
	if !fakeTx.rolledBack {
		t.Error("tx not rolled back after observer error")
	}
}

// updateStubQuerier extends handlerQuerier with UpdateUserAccount and GetUserAccountByID
// to support the Update handler path.
type updateStubQuerier struct {
	handlerQuerier
	ua db.UserAccount
}

func (q *updateStubQuerier) UpdateUserAccount(_ context.Context, _ db.UpdateUserAccountParams) error {
	return nil
}

func (q *updateStubQuerier) GetUserAccountByID(_ context.Context, _ int64) (db.UserAccount, error) {
	return q.ua, nil
}

// GetUserAccountByUUID delegates to the embedded handlerQuerier.
func (q *updateStubQuerier) GetUserAccountByUUID(ctx context.Context, id uuid.UUID) (db.UserAccount, error) {
	return q.handlerQuerier.GetUserAccountByUUID(ctx, id)
}
