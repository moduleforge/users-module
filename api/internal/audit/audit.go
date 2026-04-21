// Package audit provides a context-threaded writer for the audit_log table.
// Handlers call Write explicitly after each mutation so before/after snapshots
// are precise — no magical interceptor.
package audit

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/jackc/pgx/v5/pgtype"

	coreaudit "github.com/moduleforge/core-api/audit"
	"github.com/moduleforge/users-module/api/internal/auth"
	db "github.com/moduleforge/users-module/model/db"
)

// Compile-time assertion: pgWriter satisfies core's audit.Writer interface.
// This catches signature drift between users-module and core-module at build time.
var _ coreaudit.Writer = (*pgWriter)(nil)

// Writer records audit events.
type Writer interface {
	Write(ctx context.Context, op string, resource string, targetEntityID *int64, before, after any) error
}

type pgWriter struct {
	q *db.Queries
}

// New creates a Writer backed by pgx queries.
func New(q *db.Queries) Writer {
	return &pgWriter{q: q}
}

func (w *pgWriter) Write(ctx context.Context, op string, resource string, targetEntityID *int64, before, after any) error {
	uc := auth.MustFromContext(ctx)

	beforeJSON, err := marshalNullable(before)
	if err != nil {
		slog.ErrorContext(ctx, "audit: marshal before", "error", err)
		return nil // log but don't fail the request
	}
	afterJSON, err := marshalNullable(after)
	if err != nil {
		slog.ErrorContext(ctx, "audit: marshal after", "error", err)
		return nil
	}

	var assumedID pgtype.Int8
	if uc.AssumedUser != nil {
		assumedID = pgtype.Int8{Int64: uc.AssumedUser.UserAccountID, Valid: true}
	}

	var targetID pgtype.Int8
	if targetEntityID != nil {
		targetID = pgtype.Int8{Int64: *targetEntityID, Valid: true}
	}

	err = w.q.WriteAudit(ctx, db.WriteAuditParams{
		ActorUserAccountID:   uc.UserAccountID,
		AssumedUserAccountID: assumedID,
		TargetEntityID:       targetID,
		Op:                   op,
		Resource:             resource,
		Before:               beforeJSON,
		After:                afterJSON,
	})
	if err != nil {
		// Audit failures are logged but do not break the user's request.
		slog.ErrorContext(ctx, "audit: write failed",
			"error", err,
			"op", op,
			"resource", resource,
			"actor_user_account_id", uc.UserAccountID,
		)
	}

	return nil
}

func marshalNullable(v any) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	return json.Marshal(v)
}
