package entra_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/entra"
	"github.com/vantigo-io/vantigo/server/internal/entra/gen"
	"github.com/vantigo-io/vantigo/server/internal/entra/graphtest"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The calendar's mailbox provider (calendar Outlook sync design O2–O4)
// against the fake Graph: the status by the switch and the permission, the
// account check, Apply's batches and refusals, the calendar and Find.

const (
	vantigoMarker   = "String {7b1c5e3a-6f0d-4d5e-9a40-3c7a5e2b9d11} Name VantigoKey"
	calendarMarker  = "String {7b1c5e3a-6f0d-4d5e-9a40-3c7a5e2b9d11} Name VantigoCalendar"
	keyPrefix       = "vantigo:" + clientID + ":"
	calendarMark    = "vantigo:" + clientID
	otherInstallKey = "vantigo:9a8b7c6d-5e4f-4a3b-8c2d-1e0f9a8b7c6d:"
)

// mailboxProvider is the module's MailboxCalendar built on the harness's
// Deps, as Compose builds it.
func mailboxProvider(h *harness) contracts.MailboxCalendar {
	return entra.Module().MailboxCalendar(h.Deps())
}

// setOutlookConnect turns the Outlook connection switch on or off by c,
// which must hold entra:manage.
func setOutlookConnect(t *testing.T, c *modtest.Client, on bool) gen.EntraConnection {
	t.Helper()
	current := getConnection(t, c)
	r := c.Do(http.MethodPut, settingsPath, map[string]any{"outlookConnect": on}, ifMatch(current.Version))
	if r.Status != http.StatusOK {
		t.Fatalf("PUT /connection/settings outlookConnect=%v: status %d body %s", on, r.Status, r.Body)
	}
	var conn gen.EntraConnection
	r.JSON(&conn)
	return conn
}

// newMailboxHarness is a connected tenant whose token carries
// Calendars.ReadWrite, the switch on: the provider available. opts are the
// harness's besides.
func newMailboxHarness(t *testing.T, opts ...modtest.Option) (*harness, *graphtest.FakeGraph, *modtest.Client) {
	t.Helper()
	h, g := newTenantHarness(t, opts...)
	g.Configure(func(g *graphtest.FakeGraph) { g.Roles = append(g.Roles, "Calendars.ReadWrite") })
	o := connected(t, h)
	setOutlookConnect(t, o, true)
	return h, g, o
}

// mailboxUser is an account the module made, and its user in the tenant.
func mailboxUser(t *testing.T, h *harness, g *graphtest.FakeGraph, edit ...func(u *graphtest.User)) string {
	t.Helper()
	n := len(g.Users()) + 1
	id := addAccount(t, h, fmt.Sprintf("E%03d", n), fmt.Sprintf("u%d@nordlys.no", n))
	u := graphtest.User{ID: id.String(), UserPrincipalName: fmt.Sprintf("u%d@nordlys.no", n), AccountEnabled: true}
	for _, fn := range edit {
		fn(&u)
	}
	g.AddUser(u)
	return id.String()
}

// timed is a timed event an hour long from start.
func timed(subject string, start time.Time) *contracts.MailboxEvent {
	return &contracts.MailboxEvent{Subject: subject, Body: "Kept by Vantigo", StartsAt: start, EndsAt: start.Add(time.Hour), ShowAs: contracts.ShowAsBusy}
}

// creates are n create ops keyed key:0…, an hour apart from start.
func creates(n int, start time.Time) []contracts.MailboxOp {
	ops := make([]contracts.MailboxOp, n)
	for i := range ops {
		ops[i] = contracts.MailboxOp{Kind: contracts.MailboxOpCreate, Key: fmt.Sprintf("appointment:%d", i),
			Event: timed(fmt.Sprintf("Event %d", i), start.Add(time.Duration(i)*time.Hour))}
	}
	return ops
}

// batches counts the $batch requests among calls.
func batches(calls []graphtest.Call) int {
	n := 0
	for _, c := range calls {
		if c.Method == http.MethodPost && c.Path == "/v1.0/$batch" && !c.Batch {
			n++
		}
	}
	return n
}

// mailboxCalls are the calls made inside or outside a batch to a user's
// calendars or events.
func mailboxCalls(calls []graphtest.Call) []graphtest.Call {
	var out []graphtest.Call
	for _, c := range calls {
		if strings.Contains(c.Path, "/events") || strings.Contains(c.Path, "/calendars") {
			out = append(out, c)
		}
	}
	return out
}

// The status by the switch, the connection and the permission (design O2,
// O4), and GET /connection's outlookConnect beside it, with the permission
// among permissionsMissing only while the switch is on. Until a person
// connects, the switch on and the connection connected is untested,
// whatever the token carries: the role never judged the switch's status.
func TestMailbox_StatusAndTheConnectionsOutlookConnect(t *testing.T) {
	ctx := context.Background()
	h, g := newTenantHarness(t)
	p := mailboxProvider(h)
	o, _ := owner(t, h)
	if p.Key() != "entra" {
		t.Errorf("Key = %q", p.Key())
	}
	status := func(want, reason string) {
		t.Helper()
		got, err := p.Status(ctx)
		if err != nil || got.State != want || got.Reason != reason {
			t.Errorf("Status = %+v %v, want %s/%s", got, err, want, reason)
		}
	}
	outlookConnect := func(want gen.EntraOutlookConnectStatus, reason string, missing bool) {
		t.Helper()
		conn := getConnection(t, o)
		gotReason := ""
		if conn.OutlookConnect.Reason != nil {
			gotReason = *conn.OutlookConnect.Reason
		}
		if conn.OutlookConnect.Status != want || gotReason != reason {
			t.Errorf("outlookConnect = %+v (%q), want %s %q", conn.OutlookConnect, gotReason, want, reason)
		}
		if has := slices.Contains(conn.PermissionsMissing, "Calendars.ReadWrite"); has != missing {
			t.Errorf("permissionsMissing %v: Calendars.ReadWrite listed %v, want %v", conn.PermissionsMissing, has, missing)
		}
	}

	// A fresh installation: off, nothing to clean up with.
	status(contracts.MailboxUnavailable, "switch_off")
	outlookConnect(gen.EntraOutlookConnectStatusOff, "", false)

	// Connected without the calendar's permission: the test still connects.
	register(t, o)
	addSecret(t, o, secretValue, "Vantigo", h.Now().AddDate(1, 0, 0))
	if result := testConnection(t, o); result.Status != gen.EntraConnectionTestStatusConnected || len(result.PermissionsMissing) != 0 {
		t.Fatalf("the test without Calendars.ReadWrite, switch off: %+v, want connected with nothing missing", result)
	}
	status(contracts.MailboxCleanup, "switch_off")
	outlookConnect(gen.EntraOutlookConnectStatusOff, "", false)

	// On, the permission missing: the test still connects and names it.
	on := setOutlookConnect(t, o, true)
	if !on.OutlookConnect.Enabled || on.OutlookConnect.Status != gen.EntraOutlookConnectStatusPermissionMissing {
		t.Errorf("the PUT's answer: %+v", on.OutlookConnect)
	}
	result := testConnection(t, o)
	if result.Status != gen.EntraConnectionTestStatusConnected || !slices.Equal(result.PermissionsMissing, []string{"Calendars.ReadWrite"}) {
		t.Errorf("the test with the switch on: %+v, want connected missing Calendars.ReadWrite", result)
	}
	// The role not in the token is advisory only — an Exchange RBAC grant
	// never shows there — so the provider is available all the same.
	status(contracts.MailboxAvailable, "")
	outlookConnect(gen.EntraOutlookConnectStatusPermissionMissing, "", true)

	// Consented: available, and still untested.
	g.Configure(func(g *graphtest.FakeGraph) { g.Roles = append(g.Roles, "Calendars.ReadWrite") })
	testConnection(t, o)
	status(contracts.MailboxAvailable, "")
	outlookConnect(gen.EntraOutlookConnectStatusUntested, "", false)

	// The tenant refusing the secret: failing, unavailable with the reason.
	g.Configure(func(g *graphtest.FakeGraph) { g.Secrets[clientID] = []string{"another"} })
	testConnection(t, o)
	// The failed test saw no permission: Calendars.ReadWrite is missing with
	// the rest while the switch is on.
	status(contracts.MailboxUnavailable, "credential_invalid")
	outlookConnect(gen.EntraOutlookConnectStatusUnavailable, "credential_invalid", true)

	// Every secret revoked: unconfigured.
	h.Exec(t, `UPDATE entra.credentials SET revoked_at = $1`, h.Now())
	status(contracts.MailboxUnavailable, "unconfigured")
	outlookConnect(gen.EntraOutlookConnectStatusUnavailable, "", true)

	// The switch's changes are audited as settings changes.
	setOutlookConnect(t, o, false)
	status(contracts.MailboxUnavailable, "switch_off")
	outlookConnect(gen.EntraOutlookConnectStatusOff, "", false)
	if n := h.Count(t, `SELECT count(*) FROM entra.events WHERE action = 'entra.settings.changed' AND 'outlookConnect' = ANY(codes)`); n != 2 {
		t.Errorf("%d settings events naming outlookConnect, want 2 (on, off)", n)
	}
}

// The account check (design O3): a mailbox that is no object id, no row,
// an adoption nobody confirmed or a deleted account is ErrMailboxUnknown,
// before anything reaches the tenant; a confirmed adoption is written to.
func TestMailbox_OnlyAnAccountTheModuleManages(t *testing.T) {
	ctx := context.Background()
	h, g, _ := newMailboxHarness(t)
	p := mailboxProvider(h)

	adopted := uuid.New()
	h.Exec(t, `INSERT INTO entra.accounts (object_id, employee_number, user_principal_name, state, created_by_us, created_at, updated_at)
	           VALUES ($1, 'A1', 'adopted@nordlys.no', 'enabled', false, $2, $2)`, adopted, h.Now())
	g.AddUser(graphtest.User{ID: adopted.String(), UserPrincipalName: "adopted@nordlys.no", AccountEnabled: true})
	deleted := mailboxUser(t, h, g)
	h.Exec(t, `UPDATE entra.accounts SET state = 'deleted' WHERE object_id = $1`, deleted)
	stranger := uuid.New()
	g.AddUser(graphtest.User{ID: stranger.String(), UserPrincipalName: "stranger@nordlys.no", AccountEnabled: true})

	ops := creates(1, h.Now())
	for name, mailbox := range map[string]string{
		"no object id": "kari@nordlys.no", "no row": stranger.String(),
		"an unconfirmed adoption": adopted.String(), "a deleted account": deleted,
	} {
		if _, err := p.Apply(ctx, mailbox, "", ops); !errors.Is(err, contracts.ErrMailboxUnknown) {
			t.Errorf("Apply, %s: %v, want ErrMailboxUnknown", name, err)
		}
		if _, err := p.EnsureCalendar(ctx, mailbox, "", "Vantigo"); !errors.Is(err, contracts.ErrMailboxUnknown) {
			t.Errorf("EnsureCalendar, %s: %v, want ErrMailboxUnknown", name, err)
		}
		if err := p.DeleteCalendar(ctx, mailbox, "AAMk"); !errors.Is(err, contracts.ErrMailboxUnknown) {
			t.Errorf("DeleteCalendar, %s: %v, want ErrMailboxUnknown", name, err)
		}
		if _, err := p.Find(ctx, mailbox, "", time.Time{}); !errors.Is(err, contracts.ErrMailboxUnknown) {
			t.Errorf("Find, %s: %v, want ErrMailboxUnknown", name, err)
		}
	}
	if calls := mailboxCalls(g.Calls()); len(calls) != 0 {
		t.Errorf("the tenant was asked: %+v", calls)
	}

	h.Exec(t, `UPDATE entra.accounts SET adoption_confirmed_at = $2 WHERE object_id = $1`, adopted, h.Now())
	results, err := p.Apply(ctx, adopted.String(), "", ops)
	if err != nil || len(results) != 1 || results[0].Err != nil || results[0].EventID == "" {
		t.Errorf("a confirmed adoption: %+v %v, want the event made", results, err)
	}
}

// Apply in $batch requests of twenty, one after another: 45 creates in
// three, the marker on each, then 45 updates without it and 45 deletes.
func TestMailbox_ApplyInBatchesOfTwenty(t *testing.T) {
	ctx := context.Background()
	h, g, _ := newMailboxHarness(t)
	p := mailboxProvider(h)
	mailbox := mailboxUser(t, h, g)
	start := time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC)

	before := batches(g.Calls())
	results, err := p.Apply(ctx, mailbox, "", creates(45, start))
	if err != nil {
		t.Fatal(err)
	}
	if n := batches(g.Calls()) - before; n != 3 {
		t.Errorf("%d batches for 45 creates, want 3", n)
	}
	events := g.Events(mailbox, "")
	if len(results) != 45 || len(events) != 45 {
		t.Fatalf("%d results, %d events, want 45 each", len(results), len(events))
	}
	byID := map[string]graphtest.Event{}
	for _, e := range events {
		byID[e.ID] = e
	}
	for i, r := range results {
		e, ok := byID[r.EventID]
		if r.Err != nil || !ok || r.ChangeKey == "" || r.ChangeKey != e.ChangeKey {
			t.Fatalf("result %d: %+v (event %+v)", i, r, e)
		}
		if e.Properties[vantigoMarker] != fmt.Sprintf(keyPrefix+"appointment:%d", i) || e.Subject != fmt.Sprintf("Event %d", i) ||
			e.IsReminderOn || e.Start.TimeZone != "UTC" || e.Start.DateTime != start.Add(time.Duration(i)*time.Hour).Format("2006-01-02T15:04:05") {
			t.Fatalf("event %d: %+v", i, e)
		}
	}

	updates := make([]contracts.MailboxOp, len(results))
	for i, r := range results {
		updates[i] = contracts.MailboxOp{Kind: contracts.MailboxOpUpdate, EventID: r.EventID, Event: timed("Moved", start.AddDate(0, 0, 1))}
	}
	before = batches(g.Calls())
	updated, err := p.Apply(ctx, mailbox, "", updates)
	if err != nil {
		t.Fatal(err)
	}
	if n := batches(g.Calls()) - before; n != 3 {
		t.Errorf("%d batches for 45 updates, want 3", n)
	}
	for i, e := range g.Events(mailbox, "") {
		if _, sent := e.LastWrite["singleValueExtendedProperties"]; sent || e.Subject != "Moved" || e.Properties[vantigoMarker] == "" {
			t.Fatalf("event %d after the update: %+v", i, e)
		}
		if updated[i].Err != nil || updated[i].EventID != results[i].EventID || updated[i].ChangeKey == results[i].ChangeKey {
			t.Fatalf("update %d: %+v", i, updated[i])
		}
	}

	deletes := make([]contracts.MailboxOp, len(results))
	for i, r := range results {
		deletes[i] = contracts.MailboxOp{Kind: contracts.MailboxOpDelete, EventID: r.EventID}
	}
	deleted, err := p.Apply(ctx, mailbox, "", deletes)
	if err != nil {
		t.Fatal(err)
	}
	for i, r := range deleted {
		if r.Err != nil || r.Gone {
			t.Errorf("delete %d: %+v", i, r)
		}
	}
	if n := len(g.Events(mailbox, "")); n != 0 {
		t.Errorf("%d events left", n)
	}
}

// Gone: an update or a delete of an event no longer there; an op the
// caller built wrong is its own error, the others done.
func TestMailbox_GoneAndBadOps(t *testing.T) {
	ctx := context.Background()
	h, g, _ := newMailboxHarness(t)
	p := mailboxProvider(h)
	mailbox := mailboxUser(t, h, g)
	results, err := p.Apply(ctx, mailbox, "", []contracts.MailboxOp{
		{Kind: contracts.MailboxOpUpdate, EventID: "AAMkgone=", Event: timed("x", h.Now())},
		{Kind: contracts.MailboxOpDelete, EventID: "AAMkgone="},
		{Kind: contracts.MailboxOpCreate, Event: timed("no key", h.Now())},
		{Kind: "rename", EventID: "AAMk"},
		{Kind: contracts.MailboxOpCreate, Key: "appointment:1", Event: timed("made", h.Now())},
	})
	if err != nil {
		t.Fatal(err)
	}
	if r := results[0]; !r.Gone || r.Err != nil || r.EventID != "AAMkgone=" {
		t.Errorf("the update: %+v, want Gone", r)
	}
	if r := results[1]; !r.Gone || r.Err != nil {
		t.Errorf("the delete: %+v, want Gone and done", r)
	}
	if results[2].Err == nil || results[3].Err == nil {
		t.Errorf("the bad ops: %+v %+v, want errors", results[2], results[3])
	}
	if results[4].Err != nil || results[4].EventID == "" {
		t.Errorf("the create: %+v", results[4])
	}
}

// A mailbox outside the application's scope is ErrMailboxDenied per op, a
// user with no mailbox, or none at all, ErrMailboxGone; the ops after the
// first batch are not sent.
func TestMailbox_DeniedAndGone(t *testing.T) {
	ctx := context.Background()
	h, g, _ := newMailboxHarness(t)
	p := mailboxProvider(h)
	denied := mailboxUser(t, h, g, func(u *graphtest.User) { u.MailboxDenied = true })
	noMailbox := mailboxUser(t, h, g, func(u *graphtest.User) { u.NoMailbox = true })
	missing := addAccount(t, h, "E900", "gone@nordlys.no").String()

	for name, c := range map[string]struct {
		mailbox string
		want    error
	}{
		"denied": {denied, contracts.ErrMailboxDenied}, "no mailbox": {noMailbox, contracts.ErrMailboxGone},
		"no such user": {missing, contracts.ErrMailboxGone},
	} {
		before := batches(g.Calls())
		results, err := p.Apply(ctx, c.mailbox, "", creates(25, h.Now()))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for i, r := range results {
			if !errors.Is(r.Err, c.want) {
				t.Errorf("%s, op %d: %v, want %v", name, i, r.Err, c.want)
			}
		}
		if n := batches(g.Calls()) - before; n != 1 {
			t.Errorf("%s: %d batches, want the first alone", name, n)
		}
		if _, err := p.EnsureCalendar(ctx, c.mailbox, "", "Vantigo"); !errors.Is(err, c.want) {
			t.Errorf("%s, EnsureCalendar: %v, want %v", name, err, c.want)
		}
		if _, err := p.Find(ctx, c.mailbox, "", time.Time{}); !errors.Is(err, c.want) {
			t.Errorf("%s, Find: %v, want %v", name, err, c.want)
		}
		if err := p.DeleteCalendar(ctx, c.mailbox, "AAMk"); !errors.Is(err, c.want) {
			t.Errorf("%s, DeleteCalendar: %v, want %v", name, err, c.want)
		}
	}
}

// A 429 inside a batch is sent again once, alone, after its Retry-After; a
// second 429 is the op's failure to retry, no sentinel.
func TestMailbox_ThrottledInsideTheBatch(t *testing.T) {
	ctx := context.Background()
	h, g, _ := newMailboxHarness(t)
	p := mailboxProvider(h)
	mailbox := mailboxUser(t, h, g)

	g.ThrottleBatch(3)
	before := batches(g.Calls())
	results, err := p.Apply(ctx, mailbox, "", creates(5, h.Now()))
	if err != nil {
		t.Fatal(err)
	}
	for i, r := range results {
		if r.Err != nil || r.EventID == "" {
			t.Errorf("op %d: %+v, want made on the retry", i, r)
		}
	}
	if n := batches(g.Calls()) - before; n != 2 {
		t.Errorf("%d batches, want the batch and the retry of the throttled", n)
	}
	if n := len(g.Events(mailbox, "")); n != 5 {
		t.Errorf("%d events, want 5", n)
	}

	g.ThrottleBatch(4)
	results, err = p.Apply(ctx, mailbox, "", creates(2, h.Now()))
	if err != nil {
		t.Fatal(err)
	}
	for i, r := range results {
		if r.Err == nil || errors.Is(r.Err, contracts.ErrMailboxDenied) || errors.Is(r.Err, contracts.ErrMailboxGone) || errors.Is(r.Err, contracts.ErrMailboxUnavailable) {
			t.Errorf("op %d throttled twice: %v, want a failure to retry", i, r.Err)
		}
	}
}

// A $batch that fails whole fails the call: no result.
func TestMailbox_ABatchThatFailsWhole(t *testing.T) {
	ctx := context.Background()
	h, g, _ := newMailboxHarness(t)
	p := mailboxProvider(h)
	mailbox := mailboxUser(t, h, g)
	g.FailNext(http.StatusServiceUnavailable)
	results, err := p.Apply(ctx, mailbox, "", creates(3, h.Now()))
	if err == nil || results != nil {
		t.Errorf("a batch of creates answered 503: %+v %v, want the call failing", results, err)
	}
	// A batch of creates is not sent twice: the tenant may have made them.
	if n := batches(g.Calls()); n != 1 {
		t.Errorf("%d batches, want the one", n)
	}
	// Without a create, a 5xx is retried once.
	g.FailNext(http.StatusServiceUnavailable)
	results, err = p.Apply(ctx, mailbox, "", []contracts.MailboxOp{{Kind: contracts.MailboxOpDelete, EventID: "AAMkgone="}})
	if err != nil || len(results) != 1 || !results[0].Gone {
		t.Errorf("a delete after a 503: %+v %v, want retried and Gone", results, err)
	}
}

// Under cleanup — the switch off — only deletes are taken: the other ops
// answer ErrMailboxUnavailable unsent, EnsureCalendar is refused, Find and
// DeleteCalendar run; unavailable refuses every call.
func TestMailbox_CleanupTakesDeletesOnly(t *testing.T) {
	ctx := context.Background()
	h, g, o := newMailboxHarness(t)
	p := mailboxProvider(h)
	mailbox := mailboxUser(t, h, g)
	cal, err := p.EnsureCalendar(ctx, mailbox, "", "Vantigo")
	if err != nil {
		t.Fatal(err)
	}
	made, err := p.Apply(ctx, mailbox, "", creates(2, h.Now()))
	if err != nil {
		t.Fatal(err)
	}

	setOutlookConnect(t, o, false)
	if st, _ := p.Status(ctx); st.State != contracts.MailboxCleanup {
		t.Fatalf("Status = %+v, want cleanup", st)
	}
	before := len(mailboxCalls(g.Calls()))
	results, err := p.Apply(ctx, mailbox, "", []contracts.MailboxOp{
		creates(1, h.Now())[0],
		{Kind: contracts.MailboxOpUpdate, EventID: made[0].EventID, Event: timed("x", h.Now())},
		{Kind: contracts.MailboxOpDelete, EventID: made[1].EventID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(results[0].Err, contracts.ErrMailboxUnavailable) || !errors.Is(results[1].Err, contracts.ErrMailboxUnavailable) {
		t.Errorf("a create and an update in cleanup: %v / %v, want ErrMailboxUnavailable", results[0].Err, results[1].Err)
	}
	if results[2].Err != nil {
		t.Errorf("a delete in cleanup: %v", results[2].Err)
	}
	if sent := mailboxCalls(g.Calls())[before:]; len(sent) != 1 || sent[0].Method != http.MethodDelete {
		t.Errorf("sent in cleanup: %+v, want the delete alone", sent)
	}
	if _, err := p.EnsureCalendar(ctx, mailbox, cal, "Vantigo"); !errors.Is(err, contracts.ErrMailboxUnavailable) {
		t.Errorf("EnsureCalendar in cleanup: %v, want ErrMailboxUnavailable", err)
	}
	if refs, err := p.Find(ctx, mailbox, "", time.Time{}); err != nil || len(refs) != 1 {
		t.Errorf("Find in cleanup: %+v %v, want the one left", refs, err)
	}
	if err := p.DeleteCalendar(ctx, mailbox, cal); err != nil {
		t.Errorf("DeleteCalendar in cleanup: %v", err)
	}

	h.Exec(t, `UPDATE entra.credentials SET revoked_at = $1`, h.Now())
	if _, err := p.Apply(ctx, mailbox, "", []contracts.MailboxOp{{Kind: contracts.MailboxOpDelete, EventID: made[0].EventID}}); !errors.Is(err, contracts.ErrMailboxUnavailable) {
		t.Errorf("Apply while unavailable: %v, want ErrMailboxUnavailable", err)
	}
	if _, err := p.Find(ctx, mailbox, "", time.Time{}); !errors.Is(err, contracts.ErrMailboxUnavailable) {
		t.Errorf("Find while unavailable: %v, want ErrMailboxUnavailable", err)
	}
}

// EnsureCalendar keeps a calendar still there, makes a new one for one
// gone, and finds the one of its name when the create says it exists;
// DeleteCalendar of one gone is done.
func TestMailbox_EnsureAndDeleteTheCalendar(t *testing.T) {
	ctx := context.Background()
	h, g, _ := newMailboxHarness(t)
	p := mailboxProvider(h)
	mailbox := mailboxUser(t, h, g)

	first, err := p.EnsureCalendar(ctx, mailbox, "", "Vantigo")
	if err != nil || first == "" {
		t.Fatalf("the first: %q %v", first, err)
	}
	before := len(mailboxCalls(g.Calls()))
	kept, err := p.EnsureCalendar(ctx, mailbox, first, "Vantigo")
	if err != nil || kept != first {
		t.Errorf("a known calendar: %q %v, want %q", kept, err, first)
	}
	if sent := mailboxCalls(g.Calls())[before:]; len(sent) != 1 || sent[0].Method != http.MethodGet {
		t.Errorf("a known calendar: %+v, want one GET", sent)
	}
	// The one name made already, the id forgotten: found by its name.
	if again, err := p.EnsureCalendar(ctx, mailbox, "", "Vantigo"); err != nil || again != first {
		t.Errorf("a create of a name there: %q %v, want %q", again, err, first)
	}

	if err := p.DeleteCalendar(ctx, mailbox, first); err != nil {
		t.Fatal(err)
	}
	if err := p.DeleteCalendar(ctx, mailbox, first); err != nil {
		t.Errorf("a delete of a calendar gone: %v, want nil", err)
	}
	remade, err := p.EnsureCalendar(ctx, mailbox, first, "Vantigo")
	if err != nil || remade == "" || remade == first {
		t.Errorf("a known calendar gone: %q %v, want a new one", remade, err)
	}
	if cals := g.Calendars(mailbox); len(cals) != 2 || cals[1].ID != remade || cals[1].Name != "Vantigo" || cals[1].Properties[calendarMarker] != calendarMark {
		t.Errorf("the calendars: %+v", cals)
	}
}

// Find: in either calendar the events carrying this installation's marker
// only, across pages — never one of the person's own, nor one another
// installation sharing the tenant wrote — and, with a from, those starting
// at or after it.
func TestMailbox_Find(t *testing.T) {
	ctx := context.Background()
	h, g, _ := newMailboxHarness(t)
	p := mailboxProvider(h)
	mailbox := mailboxUser(t, h, g)
	start := time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC)

	made, err := p.Apply(ctx, mailbox, "", creates(150, start))
	if err != nil {
		t.Fatal(err)
	}
	g.AddEvent(mailbox, "", graphtest.Event{Subject: "Dentist"})
	g.AddEvent(mailbox, "", graphtest.Event{Subject: "Someone else's", Properties: map[string]string{vantigoMarker: "other:1"}})
	g.AddEvent(mailbox, "", graphtest.Event{Subject: "Another installation's", Properties: map[string]string{vantigoMarker: otherInstallKey + "appointment:1"}})
	refs, err := p.Find(ctx, mailbox, "", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 150 {
		t.Fatalf("%d refs, want the 150 marked", len(refs))
	}
	for i, r := range refs {
		if r.EventID != made[i].EventID || r.Key != fmt.Sprintf("appointment:%d", i) {
			t.Fatalf("ref %d: %+v", i, r)
		}
	}
	pages := 0
	for _, c := range g.Calls() {
		if c.Method == http.MethodGet && strings.HasSuffix(c.Path, "/events") && !c.Batch {
			pages++
		}
	}
	if pages != 2 {
		t.Errorf("%d pages, want 2 of a hundred", pages)
	}

	// From the 100th hour on: the last fifty.
	refs, err = p.Find(ctx, mailbox, "", start.Add(100*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 50 || refs[0].Key != "appointment:100" {
		t.Errorf("from the 100th hour: %d refs (%+v), want the last 50", len(refs), refs)
	}

	cal, err := p.EnsureCalendar(ctx, mailbox, "", "Vantigo")
	if err != nil {
		t.Fatal(err)
	}
	inCal, err := p.Apply(ctx, mailbox, cal, creates(1, start))
	if err != nil || inCal[0].Err != nil {
		t.Fatalf("a create in the calendar: %+v %v", inCal, err)
	}
	if n := len(g.Events(mailbox, cal)); n != 1 {
		t.Errorf("%d events in the calendar, want 1", n)
	}
	g.AddEvent(mailbox, cal, graphtest.Event{Subject: "Put there by hand"})
	g.AddEvent(mailbox, cal, graphtest.Event{Subject: "Another installation's", Properties: map[string]string{vantigoMarker: otherInstallKey + "appointment:0"}})
	before := len(g.Calls())
	refs, err = p.Find(ctx, mailbox, cal, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	want := []contracts.MailboxEventRef{{EventID: inCal[0].EventID, Key: "appointment:0"}}
	if !slices.Equal(refs, want) {
		t.Errorf("the calendar's refs: %+v, want %+v", refs, want)
	}
	for _, c := range g.Calls()[before:] {
		if strings.HasSuffix(c.Path, "/events") && !strings.Contains(c.Query, "startswith") {
			t.Errorf("the calendar's list is not filtered on the marker: %s", c.Query)
		}
	}

	// A copy the person turned into a meeting is answered Meeting, its
	// attendees counted.
	g.EditEvent(mailbox, inCal[0].EventID, func(e *graphtest.Event) { e.Attendees = []string{"kari@nordlys.no"} })
	refs, err = p.Find(ctx, mailbox, cal, time.Time{})
	if err != nil || len(refs) != 1 || !refs[0].Meeting {
		t.Errorf("a copy made a meeting: %+v %v, want Meeting", refs, err)
	}
	if refs, _ := p.Find(ctx, mailbox, "", start.Add(149*time.Hour)); len(refs) != 1 || refs[0].Meeting {
		t.Errorf("a copy with no attendees: %+v, want no meeting", refs)
	}

	// A calendar that is gone holds nothing of ours.
	if refs, err := p.Find(ctx, mailbox, "AAMkgone=", time.Time{}); err != nil || len(refs) != 0 {
		t.Errorf("a calendar gone: %+v %v, want none and no error", refs, err)
	}
}

// The calendar's own marker (design O4, O8): a calendar of the name that the
// module did not make is never adopted — the next name is made — and never
// deleted; a known id carrying no marker is not kept; five names taken by
// strangers is a failure.
func TestMailbox_TheCalendarIsAdoptedAndDeletedOnlyWithItsMarker(t *testing.T) {
	ctx := context.Background()
	h, g, _ := newMailboxHarness(t)
	p := mailboxProvider(h)
	mailbox := mailboxUser(t, h, g)

	persons := g.AddCalendar(mailbox, "Vantigo")
	g.AddEvent(mailbox, persons.ID, graphtest.Event{Subject: "My own"})
	others := g.AddCalendar(mailbox, "Vantigo (2)", map[string]string{calendarMarker: "vantigo:9a8b7c6d-5e4f-4a3b-8c2d-1e0f9a8b7c6d"})

	made, err := p.EnsureCalendar(ctx, mailbox, "", "Vantigo")
	if err != nil || made == persons.ID || made == others.ID {
		t.Fatalf("EnsureCalendar past strangers' calendars: %q %v", made, err)
	}
	var mine graphtest.Calendar
	for _, c := range g.Calendars(mailbox) {
		if c.ID == made {
			mine = c
		}
	}
	if mine.Name != "Vantigo (3)" || mine.Properties[calendarMarker] != calendarMark {
		t.Errorf("the calendar made: %+v, want Vantigo (3) carrying the marker", mine)
	}
	// The one name's create lost its answer: adopted by name and marker.
	if again, err := p.EnsureCalendar(ctx, mailbox, "", "Vantigo"); err != nil || again != made {
		t.Errorf("EnsureCalendar again: %q %v, want %q adopted", again, err, made)
	}
	// A known id that is the person's own is not kept.
	if kept, err := p.EnsureCalendar(ctx, mailbox, persons.ID, "Vantigo"); err != nil || kept != made {
		t.Errorf("EnsureCalendar of the person's calendar: %q %v, want ours %q", kept, err, made)
	}

	for _, id := range []string{persons.ID, others.ID} {
		if err := p.DeleteCalendar(ctx, mailbox, id); !errors.Is(err, contracts.ErrCalendarNotOurs) {
			t.Errorf("DeleteCalendar of a stranger's calendar: %v, want ErrCalendarNotOurs", err)
		}
	}
	for _, c := range g.Calls() {
		if c.Method == http.MethodDelete && (strings.Contains(c.Path, persons.ID) || strings.Contains(c.Path, others.ID)) {
			t.Errorf("a stranger's calendar was sent a DELETE: %+v", c)
		}
	}
	if n := len(g.Events(mailbox, persons.ID)); n != 1 {
		t.Errorf("the person's calendar holds %d events, want its one", n)
	}
	if err := p.DeleteCalendar(ctx, mailbox, made); err != nil {
		t.Errorf("DeleteCalendar of ours: %v", err)
	}

	// Every name taken by strangers: a failure, no sentinel.
	for _, name := range []string{"Vantigo (3)", "Vantigo (4)", "Vantigo (5)"} {
		g.AddCalendar(mailbox, name)
	}
	if _, err := p.EnsureCalendar(ctx, mailbox, "", "Vantigo"); err == nil || errors.Is(err, contracts.ErrMailboxUnavailable) {
		t.Errorf("five names taken: %v, want a failure", err)
	}
}

// A $batch's 429 whose Retry-After is a number is read; one longer than
// thirty seconds is not waited for — the op fails at once, to be retried —
// and a cancelled context fails the call.
func TestMailbox_ThrottleWaitsAreBounded(t *testing.T) {
	ctx := context.Background()
	h, g, _ := newMailboxHarness(t)
	p := mailboxProvider(h)
	mailbox := mailboxUser(t, h, g)

	g.ThrottleBatchFor(1, 2)
	before := batches(g.Calls())
	results, err := p.Apply(ctx, mailbox, "", creates(1, h.Now()))
	if err != nil || results[0].Err != nil {
		t.Fatalf("a 429 of two seconds as a number: %+v %v, want retried and made", results, err)
	}
	if n := batches(g.Calls()) - before; n != 2 {
		t.Errorf("%d batches, want the batch and its retry", n)
	}

	g.ThrottleBatchFor(1, 45)
	before = batches(g.Calls())
	results, err = p.Apply(ctx, mailbox, "", creates(1, h.Now()))
	if err != nil || results[0].Err == nil {
		t.Fatalf("a 429 of 45 seconds: %+v %v, want the op failing", results, err)
	}
	if n := batches(g.Calls()) - before; n != 1 {
		t.Errorf("%d batches, want no retry", n)
	}
	g.ThrottleBatchFor(0, 0)

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := p.Apply(cancelled, mailbox, "", creates(1, h.Now())); err == nil {
		t.Error("Apply on a cancelled context: no error")
	}
}

// The event in the mailbox (design O4, O6): an all-day absence at midnight
// in W. Europe Standard Time for Europe/Oslo, the end the day after the
// last; a timed event in UTC; private; no reminder.
func TestMailbox_TheEventsShape(t *testing.T) {
	ctx := context.Background()
	h, g, _ := newMailboxHarness(t)
	p := mailboxProvider(h)
	mailbox := mailboxUser(t, h, g)
	oslo, _ := time.LoadLocation("Europe/Oslo")

	results, err := p.Apply(ctx, mailbox, "", []contracts.MailboxOp{
		{Kind: contracts.MailboxOpCreate, Key: "staff.absence:7", Event: &contracts.MailboxEvent{
			Subject: "Ferie", AllDay: true, TimeZone: "Europe/Oslo", ShowAs: contracts.ShowAsOOF,
			StartDate: time.Date(2026, 7, 6, 0, 0, 0, 0, time.UTC), EndDate: time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC),
		}},
		{Kind: contracts.MailboxOpCreate, Key: "appointment:9", Event: &contracts.MailboxEvent{
			Subject: "Lege", Body: "Kept by Vantigo", Location: "Storgata 1", Private: true, ShowAs: contracts.ShowAsBusy,
			StartsAt: time.Date(2026, 7, 6, 14, 30, 0, 0, oslo), EndsAt: time.Date(2026, 7, 6, 15, 0, 0, 0, oslo),
		}},
	})
	if err != nil || results[0].Err != nil || results[1].Err != nil {
		t.Fatalf("%+v %v", results, err)
	}
	events := g.Events(mailbox, "")
	absence, appointment := events[0], events[1]
	if !absence.IsAllDay || absence.Start != (graphtest.DateTimeZone{DateTime: "2026-07-06T00:00:00", TimeZone: "W. Europe Standard Time"}) ||
		absence.End != (graphtest.DateTimeZone{DateTime: "2026-07-25T00:00:00", TimeZone: "W. Europe Standard Time"}) ||
		absence.ShowAs != "oof" || absence.Sensitivity != "normal" || absence.IsReminderOn {
		t.Errorf("the absence: %+v", absence)
	}
	if appointment.IsAllDay || appointment.Start != (graphtest.DateTimeZone{DateTime: "2026-07-06T12:30:00", TimeZone: "UTC"}) ||
		appointment.End != (graphtest.DateTimeZone{DateTime: "2026-07-06T13:00:00", TimeZone: "UTC"}) ||
		appointment.Sensitivity != "private" || appointment.Location != "Storgata 1" || appointment.BodyType != "text" || appointment.IsReminderOn {
		t.Errorf("the appointment: %+v", appointment)
	}
}

// Connections answers the connections that moved after since, oldest first
// (Outlook connection design M9): a connect, a disconnect and a reconnect
// each answered once, read between the moves — a row holds its last move
// alone; since exclusive; a refresh moving nothing; the switch off; at most
// contracts.ConnectionChangesMax. The rows alone, whatever the switch.
func TestMailbox_Connections(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newTokenFixture(t)
	p := mailboxProvider(f.h)
	mailbox := f.oid.String()
	answers := func(since time.Time, want ...contracts.ConnectionChange) {
		t.Helper()
		got, err := p.Connections(ctx, since)
		if err != nil || !connectionChangesEqual(got, want) {
			t.Errorf("Connections(%s) = %+v %v, want %+v", since, got, err, want)
		}
	}

	connected := f.h.Now()
	answers(connected.Add(-time.Second), contracts.ConnectionChange{Mailbox: mailbox, State: contracts.ConnectionActive, At: connected})
	answers(connected)

	f.h.Advance(time.Minute)
	if _, err := f.token(false); err != nil {
		t.Fatalf("token: %v", err)
	}
	answers(connected)

	f.h.Advance(time.Minute)
	if r := f.c.Do(http.MethodDelete, outlookConnectionPath, nil); r.Status != http.StatusNoContent {
		t.Fatalf("disconnect: %d %s", r.Status, r.Body)
	}
	closed := f.h.Now()
	answers(connected, contracts.ConnectionChange{Mailbox: mailbox, State: contracts.ConnectionClosing, At: closed})
	answers(closed)

	f.h.Advance(time.Minute)
	if r := f.connectAs(t, f.c, f.oid, kari); r.Status != http.StatusOK {
		t.Fatalf("reconnect: %d %s", r.Status, r.Body)
	}
	reconnected := f.h.Now()
	answers(closed, contracts.ConnectionChange{Mailbox: mailbox, State: contracts.ConnectionActive, At: reconnected})

	// The switch off closes every live row, and Connections answers it
	// with the switch off.
	f.h.Advance(time.Minute)
	setOutlookConnect(t, f.o, false)
	answers(reconnected, contracts.ConnectionChange{Mailbox: mailbox, State: contracts.ConnectionClosing, At: f.h.Now()})

	// The cap: the oldest ConnectionChangesMax.
	later := f.h.Now().Add(time.Hour)
	f.h.Exec(t, `INSERT INTO entra.outlook_connections
	    (object_id, user_id, tenant_id, scopes, state, connected_at, changed_at, created_at, updated_at)
	    SELECT gen_random_uuid(), gen_random_uuid(), $1, '{}', 'closing', $2, $2::timestamptz + n * interval '1 millisecond', $2, $2
	    FROM generate_series(1, $3::int) AS n`, tenantID, later, contracts.ConnectionChangesMax+1)
	got, err := p.Connections(ctx, f.h.Now())
	if err != nil || len(got) != contracts.ConnectionChangesMax {
		t.Fatalf("over the cap = %d %v, want %d", len(got), err, contracts.ConnectionChangesMax)
	}
	for i := 1; i < len(got); i++ {
		if !got[i-1].At.Before(got[i].At) {
			t.Fatalf("change %d at %s after %s: not oldest first", i, got[i].At, got[i-1].At)
		}
	}
	if !got[0].At.Equal(later.Add(time.Millisecond)) {
		t.Errorf("the first change at %s, want the oldest", got[0].At)
	}
}

// connectionChangesEqual compares changes with their times as instants.
func connectionChangesEqual(a, b []contracts.ConnectionChange) bool {
	return slices.EqualFunc(a, b, func(x, y contracts.ConnectionChange) bool {
		return x.Mailbox == y.Mailbox && x.State == y.State && x.At.Equal(y.At)
	})
}
