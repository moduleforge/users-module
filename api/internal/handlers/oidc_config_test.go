package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/moduleforge/users-module/api/internal/auth"
	"github.com/moduleforge/users-module/api/internal/config"
	db "github.com/moduleforge/users-module/model/db"
)

// fakeQuerier is an in-memory OIDCConfigQuerier. All access is
// synchronized so tests can make assertions from arbitrary goroutines
// (though in practice we stay on the test goroutine).
type fakeQuerier struct {
	mu        sync.Mutex
	row       db.OidcConfig
	providers map[string]db.OidcProvider
}

func newFakeQuerier(initial db.OidcConfig) *fakeQuerier {
	return &fakeQuerier{row: initial, providers: map[string]db.OidcProvider{}}
}

func (f *fakeQuerier) GetOIDCConfig(ctx context.Context) (db.OidcConfig, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.row, nil
}

func (f *fakeQuerier) UpdateOIDCConfig(ctx context.Context, optOut bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.row.OptOut = optOut
	f.row.SavedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	return nil
}

func (f *fakeQuerier) SetSetupTokenHash(ctx context.Context, hash pgtype.Text) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.row.SetupTokenHash = hash
	now := time.Now()
	f.row.SetupTokenCreatedAt = &now
	return nil
}

func (f *fakeQuerier) ClearSetupTokenHash(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.row.SetupTokenHash = pgtype.Text{}
	f.row.SetupTokenCreatedAt = nil
	return nil
}

func (f *fakeQuerier) SetOIDCProviderEnabled(ctx context.Context, arg db.SetOIDCProviderEnabledParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	existing, ok := f.providers[arg.ID]
	if ok {
		existing.Enabled = arg.Enabled
		f.providers[arg.ID] = existing
		return nil
	}
	f.providers[arg.ID] = db.OidcProvider{ID: arg.ID, Enabled: arg.Enabled}
	return nil
}

func (f *fakeQuerier) ListOIDCProviders(ctx context.Context) ([]db.OidcProvider, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]db.OidcProvider, 0, len(f.providers))
	for _, p := range f.providers {
		out = append(out, p)
	}
	return out, nil
}

// newEmptyOAuth builds an OAuth with zero providers. That's enough for
// the tests that don't actually exchange — no network init happens
// during Rebuild(ctx, empty).
func newEmptyOAuth(t *testing.T) *auth.OAuth {
	t.Helper()
	cfg := &config.Config{
		LocalAuth: config.LocalAuthConfig{JWTSecret: "test-secret-at-least-32-bytes-xx"},
	}
	o, err := auth.NewOAuth(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewOAuth: %v", err)
	}
	return o
}

// newHandler wires a handler with a starting DB row and empty env
// registry / empty OAuth — enough to cover the confirm/status/saved
// branches without triggering network init.
func newHandler(t *testing.T, initial db.OidcConfig) (*OIDCConfigHandler, *fakeQuerier) {
	t.Helper()
	fq := newFakeQuerier(initial)
	h := NewOIDCConfigHandler(OIDCConfigDeps{
		Queries:      fq,
		OAuth:        newEmptyOAuth(t),
		EnvRegistry:  config.ProviderRegistry{},
		EnvNoOIDCEnv: false,
		TokenDisplay: config.TokenDisplayBoth,
	})
	if err := h.RefreshState(context.Background()); err != nil {
		t.Fatalf("RefreshState: %v", err)
	}
	return h, fq
}

// TestStatus_Unconfirmed covers the initial unconfirmed state: no env
// providers, no opt_out, no token yet.
func TestStatus_Unconfirmed(t *testing.T) {
	h, _ := newHandler(t, db.OidcConfig{ID: 1})

	req := httptest.NewRequest(http.MethodGet, "/v1/oidc-config/status", nil)
	rr := httptest.NewRecorder()
	h.Status(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}
	var resp statusResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if resp.Confirmed {
		t.Errorf("confirmed: got true, want false")
	}
	if resp.State != string(config.BootStateNoEnvNoFlag) {
		t.Errorf("state: got %q, want %q", resp.State, config.BootStateNoEnvNoFlag)
	}
	if resp.NeedsSetupToken {
		t.Errorf("needs_setup_token: got true, but no token exists yet")
	}
}

// TestEnsureSetupToken_GeneratesAndReuses pins the token lifecycle:
// first call generates + persists a hash; second call (state still
// unconfirmed) returns empty plaintext because the token is reused.
func TestEnsureSetupToken_GeneratesAndReuses(t *testing.T) {
	h, fq := newHandler(t, db.OidcConfig{ID: 1})

	first, err := h.EnsureSetupToken(context.Background())
	if err != nil {
		t.Fatalf("EnsureSetupToken: %v", err)
	}
	if first == "" {
		t.Fatalf("expected plaintext on first call, got empty")
	}
	if !fq.row.SetupTokenHash.Valid {
		t.Fatalf("DB hash not persisted")
	}

	second, err := h.EnsureSetupToken(context.Background())
	if err != nil {
		t.Fatalf("EnsureSetupToken (reuse): %v", err)
	}
	if second != "" {
		t.Errorf("reuse should return empty plaintext, got %q", second)
	}
}

// TestEnsureSetupToken_RotatesOnRestart pins the restart-recovery
// invariant: if an operator missed the first-boot banner, a process
// restart (simulated by zero'ing the in-memory plaintext while the DB
// hash persists) must rotate the token so a fresh value is emittable.
// The prior plaintext was unrecoverable by construction, so rotation
// does not weaken one-time-use semantics.
func TestEnsureSetupToken_RotatesOnRestart(t *testing.T) {
	h, fq := newHandler(t, db.OidcConfig{ID: 1})

	first, err := h.EnsureSetupToken(context.Background())
	if err != nil {
		t.Fatalf("EnsureSetupToken: %v", err)
	}
	if first == "" {
		t.Fatalf("expected plaintext on first call, got empty")
	}
	firstHash := fq.row.SetupTokenHash.String
	if firstHash == "" {
		t.Fatalf("expected DB hash persisted, got empty")
	}

	// Simulate a restart: the DB row is unchanged, but a fresh process
	// comes up with no in-memory plaintext. The handler exposes
	// setCurrentPlain for exactly this test hook.
	h.setCurrentPlain("")

	second, err := h.EnsureSetupToken(context.Background())
	if err != nil {
		t.Fatalf("EnsureSetupToken (restart): %v", err)
	}
	if second == "" {
		t.Fatalf("restart path: expected fresh plaintext, got empty (ops would be trapped)")
	}
	if second == first {
		t.Errorf("restart path: rotated plaintext should differ from prior value")
	}
	if fq.row.SetupTokenHash.String == firstHash {
		t.Errorf("restart path: DB hash should have been rotated")
	}
	if !fq.row.SetupTokenHash.Valid {
		t.Errorf("restart path: DB hash should still be valid after rotation")
	}
}

// NOTE: filterRegistry was deleted in 9.16 when the per-provider
// enabled flag moved to oidc_providers.enabled and the merge layer
// became authoritative. The "absent = enabled by default" invariant
// is now enforced by config.MergedEnabled / config.MergedRegistry
// and covered by the merge-layer unit tests.

// TestSetupToken_LoopbackOnly covers the response-gate: requests from
// non-loopback RemoteAddr get 403 even if a token exists; loopback
// gets 200 with the current plaintext; and the endpoint returns 404
// once state is confirmed.
func TestSetupToken_LoopbackOnly(t *testing.T) {
	h, _ := newHandler(t, db.OidcConfig{ID: 1})
	plain, err := h.EnsureSetupToken(context.Background())
	if err != nil || plain == "" {
		t.Fatalf("EnsureSetupToken: err=%v plain=%q", err, plain)
	}

	t.Run("non-loopback returns 403", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/oidc-config/setup-token", nil)
		req.RemoteAddr = "192.0.2.5:5555"
		rr := httptest.NewRecorder()
		h.SetupToken(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("status: got %d, want 403", rr.Code)
		}
		// Body must not contain the token, even as a substring.
		if strings.Contains(rr.Body.String(), plain) {
			t.Errorf("403 body leaked the plaintext token")
		}
	})

	t.Run("loopback returns 200 with token", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/oidc-config/setup-token", nil)
		req.RemoteAddr = "127.0.0.1:44444"
		rr := httptest.NewRecorder()
		h.SetupToken(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status: got %d, want 200, body=%s", rr.Code, rr.Body.String())
		}
		var resp setupTokenResponse
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if resp.Token != plain {
			t.Errorf("token: got %q, want %q", resp.Token, plain)
		}
	})

	t.Run("ipv6 loopback", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/oidc-config/setup-token", nil)
		req.RemoteAddr = "[::1]:44444"
		rr := httptest.NewRecorder()
		h.SetupToken(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("ipv6 loopback: got %d, want 200", rr.Code)
		}
	})
}

// TestConfirm_OptOut covers the all-off confirmation path: a valid
// setup token + empty enabled_providers results in opt_out=true in DB
// and BootStateConfirmedOptOut.
func TestConfirm_OptOut(t *testing.T) {
	h, fq := newHandler(t, db.OidcConfig{ID: 1})
	token, err := h.EnsureSetupToken(context.Background())
	if err != nil {
		t.Fatalf("EnsureSetupToken: %v", err)
	}

	body := confirmRequest{SetupToken: token, EnabledProviders: nil, OptOut: false}
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/oidc-config/confirm", strings.NewReader(string(buf)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.Confirm(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200, body=%s", rr.Code, rr.Body.String())
	}

	if !fq.row.OptOut {
		t.Errorf("DB opt_out: got false, want true (empty enabled list implies opt-out)")
	}
	if h.CurrentState() != config.BootStateConfirmedOptOut {
		t.Errorf("state: got %q, want %q", h.CurrentState(), config.BootStateConfirmedOptOut)
	}
	// Setup token should have been cleared post-confirm.
	if fq.row.SetupTokenHash.Valid {
		t.Errorf("setup token hash should be cleared on confirm")
	}
}

// newHandlerWithAdmin is a variant of newHandler that wires an
// AdminChecker closure for tests covering the admin re-confirm path.
func newHandlerWithAdmin(t *testing.T, checker func(r *http.Request) (bool, error)) (*OIDCConfigHandler, *fakeQuerier) {
	t.Helper()
	fq := newFakeQuerier(db.OidcConfig{ID: 1})
	h := NewOIDCConfigHandler(OIDCConfigDeps{
		Queries:      fq,
		OAuth:        newEmptyOAuth(t),
		EnvRegistry:  config.ProviderRegistry{},
		EnvNoOIDCEnv: false,
		TokenDisplay: config.TokenDisplayBoth,
		AdminChecker: checker,
	})
	if err := h.RefreshState(context.Background()); err != nil {
		t.Fatalf("RefreshState: %v", err)
	}
	return h, fq
}

// TestConfirm_AdminPath_SuccessNoToken — authenticated admin can
// re-confirm without presenting a setup token.
func TestConfirm_AdminPath_SuccessNoToken(t *testing.T) {
	h, fq := newHandlerWithAdmin(t, func(r *http.Request) (bool, error) {
		return true, nil
	})

	body := confirmRequest{EnabledProviders: nil, OptOut: false}
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/oidc-config/confirm", strings.NewReader(string(buf)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.Confirm(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200, body=%s", rr.Code, rr.Body.String())
	}
	if !fq.row.OptOut {
		t.Errorf("opt_out: got false, want true (empty enabled_providers → opt-out)")
	}
}

// TestConfirm_AdminPath_AdminCheckerError — AdminChecker internal
// fault surfaces as 500, no DB mutation.
func TestConfirm_AdminPath_AdminCheckerError(t *testing.T) {
	h, fq := newHandlerWithAdmin(t, func(r *http.Request) (bool, error) {
		return false, errors.New("simulated auth backend down")
	})
	originalOptOut := fq.row.OptOut

	body := confirmRequest{EnabledProviders: nil, OptOut: true}
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/oidc-config/confirm", strings.NewReader(string(buf)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.Confirm(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status: got %d, want 500", rr.Code)
	}
	if fq.row.OptOut != originalOptOut {
		t.Errorf("opt_out changed despite 500 response")
	}
}

// TestConfirm_NoAdmin_FallsThroughToToken — AdminChecker says "not
// admin"; handler falls through and accepts the valid setup token.
func TestConfirm_NoAdmin_FallsThroughToToken(t *testing.T) {
	h, fq := newHandlerWithAdmin(t, func(r *http.Request) (bool, error) {
		return false, nil
	})
	token, err := h.EnsureSetupToken(context.Background())
	if err != nil {
		t.Fatalf("EnsureSetupToken: %v", err)
	}

	body := confirmRequest{SetupToken: token, EnabledProviders: nil, OptOut: false}
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/oidc-config/confirm", strings.NewReader(string(buf)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.Confirm(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200, body=%s", rr.Code, rr.Body.String())
	}
	if !fq.row.OptOut {
		t.Errorf("opt_out: got false, want true")
	}
}

// TestConfirm_InvalidToken verifies that an invalid setup token is
// rejected with 401 and nothing is persisted.
func TestConfirm_InvalidToken(t *testing.T) {
	h, fq := newHandler(t, db.OidcConfig{ID: 1})
	if _, err := h.EnsureSetupToken(context.Background()); err != nil {
		t.Fatalf("EnsureSetupToken: %v", err)
	}
	originalOptOut := fq.row.OptOut

	body := confirmRequest{SetupToken: "not-the-token", OptOut: true}
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/oidc-config/confirm", strings.NewReader(string(buf)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.Confirm(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d, want 401", rr.Code)
	}
	if fq.row.OptOut != originalOptOut {
		t.Errorf("opt_out changed despite 401 rejection")
	}
}

// TestSaved_ReturnsLastPersisted verifies the /saved endpoint returns
// the last successful UpdateOIDCConfig values.
func TestSaved_ReturnsLastPersisted(t *testing.T) {
	h, _ := newHandler(t, db.OidcConfig{ID: 1})
	token, err := h.EnsureSetupToken(context.Background())
	if err != nil {
		t.Fatalf("EnsureSetupToken: %v", err)
	}

	// First confirm: all-off opt-out.
	body := confirmRequest{SetupToken: token, OptOut: true}
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/oidc-config/confirm", strings.NewReader(string(buf)))
	req.Header.Set("Content-Type", "application/json")
	h.Confirm(httptest.NewRecorder(), req)

	// Now hit /saved.
	req2 := httptest.NewRequest(http.MethodGet, "/v1/oidc-config/saved", nil)
	rr2 := httptest.NewRecorder()
	h.Saved(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr2.Code)
	}
	var resp savedResponse
	if err := json.Unmarshal(rr2.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.OptOut {
		t.Errorf("opt_out: got false, want true")
	}
	if resp.SavedAt == nil {
		t.Errorf("saved_at should be set after a confirm")
	}
}

// TestConfirm_WritesPerProviderEnabled_NotJSONB — regression for
// Phase 9.16. Confirm must upsert oidc_providers.enabled per row (so
// the summary page and edit modal see the same enabled flag) and must
// not rely on the removed oidc_config.provider_enabled JSONB column.
func TestConfirm_WritesPerProviderEnabled_NotJSONB(t *testing.T) {
	h, fq := newHandler(t, db.OidcConfig{ID: 1})
	token, err := h.EnsureSetupToken(context.Background())
	if err != nil {
		t.Fatalf("EnsureSetupToken: %v", err)
	}
	// Inject an env registry so Confirm has something to toggle.
	h.deps.EnvRegistry = config.ProviderRegistry{
		"google":    config.Provider{ID: "google"},
		"microsoft": config.Provider{ID: "microsoft"},
	}

	body := confirmRequest{
		SetupToken:       token,
		EnabledProviders: []string{"google"},
		OptOut:           false,
	}
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/oidc-config/confirm", strings.NewReader(string(buf)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.Confirm(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200, body=%s", rr.Code, rr.Body.String())
	}

	// oidc_providers rows exist with correct enabled flags.
	fq.mu.Lock()
	defer fq.mu.Unlock()
	gp, ok := fq.providers["google"]
	if !ok {
		t.Fatalf("google row not written to oidc_providers")
	}
	if !gp.Enabled {
		t.Errorf("google enabled: got false, want true")
	}
	mp, ok := fq.providers["microsoft"]
	if !ok {
		t.Fatalf("microsoft row not written to oidc_providers")
	}
	if mp.Enabled {
		t.Errorf("microsoft enabled: got true, want false (not in enabled_providers list)")
	}
}

// TestRequireOIDCConfirmed_Gates verifies the middleware returns 503
// with the expected JSON payload when state is unconfirmed, and calls
// through when confirmed. The status function is re-read on every
// request, so a mid-session flip takes effect without restart.
func TestRequireOIDCConfirmed_Gates(t *testing.T) {
	var current config.BootState = config.BootStateInitFailed
	var mu sync.RWMutex
	statusFn := func() config.BootState {
		mu.RLock()
		defer mu.RUnlock()
		return current
	}
	mw := auth.RequireOIDCConfirmed(statusFn)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`"passed"`))
	})
	handler := mw(next)

	// Unconfirmed: 503 with config_path.
	req := httptest.NewRequest(http.MethodGet, "/v1/self", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("unconfirmed: got %d, want 503", rr.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["error"] != "oidc_not_confirmed" {
		t.Errorf("error: got %v, want oidc_not_confirmed", resp["error"])
	}
	if resp["config_path"] != "/oidc-config" {
		t.Errorf("config_path: got %v, want /oidc-config", resp["config_path"])
	}

	// Flip to confirmed without restarting the handler — state read is
	// live on every request.
	mu.Lock()
	current = config.BootStateConfirmedOK
	mu.Unlock()

	req2 := httptest.NewRequest(http.MethodGet, "/v1/self", nil)
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("confirmed: got %d, want 200", rr2.Code)
	}
}

// TestConfirm_BadJSON pins the body-parse branch: malformed JSON → 400.
func TestConfirm_BadJSON(t *testing.T) {
	h, _ := newHandler(t, db.OidcConfig{ID: 1})

	req := httptest.NewRequest(http.MethodPost, "/v1/oidc-config/confirm", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.Confirm(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rr.Code)
	}
}

// TestNormalizeEnabledSet_DropsUnknown covers the helper that filters
// the submitted list against the env registry. Unknown IDs should be
// silently dropped, not stored in DB.
func TestNormalizeEnabledSet_DropsUnknown(t *testing.T) {
	reg := config.ProviderRegistry{
		"google":    config.Provider{ID: "google"},
		"microsoft": config.Provider{ID: "microsoft"},
	}
	got := normalizeEnabledSet(reg, []string{"google", "not-a-provider"})
	if !got["google"] {
		t.Errorf("google should be true")
	}
	if got["microsoft"] {
		t.Errorf("microsoft should default to false")
	}
	if _, ok := got["not-a-provider"]; ok {
		t.Errorf("unknown provider leaked into enabled set")
	}
}

// TestIsLoopbackAddr pins the loopback check used by /setup-token.
func TestIsLoopbackAddr(t *testing.T) {
	cases := []struct {
		addr string
		want bool
	}{
		{"127.0.0.1:8080", true},
		{"127.0.0.5:1", true},
		{"[::1]:8080", true},
		{"localhost:8080", true},
		{"192.0.2.1:8080", false},
		{"8.8.8.8:53", false},
		{"::1", true}, // no port (unlikely from net/http but robust)
		{"", false},
	}
	for _, tc := range cases {
		t.Run(tc.addr, func(t *testing.T) {
			if got := isLoopbackAddr(tc.addr); got != tc.want {
				t.Errorf("isLoopbackAddr(%q): got %v, want %v", tc.addr, got, tc.want)
			}
		})
	}
}

// Ensure the fakeQuerier satisfies the handler's interface (compile-time).
var _ OIDCConfigQuerier = (*fakeQuerier)(nil)

// sanity-check that the fakeQuerier doesn't surface errors we don't
// expect (e.g. a missed copy of SetupTokenHash).
func TestFakeQuerier_RoundTrip(t *testing.T) {
	ctx := context.Background()
	fq := newFakeQuerier(db.OidcConfig{ID: 1})

	if err := fq.SetSetupTokenHash(ctx, pgtype.Text{String: "abc", Valid: true}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	row, err := fq.GetOIDCConfig(ctx)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !row.SetupTokenHash.Valid || row.SetupTokenHash.String != "abc" {
		t.Errorf("hash not persisted")
	}

	if err := fq.ClearSetupTokenHash(ctx); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	row, _ = fq.GetOIDCConfig(ctx)
	if row.SetupTokenHash.Valid {
		t.Errorf("hash not cleared")
	}
}

// TestRefreshState_BadDB pins the error path so a DB outage doesn't
// silently leave cachedBoot at a stale value.
func TestRefreshState_BadDB(t *testing.T) {
	h := NewOIDCConfigHandler(OIDCConfigDeps{
		Queries:      erroringQuerier{},
		OAuth:        newEmptyOAuth(t),
		EnvRegistry:  config.ProviderRegistry{},
		TokenDisplay: config.TokenDisplayBoth,
	})
	if err := h.RefreshState(context.Background()); err == nil {
		t.Fatalf("expected error from failing querier")
	}
}

type erroringQuerier struct{}

func (erroringQuerier) GetOIDCConfig(ctx context.Context) (db.OidcConfig, error) {
	return db.OidcConfig{}, errors.New("boom")
}
func (erroringQuerier) UpdateOIDCConfig(ctx context.Context, _ bool) error {
	return errors.New("boom")
}
func (erroringQuerier) SetSetupTokenHash(ctx context.Context, _ pgtype.Text) error {
	return errors.New("boom")
}
func (erroringQuerier) ClearSetupTokenHash(ctx context.Context) error {
	return errors.New("boom")
}
func (erroringQuerier) SetOIDCProviderEnabled(ctx context.Context, _ db.SetOIDCProviderEnabledParams) error {
	return errors.New("boom")
}
func (erroringQuerier) ListOIDCProviders(ctx context.Context) ([]db.OidcProvider, error) {
	return nil, errors.New("boom")
}
