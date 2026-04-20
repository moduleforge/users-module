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
	"github.com/moduleforge/users-module/api/internal/audit"
	localauth "github.com/moduleforge/users-module/api/internal/auth"
	"github.com/moduleforge/users-module/api/internal/server"
	db "github.com/moduleforge/users-module/model/db"
	coreservice "github.com/moduleforge/core-api/service"
)

// UsersHandler serves the /v1/users endpoints.
type UsersHandler struct {
	pool     *pgxpool.Pool
	q        *db.Queries
	coreQ    *coredb.Queries
	coreSvcs *coreservice.Services
	audit    audit.Writer
}

// NewUsersHandler creates a UsersHandler.
func NewUsersHandler(pool *pgxpool.Pool, q *db.Queries, coreQ *coredb.Queries, coreSvcs *coreservice.Services, aw audit.Writer) *UsersHandler {
	return &UsersHandler{pool: pool, q: q, coreQ: coreQ, coreSvcs: coreSvcs, audit: aw}
}

// createUserRequest is the body for POST /v1/users (admin).
type createUserRequest struct {
	Email      string  `json:"email"`
	Password   *string `json:"password"`
	GivenName  string  `json:"given_name"`
	FamilyName string  `json:"family_name"`
	IsAdmin    bool    `json:"is_admin"`
}

// Create handles POST /v1/users (admin).
func (h *UsersHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req createUserRequest
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
		slog.ErrorContext(r.Context(), "users.create: begin tx", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to begin transaction")
		return
	}
	defer tx.Rollback(r.Context())

	actor := localauth.MustFromContext(r.Context())
	corePrin := coreservice.Principal{
		UserID:   actor.UserID,
		EntityID: actor.EntityID,
		IsAdmin:  actor.IsAdmin,
	}

	coreQtx := coredb.New(tx)

	// Delegate entity → legal_entity → natural_person creation to core service.
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
		slog.ErrorContext(r.Context(), "users.create: create natural person chain", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to create entity")
		return
	}

	// Resolve the entity internal ID needed for the users row foreign key.
	entity, err := coreQtx.GetEntityByUUID(r.Context(), entityUUID)
	if err != nil {
		slog.ErrorContext(r.Context(), "users.create: resolve entity", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to resolve entity")
		return
	}

	qtx := h.q.WithTx(tx)

	user, err := qtx.CreateUser(r.Context(), db.CreateUserParams{
		EntityID: entity.ID,
		Email:    req.Email,
		IsAdmin:  req.IsAdmin,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if usersPgError(err, &pgErr) && pgErr.Code == "23505" {
			server.Error(w, http.StatusConflict, "email_taken", "an account with that email already exists")
			return
		}
		slog.ErrorContext(r.Context(), "users.create: create user", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to create user")
		return
	}

	if req.Password != nil {
		hash, err := localauth.HashPassword(*req.Password)
		if err != nil {
			slog.ErrorContext(r.Context(), "users.create: hash password", "error", err)
			server.Error(w, http.StatusInternalServerError, "internal_error", "failed to process password")
			return
		}
		if err := qtx.UpsertAuthLocal(r.Context(), db.UpsertAuthLocalParams{
			UserID:       user.ID,
			PasswordHash: hash,
		}); err != nil {
			slog.ErrorContext(r.Context(), "users.create: upsert auth_local", "error", err)
			server.Error(w, http.StatusInternalServerError, "internal_error", "failed to save credentials")
			return
		}
	}

	if err := tx.Commit(r.Context()); err != nil {
		slog.ErrorContext(r.Context(), "users.create: commit", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to commit transaction")
		return
	}

	entityID := user.EntityID
	_ = h.audit.Write(r.Context(), "create", "users", &entityID, nil, map[string]any{
		"uuid":     user.Uuid.String(),
		"email":    user.Email,
		"is_admin": user.IsAdmin,
	})

	server.JSON(w, http.StatusCreated, userResponse(user))
}

// List handles GET /v1/users (admin).
func (h *UsersHandler) List(w http.ResponseWriter, r *http.Request) {
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

	users, err := h.q.SearchUsers(r.Context(), db.SearchUsersParams{
		Column1: search,
		Limit:   limit,
		Offset:  offset,
	})
	if err != nil {
		slog.ErrorContext(r.Context(), "users.list: search", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to list users")
		return
	}

	resp := make([]map[string]any, 0, len(users))
	for _, u := range users {
		resp = append(resp, userResponse(u))
	}

	server.JSON(w, http.StatusOK, map[string]any{
		"users": resp,
		"total": len(resp),
	})
}

// Get handles GET /v1/users/{uuid} (admin).
func (h *UsersHandler) Get(w http.ResponseWriter, r *http.Request) {
	user, ok := h.loadUserByUUIDParam(w, r)
	if !ok {
		return
	}

	// Enrich with entity info.
	detail := userResponse(user)
	if le, err := h.coreQ.GetLegalEntityByEntityID(r.Context(), user.EntityID); err == nil {
		detail["display_name"] = le.DisplayName
		detail["entity_kind"] = le.Kind
		if le.Kind == "natural_person" {
			if np, err := h.coreQ.GetNaturalPersonByLegalEntityID(r.Context(), le.ID); err == nil {
				detail["given_name"] = np.GivenName.String
				detail["family_name"] = np.FamilyName.String
			}
		}
	}

	server.JSON(w, http.StatusOK, detail)
}

// updateUserRequest is the body for PUT /v1/users/{uuid} (admin).
type updateUserRequest struct {
	Email      *string `json:"email"`
	GivenName  *string `json:"given_name"`
	FamilyName *string `json:"family_name"`
	IsAdmin    *bool   `json:"is_admin"`
}

// Update handles PUT /v1/users/{uuid} (admin).
func (h *UsersHandler) Update(w http.ResponseWriter, r *http.Request) {
	user, ok := h.loadUserByUUIDParam(w, r)
	if !ok {
		return
	}

	var req updateUserRequest
	if err := server.Decode(r, &req); err != nil {
		server.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}

	before := userResponse(user)

	// Update email on user record.
	newEmail := user.Email
	if req.Email != nil {
		newEmail = strings.TrimSpace(strings.ToLower(*req.Email))
	}

	if err := h.q.UpdateUser(r.Context(), db.UpdateUserParams{
		ID:              user.ID,
		Email:           newEmail,
		EmailVerifiedAt: user.EmailVerifiedAt,
		AuthIssuer:      user.AuthIssuer,
		AuthID:          user.AuthID,
	}); err != nil {
		slog.ErrorContext(r.Context(), "users.update: update user", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to update user")
		return
	}

	// Update admin flag.
	if req.IsAdmin != nil {
		if err := h.q.SetAdmin(r.Context(), db.SetAdminParams{
			ID:      user.ID,
			IsAdmin: *req.IsAdmin,
		}); err != nil {
			slog.ErrorContext(r.Context(), "users.update: set admin", "error", err)
		}
	}

	// Update natural person fields.
	if req.GivenName != nil || req.FamilyName != nil {
		if le, err := h.coreQ.GetLegalEntityByEntityID(r.Context(), user.EntityID); err == nil && le.Kind == "natural_person" {
			if np, err := h.coreQ.GetNaturalPersonByLegalEntityID(r.Context(), le.ID); err == nil {
				gn := np.GivenName
				fn := np.FamilyName
				if req.GivenName != nil {
					gn = pgtype.Text{String: *req.GivenName, Valid: true}
				}
				if req.FamilyName != nil {
					fn = pgtype.Text{String: *req.FamilyName, Valid: true}
				}
				_ = h.coreQ.UpdateNaturalPerson(r.Context(), coredb.UpdateNaturalPersonParams{
					LegalEntityID: le.ID,
					GivenName:     gn,
					FamilyName:    fn,
				})
			}
		}
	}

	// Reload for after snapshot.
	updated, err := h.q.GetUserByID(r.Context(), user.ID)
	if err != nil {
		slog.ErrorContext(r.Context(), "users.update: reload user", "error", err)
	}
	after := userResponse(updated)

	entityID := user.EntityID
	_ = h.audit.Write(r.Context(), "update", "users", &entityID, before, after)

	server.JSON(w, http.StatusOK, after)
}

// Delete handles DELETE /v1/users/{uuid} (admin) — soft-deletes by archiving the entity.
func (h *UsersHandler) Delete(w http.ResponseWriter, r *http.Request) {
	user, ok := h.loadUserByUUIDParam(w, r)
	if !ok {
		return
	}

	// Fetch entity UUID for archive.
	entity, err := h.coreQ.GetEntityByUUID(r.Context(), user.Uuid)
	if err != nil {
		// entity UUID is on entity row, not user. Use a raw lookup by entity ID.
		// We need entity.uuid — use a pool query.
		var entityUUID uuid.UUID
		if err2 := h.pool.QueryRow(r.Context(), "SELECT uuid FROM entities WHERE id = $1", user.EntityID).Scan(&entityUUID); err2 != nil {
			slog.ErrorContext(r.Context(), "users.delete: get entity uuid", "error", err2)
			server.Error(w, http.StatusInternalServerError, "internal_error", "failed to find entity")
			return
		}
		entity.Uuid = entityUUID
	}

	if err := h.coreQ.ArchiveEntity(r.Context(), entity.Uuid); err != nil {
		slog.ErrorContext(r.Context(), "users.delete: archive entity", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to archive user")
		return
	}

	entityID := user.EntityID
	_ = h.audit.Write(r.Context(), "delete", "users", &entityID, userResponse(user), nil)

	w.WriteHeader(http.StatusNoContent)
}

// GrantAdmin handles POST /v1/users/{uuid}/grant-admin (admin).
func (h *UsersHandler) GrantAdmin(w http.ResponseWriter, r *http.Request) {
	h.setAdmin(w, r, true, "grant")
}

// RevokeAdmin handles POST /v1/users/{uuid}/revoke-admin (admin).
func (h *UsersHandler) RevokeAdmin(w http.ResponseWriter, r *http.Request) {
	h.setAdmin(w, r, false, "revoke")
}

func (h *UsersHandler) setAdmin(w http.ResponseWriter, r *http.Request, isAdmin bool, op string) {
	user, ok := h.loadUserByUUIDParam(w, r)
	if !ok {
		return
	}

	if err := h.q.SetAdmin(r.Context(), db.SetAdminParams{
		ID:      user.ID,
		IsAdmin: isAdmin,
	}); err != nil {
		slog.ErrorContext(r.Context(), "users.setAdmin", "error", err, "op", op)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to update admin status")
		return
	}

	entityID := user.EntityID
	_ = h.audit.Write(r.Context(), op, "users", &entityID,
		map[string]any{"is_admin": !isAdmin},
		map[string]any{"is_admin": isAdmin},
	)

	server.JSON(w, http.StatusOK, map[string]any{
		"uuid":     user.Uuid.String(),
		"is_admin": isAdmin,
	})
}

// loadUserByUUIDParam extracts the {uuid} chi param and loads the user.
func (h *UsersHandler) loadUserByUUIDParam(w http.ResponseWriter, r *http.Request) (db.User, bool) {
	rawUUID := chi.URLParam(r, "uuid")
	parsed, err := uuid.Parse(rawUUID)
	if err != nil {
		server.Error(w, http.StatusBadRequest, "bad_request", "invalid uuid")
		return db.User{}, false
	}
	user, err := h.q.GetUserByUUID(r.Context(), parsed)
	if err == pgx.ErrNoRows {
		server.Error(w, http.StatusNotFound, "not_found", "user not found")
		return db.User{}, false
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "users: load by uuid", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to load user")
		return db.User{}, false
	}
	return user, true
}

// userResponse builds a public-facing map from a db.User.
func userResponse(u db.User) map[string]any {
	return map[string]any{
		"uuid":           u.Uuid.String(),
		"email":          u.Email,
		"is_admin":       u.IsAdmin,
		"email_verified": u.EmailVerifiedAt != nil,
		"created_at":     u.CreatedAt.Time,
	}
}

// usersPgError tests whether err is a *pgconn.PgError.
func usersPgError(err error, target **pgconn.PgError) bool {
	if pgErr, ok := err.(*pgconn.PgError); ok {
		*target = pgErr
		return true
	}
	return false
}
