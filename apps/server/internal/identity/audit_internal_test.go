package identity

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/httpx"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
)

// auditRowFor reads the one audit row with action as details, actor and
// correlation id.
func auditRowFor(t *testing.T, srv *server, action string) (details string, actor *uuid.UUID, correlation *string) {
	t.Helper()
	if err := srv.deps.Pool.QueryRow(context.Background(), `
		SELECT details, actor_user_id, correlation_id FROM identity.authorization_audit_events WHERE action = $1`, action).
		Scan(&details, &actor, &correlation); err != nil {
		t.Fatalf("audit row %s: %v", action, err)
	}
	return details, actor, correlation
}

// A system actor's change has no request and no user: writeAudit records it
// with a null actor_user_id, no correlation id and details.actor naming the
// system, after the action.
func TestWriteAudit_ASystemActorWithoutARequest(t *testing.T) {
	t.Parallel()
	srv, pool := newInternalServer(t)
	target := uuid.New()
	if err := writeAudit(context.Background(), store.New(pool), nil, internalNow, auditEvent{
		system: "system:staff-accounts", targetUser: &target, action: "user.system-test", after: struct{}{},
	}); err != nil {
		t.Fatalf("writeAudit without a request: %v", err)
	}
	details, actor, correlation := auditRowFor(t, srv, "user.system-test")
	if want := `{"action":"user.system-test","actor":"system:staff-accounts"}`; details != want || actor != nil || correlation != nil {
		t.Errorf("row = %s, actor %v, correlation %v; want %s, no actor, no correlation id", details, actor, correlation, want)
	}
}

// A user actor's row is as it always was: details {"action":…} byte for
// byte, no actor key, and the request's trace id as the correlation id.
func TestWriteAudit_AUserActorsDetailsAreUnchanged(t *testing.T) {
	t.Parallel()
	srv, pool := newInternalServer(t)
	caller := uuid.New()
	var r *http.Request
	httpx.RequestID(http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) { r = req })).
		ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", nil))
	if err := writeAudit(context.Background(), store.New(pool), r, internalNow, auditEvent{
		actor: &caller, action: "user.user-test", after: struct{}{},
	}); err != nil {
		t.Fatalf("writeAudit: %v", err)
	}
	details, actor, correlation := auditRowFor(t, srv, "user.user-test")
	if details != `{"action":"user.user-test"}` || actor == nil || *actor != caller || correlation == nil || *correlation != httpx.TraceID(r) {
		t.Errorf("row = %s, actor %v, correlation %v; want {\"action\":\"user.user-test\"}, the caller and the trace id", details, actor, correlation)
	}
}
