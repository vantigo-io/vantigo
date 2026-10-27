package calendar_test

import (
	"context"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/calendar/gen"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The Outlook sync's setting (calendar Outlook sync design O5).

const (
	mailboxSyncPath    = "/api/v1/calendar/mailbox-sync"
	mailboxSyncRunPath = "/api/v1/calendar/mailbox-sync/run"
)

// mailboxRig is a calendar installation with a fake mailbox provider and a
// fake workforce register, in Oslo.
type mailboxRig struct {
	*harness
	box   *modtest.FakeMailboxCalendar
	staff *fakeWorkforce
}

func newMailboxRig(t *testing.T, opts ...modtest.Option) *mailboxRig {
	t.Helper()
	box, staff := modtest.NewFakeMailboxCalendar(), newFakeWorkforce()
	h := newHarness(t, append([]modtest.Option{modtest.WithMailboxCalendar(box), modtest.WithWorkforce(staff)}, opts...)...)
	h.SetBusinessTimeZone(t, "Europe/Oslo")
	return &mailboxRig{harness: h, box: box, staff: staff}
}

// person is a signed-in user holding calendar:access and perms, whose
// directory account is the provider's: it answers their mailbox.
func (r *mailboxRig) person(t *testing.T, perms ...string) (*modtest.Client, uuid.UUID, string) {
	t.Helper()
	c, id := signIn(t, r.harness, perms...)
	mailbox := "oid-" + id.String()
	r.staff.set(id, &contracts.DirectoryAccount{Provider: "entra", ExternalID: mailbox, Enabled: true})
	return c, id, mailbox
}

// syncBody is a PUT /mailbox-sync body.
func syncBody(enabled bool, target, detail string, kinds ...string) map[string]any {
	if kinds == nil {
		kinds = []string{}
	}
	return map[string]any{"enabled": enabled, "target": target, "detail": detail, "kinds": kinds}
}

func getSync(t *testing.T, c *modtest.Client) gen.MailboxSync {
	t.Helper()
	var out gen.MailboxSync
	do(t, c, http.MethodGet, mailboxSyncPath, nil, http.StatusOK, &out)
	return out
}

func putSync(t *testing.T, c *modtest.Client, revision int32, body map[string]any) gen.MailboxSync {
	t.Helper()
	var out gen.MailboxSync
	do(t, c, http.MethodPut, mailboxSyncPath, body, http.StatusOK, &out, ifMatch(revision))
	return out
}

// turnOn turns c's sync on, its revision read first.
func turnOn(t *testing.T, c *modtest.Client, target, detail string, kinds ...string) gen.MailboxSync {
	t.Helper()
	return putSync(t, c, getSync(t, c).Revision, syncBody(true, target, detail, kinds...))
}

func keysOf(kinds []gen.MailboxSyncKind) []string {
	out := make([]string, len(kinds))
	for i, k := range kinds {
		out[i] = k.Key
	}
	return out
}

func TestMailboxSync_GetAnswersTheDefaultsAndWhyItCannotRun(t *testing.T) {
	t.Parallel()
	t.Run("no provider", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		c, _ := signIn(t, h)
		got := getSync(t, c)
		if got.Availability != gen.MailboxSyncAvailabilityNoProvider || got.Enabled || got.Revision != 0 ||
			got.Target != gen.Default || got.Detail != gen.MailboxSyncDetailFull || got.State != gen.MailboxSyncStateOff {
			t.Errorf("defaults = %+v", got)
		}
		if !slices.Equal(got.Kinds, []string{"calendar.appointment", "staff.absence"}) {
			t.Errorf("default kinds = %v", got.Kinds)
		}
		want := []string{"calendar.appointment", "customers.follow-up", "customers.interaction", "staff.absence", "projects.task", "time.entry", "expenses.claim"}
		if keys := keysOf(got.AvailableKinds); !slices.Equal(keys, want) {
			t.Errorf("availableKinds = %v, want %v", keys, want)
		}
	})
	for _, tc := range []struct {
		name          string
		state, reason string
		account       *contracts.DirectoryAccount
		want          gen.MailboxSyncAvailability
	}{
		{name: "available", state: contracts.MailboxAvailable, want: gen.MailboxSyncAvailabilityAvailable},
		{name: "the switch off", state: contracts.MailboxCleanup, reason: "switch_off", want: gen.MailboxSyncAvailabilitySwitchOff},
		// The switch off with no credential working is unavailable to the
		// provider, and switch_off to the person all the same.
		{name: "the switch off, nothing working", state: contracts.MailboxUnavailable, reason: "switch_off", want: gen.MailboxSyncAvailabilitySwitchOff},
		// permission_missing is never answered any more: a cleanup of
		// another reason is unavailable.
		{name: "a cleanup of another reason", state: contracts.MailboxCleanup, reason: "permission_missing", want: gen.MailboxSyncAvailabilityUnavailable},
		{name: "the connection not working", state: contracts.MailboxUnavailable, reason: "not_connected", want: gen.MailboxSyncAvailabilityUnavailable},
		{name: "no account", state: contracts.MailboxAvailable, account: &contracts.DirectoryAccount{}, want: gen.MailboxSyncAvailabilityNoAccount},
		{name: "a disabled account", state: contracts.MailboxAvailable,
			account: &contracts.DirectoryAccount{Provider: "entra", ExternalID: "oid-leaver", Enabled: false}, want: gen.MailboxSyncAvailabilityNoAccount},
		{name: "another provider's account", state: contracts.MailboxAvailable,
			account: &contracts.DirectoryAccount{Provider: "google", ExternalID: "g-1", Enabled: true}, want: gen.MailboxSyncAvailabilityNoAccount},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newMailboxRig(t)
			c, id, _ := r.person(t)
			switch {
			case tc.account != nil && tc.account.Provider == "":
				r.staff.set(id, nil)
			case tc.account != nil:
				r.staff.set(id, tc.account)
			}
			r.box.SetStatus(tc.state, tc.reason)
			if got := getSync(t, c); got.Availability != tc.want {
				t.Errorf("availability = %s, want %s", got.Availability, tc.want)
			}
		})
	}
}

func TestMailboxSync_PutRefusesWhatIsNotASetting(t *testing.T) {
	t.Parallel()
	r := newMailboxRig(t)
	c, _, _ := r.person(t)
	put := func(revision string, body map[string]any, opts ...modtest.RequestOption) *modtest.Response {
		return c.Do(http.MethodPut, mailboxSyncPath, body, append(opts, modtest.Header("If-Match", revision))...)
	}
	fieldRefused(t, put("soon", syncBody(true, "default", "full")), "If-Match")
	fieldRefused(t, put("-1", syncBody(true, "default", "full")), "If-Match")
	fieldRefused(t, put("0", syncBody(true, "elsewhere", "full"),
		modtest.SkipContract("elsewhere is not in the request enum: the server's own refusal is the subject")), "target")
	fieldRefused(t, put("0", syncBody(true, "default", "titles"),
		modtest.SkipContract("titles is not in the request enum: the server's own refusal is the subject")), "detail")
	fieldRefused(t, put("0", syncBody(true, "default", "full", "calendar.subscription")), "kinds[0]")
	fieldRefused(t, put("0", syncBody(true, "default", "full", "calendar.appointment", "nobody.thing")), "kinds[1]")
	fieldRefused(t, put("0", syncBody(true, "default", "full", "staff.absence", "staff.absence")), "kinds[1]")
	if n := r.Count(t, `SELECT count(*) FROM calendar.mailbox_sync`); n != 0 {
		t.Errorf("a refused PUT wrote %d rows", n)
	}
	// A quoted revision is taken, and the kinds are kept in availableKinds'
	// order whatever order they came in.
	got := putSync(t, c, 0, syncBody(true, "own", "busy", "time.entry", "calendar.appointment"))
	if !slices.Equal(got.Kinds, []string{"calendar.appointment", "time.entry"}) || got.Target != gen.Own || got.Detail != gen.MailboxSyncDetailBusy {
		t.Errorf("written = %+v", got)
	}
	do(t, c, http.MethodPut, mailboxSyncPath, syncBody(true, "own", "busy"), http.StatusOK, nil, modtest.Header("If-Match", `W/"1"`))
}

func TestMailboxSync_PutHoldsTheRevision(t *testing.T) {
	t.Parallel()
	r := newMailboxRig(t)
	c, _, _ := r.person(t)
	first := putSync(t, c, 0, syncBody(true, "default", "full", "calendar.appointment"))
	if first.Revision != 1 || first.State != gen.MailboxSyncStatePending || !first.Enabled {
		t.Errorf("the first write = %+v", first)
	}
	p := refused(t, c.Do(http.MethodPut, mailboxSyncPath, syncBody(false, "default", "full"), ifMatch(0)), http.StatusConflict, "revision_mismatch")
	if p.Detail == "" {
		t.Error("the revision mismatch says nothing")
	}
	if second := putSync(t, c, 1, syncBody(true, "default", "busy", "calendar.appointment")); second.Revision != 2 {
		t.Errorf("the second write's revision = %d", second.Revision)
	}
}

func TestMailboxSync_TurningOnWhileItCannotRunIsRefusedAndOffIsAlwaysTaken(t *testing.T) {
	t.Parallel()
	r := newMailboxRig(t)
	c, id, _ := r.person(t)
	r.staff.set(id, nil)
	p := refused(t, c.Do(http.MethodPut, mailboxSyncPath, syncBody(true, "default", "full"), ifMatch(0)), http.StatusConflict, "mailbox_sync_unavailable")
	if p.Detail != "no_account" {
		t.Errorf("detail = %q, want no_account", p.Detail)
	}
	r.box.SetStatus(contracts.MailboxCleanup, "switch_off")
	if got := putSync(t, c, 0, syncBody(false, "default", "full")); got.State != gen.MailboxSyncStateOff || got.Enabled {
		t.Errorf("turned off while unavailable = %+v", got)
	}
}

func TestMailboxSync_TheStateFollowsTheToggleAndTheTarget(t *testing.T) {
	t.Parallel()
	r := newMailboxRig(t)
	c, id, _ := r.person(t)
	on := turnOn(t, c, "default", "full", "calendar.appointment")
	if on.State != gen.MailboxSyncStatePending {
		t.Errorf("on = %s", on.State)
	}
	if got := putSync(t, c, on.Revision, syncBody(true, "default", "busy", "calendar.appointment")); got.State != gen.MailboxSyncStatePending {
		t.Errorf("a change of detail while on = %s, want pending", got.State)
	}
	moved := putSync(t, c, on.Revision+1, syncBody(true, "own", "busy", "calendar.appointment"))
	if moved.State != gen.MailboxSyncStateStopping {
		t.Errorf("a change of target while on = %s, want stopping", moved.State)
	}
	if got := putSync(t, c, moved.Revision, syncBody(true, "own", "full", "calendar.appointment")); got.State != gen.MailboxSyncStateStopping {
		t.Errorf("a change while a clean-up is owed = %s, want stopping still", got.State)
	}
	r.Exec(t, `UPDATE calendar.mailbox_sync SET state = 'active' WHERE user_id = $1`, id)
	if got := putSync(t, c, moved.Revision+1, syncBody(false, "own", "full", "calendar.appointment")); got.State != gen.MailboxSyncStateStopping || got.Enabled {
		t.Errorf("turned off = %+v, want stopping", got)
	}
	if !modtest.One[bool](t, r.Harness, `SELECT reconcile FROM calendar.mailbox_sync WHERE user_id = $1`, id) {
		t.Error("a clean-up owed does not reconcile next")
	}
	// Someone who never turned it on, turning it off, is simply off.
	other, _, _ := r.person(t)
	if got := putSync(t, other, 0, syncBody(false, "default", "full")); got.State != gen.MailboxSyncStateOff {
		t.Errorf("never on, off = %s", got.State)
	}
}

func TestMailboxSync_RunMakesTheSyncDueAtOnce(t *testing.T) {
	t.Parallel()
	r := newMailboxRig(t)
	c, id, _ := r.person(t)
	// A sync that is off is its own code, not an availability.
	refused(t, c.Do(http.MethodPost, mailboxSyncRunPath, nil), http.StatusConflict, "mailbox_sync_off")
	on := turnOn(t, c, "default", "full", "calendar.appointment")
	r.Exec(t, `UPDATE calendar.mailbox_sync SET next_sync_at = $2 WHERE user_id = $1`, id, r.Now().Add(time.Hour))
	var got gen.MailboxSync
	do(t, c, http.MethodPost, mailboxSyncRunPath, nil, http.StatusOK, &got)
	if got.Revision != on.Revision || !got.Enabled {
		t.Errorf("Sync now = %+v", got)
	}
	if next := modtest.One[time.Time](t, r.Harness, `SELECT next_sync_at FROM calendar.mailbox_sync WHERE user_id = $1`, id); !next.Equal(r.Now()) {
		t.Errorf("next_sync_at = %v, want now %v", next, r.Now())
	}
	r.box.SetStatus(contracts.MailboxCleanup, "switch_off")
	if p := refused(t, c.Do(http.MethodPost, mailboxSyncRunPath, nil), http.StatusConflict, "mailbox_sync_unavailable"); p.Detail != "switch_off" {
		t.Errorf("detail = %q", p.Detail)
	}
}

func TestMailboxSyncWorker_IsContributedUnlessTurnedOffAndTicksEveryMinute(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		env  map[string]string
		want bool
	}{
		{name: "on by default", want: true},
		{name: "turned off", env: map[string]string{"CALENDAR_MAILBOX_SYNC_ENABLED": "0"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var opts []modtest.Option
			for k, v := range tc.env {
				opts = append(opts, modtest.WithEnv(k, v))
			}
			h := newHarness(t, opts...)
			found := false
			for _, w := range h.Workers(h.Deps()) {
				if w.Name() != "calendar-mailbox-sync" {
					continue
				}
				found = true
				if w.Interval() != time.Minute {
					t.Errorf("the worker ticks every %v", w.Interval())
				}
			}
			if found != tc.want {
				t.Errorf("calendar-mailbox-sync contributed: %v, want %v", found, tc.want)
			}
		})
	}
}

func TestMailboxSyncWorker_SkipsTheCycleWhileTheLeaseIsHeldElsewhere(t *testing.T) {
	t.Parallel()
	r := newMailboxRig(t)
	conn, err := r.Pool().Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(context.Background(), `SELECT pg_advisory_lock($1)`, int64(0x43414C4D424F5831)); err != nil {
		t.Fatal(err)
	}
	w := mailboxWorker(t, r)
	if ran, n, err := w.RunCycle(context.Background()); ran || n != 0 || err != nil {
		t.Errorf("with the lease held elsewhere the cycle ran %v, synced %d, %v", ran, n, err)
	}
	if _, err := conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, int64(0x43414C4D424F5831)); err != nil {
		t.Fatal(err)
	}
	if ran, _, err := w.RunCycle(context.Background()); !ran || err != nil {
		t.Errorf("with the lease free the cycle ran %v, %v", ran, err)
	}
}

// The PUT is rate limited: ten writes per client in ten minutes
// (calendar-mailbox-sync-write), each one making a sync due.
func TestMailboxSync_TheEleventhWriteInTenMinutesIs429(t *testing.T) {
	t.Parallel()
	r := newMailboxRig(t)
	c, _, _ := r.person(t)
	revision := getSync(t, c).Revision
	for i := range 10 {
		revision = putSync(t, c, revision, syncBody(i%2 == 0, "default", "full", "calendar.appointment")).Revision
	}
	if res := c.Do(http.MethodPut, mailboxSyncPath, syncBody(true, "default", "full"), ifMatch(revision)); res.Status != http.StatusTooManyRequests {
		t.Errorf("the 11th write = %d, want 429", res.Status)
	}
}
