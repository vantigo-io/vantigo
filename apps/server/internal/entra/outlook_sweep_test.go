package entra_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/entra/graphtest"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

// entra-outlook-sweep (Outlook connection design M6, M7): the closing
// connections nothing needs any more deleted, and the accounts of the
// connections waiting in reconnect read in the tenant once a day.

// sweepCycle is the one cycle the sweep's RunCycle runs.
type sweepCycle interface {
	worker.Worker
	RunCycle(ctx context.Context) (bool, error)
}

// outlookSweep is the module's entra-outlook-sweep, as worker mode builds
// it.
func outlookSweep(t *testing.T, h *harness) sweepCycle {
	t.Helper()
	for _, w := range h.Workers(h.Deps()) {
		if w.Name() == "entra-outlook-sweep" {
			s, ok := w.(sweepCycle)
			if !ok {
				t.Fatalf("entra-outlook-sweep has no RunCycle")
			}
			return s
		}
	}
	t.Fatal("the module contributes no entra-outlook-sweep")
	return nil
}

// insertAged puts a connection for oid in state, changed changedAgo and
// used usedAgo ago (never when zero), its account checked now.
func insertAged(t *testing.T, h *harness, oid uuid.UUID, state string, changedAgo, usedAgo time.Duration) {
	t.Helper()
	now := h.Now()
	var used *time.Time
	if usedAgo > 0 {
		u := now.Add(-usedAgo)
		used = &u
	}
	h.Exec(t, `INSERT INTO entra.outlook_connections (object_id, user_id, tenant_id, scopes, refresh_sealed, state, connected_at, used_at, account_checked_at, changed_at, created_at, updated_at)
	           VALUES ($1, $2, $3, '{calendars.readwrite}', '\x01', $4, $5, $6, $7, $5, $5, $5)`,
		oid, uuid.New(), tenantID, state, now.Add(-changedAgo), used, now)
}

// subscribe gives oid's mailbox a subscription row, which a removal still
// has to delete.
func subscribe(t *testing.T, h *harness, oid uuid.UUID) {
	t.Helper()
	h.Exec(t, `INSERT INTO entra.mailbox_subscriptions (object_id, client_state_hash, created_at, updated_at) VALUES ($1, '\x01', $2, $2)`, oid, h.Now())
}

func TestOutlookSweep_DeletesWhatNothingNeeds(t *testing.T) {
	t.Parallel()
	h, _ := newTenantHarness(t)
	connected(t, h)
	s := outlookSweep(t, h)
	if s.Interval() != time.Hour {
		t.Errorf("Interval = %v, want an hour", s.Interval())
	}
	day := 24 * time.Hour
	var (
		recentlyClosed = uuid.New()
		recentlyUsed   = uuid.New()
		subscribed     = uuid.New()
		spent          = uuid.New()
		weekOld        = uuid.New()
		active         = uuid.New()
		reconnect      = uuid.New()
		usedBefore     = uuid.New()
	)
	insertAged(t, h, recentlyClosed, "closing", time.Hour, 0)
	insertAged(t, h, recentlyUsed, "closing", 2*day, time.Hour)
	insertAged(t, h, subscribed, "closing", 2*day, 0)
	subscribe(t, h, subscribed)
	insertAged(t, h, spent, "closing", 2*day, 25*time.Hour)
	insertAged(t, h, weekOld, "closing", 8*day, time.Hour)
	subscribe(t, h, weekOld)
	insertAged(t, h, active, "active", 30*day, 0)
	insertAged(t, h, reconnect, "reconnect", 30*day, 0)
	// Used three days ago, closed just now: a day has not passed since it
	// closed.
	insertAged(t, h, usedBefore, "closing", 0, 3*day)

	ran, err := s.RunCycle(context.Background())
	if err != nil || !ran {
		t.Fatalf("RunCycle: %v %v", ran, err)
	}
	for _, kept := range []uuid.UUID{recentlyClosed, recentlyUsed, subscribed, active, reconnect, usedBefore} {
		if n := h.Count(t, `SELECT count(*) FROM entra.outlook_connections WHERE object_id = $1`, kept); n != 1 {
			t.Errorf("%s was deleted", kept)
		}
	}
	for _, gone := range []uuid.UUID{spent, weekOld} {
		if n := h.Count(t, `SELECT count(*) FROM entra.outlook_connections WHERE object_id = $1`, gone); n != 0 {
			t.Errorf("%s was kept", gone)
		}
		if n := h.Count(t, `SELECT count(*) FROM entra.events WHERE action = 'entra.outlook.deleted' AND object_id = $1 AND actor_system = 'system:entra-outlook'`, gone); n != 1 {
			t.Errorf("%d deleted events for %s, want 1", n, gone)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM entra.events WHERE action = 'entra.outlook.deleted'`); n != 2 {
		t.Errorf("%d deleted events, want 2", n)
	}

	// A day on, the one used an hour before is spent too; the subscribed
	// one waits for its seven days.
	h.Advance(day)
	if _, err := s.RunCycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := h.Count(t, `SELECT count(*) FROM entra.outlook_connections WHERE object_id IN ($1, $2)`, recentlyClosed, recentlyUsed); n != 0 {
		t.Errorf("%d of the two spent a day on were kept", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM entra.outlook_connections WHERE object_id = $1`, subscribed); n != 1 {
		t.Error("a connection whose mailbox keeps a subscription went before its seven days")
	}
}

func TestOutlookSweep_ReadsTheAccountsWaitingInReconnectOnceADay(t *testing.T) {
	t.Parallel()
	h, g := newTenantHarness(t)
	connected(t, h)
	s := outlookSweep(t, h)
	account := func(number, upn string, enabled bool) uuid.UUID {
		oid := addAccount(t, h, number, upn)
		h.Exec(t, `UPDATE entra.accounts SET state = 'enabled' WHERE object_id = $1`, oid)
		g.AddUser(graphtest.User{ID: oid.String(), AccountEnabled: enabled, UserPrincipalName: upn})
		return oid
	}
	waiting := func(oid uuid.UUID) {
		insertOutlookRow(t, h, oid, "reconnect")
	}
	enabled := account("E-1", "kari@nordlys.no", true)
	disabled := account("E-2", "ola@nordlys.no", false)
	gone := addAccount(t, h, "E-3", "per@nordlys.no")
	h.Exec(t, `UPDATE entra.accounts SET state = 'enabled' WHERE object_id = $1`, gone)
	deletedHere := addAccount(t, h, "E-4", "liv@nordlys.no")
	h.Exec(t, `UPDATE entra.accounts SET state = 'deleted' WHERE object_id = $1`, deletedHere)
	activeOne := account("E-5", "nils@nordlys.no", false)
	for _, oid := range []uuid.UUID{enabled, disabled, gone, deletedHere} {
		waiting(oid)
	}
	insertOutlookRow(t, h, activeOne, "active")

	reads := func(oid uuid.UUID) int {
		n := 0
		for _, c := range g.Calls() {
			if c.Host == graphtest.GraphHost && c.Method == http.MethodGet && c.Path == "/v1.0/users/"+oid.String() {
				n++
			}
		}
		return n
	}
	if _, err := s.RunCycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := h.Count(t, `SELECT count(*) FROM entra.outlook_connections WHERE object_id = $1 AND state = 'reconnect' AND account_checked_at = $2`, enabled, h.Now()); n != 1 {
		t.Error("an enabled account's connection is not kept in reconnect, checked now")
	}
	for oid, code := range map[uuid.UUID]string{disabled: "disabled", gone: "deleted", deletedHere: "deleted"} {
		if n := h.Count(t, `SELECT count(*) FROM entra.outlook_connections WHERE object_id = $1 AND state = 'closing' AND refresh_sealed IS NULL`, oid); n != 1 {
			t.Errorf("%s's connection (%s) is not closed with its token forgotten", oid, code)
		}
		if n := h.Count(t, `SELECT count(*) FROM entra.events WHERE action = 'entra.outlook.closed' AND object_id = $1 AND codes = ARRAY[$2]::text[] AND actor_system = 'system:entra-outlook'`, oid, code); n != 1 {
			t.Errorf("%d closed events %s for %s, want 1", n, code, oid)
		}
	}
	if reads(deletedHere) != 0 {
		t.Error("an account deleted in Vantigo was read in the tenant")
	}
	if reads(activeOne) != 0 {
		t.Error("an active connection's account was read: it is checked on use")
	}

	// Within the day, nothing is read again; a day on, the enabled one is.
	before := reads(enabled)
	h.Advance(23 * time.Hour)
	if _, err := s.RunCycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if reads(enabled) != before {
		t.Error("an account was read twice within a day")
	}
	h.Advance(2 * time.Hour)
	if _, err := s.RunCycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if reads(enabled) != before+1 {
		t.Error("an account was not read again after a day")
	}
}

// A person who reconnects while the sweep reads their account in the
// tenant keeps the new connection and its token: the close is guarded by
// the revision the sweep listed.
func TestOutlookSweep_AReconnectDuringTheAccountCheckIsKept(t *testing.T) {
	t.Parallel()
	tap := &tokenTap{}
	h, g := newTenantHarness(t, modtest.WithTransport(tap))
	tap.mu.Lock()
	tap.next = g
	tap.mu.Unlock()
	connected(t, h)
	s := outlookSweep(t, h)
	oid := addAccount(t, h, "E-1", "kari@nordlys.no")
	h.Exec(t, `UPDATE entra.accounts SET state = 'enabled' WHERE object_id = $1`, oid)
	g.AddUser(graphtest.User{ID: oid.String(), AccountEnabled: false, UserPrincipalName: "kari@nordlys.no"})
	insertOutlookRow(t, h, oid, "reconnect")
	tap.onGraph("/v1.0/users/"+oid.String(), func() *http.Response {
		h.Exec(t, `UPDATE entra.outlook_connections SET state = 'active', error_code = NULL, refresh_sealed = '\x02', revision = revision + 1 WHERE object_id = $1`, oid)
		return nil
	})
	if _, err := s.RunCycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := h.Count(t, `SELECT count(*) FROM entra.outlook_connections WHERE object_id = $1 AND state = 'active' AND refresh_sealed = '\x02'`, oid); n != 1 {
		t.Error("the sweep closed a connection reconnected while it read the account")
	}
	if n := h.Count(t, `SELECT count(*) FROM entra.events WHERE action = 'entra.outlook.closed'`); n != 0 {
		t.Errorf("%d closed events, want 0", n)
	}
}
