package calendar_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/calendar"
	"github.com/vantigo-io/vantigo/server/internal/calendar/gen"
	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/mail"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// harness is one calendar installation for one test: internal/modtest's
// shared harness composing this module beside identity, every exchange
// validated against calendar.yaml through the package recorder, and the five
// providers of delivery A as fakes in Deps.CalendarSources (fakes_test.go).
//
// The user directory is identity's own — modtest always composes identity —
// so a test shapes what it answers through identity's tables (directoryUser),
// and every Notify it is asked for is on h.Notices().
//
// MAIL_DRIVER is smtp, so invitations are available (mailAvailable) and every
// one reaches the mailbox through Deps.SMTPSend rather than a server; a test
// that wants the log driver sets MAIL_DRIVER=log with WithEnv.
type harness struct {
	*modtest.Harness
	sources *fakeSources
	mail    *mailbox
}

// smtpFrom is the harness installation's SMTP_FROM, the From and the
// ORGANIZER of every invitation.
const smtpFrom = "calendar@example.invalid"

// newHarness is a calendar installation. Every harness carries the module's
// lock rule out of its test: no call out of the module is made from inside one
// of its transactions (see lockedContractCalls).
func newHarness(t *testing.T, opts ...modtest.Option) *harness {
	t.Helper()
	sources := newFakeSources()
	box := &mailbox{}
	base := []modtest.Option{
		modtest.WithRecorder(recorder),
		modtest.WithModule(calendar.Module()),
		modtest.WithCalendarSources(sources.slot()...),
		modtest.WithSMTPSend(box.send),
		modtest.WithEnv("MAIL_DRIVER", "smtp"),
		modtest.WithEnv("SMTP_HOST", "smtp.example.invalid"),
		modtest.WithEnv("SMTP_FROM", smtpFrom),
	}
	before := lockedContractCalls.count()
	h := &harness{Harness: modtest.New(t, append(base, opts...)...), sources: sources, mail: box}
	sources.wire(h.Deps().Access)
	t.Cleanup(func() {
		if calls := lockedContractCalls.since(before); len(calls) > 0 {
			t.Errorf("a call out of this module was made from inside one of its transactions:\n%s",
				strings.Join(calls, "\n"))
		}
	})
	return h
}

// lockedContractCalls is every call out of the module made from inside one of
// its transactions. Every harness checks it when its test ends; it is one
// recorder for the package because the hook is a package-level one
// (TestMain), and each harness checks only what was recorded while it
// existed.
var lockedContractCalls = &lockedCalls{}

type lockedCalls struct {
	mu    sync.Mutex
	calls []string
}

func (l *lockedCalls) note(ctx context.Context, method string) {
	if log, ok := ctx.Value(contractCallsKey{}).(*contractCallLog); ok {
		log.mu.Lock()
		log.methods = append(log.methods, method)
		log.mu.Unlock()
	}
	if !calendar.InOpenTx(ctx) {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, method+"\n"+string(debug.Stack()))
}

// contractCallsKey carries a test's own log of the calls out of the module
// made on a context it handed in (recordContractCalls): the hook is the
// package's, so a test reads only what its context saw.
type contractCallsKey struct{}

// contractCallLog is the names of the calls out of the module, in order.
type contractCallLog struct {
	mu      sync.Mutex
	methods []string
}

// recordContractCalls is ctx with a log of every call out of the module
// made on it or a context derived from it.
func recordContractCalls(ctx context.Context) (context.Context, *contractCallLog) {
	log := &contractCallLog{}
	return context.WithValue(ctx, contractCallsKey{}, log), log
}

// list is the calls logged so far.
func (l *contractCallLog) list() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.methods)
}

func (l *lockedCalls) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.calls)
}

func (l *lockedCalls) since(n int) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if n >= len(l.calls) {
		return nil
	}
	return slices.Clone(l.calls[n:])
}

// mailbox is the SMTP seam's fake, Staff's: every mail the module hands to
// Deps.SMTPSend, kept in order — unless fail, when a test set it (failWith),
// refuses the mail, which is then not kept. It is safe for concurrent use.
type mailbox struct {
	mu   sync.Mutex
	sent []mail.Outbound
	fail func(out mail.Outbound) error
}

func (m *mailbox) send(_ context.Context, _ config.MailConfig, out mail.Outbound) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail != nil {
		if err := m.fail(out); err != nil {
			return err
		}
	}
	m.sent = append(m.sent, out)
	return nil
}

// failWith makes the mailbox answer every send fn's error (nil delivers).
func (m *mailbox) failWith(fn func(out mail.Outbound) error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.fail = fn
}

// messages is every mail kept so far, in order.
func (m *mailbox) messages() []mail.Outbound {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.sent)
}

// take is every mail kept since the last take, and empties the mailbox.
func (m *mailbox) take() []mail.Outbound {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := m.sent
	m.sent = nil
	return out
}

// The module's paths, so a typo is one compile error rather than a 404 a test
// then explains to itself.
const (
	metaPath         = "/api/v1/calendar/meta"
	itemsPath        = "/api/v1/calendar/items"
	appointmentsPath = "/api/v1/calendar/appointments"
	usersPath        = "/api/v1/calendar/users"
	contactsPath     = "/api/v1/calendar/contacts"
	rsvpReadPath     = "/api/v1/calendar/rsvp/read"
	rsvpPath         = "/api/v1/calendar/rsvp"
)

func resendPath(id, attendeeID int64) string {
	return appointmentPath(id) + "/attendees/" + strconv.FormatInt(attendeeID, 10) + "/resend"
}

func appointmentPath(id int64) string { return appointmentsPath + "/" + strconv.FormatInt(id, 10) }

func occurrencePath(id int64, date string) string {
	return appointmentPath(id) + "/occurrences/" + date
}

func responsePath(id int64) string { return appointmentPath(id) + "/response" }

func colleaguePath(id uuid.UUID, from, to string) string {
	return fmt.Sprintf("/api/v1/calendar/colleagues/%s/items?from=%s&to=%s", id, from, to)
}

func windowPath(from, to string, extra ...string) string {
	p := fmt.Sprintf("%s?from=%s&to=%s", itemsPath, from, to)
	for _, e := range extra {
		p += "&" + e
	}
	return p
}

// signIn is a caller holding calendar:access — which every operation
// requires — and the given permissions on top of it.
func signIn(t *testing.T, h *harness, permissions ...string) (*modtest.Client, uuid.UUID) {
	t.Helper()
	return h.SignInUser(t, append([]string{"calendar:access"}, permissions...)...)
}

// directoryUsers numbers the users directoryUser writes, across the package.
var directoryUsers atomic.Int64

// directoryUser writes one user straight into identity's tables with name as
// their display name, disabled when active is false: an attendee or a
// colleague who never signs in.
func directoryUser(t *testing.T, h *harness, name string, active bool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	email := fmt.Sprintf("calendar-user-%d-%s@example.test", directoryUsers.Add(1), id)
	h.Exec(t, `INSERT INTO identity.users (id, email, normalized_email, display_name, is_disabled, version, created_at, updated_at)
	           VALUES ($1, $2, upper($2), $3, $4, $5, $6, $6)`, id, email, name, !active, uuid.New(), h.Now())
	return id
}

// ifMatch is the If-Match header carrying revision.
func ifMatch(revision int32) modtest.RequestOption {
	return modtest.Header("If-Match", strconv.Itoa(int(revision)))
}

// do sends one request and fails the test unless it answered want, decoding
// the body into out when out is not nil.
func do(t *testing.T, c *modtest.Client, method, path string, body any, want int, out any, opts ...modtest.RequestOption) {
	t.Helper()
	r := c.Do(method, path, body, opts...)
	if r.Status != want {
		t.Fatalf("%s %s: status %d body %s, want %d", method, path, r.Status, r.Body, want)
	}
	if out != nil {
		r.JSON(out)
	}
}

// problem is a refusal's body as the contract's CalendarProblem declares it.
type problem struct {
	Code   string              `json:"code"`
	Title  string              `json:"title"`
	Detail string              `json:"detail"`
	Errors map[string][]string `json:"errors"`
}

// refused fails the test unless r answered status with a problem carrying
// code ("" for a plain field error), and answers the problem.
func refused(t *testing.T, r *modtest.Response, status int, code string) problem {
	t.Helper()
	if r.Status != status {
		t.Fatalf("status %d body %s, want %d %s", r.Status, r.Body, status, code)
	}
	var p problem
	if err := json.Unmarshal(r.Body, &p); err != nil {
		t.Fatalf("decode problem %s: %v", r.Body, err)
	}
	if p.Code != code {
		t.Errorf("code = %q (body %s), want %q", p.Code, r.Body, code)
	}
	return p
}

// fieldRefused fails the test unless r is a 400 with a field error on field.
func fieldRefused(t *testing.T, r *modtest.Response, field string) {
	t.Helper()
	p := refused(t, r, http.StatusBadRequest, "")
	if len(p.Errors[field]) == 0 {
		t.Errorf("no field error on %q: %v", field, p.Errors)
	}
}

// at is an instant written as wall-clock in zone.
func at(t *testing.T, zone, wall string) time.Time {
	t.Helper()
	loc, err := time.LoadLocation(zone)
	if err != nil {
		t.Fatal(err)
	}
	v, err := time.ParseInLocation("2006-01-02T15:04", wall, loc)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// timed is a timed appointment's body in zone, start and end as wall-clock
// there.
func timed(t *testing.T, title, zone, start, end string) map[string]any {
	t.Helper()
	return map[string]any{
		"title": title, "allDay": false, "timeZone": zone,
		"startsAt": at(t, zone, start).UTC().Format(time.RFC3339),
		"endsAt":   at(t, zone, end).UTC().Format(time.RFC3339),
	}
}

// allDay is an all-day appointment's body.
func allDay(title, zone, first, last string) map[string]any {
	return map[string]any{"title": title, "allDay": true, "timeZone": zone, "startDate": first, "endDate": last}
}

// with is body with the given keys set.
func with(body map[string]any, kv ...any) map[string]any {
	out := map[string]any{}
	for k, v := range body {
		out[k] = v
	}
	for i := 0; i+1 < len(kv); i += 2 {
		out[kv[i].(string)] = kv[i+1]
	}
	return out
}

// userAttendees is an AppointmentWrite's attendees naming each user id.
func userAttendees(ids ...string) []map[string]any {
	out := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		out = append(out, map[string]any{"kind": "user", "userId": id})
	}
	return out
}

// create posts body as c and answers the appointment, failing unless 201.
func create(t *testing.T, c *modtest.Client, body map[string]any) gen.Appointment {
	t.Helper()
	var a gen.Appointment
	do(t, c, http.MethodPost, appointmentsPath, body, http.StatusCreated, &a)
	return a
}

// items reads c's calendar for path, failing unless 200.
func items(t *testing.T, c *modtest.Client, path string) gen.CalendarItems {
	t.Helper()
	var out gen.CalendarItems
	do(t, c, http.MethodGet, path, nil, http.StatusOK, &out)
	return out
}

// ofKind is the items of kind, in order.
func ofKind(all gen.CalendarItems, kind string) []gen.CalendarItem {
	var out []gen.CalendarItem
	for _, it := range all.Items {
		if it.Kind == kind {
			out = append(out, it)
		}
	}
	return out
}

// ids is each item's id, in order.
func ids(list []gen.CalendarItem) []string {
	out := make([]string, len(list))
	for i, it := range list {
		out[i] = it.Id
	}
	return out
}

// itemID is the id GET /items gives one occurrence of appointment id.
func itemID(id int64, date string) string { return strconv.FormatInt(id, 10) + ":" + date }

// contains reports whether list holds v.
func contains(list []string, v string) bool { return slices.Contains(list, v) }
