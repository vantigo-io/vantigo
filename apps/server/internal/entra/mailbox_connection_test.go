package entra_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/entra/graphtest"
)

// The mailbox calendar on the person's own connection (Outlook connection
// design M9, M7): every call through their delegated token, answering
// contracts.ErrConnectionNeeded with its reason when the connection cannot
// serve it; the removals served by a closing connection while it holds its
// token and ErrMailboxGone once none can; the switch off; the markers on the
// connection's client id; the subscriptions the person's.

// mailboxCallResults runs every per-mailbox call on mailbox and answers
// each one's error by name: the removals (Find, DeleteCalendar, Apply of a
// delete, Unwatch) and the rest (EnsureCalendar, Apply of a create, Delta,
// Marked, Versions, Events, Watch). calendarID and eventID are what the
// removals name.
func mailboxCallResults(ctx context.Context, p contracts.MailboxCalendar, mailbox, calendarID, eventID string, now time.Time) (removals, rest map[string]error) {
	applyErr := func(ops []contracts.MailboxOp) error {
		results, err := p.Apply(ctx, mailbox, "", ops)
		if err != nil {
			return err
		}
		for _, r := range results {
			if r.Err != nil {
				return r.Err
			}
		}
		return nil
	}
	removals = map[string]error{
		"Find":           func() error { _, err := p.Find(ctx, mailbox, "", time.Time{}); return err }(),
		"Apply delete":   applyErr([]contracts.MailboxOp{{Kind: contracts.MailboxOpDelete, EventID: eventID}}),
		"DeleteCalendar": p.DeleteCalendar(ctx, mailbox, calendarID),
		"Unwatch":        p.Unwatch(ctx, mailbox),
	}
	rest = map[string]error{
		"EnsureCalendar": func() error { _, err := p.EnsureCalendar(ctx, mailbox, "", "Vantigo"); return err }(),
		"Apply create":   applyErr(creates(1, now)),
		"Apply mixed": applyErr([]contracts.MailboxOp{creates(1, now)[0],
			{Kind: contracts.MailboxOpDelete, EventID: eventID}}),
		"Delta":    func() error { _, err := p.Delta(ctx, mailbox, now, now.Add(time.Hour), ""); return err }(),
		"Marked":   func() error { _, err := p.Marked(ctx, mailbox, []string{eventID}); return err }(),
		"Versions": func() error { _, err := p.Versions(ctx, mailbox, "", now, now.Add(time.Hour)); return err }(),
		"Events":   func() error { _, err := p.Events(ctx, mailbox, "", []string{eventID}); return err }(),
		"Watch":    p.Watch(ctx, mailbox),
	}
	return removals, rest
}

// neededReason is err's connection reason, "" when it is no
// ErrConnectionNeeded.
func neededReason(err error) string {
	var n *contracts.ConnectionNeededError
	if !errors.Is(err, contracts.ErrConnectionNeeded) || !errors.As(err, &n) {
		return ""
	}
	return n.Reason
}

// A person who never connected: every call answers not_connected, the
// removals ErrMailboxGone — no connection can ever remove anything — and
// nothing reaches the mailbox.
func TestMailboxConnection_NotConnected(t *testing.T) {
	ctx := context.Background()
	h, g, _ := newNoticesHarness(t)
	p := mailboxProvider(h)
	id := addAccount(t, h, "E900", "kari@nordlys.no")
	h.Exec(t, `UPDATE entra.accounts SET state = 'enabled' WHERE object_id = $1`, id)
	g.AddUser(graphtest.User{ID: id.String(), UserPrincipalName: "kari@nordlys.no", AccountEnabled: true})
	subscribe(t, h, id)
	h.Exec(t, `UPDATE entra.mailbox_subscriptions SET subscription_id = $2, expires_at = $3, state = 'active' WHERE object_id = $1`,
		id, "0b8c3d4e-5f60-4a71-8b92-a3b4c5d6e7f8", h.Now().Add(time.Hour))

	removals, rest := mailboxCallResults(ctx, p, id.String(), "AAMkcal", "AAMkevent", h.Now())
	for name, err := range rest {
		if neededReason(err) != contracts.ConnectionNotConnected {
			t.Errorf("%s: %v, want ErrConnectionNeeded not_connected", name, err)
		}
	}
	for name, err := range removals {
		if name == "Unwatch" {
			continue
		}
		if !errors.Is(err, contracts.ErrMailboxGone) || errors.Is(err, contracts.ErrConnectionNeeded) {
			t.Errorf("%s: %v, want ErrMailboxGone alone", name, err)
		}
	}
	// Unwatch forgets the subscription no connection can delete.
	if err := removals["Unwatch"]; err != nil {
		t.Errorf("Unwatch: %v, want nil, the row forgotten", err)
	}
	if n := h.Count(t, `SELECT count(*) FROM entra.mailbox_subscriptions WHERE object_id = $1`, id); n != 0 {
		t.Error("the subscription row is kept")
	}
	if calls := mailboxCalls(g.Calls()); len(calls) != 0 {
		t.Errorf("the mailbox was asked: %+v", calls)
	}
}

// A disconnect (closing, its token kept): the removals delete with the
// person's token — Find, Apply of deletes, DeleteCalendar, Unwatch — while
// every other call, an Apply with a create among deletes too, is refused
// whole with closing.
func TestMailboxConnection_ADisconnectStillRemoves(t *testing.T) {
	ctx := context.Background()
	h, g, _ := newNoticesHarness(t)
	p := mailboxProvider(h)
	id, person := mailboxPerson(t, h, g)
	mailbox := id.String()
	cal, err := p.EnsureCalendar(ctx, mailbox, "", "Vantigo")
	if err != nil {
		t.Fatal(err)
	}
	results, err := p.Apply(ctx, mailbox, "", creates(2, h.Now()))
	if err != nil || results[0].Err != nil || results[1].Err != nil {
		t.Fatalf("Apply: %+v %v", results, err)
	}
	if err := p.Watch(ctx, mailbox); err != nil {
		t.Fatal(err)
	}
	if r := person.Do(http.MethodDelete, outlookConnectionPath, nil); r.Status != http.StatusNoContent {
		t.Fatalf("disconnect: %d %s", r.Status, r.Body)
	}

	before := len(mailboxCalls(g.Calls()))
	removals, rest := mailboxCallResults(ctx, p, mailbox, cal, results[0].EventID, h.Now())
	for name, err := range rest {
		if neededReason(err) != contracts.ConnectionClosingReason {
			t.Errorf("%s: %v, want ErrConnectionNeeded closing", name, err)
		}
	}
	for name, err := range removals {
		if err != nil {
			t.Errorf("%s while closing: %v, want done", name, err)
		}
	}
	if events := g.Events(mailbox, ""); len(events) != 1 || events[0].ID != results[1].EventID {
		t.Errorf("the default calendar holds %+v, want the second copy alone", events)
	}
	for _, c := range g.Calendars(mailbox) {
		if c.ID == cal {
			t.Error("the calendar is not deleted")
		}
	}
	if len(g.Subscriptions()) != 0 || h.Count(t, `SELECT count(*) FROM entra.mailbox_subscriptions`) != 0 {
		t.Error("the subscription is not deleted")
	}
	// Every call that reached the mailbox was a removal's: a read or a
	// delete, never a write.
	for _, c := range mailboxCalls(g.Calls())[before:] {
		if c.Method != http.MethodGet && c.Method != http.MethodDelete {
			t.Errorf("a %s %s while closing", c.Method, c.Path)
		}
	}
}

// Microsoft's refusals the person answers: a refused refresh (mfa) and a
// claims challenge (sign_in_again) move the connection to reconnect, and
// every call, the removals too, answers ErrConnectionNeeded with the code.
func TestMailboxConnection_ReconnectCodes(t *testing.T) {
	ctx := context.Background()
	h, g, _ := newNoticesHarness(t)
	p := mailboxProvider(h)

	mfa, _ := mailboxPerson(t, h, g)
	g.RefuseRefresh(h.outlook.refreshToken(t, mfa), "invalid_grant", 50076)
	removals, rest := mailboxCallResults(ctx, p, mfa.String(), "AAMkcal", "AAMkevent", h.Now())
	for name, err := range rest {
		if neededReason(err) != "mfa" {
			t.Errorf("%s: %v, want mfa", name, err)
		}
	}
	for name, err := range removals {
		if name != "Unwatch" && neededReason(err) != "mfa" {
			t.Errorf("%s: %v, want mfa", name, err)
		}
	}
	if r := h.outlook.row(t, mfa); r.State != "reconnect" || r.Code != "mfa" {
		t.Errorf("the connection = %+v, want reconnect mfa", r)
	}

	challenged, _ := mailboxPerson(t, h, g)
	g.ChallengeNext(challenged.String())
	if _, err := p.EnsureCalendar(ctx, challenged.String(), "", "Vantigo"); neededReason(err) != "sign_in_again" {
		t.Errorf("EnsureCalendar after a claims challenge: %v, want sign_in_again", err)
	}
	if _, err := p.Find(ctx, challenged.String(), "", time.Time{}); neededReason(err) != "sign_in_again" {
		t.Errorf("Find in reconnect: %v, want sign_in_again", err)
	}
	if r := h.outlook.row(t, challenged); r.State != "reconnect" || r.Code != "sign_in_again" {
		t.Errorf("the connection = %+v, want reconnect sign_in_again", r)
	}
}

// No token left for the removals — a leaver's close forgets it, as does an
// account closed on use, and a closing token Microsoft refuses is forgotten
// — answers ErrMailboxGone, never ErrConnectionNeeded; Unwatch forgets the
// subscription it can no longer delete, and the sweep then deletes the
// closing row a day on.
func TestMailboxConnection_NoTokenLeftIsTheMailboxGone(t *testing.T) {
	ctx := context.Background()
	h, g, _ := newNoticesHarness(t)
	p := mailboxProvider(h)
	gone := func(name string, err error) {
		t.Helper()
		if !errors.Is(err, contracts.ErrMailboxGone) || errors.Is(err, contracts.ErrConnectionNeeded) {
			t.Errorf("%s: %v, want ErrMailboxGone alone", name, err)
		}
	}

	// A leaver: Deactivate closes the connection forgetting its token
	// (lifecycle_deactivate.go, its own test); the row as it leaves it.
	leaver := mailboxUser(t, h, g)
	if err := p.Watch(ctx, leaver); err != nil {
		t.Fatal(err)
	}
	h.Exec(t, `UPDATE entra.outlook_connections SET state = 'closing', refresh_sealed = NULL, changed_at = $2, revision = revision + 1 WHERE object_id = $1`, leaver, h.Now())
	_, err := p.Find(ctx, leaver, "", time.Time{})
	gone("Find, a leaver", err)
	gone("DeleteCalendar, a leaver", p.DeleteCalendar(ctx, leaver, "AAMkcal"))
	if _, err := p.Apply(ctx, leaver, "", []contracts.MailboxOp{{Kind: contracts.MailboxOpDelete, EventID: "AAMkevent"}}); err != nil {
		gone("Apply of a delete, a leaver", err)
	} else {
		t.Error("Apply of a delete, a leaver: nil, want ErrMailboxGone")
	}
	if err := p.Unwatch(ctx, leaver); err != nil {
		t.Errorf("Unwatch, a leaver: %v, want nil", err)
	}
	if n := h.Count(t, `SELECT count(*) FROM entra.mailbox_subscriptions WHERE object_id = $1`, leaver); n != 0 {
		t.Error("the leaver's subscription row is kept")
	}
	s := outlookSweep(t, h)
	h.Advance(25 * time.Hour)
	if _, err := s.RunCycle(ctx); err != nil {
		t.Fatal(err)
	}
	if n := h.Count(t, `SELECT count(*) FROM entra.outlook_connections WHERE object_id = $1`, leaver); n != 0 {
		t.Error("the sweep kept the leaver's closing row a day on")
	}

	// An account closed on use: the call that finds it disabled needs its
	// person, closing; the removals after it find no token.
	disabled := mailboxUser(t, h, g)
	h.Exec(t, `UPDATE entra.accounts SET state = 'disabled' WHERE object_id = $1`, disabled)
	if _, err := p.EnsureCalendar(ctx, disabled, "", "Vantigo"); neededReason(err) != contracts.ConnectionClosingReason {
		t.Errorf("EnsureCalendar of a disabled account: %v, want closing", err)
	}
	_, err = p.Find(ctx, disabled, "", time.Time{})
	gone("Find, an account closed on use", err)

	// A disconnect whose token Microsoft then refuses: forgotten, gone.
	id, person := mailboxPerson(t, h, g)
	if r := person.Do(http.MethodDelete, outlookConnectionPath, nil); r.Status != http.StatusNoContent {
		t.Fatalf("disconnect: %d %s", r.Status, r.Body)
	}
	g.RefuseRefresh(h.outlook.refreshToken(t, id), "invalid_grant", 70008)
	_, err = p.Find(ctx, id.String(), "", time.Time{})
	gone("Find, a refused closing token", err)
	if n := h.Count(t, `SELECT count(*) FROM entra.outlook_connections WHERE object_id = $1 AND state = 'closing' AND refresh_sealed IS NULL`, id); n != 1 {
		t.Error("the refused closing token is not forgotten")
	}
	gone("DeleteCalendar, a refused closing token", p.DeleteCalendar(ctx, id.String(), "AAMkcal"))
}

// The switch off: Status cleanup, and every closed connection serves the
// removals — Find, Apply's deletes (its creates refused unsent),
// DeleteCalendar, Unwatch — and nothing else.
func TestMailboxConnection_TheSwitchOffRemovesOnEveryClosedConnection(t *testing.T) {
	ctx := context.Background()
	h, g, o := newNoticesHarness(t)
	p := mailboxProvider(h)
	type copies struct{ mailbox, cal, event string }
	var people []copies
	for range 2 {
		mailbox := mailboxUser(t, h, g)
		cal, err := p.EnsureCalendar(ctx, mailbox, "", "Vantigo")
		if err != nil {
			t.Fatal(err)
		}
		results, err := p.Apply(ctx, mailbox, "", creates(1, h.Now()))
		if err != nil || results[0].Err != nil {
			t.Fatalf("Apply: %+v %v", results, err)
		}
		if err := p.Watch(ctx, mailbox); err != nil {
			t.Fatal(err)
		}
		people = append(people, copies{mailbox, cal, results[0].EventID})
	}
	setOutlookConnect(t, o, false)
	if st, err := p.Status(ctx); err != nil || st.State != contracts.MailboxCleanup {
		t.Fatalf("Status = %+v %v, want cleanup", st, err)
	}
	if n := h.Count(t, `SELECT count(*) FROM entra.outlook_connections WHERE state = 'closing' AND refresh_sealed IS NOT NULL`); n != 2 {
		t.Fatalf("%d closed connections holding their token, want 2", n)
	}

	for _, c := range people {
		if refs, err := p.Find(ctx, c.mailbox, "", time.Time{}); err != nil || len(refs) != 1 {
			t.Errorf("Find under cleanup: %+v %v", refs, err)
		}
		results, err := p.Apply(ctx, c.mailbox, "", []contracts.MailboxOp{creates(1, h.Now())[0], {Kind: contracts.MailboxOpDelete, EventID: c.event}})
		if err != nil || !errors.Is(results[0].Err, contracts.ErrMailboxUnavailable) || results[1].Err != nil {
			t.Errorf("Apply under cleanup: %+v %v, want the create refused and the delete done", results, err)
		}
		if err := p.DeleteCalendar(ctx, c.mailbox, c.cal); err != nil {
			t.Errorf("DeleteCalendar under cleanup: %v", err)
		}
		if err := p.Unwatch(ctx, c.mailbox); err != nil {
			t.Errorf("Unwatch under cleanup: %v", err)
		}
		if _, err := p.EnsureCalendar(ctx, c.mailbox, "", "Vantigo"); !errors.Is(err, contracts.ErrMailboxUnavailable) {
			t.Errorf("EnsureCalendar under cleanup: %v, want ErrMailboxUnavailable", err)
		}
		if _, err := p.Delta(ctx, c.mailbox, h.Now(), h.Now().Add(time.Hour), ""); !errors.Is(err, contracts.ErrMailboxUnavailable) {
			t.Errorf("Delta under cleanup: %v, want ErrMailboxUnavailable", err)
		}
		if len(g.Events(c.mailbox, "")) != 0 {
			t.Errorf("%s keeps copies", c.mailbox)
		}
	}
	if len(g.Subscriptions()) != 0 {
		t.Errorf("subscriptions left: %+v", g.Subscriptions())
	}
}

// The markers take the connection's client id: a copy written before the
// person's token — the application credential carried the same client id —
// is still the installation's.
func TestMailboxConnection_MarkersOnTheConnectionsClientID(t *testing.T) {
	ctx := context.Background()
	h, g, _ := newMailboxHarness(t)
	p := mailboxProvider(h)
	mailbox := mailboxUser(t, h, g)
	start := h.Now().Add(time.Hour)
	before := g.AddEvent(mailbox, "", graphtest.Event{
		Subject: "Written before", Start: graphtest.DateTimeZone{DateTime: start.UTC().Format("2006-01-02T15:04:05"), TimeZone: "UTC"},
		End:        graphtest.DateTimeZone{DateTime: start.Add(time.Hour).UTC().Format("2006-01-02T15:04:05"), TimeZone: "UTC"},
		Properties: map[string]string{vantigoMarker: keyPrefix + "appointment:1"},
	})
	g.AddEvent(mailbox, "", graphtest.Event{
		Subject: "Another installation's", Start: before.Start, End: before.End,
		Properties: map[string]string{vantigoMarker: otherInstallKey + "appointment:1"},
	})
	refs, err := p.Find(ctx, mailbox, "", time.Time{})
	if err != nil || len(refs) != 1 || refs[0].EventID != before.ID || refs[0].Key != "appointment:1" {
		t.Errorf("Find = %+v %v, want the copy written before", refs, err)
	}
}

// The subscription is the person's: created and renewed with their token
// (its creator them). One the directory asked to reauthorize while the
// connection waits in reconnect is left alone — Watch answers the
// connection's reason, Graph unasked — and the Watch after a reconnect
// renews it.
func TestMailboxConnection_TheSubscriptionIsThePersons(t *testing.T) {
	ctx := context.Background()
	h, g, _ := newNoticesHarness(t)
	p := mailboxProvider(h)
	id, _ := mailboxPerson(t, h, g)
	mailbox := id.String()
	if err := p.Watch(ctx, mailbox); err != nil {
		t.Fatal(err)
	}
	sub := subscriptionOf(t, g, mailbox)
	if sub.CreatorID != mailbox {
		t.Errorf("the subscription's creator = %q, want %s", sub.CreatorID, mailbox)
	}
	h.Advance(5 * 24 * time.Hour)
	before := len(g.Calls())
	if err := p.Watch(ctx, mailbox); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(methods(subscriptionCalls(g.Calls()[before:])), ","); got != "PATCH OK" {
		t.Errorf("the renewal: %s, want PATCH OK", got)
	}

	h.Exec(t, `UPDATE entra.mailbox_subscriptions SET state = 'reauthorize' WHERE object_id = $1`, id)
	h.Exec(t, `UPDATE entra.outlook_connections SET state = 'reconnect', error_code = 'mfa', revision = revision + 1 WHERE object_id = $1`, id)
	before = len(g.Calls())
	if err := p.Watch(ctx, mailbox); neededReason(err) != "mfa" {
		t.Errorf("Watch in reconnect: %v, want mfa", err)
	}
	if calls := subscriptionCalls(g.Calls()[before:]); len(calls) != 0 {
		t.Errorf("Graph was asked: %v", methods(calls))
	}
	if n := h.Count(t, `SELECT count(*) FROM entra.mailbox_subscriptions WHERE object_id = $1 AND state = 'reauthorize'`, id); n != 1 {
		t.Error("the reauthorize row was touched")
	}

	reconnectMailbox(t, h, mailbox)
	before = len(g.Calls())
	if err := p.Watch(ctx, mailbox); err != nil {
		t.Fatalf("Watch after the reconnect: %v", err)
	}
	if got := strings.Join(methods(subscriptionCalls(g.Calls()[before:])), ","); got != "PATCH OK" {
		t.Errorf("after the reconnect: %s, want PATCH OK", got)
	}
	if n := h.Count(t, `SELECT count(*) FROM entra.mailbox_subscriptions WHERE object_id = $1 AND state = 'active'`, id); n != 1 {
		t.Error("the renewed row is not active")
	}
}

// The refresh's credential gone — every secret refused by the tenant —
// answers ErrMailboxUnavailable, as the application path did.
func TestMailboxConnection_NoCredentialForTheRefresh(t *testing.T) {
	ctx := context.Background()
	h, g, _ := newMailboxHarness(t)
	p := mailboxProvider(h)
	mailbox := mailboxUser(t, h, g)
	g.Configure(func(g *graphtest.FakeGraph) { g.Secrets[clientID] = []string{"another secret"} })
	h.Advance(2 * time.Hour) // the cached application and delegated tokens expired
	if _, err := p.EnsureCalendar(ctx, mailbox, "", "Vantigo"); !errors.Is(err, contracts.ErrMailboxUnavailable) {
		t.Errorf("EnsureCalendar with no credential: %v, want ErrMailboxUnavailable", err)
	}
}

// A subscription the application made before delivery B — its creator no
// person — cannot be renewed or deleted with the person's token: Graph
// answers 403. It is not this connection's to manage, so the row is
// forgotten — a renewal creates the person's own, an Unwatch is done — and
// no row is left deleting nor any refusal answered at every sync.
func TestMailboxConnection_TheApplicationsSubscriptionIsForgotten(t *testing.T) {
	ctx := context.Background()
	h, g, _ := newNoticesHarness(t)
	p := mailboxProvider(h)
	id, _ := mailboxPerson(t, h, g)
	mailbox := id.String()
	// The row holds the application's subscription, as B found it.
	applications := func() graphtest.Subscription {
		t.Helper()
		if err := p.Watch(ctx, mailbox); err != nil {
			t.Fatal(err)
		}
		own := subscriptionOf(t, g, mailbox)
		g.RemoveSubscription(own.ID)
		app := g.AddSubscription(graphtest.Subscription{Resource: own.Resource, ChangeType: own.ChangeType,
			NotificationURL: own.NotificationURL, LifecycleNotificationURL: own.LifecycleNotificationURL,
			ClientState: own.ClientState, Expiration: own.Expiration})
		h.Exec(t, `UPDATE entra.mailbox_subscriptions SET subscription_id = $2 WHERE object_id = $1`, id, app.ID)
		return app
	}

	app := applications()
	h.Advance(5 * 24 * time.Hour)
	before := len(g.Calls())
	if err := p.Watch(ctx, mailbox); err != nil {
		t.Fatalf("Watch of the application's subscription: %v", err)
	}
	if got := strings.Join(methods(subscriptionCalls(g.Calls()[before:])), ","); got != "PATCH Forbidden,POST Created" {
		t.Errorf("the renewal: %s, want the PATCH's 403 then a create", got)
	}
	var made graphtest.Subscription
	for _, s := range g.Subscriptions() {
		if s.ID != app.ID {
			made = s
		}
	}
	if made.CreatorID != mailbox {
		t.Errorf("the subscription made: %+v, want the person's", made)
	}
	if n := h.Count(t, `SELECT count(*) FROM entra.mailbox_subscriptions WHERE object_id = $1 AND subscription_id = $2 AND state = 'active'`, id, made.ID); n != 1 {
		t.Error("the row does not hold the person's subscription")
	}

	g.RemoveSubscription(made.ID)
	g.RemoveSubscription(app.ID)
	h.Exec(t, `DELETE FROM entra.mailbox_subscriptions WHERE object_id = $1`, id)
	applications()
	before = len(g.Calls())
	if err := p.Unwatch(ctx, mailbox); err != nil {
		t.Fatalf("Unwatch of the application's subscription: %v", err)
	}
	if got := strings.Join(methods(subscriptionCalls(g.Calls()[before:])), ","); got != "DELETE Forbidden" {
		t.Errorf("the delete: %s, want DELETE Forbidden", got)
	}
	if n := h.Count(t, `SELECT count(*) FROM entra.mailbox_subscriptions WHERE object_id = $1`, id); n != 0 {
		t.Error("the row is kept")
	}
}
