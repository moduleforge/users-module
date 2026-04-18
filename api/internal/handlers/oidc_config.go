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
	UpdateOIDCConfig(ctx context.Context, arg db.UpdateOIDCConfigParams) error
	SetSetupTokenHash(ctx context.Context, setupTokenHash pgtype.Text) error
	ClearSetupTokenHash(ctx context.Context) error
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

// RefreshState reads the oidc_config row and recomputes BootState.
// Called once at boot and once after every successful confirm; any
// future trigger that changes the effective state should call this too.
func (h *OIDCConfigHandler) RefreshState(ctx context.Context) error {
	row, err := h.deps.Queries.GetOIDCConfig(ctx)
	if err != nil {
		return err
	}
	dbView, err := decodeDBView(row)
	if err != nil {
		return err
	}
	state := config.DetermineBootState(
		h.buildProviderViews(dbView.ProviderOverrides),
		dbView,
		h.deps.EnvNoOIDCEnv,
	)
	h.mu.Lock()
	h.cachedDB = row
	h.cachedBoot = state
	h.mu.Unlock()
	return nil
}

// ApplyDBOverridesToOAuth re-runs OAuth.Rebuild when the DB-persisted
// provider_enabled overrides filter out env-configured providers (e.g.
// on a boot that inherited a prior "microsoft off" override). After
// this call oauth.EnabledProviders() matches the DB-intended enabled
// set. Safe to call even when no overrides exist — in that case it's
// a no-op (the current OAuth already reflects the full env registry).
//
// Call once after RefreshState at boot; Confirm() handles the runtime
// case inline.
func (h *OIDCConfigHandler) ApplyDBOverridesToOAuth(ctx context.Context) error {
	h.mu.RLock()
	rawOverrides := h.cachedDB.ProviderEnabled
	h.mu.RUnlock()

	overrides := h.overridesOrEmpty(ctx, rawOverrides)
	if len(overrides) == 0 {
		// Malformed / missing overrides: leave OAuth alone. The plan
		// treats empty overrides as "no DB opinion" → use full env.
		return nil
	}

	// Only rebuild if overrides actually change the set. A trivial
	// "all true" override matches the full registry, so skip.
	filtered := filterRegistry(h.deps.EnvRegistry, overrides)
	if len(filtered) == len(h.deps.EnvRegistry) {
		return nil
	}
	if err := h.deps.OAuth.Rebuild(ctx, filtered); err != nil {
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

	overrides := h.overridesOrEmpty(r.Context(), row.ProviderEnabled)
	providers := h.buildStatusProviders(overrides)

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

	// Compute the provider_enabled override map. If the caller submits
	// an empty enabled_providers array we honor opt_out semantics — all
	// known providers marked off + opt_out=true unless explicitly set.
	overrides := buildOverrides(h.deps.EnvRegistry, req.EnabledProviders)
	optOut := req.OptOut || len(req.EnabledProviders) == 0

	overrideJSON, err := json.Marshal(overrides)
	if err != nil {
		slog.ErrorContext(r.Context(), "oidc confirm: marshal overrides", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to persist configuration")
		return
	}

	if err := h.deps.Queries.UpdateOIDCConfig(r.Context(), db.UpdateOIDCConfigParams{
		ProviderEnabled: overrideJSON,
		OptOut:          optOut,
	}); err != nil {
		slog.ErrorContext(r.Context(), "oidc confirm: db update", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to persist configuration")
		return
	}

	// Rebuild OAuth with the filtered registry so providers the operator
	// just toggled off stop appearing in /v1/auth/providers and (if a
	// previously-failed provider was toggled on) its init gets retried.
	filtered := filterRegistry(h.deps.EnvRegistry, overrides)
	if err := h.deps.OAuth.Rebuild(r.Context(), filtered); err != nil {
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
// button — returns the last-saved DB snapshot so the client can
// re-populate the toggle state without re-posting.
func (h *OIDCConfigHandler) Saved(w http.ResponseWriter, r *http.Request) {
	h.mu.RLock()
	row := h.cachedDB
	h.mu.RUnlock()

	overrides := h.overridesOrEmpty(r.Context(), row.ProviderEnabled)
	resp := savedResponse{
		EnabledProviders: overrides,
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

// decodeDBView translates a sqlc OidcConfig row into the
// config.DBConfigView shape DetermineBootState expects. Malformed
// JSONB is treated as "no overrides" — the safer default than
// crashing.
func decodeDBView(row db.OidcConfig) (config.DBConfigView, error) {
	overrides, err := parseProviderOverrides(row.ProviderEnabled)
	if err != nil {
		return config.DBConfigView{}, err
	}
	return config.DBConfigView{
		ProviderOverrides: overrides,
		OptOut:            row.OptOut,
	}, nil
}

// parseProviderOverrides decodes the provider_enabled JSONB column.
// An empty / NULL / malformed value yields an empty map (non-error)
// so boot keeps going; callers treating that as "no overrides" is the
// correct fallback.
func parseProviderOverrides(raw []byte) (map[string]bool, error) {
	if len(raw) == 0 {
		return map[string]bool{}, nil
	}
	var out map[string]bool
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]bool{}, err
	}
	if out == nil {
		out = map[string]bool{}
	}
	return out, nil
}

// overridesOrEmpty is the non-fatal wrapper around parseProviderOverrides
// used by request handlers. A parse failure is logged at Warn level with
// a truncated preview of the raw value so operators can diagnose a
// corrupt provider_enabled JSONB payload without chasing silent empty
// maps, and the empty-map fallback keeps the request path working.
func (h *OIDCConfigHandler) overridesOrEmpty(ctx context.Context, raw []byte) map[string]bool {
	overrides, err := parseProviderOverrides(raw)
	if err != nil {
		slog.WarnContext(ctx, "oidc config: failed to parse provider_enabled JSONB; falling back to empty overrides",
			"provider_overrides_parse_failed", true,
			"error", err,
			"raw_preview", rawPreview(raw, 128),
		)
	}
	return overrides
}

// rawPreview returns up to maxLen bytes of raw as a string suitable for
// structured logging; longer payloads are truncated with a trailing
// ellipsis so a pathological JSONB blob doesn't flood the log.
func rawPreview(raw []byte, maxLen int) string {
	if len(raw) <= maxLen {
		return string(raw)
	}
	return string(raw[:maxLen]) + "..."
}

// buildProviderViews turns the env-registry + OAuth init state into
// the ProviderInitView slice DetermineBootState consumes. Takes the
// DB overrides as input so Enabled reflects the resolved (post-filter)
// value, consistent with what the onboarding UI shows.
func (h *OIDCConfigHandler) buildProviderViews(overrides map[string]bool) []config.ProviderInitView {
	all := h.deps.OAuth.AllProviders()
	out := make([]config.ProviderInitView, 0, len(h.deps.EnvRegistry))

	// Iterate the env registry (source of truth for "configured") and
	// cross-reference OAuth.AllProviders for InitOK.
	initByID := make(map[string]bool, len(all))
	for _, s := range all {
		initByID[s.ID] = s.InitOK
	}

	for id := range h.deps.EnvRegistry {
		v := config.ProviderInitView{
			ID:         id,
			Configured: true,
			Enabled:    true,
			InitOK:     initByID[id],
		}
		if len(overrides) > 0 {
			if sel, ok := overrides[id]; ok {
				v.Enabled = sel
			}
		}
		out = append(out, v)
	}
	return out
}

// buildStatusProviders renders the provider list for the /status
// response. Combines registry metadata (display name) with the
// OAuth-observed InitOK/Err and the resolved enabled flag. Sorted by
// ID for deterministic output.
func (h *OIDCConfigHandler) buildStatusProviders(overrides map[string]bool) []statusProvider {
	all := h.deps.OAuth.AllProviders()
	out := make([]statusProvider, 0, len(h.deps.EnvRegistry))

	// Pre-index by ID so we can surface per-provider Err.
	byID := make(map[string]*auth.ProviderState, len(all))
	for _, s := range all {
		byID[s.ID] = s
	}

	ids := h.deps.EnvRegistry.IDs()
	for _, id := range ids {
		p := h.deps.EnvRegistry[id]
		enabled := true
		if len(overrides) > 0 {
			if v, ok := overrides[id]; ok {
				enabled = v
			}
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

// buildOverrides computes the provider_enabled JSONB contents from the
// submitted enabled list. The output includes every provider in the
// env registry (false for omitted IDs) so the DB row is a complete
// picture, not a delta — simpler to reason about on subsequent reads.
// Providers submitted that aren't in the env registry are silently
// dropped.
func buildOverrides(registry config.ProviderRegistry, enabled []string) map[string]bool {
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

// filterRegistry returns a new ProviderRegistry containing the
// entries that should be active per DB overrides.
//
// INVARIANT: absent = enabled by default; only an explicit `false`
// override disables a provider. This matches DetermineBootState so a
// partial overrides map (e.g. only google mentioned) does not silently
// drop microsoft. The original registry is not mutated.
func filterRegistry(registry config.ProviderRegistry, overrides map[string]bool) config.ProviderRegistry {
	out := make(config.ProviderRegistry, len(registry))
	for id, p := range registry {
		if v, ok := overrides[id]; ok && !v {
			// Explicit false → disabled.
			continue
		}
		// Either no override for this id (default on) or override is true.
		out[id] = p
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
