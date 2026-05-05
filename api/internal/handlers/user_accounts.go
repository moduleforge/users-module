package handlers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	coredb "github.com/moduleforge/core-model/db"
	coreAuthz "github.com/moduleforge/core-api/authz"
	"github.com/moduleforge/core-api/entity"
	"github.com/moduleforge/core-api/observer"
	"github.com/moduleforge/core-api/txhelper"
	coreservice "github.com/moduleforge/core-api/service"
	localauth "github.com/moduleforge/users-module/api/internal/auth"
	localAuthz "github.com/moduleforge/users-module/api/internal/authz"
	"github.com/moduleforge/users-module/api/internal/server"
	db "github.com/moduleforge/users-module/model/db"
)

// userAccountEntity is a minimal entity.Entity stub for user_account resources.
// The EntityID is the entity_id (account_holder) of the user account, not the
// user_account.id.
type userAccountEntity struct {
	id *int64
}

func (e userAccountEntity) Resource() string { return "user_account" }
func (e userAccountEntity) EntityID() *int64 { return e.id }

// Compile-time: userAccountEntity satisfies entity.Entity.
var _ entity.Entity = userAccountEntity{}

// UserAccountsHandler serves the /v1/user-accounts endpoints.
type UserAccountsHandler struct {
	pool       txhelper.DB
	q          db.Querier
	newQuerier func(pgx.Tx) db.Querier // factory for tx-scoped querier; defaults to db.New
	coreQ      *coredb.Queries
	coreSvcs   *coreservice.Services
	az         coreAuthz.Authorizer
	observers  *observer.ObserverGroup
}

// NewUserAccountsHandler creates a UserAccountsHandler.
func NewUserAccountsHandler(
	pool txhelper.DB,
	q *db.Queries,
	coreQ *coredb.Queries,
	coreSvcs *coreservice.Services,
	az coreAuthz.Authorizer,
	observers *observer.ObserverGroup,
) *UserAccountsHandler {
	return &UserAccountsHandler{
		pool:       pool,
		q:          q,
		newQuerier: func(tx pgx.Tx) db.Querier { return db.New(tx) },
		coreQ:      coreQ,
		coreSvcs:   coreSvcs,
		az:         az,
		observers:  observers,
	}
}

// writeAuthzError maps an authz error to the appropriate HTTP status.
// ErrUnauthenticated → 401; anything else (including ErrForbidden) → 403.
func writeAuthzError(w http.ResponseWriter, err error) {
	if errors.Is(err, localAuthz.ErrUnauthenticated) {
		server.Error(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	server.Error(w, http.StatusForbidden, "forbidden", "access denied")
}

// createUserAccountRequest is the body for POST /v1/user-accounts (admin).
type createUserAccountRequest struct {
	Email      string  `json:"email"`
	Password   *string `json:"password"`
	GivenName  string  `json:"given_name"`
	FamilyName string  `json:"family_name"`
	IsAdmin    bool    `json:"is_admin"`
}

// Create handles POST /v1/user-accounts (admin).
//
// The creation is two-phase because the core NaturalPerson.Create service opens
// its own transaction. Phase 1: create entity/legal_entity/natural_person via core
// service (its own tx). Phase 2: create user_account row in a separate tx that also
// writes the audit event.
func (h *UserAccountsHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req createUserAccountRequest
	if err := server.Decode(r, &req); err != nil {
		server.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}

	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	if req.Email == "" {
		server.Error(w, http.StatusBadRequest, "validation_error", "email is required")
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
	if req.Password != nil && len(*req.Password) < 12 {
		server.Error(w, http.StatusBadRequest, "validation_error", "password must be at least 12 characters")
		return
	}

	// 1. Authorize: create is admin-only; target has no entity ID yet.
	if err := h.az.Authorize(r.Context(), "create", userAccountEntity{}); err != nil {
		writeAuthzError(w, err)
		return
	}

	actor := localauth.MustFromContext(r.Context())
	corePrin := coreservice.Principal{
		UserID:   actor.UserAccountID,
		EntityID: actor.EntityID,
		IsAdmin:  actor.IsAdmin,
	}

	// Phase 1: create the entity chain (core service opens its own tx).
	_, entityUUID, err := h.coreSvcs.NaturalPerson.Create(
		r.Context(),
		h.coreQ,
		corePrin,
		coreservice.CreateNaturalPersonInput{
			GivenName:  req.GivenName,
			FamilyName: req.FamilyName,
		},
	)
	if err != nil {
		slog.ErrorContext(r.Context(), "user_accounts.create: create natural person chain", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to create entity")
		return
	}

	// Resolve the entity internal ID needed for the user_accounts FK.
	entity, err := h.coreQ.GetEntityByUUID(r.Context(), entityUUID)
	if err != nil {
		slog.ErrorContext(r.Context(), "user_accounts.create: resolve entity", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to resolve entity")
		return
	}
	entityID := entity.ID

	// Phase 2: create user_account + optional auth_local, emit audit event — all in one tx.
	var ua db.UserAccount
	txErr := txhelper.Run(r.Context(), h.pool, func(ctx context.Context, tx pgx.Tx) error {
		qtx := h.newQuerier(tx)

		var err error
		ua, err = qtx.CreateUserAccount(ctx, db.CreateUserAccountParams{
			AccountHolder: entityID,
			Email:         req.Email,
			IsAdmin:       req.IsAdmin,
		})
		if err != nil {
			return err
		}

		if req.Password != nil {
			hash, err := localauth.HashPassword(*req.Password)
			if err != nil {
				return err
			}
			if err := qtx.UpsertAuthLocal(ctx, db.UpsertAuthLocalParams{
				UserAccountID: ua.ID,
				PasswordHash:  hash,
			}); err != nil {
				return err
			}
		}

		after := map[string]any{
			"uuid":     ua.Uuid.String(),
			"email":    ua.Email,
			"is_admin": ua.IsAdmin,
		}
		return h.observers.Observe(ctx, tx, "create", "user_account", &entityID, nil, after)
	})
	if txErr != nil {
		var pgErr *pgconn.PgError
		if userAccountsPgError(txErr, &pgErr) && pgErr.Code == "23505" {
			server.Error(w, http.StatusConflict, "email_taken", "an account with that email already exists")
			return
		}
		slog.ErrorContext(r.Context(), "user_accounts.create: tx", "error", txErr)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to create user account")
		return
	}

	// Post-commit observe.
	after := map[string]any{
		"uuid":     ua.Uuid.String(),
		"email":    ua.Email,
		"is_admin": ua.IsAdmin,
	}
	h.observers.ObserveAfterCommit(r.Context(), "create", "user_account", &entityID, nil, after)

	server.JSON(w, http.StatusCreated, userAccountResponse(ua))
}

// List handles GET /v1/user-accounts (admin).
func (h *UserAccountsHandler) List(w http.ResponseWriter, r *http.Request) {
	// Authorize: list is admin-only; no target entity ID.
	if err := h.az.Authorize(r.Context(), "list", userAccountEntity{}); err != nil {
		writeAuthzError(w, err)
		return
	}

	q := r.URL.Query()
	search := q.Get("q")
	if email := q.Get("email"); email != "" && search == "" {
		search = email
	}

	limit := int32(20)
	offset := int32(0)
	if l := q.Get("limit"); l != "" {
		v, err := strconv.ParseInt(l, 10, 32)
		if err == nil && v > 0 && v <= 200 {
			limit = int32(v)
		}
	}
	if o := q.Get("offset"); o != "" {
		v, err := strconv.ParseInt(o, 10, 32)
		if err == nil && v >= 0 {
			offset = int32(v)
		}
	}

	accounts, err := h.q.SearchUserAccounts(r.Context(), db.SearchUserAccountsParams{
		Column1: search,
		Limit:   limit,
		Offset:  offset,
	})
	if err != nil {
		slog.ErrorContext(r.Context(), "user_accounts.list: search", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to list user accounts")
		return
	}

	resp := make([]map[string]any, 0, len(accounts))
	for _, ua := range accounts {
		resp = append(resp, userAccountResponse(ua))
	}

	server.JSON(w, http.StatusOK, map[string]any{
		"user_accounts": resp,
		"total":         len(resp),
	})
}

// Get handles GET /v1/user-accounts/{uuid} (admin).
func (h *UserAccountsHandler) Get(w http.ResponseWriter, r *http.Request) {
	ua, ok := h.loadUserAccountByUUIDParam(w, r)
	if !ok {
		return
	}

	// Authorize: read — use account_holder as the entity ID.
	eid := ua.AccountHolder
	if err := h.az.Authorize(r.Context(), "read", userAccountEntity{id: &eid}); err != nil {
		writeAuthzError(w, err)
		return
	}

	// Enrich with entity info. account_holder = entity_id.
	detail := userAccountResponse(ua)
	if np, err := h.coreQ.GetNaturalPersonByEntityID(r.Context(), ua.AccountHolder); err == nil {
		detail["given_name"] = np.GivenName.String
		detail["family_name"] = np.FamilyName.String
		detail["entity_kind"] = "natural_person"
		detail["display_name"] = strings.TrimSpace(np.GivenName.String + " " + np.FamilyName.String)
	}

	server.JSON(w, http.StatusOK, detail)
}

// updateUserAccountRequest is the body for PUT /v1/user-accounts/{uuid} (admin).
type updateUserAccountRequest struct {
	Email      *string `json:"email"`
	GivenName  *string `json:"given_name"`
	FamilyName *string `json:"family_name"`
	IsAdmin    *bool   `json:"is_admin"`
}

// Update handles PUT /v1/user-accounts/{uuid} (admin).
func (h *UserAccountsHandler) Update(w http.ResponseWriter, r *http.Request) {
	ua, ok := h.loadUserAccountByUUIDParam(w, r)
	if !ok {
		return
	}

	// Authorize: update — use account_holder as the entity ID.
	eid := ua.AccountHolder
	if err := h.az.Authorize(r.Context(), "update", userAccountEntity{id: &eid}); err != nil {
		writeAuthzError(w, err)
		return
	}

	var req updateUserAccountRequest
	if err := server.Decode(r, &req); err != nil {
		server.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}

	// Snapshot before state.
	before := map[string]any{
		"uuid":     ua.Uuid.String(),
		"email":    ua.Email,
		"is_admin": ua.IsAdmin,
	}

	newEmail := ua.Email
	if req.Email != nil {
		newEmail = strings.TrimSpace(strings.ToLower(*req.Email))
	}

	var updated db.UserAccount
	txErr := txhelper.Run(r.Context(), h.pool, func(ctx context.Context, tx pgx.Tx) error {
		qtx := h.newQuerier(tx)

		if err := qtx.UpdateUserAccount(ctx, db.UpdateUserAccountParams{
			ID:              ua.ID,
			Email:           newEmail,
			EmailVerifiedAt: ua.EmailVerifiedAt,
			AuthIssuer:      ua.AuthIssuer,
			AuthID:          ua.AuthID,
		}); err != nil {
			return err
		}

		if req.IsAdmin != nil {
			if err := qtx.SetAdmin(ctx, db.SetAdminParams{
				ID:      ua.ID,
				IsAdmin: *req.IsAdmin,
			}); err != nil {
				slog.ErrorContext(ctx, "user_accounts.update: set admin", "error", err)
			}
		}

		// Update natural person fields via core queries (in the same tx).
		if req.GivenName != nil || req.FamilyName != nil {
			coreQtx := coredb.New(tx)
			if np, err := coreQtx.GetNaturalPersonByEntityID(ctx, ua.AccountHolder); err == nil {
				gn := np.GivenName
				fn := np.FamilyName
				if req.GivenName != nil {
					gn = pgtype.Text{String: *req.GivenName, Valid: true}
				}
				if req.FamilyName != nil {
					fn = pgtype.Text{String: *req.FamilyName, Valid: true}
				}
				_ = coreQtx.UpdateNaturalPerson(ctx, coredb.UpdateNaturalPersonParams{
					EntityID:   ua.AccountHolder,
					GivenName:  gn,
					FamilyName: fn,
				})
			}
		}

		// Reload for after snapshot.
		var err error
		updated, err = qtx.GetUserAccountByID(ctx, ua.ID)
		if err != nil {
			slog.ErrorContext(ctx, "user_accounts.update: reload user account", "error", err)
			// Best-effort: use pre-mutation data as the after snapshot.
			updated = ua
			updated.Email = newEmail
		}

		after := map[string]any{
			"uuid":     updated.Uuid.String(),
			"email":    updated.Email,
			"is_admin": updated.IsAdmin,
		}
		return h.observers.Observe(ctx, tx, "update", "user_account", &eid, before, after)
	})
	if txErr != nil {
		slog.ErrorContext(r.Context(), "user_accounts.update: tx", "error", txErr)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to update user account")
		return
	}

	// Post-commit observe.
	after := map[string]any{
		"uuid":     updated.Uuid.String(),
		"email":    updated.Email,
		"is_admin": updated.IsAdmin,
	}
	h.observers.ObserveAfterCommit(r.Context(), "update", "user_account", &eid, nil, after)

	server.JSON(w, http.StatusOK, userAccountResponse(updated))
}

// Delete handles DELETE /v1/user-accounts/{uuid} (admin) — soft-deletes by archiving the entity.
func (h *UserAccountsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	ua, ok := h.loadUserAccountByUUIDParam(w, r)
	if !ok {
		return
	}

	// Authorize: delete — use account_holder as the entity ID.
	eid := ua.AccountHolder
	if err := h.az.Authorize(r.Context(), "delete", userAccountEntity{id: &eid}); err != nil {
		writeAuthzError(w, err)
		return
	}

	// Snapshot before state.
	before := map[string]any{
		"uuid":     ua.Uuid.String(),
		"email":    ua.Email,
		"is_admin": ua.IsAdmin,
	}

	txErr := txhelper.Run(r.Context(), h.pool, func(ctx context.Context, tx pgx.Tx) error {
		coreQtx := coredb.New(tx)

		entityRow, err := coreQtx.GetEntityByID(ctx, ua.AccountHolder)
		if err != nil {
			return err
		}

		if err := coreQtx.ArchiveEntity(ctx, entityRow.Uuid); err != nil {
			return err
		}

		return h.observers.Observe(ctx, tx, "delete", "user_account", &eid, before, nil)
	})
	if txErr != nil {
		slog.ErrorContext(r.Context(), "user_accounts.delete: tx", "error", txErr)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to archive user account")
		return
	}

	h.observers.ObserveAfterCommit(r.Context(), "delete", "user_account", &eid, before, nil)
	w.WriteHeader(http.StatusNoContent)
}

// GrantAdmin handles POST /v1/user-accounts/{uuid}/grant-admin (admin).
func (h *UserAccountsHandler) GrantAdmin(w http.ResponseWriter, r *http.Request) {
	h.setAdmin(w, r, true, "grant")
}

// RevokeAdmin handles POST /v1/user-accounts/{uuid}/revoke-admin (admin).
func (h *UserAccountsHandler) RevokeAdmin(w http.ResponseWriter, r *http.Request) {
	h.setAdmin(w, r, false, "revoke")
}

func (h *UserAccountsHandler) setAdmin(w http.ResponseWriter, r *http.Request, isAdmin bool, op string) {
	ua, ok := h.loadUserAccountByUUIDParam(w, r)
	if !ok {
		return
	}

	// Authorize: grant/revoke — admin action on a target entity.
	eid := ua.AccountHolder
	if err := h.az.Authorize(r.Context(), op, userAccountEntity{id: &eid}); err != nil {
		writeAuthzError(w, err)
		return
	}

	before := map[string]any{"is_admin": !isAdmin}
	after := map[string]any{"is_admin": isAdmin}

	txErr := txhelper.Run(r.Context(), h.pool, func(ctx context.Context, tx pgx.Tx) error {
		qtx := h.newQuerier(tx)
		if err := qtx.SetAdmin(ctx, db.SetAdminParams{
			ID:      ua.ID,
			IsAdmin: isAdmin,
		}); err != nil {
			return err
		}
		return h.observers.Observe(ctx, tx, op, "user_account", &eid, before, after)
	})
	if txErr != nil {
		slog.ErrorContext(r.Context(), "user_accounts.setAdmin", "error", txErr, "op", op)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to update admin status")
		return
	}

	h.observers.ObserveAfterCommit(r.Context(), op, "user_account", &eid, before, after)

	server.JSON(w, http.StatusOK, map[string]any{
		"uuid":     ua.Uuid.String(),
		"is_admin": isAdmin,
	})
}

// loadUserAccountByUUIDParam extracts the {uuid} chi param and loads the user account.
func (h *UserAccountsHandler) loadUserAccountByUUIDParam(w http.ResponseWriter, r *http.Request) (db.UserAccount, bool) {
	rawUUID := chi.URLParam(r, "uuid")
	parsed, err := uuid.Parse(rawUUID)
	if err != nil {
		server.Error(w, http.StatusBadRequest, "bad_request", "invalid uuid")
		return db.UserAccount{}, false
	}
	ua, err := h.q.GetUserAccountByUUID(r.Context(), parsed)
	if err == pgx.ErrNoRows {
		server.Error(w, http.StatusNotFound, "not_found", "user account not found")
		return db.UserAccount{}, false
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "user_accounts: load by uuid", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to load user account")
		return db.UserAccount{}, false
	}
	return ua, true
}

// userAccountResponse builds a public-facing map from a db.UserAccount.
func userAccountResponse(ua db.UserAccount) map[string]any {
	return map[string]any{
		"uuid":           ua.Uuid.String(),
		"email":          ua.Email,
		"is_admin":       ua.IsAdmin,
		"email_verified": ua.EmailVerifiedAt != nil,
		"created_at":     ua.CreatedAt.Time,
	}
}

// userAccountsPgError tests whether err is a *pgconn.PgError.
func userAccountsPgError(err error, target **pgconn.PgError) bool {
	if pgErr, ok := err.(*pgconn.PgError); ok {
		*target = pgErr
		return true
	}
	return false
}
