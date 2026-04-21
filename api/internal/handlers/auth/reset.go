package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	localauth "github.com/moduleforge/users-module/api/internal/auth"
	"github.com/moduleforge/users-module/api/internal/server"
	db "github.com/moduleforge/users-module/model/db"
)

// passwordResetRequestBody is the body for POST /v1/auth/password-reset/request.
type passwordResetRequestBody struct {
	Email string `json:"email"`
}

// passwordResetConfirmBody is the body for POST /v1/auth/password-reset/confirm.
type passwordResetConfirmBody struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

// PasswordResetRequest handles POST /v1/auth/password-reset/request.
// Always returns 204 to prevent email enumeration.
func (h *Handler) PasswordResetRequest(w http.ResponseWriter, r *http.Request) {
	var req passwordResetRequestBody
	if err := server.Decode(r, &req); err != nil {
		server.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}

	req.Email = strings.TrimSpace(strings.ToLower(req.Email))

	// Always respond 204. Real work is fire-and-forget.
	go func() {
		if req.Email == "" {
			return
		}
		ctx := r.Context()
		ua, err := h.queries.GetUserAccountByEmail(ctx, req.Email)
		if err != nil {
			return // silently ignore not-found or db errors
		}

		// Generate 32-byte random token.
		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			slog.ErrorContext(ctx, "password_reset: generate token", "error", err)
			return
		}
		plainToken := hex.EncodeToString(raw)

		// SHA-256 hash to store.
		sum := sha256.Sum256([]byte(plainToken))
		tokenHash := hex.EncodeToString(sum[:])

		expiresAt := pgtype.Timestamptz{Time: time.Now().Add(30 * time.Minute), Valid: true}
		_, err = h.queries.CreatePasswordReset(ctx, db.CreatePasswordResetParams{
			UserAccountID: ua.ID,
			TokenHash:     tokenHash,
			ExpiresAt:     expiresAt,
		})
		if err != nil {
			slog.ErrorContext(ctx, "password_reset: insert", "error", err)
			return
		}

		resetURL := fmt.Sprintf("%s/reset-password?token=%s", strings.TrimRight(h.guiBase, "/"), plainToken)
		body := fmt.Sprintf("Click the link below to reset your password:\n\n%s\n\nThis link expires in 30 minutes. If you did not request a password reset, ignore this email.", resetURL)

		if err := h.sender.Send(ctx, ua.Email, "Reset your password", body); err != nil {
			slog.ErrorContext(ctx, "password_reset: send email", "error", err)
		}
	}()

	w.WriteHeader(http.StatusNoContent)
}

// PasswordResetConfirm handles POST /v1/auth/password-reset/confirm.
func (h *Handler) PasswordResetConfirm(w http.ResponseWriter, r *http.Request) {
	var req passwordResetConfirmBody
	if err := server.Decode(r, &req); err != nil {
		server.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}

	if req.Token == "" {
		server.Error(w, http.StatusBadRequest, "bad_request", "token is required")
		return
	}
	if len(req.Password) < 12 {
		server.Error(w, http.StatusBadRequest, "validation_error", "password must be at least 12 characters")
		return
	}

	// Hash the incoming token for lookup.
	sum := sha256.Sum256([]byte(req.Token))
	tokenHash := hex.EncodeToString(sum[:])

	reset, err := h.queries.GetActivePasswordReset(r.Context(), tokenHash)
	if err == pgx.ErrNoRows {
		server.Error(w, http.StatusUnauthorized, "unauthorized", "invalid or expired reset token")
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "password_reset_confirm: get reset", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to look up token")
		return
	}

	hash, err := localauth.HashPassword(req.Password)
	if err != nil {
		slog.ErrorContext(r.Context(), "password_reset_confirm: hash password", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to process password")
		return
	}

	if err := h.queries.UpsertAuthLocal(r.Context(), db.UpsertAuthLocalParams{
		UserAccountID: reset.UserAccountID,
		PasswordHash:  hash,
	}); err != nil {
		slog.ErrorContext(r.Context(), "password_reset_confirm: upsert auth_local", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to update password")
		return
	}

	if err := h.queries.ConsumePasswordReset(r.Context(), reset.ID); err != nil {
		slog.ErrorContext(r.Context(), "password_reset_confirm: consume reset", "error", err)
	}

	w.WriteHeader(http.StatusNoContent)
}
