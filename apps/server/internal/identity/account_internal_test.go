package identity

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/identity/gen"
)

// TestAccountPassword_APendingUserIsUnauthenticated covers the window
// between the own password change's read of the caller and its write: a
// user made pending in between (a pending user's only way to a password is
// accepting their invitation) is written no row, so the change answers 401
// unauthenticated — never a 200 for a password not stored — and revokes no
// session and spends no reset link.
func TestAccountPassword_APendingUserIsUnauthenticated(t *testing.T) {
	t.Parallel()
	srv, pool := newInternalServer(t)
	ctx := context.Background()
	const current = "CurrentPassword123"
	hash, err := hashPassword(current)
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO identity.users (id, email, normalized_email, display_name, password_hash, version, created_at, updated_at)
		VALUES ($1, 'pending@example.test', 'PENDING@EXAMPLE.TEST', 'Pending', $2, gen_random_uuid(), now(), now())`, id, hash); err != nil {
		t.Fatal(err)
	}
	// The user is pending at the write, though the read of the caller passed.
	if _, err := pool.Exec(ctx, `UPDATE identity.users SET managed_by = 'staff', employee_number = 'P-1', employee_number_key = 'P-1', pending_since = now() WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO identity.password_reset_tokens (token_hash, user_id, expires_at) VALUES ('\x01', $1, now() + interval '1 hour')`, id); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	if _, err := srv.access.createSession(ctx, pool, id, false, false, r); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.access.createSession(ctx, pool, id, false, false, r); err != nil {
		t.Fatal(err)
	}
	var sessionID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM identity.sessions WHERE user_id = $1 ORDER BY id LIMIT 1`, id).Scan(&sessionID); err != nil {
		t.Fatal(err)
	}

	newPassword := "AnotherPassword123"
	current2 := current
	answer, err := srv.PostIdentityAccountPassword(contracts.WithPrincipal(ctx, contracts.Principal{UserID: id, SessionID: sessionID}),
		gen.PostIdentityAccountPasswordRequestObject{Body: &gen.AccountPasswordRequest{CurrentPassword: &current2, NewPassword: &newPassword}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := answer.(gen.PostIdentityAccountPassword401JSONResponse); !ok {
		t.Fatalf("the change = %#v, want 401", answer)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM identity.users WHERE id = $1 AND password_hash = $2`, id, hash); n != 1 {
		t.Error("the password changed")
	}
	if n := countRows(t, pool, `SELECT count(*) FROM identity.sessions WHERE user_id = $1 AND revoked_at IS NULL`, id); n != 2 {
		t.Errorf("%d live sessions, want both kept", n)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM identity.password_reset_tokens WHERE user_id = $1`, id); n != 1 {
		t.Errorf("%d reset links, want the one kept", n)
	}
}
