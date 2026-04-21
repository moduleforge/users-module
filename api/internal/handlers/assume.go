package handlers

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/moduleforge/users-module/api/internal/auth"
	"github.com/moduleforge/users-module/api/internal/server"
	db "github.com/moduleforge/users-module/model/db"
)

// AssumeHandler handles identity assumption for admins.
type AssumeHandler struct {
	q         *db.Queries
	jwtSecret string
	issuer    string
}

// NewAssumeHandler creates an AssumeHandler.
func NewAssumeHandler(q *db.Queries, jwtSecret, issuer string) *AssumeHandler {
	return &AssumeHandler{q: q, jwtSecret: jwtSecret, issuer: issuer}
}

// Assume handles POST /v1/user-accounts/{uuid}/assume (admin).
// Returns a JWT where the bearer acts as the assumed user account while the
// audit trail preserves the original admin identity.
func (h *AssumeHandler) Assume(w http.ResponseWriter, r *http.Request) {
	rawUUID := chi.URLParam(r, "uuid")
	parsed, err := uuid.Parse(rawUUID)
	if err != nil {
		server.Error(w, http.StatusBadRequest, "bad_request", "invalid uuid")
		return
	}

	assumedUA, err := h.q.GetUserAccountByUUID(r.Context(), parsed)
	if err == pgx.ErrNoRows {
		server.Error(w, http.StatusNotFound, "not_found", "user account not found")
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "assume: get target user account", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to load target user account")
		return
	}

	uc := auth.MustFromContext(r.Context())

	// Load the admin's own user account record to include in the JWT.
	adminUA, err := h.q.GetUserAccountByID(r.Context(), uc.UserAccountID)
	if err != nil {
		slog.ErrorContext(r.Context(), "assume: get admin user account", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to load admin user account")
		return
	}

	token, err := auth.IssueAssumeJWT(adminUA, assumedUA, h.jwtSecret, h.issuer)
	if err != nil {
		slog.ErrorContext(r.Context(), "assume: issue jwt", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to issue token")
		return
	}

	slog.InfoContext(r.Context(), "admin assuming identity",
		"admin_uuid", adminUA.Uuid.String(),
		"assumed_uuid", assumedUA.Uuid.String(),
	)

	server.JSON(w, http.StatusOK, map[string]any{
		"token": token,
		"assumed_user": map[string]any{
			"uuid":  assumedUA.Uuid.String(),
			"email": assumedUA.Email,
		},
	})
}

// EndAssume handles DELETE /v1/assume (auth).
// The client simply discards the assume token and uses the original admin token,
// so this endpoint returns 204 as an explicit "clear" signal.
func (h *AssumeHandler) EndAssume(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}
