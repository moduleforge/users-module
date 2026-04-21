package auth

import (
	"crypto/rand"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/crypto/bcrypt"

	localauth "github.com/moduleforge/users-module/api/internal/auth"
	"github.com/moduleforge/users-module/api/internal/server"
	db "github.com/moduleforge/users-module/model/db"
)

// emailCodeRequestBody is the body for POST /v1/auth/email-code/request.
type emailCodeRequestBody struct {
	Email   string `json:"email"`
	Purpose string `json:"purpose"` // "login" or "verify_email"
}

// emailCodeVerifyBody is the body for POST /v1/auth/email-code/verify.
type emailCodeVerifyBody struct {
	Email   string `json:"email"`
	Code    string `json:"code"`
	Purpose string `json:"purpose"`
}

// EmailCodeRequest handles POST /v1/auth/email-code/request.
// Always returns 204 within ~200ms to avoid email enumeration.
func (h *Handler) EmailCodeRequest(w http.ResponseWriter, r *http.Request) {
	var req emailCodeRequestBody
	if err := server.Decode(r, &req); err != nil {
		server.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}

	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	if req.Purpose == "" {
		req.Purpose = "login"
	}

	// Perform the real work in a goroutine; always wait ~200ms before responding.
	done := make(chan struct{}, 1)
	go func() {
		defer func() { done <- struct{}{} }()
		h.sendEmailCode(r, req.Email, req.Purpose)
	}()

	// Ensure at least 200ms response time.
	timer := time.NewTimer(200 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C:
		<-done
	case <-done:
		<-timer.C
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) sendEmailCode(r *http.Request, email, purpose string) {
	ctx := r.Context()

	ua, err := h.queries.GetUserAccountByEmail(ctx, email)
	if err == pgx.ErrNoRows {
		return // anti-enumeration: silently skip
	}
	if err != nil {
		slog.ErrorContext(ctx, "email_code: get user account", "error", err)
		return
	}

	// Generate 6-digit code.
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		slog.ErrorContext(ctx, "email_code: generate code", "error", err)
		return
	}
	code := fmt.Sprintf("%06d", n.Int64())

	hash, err := bcrypt.GenerateFromPassword([]byte(code), bcrypt.DefaultCost)
	if err != nil {
		slog.ErrorContext(ctx, "email_code: hash code", "error", err)
		return
	}

	expiresAt := pgtype.Timestamptz{Time: time.Now().Add(5 * time.Minute), Valid: true}
	_, err = h.queries.CreateEmailCode(ctx, db.CreateEmailCodeParams{
		UserAccountID: ua.ID,
		CodeHash:      string(hash),
		Purpose:       purpose,
		ExpiresAt:     expiresAt,
	})
	if err != nil {
		slog.ErrorContext(ctx, "email_code: insert", "error", err)
		return
	}

	subject := "Your login code"
	if purpose == "verify_email" {
		subject = "Verify your email address"
	}
	body := fmt.Sprintf("Your verification code is: %s\n\nThis code expires in 5 minutes.", code)

	if err := h.sender.Send(ctx, ua.Email, subject, body); err != nil {
		slog.ErrorContext(ctx, "email_code: send email", "error", err)
	}
}

// EmailCodeVerify handles POST /v1/auth/email-code/verify.
func (h *Handler) EmailCodeVerify(w http.ResponseWriter, r *http.Request) {
	var req emailCodeVerifyBody
	if err := server.Decode(r, &req); err != nil {
		server.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}

	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	if req.Email == "" || req.Code == "" {
		server.Error(w, http.StatusBadRequest, "bad_request", "email and code are required")
		return
	}
	if req.Purpose == "" {
		req.Purpose = "login"
	}

	ua, err := h.queries.GetUserAccountByEmail(r.Context(), req.Email)
	if err == pgx.ErrNoRows {
		server.Error(w, http.StatusUnauthorized, "unauthorized", "invalid code")
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "email_code_verify: get user account", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to look up user account")
		return
	}

	emailCode, err := h.queries.GetActiveEmailCode(r.Context(), db.GetActiveEmailCodeParams{
		UserAccountID: ua.ID,
		Purpose:       req.Purpose,
	})
	if err == pgx.ErrNoRows {
		server.Error(w, http.StatusUnauthorized, "unauthorized", "invalid or expired code")
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "email_code_verify: get code", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to look up code")
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(emailCode.CodeHash), []byte(req.Code)); err != nil {
		server.Error(w, http.StatusUnauthorized, "unauthorized", "invalid or expired code")
		return
	}

	// Consume the code.
	if err := h.queries.ConsumeEmailCode(r.Context(), emailCode.ID); err != nil {
		slog.ErrorContext(r.Context(), "email_code_verify: consume code", "error", err)
	}

	if req.Purpose == "login" {
		token, err := localauth.IssueLocalJWT(ua, ua.IsAdmin, h.jwtSecret, h.issuer)
		if err != nil {
			slog.ErrorContext(r.Context(), "email_code_verify: issue jwt", "error", err)
			server.Error(w, http.StatusInternalServerError, "internal_error", "failed to issue token")
			return
		}
		server.JSON(w, http.StatusOK, map[string]any{
			"token": token,
			"user": map[string]any{
				"uuid":     ua.Uuid.String(),
				"email":    ua.Email,
				"is_admin": ua.IsAdmin,
			},
		})
		return
	}

	// verify_email purpose: mark email as verified.
	now := time.Now()
	_ = h.queries.UpdateUserAccount(r.Context(), db.UpdateUserAccountParams{
		ID:              ua.ID,
		Email:           ua.Email,
		EmailVerifiedAt: &now,
		AuthIssuer:      ua.AuthIssuer,
		AuthID:          ua.AuthID,
	})

	w.WriteHeader(http.StatusNoContent)
}
