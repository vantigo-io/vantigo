package staff_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// blockLink turns personID's link into a worker link the accounts worker
// blocked for reason, naming the user it named.
func blockLink(t *testing.T, h *harness, personID int64, reason string) {
	t.Helper()
	h.Exec(t, `UPDATE staff.identity_links SET state = 'blocked', managed = true, blocked_reason = $2 WHERE person_id = $1`, personID, reason)
}

// A worker link the accounts worker blocked as someone else's account
// (mismatch) names no active user of the person's, as it gives that user no
// Self (the final re-check's M-a, after M1): a manager whose only link is
// blocked so is no manager-decider — the report's request mails the holders
// by right, not the manager — and a head of the structure whose only manager
// is linked so approves their own, by B's narrowed rule. A holder by right
// linked by such a link is greeted as their user, not as the person.
func TestAccounts_ABlockedLinkIsNoActiveUser(t *testing.T) {
	t.Parallel()
	w := newDecisionWorld(t)
	h := w.h
	_, owner := ownerUser(t, h)
	h.Exec(t, `UPDATE staff.people SET work_email = 'bjorn@example.test' WHERE id = $1`, w.boss.Id)
	blockLink(t, h, w.boss.Id, "mismatch")
	requestOwn(t, w.kariC, absence(t, h, "ferie", "2026-10-05", "2026-10-09"))
	got := recipients(h)
	if slices.Contains(got, "bjorn@example.test") || !slices.Contains(got, emailOf(t, h, owner)) {
		t.Errorf("Kari's request mailed %v, want the holders by right (the Owner among them), not the manager whose link is blocked", got)
	}

	// Olav heads the structure; Sigrid, his manager, is linked only by a
	// blocked link: he approves his own.
	setManager(t, h, w.olav.Id, w.sigrid.Id)
	blockLink(t, h, w.sigrid.Id, "mismatch")
	own := requestOwn(t, w.olavC, absence(t, h, "ferie", "2026-10-14", "2026-10-14"))
	approved(t, w.olavC, own.Id)

	// Olav's own link blocked: his user, a holder of staff:manage, is
	// greeted in the user's language, not the person's.
	olavUser := modtest.One[uuid.UUID](t, h.Harness, `SELECT external_id::uuid FROM staff.identity_links WHERE person_id = $1`, w.olav.Id)
	h.Exec(t, `UPDATE staff.people SET preferred_language = 'en' WHERE id = $1`, w.olav.Id)
	h.Exec(t, `UPDATE identity.users SET preferred_language = 'nb' WHERE id = $1`, olavUser)
	blockLink(t, h, w.olav.Id, "account_not_managed")
	requestOwn(t, w.hildC, absence(t, h, "ferie", "2026-11-02", "2026-11-06"))
	if m := lastMail(t, h, emailOf(t, h, olavUser)); !strings.Contains(m, "Fraværssøknad fra Hilde Nordmann") {
		t.Errorf("the mail to Olav's user is not in the user's language (nb):\n%s", m)
	}
}
