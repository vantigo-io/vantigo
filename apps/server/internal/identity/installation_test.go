package identity_test

import (
	"context"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/identity"
)

func installation(t *testing.T, h *harness) identity.InstallationStatus {
	t.Helper()
	st, err := identity.ReadInstallationStatus(context.Background(), h.deps)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestInstallationStatus_FollowsTheBootstrap(t *testing.T) {
	t.Parallel()
	h := invitedHarness(t)

	if st := installation(t, h); st.Bootstrap != identity.BootstrapPending || st.Users != 0 {
		t.Errorf("fresh: %+v, want pending with no users", st)
	}

	if err := h.runStartup(); err != nil {
		t.Fatal(err)
	}
	if st := installation(t, h); st.Bootstrap != identity.BootstrapInvited {
		t.Errorf("after startup: %+v, want invited", st)
	}

	token := mailedLink(t, h, bootstrapOwner).Query().Get("token")
	if r := accept(h.client(t), token, inviteePassword, ""); r.status != 201 {
		t.Fatalf("accept: status %d", r.status)
	}
	st := installation(t, h)
	if st.Bootstrap != identity.BootstrapCompleted || st.Users != 1 || st.ActiveUsers != 1 {
		t.Errorf("after acceptance: %+v, want completed with one active user", st)
	}
}

func TestInstallationStatus_ADisabledUserIsNotActive(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.bootstrapOwner(t)
	id := h.insertUser(t, "disabled@example.test")
	h.exec(t, `UPDATE identity.users SET is_disabled = true WHERE id = $1`, id)

	if st := installation(t, h); st.Users != 2 || st.ActiveUsers != 1 {
		t.Errorf("%+v, want 2 users of which 1 active", st)
	}
}
