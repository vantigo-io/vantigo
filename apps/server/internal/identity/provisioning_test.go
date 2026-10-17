package identity_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/identity"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

// Identity's account provisioning provider (staff accounts plan, Task 2b):
// contracts.AccountProvisioning against the real identity, in process, each
// call on identity's own pool in its own transactions.

// The two actors a provisioning call names.
var systemActor = contracts.Actor{System: contracts.SystemActorStaffAccounts}

func userActor(id uuid.UUID) contracts.Actor { return contracts.Actor{UserID: &id} }

// accounts builds the module's provider over the harness's dependencies, as
// Compose and Workers build it. Each call builds a new instance: the provider
// holds no state, so two instances answer the same.
func (h *harness) accounts(t testing.TB) contracts.AccountProvisioning {
	t.Helper()
	build := identity.Module(h.access).Accounts
	if build == nil {
		t.Fatal("the module declares no account provisioning")
	}
	return build(h.deps)
}

// spec is an AccountSpec for a person numbered number, at email.
func spec(email, number string) contracts.AccountSpec {
	return contracts.AccountSpec{Email: email, DisplayName: "Person " + number, PreferredLanguage: "nb", EmployeeNumber: number}
}

// employeeRole names a custom role carrying staff:access as the set Staff
// may give, and returns it.
func employeeRole(t *testing.T, h *harness) uuid.UUID {
	t.Helper()
	role := h.insertRole(t, "Employee", "staff:access")
	h.exec(t, `INSERT INTO identity.provisioning_roles (role_id, created_at) VALUES ($1, $2)`, role, h.now())
	return role
}

// mustPrepare prepares spec as the system and returns the ref.
func mustPrepare(t *testing.T, p contracts.AccountProvisioning, s contracts.AccountSpec) contracts.AccountRef {
	t.Helper()
	ref, err := p.Prepare(context.Background(), systemActor, s)
	if err != nil {
		t.Fatalf("Prepare(%s): %v", s.EmployeeNumber, err)
	}
	return ref
}

// mustActivate activates id as the system and returns the ref.
func mustActivate(t *testing.T, p contracts.AccountProvisioning, id uuid.UUID, opts contracts.ActivateOpts) contracts.AccountRef {
	t.Helper()
	ref, err := p.Activate(context.Background(), systemActor, id, opts)
	if err != nil {
		t.Fatalf("Activate(%s): %v", id, err)
	}
	return ref
}

// accountOf reads id through the provider, failing t when it is unknown.
func accountOf(t *testing.T, p contracts.AccountProvisioning, id uuid.UUID, want string) contracts.AccountRef {
	t.Helper()
	ref, err := p.Account(context.Background(), id, want)
	if err != nil || ref == nil {
		t.Fatalf("Account(%s) = %v, %v", id, ref, err)
	}
	return *ref
}

// wantErr fails t unless err is target.
func wantErr(t *testing.T, what string, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Errorf("%s: %v, want %v", what, err, target)
	}
}

// inviteToken is the token of the latest invitation mailed to email.
func inviteToken(t *testing.T, h *harness, email string) string {
	t.Helper()
	msgs := h.mailTo(email)
	if len(msgs) == 0 {
		t.Fatalf("no mail to %s", email)
	}
	_, token, ok := strings.Cut(msgs[len(msgs)-1].TextBody, "?token=")
	if !ok {
		t.Fatalf("the mail to %s carries no token: %q", email, msgs[len(msgs)-1].TextBody)
	}
	return token
}

// footprint is how many rows a provider's write leaves behind: users,
// role assignments, invitations, audit rows and live sessions, and the
// version of every user, as one string.
func footprint(t *testing.T, h *harness) string {
	t.Helper()
	var s string
	ctx, cancel := context.WithTimeout(context.Background(), fixtureTimeout)
	defer cancel()
	if err := h.pool.QueryRow(ctx, `SELECT concat_ws(':',
		(SELECT count(*) FROM identity.users), (SELECT count(*) FROM identity.user_roles),
		(SELECT count(*) FROM identity.invitations), (SELECT count(*) FROM identity.invitations WHERE revoked_at IS NOT NULL),
		(SELECT count(*) FROM identity.authorization_audit_events), (SELECT count(*) FROM identity.sessions WHERE revoked_at IS NULL),
		(SELECT string_agg(version::text || is_disabled::text, ',' ORDER BY id) FROM identity.users))`).Scan(&s); err != nil {
		t.Fatalf("footprint: %v", err)
	}
	return s
}

// An actor that is not Valid — none, the nil user, an unknown system, or
// both a user and a system — is refused ErrInvalidActor by every write,
// before anything is written, audited or mailed.
func TestProvisioning_AnInvalidActorWritesNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	employeeRole(t, h)
	p := h.accounts(t)
	ref := mustPrepare(t, p, spec("valid@example.test", "V-1"))
	before, mails := footprint(t, h), len(h.mail.Messages())

	nilUser, someone := uuid.Nil, uuid.New()
	for name, actor := range map[string]contracts.Actor{
		"none":            {},
		"the nil user":    {UserID: &nilUser},
		"another system":  {System: "system:other"},
		"user and system": {UserID: &someone, System: contracts.SystemActorStaffAccounts},
	} {
		ctx := context.Background()
		_, err := p.Prepare(ctx, actor, spec("other@example.test", "V-2"))
		wantErr(t, name+": Prepare", err, contracts.ErrInvalidActor)
		_, err = p.Activate(ctx, actor, ref.UserID, contracts.ActivateOpts{FreshInvitation: true})
		wantErr(t, name+": Activate", err, contracts.ErrInvalidActor)
		_, err = p.Deactivate(ctx, actor, ref.UserID)
		wantErr(t, name+": Deactivate", err, contracts.ErrInvalidActor)
		_, err = p.Update(ctx, actor, ref.UserID, spec("changed@example.test", "V-3"))
		wantErr(t, name+": Update", err, contracts.ErrInvalidActor)
	}
	if after := footprint(t, h); after != before {
		t.Errorf("footprint %s after the refusals, want %s", after, before)
	}
	if n := len(h.mail.Messages()); n != mails {
		t.Errorf("%d mails after the refusals, want %d", n, mails)
	}
}

// Prepare in local mode creates the user Staff asks for: Staff-managed with
// the number, pending, disabled by Staff, no credential, the email
// unconfirmed, the built-in User role (plan reading 5) and none of the
// employee roles yet, audited user.staff-prepared as the system; the
// address's unnamed invitation is replaced. Called again, by the number
// however it is cased, it finds the same user and writes nothing.
func TestProvisioning_PrepareCreatesAPendingManagedUser(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	role := employeeRole(t, h)
	const email = "kari@example.test"
	if r := owner.do(http.MethodPost, "/api/v1/identity/owner/invitations", map[string]string{"email": email, "role": identity.RoleUser}); r.status != http.StatusCreated {
		t.Fatalf("an Owner's invitation: %d %s", r.status, r.body)
	}
	p := h.accounts(t)

	ref := mustPrepare(t, p, contracts.AccountSpec{Email: " " + email + " ", DisplayName: " Kari Nordmann ", PreferredLanguage: "nb", EmployeeNumber: " e-17 "})
	if ref.UserID == uuid.Nil || ref.State != contracts.AccountStatePending || ref.DisabledBy != contracts.DisabledByStaff || !ref.Managed ||
		ref.EmployeeNumber != "e-17" || ref.HasCredential || ref.EmailDiffers || ref.Invitation != (contracts.Invitation{State: contracts.InvitationNone}) {
		t.Errorf("ref = %+v", ref)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.users WHERE id = $1 AND email = $2 AND normalized_email = upper($2) AND NOT email_confirmed
	                     AND display_name = 'Kari Nordmann' AND preferred_language = 'nb' AND password_hash IS NULL
	                     AND is_disabled AND disabled_by = 'staff' AND managed_by = 'staff' AND employee_number = 'e-17' AND employee_number_key = 'E-17'
	                     AND pending_since = $3 AND invitations_since = $3`, ref.UserID, email, h.now()); n != 1 {
		t.Errorf("the user is not as prepared: %s", userState(t, h, ref.UserID))
	}
	if n := h.count(t, `SELECT count(*) FROM identity.user_roles WHERE user_id = $1 AND role_id = $2`, ref.UserID, identity.RoleUserID); n != 1 {
		t.Error("the user lacks the built-in User role")
	}
	if n := h.count(t, `SELECT count(*) FROM identity.user_roles WHERE user_id = $1 AND role_id = $2`, ref.UserID, role); n != 0 {
		t.Error("Prepare gave an employee role; only Activate gives them")
	}
	if n := h.count(t, `SELECT count(*) FROM identity.invitations WHERE normalized_email = upper($1) AND revoked_reason = 'replaced' AND user_id IS NULL`, email); n != 1 {
		t.Error("the Owner's invitation to the address was not replaced")
	}
	events := h.auditEvents(t, "user.staff-prepared")
	if len(events) != 1 || events[0].Actor != nil || events[0].Details != `{"action":"user.staff-prepared","actor":"system:staff-accounts"}` ||
		events[0].TargetUser == nil || *events[0].TargetUser != ref.UserID {
		t.Errorf("audit = %+v, want one user.staff-prepared row by the system", events)
	}

	before := footprint(t, h)
	again := mustPrepare(t, p, spec("another@example.test", "E-17"))
	if again != ref {
		t.Errorf("Prepare again = %+v, want %+v", again, ref)
	}
	if after := footprint(t, h); after != before {
		t.Errorf("Prepare again wrote: %s → %s", before, after)
	}
}

// A user's actor is written as the audit row's actor, with the action alone
// in the details and no correlation id.
func TestProvisioning_AUserActorIsAudited(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, ownerID := h.bootstrapOwner(t)
	ref, err := h.accounts(t).Prepare(context.Background(), userActor(ownerID), spec("by-hand@example.test", "H-1"))
	if err != nil {
		t.Fatal(err)
	}
	events := h.auditEvents(t, "user.staff-prepared")
	if len(events) != 1 || events[0].Actor == nil || *events[0].Actor != ownerID || events[0].Details != `{"action":"user.staff-prepared"}` {
		t.Errorf("audit = %+v, want the user as actor", events)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.authorization_audit_events WHERE target_user_id = $1 AND correlation_id IS NOT NULL`, ref.UserID); n != 0 {
		t.Errorf("%d rows with a correlation id, want none", n)
	}
}

// Prepare's refusals, each writing nothing: an address another user holds,
// however it is cased (ErrAccountEmailTaken); a number an unmanaged user
// holds (ErrEmployeeNumberTaken); a managed Owner or SystemAdmin found by
// the number (ErrAccountPrivileged).
func TestProvisioning_PrepareRefusals(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	p := h.accounts(t)
	taken := h.createUser(t, owner, "taken@example.test", identity.RoleUser)
	mustManage(t, owner, taken, "T-1")
	h.exec(t, `UPDATE identity.users SET managed_by = NULL WHERE id = $1`, taken) // holds T-1 unmanaged
	privileged := h.insertUser(t, "admin@example.test", identity.RoleUserID, identity.RoleSystemAdminID)
	makePending(t, h, privileged, "A-1")
	h.exec(t, `UPDATE identity.users SET pending_since = NULL WHERE id = $1`, privileged)
	before := footprint(t, h)

	_, err := p.Prepare(context.Background(), systemActor, spec("TAKEN@example.test", "N-1"))
	wantErr(t, "a taken address", err, contracts.ErrAccountEmailTaken)
	_, err = p.Prepare(context.Background(), systemActor, spec("new@example.test", "t-1"))
	wantErr(t, "an unmanaged holder of the number", err, contracts.ErrEmployeeNumberTaken)
	_, err = p.Prepare(context.Background(), systemActor, spec("new@example.test", "A-1"))
	wantErr(t, "a managed SystemAdmin", err, contracts.ErrAccountPrivileged)
	for _, bad := range []contracts.AccountSpec{
		spec("new@example.test", "Å-1"), spec("new@example.test", ""), spec("not an address", "N-2"),
		{Email: "new@example.test", DisplayName: " ", PreferredLanguage: "nb", EmployeeNumber: "N-3"},
		{Email: "new@example.test", DisplayName: "N", PreferredLanguage: "sv", EmployeeNumber: "N-4"},
	} {
		if _, err := p.Prepare(context.Background(), systemActor, bad); err == nil {
			t.Errorf("Prepare(%+v) succeeded, want it refused", bad)
		}
	}
	if after := footprint(t, h); after != before {
		t.Errorf("the refusals wrote: %s → %s", before, after)
	}
}

// Two numbers racing: an unmanaged user takes the number in a transaction
// committed while Prepare waits on the unique index; Prepare answers
// ErrEmployeeNumberTaken and creates no user.
func TestProvisioning_PrepareLosesANumberRace(t *testing.T) {
	h := newHarness(t)
	p := h.accounts(t)
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, h.cfg.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	other := uuid.New()
	if _, err := tx.Exec(ctx, `INSERT INTO identity.users (id, email, normalized_email, display_name, employee_number, employee_number_key, version, created_at, updated_at)
		VALUES ($1, 'other@example.test', 'OTHER@EXAMPLE.TEST', 'Other', 'R-1', 'R-1', gen_random_uuid(), now(), now())`, other); err != nil {
		t.Fatal(err)
	}
	var holderPID int
	if err := tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&holderPID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := p.Prepare(ctx, systemActor, spec("racer@example.test", "r-1"))
		done <- err
	}()
	deadline := time.Now().Add(15 * time.Second)
	for h.count(t, `SELECT count(*) FROM pg_stat_activity WHERE $1 = ANY (pg_blocking_pids(pid))`, holderPID) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("Prepare never waited on the holder's number")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	wantErr(t, "the race's loser", <-done, contracts.ErrEmployeeNumberTaken)
	if n := h.count(t, `SELECT count(*) FROM identity.users WHERE normalized_email = 'RACER@EXAMPLE.TEST'`); n != 0 {
		t.Error("the loser created a user")
	}
}

// Activate in local mode gives a pending user the employee roles, enables
// them (disabled_by cleared; still pending, so still unable to sign in) and
// invites them by name, mailed after its commit; the ref says the
// invitation is active, one delivered. Accepting it completes the user,
// who then holds the role. Activate again changes nothing and mails nothing,
// and once the user is active an Owner's removal of the role is not undone.
func TestProvisioning_ActivateEnablesAndInvitesAPendingUser(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.bootstrapOwner(t)
	role := employeeRole(t, h)
	p := h.accounts(t)
	const email = "ola@example.test"
	ref := mustPrepare(t, p, spec(email, "O-1"))

	got := mustActivate(t, p, ref.UserID, contracts.ActivateOpts{})
	if got.State != contracts.AccountStatePending || got.DisabledBy != "" || got.Invitation != (contracts.Invitation{State: contracts.InvitationActive, Issued: 1}) {
		t.Errorf("ref = %+v, want pending, enabled, one active invitation", got)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.users WHERE id = $1 AND NOT is_disabled AND disabled_by IS NULL AND pending_since IS NOT NULL`, ref.UserID); n != 1 {
		t.Errorf("the user is not enabled and pending: %s", userState(t, h, ref.UserID))
	}
	if n := h.count(t, `SELECT count(*) FROM identity.user_roles WHERE user_id = $1 AND role_id = $2`, ref.UserID, role); n != 1 {
		t.Error("the employee role was not given")
	}
	if n := h.count(t, `SELECT count(*) FROM identity.invitations WHERE user_id = $1 AND email = $2 AND role = 'User' AND invited_by_user_id IS NULL AND revoked_at IS NULL`, ref.UserID, email); n != 1 {
		t.Error("no named invitation to the user's address")
	}
	if events := h.auditEvents(t, "user.staff-activated"); len(events) != 1 || events[0].Actor != nil {
		t.Errorf("audit = %+v, want one user.staff-activated by the system", events)
	}
	if n := len(h.mailTo(email)); n != 1 {
		t.Fatalf("%d mails, want the invitation", n)
	}
	if c := h.client(t); c.do(http.MethodPost, "/api/v1/identity/login", map[string]string{"email": email, "password": userPassword}).status == http.StatusOK {
		t.Error("a pending user signed in before accepting")
	}

	before := footprint(t, h)
	if again := mustActivate(t, p, ref.UserID, contracts.ActivateOpts{}); again != got {
		t.Errorf("Activate again = %+v, want %+v", again, got)
	}
	if after := footprint(t, h); after != before || len(h.mailTo(email)) != 1 {
		t.Errorf("Activate again wrote or mailed: %s → %s, %d mails", before, after, len(h.mailTo(email)))
	}

	if r := accept(h.client(t), inviteToken(t, h, email), inviteePassword, ""); r.status != http.StatusCreated {
		t.Fatalf("accept: %d %s", r.status, r.body)
	}
	if active := accountOf(t, p, ref.UserID, email); active.State != contracts.AccountStateActive || !active.HasCredential || active.Invitation.State != contracts.InvitationNone {
		t.Errorf("after the acceptance ref = %+v, want active with a credential", active)
	}
	h.exec(t, `DELETE FROM identity.user_roles WHERE user_id = $1 AND role_id = $2`, ref.UserID, role)
	mustActivate(t, p, ref.UserID, contracts.ActivateOpts{})
	if n := h.count(t, `SELECT count(*) FROM identity.user_roles WHERE user_id = $1 AND role_id = $2`, ref.UserID, role); n != 0 {
		t.Error("Activate gave back a role an Owner removed from an active user")
	}
}

// Activate's refusals in local mode: no employee role named
// (ErrEmployeeRoleMissing), a role in the set that has come to carry an
// administrative key by a write outside identity's checks
// (ErrEmployeeRoleUnsafe), an Owner's disable (ErrBlockedByOwner), an
// unmanaged user, a managed privileged one, and a user who is gone — each
// writing and mailing nothing.
func TestProvisioning_ActivateRefusals(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	p := h.accounts(t)
	ref := mustPrepare(t, p, spec("refused@example.test", "F-1"))
	plain := h.createUser(t, owner, "plain@example.test", identity.RoleUser)
	admin := h.insertUser(t, "admin@example.test", identity.RoleUserID, identity.RoleSystemAdminID)
	makePending(t, h, admin, "F-2")
	ctx := context.Background()

	before := footprint(t, h)
	_, err := p.Activate(ctx, systemActor, ref.UserID, contracts.ActivateOpts{})
	wantErr(t, "no employee role", err, contracts.ErrEmployeeRoleMissing)
	if after := footprint(t, h); after != before {
		t.Errorf("the missing role's refusal wrote: %s → %s", before, after)
	}

	role := employeeRole(t, h)
	h.exec(t, `INSERT INTO identity.role_permissions (role_id, permission_key) VALUES ($1, 'staff:manage')`, role)
	before = footprint(t, h)
	_, err = p.Activate(ctx, systemActor, ref.UserID, contracts.ActivateOpts{})
	wantErr(t, "a role carrying staff:manage", err, contracts.ErrEmployeeRoleUnsafe)
	h.exec(t, `DELETE FROM identity.role_permissions WHERE role_id = $1 AND permission_key = 'staff:manage'`, role)
	h.exec(t, `INSERT INTO identity.role_permissions (role_id, permission_key) VALUES ($1, 'identity:manage')`, role)
	_, err = p.Activate(ctx, systemActor, ref.UserID, contracts.ActivateOpts{})
	wantErr(t, "a role carrying identity:manage", err, contracts.ErrEmployeeRoleUnsafe)
	h.exec(t, `DELETE FROM identity.role_permissions WHERE role_id = $1 AND permission_key = 'identity:manage'`, role)

	_, err = p.Activate(ctx, systemActor, plain, contracts.ActivateOpts{})
	wantErr(t, "an unmanaged user", err, contracts.ErrAccountNotManaged)
	_, err = p.Activate(ctx, systemActor, admin, contracts.ActivateOpts{})
	wantErr(t, "a managed SystemAdmin", err, contracts.ErrAccountPrivileged)
	_, err = p.Activate(ctx, systemActor, uuid.New(), contracts.ActivateOpts{})
	wantErr(t, "a user who is gone", err, contracts.ErrUserGone)
	if after := footprint(t, h); after != before || len(h.mail.Messages()) != 0 {
		t.Errorf("the refusals wrote or mailed: %s → %s, %d mails", before, after, len(h.mail.Messages()))
	}

	if r := owner.do(http.MethodPost, userPath(ref.UserID, "/disable"), nil); r.status != http.StatusOK {
		t.Fatalf("the Owner's disable: %d %s", r.status, r.body)
	}
	before = footprint(t, h)
	_, err = p.Activate(ctx, systemActor, ref.UserID, contracts.ActivateOpts{FreshInvitation: true})
	wantErr(t, "an Owner's disable", err, contracts.ErrBlockedByOwner)
	if got := accountOf(t, p, ref.UserID, ""); got.DisabledBy != contracts.DisabledByOwner {
		t.Errorf("ref = %+v, want disabled by an Owner", got)
	}
	if after := footprint(t, h); after != before || len(h.mail.Messages()) != 0 {
		t.Errorf("the blocked Activate wrote or mailed: %s → %s", before, after)
	}
}

// Activate invites only a pending user who could complete an invitation:
// one holding a passkey or an OIDC link (or a password) gets none.
func TestProvisioning_ActivateInvitesNoUserWithACredential(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	employeeRole(t, h)
	p := h.accounts(t)
	for i, kind := range []string{"password", "passkey", "oidc"} {
		email := kind + "@example.test"
		ref := mustPrepare(t, p, spec(email, "C-"+string(rune('1'+i))))
		giveCredential(t, h, ref.UserID, kind)
		got := mustActivate(t, p, ref.UserID, contracts.ActivateOpts{})
		if n := len(h.mailTo(email)); n != 0 || got.Invitation.State != contracts.InvitationNone || !got.HasCredential {
			t.Errorf("%s: %d mails, ref %+v, want none and a credential", kind, n, got)
		}
		if n := h.count(t, `SELECT count(*) FROM identity.invitations WHERE user_id = $1`, ref.UserID); n != 0 {
			t.Errorf("%s: %d invitations, want none", kind, n)
		}
	}
}

// An invitation that lapses unanswered is re-issued by the next Activate
// while fewer than three were delivered in the spell; the third lapsed is
// exhausted and none is issued. A failed delivery revokes its invitation
// delivery_failed, fails the call, and is not counted. FreshInvitation
// starts a new spell and issues again.
func TestProvisioning_InvitationsAreReissuedUpToThreeDelivered(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	employeeRole(t, h)
	p := h.accounts(t)
	const email = "lapse@example.test"
	ref := mustPrepare(t, p, spec(email, "L-1"))
	lapse := func() { h.advance(h.cfg.InvitationLifetime + time.Minute) }

	mustActivate(t, p, ref.UserID, contracts.ActivateOpts{})
	lapse()
	if got := accountOf(t, p, ref.UserID, ""); got.Invitation != (contracts.Invitation{State: contracts.InvitationExpired, Issued: 1}) {
		t.Errorf("after a lapse = %+v, want expired, one", got.Invitation)
	}
	h.mail.FailNext(errors.New("the mail server is away"))
	if _, err := p.Activate(context.Background(), systemActor, ref.UserID, contracts.ActivateOpts{}); err == nil {
		t.Error("Activate succeeded although its invitation was not delivered")
	}
	if n := h.count(t, `SELECT count(*) FROM identity.invitations WHERE user_id = $1 AND revoked_reason = 'delivery_failed'`, ref.UserID); n != 1 {
		t.Error("the undelivered invitation was not revoked delivery_failed")
	}
	if got := accountOf(t, p, ref.UserID, ""); got.Invitation.Issued != 1 {
		t.Errorf("issued = %d after a failed delivery, want 1", got.Invitation.Issued)
	}
	if got := mustActivate(t, p, ref.UserID, contracts.ActivateOpts{}); got.Invitation != (contracts.Invitation{State: contracts.InvitationActive, Issued: 2}) {
		t.Errorf("the retry = %+v, want active, two", got.Invitation)
	}
	lapse()
	mustActivate(t, p, ref.UserID, contracts.ActivateOpts{})
	lapse()
	got := mustActivate(t, p, ref.UserID, contracts.ActivateOpts{})
	if got.Invitation != (contracts.Invitation{State: contracts.InvitationExhausted, Issued: 3}) || len(h.mailTo(email)) != 3 {
		t.Errorf("after three lapsed = %+v and %d mails, want exhausted, three", got.Invitation, len(h.mailTo(email)))
	}
	h.advance(time.Minute)
	fresh := mustActivate(t, p, ref.UserID, contracts.ActivateOpts{FreshInvitation: true})
	if fresh.Invitation != (contracts.Invitation{State: contracts.InvitationActive, Issued: 1}) || len(h.mailTo(email)) != 4 {
		t.Errorf("a fresh invitation = %+v and %d mails, want active, one, a fourth mail", fresh.Invitation, len(h.mailTo(email)))
	}
}

// An Owner's revoke of a pending user's invitation is reported
// revoked_by_owner and never undone by Activate; FreshInvitation (Staff's
// Run now) overrides it and starts a new spell.
func TestProvisioning_AnOwnersRevokeHoldsUntilAFreshInvitation(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	employeeRole(t, h)
	p := h.accounts(t)
	const email = "revoked@example.test"
	ref := mustPrepare(t, p, spec(email, "W-1"))
	mustActivate(t, p, ref.UserID, contracts.ActivateOpts{})
	var invID uuid.UUID
	if err := h.pool.QueryRow(context.Background(), `SELECT id FROM identity.invitations WHERE user_id = $1`, ref.UserID).Scan(&invID); err != nil {
		t.Fatal(err)
	}
	if r := owner.do(http.MethodPost, "/api/v1/identity/owner/invitations/"+invID.String()+"/revoke", nil); r.status != http.StatusOK {
		t.Fatalf("the Owner's revoke: %d %s", r.status, r.body)
	}
	got := mustActivate(t, p, ref.UserID, contracts.ActivateOpts{})
	if got.Invitation != (contracts.Invitation{State: contracts.InvitationRevokedByOwner, Issued: 1}) || len(h.mailTo(email)) != 1 {
		t.Errorf("after the revoke = %+v and %d mails, want revoked_by_owner and no new mail", got.Invitation, len(h.mailTo(email)))
	}
	h.advance(time.Minute)
	if early := mustActivate(t, p, ref.UserID, contracts.ActivateOpts{FreshInvitation: true}); early.Invitation.State != contracts.InvitationRevokedByOwner || len(h.mailTo(email)) != 1 {
		t.Errorf("a fresh invitation within ten minutes of the last = %+v and %d mails, want the revoke kept and no mail", early.Invitation, len(h.mailTo(email)))
	}
	h.advance(10 * time.Minute)
	fresh := mustActivate(t, p, ref.UserID, contracts.ActivateOpts{FreshInvitation: true})
	if fresh.Invitation != (contracts.Invitation{State: contracts.InvitationActive, Issued: 1}) || len(h.mailTo(email)) != 2 {
		t.Errorf("the fresh invitation = %+v and %d mails, want active, one", fresh.Invitation, len(h.mailTo(email)))
	}
}

// A fresh invitation never re-sends one that can still be accepted: while
// the latest is open, FreshInvitation changes nothing and mails nothing.
// After a lapse, two fresh calls within ten minutes send one invitation.
func TestProvisioning_AFreshInvitationNeverResendsAnOpenOne(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	employeeRole(t, h)
	p := h.accounts(t)
	const email = "open@example.test"
	ref := mustPrepare(t, p, spec(email, "N-1"))
	mustActivate(t, p, ref.UserID, contracts.ActivateOpts{})
	open := func() []uuid.UUID {
		rows, err := h.pool.Query(context.Background(), `SELECT id FROM identity.invitations WHERE user_id = $1 AND revoked_at IS NULL AND accepted_at IS NULL ORDER BY id`, ref.UserID)
		if err != nil {
			t.Fatal(err)
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil {
			t.Fatal(err)
		}
		return ids
	}
	first := open()
	h.advance(time.Hour)
	got := mustActivate(t, p, ref.UserID, contracts.ActivateOpts{FreshInvitation: true})
	if now := open(); len(now) != 1 || now[0] != first[0] || len(h.mailTo(email)) != 1 {
		t.Errorf("a fresh call while open: invitations %v (was %v), %d mails, want the same one and one mail", now, first, len(h.mailTo(email)))
	}
	if got.Invitation != (contracts.Invitation{State: contracts.InvitationActive, Issued: 1}) {
		t.Errorf("ref = %+v, want the open invitation, one", got.Invitation)
	}

	h.advance(h.cfg.InvitationLifetime)
	mustActivate(t, p, ref.UserID, contracts.ActivateOpts{FreshInvitation: true})
	h.advance(5 * time.Minute)
	mustActivate(t, p, ref.UserID, contracts.ActivateOpts{FreshInvitation: true})
	if n := len(h.mailTo(email)); n != 2 {
		t.Errorf("%d mails after two fresh calls within ten minutes of a lapse, want 2", n)
	}
}

// Activate gives the roles only on the call that enables the user: a
// pending user Staff has already enabled is given nothing again, so an
// Owner's removal of a role before they accept is not undone.
func TestProvisioning_ActivateGivesTheRolesOnlyWhenItEnables(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	role := employeeRole(t, h)
	p := h.accounts(t)
	ref := mustPrepare(t, p, spec("removed@example.test", "R-7"))
	mustActivate(t, p, ref.UserID, contracts.ActivateOpts{})
	h.exec(t, `DELETE FROM identity.user_roles WHERE user_id = $1 AND role_id = $2`, ref.UserID, role)
	before := footprint(t, h)
	mustActivate(t, p, ref.UserID, contracts.ActivateOpts{})
	if n := h.count(t, `SELECT count(*) FROM identity.user_roles WHERE user_id = $1 AND role_id = $2`, ref.UserID, role); n != 0 {
		t.Error("Activate gave back a role an Owner removed from a pending user")
	}
	if after := footprint(t, h); after != before {
		t.Errorf("Activate of an enabled pending user wrote: %s → %s", before, after)
	}
	h.exec(t, `DELETE FROM identity.provisioning_roles`)
	if _, err := p.Activate(context.Background(), systemActor, ref.UserID, contracts.ActivateOpts{}); err != nil {
		t.Errorf("Activate of an enabled user with no set named = %v, want nothing to give and no refusal", err)
	}
}

// Deactivate disables a managed user as Staff's, ends their sessions,
// revokes their acceptable invitations deactivated and audits
// user.staff-deactivated; again it changes nothing. An Owner's disable is
// kept as the Owner's. Activate then re-enables what Staff disabled.
func TestProvisioning_DeactivateDisablesEndsSessionsAndRevokes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	employeeRole(t, h)
	p := h.accounts(t)
	ref := mustPrepare(t, p, spec("leaver@example.test", "D-1"))
	mustActivate(t, p, ref.UserID, contracts.ActivateOpts{})
	h.session(t, ref.UserID, false)

	got, err := p.Deactivate(context.Background(), systemActor, ref.UserID)
	if err != nil {
		t.Fatal(err)
	}
	if got.DisabledBy != contracts.DisabledByStaff || got.Invitation.State == contracts.InvitationActive {
		t.Errorf("ref = %+v, want disabled by Staff, no active invitation", got)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.users WHERE id = $1 AND is_disabled AND disabled_by = 'staff'`, ref.UserID); n != 1 {
		t.Error("the user is not disabled by Staff")
	}
	if n := h.count(t, `SELECT count(*) FROM identity.sessions WHERE user_id = $1 AND revoked_at IS NULL`, ref.UserID); n != 0 {
		t.Errorf("%d live sessions, want none", n)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.invitations WHERE user_id = $1 AND revoked_reason = 'deactivated'`, ref.UserID); n != 1 {
		t.Error("the invitation was not revoked deactivated")
	}
	if events := h.auditEvents(t, "user.staff-deactivated"); len(events) != 1 {
		t.Errorf("%d user.staff-deactivated rows, want 1", len(events))
	}
	before := footprint(t, h)
	if _, err := p.Deactivate(context.Background(), systemActor, ref.UserID); err != nil {
		t.Fatal(err)
	}
	if after := footprint(t, h); after != before {
		t.Errorf("Deactivate again wrote: %s → %s", before, after)
	}

	if back := mustActivate(t, p, ref.UserID, contracts.ActivateOpts{}); back.DisabledBy != "" || back.Invitation.State != contracts.InvitationActive {
		t.Errorf("re-activated ref = %+v, want enabled and invited again", back)
	}
	h.exec(t, `UPDATE identity.users SET is_disabled = true, disabled_by = 'owner' WHERE id = $1`, ref.UserID)
	if kept, err := p.Deactivate(context.Background(), systemActor, ref.UserID); err != nil || kept.DisabledBy != contracts.DisabledByOwner {
		t.Errorf("Deactivate over an Owner's disable = %+v, %v, want the Owner's kept", kept, err)
	}
}

// Deactivate's refusals: an unmanaged user, the last active Owner (only a
// managed Owner made by SQL can be one), a managed SystemAdmin, a user who
// is gone.
func TestProvisioning_DeactivateRefusals(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	p := h.accounts(t)
	lastOwner := h.insertUser(t, "last-owner@example.test", identity.RoleOwnerID)
	makePending(t, h, lastOwner, "Z-1")
	h.exec(t, `UPDATE identity.users SET pending_since = NULL WHERE id = $1`, lastOwner)
	admin := h.insertUser(t, "admin@example.test", identity.RoleUserID, identity.RoleSystemAdminID)
	makePending(t, h, admin, "Z-2")
	plain := h.insertUser(t, "plain@example.test", identity.RoleUserID)
	before := footprint(t, h)
	ctx := context.Background()

	_, err := p.Deactivate(ctx, systemActor, plain)
	wantErr(t, "an unmanaged user", err, contracts.ErrAccountNotManaged)
	_, err = p.Deactivate(ctx, systemActor, lastOwner)
	wantErr(t, "the last active Owner", err, contracts.ErrLastOwner)
	_, err = p.Deactivate(ctx, systemActor, admin)
	wantErr(t, "a managed SystemAdmin", err, contracts.ErrAccountPrivileged)
	_, err = p.Deactivate(ctx, systemActor, uuid.New())
	wantErr(t, "a user who is gone", err, contracts.ErrUserGone)
	if after := footprint(t, h); after != before {
		t.Errorf("the refusals wrote: %s → %s", before, after)
	}
}

// Update of a pending user in local mode writes the name, the language, the
// number and the address; the new address revokes the user's invitations
// email_changed and starts a new spell. A user who accepted keeps their
// address — the ref says it differs — while the rest still syncs.
func TestProvisioning_UpdateWritesTheAddressOnlyWhilePending(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	employeeRole(t, h)
	p := h.accounts(t)
	ref := mustPrepare(t, p, spec("first@example.test", "U-1"))
	mustActivate(t, p, ref.UserID, contracts.ActivateOpts{})
	h.advance(time.Minute)

	got, err := p.Update(context.Background(), systemActor, ref.UserID,
		contracts.AccountSpec{Email: "second@example.test", DisplayName: "Renamed", PreferredLanguage: "en", EmployeeNumber: "U-2"})
	if err != nil {
		t.Fatal(err)
	}
	if got.EmployeeNumber != "U-2" || got.Invitation != (contracts.Invitation{State: contracts.InvitationNone}) {
		t.Errorf("ref = %+v, want U-2 and a new spell with nothing issued", got)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.users WHERE id = $1 AND email = 'second@example.test' AND normalized_email = 'SECOND@EXAMPLE.TEST'
	                     AND display_name = 'Renamed' AND preferred_language = 'en' AND employee_number_key = 'U-2' AND invitations_since = $2`, ref.UserID, h.now()); n != 1 {
		t.Errorf("the user is not as updated: %s", userState(t, h, ref.UserID))
	}
	if n := h.count(t, `SELECT count(*) FROM identity.invitations WHERE user_id = $1 AND revoked_reason = 'email_changed'`, ref.UserID); n != 1 {
		t.Error("the invitation to the old address was not revoked email_changed")
	}
	if events := h.auditEvents(t, "user.staff-updated"); len(events) != 1 {
		t.Errorf("%d user.staff-updated rows, want 1", len(events))
	}
	// The same values again, the number spelled otherwise: the number is
	// kept as given, nothing else written.
	if _, err := p.Update(context.Background(), systemActor, ref.UserID,
		contracts.AccountSpec{Email: "SECOND@example.test", DisplayName: "Renamed", PreferredLanguage: "en", EmployeeNumber: "u-2"}); err != nil {
		t.Fatal(err)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.users WHERE id = $1 AND email = 'second@example.test' AND employee_number = 'u-2'`, ref.UserID); n != 1 {
		t.Errorf("a case-only address and a respelled number: %s", userState(t, h, ref.UserID))
	}

	if invited := mustActivate(t, p, ref.UserID, contracts.ActivateOpts{}); invited.Invitation != (contracts.Invitation{State: contracts.InvitationActive, Issued: 1}) {
		t.Errorf("the new address's invitation = %+v, want active, one in the new spell", invited.Invitation)
	}
	if r := accept(h.client(t), inviteToken(t, h, "second@example.test"), inviteePassword, ""); r.status != http.StatusCreated {
		t.Fatalf("accept: %d %s", r.status, r.body)
	}
	got, err = p.Update(context.Background(), systemActor, ref.UserID,
		contracts.AccountSpec{Email: "third@example.test", DisplayName: "Renamed Again", PreferredLanguage: "nb", EmployeeNumber: "U-3"})
	if err != nil {
		t.Fatal(err)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.users WHERE id = $1 AND email = 'second@example.test' AND display_name = 'Renamed Again'
	                     AND preferred_language = 'nb' AND employee_number = 'U-3'`, ref.UserID); n != 1 {
		t.Errorf("an active user's update: %s, want the address kept and the rest written", userState(t, h, ref.UserID))
	}
	if !accountOf(t, p, ref.UserID, "third@example.test").EmailDiffers || accountOf(t, p, ref.UserID, "Second@Example.test").EmailDiffers {
		t.Error("EmailDiffers does not compare the address as identity does")
	}
}

// Update's refusals, each writing nothing: an address another user holds
// while pending (ErrAccountEmailTaken), a number another user holds
// (ErrEmployeeNumberTaken), an unmanaged user, a user who is gone.
func TestProvisioning_UpdateRefusals(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	p := h.accounts(t)
	ref := mustPrepare(t, p, spec("mine@example.test", "Q-1"))
	other := mustPrepare(t, p, spec("theirs@example.test", "Q-2"))
	plain := h.insertUser(t, "plain@example.test", identity.RoleUserID)
	before := footprint(t, h)
	ctx := context.Background()

	_, err := p.Update(ctx, systemActor, ref.UserID, spec("THEIRS@example.test", "Q-1"))
	wantErr(t, "a taken address", err, contracts.ErrAccountEmailTaken)
	_, err = p.Update(ctx, systemActor, ref.UserID, spec("mine@example.test", "q-2"))
	wantErr(t, "a taken number", err, contracts.ErrEmployeeNumberTaken)
	_, err = p.Update(ctx, systemActor, plain, spec("plain@example.test", "Q-3"))
	wantErr(t, "an unmanaged user", err, contracts.ErrAccountNotManaged)
	_, err = p.Update(ctx, systemActor, uuid.New(), spec("gone@example.test", "Q-4"))
	wantErr(t, "a user who is gone", err, contracts.ErrUserGone)
	if after := footprint(t, h); after != before {
		t.Errorf("the refusals wrote: %s → %s", before, after)
	}
	_ = other
}

// Account reports a user, or nil for an unknown id. EmailDiffers compares
// as identity compares addresses: a difference of case or surrounding space
// is none, an empty wanted address and a placeholder address report none.
// Under SCIM a directory-deactivated non-Owner reads disabled by the
// directory; without SCIM the same user reads as identity holds them.
func TestProvisioning_AccountReportsTheUser(t *testing.T) {
	t.Parallel()
	for _, scim := range []bool{true, false} {
		var opts []harnessOption
		if scim {
			opts = append(opts, withEnv("SCIM_TOKEN", scimToken))
		}
		h := newHarness(t, opts...)
		p := h.accounts(t)
		if ref, err := p.Account(context.Background(), uuid.New(), ""); ref != nil || err != nil {
			t.Errorf("an unknown user = %v, %v, want nil, nil", ref, err)
		}
		id := h.insertUser(t, "Case@Example.test", identity.RoleUserID)
		makePending(t, h, id, "S-1")
		h.exec(t, `UPDATE identity.users SET pending_since = NULL WHERE id = $1`, id)
		for want, differs := range map[string]bool{"case@example.TEST": false, " case@example.test ": false, "": false, "other@example.test": true} {
			if got := accountOf(t, p, id, want).EmailDiffers; got != differs {
				t.Errorf("EmailDiffers(%q) = %v, want %v", want, got, differs)
			}
		}
		placeholder := h.insertUser(t, "scim-"+uuid.NewString()+"@sso.invalid", identity.RoleUserID)
		if accountOf(t, p, placeholder, "someone@example.test").EmailDiffers {
			t.Error("a placeholder address reported a difference")
		}
		h.exec(t, `INSERT INTO identity.scim_user_mappings (resource_id, user_id, external_id, user_name, upstream_active, version, etag, created_at, updated_at)
		        VALUES ($1, $2, $3, $3, false, 1, 'etag', $4, $4)`, uuid.New(), id, id.String(), h.now())
		got := accountOf(t, p, id, "")
		if scim && (got.State != contracts.AccountStateDisabled || got.DisabledBy != contracts.DisabledByDirectory) {
			t.Errorf("under SCIM a directory-deactivated user = %+v, want disabled by the directory", got)
		}
		if !scim && (got.State != contracts.AccountStateActive || got.DisabledBy != "") {
			t.Errorf("without SCIM the same user = %+v, want active", got)
		}
		if got.HasCredential || !got.Managed || got.EmployeeNumber != "S-1" {
			t.Errorf("ref = %+v", got)
		}
	}
}

// A leaver loses access whatever the directory says (S23): Deactivate
// disables a managed user identity holds enabled even while the directory
// already reports them inactive — SCIM never writes is_disabled, and the
// directory's deactivation stops sessions only while SCIM is configured. Once
// the directory reactivates them, or SCIM is gone, the user reads disabled by
// Staff and still cannot sign in; Activate (a rehire) re-enables what Staff
// disabled.
func TestProvisioning_DeactivateDisablesWhateverTheDirectorySays(t *testing.T) {
	t.Parallel()
	for _, scim := range []bool{true, false} {
		var opts []harnessOption
		if scim {
			opts = append(opts, withEnv("SCIM_TOKEN", scimToken))
		}
		h := newHarness(t, opts...)
		h.bootstrapOwner(t)
		employeeRole(t, h)
		p := h.accounts(t)
		const email = "dir@example.test"
		id := h.seedUser(t, email, userPassword, identity.RoleUserID)
		makePending(t, h, id, "Y-1")
		h.exec(t, `UPDATE identity.users SET pending_since = NULL WHERE id = $1`, id)
		h.exec(t, `INSERT INTO identity.scim_user_mappings (resource_id, user_id, external_id, user_name, upstream_active, version, etag, created_at, updated_at)
		        VALUES ($1, $2, $3, $3, false, 1, 'etag', $4, $4)`, uuid.New(), id, id.String(), h.now())
		token := h.session(t, id, false)

		if _, err := p.Deactivate(context.Background(), systemActor, id); err != nil {
			t.Fatalf("SCIM %v: %v", scim, err)
		}
		if n := h.count(t, `SELECT count(*) FROM identity.users WHERE id = $1 AND is_disabled AND disabled_by = 'staff'`, id); n != 1 {
			t.Errorf("SCIM %v: the leaver is not disabled by Staff: %s", scim, userState(t, h, id))
		}
		if n := h.count(t, `SELECT count(*) FROM identity.sessions WHERE user_id = $1 AND revoked_at IS NULL`, id); n != 0 {
			t.Errorf("SCIM %v: %d live sessions, want none", scim, n)
		}
		h.exec(t, `UPDATE identity.scim_user_mappings SET upstream_active = true WHERE user_id = $1`, id)
		if got := accountOf(t, p, id, ""); got.State != contracts.AccountStateDisabled || got.DisabledBy != contracts.DisabledByStaff {
			t.Errorf("SCIM %v: reactivated upstream = %+v, want disabled by Staff", scim, got)
		}
		for name, tok := range map[string]string{"old": token, "new": h.session(t, id, false)} {
			if _, o := check(t, h, tok, "session"); o == allowed {
				t.Errorf("SCIM %v: the leaver's %s session is admitted", scim, name)
			}
		}
		if r := h.client(t).do(http.MethodPost, "/api/v1/identity/login", map[string]string{"email": email, "password": userPassword}); r.status == http.StatusOK {
			t.Errorf("SCIM %v: the leaver signed in", scim)
		}
		if back := mustActivate(t, p, id, contracts.ActivateOpts{}); back.State != contracts.AccountStateActive || back.DisabledBy != "" {
			t.Errorf("SCIM %v: the rehire = %+v, want active", scim, back)
		}
	}
}

// Activate re-enables a user Staff disabled even while the directory
// reports them inactive (it still blocks their sessions while SCIM is
// configured), and the ref reports the directory's deactivation meanwhile.
func TestProvisioning_ActivateReenablesUnderAnInactiveDirectory(t *testing.T) {
	t.Parallel()
	h := newHarness(t, withEnv("SCIM_TOKEN", scimToken))
	owner, _ := h.bootstrapOwner(t)
	p := h.accounts(t)
	id := h.createUser(t, owner, "rehire@example.test", identity.RoleUser)
	mustManage(t, owner, id, "Y-2")
	if _, err := p.Deactivate(context.Background(), systemActor, id); err != nil {
		t.Fatal(err)
	}
	h.exec(t, `INSERT INTO identity.scim_user_mappings (resource_id, user_id, external_id, user_name, upstream_active, version, etag, created_at, updated_at)
	        VALUES ($1, $2, $3, $3, false, 1, 'etag', $4, $4)`, uuid.New(), id, id.String(), h.now())
	got := mustActivate(t, p, id, contracts.ActivateOpts{})
	if n := h.count(t, `SELECT count(*) FROM identity.users WHERE id = $1 AND NOT is_disabled AND disabled_by IS NULL`, id); n != 1 {
		t.Errorf("the Staff-disabled user was not re-enabled: %s", userState(t, h, id))
	}
	if got.State != contracts.AccountStateDisabled || got.DisabledBy != contracts.DisabledByDirectory {
		t.Errorf("ref = %+v, want disabled by the directory while it says inactive", got)
	}
}

// A leaver is disabled even when this process's configuration derives
// another mode than the recorded one: Deactivate reads no mode. Every other
// write still answers ErrModeMismatch.
func TestProvisioning_DeactivateIgnoresAModeMismatch(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	employeeRole(t, h)
	p := h.accounts(t)
	ref := mustPrepare(t, p, spec("mismatch@example.test", "M-9"))
	mustActivate(t, p, ref.UserID, contracts.ActivateOpts{})
	h.exec(t, `UPDATE identity.system_settings SET authority_mode = 'federated'`)
	ctx := context.Background()
	_, err := p.Prepare(ctx, systemActor, spec("other@example.test", "M-8"))
	wantErr(t, "Prepare", err, contracts.ErrModeMismatch)
	_, err = p.Activate(ctx, systemActor, ref.UserID, contracts.ActivateOpts{})
	wantErr(t, "Activate", err, contracts.ErrModeMismatch)
	_, err = p.Update(ctx, systemActor, ref.UserID, spec("mismatch@example.test", "M-9"))
	wantErr(t, "Update", err, contracts.ErrModeMismatch)
	if _, err := p.Deactivate(ctx, systemActor, ref.UserID); err != nil {
		t.Fatalf("Deactivate under a mismatch: %v", err)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.users WHERE id = $1 AND is_disabled AND disabled_by = 'staff'`, ref.UserID); n != 1 {
		t.Errorf("the leaver is not disabled: %s", userState(t, h, ref.UserID))
	}
}

// In federated mode Prepare finds by number only (ErrAccountNotFound, no
// user created, for a number nobody holds); Update writes the language and
// the number, never the name or the address; Deactivate disables, and
// Activate re-enables only what Staff disabled — no role, no invitation — and
// refuses an Owner's disable.
func TestProvisioning_FederatedLinksAndNeverCreates(t *testing.T) {
	t.Parallel()
	h := newHarness(t, withEnv("SCIM_TOKEN", scimToken))
	owner, _ := h.bootstrapOwner(t)
	employeeRole(t, h)
	p := h.accounts(t)
	if mode, err := p.Mode(context.Background()); err != nil || mode != contracts.AccountModeFederated {
		t.Fatalf("Mode = %q, %v, want federated", mode, err)
	}
	users := h.count(t, `SELECT count(*) FROM identity.users`)
	_, err := p.Prepare(context.Background(), systemActor, spec("nobody@example.test", "G-0"))
	wantErr(t, "a number nobody holds", err, contracts.ErrAccountNotFound)
	if n := h.count(t, `SELECT count(*) FROM identity.users`); n != users {
		t.Error("federated Prepare created a user")
	}
	id := h.createUser(t, owner, "idp@example.test", identity.RoleUser)
	mustManage(t, owner, id, "G-1")
	roles := h.count(t, `SELECT count(*) FROM identity.user_roles WHERE user_id = $1`, id)
	if ref := mustPrepare(t, p, spec("ignored@example.test", "g-1")); ref.UserID != id || ref.State != contracts.AccountStateActive {
		t.Errorf("federated Prepare = %+v, want the adopted user", ref)
	}
	if _, err := p.Update(context.Background(), systemActor, id,
		contracts.AccountSpec{Email: "new@example.test", DisplayName: "New Name", PreferredLanguage: "en", EmployeeNumber: "G-2"}); err != nil {
		t.Fatal(err)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.users WHERE id = $1 AND email = 'idp@example.test' AND display_name = 'idp@example.test'
	                     AND preferred_language = 'en' AND employee_number = 'G-2'`, id); n != 1 {
		t.Errorf("federated Update: %s, want language and number only", userState(t, h, id))
	}
	if _, err := p.Deactivate(context.Background(), systemActor, id); err != nil {
		t.Fatal(err)
	}
	back := mustActivate(t, p, id, contracts.ActivateOpts{FreshInvitation: true})
	if back.State != contracts.AccountStateActive || back.DisabledBy != "" {
		t.Errorf("federated re-enable = %+v, want active", back)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.user_roles WHERE user_id = $1`, id); n != roles {
		t.Errorf("federated Activate changed the roles: %d, want %d", n, roles)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.invitations WHERE user_id = $1`, id); n != 0 || len(h.mailTo("idp@example.test")) != 0 {
		t.Error("federated Activate invited")
	}
	if r := owner.do(http.MethodPost, userPath(id, "/disable"), nil); r.status != http.StatusOK {
		t.Fatalf("the Owner's disable: %d %s", r.status, r.body)
	}
	_, err = p.Activate(context.Background(), systemActor, id, contracts.ActivateOpts{})
	wantErr(t, "federated over an Owner's disable", err, contracts.ErrBlockedByOwner)
}

// Mode answers the recorded mode; when this process's own configuration
// derives another, Mode and every write but Deactivate answer
// ErrModeMismatch and nothing is written; with no mode recorded yet, the derivation. Nothing is cached:
// the recorded value is read on every call, so two instances agree and see a
// change at once.
func TestProvisioning_ModeIsTheRecordedOne(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	employeeRole(t, h)
	first, second := h.accounts(t), h.accounts(t)
	ref := mustPrepare(t, first, spec("mode@example.test", "M-1"))
	ctx := context.Background()
	for _, p := range []contracts.AccountProvisioning{first, second} {
		if mode, err := p.Mode(ctx); err != nil || mode != contracts.AccountModeLocal {
			t.Errorf("Mode = %q, %v, want local", mode, err)
		}
	}

	h.exec(t, `UPDATE identity.system_settings SET authority_mode = 'federated'`)
	before := footprint(t, h)
	for _, p := range []contracts.AccountProvisioning{first, second} {
		_, err := p.Mode(ctx)
		wantErr(t, "Mode", err, contracts.ErrModeMismatch)
		_, err = p.Prepare(ctx, systemActor, spec("other@example.test", "M-2"))
		wantErr(t, "Prepare", err, contracts.ErrModeMismatch)
		_, err = p.Activate(ctx, systemActor, ref.UserID, contracts.ActivateOpts{})
		wantErr(t, "Activate", err, contracts.ErrModeMismatch)
		_, err = p.Update(ctx, systemActor, ref.UserID, spec("mode@example.test", "M-3"))
		wantErr(t, "Update", err, contracts.ErrModeMismatch)
	}
	if after := footprint(t, h); after != before || len(h.mail.Messages()) != 0 {
		t.Errorf("a mismatch wrote or mailed: %s → %s", before, after)
	}

	h.exec(t, `UPDATE identity.system_settings SET authority_mode = NULL`)
	if mode, err := second.Mode(ctx); err != nil || mode != contracts.AccountModeLocal {
		t.Errorf("unrecorded Mode = %q, %v, want the derivation", mode, err)
	}
	if a, b := accountOf(t, first, ref.UserID, "x@example.test"), accountOf(t, second, ref.UserID, "x@example.test"); a != b {
		t.Errorf("two instances disagree: %+v and %+v", a, b)
	}
}

// ProvisioningRoles lists the set Staff may give by name; empty when none
// is named.
func TestProvisioning_ProvisioningRoles(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	p := h.accounts(t)
	if roles, err := p.ProvisioningRoles(context.Background()); err != nil || len(roles) != 0 {
		t.Errorf("no set = %+v, %v, want none", roles, err)
	}
	b := h.insertRole(t, "Bravo", "staff:access")
	a := h.insertRole(t, "Alpha", "staff:access")
	h.exec(t, `INSERT INTO identity.provisioning_roles (role_id, created_at) VALUES ($1, $3), ($2, $3)`, b, a, h.now())
	roles, err := p.ProvisioningRoles(context.Background())
	if err != nil || len(roles) != 2 || roles[0] != (contracts.RoleRef{ID: a, Name: "Alpha"}) || roles[1] != (contracts.RoleRef{ID: b, Name: "Bravo"}) {
		t.Errorf("roles = %+v, %v, want Alpha then Bravo", roles, err)
	}
}

// A pending Staff-managed user meeting workforce OIDC's first sign-in by
// their address is account_locked — never linked, never a second user —
// whether Staff has enabled them yet or not.
func TestProvisioning_APendingUserMeetingOIDCIsLocked(t *testing.T) {
	t.Parallel()
	f := newFakeOIDC(t)
	h := newHarness(t, f.options()...)
	id := h.insertUser(t, fakeEmail, identity.RoleUserID)
	makePending(t, h, id, "J-1") // enabled, as after Activate
	for _, disabled := range []bool{false, true} {
		h.exec(t, `UPDATE identity.users SET is_disabled = $2, disabled_by = CASE WHEN $2 THEN 'staff' END WHERE id = $1`, id, disabled)
		assertOIDCRedirect(t, h.oidcSignIn(t, h.client(t), f), oidcErrorLocation("account_locked"))
	}
	if n := h.count(t, `SELECT count(*) FROM identity.users`); n != 1 {
		t.Errorf("%d users, want the pending one only", n)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.oidc_links`); n != 0 {
		t.Errorf("%d links, want none", n)
	}
}

// SCIM's create of a User at a pending Staff-managed user's address is
// refused DuplicateEmail, with no second user.
func TestProvisioning_SCIMCreateMeetsAPendingUser(t *testing.T) {
	t.Parallel()
	h := newScimHarness(t)
	pendingUser(t, h, "pending-scim@example.test", "Kari", "K-1", identity.RoleUserID)
	users := h.count(t, `SELECT count(*) FROM identity.users`)
	r := h.scim(t, scimToken).do(http.MethodPost, "/Users", userResource("kari", map[string]any{
		"emails": []map[string]any{{"value": "Pending-Scim@example.test", "type": "work"}},
	}))
	wantScimError(t, "create at a pending address", r, http.StatusBadRequest, "invalidValue", "DuplicateEmail")
	if n := h.count(t, `SELECT count(*) FROM identity.users`); n != users {
		t.Errorf("%d users, want %d", n, users)
	}
}

// Contract coverage for AccountProvisioning (plan M10): every method called
// in both modes against the real provider, each answering what the contract
// says for a Staff-managed user.
func TestProvisioning_EveryMethodInBothModes(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{contracts.AccountModeLocal, contracts.AccountModeFederated} {
		var opts []harnessOption
		if mode == contracts.AccountModeFederated {
			opts = append(opts, withEnv("SCIM_TOKEN", scimToken))
		}
		h := newHarness(t, opts...)
		owner, _ := h.bootstrapOwner(t)
		employeeRole(t, h)
		p := h.accounts(t)
		ctx := context.Background()
		var id uuid.UUID
		if mode == contracts.AccountModeLocal {
			id = mustPrepare(t, p, spec("both@example.test", "B-1")).UserID
		} else {
			id = h.createUser(t, owner, "both@example.test", identity.RoleUser)
			mustManage(t, owner, id, "B-1")
		}
		calls := []struct {
			name string
			call func() error
		}{
			{"Mode", func() error {
				got, err := p.Mode(ctx)
				if err == nil && got != mode {
					return errors.New("mode " + got)
				}
				return err
			}},
			{"Prepare", func() error { _, err := p.Prepare(ctx, systemActor, spec("both@example.test", "B-1")); return err }},
			{"Activate", func() error { _, err := p.Activate(ctx, systemActor, id, contracts.ActivateOpts{}); return err }},
			{"Update", func() error { _, err := p.Update(ctx, systemActor, id, spec("both@example.test", "B-1")); return err }},
			{"Account", func() error { _, err := p.Account(ctx, id, "both@example.test"); return err }},
			{"Deactivate", func() error { _, err := p.Deactivate(ctx, systemActor, id); return err }},
			{"ProvisioningRoles", func() error { _, err := p.ProvisioningRoles(ctx); return err }},
		}
		for _, c := range calls {
			if err := c.call(); err != nil {
				t.Errorf("%s: %s: %v", mode, c.name, err)
			}
		}
	}
}

// Worker mode never composes: module.Workers resolves identity's provider
// into the Deps a worker is built from, beside the user directory, and the
// provider it hands over is identity's own, acting on the installation.
func TestProvisioning_WorkersResolveIdentitysProvider(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	var got module.Deps
	probe := module.Module{
		Name:    "staff",
		Workers: func(d module.Deps) []worker.Worker { got = d; return nil },
	}
	deps := h.deps
	deps.Config = func() *config.Config { c := *h.cfg; c.Modules = []string{"staff"}; return &c }()
	module.Workers(deps, identity.Module(h.access), probe)
	if got.Accounts == nil || got.Users == nil {
		t.Fatalf("worker Deps: Accounts %v, Users %v, want both", got.Accounts, got.Users)
	}
	if mode, err := got.Accounts.Mode(context.Background()); err != nil || mode != contracts.AccountModeLocal {
		t.Errorf("the worker's provider Mode = %q, %v, want local", mode, err)
	}
	ref, err := got.Accounts.Prepare(context.Background(), systemActor, spec("worker@example.test", "X-1"))
	if err != nil || h.count(t, `SELECT count(*) FROM identity.users WHERE id = $1`, ref.UserID) != 1 {
		t.Errorf("the worker's provider did not prepare in the installation: %+v, %v", ref, err)
	}
}
