package handlers

import (
	"errors"
	"net/http"

	coreservice "github.com/moduleforge/core-api/service"
	coredb "github.com/moduleforge/core-model/db"
	"github.com/moduleforge/users-module/api/internal/auth"
	"github.com/moduleforge/users-module/api/internal/server"
	db "github.com/moduleforge/users-module/model/db"
)

// SelfHandler serves /v1/self. /self is a composite identity endpoint:
// core-module owns the entity data (given_name, family_name, etc.) via
// EntityService.GetSelf, while users-module owns the users-row data
// (email, is_admin, timestamps, uuid). This handler stitches the two.
type SelfHandler struct {
	q        *db.Queries
	coreQ    *coredb.Queries
	coreSvcs *coreservice.Services
}

// NewSelfHandler constructs the /self handler with its dependencies.
func NewSelfHandler(q *db.Queries, coreQ *coredb.Queries, coreSvcs *coreservice.Services) *SelfHandler {
	return &SelfHandler{q: q, coreQ: coreQ, coreSvcs: coreSvcs}
}

// Get returns the caller's full profile: user account row fields + entity/subtype.
func (h *SelfHandler) Get(w http.ResponseWriter, r *http.Request) {
	uc := auth.MustFromContext(r.Context())

	ua, err := h.q.GetUserAccountByID(r.Context(), uc.UserAccountID)
	if err != nil {
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to load user account")
		return
	}

	principal := coreservice.Principal{UserID: uc.UserAccountID, EntityID: uc.EntityID, IsAdmin: uc.IsAdmin}
	profile, err := h.coreSvcs.Entity.GetSelf(r.Context(), h.coreQ, principal)
	if err != nil {
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to load entity")
		return
	}

	server.JSON(w, http.StatusOK, buildSelfResponse(ua, profile))
}

// selfUpdateRequest is the body for PUT /v1/self.
type selfUpdateRequest struct {
	GivenName  *string `json:"given_name"`
	FamilyName *string `json:"family_name"`
}

// Put updates the caller's mutable profile fields (currently only
// natural_person given_name/family_name). Returns the composed profile.
func (h *SelfHandler) Put(w http.ResponseWriter, r *http.Request) {
	uc := auth.MustFromContext(r.Context())

	var req selfUpdateRequest
	if err := server.Decode(r, &req); err != nil {
		server.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}

	ua, err := h.q.GetUserAccountByID(r.Context(), uc.UserAccountID)
	if err != nil {
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to load user account")
		return
	}

	principal := coreservice.Principal{UserID: uc.UserAccountID, EntityID: uc.EntityID, IsAdmin: uc.IsAdmin}

	if req.GivenName != nil || req.FamilyName != nil {
		// account_holder = entity_id on the legal_entities/natural_persons chain.
		entity, err := h.coreQ.GetEntityByID(r.Context(), ua.AccountHolder)
		if err != nil {
			server.Error(w, http.StatusInternalServerError, "internal_error", "failed to resolve entity")
			return
		}
		err = h.coreSvcs.NaturalPerson.UpdateByEntityUUID(
			r.Context(),
			h.coreQ,
			entity.Uuid,
			coreservice.UpdateNaturalPersonInput{GivenName: req.GivenName, FamilyName: req.FamilyName},
			principal,
		)
		if err != nil {
			writeCoreServiceErr(w, err)
			return
		}
	}

	// Re-fetch the now-updated profile.
	profile, err := h.coreSvcs.Entity.GetSelf(r.Context(), h.coreQ, principal)
	if err != nil {
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to reload entity")
		return
	}

	server.JSON(w, http.StatusOK, buildSelfResponse(ua, profile))
}

// buildSelfResponse composes the flat response shape the frontend
// (UserSelf interface) expects.
func buildSelfResponse(ua db.UserAccount, profile coreservice.Profile) map[string]any {
	resp := map[string]any{
		"uuid":       ua.Uuid.String(),
		"email":      ua.Email,
		"is_admin":   ua.IsAdmin,
		"created_at": ua.CreatedAt.Time,
		"updated_at": ua.UpdatedAt.Time,
	}

	switch profile.Kind {
	case "natural_person":
		if np := profile.NaturalPerson; np != nil {
			resp["given_name"] = np.GivenName.String
			resp["family_name"] = np.FamilyName.String
		}
	case "corporation":
		if corp := profile.Corporation; corp != nil {
			resp["legal_name"] = corp.LegalName
		}
	case "service_account":
		if sa := profile.ServiceAccount; sa != nil {
			resp["label"] = sa.Label
		}
	}

	return resp
}

// writeCoreServiceErr maps core service sentinels to HTTP responses.
func writeCoreServiceErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, coreservice.ErrNotFound):
		server.Error(w, http.StatusNotFound, "not_found", "resource not found")
	case errors.Is(err, coreservice.ErrForbidden):
		server.Error(w, http.StatusForbidden, "forbidden", "access denied")
	case errors.Is(err, coreservice.ErrInvalidInput):
		server.Error(w, http.StatusBadRequest, "invalid_input", err.Error())
	default:
		server.Error(w, http.StatusInternalServerError, "internal_error", "an internal error occurred")
	}
}
