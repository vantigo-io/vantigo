package calendar_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/calendar/gen"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The mailbox sync on many providers (Google Workspace design G3): each
// person is served by the provider their directory account belongs to, the
// notices and connections are read per provider and mark only that
// provider's rows, and a person whose account moves to another provider is
// cleaned up on the old one before the new one is written.

// twoProviders is a mailbox rig with a second provider beside the rig's
// Entra fake (r.box), under the key google.
func twoProviders(t *testing.T) (*mailboxRig, *modtest.FakeMailboxCalendar) {
	t.Helper()
	google := modtest.NewFakeMailboxCalendar().WithKey("google")
	return newMailboxRig(t, modtest.WithMailboxCalendar(google)), google
}

// googlePerson is a signed-in user holding calendar:access whose directory
// account is mailbox, Google's.
func googlePerson(t *testing.T, r *mailboxRig, mailbox string) (*modtest.Client, uuid.UUID) {
	t.Helper()
	c, id := signIn(t, r.harness)
	r.staff.set(id, &contracts.DirectoryAccount{Provider: "google", ExternalID: mailbox, Enabled: true})
	return c, id
}

// rowProvider is the provider the person's row records with its mailbox.
func rowProvider(t *testing.T, r *mailboxRig, id uuid.UUID) string {
	t.Helper()
	var p *string
	if err := r.Pool().QueryRow(context.Background(), `SELECT provider FROM calendar.mailbox_sync WHERE user_id = $1`, id).Scan(&p); err != nil {
		t.Fatal(err)
	}
	if p == nil {
		return ""
	}
	return *p
}

// callsOf is the methods a fake was asked since its first from calls.
func callsOf(box *modtest.FakeMailboxCalendar, from int, method string) []modtest.MailboxCall {
	var out []modtest.MailboxCall
	for _, c := range box.Calls()[from:] {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

func TestMailboxProviders_EachPersonIsServedByTheirOwnProvider(t *testing.T) {
	t.Parallel()
	r, google := twoProviders(t)
	ola, olaID, mbOla := r.person(t)
	kari, kariID := googlePerson(t, r, "g-kari")

	for _, c := range []*modtest.Client{ola, kari} {
		create(t, c, timed(t, "Plan", oslo, "2026-09-14T09:00", "2026-09-14T10:00"))
		turnOn(t, c, "default", "full", "calendar.appointment")
	}
	if n := mailboxCycle(t, r); n != 2 {
		t.Fatalf("synced %d, want both", n)
	}
	if got := len(r.box.EventsIn(mbOla, "")); got != 1 {
		t.Errorf("Ola's copies in Entra = %d, want 1", got)
	}
	if got := len(google.EventsIn("g-kari", "")); got != 1 {
		t.Errorf("Kari's copies in Google = %d, want 1", got)
	}
	for _, c := range r.box.Calls() {
		if c.Mailbox == "g-kari" {
			t.Errorf("Entra was asked about Kari's mailbox: %+v", c)
		}
	}
	for _, c := range google.Calls() {
		if c.Mailbox == mbOla {
			t.Errorf("Google was asked about Ola's mailbox: %+v", c)
		}
	}
	if !r.box.Watched(mbOla) || !google.Watched("g-kari") || r.box.Watched("g-kari") || google.Watched(mbOla) {
		t.Error("each mailbox is watched by its own provider alone")
	}
	if p := rowProvider(t, r, olaID); p != "entra" {
		t.Errorf("Ola's row's provider = %q, want entra", p)
	}
	if p := rowProvider(t, r, kariID); p != "google" {
		t.Errorf("Kari's row's provider = %q, want google", p)
	}

	// GET /mailbox-sync names each person's provider.
	if got := getSync(t, ola).Provider; got == nil || got.Key != "entra" || got.DisplayName != "Outlook" {
		t.Errorf("Ola's provider = %+v, want entra, Outlook", got)
	}
	if got := getSync(t, kari).Provider; got == nil || got.Key != "google" || got.DisplayName != "google" {
		t.Errorf("Kari's provider = %+v, want google", got)
	}
}

// Each provider's notices and connections are asked of it alone, with its
// own cursor, and mark only the rows of its own mailboxes — even when two
// providers name the same id.
func TestMailboxProviders_NoticesAndConnectionsArePerProvider(t *testing.T) {
	t.Parallel()
	r, google := twoProviders(t)
	w := mailboxWorker(t, r)
	cycle := func() int {
		t.Helper()
		n, err := w.RunCycle(context.Background())
		if err != nil {
			t.Fatalf("cycle: %v", err)
		}
		return n
	}
	const same = "same-id"
	ola, olaID := signIn(t, r.harness)
	r.staff.set(olaID, &contracts.DirectoryAccount{Provider: "entra", ExternalID: same, Enabled: true})
	kari, kariID := googlePerson(t, r, same)
	for _, c := range []*modtest.Client{ola, kari} {
		create(t, c, timed(t, "Plan", oslo, "2026-09-14T09:00", "2026-09-14T10:00"))
		turnOn(t, c, "default", "full", "calendar.appointment")
	}
	if n := cycle(); n != 2 {
		t.Fatalf("synced %d, want both", n)
	}
	synced := r.Now()

	// Google notifies its mailbox: Kari alone is synced.
	r.Advance(5 * time.Minute)
	google.Notify(same, synced.Add(4*time.Minute))
	fromEntra, fromGoogle := len(r.box.Calls()), len(google.Calls())
	if n := cycle(); n != 1 {
		t.Fatalf("synced %d after Google's notice, want Kari alone", n)
	}
	if !lastSynced(t, r, kariID).Equal(r.Now()) || lastSynced(t, r, olaID).Equal(r.Now()) {
		t.Errorf("after Google's notice: Kari last %v, Ola last %v; want Kari alone synced",
			lastSynced(t, r, kariID), lastSynced(t, r, olaID))
	}
	for name, box := range map[string]*modtest.FakeMailboxCalendar{"entra": r.box, "google": google} {
		from := fromEntra
		if name == "google" {
			from = fromGoogle
		}
		if n := len(callsOf(box, from, "Notified")); n != 1 {
			t.Errorf("%s asked Notified %d times, want once", name, n)
		}
		if n := len(callsOf(box, from, "Connections")); n != 1 {
			t.Errorf("%s asked Connections %d times, want once", name, n)
		}
	}
	// Each cursor is its own: the next cycle asks Google from its notice,
	// Entra from a poll before its first cycle.
	fromEntra, fromGoogle = len(r.box.Calls()), len(google.Calls())
	r.Advance(time.Minute)
	if n := cycle(); n != 0 {
		t.Fatalf("synced %d on the quiet cycle, want none", n)
	}
	if asked := callsOf(google, fromGoogle, "Notified"); len(asked) != 1 || !asked[0].From.Equal(synced.Add(4*time.Minute-30*time.Second)) {
		t.Errorf("Google's Notified = %+v, want from its notice less the overlap", asked)
	}
	if asked := callsOf(r.box, fromEntra, "Notified"); len(asked) != 1 || !asked[0].From.Before(synced) {
		t.Errorf("Entra's Notified = %+v, want from its own first cursor", asked)
	}

	// Entra's connection moves: Ola alone is woken.
	r.box.ChangeConnection(same, contracts.ConnectionActive, r.Now().Add(10*time.Second))
	r.Advance(30 * time.Second)
	if n := cycle(); n != 1 {
		t.Fatalf("synced %d after Entra's connection change, want Ola alone", n)
	}
	if !lastSynced(t, r, olaID).Equal(r.Now()) || lastSynced(t, r, kariID).Equal(r.Now()) {
		t.Errorf("after Entra's change: Ola last %v, Kari last %v; want Ola alone synced",
			lastSynced(t, r, olaID), lastSynced(t, r, kariID))
	}
}

// A person whose account moves to another provider is a changed mailbox
// (design O8): the copies are removed from the old provider's mailbox and
// its subscription deleted, then the new provider's mailbox is written and
// the row records it.
func TestMailboxProviders_AProviderChangeCleansUpOnTheOldOne(t *testing.T) {
	t.Parallel()
	r, google := twoProviders(t)
	_, me, mb := watchedPerson(t, r)
	if got := len(r.box.EventsIn(mb, "")); got != 1 {
		t.Fatalf("copies in Entra = %d, want 1", got)
	}
	// The same id under Google: still another mailbox.
	r.staff.set(me, &contracts.DirectoryAccount{Provider: "google", ExternalID: mb, Enabled: true})
	r.Advance(16 * time.Minute)
	if n := mailboxCycle(t, r); n != 1 {
		t.Fatalf("synced %d, want the person", n)
	}
	if got := len(r.box.EventsIn(mb, "")); got != 0 {
		t.Errorf("copies left in Entra = %d, want none", got)
	}
	if got := r.box.UnwatchCalls(); !slices.Equal(got, []string{mb}) || r.box.Watched(mb) {
		t.Errorf("Entra's Unwatch calls = %v, want the old mailbox", got)
	}
	if got := len(google.EventsIn(mb, "")); got != 1 || !google.Watched(mb) {
		t.Errorf("copies in Google = %d, watched %v; want 1, watched", got, google.Watched(mb))
	}
	if p := rowProvider(t, r, me); p != "google" {
		t.Errorf("the row's provider = %q, want google", p)
	}
	if row := readSyncRow(t, r, me); row.state != "active" {
		t.Errorf("the row = %+v, want active", row)
	}
}

// A person whose account is gone has the old provider's copies removed, on
// that provider, whatever the other provider says.
func TestMailboxProviders_ALostAccountCleansUpOnTheRowsProvider(t *testing.T) {
	t.Parallel()
	r, google := twoProviders(t)
	c, me := googlePerson(t, r, "g-kari")
	create(t, c, timed(t, "Plan", oslo, "2026-09-14T09:00", "2026-09-14T10:00"))
	turnOn(t, c, "default", "full", "calendar.appointment")
	mailboxCycle(t, r)
	if got := len(google.EventsIn("g-kari", "")); got != 1 {
		t.Fatalf("copies in Google = %d, want 1", got)
	}
	r.staff.set(me, nil)
	r.box.SetStatus(contracts.MailboxUnavailable, "not_configured")
	r.Advance(16 * time.Minute)
	mailboxCycle(t, r)
	if got := len(google.EventsIn("g-kari", "")); got != 0 {
		t.Errorf("copies left in Google = %d, want none", got)
	}
	if row := readSyncRow(t, r, me); row.state != "unavailable" || row.errCode == nil || *row.errCode != "no_account" {
		t.Errorf("the row = %+v, want unavailable, no_account", row)
	}
}

// The availability is asked in order (design G3): no_provider with no
// provider at all, then no_account — before any provider's status — then
// the status of the provider the person's account belongs to.
func TestMailboxProviders_TheAvailabilityOrder(t *testing.T) {
	t.Parallel()
	t.Run("no account comes before the status", func(t *testing.T) {
		t.Parallel()
		r, google := twoProviders(t)
		c, id, _ := r.person(t)
		r.staff.set(id, nil)
		r.box.SetStatus(contracts.MailboxUnavailable, "not_configured")
		google.SetStatus(contracts.MailboxCleanup, "switch_off")
		got := getSync(t, c)
		if got.Availability != gen.MailboxSyncAvailabilityNoAccount || got.Provider != nil {
			t.Errorf("availability %s, provider %+v; want no_account and none", got.Availability, got.Provider)
		}
	})
	t.Run("the account's provider's status", func(t *testing.T) {
		t.Parallel()
		r, google := twoProviders(t)
		c, _ := googlePerson(t, r, "g-kari")
		r.box.SetStatus(contracts.MailboxAvailable, "")
		google.SetStatus(contracts.MailboxCleanup, "switch_off")
		got := getSync(t, c)
		if got.Availability != gen.MailboxSyncAvailabilitySwitchOff || got.Provider == nil || got.Provider.Key != "google" {
			t.Errorf("availability %s, provider %+v; want Google's switch_off", got.Availability, got.Provider)
		}
	})
	t.Run("an account of a provider that is not on", func(t *testing.T) {
		t.Parallel()
		r := newMailboxRig(t)
		c, _ := googlePerson(t, r, "g-kari")
		got := getSync(t, c)
		if got.Availability != gen.MailboxSyncAvailabilityNoAccount || got.Provider != nil {
			t.Errorf("availability %s, provider %+v; want no_account and none", got.Availability, got.Provider)
		}
	})
	t.Run("no provider at all", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		c, _ := signIn(t, h)
		got := getSync(t, c)
		if got.Availability != gen.MailboxSyncAvailabilityNoProvider || got.Provider != nil {
			t.Errorf("availability %s, provider %+v; want no_provider and none", got.Availability, got.Provider)
		}
	})
}
