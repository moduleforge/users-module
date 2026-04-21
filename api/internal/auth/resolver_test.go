package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	db "github.com/moduleforge/users-module/model/db"
)

// newResolverWithStub builds a UserResolver whose only moving part is a stub
// uuidLookup. Pool and queries are intentionally nil — the tests below only
// exercise the local-issuer fast path, which must not reach for either.
func newResolverWithStub(t *testing.T, localIssuer string, lookup uuidLookupFn) *UserResolver {
	t.Helper()
	return &UserResolver{
		pool:        nil,
		queries:     nil,
		adminRole:   "admin",
		localIssuer: localIssuer,
		uuidLookup:  lookup,
	}
}

func TestResolver_LocalIssuerFastPath_Success(t *testing.T) {
	wantUUID := uuid.New()
	wantUser := db.UserAccount{
		ID:            42,
		Uuid:          wantUUID,
		AccountHolder: 41,
		Email:         "alice@example.com",
		IsAdmin:       false,
	}

	calls := 0
	lookup := func(ctx context.Context, u uuid.UUID) (db.UserAccount, error) {
		calls++
		if u != wantUUID {
			t.Errorf("uuidLookup got %s, want %s", u, wantUUID)
		}
		return wantUser, nil
	}
	r := newResolverWithStub(t, "users-module-local", lookup)

	p := Principal{
		Subject: wantUUID.String(),
		Issuer:  "users-module-local",
		Email:   "", // local JWT does not carry email
	}
	uc, err := r.Resolve(context.Background(), p)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if calls != 1 {
		t.Errorf("uuidLookup call count = %d, want 1", calls)
	}
	if uc.UserAccountID != wantUser.ID {
		t.Errorf("UserAccountID = %d, want %d", uc.UserAccountID, wantUser.ID)
	}
	if uc.UserUUID != wantUUID.String() {
		t.Errorf("UserUUID = %q, want %q", uc.UserUUID, wantUUID.String())
	}
	if uc.Email != wantUser.Email {
		t.Errorf("Email = %q, want %q", uc.Email, wantUser.Email)
	}
}

func TestResolver_LocalIssuerFastPath_DeletedUser(t *testing.T) {
	lookup := func(ctx context.Context, u uuid.UUID) (db.UserAccount, error) {
		return db.UserAccount{}, pgx.ErrNoRows
	}
	r := newResolverWithStub(t, "users-module-local", lookup)

	p := Principal{
		Subject: uuid.New().String(),
		Issuer:  "users-module-local",
	}
	_, err := r.Resolve(context.Background(), p)
	if err == nil {
		t.Fatal("expected ErrUserGone, got nil")
	}
	if !errors.Is(err, ErrUserGone) {
		t.Errorf("expected ErrUserGone, got %v", err)
	}
}

func TestResolver_LocalIssuerFastPath_BadSubject(t *testing.T) {
	// uuidLookup should never be called if sub is not a valid UUID.
	called := false
	lookup := func(ctx context.Context, u uuid.UUID) (db.UserAccount, error) {
		called = true
		return db.UserAccount{}, nil
	}
	r := newResolverWithStub(t, "users-module-local", lookup)

	p := Principal{
		Subject: "not-a-uuid",
		Issuer:  "users-module-local",
	}
	_, err := r.Resolve(context.Background(), p)
	if err == nil {
		t.Fatal("expected error for non-UUID subject")
	}
	if called {
		t.Error("uuidLookup should not be called for malformed subject")
	}
}

// TestResolver_OIDCPath_FastPathSkipped confirms that a principal whose issuer
// is something other than LocalIssuer does not take the fast path. We stub the
// fast path to fail loudly if it runs, then watch Resolve fall through to the
// OIDC code — which in turn fails because queries is nil. The specific failure
// mode isn't the point; the point is that fast path gets skipped.
func TestResolver_OIDCPath_FastPathSkipped(t *testing.T) {
	lookup := func(ctx context.Context, u uuid.UUID) (db.UserAccount, error) {
		t.Fatal("local-issuer fast path must not run for non-local issuer")
		return db.UserAccount{}, nil
	}
	r := newResolverWithStub(t, "users-module-local", lookup)

	p := Principal{
		Subject: "google-sub-123",
		Issuer:  "https://accounts.google.com",
		Email:   "user@example.com",
	}

	// Resolve will panic/fail inside the OIDC path because queries is nil.
	// We recover so the test passes as long as the lookup stub was not called.
	defer func() {
		_ = recover()
	}()
	_, _ = r.Resolve(context.Background(), p)
}
