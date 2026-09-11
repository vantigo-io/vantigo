package identity

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/httpx"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
)

// auditEvent is one row of identity's authorization audit trail, as .NET's
// AuthorizationAuditWriter recorded it (AZ/AuthorizationAuditWriter.cs).
// before and after are marshalled to JSON as given, so a caller that ports
// a .NET event keeps its property names and order in its own struct.
type auditEvent struct {
	actor      *uuid.UUID // nil for an anonymous change (bootstrap)
	targetUser *uuid.UUID
	targetRole *uuid.UUID
	action     string
	before     any
	after      any
	mfa        bool // the actor's session verified a second factor
}

// writeAudit records e at now on q, which must be the caller's transaction:
// the row commits or rolls back with the change it describes, so a failed
// audit write undoes the change. details is {"action":…} and the
// correlation id is r's trace id, as .NET wrote them.
func writeAudit(ctx context.Context, q *store.Queries, r *http.Request, now time.Time, e auditEvent) error {
	details, err := json.Marshal(struct {
		Action string `json:"action"`
	}{e.action})
	if err != nil {
		return fmt.Errorf("identity: audit %s: %w", e.action, err)
	}
	before, err := json.Marshal(e.before)
	if err != nil {
		return fmt.Errorf("identity: audit %s: %w", e.action, err)
	}
	after, err := json.Marshal(e.after)
	if err != nil {
		return fmt.Errorf("identity: audit %s: %w", e.action, err)
	}
	var correlation *string
	if id := httpx.TraceID(r); id != "" {
		correlation = &id
	}
	beforeJSON, afterJSON := string(before), string(after)
	if err := q.InsertAuditEvent(ctx, store.InsertAuditEventParams{
		ActorUserID:      e.actor,
		TargetUserID:     e.targetUser,
		TargetRoleID:     e.targetRole,
		Action:           e.action,
		Details:          string(details),
		BeforeJson:       &beforeJSON,
		AfterJson:        &afterJSON,
		CorrelationID:    correlation,
		MfaAuthenticated: e.mfa,
		OccurredAt:       now,
	}); err != nil {
		return fmt.Errorf("identity: audit %s: %w", e.action, err)
	}
	return nil
}
