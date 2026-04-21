package handlers

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	coreservice "github.com/moduleforge/core-api/service"
	coredb "github.com/moduleforge/core-model/db"
	"github.com/moduleforge/users-module/api/internal/server"
	db "github.com/moduleforge/users-module/model/db"
)

// AuditHandler serves audit log endpoints.
type AuditHandler struct {
	q        *db.Queries
	coreQ    *coredb.Queries
	coreSvcs *coreservice.Services
}

// NewAuditHandler creates an AuditHandler.
func NewAuditHandler(q *db.Queries, coreQ *coredb.Queries, coreSvcs *coreservice.Services) *AuditHandler {
	return &AuditHandler{q: q, coreQ: coreQ, coreSvcs: coreSvcs}
}

// ByUser handles GET /v1/user-accounts/{uuid}/audit (admin).
// Returns audit log entries where the user account is the actor.
func (h *AuditHandler) ByUser(w http.ResponseWriter, r *http.Request) {
	rawUUID := chi.URLParam(r, "uuid")
	parsed, err := uuid.Parse(rawUUID)
	if err != nil {
		server.Error(w, http.StatusBadRequest, "bad_request", "invalid uuid")
		return
	}

	ua, err := h.q.GetUserAccountByUUID(r.Context(), parsed)
	if err == pgx.ErrNoRows {
		server.Error(w, http.StatusNotFound, "not_found", "user account not found")
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "audit.by_user: get user account", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to load user account")
		return
	}

	limit, offset := auditPagination(r)

	entries, err := h.q.ListAuditByActor(r.Context(), db.ListAuditByActorParams{
		ActorUserAccountID: ua.ID,
		Limit:              limit,
		Offset:             offset,
	})
	if err != nil {
		slog.ErrorContext(r.Context(), "audit.by_user: list", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to list audit log")
		return
	}

	server.JSON(w, http.StatusOK, map[string]any{
		"audit": auditResponses(entries),
		"total": len(entries),
	})
}

// ByEntity handles GET /v1/audit/{entity_uuid} (admin).
// Returns audit log entries where the entity is the target.
func (h *AuditHandler) ByEntity(w http.ResponseWriter, r *http.Request) {
	rawUUID := chi.URLParam(r, "entity_uuid")
	parsed, err := uuid.Parse(rawUUID)
	if err != nil {
		server.Error(w, http.StatusBadRequest, "bad_request", "invalid entity uuid")
		return
	}

	entity, err := h.coreSvcs.Entity.GetByUUID(r.Context(), h.coreQ, parsed)
	if errors.Is(err, coreservice.ErrNotFound) {
		server.Error(w, http.StatusNotFound, "not_found", "entity not found")
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "audit.by_entity: get entity", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to load entity")
		return
	}

	limit, offset := auditPagination(r)

	entries, err := h.q.ListAuditByTarget(r.Context(), db.ListAuditByTargetParams{
		TargetEntityID: pgtype.Int8{Int64: entity.ID, Valid: true},
		Limit:          limit,
		Offset:         offset,
	})
	if err != nil {
		slog.ErrorContext(r.Context(), "audit.by_entity: list", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to list audit log")
		return
	}

	server.JSON(w, http.StatusOK, map[string]any{
		"audit": auditResponses(entries),
		"total": len(entries),
	})
}

func auditPagination(r *http.Request) (limit, offset int32) {
	limit = 20
	offset = 0
	q := r.URL.Query()
	if l := q.Get("limit"); l != "" {
		if v, err := strconv.ParseInt(l, 10, 32); err == nil && v > 0 && v <= 200 {
			limit = int32(v)
		}
	}
	if o := q.Get("offset"); o != "" {
		if v, err := strconv.ParseInt(o, 10, 32); err == nil && v >= 0 {
			offset = int32(v)
		}
	}
	return limit, offset
}

func auditResponses(entries []db.AuditLog) []map[string]any {
	out := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		row := map[string]any{
			"id":                    e.ID,
			"actor_user_account_id": e.ActorUserAccountID,
			"op":                    e.Op,
			"resource":              e.Resource,
			"at":                    e.At.Time,
		}
		if e.AssumedUserAccountID.Valid {
			row["assumed_user_account_id"] = e.AssumedUserAccountID.Int64
		}
		if e.TargetEntityID.Valid {
			row["target_entity_id"] = e.TargetEntityID.Int64
		}
		if e.Before != nil {
			row["before"] = string(e.Before)
		}
		if e.After != nil {
			row["after"] = string(e.After)
		}
		out = append(out, row)
	}
	return out
}
