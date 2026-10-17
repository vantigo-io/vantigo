package identity_test

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/identity"
)

// The invitation that completes a pending user (staff accounts plan, Task
// 1b; pre-flight I7): Staff's provisioning creates the user pending and
// invites them by name, and accepting sets their password on the account
// that already exists, instead of creating one.

const completingAudit = "invitation.accepted-completing-user"

// pendingUser is a pending Staff-managed User named name, enabled and
// without a credential, as Staff's provisioning leaves one before it issues
// the invitation: made by SQL, since no endpoint makes one yet.
func pendingUser(t *testing.T, h *harness, email, name, number string, roles ...uuid.UUID) uuid.UUID {
	t.Helper()
	id := h.insertUser(t, email, roles...)
	makePending(t, h, id, number)
	h.exec(t, `UPDATE identity.users SET display_name = $2 WHERE id = $1`, id, name)
	return id
}

// issueNamed issues a User invitation to email naming user (nil for none)
// through identity's issue, and returns its id and token.
func issueNamed(t *testing.T, h *harness, email string, user *uuid.UUID) (uuid.UUID, string) {
	t.Helper()
	id, token, err := identity.IssueInvitationForTest(context.Background(), h.pool, email, user, h.now())
	if err != nil {
		t.Fatalf("issue a named invitation to %s: %v", email, err)
	}
	return id, token
}

// userState is what completion may change on a user, as one string.
func userState(t *testing.T, h *harness, id uuid.UUID) string {
	t.Helper()
	var s string
	ctx, cancel := context.WithTimeout(context.Background(), fixtureTimeout)
	defer cancel()
	if err := h.pool.QueryRow(ctx, `
		SELECT concat_ws(':', display_name, password_hash IS NOT NULL, email_confirmed, pending_since IS NOT NULL,
		                 coalesce(managed_by, '-'), coalesce(employee_number, '-'), is_disabled, version)
		FROM identity.users WHERE id = $1`, id).Scan(&s); err != nil {
		t.Fatalf("user state %s: %v", id, err)
	}
	return s
}

// An invitation naming a pending user completes that user: the password is
// set, the email confirmed and the pending mark cleared on the existing row;
// the user keeps their display name (the request's is ignored) and their
// roles; the invitation is accepted, invitation.accepted-completing-user is
// audited with no actor, and the user is signed in.
func TestInvitations_ANamedInvitationCompletesAPendingUser(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.bootstrapOwner(t)
	const email = "pending@example.test"
	id := pendingUser(t, h, email, "Pending Person", "E-1", identity.RoleUserID)
	users := h.count(t, `SELECT count(*) FROM identity.users`)
	invID, token := issueNamed(t, h, email, &id)

	invitee := h.client(t)
	r := accept(invitee, token, inviteePassword, "A Name Of Their Own")
	if r.status != http.StatusCreated {
		t.Fatalf("accept: status %d body %s", r.status, r.body)
	}
	var body struct {
		User struct {
			ID          uuid.UUID `json:"id"`
			DisplayName string    `json:"displayName"`
			Email       *string   `json:"email"`
			Roles       []string  `json:"roles"`
		} `json:"user"`
		MfaEnrollmentRequired bool `json:"mfaEnrollmentRequired"`
	}
	r.json(&body)
	if body.User.ID != id || body.User.DisplayName != "Pending Person" || str(body.User.Email) != email ||
		!slices.Equal(body.User.Roles, []string{identity.RoleUser}) || body.MfaEnrollmentRequired {
		t.Errorf("accept body = %s, want the pending user, their own name and roles", r.body)
	}
	admitted(t, invitee)

	if n := h.count(t, `SELECT count(*) FROM identity.users`); n != users {
		t.Errorf("%d users after the acceptance, want %d: none created", n, users)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.users WHERE id = $1 AND password_hash IS NOT NULL AND email_confirmed
	                     AND pending_since IS NULL AND display_name = 'Pending Person' AND managed_by = 'staff' AND employee_number = 'E-1'
	                     AND NOT is_disabled`, id); n != 1 {
		t.Errorf("the user is not completed as it should be: %s", userState(t, h, id))
	}
	if n := h.count(t, `SELECT count(*) FROM identity.user_roles WHERE user_id = $1`, id); n != 1 {
		t.Errorf("%d roles, want the one User role unchanged", n)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.invitations WHERE id = $1 AND accepted_at = $2`, invID, h.now()); n != 1 {
		t.Errorf("the invitation is not marked accepted at the clock")
	}
	events := h.auditEvents(t, completingAudit)
	if len(events) != 1 {
		t.Fatalf("%d %s rows, want 1", len(events), completingAudit)
	}
	e := events[0]
	wantBefore := `{"InvitationId":"` + invID.String() + `","Roles":["User"]}`
	wantAfter := `{"UserId":"` + id.String() + `","Roles":["User"],"PermissionKeys":[],"IsDisabled":false,"IsDeleted":false}`
	if e.Actor != nil || e.TargetUser == nil || *e.TargetUser != id || e.Details != `{"action":"`+completingAudit+`"}` ||
		e.Before != wantBefore || e.After != wantAfter || e.MFA {
		t.Errorf("audit = %+v, want no actor, the user, before %s and after %s", e, wantBefore, wantAfter)
	}
	if n := len(h.auditEvents(t, "invitation.accepted-with-role")); n != 0 {
		t.Errorf("%d invitation.accepted-with-role rows, want 0", n)
	}
	h.login(t, email, inviteePassword)

	// Spent: the token completes nothing twice.
	if r := accept(h.client(t), token, inviteePassword, ""); r.status != http.StatusBadRequest || r.code() != "invitation_invalid" {
		t.Errorf("second acceptance: status %d code %q, want 400 invitation_invalid", r.status, r.code())
	}
}

// mfaEnrollmentRequired is judged on the roles the user really holds, not
// the invitation's User: a pending user holding SystemAdmin must enrol.
func TestInvitations_CompletionJudgesMFAOnTheUsersRoles(t *testing.T) {
	t.Parallel()
	h := newHarness(t, withEnv("OWNERS_REQUIRE_MFA", "1"))
	const email = "pending-admin@example.test"
	id := pendingUser(t, h, email, "Pending Admin", "E-2", identity.RoleUserID, identity.RoleSystemAdminID)
	_, token := issueNamed(t, h, email, &id)

	r := accept(h.client(t), token, inviteePassword, "")
	if r.status != http.StatusCreated {
		t.Fatalf("accept: status %d body %s", r.status, r.body)
	}
	var body struct {
		User struct {
			Roles []string `json:"roles"`
		} `json:"user"`
		MfaEnrollmentRequired bool `json:"mfaEnrollmentRequired"`
	}
	r.json(&body)
	if !body.MfaEnrollmentRequired || !slices.Equal(body.User.Roles, []string{identity.RoleSystemAdmin, identity.RoleUser}) {
		t.Errorf("accept body = %s, want SystemAdmin and User and mfaEnrollmentRequired", r.body)
	}
}

// An invitation naming a user it cannot complete is the uniform 400
// invitation_invalid, and nothing is written: no user created or changed,
// the invitation not accepted, no audit row. Each case is a pending user one
// condition away from completable — or no user at all.
func TestInvitations_ANamedInvitationRefusesWhatItCannotComplete(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.bootstrapOwner(t)
	cases := []struct {
		name  string
		setup func(t *testing.T, id uuid.UUID) (email string, named uuid.UUID)
	}{
		{"names nobody", func(*testing.T, uuid.UUID) (string, uuid.UUID) { return "nobody@example.test", uuid.New() }},
		{"another address", func(*testing.T, uuid.UUID) (string, uuid.UUID) { return "elsewhere@example.test", uuid.Nil }},
		{"disabled", func(t *testing.T, id uuid.UUID) (string, uuid.UUID) {
			h.exec(t, `UPDATE identity.users SET is_disabled = true, disabled_by = 'staff' WHERE id = $1`, id)
			return "", uuid.Nil
		}},
		{"disabled by an Owner", func(t *testing.T, id uuid.UUID) (string, uuid.UUID) {
			h.exec(t, `UPDATE identity.users SET is_disabled = true, disabled_by = 'owner' WHERE id = $1`, id)
			return "", uuid.Nil
		}},
		{"unmanaged", func(t *testing.T, id uuid.UUID) (string, uuid.UUID) {
			h.exec(t, `UPDATE identity.users SET managed_by = NULL WHERE id = $1`, id)
			return "", uuid.Nil
		}},
		{"completed", func(t *testing.T, id uuid.UUID) (string, uuid.UUID) {
			h.exec(t, `UPDATE identity.users SET pending_since = NULL WHERE id = $1`, id)
			return "", uuid.Nil
		}},
		{"a password", func(t *testing.T, id uuid.UUID) (string, uuid.UUID) {
			hash, err := identity.HashPassword(userPassword)
			if err != nil {
				t.Fatal(err)
			}
			h.exec(t, `UPDATE identity.users SET password_hash = $2 WHERE id = $1`, id, hash)
			return "", uuid.Nil
		}},
		{"a passkey", func(t *testing.T, id uuid.UUID) (string, uuid.UUID) {
			h.exec(t, `INSERT INTO identity.passkeys (credential_id, user_id, name, credential, user_verified, backup_eligible, backed_up, created_at)
			           VALUES ($1, $2, 'Key', '{}', true, false, false, $3)`, id[:], id, h.now())
			return "", uuid.Nil
		}},
		{"an OIDC link", func(t *testing.T, id uuid.UUID) (string, uuid.UUID) {
			h.exec(t, `INSERT INTO identity.oidc_links (issuer, subject, user_id, created_at) VALUES ('https://idp.example.test', $1, $2, $3)`,
				id.String(), id, h.now())
			return "", uuid.Nil
		}},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			email := "case" + string(rune('a'+i)) + "@example.test"
			id := pendingUser(t, h, email, "Case "+c.name, "C-"+string(rune('A'+i)), identity.RoleUserID)
			invEmail, named := c.setup(t, id)
			if invEmail == "" {
				invEmail = email
			}
			if named == uuid.Nil {
				named = id
			}
			before := userState(t, h, id)
			users := h.count(t, `SELECT count(*) FROM identity.users`)
			invID, token := issueNamed(t, h, invEmail, &named)

			r := accept(h.client(t), token, inviteePassword, "")
			if r.status != http.StatusBadRequest || r.code() != "invitation_invalid" {
				t.Fatalf("accept: status %d code %q body %s, want 400 invitation_invalid", r.status, r.code(), r.body)
			}
			if got := userState(t, h, id); got != before {
				t.Errorf("user after the refusal = %s, want %s", got, before)
			}
			if n := h.count(t, `SELECT count(*) FROM identity.users`); n != users {
				t.Errorf("%d users after the refusal, want %d", n, users)
			}
			if n := h.count(t, `SELECT count(*) FROM identity.users WHERE normalized_email = upper($1)`, invEmail); invEmail != email && n != 0 {
				t.Errorf("an account was created for %s", invEmail)
			}
			if n := h.count(t, `SELECT count(*) FROM identity.invitations WHERE id = $1 AND accepted_at IS NULL`, invID); n != 1 {
				t.Errorf("the invitation was accepted")
			}
		})
	}
	if n := len(h.auditEvents(t, completingAudit)) + len(h.auditEvents(t, "invitation.accepted-with-role")); n != 0 {
		t.Errorf("%d acceptance audit rows, want 0", n)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.sessions s JOIN identity.users u ON u.id = s.user_id WHERE u.email LIKE 'case%'`); n != 0 {
		t.Errorf("%d sessions for the refused users, want 0", n)
	}
}

// A resend of a named invitation names the same user, and its new token
// completes them.
func TestInvitations_ResendCarriesTheNamedUser(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	const email = "resend-pending@example.test"
	id := pendingUser(t, h, email, "Resend Pending", "E-3", identity.RoleUserID)
	first, _ := issueNamed(t, h, email, &id)

	r := owner.do(http.MethodPost, ownerInvitationsPath+"/"+first.String()+"/resend", nil)
	if r.status != http.StatusOK {
		t.Fatalf("resend: status %d body %s", r.status, r.body)
	}
	var second invitation
	r.json(&second)
	if n := h.count(t, `SELECT count(*) FROM identity.invitations WHERE id = $1 AND user_id = $2`, second.ID, id); n != 1 {
		t.Errorf("the resent invitation does not name the user")
	}
	token := mailedLink(t, h, email).Query().Get("token")
	if r := accept(h.client(t), token, inviteePassword, ""); r.status != http.StatusCreated {
		t.Fatalf("accept the resent invitation: status %d body %s", r.status, r.body)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.users WHERE id = $1 AND pending_since IS NULL AND password_hash IS NOT NULL`, id); n != 1 {
		t.Errorf("the resent invitation did not complete the user")
	}
}

// An Owner cannot invite a pending user's address by hand: the account
// exists, 409 account_exists, and no invitation is made.
func TestInvitations_TheOwnersInviteOfAPendingAddressIsAccountExists(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	const email = "owner-invites-pending@example.test"
	pendingUser(t, h, email, "Pending", "E-4", identity.RoleUserID)

	r := owner.do(http.MethodPost, ownerInvitationsPath, map[string]string{"email": strings.ToUpper(email), "role": identity.RoleUser})
	if r.status != http.StatusConflict || r.code() != "account_exists" {
		t.Errorf("invite a pending address: status %d code %q, want 409 account_exists", r.status, r.code())
	}
	if n := h.count(t, `SELECT count(*) FROM identity.invitations`); n != 0 {
		t.Errorf("%d invitations, want none", n)
	}
	if n := len(h.mailTo(email)) + len(h.mailTo(strings.ToUpper(email))); n != 0 {
		t.Errorf("%d mails, want none", n)
	}
}

// The owner's invitation list says why a revoked invitation was revoked,
// and says nothing for one that is not.
func TestInvitations_TheListShowsTheRevokeReason(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	first, _ := invite(t, h, owner, map[string]string{"email": "reason@example.test", "role": identity.RoleUser})
	r := owner.do(http.MethodPost, ownerInvitationsPath+"/"+first.ID.String()+"/resend", nil)
	if r.status != http.StatusOK {
		t.Fatalf("resend: status %d body %s", r.status, r.body)
	}
	if strings.Contains(string(r.body), "revokedReason") {
		t.Errorf("the active invitation's body %s names a revoke reason", r.body)
	}
	var second invitation
	r.json(&second)
	r = owner.do(http.MethodPost, ownerInvitationsPath+"/"+second.ID.String()+"/revoke", nil)
	if r.status != http.StatusOK || !strings.Contains(string(r.body), `"revokedReason":"owner"`) {
		t.Errorf("revoke: status %d body %s, want revokedReason owner", r.status, r.body)
	}
	other, _ := invite(t, h, owner, map[string]string{"email": "reason-active@example.test", "role": identity.RoleUser})

	r = owner.do(http.MethodGet, ownerInvitationsPath, nil)
	var list []struct {
		ID            uuid.UUID `json:"id"`
		RevokedReason *string   `json:"revokedReason"`
	}
	r.json(&list)
	reasons := map[uuid.UUID]string{}
	for _, inv := range list {
		reasons[inv.ID] = str(inv.RevokedReason)
	}
	if want := map[uuid.UUID]string{first.ID: "replaced", second.ID: "owner", other.ID: "<nil>"}; len(reasons) != 3 ||
		reasons[first.ID] != want[first.ID] || reasons[second.ID] != want[second.ID] || reasons[other.ID] != want[other.ID] {
		t.Errorf("revoke reasons = %v, want %v", reasons, want)
	}
}

// An invitation naming a user never grants its stored role nor runs the
// first Owner's bootstrap: one whose role reads Owner (no code path stores
// one; a test does), accepted where no Owner exists yet, completes the user
// with the roles they hold and leaves the bootstrap unconsumed.
func TestInvitations_ANamedInvitationGrantsNoRole(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const email = "named-owner@example.test"
	id := pendingUser(t, h, email, "Named Owner", "E-5", identity.RoleUserID)
	invID, token := issueNamed(t, h, email, &id)
	h.exec(t, `UPDATE identity.invitations SET role = 'Owner' WHERE id = $1`, invID)

	r := accept(h.client(t), token, inviteePassword, "")
	if r.status != http.StatusCreated {
		t.Fatalf("accept: status %d body %s", r.status, r.body)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.user_roles WHERE user_id = $1`, id); n != 1 {
		t.Errorf("%d roles, want the one User role", n)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.user_roles WHERE user_id = $1 AND role_id <> $2`, id, identity.RoleUserID); n != 0 {
		t.Errorf("the user was granted a role")
	}
	if n := h.count(t, `SELECT count(*) FROM identity.bootstrap_state`); n != 0 {
		t.Errorf("the bootstrap marker was written")
	}
}

// Completion takes the owner lock before the invitation's and the user's
// row locks: while a raw connection holds the owner lock, the acceptance
// waits on it (pg_blocking_pids) holding neither row; released, it
// completes the user.
func TestInvitations_CompletionTakesTheOwnerLockFirst(t *testing.T) {
	h := newHarness(t)
	const email = "lock-order@example.test"
	id := pendingUser(t, h, email, "Lock Order", "E-6", identity.RoleUserID)
	invID, token := issueNamed(t, h, email, &id)

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	connect := func() *pgx.Conn {
		c, err := pgx.Connect(ctx, h.cfg.DatabaseURL)
		if err != nil {
			t.Fatalf("connect: %v", err)
		}
		t.Cleanup(func() { _ = c.Close(context.Background()) })
		return c
	}
	holder := connect()
	tx, err := holder.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, int64(identity.OwnerMutationLock)); err != nil {
		t.Fatalf("take the owner lock: %v", err)
	}
	var holderPID int
	if err := tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&holderPID); err != nil {
		t.Fatalf("holder pid: %v", err)
	}

	result := make(chan *resp, 1)
	go func() { result <- accept(h.client(t), token, inviteePassword, "") }()
	deadline := time.Now().Add(15 * time.Second)
	for h.count(t, `SELECT count(*) FROM pg_stat_activity WHERE $1 = ANY (pg_blocking_pids(pid))`, holderPID) != 1 {
		if time.Now().After(deadline) {
			_ = tx.Rollback(ctx)
			t.Fatal("the acceptance never waited on the owner lock")
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Parked on the owner lock, it holds neither row.
	probe := connect()
	for _, q := range []struct {
		sql string
		id  uuid.UUID
	}{
		{`SELECT 1 FROM identity.invitations WHERE id = $1 FOR UPDATE NOWAIT`, invID},
		{`SELECT 1 FROM identity.users WHERE id = $1 FOR UPDATE NOWAIT`, id},
	} {
		ptx, err := probe.Begin(ctx)
		if err != nil {
			t.Fatalf("begin the probe: %v", err)
		}
		if _, err := ptx.Exec(ctx, q.sql, q.id); err != nil {
			t.Errorf("%s while the acceptance waits: %v, want the row free", q.sql, err)
		}
		_ = ptx.Rollback(ctx)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("release the owner lock: %v", err)
	}
	if r := <-result; r.status != http.StatusCreated {
		t.Fatalf("accept: status %d body %s", r.status, r.body)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.users WHERE id = $1 AND pending_since IS NULL`, id); n != 1 {
		t.Errorf("the user was not completed")
	}
}
