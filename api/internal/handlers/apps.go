package handlers

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	coreAuthz "github.com/moduleforge/core-api/authz"
	"github.com/moduleforge/core-api/entity"
	"github.com/moduleforge/core-api/observer"
	"github.com/moduleforge/core-api/txhelper"
	"github.com/moduleforge/users-module/api/internal/server"
	db "github.com/moduleforge/users-module/model/db"
)

// appEntity is a minimal entity.Entity stub for app resources.
// Apps do not have an entity_id in the core entity hierarchy (they are not
// entities in the core sense), so EntityID always returns nil except for
// specific-app operations.
//
// Note: apps are not in the core entity table, so the entity_id here is the
// apps.id cast as *int64 for observer targeting purposes only. The authorizer
// treats nil entity ID as "admin-only" and a non-nil ID as "own or admin".
// Since apps are always admin-managed, we pass nil for list/create and the
// app.ID for specific app operations.
type appEntity struct {
	id *int64
}

func (e appEntity) Resource() string { return "app" }
func (e appEntity) EntityID() *int64 { return e.id }

// Compile-time: appEntity satisfies entity.Entity.
var _ entity.Entity = appEntity{}

// AppsHandler serves /v1/apps endpoints.
type AppsHandler struct {
	pool       txhelper.DB
	q          db.Querier
	newQuerier func(pgx.Tx) db.Querier // factory for tx-scoped querier; defaults to db.New
	az         coreAuthz.Authorizer
	observers  *observer.ObserverGroup
}

// NewAppsHandler creates an AppsHandler.
func NewAppsHandler(
	pool txhelper.DB,
	q *db.Queries,
	az coreAuthz.Authorizer,
	observers *observer.ObserverGroup,
) *AppsHandler {
	return &AppsHandler{
		pool:       pool,
		q:          q,
		newQuerier: func(tx pgx.Tx) db.Querier { return db.New(tx) },
		az:         az,
		observers:  observers,
	}
}

type createAppRequest struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// Create handles POST /v1/apps (admin).
func (h *AppsHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req createAppRequest
	if err := server.Decode(r, &req); err != nil {
		server.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	req.Slug = strings.TrimSpace(req.Slug)
	req.Name = strings.TrimSpace(req.Name)
	if req.Slug == "" {
		server.Error(w, http.StatusBadRequest, "validation_error", "slug is required")
		return
	}
	if req.Name == "" {
		server.Error(w, http.StatusBadRequest, "validation_error", "name is required")
		return
	}

	// 1. Authorize: create is admin-only.
	if err := h.az.Authorize(r.Context(), "create", appEntity{}); err != nil {
		writeAuthzError(w, err)
		return
	}

	var app db.App
	txErr := txhelper.Run(r.Context(), h.pool, func(ctx context.Context, tx pgx.Tx) error {
		qtx := h.newQuerier(tx)
		var err error
		app, err = qtx.CreateApp(ctx, db.CreateAppParams{
			Slug: req.Slug,
			Name: req.Name,
		})
		if err != nil {
			return err
		}

		after := map[string]any{
			"uuid": app.Uuid.String(),
			"slug": app.Slug,
			"name": app.Name,
		}
		// Apps don't have a core entity_id; pass nil.
		return h.observers.Observe(ctx, tx, "create", "app", nil, nil, after)
	})
	if txErr != nil {
		slog.ErrorContext(r.Context(), "apps.create", "error", txErr)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to create app")
		return
	}

	after := map[string]any{
		"uuid": app.Uuid.String(),
		"slug": app.Slug,
		"name": app.Name,
	}
	h.observers.ObserveAfterCommit(r.Context(), "create", "app", nil, nil, after)

	server.JSON(w, http.StatusCreated, appResponse(app))
}

// List handles GET /v1/apps (admin).
func (h *AppsHandler) List(w http.ResponseWriter, r *http.Request) {
	// Authorize: list is admin-only.
	if err := h.az.Authorize(r.Context(), "list", appEntity{}); err != nil {
		writeAuthzError(w, err)
		return
	}

	apps, err := h.q.ListApps(r.Context())
	if err != nil {
		slog.ErrorContext(r.Context(), "apps.list", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to list apps")
		return
	}

	resp := make([]map[string]any, 0, len(apps))
	for _, a := range apps {
		resp = append(resp, appResponse(a))
	}
	server.JSON(w, http.StatusOK, map[string]any{"apps": resp})
}

// GetApp handles GET /v1/apps/{uuid} (admin).
func (h *AppsHandler) GetApp(w http.ResponseWriter, r *http.Request) {
	app, ok := h.loadAppByUUIDParam(w, r)
	if !ok {
		return
	}

	// Authorize: read — admin only for apps.
	if err := h.az.Authorize(r.Context(), "read", appEntity{}); err != nil {
		writeAuthzError(w, err)
		return
	}

	server.JSON(w, http.StatusOK, appResponse(app))
}

type updateAppRequest struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// UpdateApp handles PUT /v1/apps/{uuid} (admin).
func (h *AppsHandler) UpdateApp(w http.ResponseWriter, r *http.Request) {
	app, ok := h.loadAppByUUIDParam(w, r)
	if !ok {
		return
	}

	// Authorize: update — admin only for apps.
	if err := h.az.Authorize(r.Context(), "update", appEntity{}); err != nil {
		writeAuthzError(w, err)
		return
	}

	var req updateAppRequest
	if err := server.Decode(r, &req); err != nil {
		server.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}

	newSlug := app.Slug
	if strings.TrimSpace(req.Slug) != "" {
		newSlug = strings.TrimSpace(req.Slug)
	}
	newName := app.Name
	if strings.TrimSpace(req.Name) != "" {
		newName = strings.TrimSpace(req.Name)
	}

	before := map[string]any{
		"uuid": app.Uuid.String(),
		"slug": app.Slug,
		"name": app.Name,
	}

	var updated db.App
	txErr := txhelper.Run(r.Context(), h.pool, func(ctx context.Context, tx pgx.Tx) error {
		qtx := h.newQuerier(tx)
		if err := qtx.UpdateApp(ctx, db.UpdateAppParams{
			ID:   app.ID,
			Slug: newSlug,
			Name: newName,
		}); err != nil {
			return err
		}

		var err error
		updated, err = qtx.GetAppByUUID(ctx, app.Uuid)
		if err != nil {
			// Best-effort: use computed values.
			updated = app
			updated.Slug = newSlug
			updated.Name = newName
		}

		after := map[string]any{
			"uuid": updated.Uuid.String(),
			"slug": updated.Slug,
			"name": updated.Name,
		}
		return h.observers.Observe(ctx, tx, "update", "app", nil, before, after)
	})
	if txErr != nil {
		slog.ErrorContext(r.Context(), "apps.update", "error", txErr)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to update app")
		return
	}

	after := map[string]any{
		"uuid": updated.Uuid.String(),
		"slug": updated.Slug,
		"name": updated.Name,
	}
	h.observers.ObserveAfterCommit(r.Context(), "update", "app", nil, before, after)
	server.JSON(w, http.StatusOK, appResponse(updated))
}

// DeleteApp handles DELETE /v1/apps/{uuid} (admin).
func (h *AppsHandler) DeleteApp(w http.ResponseWriter, r *http.Request) {
	app, ok := h.loadAppByUUIDParam(w, r)
	if !ok {
		return
	}

	// Authorize: delete — admin only for apps.
	if err := h.az.Authorize(r.Context(), "delete", appEntity{}); err != nil {
		writeAuthzError(w, err)
		return
	}

	before := map[string]any{
		"uuid": app.Uuid.String(),
		"slug": app.Slug,
		"name": app.Name,
	}

	txErr := txhelper.Run(r.Context(), h.pool, func(ctx context.Context, tx pgx.Tx) error {
		qtx := h.newQuerier(tx)
		if err := qtx.ArchiveApp(ctx, app.ID); err != nil {
			return err
		}
		return h.observers.Observe(ctx, tx, "delete", "app", nil, before, nil)
	})
	if txErr != nil {
		slog.ErrorContext(r.Context(), "apps.delete", "error", txErr)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to archive app")
		return
	}

	h.observers.ObserveAfterCommit(r.Context(), "delete", "app", nil, before, nil)
	w.WriteHeader(http.StatusNoContent)
}

// --- apps_users endpoints ---
// These endpoints are admin-only management operations. They do not emit
// audit events (no equivalent in the original audit gap report) but they
// do require authorization.

type assignUserRequest struct {
	UserUUID string   `json:"user_uuid"`
	Roles    []string `json:"roles"`
}

// AssignUser handles POST /v1/apps/{uuid}/user-accounts (admin).
func (h *AppsHandler) AssignUser(w http.ResponseWriter, r *http.Request) {
	app, ok := h.loadAppByUUIDParam(w, r)
	if !ok {
		return
	}

	// Authorize: update (assigning a user to an app is an app mutation).
	if err := h.az.Authorize(r.Context(), "update", appEntity{}); err != nil {
		writeAuthzError(w, err)
		return
	}

	var req assignUserRequest
	if err := server.Decode(r, &req); err != nil {
		server.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if req.UserUUID == "" {
		server.Error(w, http.StatusBadRequest, "validation_error", "user_uuid is required")
		return
	}

	userUUID, err := uuid.Parse(req.UserUUID)
	if err != nil {
		server.Error(w, http.StatusBadRequest, "bad_request", "invalid user_uuid")
		return
	}

	ua, err := h.q.GetUserAccountByUUID(r.Context(), userUUID)
	if err == pgx.ErrNoRows {
		server.Error(w, http.StatusNotFound, "not_found", "user account not found")
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "apps.assign_user: get user account", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to load user account")
		return
	}

	roles := req.Roles
	if roles == nil {
		roles = []string{}
	}

	if err := h.q.AssignUserAccountToApp(r.Context(), db.AssignUserAccountToAppParams{
		AppID:         app.ID,
		UserAccountID: ua.ID,
		Roles:         roles,
	}); err != nil {
		slog.ErrorContext(r.Context(), "apps.assign_user", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to assign user account to app")
		return
	}

	server.JSON(w, http.StatusCreated, map[string]any{
		"app_uuid":  app.Uuid.String(),
		"user_uuid": ua.Uuid.String(),
		"roles":     roles,
	})
}

// ListAppUsers handles GET /v1/apps/{uuid}/user-accounts (admin).
func (h *AppsHandler) ListAppUsers(w http.ResponseWriter, r *http.Request) {
	app, ok := h.loadAppByUUIDParam(w, r)
	if !ok {
		return
	}

	// Authorize: read (listing app members).
	if err := h.az.Authorize(r.Context(), "read", appEntity{}); err != nil {
		writeAuthzError(w, err)
		return
	}

	members, err := h.q.ListAppUserAccounts(r.Context(), app.ID)
	if err != nil {
		slog.ErrorContext(r.Context(), "apps.list_users", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to list app user accounts")
		return
	}

	resp := make([]map[string]any, 0, len(members))
	for _, m := range members {
		resp = append(resp, map[string]any{
			"user_account_id": m.UserAccountID,
			"roles":           m.Roles,
		})
	}
	server.JSON(w, http.StatusOK, map[string]any{"user_accounts": resp})
}

// RemoveUser handles DELETE /v1/apps/{uuid}/user-accounts/{user_account_uuid} (admin).
func (h *AppsHandler) RemoveUser(w http.ResponseWriter, r *http.Request) {
	app, ok := h.loadAppByUUIDParam(w, r)
	if !ok {
		return
	}

	// Authorize: update (removing a user from an app is an app mutation).
	if err := h.az.Authorize(r.Context(), "update", appEntity{}); err != nil {
		writeAuthzError(w, err)
		return
	}

	rawUserUUID := chi.URLParam(r, "user_account_uuid")
	userUUID, err := uuid.Parse(rawUserUUID)
	if err != nil {
		server.Error(w, http.StatusBadRequest, "bad_request", "invalid user uuid")
		return
	}

	ua, err := h.q.GetUserAccountByUUID(r.Context(), userUUID)
	if err == pgx.ErrNoRows {
		server.Error(w, http.StatusNotFound, "not_found", "user account not found")
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "apps.remove_user: get user account", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to load user account")
		return
	}

	if err := h.q.RemoveUserAccountFromApp(r.Context(), db.RemoveUserAccountFromAppParams{
		AppID:         app.ID,
		UserAccountID: ua.ID,
	}); err != nil {
		slog.ErrorContext(r.Context(), "apps.remove_user", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to remove user account from app")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

type updateRolesRequest struct {
	Roles []string `json:"roles"`
}

// UpdateUserRoles handles PUT /v1/apps/{uuid}/user-accounts/{user_account_uuid}/roles (admin).
func (h *AppsHandler) UpdateUserRoles(w http.ResponseWriter, r *http.Request) {
	app, ok := h.loadAppByUUIDParam(w, r)
	if !ok {
		return
	}

	// Authorize: update (changing roles is an app mutation).
	if err := h.az.Authorize(r.Context(), "update", appEntity{}); err != nil {
		writeAuthzError(w, err)
		return
	}

	rawUserUUID := chi.URLParam(r, "user_account_uuid")
	userUUID, err := uuid.Parse(rawUserUUID)
	if err != nil {
		server.Error(w, http.StatusBadRequest, "bad_request", "invalid user uuid")
		return
	}

	ua, err := h.q.GetUserAccountByUUID(r.Context(), userUUID)
	if err == pgx.ErrNoRows {
		server.Error(w, http.StatusNotFound, "not_found", "user account not found")
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "apps.update_roles: get user account", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to load user account")
		return
	}

	var req updateRolesRequest
	if err := server.Decode(r, &req); err != nil {
		server.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if req.Roles == nil {
		req.Roles = []string{}
	}

	if err := h.q.SetAppUserAccountRoles(r.Context(), db.SetAppUserAccountRolesParams{
		AppID:         app.ID,
		UserAccountID: ua.ID,
		Roles:         req.Roles,
	}); err != nil {
		slog.ErrorContext(r.Context(), "apps.update_roles", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to update roles")
		return
	}

	server.JSON(w, http.StatusOK, map[string]any{
		"app_uuid":  app.Uuid.String(),
		"user_uuid": ua.Uuid.String(),
		"roles":     req.Roles,
	})
}

// loadAppByUUIDParam extracts the {uuid} chi param and loads the app.
func (h *AppsHandler) loadAppByUUIDParam(w http.ResponseWriter, r *http.Request) (db.App, bool) {
	rawUUID := chi.URLParam(r, "uuid")
	parsed, err := uuid.Parse(rawUUID)
	if err != nil {
		server.Error(w, http.StatusBadRequest, "bad_request", "invalid uuid")
		return db.App{}, false
	}
	app, err := h.q.GetAppByUUID(r.Context(), parsed)
	if err == pgx.ErrNoRows {
		server.Error(w, http.StatusNotFound, "not_found", "app not found")
		return db.App{}, false
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "apps: load by uuid", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to load app")
		return db.App{}, false
	}
	return app, true
}

func appResponse(a db.App) map[string]any {
	return map[string]any{
		"uuid":       a.Uuid.String(),
		"slug":       a.Slug,
		"name":       a.Name,
		"created_at": a.CreatedAt.Time,
	}
}
