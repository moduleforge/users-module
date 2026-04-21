package handlers

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	coredb "github.com/moduleforge/core-model/db"
	coreservice "github.com/moduleforge/core-api/service"
	"github.com/moduleforge/users-module/api/internal/audit"
	localauth "github.com/moduleforge/users-module/api/internal/auth"
	"github.com/moduleforge/users-module/api/internal/server"
	db "github.com/moduleforge/users-module/model/db"
)

// UserAccountsHandler serves the /v1/user-accounts endpoints.
type UserAccountsHandler struct {
	pool     *pgxpool.Pool
	q        *db.Queries
	coreQ    *coredb.Queries
	coreSvcs *coreservice.Services
	audit    audit.Writer
}

// NewUsersHandler creates a UserAccountsHandler.
// Name kept as NewUsersHandler so main.go wiring doesn't change.
func NewUsersHandler(pool *pgxpool.Pool, q *db.Queries, coreQ *coredb.Queries, coreSvcs *coreservice.Services, aw audit.Writer) *UserAccountsHandler {
	return &UserAccountsHandler{pool: pool, q: q, coreQ: coreQ, coreSvcs: coreSvcs, audit: aw}
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

	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		slog.ErrorContext(r.Context(), "user_accounts.create: begin tx", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to begin transaction")
		return
	}
	defer tx.Rollback(r.Context())

	actor := localauth.MustFromContext(r.Context())
	corePrin := coreservice.Principal{
		UserID:   actor.UserAccountID, // Principal.UserID is core-owned generic field
		EntityID: actor.EntityID,
		IsAdmin:  actor.IsAdmin,
	}

	coreQtx := coredb.New(tx)

	// Delegate entity -> legal_entity -> natural_person creation to core service.
	// The tx-scoped querier keeps all inserts in the same transaction.
	_, entityUUID, err := h.coreSvcs.NaturalPerson.Create(
		r.Context(),
		coreQtx,
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

	// Resolve the entity internal ID needed for the user_accounts row foreign key.
	entity, err := coreQtx.GetEntityByUUID(r.Context(), entityUUID)
	if err != nil {
		slog.ErrorContext(r.Context(), "user_accounts.create: resolve entity", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to resolve entity")
		return
	}

	qtx := h.q.WithTx(tx)

	// account_holder references legal_entities(entity_id); entity.ID is valid
	// here because core service created the legal_entity row above (only
	// natural_persons and corporations — legal entity subtypes — can be
	// account holders; service_accounts cannot).
	ua, err := qtx.CreateUserAccount(r.Context(), db.CreateUserAccountParams{
		AccountHolder: entity.ID,
		Email:         req.Email,
		IsAdmin:       req.IsAdmin,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if userAccountsPgError(err, &pgErr) && pgErr.Code == "23505" {
			server.Error(w, http.StatusConflict, "email_taken", "an account with that email already exists")
			return
		}
		slog.ErrorContext(r.Context(), "user_accounts.create: create user account", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to create user account")
		return
	}

	if req.Password != nil {
		hash, err := localauth.HashPassword(*req.Password)
		if err != nil {
			slog.ErrorContext(r.Context(), "user_accounts.create: hash password", "error", err)
			server.Error(w, http.StatusInternalServerError, "internal_error", "failed to process password")
			return
		}
		if err := qtx.UpsertAuthLocal(r.Context(), db.UpsertAuthLocalParams{
			UserAccountID: ua.ID,
			PasswordHash:  hash,
		}); err != nil {
			slog.ErrorContext(r.Context(), "user_accounts.create: upsert auth_local", "error", err)
			server.Error(w, http.StatusInternalServerError, "internal_error", "failed to save credentials")
			return
		}
	}

	if err := tx.Commit(r.Context()); err != nil {
		slog.ErrorContext(r.Context(), "user_accounts.create: commit", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to commit transaction")
		return
	}

	accountHolder := ua.AccountHolder
	_ = h.audit.Write(r.Context(), "create", "user_account", &accountHolder, nil, map[string]any{
		"uuid":     ua.Uuid.String(),
		"email":    ua.Email,
		"is_admin": ua.IsAdmin,
	})

	server.JSON(w, http.StatusCreated, userAccountResponse(ua))
}

// List handles GET /v1/user-accounts (admin).
func (h *UserAccountsHandler) List(w http.ResponseWriter, r *http.Request) {
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

	var req updateUserAccountRequest
	if err := server.Decode(r, &req); err != nil {
		server.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}

	before := userAccountResponse(ua)

	newEmail := ua.Email
	if req.Email != nil {
		newEmail = strings.TrimSpace(strings.ToLower(*req.Email))
	}

	if err := h.q.UpdateUserAccount(r.Context(), db.UpdateUserAccountParams{
		ID:              ua.ID,
		Email:           newEmail,
		EmailVerifiedAt: ua.EmailVerifiedAt,
		AuthIssuer:      ua.AuthIssuer,
		AuthID:          ua.AuthID,
	}); err != nil {
		slog.ErrorContext(r.Context(), "user_accounts.update: update user account", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to update user account")
		return
	}

	if req.IsAdmin != nil {
		if err := h.q.SetAdmin(r.Context(), db.SetAdminParams{
			ID:      ua.ID,
			IsAdmin: *req.IsAdmin,
		}); err != nil {
			slog.ErrorContext(r.Context(), "user_accounts.update: set admin", "error", err)
		}
	}

	// Update natural person fields. account_holder = entity_id.
	if req.GivenName != nil || req.FamilyName != nil {
		if np, err := h.coreQ.GetNaturalPersonByEntityID(r.Context(), ua.AccountHolder); err == nil {
			gn := np.GivenName
			fn := np.FamilyName
			if req.GivenName != nil {
				gn = pgtype.Text{String: *req.GivenName, Valid: true}
			}
			if req.FamilyName != nil {
				fn = pgtype.Text{String: *req.FamilyName, Valid: true}
			}
			_ = h.coreQ.UpdateNaturalPerson(r.Context(), coredb.UpdateNaturalPersonParams{
				EntityID:   ua.AccountHolder,
				GivenName:  gn,
				FamilyName: fn,
			})
		}
	}

	// Reload for after snapshot.
	updated, err := h.q.GetUserAccountByID(r.Context(), ua.ID)
	if err != nil {
		slog.ErrorContext(r.Context(), "user_accounts.update: reload user account", "error", err)
	}
	after := userAccountResponse(updated)

	accountHolder := ua.AccountHolder
	_ = h.audit.Write(r.Context(), "update", "user_account", &accountHolder, before, after)

	server.JSON(w, http.StatusOK, after)
}

// Delete handles DELETE /v1/user-accounts/{uuid} (admin) — soft-deletes by archiving the entity.
func (h *UserAccountsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	ua, ok := h.loadUserAccountByUUIDParam(w, r)
	if !ok {
		return
	}

	// Fetch entity UUID for archive. account_holder is the entity_id.
	entity, err := h.coreQ.GetEntityByUUID(r.Context(), ua.Uuid)
	if err != nil {
		var entityUUID uuid.UUID
		if err2 := h.pool.QueryRow(r.Context(), "SELECT uuid FROM entities WHERE id = $1", ua.AccountHolder).Scan(&entityUUID); err2 != nil {
			slog.ErrorContext(r.Context(), "user_accounts.delete: get entity uuid", "error", err2)
			server.Error(w, http.StatusInternalServerError, "internal_error", "failed to find entity")
			return
		}
		entity.Uuid = entityUUID
	}

	if err := h.coreQ.ArchiveEntity(r.Context(), entity.Uuid); err != nil {
		slog.ErrorContext(r.Context(), "user_accounts.delete: archive entity", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to archive user account")
		return
	}

	accountHolder := ua.AccountHolder
	_ = h.audit.Write(r.Context(), "delete", "user_account", &accountHolder, userAccountResponse(ua), nil)

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

	if err := h.q.SetAdmin(r.Context(), db.SetAdminParams{
		ID:      ua.ID,
		IsAdmin: isAdmin,
	}); err != nil {
		slog.ErrorContext(r.Context(), "user_accounts.setAdmin", "error", err, "op", op)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to update admin status")
		return
	}

	accountHolder := ua.AccountHolder
	_ = h.audit.Write(r.Context(), op, "user_account", &accountHolder,
		map[string]any{"is_admin": !isAdmin},
		map[string]any{"is_admin": isAdmin},
	)

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
