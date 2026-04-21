package handlers

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/moduleforge/users-module/api/internal/audit"
	"github.com/moduleforge/users-module/api/internal/server"
	db "github.com/moduleforge/users-module/model/db"
)

// AppsHandler serves /v1/apps endpoints.
type AppsHandler struct {
	q     *db.Queries
	audit audit.Writer
}

// NewAppsHandler creates an AppsHandler.
func NewAppsHandler(q *db.Queries, aw audit.Writer) *AppsHandler {
	return &AppsHandler{q: q, audit: aw}
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

	app, err := h.q.CreateApp(r.Context(), db.CreateAppParams{
		Slug: req.Slug,
		Name: req.Name,
	})
	if err != nil {
		slog.ErrorContext(r.Context(), "apps.create", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to create app")
		return
	}

	_ = h.audit.Write(r.Context(), "create", "apps", nil, nil, appResponse(app))

	server.JSON(w, http.StatusCreated, appResponse(app))
}

// List handles GET /v1/apps (admin).
func (h *AppsHandler) List(w http.ResponseWriter, r *http.Request) {
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

	before := appResponse(app)

	if err := h.q.UpdateApp(r.Context(), db.UpdateAppParams{
		ID:   app.ID,
		Slug: newSlug,
		Name: newName,
	}); err != nil {
		slog.ErrorContext(r.Context(), "apps.update", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to update app")
		return
	}

	after := map[string]any{
		"uuid": app.Uuid.String(),
		"slug": newSlug,
		"name": newName,
	}
	_ = h.audit.Write(r.Context(), "update", "apps", nil, before, after)

	// Return refreshed record.
	updated, err := h.q.GetAppByUUID(r.Context(), app.Uuid)
	if err != nil {
		server.JSON(w, http.StatusOK, after)
		return
	}
	server.JSON(w, http.StatusOK, appResponse(updated))
}

// DeleteApp handles DELETE /v1/apps/{uuid} (admin).
func (h *AppsHandler) DeleteApp(w http.ResponseWriter, r *http.Request) {
	app, ok := h.loadAppByUUIDParam(w, r)
	if !ok {
		return
	}

	if err := h.q.ArchiveApp(r.Context(), app.ID); err != nil {
		slog.ErrorContext(r.Context(), "apps.delete", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to archive app")
		return
	}

	_ = h.audit.Write(r.Context(), "delete", "apps", nil, appResponse(app), nil)

	w.WriteHeader(http.StatusNoContent)
}

// --- apps_users endpoints ---

type assignUserRequest struct {
	UserUUID string   `json:"user_uuid"`
	Roles    []string `json:"roles"`
}

// AssignUser handles POST /v1/apps/{uuid}/users (admin).
func (h *AppsHandler) AssignUser(w http.ResponseWriter, r *http.Request) {
	app, ok := h.loadAppByUUIDParam(w, r)
	if !ok {
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

// ListAppUsers handles GET /v1/apps/{uuid}/users (admin).
func (h *AppsHandler) ListAppUsers(w http.ResponseWriter, r *http.Request) {
	app, ok := h.loadAppByUUIDParam(w, r)
	if !ok {
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
	server.JSON(w, http.StatusOK, map[string]any{"users": resp})
}

// RemoveUser handles DELETE /v1/apps/{uuid}/users/{user_uuid} (admin).
func (h *AppsHandler) RemoveUser(w http.ResponseWriter, r *http.Request) {
	app, ok := h.loadAppByUUIDParam(w, r)
	if !ok {
		return
	}

	rawUserUUID := chi.URLParam(r, "user_uuid")
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

// UpdateUserRoles handles PUT /v1/apps/{uuid}/users/{user_uuid}/roles (admin).
func (h *AppsHandler) UpdateUserRoles(w http.ResponseWriter, r *http.Request) {
	app, ok := h.loadAppByUUIDParam(w, r)
	if !ok {
		return
	}

	rawUserUUID := chi.URLParam(r, "user_uuid")
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
