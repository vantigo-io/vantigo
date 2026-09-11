package identity

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/identity/gen"
)

// TestSetPassword_AVanishedUserIsNotFound covers the window between the
// owner-set password's lookup of its target and its transaction: a user
// deleted in between is the bare 404, not a 200 for nobody, and nothing is
// written.
func TestSetPassword_AVanishedUserIsNotFound(t *testing.T) {
	t.Parallel()
	srv, pool := newInternalServer(t)
	gone := uuid.New() // looked up a moment ago, deleted since

	answer, err := refusalOr[gen.PostIdentityOwnerUsersByIdPasswordResponseObject](
		srv.setPassword(context.Background(), gone, "AnyPassword123", nil))
	r, ok := answer.(refusal)
	if err != nil || !ok || r.status != http.StatusNotFound || r.body != nil {
		t.Fatalf("setPassword for a vanished user = %v, %v; want the bare 404", answer, err)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM identity.users`); n != 0 {
		t.Errorf("%d users, want none", n)
	}
}
