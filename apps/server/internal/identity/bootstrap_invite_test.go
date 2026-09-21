package identity_test

import (
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/identity"
)

const bootstrapOwner = "owner@customer.example"

func invitedHarness(t *testing.T) *harness {
	t.Helper()
	return newHarness(t, withEnv("BOOTSTRAP_OWNER_EMAIL", bootstrapOwner))
}

func pendingOwnerInvitations(t *testing.T, h *harness) int {
	t.Helper()
	return h.count(t, `SELECT count(*) FROM identity.invitations
		WHERE role = 'Owner' AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at > $1`, h.now())
}

func TestBootstrapInvite_AnonymousBootstrapIsClosed(t *testing.T) {
	t.Parallel()
	h := invitedHarness(t)
	c := h.client(t)
	if bootstrapAvailable(t, c) {
		t.Error("bootstrap-status must report unavailable when BOOTSTRAP_OWNER_EMAIL is set")
	}
	r := c.do(http.MethodPost, bootstrapPath, bootstrapBody("intruder@example.test"))
	if r.status != http.StatusConflict || r.code() != "bootstrap_unavailable" {
		t.Fatalf("anonymous bootstrap: status %d code %q, want 409 bootstrap_unavailable", r.status, r.code())
	}
	if n := h.count(t, `SELECT count(*) FROM identity.users`); n != 0 {
		t.Errorf("users = %d; the anonymous bootstrap created an account", n)
	}
}

func TestBootstrapInvite_StartupInvitesTheOwnerOnce(t *testing.T) {
	t.Parallel()
	h := invitedHarness(t)

	if err := h.runStartup(); err != nil {
		t.Fatal(err)
	}
	if got := len(h.mailTo(bootstrapOwner)); got != 1 {
		t.Fatalf("mails = %d, want 1", got)
	}
	if n := pendingOwnerInvitations(t, h); n != 1 {
		t.Fatalf("pending owner invitations = %d, want 1", n)
	}

	// A second replica, or a restart, sends nothing more.
	if err := h.runStartup(); err != nil {
		t.Fatal(err)
	}
	if got := len(h.mailTo(bootstrapOwner)); got != 1 {
		t.Errorf("mails after a second start = %d, want still 1", got)
	}
}

func TestBootstrapInvite_AcceptingSeatsTheAdministrator(t *testing.T) {
	t.Parallel()
	h := invitedHarness(t)
	if err := h.runStartup(); err != nil {
		t.Fatal(err)
	}
	token := mailedLink(t, h, bootstrapOwner).Query().Get("token")

	c := h.client(t)
	r := accept(c, token, inviteePassword, "Customer Owner")
	if r.status != http.StatusCreated {
		t.Fatalf("accept: status %d body %s", r.status, r.body)
	}
	var got struct {
		User struct {
			Roles []string `json:"roles"`
		} `json:"user"`
	}
	r.json(&got)
	slices.Sort(got.User.Roles)
	if !slices.Equal(got.User.Roles, []string{identity.RoleOwner, identity.RoleSystemAdmin}) {
		t.Errorf("roles %v, want [Owner SystemAdmin]", got.User.Roles)
	}

	// With an Owner in place, later starts invite nobody.
	if err := h.runStartup(); err != nil {
		t.Fatal(err)
	}
	if got := len(h.mailTo(bootstrapOwner)); got != 1 {
		t.Errorf("mails after the Owner exists = %d, want still 1", got)
	}
}

func TestBootstrapInvite_AnExpiredInvitationIsReissuedOnTheNextStart(t *testing.T) {
	t.Parallel()
	h := invitedHarness(t)
	if err := h.runStartup(); err != nil {
		t.Fatal(err)
	}
	first := mailedLink(t, h, bootstrapOwner).Query().Get("token")

	h.advance(h.cfg.InvitationLifetime + time.Minute)
	if err := h.runStartup(); err != nil {
		t.Fatal(err)
	}
	if got := len(h.mailTo(bootstrapOwner)); got != 2 {
		t.Fatalf("mails = %d, want a second invitation", got)
	}
	second := mailedLink(t, h, bootstrapOwner).Query().Get("token")
	if second == first {
		t.Error("the re-issued invitation must carry a new token")
	}
	if n := pendingOwnerInvitations(t, h); n != 1 {
		t.Errorf("pending owner invitations = %d, want exactly 1", n)
	}
}

func TestBootstrapInvite_AFailedSendDoesNotStopStartup(t *testing.T) {
	t.Parallel()
	h := invitedHarness(t)
	h.mail.FailNext(errors.New(mailFailureMarker))

	if err := h.runStartup(); err != nil {
		t.Fatalf("RunStartup = %v; a mail failure must not stop the process", err)
	}
	if n := pendingOwnerInvitations(t, h); n != 0 {
		t.Errorf("pending owner invitations = %d; an unsent invitation must be revoked", n)
	}
	if !strings.Contains(string(h.log.Bytes()), "bootstrap owner invitation could not be sent") {
		t.Error("the failure was not logged")
	}
	if strings.Contains(string(h.log.Bytes()), mailFailureMarker) {
		t.Error("the mail driver's error reached the log; it can name the recipient")
	}

	// The next start retries.
	if err := h.runStartup(); err != nil {
		t.Fatal(err)
	}
	if got := len(h.mailTo(bootstrapOwner)); got != 1 {
		t.Errorf("mails after the retry = %d, want 1", got)
	}
}

func TestBootstrapInvite_UnsetKeepsTheSetupFlow(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	if err := h.runStartup(); err != nil {
		t.Fatal(err)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.invitations`); n != 0 {
		t.Errorf("invitations = %d; nothing may be issued without BOOTSTRAP_OWNER_EMAIL", n)
	}
	if !bootstrapAvailable(t, h.client(t)) {
		t.Error("/setup must stay available")
	}
}
