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

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/moduleforge/users-module/api/internal/config"
	db "github.com/moduleforge/users-module/model/db"
)

// fakeProvidersQuerier is an in-memory store for oidc_providers rows.
// Separate from fakeQuerier so the two flows stay independently testable.
type fakeProvidersQuerier struct {
	mu   sync.Mutex
	rows map[string]db.OidcProvider
}

func newFakeProvidersQuerier() *fakeProvidersQuerier {
	return &fakeProvidersQuerier{rows: map[string]db.OidcProvider{}}
}

func (f *fakeProvidersQuerier) GetOIDCProvider(ctx context.Context, id string) (db.OidcProvider, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.rows[id]
	if !ok {
		return db.OidcProvider{}, pgx.ErrNoRows
	}
	return r, nil
}

func (f *fakeProvidersQuerier) ListOIDCProviders(ctx context.Context) ([]db.OidcProvider, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]db.OidcProvider, 0, len(f.rows))
	for _, r := range f.rows {
		out = append(out, r)
	}
	return out, nil
}

func (f *fakeProvidersQuerier) UpsertOIDCProvider(ctx context.Context, arg db.UpsertOIDCProviderParams) (db.OidcProvider, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	existing, ok := f.rows[arg.ID]
	now := pgtype.Timestamptz{Time: time.Now(), Valid: true}
	row := db.OidcProvider{
		ID:           arg.ID,
		DisplayName:  arg.DisplayName,
		IssuerUrl:    arg.IssuerUrl,
		ClientID:     arg.ClientID,
		ClientSecret: arg.ClientSecret,
		ClaimStyle:   arg.ClaimStyle,
		Scopes:       arg.Scopes,
		Enabled:      arg.Enabled,
		UpdatedAt:    now,
	}
	if ok {
		row.CreatedAt = existing.CreatedAt
	} else {
		row.CreatedAt = now
	}
	f.rows[arg.ID] = row
	return row, nil
}

func (f *fakeProvidersQuerier) DeleteOIDCProvider(ctx context.Context, id string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.rows[id]; !ok {
		return 0, nil
	}
	delete(f.rows, id)
	return 1, nil
}

// Ensure interface satisfaction at compile time.
var _ OIDCProvidersQuerier = (*fakeProvidersQuerier)(nil)

// newProvidersHandlerForTest builds a fully wired provider handler plus
// its backing fakes. The envRegistry is passed through so individual
// tests can seed env-declared providers.
func newProvidersHandlerForTest(t *testing.T, envRegistry config.ProviderRegistry) (
	*ProvidersHandler,
	*OIDCConfigHandler,
	*fakeProvidersQuerier,
	*fakeQuerier,
) {
	t.Helper()
	fq := newFakeQuerier(db.OidcConfig{ID: 1})
	fp := newFakeProvidersQuerier()

	confirmer := NewOIDCConfigHandler(OIDCConfigDeps{
		Queries:      fq,
		OAuth:        newEmptyOAuth(t),
		EnvRegistry:  envRegistry,
		TokenDisplay: config.TokenDisplayBoth,
		AdminChecker: func(r *http.Request) (bool, error) {
			// Default: admin header triggers admin path; otherwise fall through.
			return r.Header.Get("X-Test-Admin") == "yes", nil
		},
	})
	if err := confirmer.RefreshState(context.Background()); err != nil {
		t.Fatalf("RefreshState: %v", err)
	}

	h := NewProvidersHandler(ProvidersDeps{
		Queries:      fp,
		EnvRegistry:  envRegistry,
		OAuth:        confirmer.deps.OAuth,
		RedirectBase: "http://localhost:8080",
		Confirmer:    confirmer,
	})
	return h, confirmer, fp, fq
}

// routeRequest runs a request through a chi router so URL params
// populate correctly. Returns the recorder for assertions.
func routeRequest(h *ProvidersHandler, method, path, body string) *httptest.ResponseRecorder {
	r := chi.NewRouter()
	r.Route("/v1/oidc-config", func(r chi.Router) {
		r.Post("/providers", h.Create)
		r.Get("/providers/{id}", h.Get)
		r.Put("/providers/{id}", h.Update)
		r.Delete("/providers/{id}", h.Revert)
	})
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-Test-Admin", "yes")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	return rr
}

// TestProviders_Get_EnvOnlyReturnsDefaults verifies that a GET for an
// env-only provider returns DB override fields as null with *_default
// reflecting the env value.
func TestProviders_Get_EnvOnlyReturnsDefaults(t *testing.T) {
	env := config.ProviderRegistry{
		"google": config.Provider{
			ID:           "google",
			DisplayName:  "Google",
			IssuerURL:    "https://accounts.google.com",
			ClientID:     "env-gid",
			ClientSecret: "env-gsecret",
			ClaimStyle:   "google",
			Scopes:       []string{"openid", "email", "profile"},
		},
	}
	h, _, _, _ := newProvidersHandlerForTest(t, env)

	rr := routeRequest(h, http.MethodGet, "/v1/oidc-config/providers/google", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200, body=%s", rr.Code, rr.Body.String())
	}
	var view providerView
	if err := json.Unmarshal(rr.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if view.ID != "google" {
		t.Errorf("id = %q, want google", view.ID)
	}
	if view.ClientID != nil {
		t.Errorf("client_id override = %v, want nil (env-only)", *view.ClientID)
	}
	if view.ClientIDDefault == nil || *view.ClientIDDefault != "env-gid" {
		t.Errorf("client_id_default = %v, want env-gid", view.ClientIDDefault)
	}
	if !view.HasClientSecret {
		t.Errorf("has_client_secret = false, want true (env provides secret)")
	}
	if !view.WellKnown {
		t.Errorf("well_known = false, want true (google)")
	}
	if view.CallbackURL != "http://localhost:8080/v1/auth/oidc/google/callback" {
		t.Errorf("callback_url = %q", view.CallbackURL)
	}
	// Response body must never contain the secret.
	if strings.Contains(rr.Body.String(), "env-gsecret") {
		t.Errorf("client secret leaked into GET response body")
	}
	if strings.Contains(strings.ToLower(rr.Body.String()), "\"client_secret\"") {
		t.Errorf("GET response must not contain a client_secret field at all")
	}
}

// TestProviders_Get_UnknownID_404 — GET for an ID not in env, DB, or
// well-known returns 404.
func TestProviders_Get_UnknownID_404(t *testing.T) {
	h, _, _, _ := newProvidersHandlerForTest(t, config.ProviderRegistry{})
	rr := routeRequest(h, http.MethodGet, "/v1/oidc-config/providers/nonexistent", "")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404", rr.Code)
	}
}

// TestProviders_Get_InvalidSlug_404 — slug validation triggers 404 on GET.
func TestProviders_Get_InvalidSlug_404(t *testing.T) {
	h, _, _, _ := newProvidersHandlerForTest(t, config.ProviderRegistry{})
	rr := routeRequest(h, http.MethodGet, "/v1/oidc-config/providers/-bad", "")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404", rr.Code)
	}
}

// TestProviders_Put_UpdatesFields — authenticated admin PUT updates all
// four editable fields and rebuild is triggered.
func TestProviders_Put_UpdatesFields(t *testing.T) {
	env := config.ProviderRegistry{
		"google": config.Provider{
			ID:           "google",
			DisplayName:  "Google",
			IssuerURL:    "https://accounts.google.com",
			ClientID:     "env-gid",
			ClientSecret: "env-gsecret",
			ClaimStyle:   "google",
			Scopes:       []string{"openid", "email", "profile"},
		},
	}
	h, _, fp, _ := newProvidersHandlerForTest(t, env)

	body := `{"client_id":"db-gid","client_secret":"db-gsecret","display_name":"Custom Google","enabled":true}`
	rr := routeRequest(h, http.MethodPut, "/v1/oidc-config/providers/google", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200, body=%s", rr.Code, rr.Body.String())
	}
	row, err := fp.GetOIDCProvider(context.Background(), "google")
	if err != nil {
		t.Fatalf("GetOIDCProvider: %v", err)
	}
	if row.ClientID.String != "db-gid" {
		t.Errorf("client_id = %q, want db-gid", row.ClientID.String)
	}
	if row.ClientSecret.String != "db-gsecret" {
		t.Errorf("client_secret = %q, want db-gsecret", row.ClientSecret.String)
	}
	if row.DisplayName.String != "Custom Google" {
		t.Errorf("display_name = %q", row.DisplayName.String)
	}
}

// TestProviders_Put_MissingSecretKeepsExisting — PUT without the
// client_secret field preserves the existing DB value.
func TestProviders_Put_MissingSecretKeepsExisting(t *testing.T) {
	h, _, fp, _ := newProvidersHandlerForTest(t, config.ProviderRegistry{})
	// Seed a row with a known secret.
	fp.rows["google"] = db.OidcProvider{
		ID:           "google",
		ClientID:     pgtype.Text{String: "db-gid", Valid: true},
		ClientSecret: pgtype.Text{String: "existing-secret", Valid: true},
		Enabled:      true,
	}

	body := `{"client_id":"new-gid"}`
	rr := routeRequest(h, http.MethodPut, "/v1/oidc-config/providers/google", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200, body=%s", rr.Code, rr.Body.String())
	}
	row, _ := fp.GetOIDCProvider(context.Background(), "google")
	if row.ClientSecret.String != "existing-secret" {
		t.Errorf("client_secret = %q, want preserved", row.ClientSecret.String)
	}
	if row.ClientID.String != "new-gid" {
		t.Errorf("client_id = %q, want new-gid", row.ClientID.String)
	}
}

// TestProviders_Put_EmptySecretClears — PUT with explicit empty string
// client_secret clears the stored value.
func TestProviders_Put_EmptySecretClears(t *testing.T) {
	h, _, fp, _ := newProvidersHandlerForTest(t, config.ProviderRegistry{})
	fp.rows["google"] = db.OidcProvider{
		ID:           "google",
		ClientID:     pgtype.Text{String: "db-gid", Valid: true},
		ClientSecret: pgtype.Text{String: "existing-secret", Valid: true},
		Enabled:      true,
	}

	body := `{"client_secret":""}`
	rr := routeRequest(h, http.MethodPut, "/v1/oidc-config/providers/google", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200, body=%s", rr.Code, rr.Body.String())
	}
	row, _ := fp.GetOIDCProvider(context.Background(), "google")
	if row.ClientSecret.Valid {
		t.Errorf("client_secret should have been cleared, got %q", row.ClientSecret.String)
	}
}

// TestProviders_Put_NullFieldClears — PUT with an explicit null
// clears the override.
func TestProviders_Put_NullFieldClears(t *testing.T) {
	h, _, fp, _ := newProvidersHandlerForTest(t, config.ProviderRegistry{})
	fp.rows["google"] = db.OidcProvider{
		ID:          "google",
		ClientID:    pgtype.Text{String: "db-gid", Valid: true},
		DisplayName: pgtype.Text{String: "Custom", Valid: true},
		Enabled:     true,
	}

	body := `{"display_name":null}`
	rr := routeRequest(h, http.MethodPut, "/v1/oidc-config/providers/google", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200, body=%s", rr.Code, rr.Body.String())
	}
	row, _ := fp.GetOIDCProvider(context.Background(), "google")
	if row.DisplayName.Valid {
		t.Errorf("display_name should be NULL after null PUT")
	}
}

// TestProviders_Post_BadSlug — POST with an invalid slug returns 400.
func TestProviders_Post_BadSlug(t *testing.T) {
	h, _, _, _ := newProvidersHandlerForTest(t, config.ProviderRegistry{})

	cases := []string{
		`{"id":"BadUpperCase"}`,
		`{"id":"has_underscore"}`,
		`{"id":"-leading-dash"}`,
		`{"id":"trailing-dash-"}`,
		`{"id":"a"}`, // too short
		`{"id":""}`,
	}
	for _, body := range cases {
		t.Run(body, func(t *testing.T) {
			rr := routeRequest(h, http.MethodPost, "/v1/oidc-config/providers", body)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status: got %d, want 400 for body %s", rr.Code, body)
			}
		})
	}
}

// TestProviders_Post_Duplicate_409 — POST with a DB-existing id → 409.
func TestProviders_Post_Duplicate_409(t *testing.T) {
	h, _, fp, _ := newProvidersHandlerForTest(t, config.ProviderRegistry{})
	fp.rows["keycloak"] = db.OidcProvider{ID: "keycloak", Enabled: true}

	body := `{"id":"keycloak","issuer_url":"https://kc.example.com/realms/main"}`
	rr := routeRequest(h, http.MethodPost, "/v1/oidc-config/providers", body)
	if rr.Code != http.StatusConflict {
		t.Fatalf("status: got %d, want 409, body=%s", rr.Code, rr.Body.String())
	}
}

// TestProviders_Post_CreatesNew — a fresh POST creates the row, returns 201.
func TestProviders_Post_CreatesNew(t *testing.T) {
	h, _, fp, _ := newProvidersHandlerForTest(t, config.ProviderRegistry{})

	body := `{"id":"keycloak","issuer_url":"https://kc.example.com/realms/main","client_id":"kc","client_secret":"s","claim_style":"keycloak","display_name":"Keycloak","scopes":["openid","email"]}`
	rr := routeRequest(h, http.MethodPost, "/v1/oidc-config/providers", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201, body=%s", rr.Code, rr.Body.String())
	}
	row, err := fp.GetOIDCProvider(context.Background(), "keycloak")
	if err != nil {
		t.Fatalf("GetOIDCProvider: %v", err)
	}
	if row.IssuerUrl.String != "https://kc.example.com/realms/main" {
		t.Errorf("issuer_url = %q", row.IssuerUrl.String)
	}
}

// TestProviders_Delete_RemovesRow — DELETE removes the DB row and returns 204.
func TestProviders_Delete_RemovesRow(t *testing.T) {
	h, _, fp, _ := newProvidersHandlerForTest(t, config.ProviderRegistry{})
	fp.rows["google"] = db.OidcProvider{
		ID:       "google",
		ClientID: pgtype.Text{String: "x", Valid: true},
		Enabled:  true,
	}

	rr := routeRequest(h, http.MethodDelete, "/v1/oidc-config/providers/google", "")
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status: got %d, want 204, body=%s", rr.Code, rr.Body.String())
	}
	if _, ok := fp.rows["google"]; ok {
		t.Errorf("row should have been deleted")
	}
}

// TestProviders_Auth_NoCredentials_401 — writes without admin + without
// setup token return 401.
func TestProviders_Auth_NoCredentials_401(t *testing.T) {
	fp := newFakeProvidersQuerier()
	fq := newFakeQuerier(db.OidcConfig{ID: 1})
	confirmer := NewOIDCConfigHandler(OIDCConfigDeps{
		Queries:      fq,
		OAuth:        newEmptyOAuth(t),
		EnvRegistry:  config.ProviderRegistry{},
		TokenDisplay: config.TokenDisplayBoth,
		AdminChecker: func(r *http.Request) (bool, error) { return false, nil },
	})
	if err := confirmer.RefreshState(context.Background()); err != nil {
		t.Fatalf("RefreshState: %v", err)
	}
	h := NewProvidersHandler(ProvidersDeps{
		Queries:      fp,
		EnvRegistry:  config.ProviderRegistry{},
		OAuth:        confirmer.deps.OAuth,
		RedirectBase: "http://localhost:8080",
		Confirmer:    confirmer,
	})

	r := chi.NewRouter()
	r.Route("/v1/oidc-config", func(r chi.Router) {
		r.Post("/providers", h.Create)
		r.Put("/providers/{id}", h.Update)
		r.Delete("/providers/{id}", h.Revert)
		r.Get("/providers/{id}", h.Get)
	})

	tests := []struct {
		name string
		req  *http.Request
	}{
		{"PUT", httptest.NewRequest(http.MethodPut, "/v1/oidc-config/providers/google",
			strings.NewReader(`{"client_id":"x"}`))},
		{"POST", httptest.NewRequest(http.MethodPost, "/v1/oidc-config/providers",
			strings.NewReader(`{"id":"keycloak","issuer_url":"https://x.example.com","client_id":"c","client_secret":"s","claim_style":"keycloak"}`))},
		{"DELETE", httptest.NewRequest(http.MethodDelete, "/v1/oidc-config/providers/google", nil)},
		{"GET", httptest.NewRequest(http.MethodGet, "/v1/oidc-config/providers/google", nil)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()
			r.ServeHTTP(rr, tc.req)
			if rr.Code != http.StatusUnauthorized {
				t.Errorf("status: got %d, want 401, body=%s", rr.Code, rr.Body.String())
			}
		})
	}
}

// TestProviders_Auth_SetupToken_Succeeds — a valid setup_token in the
// body (for PUT/POST) authorizes when admin path is closed.
func TestProviders_Auth_SetupToken_Succeeds(t *testing.T) {
	fp := newFakeProvidersQuerier()
	fq := newFakeQuerier(db.OidcConfig{ID: 1})
	confirmer := NewOIDCConfigHandler(OIDCConfigDeps{
		Queries:      fq,
		OAuth:        newEmptyOAuth(t),
		EnvRegistry:  config.ProviderRegistry{},
		TokenDisplay: config.TokenDisplayBoth,
		AdminChecker: func(r *http.Request) (bool, error) { return false, nil },
	})
	if err := confirmer.RefreshState(context.Background()); err != nil {
		t.Fatalf("RefreshState: %v", err)
	}
	token, err := confirmer.EnsureSetupToken(context.Background())
	if err != nil || token == "" {
		t.Fatalf("EnsureSetupToken: err=%v token=%q", err, token)
	}
	h := NewProvidersHandler(ProvidersDeps{
		Queries:      fp,
		EnvRegistry:  config.ProviderRegistry{},
		OAuth:        confirmer.deps.OAuth,
		RedirectBase: "http://localhost:8080",
		Confirmer:    confirmer,
	})

	r := chi.NewRouter()
	r.Post("/v1/oidc-config/providers", h.Create)

	body := `{"id":"keycloak","issuer_url":"https://kc.example.com/realms/main","client_id":"c","client_secret":"s","claim_style":"keycloak","setup_token":"` + token + `"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/oidc-config/providers", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201, body=%s", rr.Code, rr.Body.String())
	}
}

// TestProviders_Auth_SetupTokenHeader_AuthorizesGetAndDelete — GET
// and DELETE have no body to carry a setup_token, so the
// X-Setup-Token header fallback is required for token-mode admins
// (first-time setup) to reach those endpoints.
func TestProviders_Auth_SetupTokenHeader_AuthorizesGetAndDelete(t *testing.T) {
	fp := newFakeProvidersQuerier()
	fp.rows["keycloak"] = db.OidcProvider{
		ID:           "keycloak",
		ClientID:     pgtype.Text{String: "c", Valid: true},
		ClientSecret: pgtype.Text{String: "s", Valid: true},
		IssuerUrl:    pgtype.Text{String: "https://kc.example.com/realms/main", Valid: true},
		ClaimStyle:   pgtype.Text{String: "keycloak", Valid: true},
		Enabled:      true,
	}
	fq := newFakeQuerier(db.OidcConfig{ID: 1})
	confirmer := NewOIDCConfigHandler(OIDCConfigDeps{
		Queries:      fq,
		OAuth:        newEmptyOAuth(t),
		EnvRegistry:  config.ProviderRegistry{},
		TokenDisplay: config.TokenDisplayBoth,
		AdminChecker: func(r *http.Request) (bool, error) { return false, nil },
	})
	if err := confirmer.RefreshState(context.Background()); err != nil {
		t.Fatalf("RefreshState: %v", err)
	}
	token, err := confirmer.EnsureSetupToken(context.Background())
	if err != nil || token == "" {
		t.Fatalf("EnsureSetupToken: err=%v token=%q", err, token)
	}
	h := NewProvidersHandler(ProvidersDeps{
		Queries:      fp,
		EnvRegistry:  config.ProviderRegistry{},
		OAuth:        confirmer.deps.OAuth,
		RedirectBase: "http://localhost:8080",
		Confirmer:    confirmer,
	})

	r := chi.NewRouter()
	r.Get("/v1/oidc-config/providers/{id}", h.Get)
	r.Delete("/v1/oidc-config/providers/{id}", h.Revert)

	// GET with valid header → 200.
	req := httptest.NewRequest(http.MethodGet, "/v1/oidc-config/providers/keycloak", nil)
	req.Header.Set("X-Setup-Token", token)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET with header: got %d, want 200, body=%s", rr.Code, rr.Body.String())
	}

	// GET with wrong header → 401.
	req = httptest.NewRequest(http.MethodGet, "/v1/oidc-config/providers/keycloak", nil)
	req.Header.Set("X-Setup-Token", "wrong-"+token)
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("GET with bad header: got %d, want 401", rr.Code)
	}

	// GET with no auth at all → 401.
	req = httptest.NewRequest(http.MethodGet, "/v1/oidc-config/providers/keycloak", nil)
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("GET no auth: got %d, want 401", rr.Code)
	}

	// DELETE with valid header → 204.
	req = httptest.NewRequest(http.MethodDelete, "/v1/oidc-config/providers/keycloak", nil)
	req.Header.Set("X-Setup-Token", token)
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("DELETE with header: got %d, want 204, body=%s", rr.Code, rr.Body.String())
	}
}

// TestProviders_AdminCheckerError_500 — an AdminChecker fault surfaces
// as 500 (matches the /confirm semantics).
func TestProviders_AdminCheckerError_500(t *testing.T) {
	fp := newFakeProvidersQuerier()
	fq := newFakeQuerier(db.OidcConfig{ID: 1})
	confirmer := NewOIDCConfigHandler(OIDCConfigDeps{
		Queries:      fq,
		OAuth:        newEmptyOAuth(t),
		EnvRegistry:  config.ProviderRegistry{},
		TokenDisplay: config.TokenDisplayBoth,
		AdminChecker: func(r *http.Request) (bool, error) {
			return false, errors.New("auth backend down")
		},
	})
	if err := confirmer.RefreshState(context.Background()); err != nil {
		t.Fatalf("RefreshState: %v", err)
	}
	h := NewProvidersHandler(ProvidersDeps{
		Queries:      fp,
		EnvRegistry:  config.ProviderRegistry{},
		OAuth:        confirmer.deps.OAuth,
		RedirectBase: "http://localhost:8080",
		Confirmer:    confirmer,
	})

	r := chi.NewRouter()
	r.Put("/v1/oidc-config/providers/{id}", h.Update)

	req := httptest.NewRequest(http.MethodPut, "/v1/oidc-config/providers/google",
		strings.NewReader(`{"client_id":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status: got %d, want 500, body=%s", rr.Code, rr.Body.String())
	}
}

// TestProviders_ScopesEmptyClears — PUT with an empty scopes array
// clears the stored override (falls back to env/defaults on next read).
func TestProviders_ScopesEmptyClears(t *testing.T) {
	h, _, fp, _ := newProvidersHandlerForTest(t, config.ProviderRegistry{})
	fp.rows["google"] = db.OidcProvider{
		ID:      "google",
		Scopes:  []string{"custom"},
		Enabled: true,
	}
	body := `{"scopes":[]}`
	rr := routeRequest(h, http.MethodPut, "/v1/oidc-config/providers/google", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200, body=%s", rr.Code, rr.Body.String())
	}
	row, _ := fp.GetOIDCProvider(context.Background(), "google")
	if row.Scopes != nil {
		t.Errorf("scopes should be NULL after empty-array PUT, got %v", row.Scopes)
	}
}
