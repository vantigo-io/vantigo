package identity

import (
	"context"
	"testing"
	"time"
)

// recordedMode is system_settings' authority_mode and authority_mode_at.
func recordedMode(t *testing.T, srv *server) (mode string, at time.Time) {
	t.Helper()
	if err := srv.deps.Pool.QueryRow(context.Background(),
		`SELECT coalesce(authority_mode, ''), coalesce(authority_mode_at, 'epoch') FROM identity.system_settings WHERE id = 1`).Scan(&mode, &at); err != nil {
		t.Fatal(err)
	}
	return mode, at
}

// The API process records the mode the single sign-on settings derive —
// federated with OIDC or SCIM on, else local — with when; recording
// the same mode again changes nothing, and a change is recorded and audited
// authority-mode.changed with no actor, the modes before and after. The
// maintenance settings the row also holds are left as they were.
func TestRecordAuthorityMode_RecordsTheDerivedModeAndAuditsAChange(t *testing.T) {
	t.Parallel()
	srv, pool := newInternalServer(t)
	ctx := context.Background()
	d := srv.deps
	local, federated, oidc := ssoSettings{}, ssoSettings{SCIM: &scimSettings{}}, ssoSettings{OIDC: &oidcSettings{}}
	if got := deriveAuthorityMode(local); got != "local" {
		t.Errorf("derived without OIDC or SCIM = %q, want local", got)
	}
	if deriveAuthorityMode(federated) != "federated" || deriveAuthorityMode(oidc) != "federated" {
		t.Error("OIDC or SCIM does not derive federated")
	}

	if err := recordAuthorityMode(ctx, d, local); err != nil {
		t.Fatal(err)
	}
	if mode, at := recordedMode(t, srv); mode != "local" || !at.Equal(internalNow) {
		t.Errorf("recorded %q at %v, want local at %v", mode, at, internalNow)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM identity.system_settings WHERE id = 1 AND NOT maintenance_enabled AND maintenance_message IS NULL`); n != 1 {
		t.Error("the first record did not leave maintenance off")
	}
	if n := countRows(t, pool, `SELECT count(*) FROM identity.authorization_audit_events WHERE action = 'authority-mode.changed'`); n != 0 {
		t.Errorf("%d changes audited for the first record, want none", n)
	}
	if _, err := pool.Exec(ctx, `UPDATE identity.system_settings SET maintenance_enabled = true, maintenance_message = 'kept'`); err != nil {
		t.Fatal(err)
	}

	later := d
	later.Clock = func() time.Time { return internalNow.Add(time.Hour) }
	if err := recordAuthorityMode(ctx, d, local); err != nil {
		t.Fatal(err)
	}
	if _, at := recordedMode(t, srv); !at.Equal(internalNow) {
		t.Errorf("the same mode recorded again moved its time to %v", at)
	}
	if err := recordAuthorityMode(ctx, later, federated); err != nil {
		t.Fatal(err)
	}
	if mode, at := recordedMode(t, srv); mode != "federated" || !at.Equal(internalNow.Add(time.Hour)) {
		t.Errorf("recorded %q at %v, want federated an hour on", mode, at)
	}
	var details, before, after string
	var actor *string
	if err := pool.QueryRow(ctx, `SELECT actor_user_id::text, details, before_json, after_json FROM identity.authorization_audit_events WHERE action = 'authority-mode.changed'`).
		Scan(&actor, &details, &before, &after); err != nil {
		t.Fatalf("the change's audit: %v", err)
	}
	if actor != nil || details != `{"action":"authority-mode.changed"}` || before != `{"mode":"local"}` || after != `{"mode":"federated"}` {
		t.Errorf("audit = actor %v, %s, %s → %s", actor, details, before, after)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM identity.system_settings WHERE maintenance_enabled AND maintenance_message = 'kept'`); n != 1 {
		t.Error("recording the mode changed the maintenance settings")
	}
}
