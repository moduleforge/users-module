// Package auth provides HTTP handlers for local authentication flows.
package auth

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	coredb "github.com/moduleforge/core-model/db"
	localauth "github.com/moduleforge/users-module/api/internal/auth"
	"github.com/moduleforge/users-module/api/internal/server"
	db "github.com/moduleforge/users-module/model/db"
)

// Sender is the email-sending interface expected by this package.
// It matches email.Sender to avoid a concrete import dependency.
type Sender interface {
	Send(ctx context.Context, to, subject, textBody string) error
}

// Handler bundles dependencies for the local auth HTTP handlers.
type Handler struct {
	pool      *pgxpool.Pool
	queries   *db.Queries
	coreQ     *coredb.Queries
	jwtSecret string
	issuer    string
	sender    Sender
	guiBase   string
}

// New constructs a Handler.
func New(pool *pgxpool.Pool, queries *db.Queries, coreQ *coredb.Queries, jwtSecret, issuer string, sender Sender, guiBase string) *Handler {
	return &Handler{
		pool:      pool,
		queries:   queries,
		coreQ:     coreQ,
		jwtSecret: jwtSecret,
		issuer:    issuer,
		sender:    sender,
		guiBase:   guiBase,
	}
}

// registerRequest is the body for POST /v1/auth/register.
type registerRequest struct {
	Email      string `json:"email"`
	Password   string `json:"password"`
	GivenName  string `json:"given_name"`
	FamilyName string `json:"family_name"`
}

// Register handles POST /v1/auth/register.
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := server.Decode(r, &req); err != nil {
		server.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}

	req.Email = strings.TrimSpace(strings.ToLower(req.Email))

	if req.Email == "" {
		server.Error(w, http.StatusBadRequest, "validation_error", "email is required")
		return
	}
	if len(req.Password) < 12 {
		server.Error(w, http.StatusBadRequest, "validation_error", "password must be at least 12 characters")
		return
	}
	if strings.TrimSpace(req.GivenName) == "" {
		server.Error(w, http.StatusBadRequest, "validation_error", "given_name is required")
		return
	}
	if strings.TrimSpace(req.FamilyName) == "" {
		server.Error(w, http.StatusBadRequest, "validation_error", "family_name is required")
		return
	}

	hash, err := localauth.HashPassword(req.Password)
	if err != nil {
		slog.ErrorContext(r.Context(), "register: hash password", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to process password")
		return
	}

	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		slog.ErrorContext(r.Context(), "register: begin tx", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to begin transaction")
		return
	}
	defer tx.Rollback(r.Context())

	qtx := h.queries.WithTx(tx)
	coreQtx := h.coreQ.WithTx(tx)

	// Determine first-user bootstrap.
	var userAccountCount int64
	if err := tx.QueryRow(r.Context(), "SELECT count(*) FROM user_accounts").Scan(&userAccountCount); err != nil {
		slog.ErrorContext(r.Context(), "register: count user_accounts", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to check user count")
		return
	}
	isFirst := userAccountCount == 0

	// Resolve the natural_person type ID from the types registry.
	npType, err := coreQtx.GetTypeBySlug(r.Context(), "natural_person")
	if err != nil {
		slog.ErrorContext(r.Context(), "register: resolve natural_person type", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to resolve entity type")
		return
	}

	entity, err := coreQtx.CreateEntity(r.Context(), npType.ID)
	if err != nil {
		slog.ErrorContext(r.Context(), "register: create entity", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to create entity")
		return
	}

	// Create legal entity (pure FK anchor — no kind/display_name).
	_, err = coreQtx.CreateLegalEntity(r.Context(), entity.ID)
	if err != nil {
		slog.ErrorContext(r.Context(), "register: create legal entity", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to create legal entity")
		return
	}

	_, err = coreQtx.CreateNaturalPerson(r.Context(), coredb.CreateNaturalPersonParams{
		EntityID:   entity.ID,
		GivenName:  pgtype.Text{String: req.GivenName, Valid: true},
		FamilyName: pgtype.Text{String: req.FamilyName, Valid: true},
	})
	if err != nil {
		slog.ErrorContext(r.Context(), "register: create natural person", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to create natural person")
		return
	}

	// account_holder references legal_entities(entity_id); entity.ID is valid
	// because we just created the legal_entity row above.
	ua, err := qtx.CreateUserAccount(r.Context(), db.CreateUserAccountParams{
		AccountHolder: entity.ID,
		Email:         req.Email,
		IsAdmin:       isFirst,
	})
	if err != nil {
		// Check for unique violation on email.
		var pgErr *pgconn.PgError
		if isPgError(err, &pgErr) && pgErr.Code == "23505" {
			server.Error(w, http.StatusConflict, "email_taken", "an account with that email already exists")
			return
		}
		slog.ErrorContext(r.Context(), "register: create user account", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to create user account")
		return
	}

	if err := qtx.UpsertAuthLocal(r.Context(), db.UpsertAuthLocalParams{
		UserAccountID: ua.ID,
		PasswordHash:  hash,
	}); err != nil {
		slog.ErrorContext(r.Context(), "register: upsert auth_local", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to save credentials")
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		slog.ErrorContext(r.Context(), "register: commit", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to commit transaction")
		return
	}

	slog.InfoContext(r.Context(), "user registered", "user_account_uuid", ua.Uuid.String(), "email", ua.Email)

	server.JSON(w, http.StatusCreated, map[string]any{
		"uuid":  ua.Uuid.String(),
		"email": ua.Email,
	})
}

// isPgError tests whether err is a *pgconn.PgError and stores it in target.
func isPgError(err error, target **pgconn.PgError) bool {
	if pgErr, ok := err.(*pgconn.PgError); ok {
		*target = pgErr
		return true
	}
	return false
}
