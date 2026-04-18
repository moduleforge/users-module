// Package handlers — oidc_config endpoints cover the phase 9.9a
// onboarding flow. The admin starts with an unconfirmed boot state,
// retrieves a one-time setup token from the server logs (or, in
// localhost mode, from the /setup-token endpoint), pastes it into the
// GUI, and confirms a provider selection. Successful confirm clears
// the setup-token hash in DB, recomputes BootState, and (via Rebuild)
// refreshes the OAuth registry so /v1/auth/oidc/* starts serving real
// traffic without a process restart.
package handlers

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/moduleforge/users-module/api/internal/auth"
	"github.com/moduleforge/users-module/api/internal/config"
	"github.com/moduleforge/users-module/api/internal/server"
	db "github.com/moduleforge/users-module/model/db"
)

// OIDCConfigQuerier is the narrow subset of db.Querier this handler
// actually uses. Declared here so handler tests can supply an
// in-memory fake without importing pgx; production wires *db.Queries,
// which satisfies this interface by virtue of sqlc's emit_interface.
type OIDCConfigQuerier interface {
	GetOIDCConfig(ctx context.Context) (db.OidcConfig, error)
	// UpdateOIDCConfig persists the singleton's opt-out flag. The
	// per-provider enabled JSONB that used to live here was removed in
	// 9.16 — see SetOIDCProviderEnabled on the providers querier.
	UpdateOIDCConfig(ctx context.Context, optOut bool) error
	SetSetupTokenHash(ctx context.Context, setupTokenHash pgtype.Text) error
	ClearSetupTokenHash(ctx context.Context) error
	// SetOIDCProviderEnabled is the per-provider toggle path — canonical
	// source of "enabled" since 9.16. Creates a row with only the
	// enabled flag set if none exists; otherwise updates only that
	// column. Lives on this interface (rather than a separate one) so
	// Confirm can write enabled + opt-out in a single dep.
	SetOIDCProviderEnabled(ctx context.Context, arg db.SetOIDCProviderEnabledParams) error
	// ListOIDCProviders is consumed via config.LoadMergedProviders to
	// build the merged registry read by buildProviderViews and
	// buildStatusProviders (since 9.16 the enabled flag lives on each
	// row, so status needs per-row visibility).
	ListOIDCProviders(ctx context.Context) ([]db.OidcProvider, error)
}

// OIDCConfigDeps is the set of collaborators the OIDC config handler
// needs. Passed in to keep the handler testable (swap in-memory fakes
// for queries and oauth without importing pgx in tests) and to keep
// main.go wiring explicit.
type OIDCConfigDeps struct {
	Queries      OIDCConfigQuerier
	OAuth        *auth.OAuth
	EnvRegistry  config.ProviderRegistry
	EnvNoOIDCEnv bool
	TokenDisplay config.TokenDisplay
	// AdminChecker is an optional second authorization path for
	// /confirm — when non-nil, a request arriving with a valid admin
	// session is allowed to re-confirm without a setup token.
	// Returns:
	//   (true, nil)  — caller is an authenticated admin; authorize.
	//   (false, nil) — no session or not an admin; fall through to
	//                  the setup-token path (not an error).
	//   (_,     err) — internal fault validating the session; surface
	//                  as 500 (do not silently degrade).
	AdminChecker func(r *http.Request) (isAdmin bool, err error)
}

// OIDCConfigHandler groups the three endpoints (/status, /confirm,
// /saved) plus the optional /setup-token route and the
// RequireOIDCConfirmed status callback. All methods are safe for
// concurrent use.
type OIDCConfigHandler struct {
	deps OIDCConfigDeps

	mu           sync.RWMutex
	cachedDB     db.OidcConfig
	cachedBoot   config.BootStateResult
	currentPlain string // plaintext setup token kept in-memory for /setup-token; cleared on confirm.
}

// NewOIDCConfigHandler constructs the handler. Callers should run
// RefreshState once at boot so the initial cached snapshot reflects
// current DB + OAuth init outcomes; RequireOIDCConfirmed then reads
// from the cache.
func NewOIDCConfigHandler(deps OIDCConfigDeps) *OIDCConfigHandler {
	return &OIDCConfigHandler{deps: deps}
}

// RefreshState reads the oidc_config row, merges env + DB provider
// rows, and recomputes BootState. Called once at boot and once after
// every state-changing request (confirm, provider CRUD).
func (h *OIDCConfigHandler) RefreshState(ctx context.Context) error {
	row, err := h.deps.Queries.GetOIDCConfig(ctx)
	if err != nil {
		return err
	}
	merged, err := config.LoadMergedProviders(ctx, h.deps.EnvRegistry, h.deps.Queries)
	if err != nil {
		return err
	}
	dbView := config.DBConfigView{OptOut: row.OptOut}
	state := config.DetermineBootState(
		h.buildProviderViews(merged),
		dbView,
		h.deps.EnvNoOIDCEnv,
	)
	h.mu.Lock()
	h.cachedDB = row
	h.cachedBoot = state
	h.mu.Unlock()
	return nil
}

// ApplyDBOverridesToOAuth re-runs OAuth.Rebuild using the merged
// registry so DB-layer disables / overrides (e.g. "microsoft off" saved
// last session) are reflected in OAuth.EnabledProviders() on first
// serve. A no-op when no DB rows disagree with env.
//
// Call once after RefreshState at boot; Confirm and the providers
// handler handle the runtime case inline.
func (h *OIDCConfigHandler) ApplyDBOverridesToOAuth(ctx context.Context) error {
	merged, err := config.LoadMergedProviders(ctx, h.deps.EnvRegistry, h.deps.Queries)
	if err != nil {
		return err
	}
	registry := config.MergedRegistry(merged)
	if err := h.deps.OAuth.Rebuild(ctx, registry); err != nil {
		return err
	}
	// The rebuild may have flipped InitOK for the retained set; refresh.
	return h.RefreshState(ctx)
}

// CurrentState returns a snapshot suitable for the
// RequireOIDCConfirmed middleware. Cheap (in-memory read); RefreshState
// is called on state-changing paths, not on every middleware pass.
func (h *OIDCConfigHandler) CurrentState() config.BootState {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.cachedBoot.State
}

// ----- GET /v1/oidc-config/status ---------------------------------

type statusResponse struct {
	State             string             `json:"state"`
	Confirmed         bool               `json:"confirmed"`
	Providers         []statusProvider   `json:"providers"`
	NoOIDCAccountsEnv bool               `json:"no_oidc_accounts_env"`
	NeedsSetupToken   bool               `json:"needs_setup_token"`
	SavedAt           *pgTimestampString `json:"saved_at,omitempty"`
}

type statusProvider struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Configured  bool   `json:"configured"`
	Enabled     bool   `json:"enabled"`
	InitOK      bool   `json:"init_ok"`
	Error       string `json:"error,omitempty"`
	// CallbackURL is the public-facing OIDC callback for this provider,
	// useful to the admin UI so it can display the value an operator
	// must register at their IdP. Empty when OAuthRedirectBaseURL is
	// unset (OIDC disabled).
	CallbackURL string `json:"callback_url,omitempty"`
}

// Status handles GET /v1/oidc-config/status. It is deliberately
// unauthenticated — the onboarding page needs to render before any
// credentials exist — and the response body is limited to
// operator-visible status, no secrets.
func (h *OIDCConfigHandler) Status(w http.ResponseWriter, r *http.Request) {
	h.mu.RLock()
	boot := h.cachedBoot
	row := h.cachedDB
	h.mu.RUnlock()

	merged, err := config.LoadMergedProviders(r.Context(), h.deps.EnvRegistry, h.deps.Queries)
	if err != nil {
		slog.ErrorContext(r.Context(), "oidc status: load merged providers", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to load provider state")
		return
	}
	providers := h.buildStatusProviders(merged)

	resp := statusResponse{
		State:             string(boot.State),
		Confirmed:         boot.State.Confirmed(),
		Providers:         providers,
		NoOIDCAccountsEnv: h.deps.EnvNoOIDCEnv,
		NeedsSetupToken:   row.SetupTokenHash.Valid && !boot.State.Confirmed(),
	}
	if row.SavedAt.Valid {
		ts := pgTimestampString(row.SavedAt.Time.UTC().Format("2006-01-02T15:04:05Z"))
		resp.SavedAt = &ts
	}
	server.JSON(w, http.StatusOK, resp)
}

// pgTimestampString exists only so status JSON renders as an ISO-8601
// string rather than the pgtype.Timestamptz Go struct default.
type pgTimestampString string

func (p pgTimestampString) MarshalJSON() ([]byte, error) {
	return json.Marshal(string(p))
}

// ----- GET /v1/oidc-config/setup-token ----------------------------

type setupTokenResponse struct {
	Token string `json:"token"`
}

// SetupToken handles GET /v1/oidc-config/setup-token. Mounted only
// when TOKEN_DISPLAY is {localhost, both}. Response returns the
// plaintext token only for requests whose RemoteAddr parses to a
// loopback address — inside docker this means the operator must exec
// into the container to read the value (e.g.
//
//	docker exec users-module-api wget -qO- http://localhost:8080/v1/oidc-config/setup-token
//
// which is the intended failure mode for remote access). Returns 404
// once the state is confirmed so a rogue operator can't fish for a
// stale hash.
func (h *OIDCConfigHandler) SetupToken(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackAddr(r.RemoteAddr) {
		server.Error(w, http.StatusForbidden, "forbidden", "setup token endpoint is loopback-only")
		return
	}

	h.mu.RLock()
	state := h.cachedBoot.State
	h.mu.RUnlock()
	if state.Confirmed() {
		server.Error(w, http.StatusNotFound, "not_found", "setup already confirmed")
		return
	}

	h.mu.RLock()
	tok := h.currentPlainTokenLocked()
	h.mu.RUnlock()
	if tok == "" {
		server.Error(w, http.StatusNotFound, "not_found", "no active setup token")
		return
	}
	server.JSON(w, http.StatusOK, setupTokenResponse{Token: tok})
}

// currentPlainTokenLocked returns the most recently minted plaintext
// setup token held in memory. We never reconstruct plaintext from the
// stored hash; EnsureSetupToken stashes the plaintext at emission time
// for this endpoint's use. Caller must hold h.mu (read or write).
func (h *OIDCConfigHandler) currentPlainTokenLocked() string {
	return h.currentPlain
}

// setCurrentPlain atomically replaces the in-memory plaintext copy.
// Cleared (empty string) once state flips to confirmed so a leaking
// future read cannot surface a stale token.
func (h *OIDCConfigHandler) setCurrentPlain(plain string) {
	h.mu.Lock()
	h.currentPlain = plain
	h.mu.Unlock()
}

// ----- POST /v1/oidc-config/confirm -------------------------------

type confirmRequest struct {
	SetupToken       string   `json:"setup_token"`
	EnabledProviders []string `json:"enabled_providers"`
	OptOut           bool     `json:"opt_out"`
}

// Confirm handles POST /v1/oidc-config/confirm. The caller must either
// present a valid setup token (pre-confirmation) or be an authenticated
// admin (post-confirmation reconfig).
func (h *OIDCConfigHandler) Confirm(w http.ResponseWriter, r *http.Request) {
	var req confirmRequest
	if err := server.Decode(r, &req); err != nil {
		server.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}

	authorized, err := h.authorizeConfirm(r, req.SetupToken)
	if err != nil {
		slog.ErrorContext(r.Context(), "oidc confirm: admin check failed", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to authorize request")
		return
	}
	if !authorized {
		server.Error(w, http.StatusUnauthorized, "unauthorized", "setup token or admin session required")
		return
	}

	// Admin reconfigure edge case: if an authenticated admin re-confirms
	// with a selection that still has a broken provider, the handler
	// returns 200 with the recomputed status payload but the new state
	// flips to InitFailed (strict, Phase 9.10a). The admin's current
	// session remains valid for the process, but RequireOIDCConfirmed
	// will 503 their next /v1/* request. Recovery requires a process
	// restart so EnsureSetupToken can mint a fresh banner token.

	// Normalize the submitted enabled list: lowercase, drop unknown IDs,
	// dedupe. Empty list implies opt-out semantics (no OIDC available).
	wanted := normalizeEnabledSet(h.deps.EnvRegistry, req.EnabledProviders)
	optOut := req.OptOut || len(wanted) == 0

	// Persist per-provider enabled flags on oidc_providers (the canonical
	// source of truth since 9.16). For each env-known provider, write the
	// toggle value; SetOIDCProviderEnabled is an upsert that leaves other
	// columns untouched so field-level overrides from the Edit modal are
	// preserved.
	for id := range h.deps.EnvRegistry {
		if err := h.deps.Queries.SetOIDCProviderEnabled(r.Context(), db.SetOIDCProviderEnabledParams{
			ID:      id,
			Enabled: wanted[id],
		}); err != nil {
			slog.ErrorContext(r.Context(), "oidc confirm: set provider enabled", "error", err, "provider", id)
			server.Error(w, http.StatusInternalServerError, "internal_error", "failed to persist configuration")
			return
		}
	}

	if err := h.deps.Queries.UpdateOIDCConfig(r.Context(), optOut); err != nil {
		slog.ErrorContext(r.Context(), "oidc confirm: db update", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to persist configuration")
		return
	}

	// Rebuild OAuth with the MERGED registry (env + DB per-row overrides).
	// Critical: using the env-only registry here was the pre-9.16 bug
	// that made an admin's just-saved provider changes disappear when
	// they hit Confirm.
	merged, err := config.LoadMergedProviders(r.Context(), h.deps.EnvRegistry, h.deps.Queries)
	if err != nil {
		slog.ErrorContext(r.Context(), "oidc confirm: load merged providers", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to reload providers")
		return
	}
	if err := h.deps.OAuth.Rebuild(r.Context(), config.MergedRegistry(merged)); err != nil {
		slog.ErrorContext(r.Context(), "oidc confirm: rebuild oauth", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to reload providers")
		return
	}

	// Recompute state now that both DB + OAuth have the new view.
	if err := h.RefreshState(r.Context()); err != nil {
		slog.ErrorContext(r.Context(), "oidc confirm: refresh state", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to recompute state")
		return
	}

	// If the new state is confirmed, clear the active setup token: it
	// has served its one-shot purpose. We do this after the state
	// refresh so the middleware never sees a half-updated view.
	if h.CurrentState().Confirmed() {
		if err := h.deps.Queries.ClearSetupTokenHash(r.Context()); err != nil {
			// Non-fatal: the token hash lingering in DB isn't a security
			// issue (it's one-shot and will be reused only if the state
			// flips back to unconfirmed), but worth logging.
			slog.WarnContext(r.Context(), "oidc confirm: clear setup token", "error", err)
		}
		h.setCurrentPlain("")
		if err := h.RefreshState(r.Context()); err != nil {
			slog.WarnContext(r.Context(), "oidc confirm: post-clear refresh", "error", err)
		}
	}

	h.Status(w, r)
}

// authorizeConfirm validates either an authenticated admin session OR
// a valid setup token. Admin path is checked first so admins doing a
// routine reconfigure don't have to fetch a fresh setup token. If the
// caller didn't submit a token via the JSON body (e.g. GET / DELETE
// endpoints that have no body), the X-Setup-Token header is consulted
// as a fallback so the same dual-auth model applies uniformly.
// Returns:
//   - (true, nil)  — authorized; proceed.
//   - (false, nil) — neither path succeeded; 401.
//   - (_,    err)  — AdminChecker had an internal fault; 500.
func (h *OIDCConfigHandler) authorizeConfirm(r *http.Request, submitted string) (bool, error) {
	if h.deps.AdminChecker != nil {
		isAdmin, err := h.deps.AdminChecker(r)
		if err != nil {
			return false, err
		}
		if isAdmin {
			return true, nil
		}
	}
	token := submitted
	if token == "" {
		token = r.Header.Get("X-Setup-Token")
	}
	if token != "" {
		h.mu.RLock()
		hash := h.cachedDB.SetupTokenHash
		h.mu.RUnlock()
		if hash.Valid && auth.VerifySetupToken(token, hash.String) {
			return true, nil
		}
	}
	return false, nil
}

// ----- GET /v1/oidc-config/saved ---------------------------------

type savedResponse struct {
	EnabledProviders map[string]bool    `json:"enabled_providers"`
	OptOut           bool               `json:"opt_out"`
	SavedAt          *pgTimestampString `json:"saved_at,omitempty"`
}

// Saved handles GET /v1/oidc-config/saved. Powers the GUI "revert"
// button — returns the current per-provider enabled state aggregated
// from oidc_providers rows so the client can restore toggle state
// without re-posting.
func (h *OIDCConfigHandler) Saved(w http.ResponseWriter, r *http.Request) {
	h.mu.RLock()
	row := h.cachedDB
	h.mu.RUnlock()

	merged, err := config.LoadMergedProviders(r.Context(), h.deps.EnvRegistry, h.deps.Queries)
	if err != nil {
		slog.ErrorContext(r.Context(), "oidc saved: load merged providers", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to load saved config")
		return
	}
	enabled := make(map[string]bool, len(merged))
	for id, m := range merged {
		enabled[id] = m.MergedEnabled()
	}
	resp := savedResponse{
		EnabledProviders: enabled,
		OptOut:           row.OptOut,
	}
	if row.SavedAt.Valid {
		ts := pgTimestampString(row.SavedAt.Time.UTC().Format("2006-01-02T15:04:05Z"))
		resp.SavedAt = &ts
	}
	server.JSON(w, http.StatusOK, resp)
}

// ----- Setup token lifecycle helpers -----------------------------

// EnsureSetupToken maintains the invariant "at most one live setup
// token exists per unconfirmed state." It has three branches when the
// state is unconfirmed:
//
//  1. DB hash missing — first-boot path: generate a new token, persist
//     its hash, stash the plaintext in memory, return the plaintext so
//     the caller can emit the banner.
//  2. DB hash present but no in-memory plaintext — restart path: the
//     prior process died before (or just after) emitting its banner and
//     the plaintext was unrecoverable anyway. Rotate the token so the
//     operator has a recoverable value; the one-time-use property is
//     unchanged because the old plaintext was already lost.
//  3. DB hash present and in-memory plaintext already held — same
//     process called EnsureSetupToken twice. Return "" so the banner is
//     not re-emitted for a no-op.
//
// When the state is confirmed the method idempotently ensures the DB
// hash is cleared and returns "".
func (h *OIDCConfigHandler) EnsureSetupToken(ctx context.Context) (string, error) {
	h.mu.RLock()
	state := h.cachedBoot.State
	hash := h.cachedDB.SetupTokenHash
	plainInMem := h.currentPlain
	h.mu.RUnlock()

	if state.Confirmed() {
		// Idempotently make sure the DB hash is cleared.
		if hash.Valid {
			if err := h.deps.Queries.ClearSetupTokenHash(ctx); err != nil {
				return "", err
			}
			if err := h.RefreshState(ctx); err != nil {
				return "", err
			}
		}
		return "", nil
	}

	// Unconfirmed & DB hash present & in-memory plaintext already held:
	// same-process repeat call. Nothing to do, don't re-emit the banner.
	if hash.Valid && plainInMem != "" {
		return "", nil
	}

	// Either the DB has no hash (first-boot path) or the DB has a hash
	// but we have no plaintext (restart path). Both paths mint a fresh
	// token; the restart path additionally overwrites the stored hash.
	// Before overwriting we clear the existing hash so SetSetupTokenHash
	// replaces rather than races with a stale value — SetSetupTokenHash
	// is an UPDATE so this is belt-and-braces, but the explicit clear
	// also drops the old SetupTokenCreatedAt in the fake-querier tests.
	if hash.Valid {
		if err := h.deps.Queries.ClearSetupTokenHash(ctx); err != nil {
			return "", err
		}
	}

	plain, digest, err := auth.GenerateSetupToken()
	if err != nil {
		return "", err
	}
	if err := h.deps.Queries.SetSetupTokenHash(ctx, pgtype.Text{String: digest, Valid: true}); err != nil {
		return "", err
	}
	h.setCurrentPlain(plain)
	if err := h.RefreshState(ctx); err != nil {
		return "", err
	}
	return plain, nil
}

// ----- Helpers --------------------------------------------------

// buildProviderViews turns the merged-registry view into the
// ProviderInitView slice DetermineBootState consumes. Enabled is read
// from each provider's MergedEnabled() — which consults
// oidc_providers.enabled (the 9.16 canonical source of truth).
func (h *OIDCConfigHandler) buildProviderViews(merged map[string]*config.MergedProvider) []config.ProviderInitView {
	all := h.deps.OAuth.AllProviders()
	initByID := make(map[string]bool, len(all))
	for _, s := range all {
		initByID[s.ID] = s.InitOK
	}

	out := make([]config.ProviderInitView, 0, len(h.deps.EnvRegistry))
	for id := range h.deps.EnvRegistry {
		enabled := true
		if m, ok := merged[id]; ok {
			enabled = m.MergedEnabled()
		}
		out = append(out, config.ProviderInitView{
			ID:         id,
			Configured: true,
			Enabled:    enabled,
			InitOK:     initByID[id],
		})
	}
	return out
}

// buildStatusProviders renders the provider list for the /status
// response. Combines registry metadata (display name) with the
// OAuth-observed InitOK/Err and the merged enabled flag. Sorted by
// ID for deterministic output.
func (h *OIDCConfigHandler) buildStatusProviders(merged map[string]*config.MergedProvider) []statusProvider {
	all := h.deps.OAuth.AllProviders()
	out := make([]statusProvider, 0, len(h.deps.EnvRegistry))

	byID := make(map[string]*auth.ProviderState, len(all))
	for _, s := range all {
		byID[s.ID] = s
	}

	ids := h.deps.EnvRegistry.IDs()
	for _, id := range ids {
		p := h.deps.EnvRegistry[id]
		enabled := true
		if m, ok := merged[id]; ok {
			enabled = m.MergedEnabled()
		}
		sp := statusProvider{
			ID:          id,
			DisplayName: p.DisplayName,
			Configured:  true,
			Enabled:     enabled,
		}
		if h.deps.OAuth != nil && h.deps.OAuth.RedirectBase != "" {
			sp.CallbackURL = strings.TrimRight(h.deps.OAuth.RedirectBase, "/") + "/v1/auth/oidc/" + id + "/callback"
		}
		if state := byID[id]; state != nil {
			sp.InitOK = state.InitOK
			if state.Err != nil {
				sp.Error = state.Err.Error()
			}
		}
		out = append(out, sp)
	}
	return out
}

// normalizeEnabledSet returns the set of env-known provider IDs the
// caller asked to enable. Unknown IDs are silently dropped; case
// and whitespace are normalized so "Google " works the same as
// "google". The returned map is complete across the env registry —
// absent IDs get false.
func normalizeEnabledSet(registry config.ProviderRegistry, enabled []string) map[string]bool {
	out := make(map[string]bool, len(registry))
	for id := range registry {
		out[id] = false
	}
	for _, id := range enabled {
		id = strings.ToLower(strings.TrimSpace(id))
		if _, known := registry[id]; !known {
			continue
		}
		out[id] = true
	}
	return out
}

// isLoopbackAddr reports whether addr (in host:port form, as supplied
// by net/http's RemoteAddr) refers to a loopback interface. We accept
// 127.0.0.0/8, ::1, and the literal "localhost" for belt-and-braces.
func isLoopbackAddr(addr string) bool {
	host := addr
	if h, _, err := net.SplitHostPort(addr); err == nil {
		host = h
	}
	host = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
