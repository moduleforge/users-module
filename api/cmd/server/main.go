package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/go-chi/chi/v5"

	corehttpapi "github.com/moduleforge/core-api/httpapi"
	"github.com/moduleforge/core-api/fieldcrypto"
	coreservice "github.com/moduleforge/core-api/service"
	"github.com/moduleforge/core-api/display"
	"github.com/moduleforge/users-module/api/internal/audit"
	"github.com/moduleforge/users-module/api/internal/auth"
	"github.com/moduleforge/users-module/api/internal/config"
	coredb "github.com/moduleforge/core-model/db"
	localdb "github.com/moduleforge/users-module/api/internal/db"
	"github.com/moduleforge/users-module/api/internal/email"
	"github.com/moduleforge/users-module/api/internal/handlers"
	authhandlers "github.com/moduleforge/users-module/api/internal/handlers/auth"
	"github.com/moduleforge/users-module/api/internal/observability"
	"github.com/moduleforge/users-module/api/internal/server"
	db "github.com/moduleforge/users-module/model/db"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("config load failed", "error", err)
		os.Exit(1)
	}

	logLevel := resolveLogLevel(os.Getenv("LOG_LEVEL"))
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel}))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	otelShutdown, err := observability.Init(ctx, cfg)
	if err != nil {
		slog.ErrorContext(ctx, "otel init failed", "error", err)
		os.Exit(1)
	}

	// Open pgx pool.
	pool, err := localdb.New(ctx, cfg)
	if err != nil {
		slog.ErrorContext(ctx, "database connection failed", "error", err)
		os.Exit(1)
	}

	// Build query layer.
	queries := db.New(pool)
	coreQueries := coredb.New(pool)

	// Build auth components. The Verifier is used by RequireAuth to validate
	// incoming Bearer tokens — post-Phase 9 those are always the local JWTs
	// minted by /v1/auth/login or the OIDC callback, never a raw provider
	// id_token. So the verifier is local-only.
	verifier, err := auth.NewVerifier(ctx, "", "", cfg.LocalAuth.JWTSecret, cfg.LocalAuth.LocalIssuer)
	if err != nil {
		slog.ErrorContext(ctx, "auth verifier init failed", "error", err)
		os.Exit(1)
	}

	// RequireAuth needs a ClaimMapper to turn the local JWT's claims into a
	// Principal. The local JWT uses flat "email" + "roles" claims, which the
	// generic mapper handles with the pass-through paths below. After Phase 9,
	// inbound Bearer tokens are always these locally-minted JWTs — provider
	// id_tokens are traded for a local JWT by the OIDC callback, not presented
	// directly to API endpoints.
	localMapper, err := auth.NewClaimMapper("generic", auth.MapperOptions{
		AdminRole: cfg.Auth.AdminRole,
		EmailPath: "email",
		RolesPath: "roles",
	})
	if err != nil {
		slog.ErrorContext(ctx, "local claim mapper init failed", "error", err)
		os.Exit(1)
	}

	// Merge env-declared providers with the DB-persisted override layer
	// (phase 9.11a) so a prior admin edit sticks across restarts.
	// LoadMergedProviders is idempotent with no DB rows — boots without
	// oidc_providers entries produce a registry identical to env.
	merged, err := config.LoadMergedProviders(ctx, cfg.Providers, queries)
	if err != nil {
		slog.ErrorContext(ctx, "oidc provider merge failed", "error", err)
		os.Exit(1)
	}
	cfg.Providers = config.MergedRegistry(merged)

	// Build the OAuth orchestrator. Per-provider discovery failures are
	// captured in ProviderState.Err and logged; the bad provider is simply
	// omitted from EnabledProviders(). Only construction-level problems
	// (missing JWT_SECRET, missing OAUTH_REDIRECT_BASE_URL) are still fatal
	// because no amount of provider toggling can recover from them.
	oauth, err := auth.NewOAuth(ctx, cfg)
	if err != nil {
		slog.ErrorContext(ctx, "oauth init failed", "error", err)
		os.Exit(1)
	}
	slog.InfoContext(ctx, "oauth initialized",
		"status", oauth.Status(),
		"enabled_providers", len(oauth.EnabledProviders()),
		"total_providers", len(oauth.AllProviders()),
	)

	// Build the UserResolver up-front — both the onboarding
	// AdminChecker and the post-auth /v1/* handlers need it.
	resolver := auth.NewUserResolver(pool, queries, coreQueries, cfg.Auth.AdminRole, cfg.LocalAuth.LocalIssuer)

	// Build the onboarding handler + state cache. The handler owns the
	// oidc_config row and the derived BootState; RequireOIDCConfirmed
	// reads its CurrentState closure on every /v1/* request.
	onboarding := handlers.NewOIDCConfigHandler(handlers.OIDCConfigDeps{
		Queries:      queries,
		OAuth:        oauth,
		EnvRegistry:  cfg.Providers,
		EnvNoOIDCEnv: oauth.EnvNoOIDCAccounts(),
		TokenDisplay: cfg.Onboarding.TokenDisplay,
		// AdminChecker lets an authenticated admin re-confirm without
		// fetching a fresh setup token (Phase 9.10a). Returns
		// (false, nil) on missing/invalid auth so /confirm falls
		// through to the setup-token check; surfaces internal faults
		// as errors so they become 500 instead of being masked.
		AdminChecker: func(r *http.Request) (bool, error) {
			uc, err := auth.AuthenticateRequest(r, verifier, localMapper, resolver)
			if err != nil {
				if errors.Is(err, auth.ErrNoAuthHeader) ||
					errors.Is(err, auth.ErrInvalidToken) ||
					errors.Is(err, auth.ErrUserGone) {
					return false, nil
				}
				return false, err
			}
			return uc.IsAdmin, nil
		},
	})
	if err := onboarding.RefreshState(ctx); err != nil {
		slog.ErrorContext(ctx, "oidc_config: initial state load failed", "error", err)
		os.Exit(1)
	}
	// Replay DB overrides on top of the env-built registry so a prior
	// "microsoft off" confirmation sticks across restarts.
	if err := onboarding.ApplyDBOverridesToOAuth(ctx); err != nil {
		slog.ErrorContext(ctx, "oidc_config: apply DB overrides failed", "error", err)
		os.Exit(1)
	}

	// Setup-token + state-display lifecycle. TOKEN_DISPLAY=none is the
	// production-strict escape hatch — revert to Phase 9.1's fail-fast
	// if state is unconfirmed; onboarding endpoints are NOT mounted
	// regardless of whether this exits.
	if cfg.Onboarding.TokenDisplay == config.TokenDisplayNone {
		if !onboarding.CurrentState().Confirmed() {
			slog.ErrorContext(ctx, "TOKEN_DISPLAY=none and OIDC state is unconfirmed — exiting per fail-fast policy",
				"state", string(onboarding.CurrentState()),
			)
			for _, s := range oauth.AllProviders() {
				if !s.InitOK {
					slog.ErrorContext(ctx, "provider init failed",
						"provider", s.ID,
						"error", s.Err,
					)
				}
			}
			os.Exit(1)
		}
	} else {
		// Ensure the setup token is active iff the state calls for
		// it. EnsureSetupToken returns a non-empty plaintext in two
		// cases: first-boot (no prior hash) and restart-with-unconfirmed
		// (prior hash present but the plaintext was unrecoverable, so
		// the token is rotated to give ops a fresh recoverable value).
		// Both cases should trigger a fresh banner.
		plain, err := onboarding.EnsureSetupToken(ctx)
		if err != nil {
			slog.ErrorContext(ctx, "oidc_config: ensure setup token", "error", err)
			os.Exit(1)
		}
		if plain != "" {
			if cfg.Onboarding.TokenDisplay == config.TokenDisplayStderr ||
				cfg.Onboarding.TokenDisplay == config.TokenDisplayBoth {
				auth.PrintSetupTokenBanner(plain, cfg.Server.GUIBaseURL+"/oidc-config")
			}
			if cfg.Onboarding.TokenDisplay == config.TokenDisplayLocalhost {
				// Structured log only; the banner is stderr-exclusive.
				slog.ErrorContext(ctx, "oidc onboarding required: setup token ready (use /v1/oidc-config/setup-token from loopback)",
					"setup_token_required", true,
				)
			}
		}
	}

	// Initialize the field cipher for SSN/EIN encryption. Fail fast if the
	// key env var is missing or malformed — the server cannot operate without it.
	fieldCipher, err := fieldcrypto.NewFromEnv()
	if err != nil {
		slog.ErrorContext(ctx, "field cipher init failed", "error", err)
		os.Exit(1)
	}

	auditWriter := audit.New(queries)

	// Build display renderer registry. Only core builtins are registered here;
	// peer modules (tags, contacts, etc.) are composed at the application layer,
	// not from inside users-module.
	displayReg := display.NewRegistry(coredb.New(pool))
	coreservice.RegisterBuiltins(displayReg, coredb.New(pool))

	// Build core services and router. coreSvcs delegates entity CRUD to the
	// service layer; coreRouter mounts /entities/* routes (including /self).
	coreSvcs := coreservice.New(coredb.New(pool), auditWriter, fieldCipher)
	coreRouter := corehttpapi.NewRouter(corehttpapi.Deps{
		Pool:      pool,
		Services:  coreSvcs,
		Audit:     auditWriter,
		Principal: auth.CorePrincipalAdapter{},
		Logger:    logger,
	})

	// Build email sender.
	emailSender := email.NewSMTPSender(
		cfg.SMTP.Host,
		cfg.SMTP.Port,
		cfg.SMTP.From,
		cfg.SMTP.User,
		cfg.SMTP.Pass,
	)

	// Build server + router.
	srv, r := server.New(cfg)

	// Health endpoints (unauthenticated).
	r.Get("/healthz", handlers.Live)
	r.Get("/readyz", handlers.Ready(pool))

	// Local auth handlers (unauthenticated).
	authHandler := authhandlers.New(
		pool,
		queries,
		coreQueries,
		auditWriter,
		cfg.LocalAuth.JWTSecret,
		cfg.LocalAuth.LocalIssuer,
		emailSender,
		cfg.Server.GUIBaseURL,
	)

	oidcHandler := authhandlers.NewOIDCHandler(queries, oauth, resolver, cfg)

	// Handlers for authenticated routes.
	selfHandler := handlers.NewSelfHandler(queries, coreQueries, coreSvcs, auditWriter)
	usersHandler := handlers.NewUserAccountsHandler(pool, queries, coreQueries, coreSvcs, auditWriter)
	assumeHandler := handlers.NewAssumeHandler(queries, cfg.LocalAuth.JWTSecret, cfg.LocalAuth.LocalIssuer)
	auditHandler := handlers.NewAuditHandler(queries, coreQueries, coreSvcs)
	appsHandler := handlers.NewAppsHandler(queries, auditWriter)

	providersHandler := handlers.NewProvidersHandler(handlers.ProvidersDeps{
		Queries:      queries,
		EnvRegistry:  cfg.Providers,
		OAuth:        oauth,
		RedirectBase: cfg.Auth.OAuthRedirectBaseURL,
		Confirmer:    onboarding,
	})

	// Onboarding endpoints. Mounted only when TOKEN_DISPLAY != none.
	// They must be reachable even when state is unconfirmed (the whole
	// point), so they sit OUTSIDE the RequireOIDCConfirmed gate.
	if cfg.Onboarding.TokenDisplay != config.TokenDisplayNone {
		r.Route("/v1/oidc-config", func(r chi.Router) {
			r.Get("/status", onboarding.Status)
			r.Post("/confirm", onboarding.Confirm)
			r.Get("/saved", onboarding.Saved)
			// Per-provider CRUD (phase 9.11a). All writes require admin
			// OR setup token; reads require the same (no public info).
			r.Post("/providers", providersHandler.Create)
			r.Get("/providers/{id}", providersHandler.Get)
			r.Put("/providers/{id}", providersHandler.Update)
			r.Delete("/providers/{id}", providersHandler.Revert)
			if cfg.Onboarding.TokenDisplay == config.TokenDisplayLocalhost ||
				cfg.Onboarding.TokenDisplay == config.TokenDisplayBoth {
				r.Get("/setup-token", onboarding.SetupToken)
			}
		})
	}

	// Everything else on /v1 — including local + OIDC auth — is gated
	// by RequireOIDCConfirmed. When TOKEN_DISPLAY=none the middleware
	// is effectively a no-op (we already exited on unconfirmed state),
	// but attaching it unconditionally keeps behavior consistent and
	// cheap (a CurrentState() read is a single atomic pointer load).
	requireConfirmed := auth.RequireOIDCConfirmed(onboarding.CurrentState)

	r.Route("/v1/auth", func(r chi.Router) {
		r.Use(requireConfirmed)

		r.Post("/register", authHandler.Register)
		r.Post("/login", authHandler.Login)
		r.Post("/email-code/request", authHandler.EmailCodeRequest)
		r.Post("/email-code/verify", authHandler.EmailCodeVerify)
		r.Post("/password-reset/request", authHandler.PasswordResetRequest)
		r.Post("/password-reset/confirm", authHandler.PasswordResetConfirm)

		// OIDC provider discovery + authorization-code flow (unauthenticated).
		r.Get("/providers", oidcHandler.ListProviders)
		r.Get("/oidc/{provider}/start", oidcHandler.Start)
		r.Get("/oidc/{provider}/callback", oidcHandler.Callback)
	})

	r.Route("/v1", func(r chi.Router) {
		r.Use(requireConfirmed)
		r.Group(func(r chi.Router) {
			r.Use(auth.RequireAuth(verifier, localMapper, resolver))

			// /v1/self — composed identity endpoint owned by users-module
			// (uses core's EntityService.GetSelf internally for the entity portion).
			r.Get("/self", selfHandler.Get)
			r.Put("/self", selfHandler.Put)

			// Core entity CRUD: /v1/entities/natural-persons, /corporations, etc.
			r.Mount("/", coreRouter)

			// Assume identity (admin).
			r.Delete("/assume", assumeHandler.EndAssume)

			// Admin-only routes.
			r.Group(func(r chi.Router) {
				r.Use(auth.RequireAdmin)

				// User account management.
				r.Get("/user-accounts", usersHandler.List)
				r.Post("/user-accounts", usersHandler.Create)
				r.Get("/user-accounts/{uuid}", usersHandler.Get)
				r.Put("/user-accounts/{uuid}", usersHandler.Update)
				r.Delete("/user-accounts/{uuid}", usersHandler.Delete)
				r.Post("/user-accounts/{uuid}/grant-admin", usersHandler.GrantAdmin)
				r.Post("/user-accounts/{uuid}/revoke-admin", usersHandler.RevokeAdmin)
				r.Post("/user-accounts/{uuid}/assume", assumeHandler.Assume)

				// Audit log.
				r.Get("/user-accounts/{uuid}/audit", auditHandler.ByUser)
				r.Get("/audit/{entity_uuid}", auditHandler.ByEntity)

				// Apps (multi-tenancy).
				r.Post("/apps", appsHandler.Create)
				r.Get("/apps", appsHandler.List)
				r.Get("/apps/{uuid}", appsHandler.GetApp)
				r.Put("/apps/{uuid}", appsHandler.UpdateApp)
				r.Delete("/apps/{uuid}", appsHandler.DeleteApp)

				// Apps user-accounts.
				r.Post("/apps/{uuid}/user-accounts", appsHandler.AssignUser)
				r.Get("/apps/{uuid}/user-accounts", appsHandler.ListAppUsers)
				r.Delete("/apps/{uuid}/user-accounts/{user_account_uuid}", appsHandler.RemoveUser)
				r.Put("/apps/{uuid}/user-accounts/{user_account_uuid}/roles", appsHandler.UpdateUserRoles)
			})
		})
	})

	slog.InfoContext(ctx, "users-api starting", "addr", cfg.Server.Addr)

	// Start server in background.
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.ErrorContext(ctx, "server error", "error", err)
			os.Exit(1)
		}
	}()

	// Block until signal.
	<-ctx.Done()
	stop()

	slog.Info("shutdown signal received, beginning graceful shutdown")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
	defer cancel()

	// Shutdown sequence: HTTP server → pool → OTel.
	slog.Info("shutting down server")
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("server shutdown error", "error", err)
	}

	slog.Info("closing database pool")
	pool.Close()

	slog.Info("flushing otel telemetry")
	if err := otelShutdown(shutdownCtx); err != nil {
		slog.Error("otel shutdown error", "error", err)
	}

	slog.Info("shutdown complete")
}

func resolveLogLevel(level string) slog.Level {
	switch strings.ToUpper(level) {
	case "DEBUG":
		return slog.LevelDebug
	case "WARN":
		return slog.LevelWarn
	case "ERROR":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
