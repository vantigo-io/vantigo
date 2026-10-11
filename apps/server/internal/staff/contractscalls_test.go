package staff_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/mail"
	"github.com/vantigo-io/vantigo/server/internal/staff"
)

// The harness's recorder fails a test on a call out of the module made inside
// any of its transactions — a locked one, or a read-only snapshot (withReadTx),
// which holds a pool connection the call's own read could starve for — and on
// no call made outside one. Not parallel: a forbidden call recorded here would
// fail any harness open beside it, and it is forgotten when the test ends.
func TestContractCallHook_FailsACallMadeInsideATransaction(t *testing.T) {
	user := uuid.New()
	ctx := contracts.WithPrincipal(context.Background(), contracts.Principal{UserID: user})
	before := lockedContractCalls.count()
	t.Cleanup(func() { lockedContractCalls.forget(before) })

	_ = staff.NoteContractCall(ctx, "Users.User")
	if calls := lockedContractCalls.since(before); len(calls) != 0 {
		t.Fatalf("a call outside any transaction recorded %d forbidden calls, want none", len(calls))
	}

	_ = staff.NoteContractCall(staff.ReadTxContext(ctx), "Users.Users")
	_ = staff.NoteContractCall(staff.LockedContext(ctx), "SMTPSend")
	calls := lockedContractCalls.since(before)
	if len(calls) != 2 || !strings.HasPrefix(calls[0], "Users.Users\n") || !strings.HasPrefix(calls[1], "SMTPSend\n") {
		t.Errorf("forbidden calls = %d %v, want the directory read in a snapshot and the send under a lock", len(calls), calls)
	}
	got := contractCalls.by(user)
	if len(got) != 3 || got[0].inTx || !got[1].inTx || !got[2].inTx {
		t.Errorf("every call = %+v, want three, the last two inside a transaction", got)
	}
}

// The harness's installation can send mail — Deps.Config's driver is smtp —
// and its SMTP seam delivers to the mailbox; the accessor refuses a send made
// from inside a transaction outright, beside reporting it.
func TestHarness_TheMailSeamReachesTheMailbox(t *testing.T) {
	h := newHarness(t)
	if !staff.MailAvailable(h.Deps()) {
		t.Fatal("the harness's mail is not available, want MAIL_DRIVER smtp")
	}
	ctx := context.Background()
	msg := mail.Outbound{To: []string{"kari@example.invalid"}, Subject: "Hei"}
	if err := staff.SMTPSend(ctx, h.Deps(), msg); err != nil {
		t.Fatalf("send: %v", err)
	}
	if got := h.mail.messages(); len(got) != 1 || got[0].Subject != "Hei" {
		t.Errorf("mailbox = %+v, want the one message", got)
	}

	before := lockedContractCalls.count()
	t.Cleanup(func() { lockedContractCalls.forget(before) })
	if err := staff.SMTPSend(staff.ReadTxContext(ctx), h.Deps(), msg); err == nil {
		t.Error("a send inside a read-only snapshot succeeded, want it refused")
	}
	if got := h.mail.messages(); len(got) != 1 {
		t.Errorf("mailbox = %d messages, want the refused send never delivered", len(got))
	}
	if calls := lockedContractCalls.since(before); len(calls) != 1 {
		t.Errorf("forbidden calls = %d, want the refused send recorded", len(calls))
	}
}

// The audience resolver finds a user's person through the self-service link,
// whatever state the link is in, and nobody before it exists.
func TestPersonForUser_ThroughTheSelfServiceLink(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	_, userID := signIn(t, h)
	person := seedPerson(t, h, "Kari", "Nordmann")

	if got, err := staff.PersonForUser(ctx, h.Deps(), userID); err != nil || got != nil {
		t.Fatalf("before the link = %v, %v, want nobody", got, err)
	}
	link(t, h, person, userID)
	for _, state := range []string{"linked", "pending", "active", "disabled", "failed", "blocked"} {
		h.Exec(t, `UPDATE staff.identity_links SET state = $1 WHERE person_id = $2`, state, person)
		if got, err := staff.PersonForUser(ctx, h.Deps(), userID); err != nil || got == nil || *got != person {
			t.Errorf("state %s = %v, %v, want person %d", state, got, err, person)
		}
	}
	if got, err := staff.PersonForUser(ctx, h.Deps(), uuid.New()); err != nil || got != nil {
		t.Errorf("another user = %v, %v, want nobody", got, err)
	}
}
