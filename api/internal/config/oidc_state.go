package config

// BootState captures the full (env + DB + init) verdict on the OIDC
// subsystem's configuration at any given moment. Unlike
// auth.OverallStatus — which only knows what env and per-provider init
// told it — BootState layers the DB-persisted operator choice (opt_out
// flag, per-provider enable overrides) on top so callers get a single
// answer to "can we serve traffic or do we need onboarding?".
type BootState string

const (
	// BootStateConfirmedOK means the operator (explicitly or implicitly)
	// approved the current OIDC configuration and at least one provider
	// initialized successfully. Normal-operations state.
	BootStateConfirmedOK BootState = "confirmed_ok"

	// BootStateConfirmedOptOut means the operator intentionally runs
	// without OIDC — either by setting NO_OIDC_ACCOUNTS=1 in env or by
	// confirming an all-off configuration in the onboarding UI.
	BootStateConfirmedOptOut BootState = "confirmed_opt_out"

	// BootStateInitFailed means providers are configured via env but every
	// enabled one failed to initialize at boot. This is the most common
	// unconfirmed state in practice (bad secrets, wrong issuer URL, etc.)
	// and the main trigger for the /oidc-config onboarding flow.
	BootStateInitFailed BootState = "init_failed"

	// BootStateNoEnvNoFlag means no providers were configured via env AND
	// the operator hasn't opted out (via NO_OIDC_ACCOUNTS or the DB flag).
	// Indistinguishable from a fresh install; also routes to onboarding.
	BootStateNoEnvNoFlag BootState = "no_env_no_flag"
)

// Confirmed reports whether the state represents a successfully configured
// system that can serve traffic. The negation is what the onboarding
// middleware uses to gate /v1/* routes.
func (s BootState) Confirmed() bool {
	return s == BootStateConfirmedOK || s == BootStateConfirmedOptOut
}

// ProviderInitView is the minimal per-provider snapshot DetermineBootState
// consumes. It exists so oidc_state stays free of a hard dependency on the
// auth package (which imports this one); callers adapt their own
// ProviderState to this shape at the call site.
type ProviderInitView struct {
	// ID is the lowercased provider identifier (e.g. "google").
	ID string
	// Configured reports whether the provider appeared in the env-derived
	// registry (i.e. CLIENT_ID was set). Always true in today's flow,
	// carried as a named field so a future "always show all well-known
	// providers" feature doesn't need a signature change.
	Configured bool
	// Enabled reflects the resolved on/off state after applying DB
	// provider_enabled overrides on top of env. A false value hides the
	// provider from init (and from EnabledProviders()) without removing it
	// from AllProviders(), so the onboarding UI can still render its
	// toggle.
	Enabled bool
	// InitOK mirrors ProviderState.InitOK — only meaningful when Enabled;
	// set false for providers filtered out before init.
	InitOK bool
}

// DBConfigView is the DetermineBootState-facing summary of the
// oidc_config singleton row. Carried as a named struct so main.go can
// hand-build one from the sqlc-generated OidcConfig without this package
// having to depend on model/db. Per-provider enable flags live on the
// oidc_providers table (since 9.16); the caller resolves them into
// ProviderInitView.Enabled before calling DetermineBootState, so this
// struct only carries the singleton-scope opt-out bit.
type DBConfigView struct {
	// OptOut captures the DB-persisted equivalent of NO_OIDC_ACCOUNTS,
	// set by confirming an all-off configuration in the onboarding UI.
	OptOut bool
}

// BootStateResult bundles the computed verdict with the derived set of
// provider IDs that should be enabled. Callers use Enabled to decide
// whether to Rebuild OAuth with a filtered registry.
type BootStateResult struct {
	State BootState
	// Enabled is the set of provider IDs that survived both env presence
	// and DB-override filtering. Sorted ascending for stable test output.
	Enabled []string
}

// DetermineBootState is the single source of truth for the onboarding
// state machine. Inputs are already-loaded: provider views (each with
// Configured / Enabled / InitOK pre-populated from the merge layer and
// the OAuth registry), the DB singleton row summary, and the
// NO_OIDC_ACCOUNTS env flag. The function is pure — no I/O, no globals —
// so it's trivially unit-testable.
//
// Algorithm (strict, Phase 9.10a; enable-source unified 9.16):
//  1. Partition providers into "in-env" candidates.
//  2. Filter by p.Enabled (the merged view's effective enable flag;
//     caller resolves env / oidc_providers.enabled before the call).
//  3. Count InitOK among the enabled set.
//  4. Branch:
//     - no candidates → ConfirmedOptOut iff DB opt_out OR envFlag;
//     else NoEnvNoFlag.
//     - all enabled InitOK → ConfirmedOK.
//     - any enabled failed → InitFailed (strict; partial failure is
//       fatal — operators must disable a broken provider explicitly
//       via /confirm before the app will serve traffic).
func DetermineBootState(providers []ProviderInitView, db DBConfigView, envNoOIDCAccounts bool) BootStateResult {
	enabled := make([]string, 0, len(providers))
	initOKCount := 0
	candidateCount := 0

	for _, p := range providers {
		if !p.Configured {
			continue
		}
		candidateCount++
		if !p.Enabled {
			continue
		}
		enabled = append(enabled, p.ID)
		if p.InitOK {
			initOKCount++
		}
	}

	// No providers configured via env at all: onboarding opt-out is the
	// only way out of NoEnvNoFlag. DB opt_out and env flag are equivalent.
	if candidateCount == 0 {
		if db.OptOut || envNoOIDCAccounts {
			return BootStateResult{State: BootStateConfirmedOptOut, Enabled: enabled}
		}
		return BootStateResult{State: BootStateNoEnvNoFlag, Enabled: enabled}
	}

	// At least one env-configured provider exists. If the operator
	// disabled all of them via DB overrides AND set opt_out, treat it
	// as an explicit opt-out via the confirm UI. If they disabled all
	// without opt_out, that's a limbo state — force onboarding so
	// they explicitly opt out or re-enable something.
	if len(enabled) == 0 {
		if db.OptOut {
			return BootStateResult{State: BootStateConfirmedOptOut, Enabled: enabled}
		}
		return BootStateResult{State: BootStateInitFailed, Enabled: enabled}
	}

	// Phase 9.10a: strict confirmation. Every enabled provider must
	// have initialized. Any failure forces InitFailed — partial
	// success is not "confirmed." The admin must either fix the env
	// for the broken provider OR disable it explicitly via
	// /v1/oidc-config/confirm before the app will serve traffic.
	if len(enabled)-initOKCount > 0 {
		return BootStateResult{State: BootStateInitFailed, Enabled: enabled}
	}

	// All enabled candidates initialized cleanly.
	return BootStateResult{State: BootStateConfirmedOK, Enabled: enabled}
}
