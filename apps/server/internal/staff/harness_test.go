package staff_test

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

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/mail"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/staff"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

// harness is one staff installation for one test: internal/modtest's shared
// harness composing this module beside identity, every exchange validated
// against staff.yaml through the package recorder, plus the mailbox its SMTP
// seam delivers to.
//
// The user directory is identity's own: modtest always composes identity,
// which provides contracts.UserDirectory, and Compose would overwrite any
// Deps.Users a harness preset — so a test shapes what the directory answers
// through identity's tables, as the customers and expenses harnesses do. The
// contract-call tests in contractscalls_internal_test.go hold a fake
// directory instead, because their subject is where a call is made from.
//
// Deps.Config is the harness's own: MAIL_DRIVER is smtp, so the module's mail
// is available (mailAvailable) and every send reaches the mailbox through
// Deps.SMTPSend rather than a server.
type harness struct {
	*modtest.Harness
	mail *mailbox
}

// newHarness is a staff installation. Every harness carries the module's lock
// rule out of its test: no call out of the module's own database — the user
// directory, the mail — is made from inside one of its transactions, a
// locked one (withLockedTx, withPersonLock) or a read-only snapshot
// (withReadTx), each of which holds a pool connection the call's own read
// could starve for (see lockedContractCalls).
func newHarness(t *testing.T, opts ...modtest.Option) *harness {
	t.Helper()
	box := &mailbox{}
	base := []modtest.Option{
		modtest.WithRecorder(recorder),
		modtest.WithModule(staff.Module()),
		modtest.WithSMTPSend(box.send),
		modtest.WithEnv("MAIL_DRIVER", "smtp"),
		modtest.WithEnv("SMTP_HOST", "smtp.example.invalid"),
		modtest.WithEnv("SMTP_FROM", "hr@example.invalid"),
	}
	before := lockedContractCalls.count()
	h := &harness{Harness: modtest.New(t, append(base, opts...)...), mail: box}
	t.Cleanup(func() {
		if calls := lockedContractCalls.since(before); len(calls) > 0 {
			t.Errorf("a call outside this module's own database was made from inside one of its transactions:\n%s",
				strings.Join(calls, "\n"))
		}
	})
	return h
}

// mailbox is the SMTP seam's fake: every mail the module hands to
// Deps.SMTPSend, kept in order. It is safe for concurrent use.
type mailbox struct {
	mu   sync.Mutex
	sent []mail.Outbound
}

func (m *mailbox) send(_ context.Context, _ config.MailConfig, out mail.Outbound) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, out)
	return nil
}

func (m *mailbox) messages() []mail.Outbound {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.sent)
}

// lockedContractCalls is every call out of the module made from inside one of
// its transactions (staff.InOpenTx: a locked one, or a read-only snapshot).
// The user directory reads through the same connection pool as the module, so
// a transaction that waits for it while holding a connection — and locks —
// can starve the pool under load; a mail server under a row lock is the same
// hazard by another route. Every harness checks it when its test ends. It is
// one recorder for the package because the hook is a package-level one
// (TestMain); each harness checks only what was recorded while it existed,
// and the stack beside each call names the path that made it.
var lockedContractCalls = &lockedCalls{}

type lockedCalls struct {
	mu    sync.Mutex
	calls []string
}

func (l *lockedCalls) note(ctx context.Context, method string) {
	if !staff.InOpenTx(ctx) {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, method+"\n"+string(debug.Stack()))
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

// forget drops what was recorded after the first n calls: the recorder's own
// test, which records a forbidden call on purpose, leaves nothing for a later
// harness to fail on.
func (l *lockedCalls) forget(n int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = l.calls[:min(n, len(l.calls))]
}

// contractCalls is every call out of the module, each with the caller whose
// request made it and whether it was made inside one of the module's
// transactions — the whole record lockedContractCalls keeps only the
// forbidden part of. Tests run in parallel against one package-level hook,
// and a caller's id is what tells one test's calls from another's.
var contractCalls = &allCalls{}

// contractCall is one call out of the module.
type contractCall struct {
	method string
	userID uuid.UUID
	inTx   bool
}

type allCalls struct {
	mu    sync.Mutex
	calls []contractCall
}

func (a *allCalls) note(ctx context.Context, method string) {
	p, _ := contracts.PrincipalFrom(ctx)
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls = append(a.calls, contractCall{method: method, userID: p.UserID, inTx: staff.InOpenTx(ctx)})
}

// by is every call a request of userID's made, in order.
func (a *allCalls) by(userID uuid.UUID) []contractCall {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []contractCall
	for _, c := range a.calls {
		if c.userID == userID {
			out = append(out, c)
		}
	}
	return out
}

// noteContractCall is the hook TestMain installs: every call is recorded, and
// one the lock rule forbids is also recorded as a failure.
func noteContractCall(ctx context.Context, method string) {
	lockedContractCalls.note(ctx, method)
	contractCalls.note(ctx, method)
}

// The module's paths, so a typo is one compile error rather than a 404 a test
// then explains to itself.
const (
	metaPath          = "/api/v1/staff/meta"
	settingsPath      = "/api/v1/staff/settings"
	legalEntitiesPath = "/api/v1/staff/legal-entities"
	businessUnitsPath = "/api/v1/staff/business-units"
	departmentsPath   = "/api/v1/staff/departments"
	locationsPath     = "/api/v1/staff/locations"
)

// rowPath is a structure row's own path, and archivePath its archive.
func rowPath(base string, id int64) string     { return base + "/" + strconv.FormatInt(id, 10) }
func archivePath(base string, id int64) string { return rowPath(base, id) + "/archive" }

// signIn is a caller holding staff:access — which every operation requires —
// and the given permissions on top of it.
func signIn(t *testing.T, h *harness, permissions ...string) (*modtest.Client, uuid.UUID) {
	t.Helper()
	return h.SignInUser(t, append([]string{"staff:access"}, permissions...)...)
}

// ifMatch is the If-Match header carrying revision, as a client sends back
// what it read.
func ifMatch(revision int32) modtest.RequestOption {
	return modtest.Header("If-Match", strconv.Itoa(int(revision)))
}

// people numbers every person seedPerson writes, across the package, so two
// parallel tests never seed the same employee number into one installation.
var people atomic.Int64

// seedPerson writes one person directly. The people endpoints are delivery
// A1's Task 3a; until then a department's manager and a self-service link need
// a row to name, and the row is all they need.
func seedPerson(t *testing.T, h *harness, given, family string) int64 {
	t.Helper()
	number := fmt.Sprintf("T%d", people.Add(1))
	return modtest.One[int64](t, h.Harness, `
		INSERT INTO staff.people (employee_number, given_name, family_name, display_name, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $5) RETURNING id`, number, given, family, given+" "+family, h.Now())
}

// link writes the self-service link between a person and a user directly, in
// the state HR's PUT /people/{id}/user writes: the identity provider, the
// user's id as text, linked and not managed. It is the seed for the matrix
// tests, whose subject is who reads what; a test whose subject is the link
// itself goes through the endpoint (linkThroughAPI).
func link(t *testing.T, h *harness, personID int64, userID uuid.UUID) {
	t.Helper()
	h.Exec(t, `
		INSERT INTO staff.identity_links (person_id, provider, external_id, state, managed, created_at, updated_at)
		VALUES ($1, 'identity', $2, 'linked', false, $3, $3)`, personID, userID.String(), h.Now())
}

// today is the installation's calendar day at the harness clock, in the
// business time zone the settings row holds — the day the module must judge
// "today" by.
func today(t *testing.T, h *harness) string {
	t.Helper()
	zone := modtest.One[string](t, h.Harness, `SELECT time_zone FROM staff.settings`)
	loc, err := time.LoadLocation(zone)
	if err != nil {
		t.Fatalf("load %q: %v", zone, err)
	}
	return h.Now().In(loc).Format(time.DateOnly)
}

// setClock moves the harness clock forward to at. The harness clock only ever
// moves forward, so a test asking for an earlier instant has a bug of its own.
func setClock(t *testing.T, h *harness, at time.Time) {
	t.Helper()
	d := at.Sub(h.Now())
	if d < 0 {
		t.Fatalf("setClock(%s): the clock is already at %s", at, h.Now())
	}
	h.Advance(d)
}

// getMeta reads the module metadata and fails the test unless it answered 200.
func getMeta(t *testing.T, c *modtest.Client) gen.StaffMeta {
	t.Helper()
	r := c.Do(http.MethodGet, metaPath, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("get meta: status %d body %s, want 200", r.Status, r.Body)
	}
	var meta gen.StaffMeta
	r.JSON(&meta)
	return meta
}

// raw decodes a response body as a bare JSON object, for a test whose subject
// is whether a key is there at all — a nil pointer cannot tell "absent" from
// "null".
func raw(t *testing.T, r *modtest.Response) map[string]any {
	t.Helper()
	var out map[string]any
	r.JSON(&out)
	return out
}

// problem is a refusal's body as the contract's StaffProblem declares it.
type problem struct {
	Code   string              `json:"code"`
	Title  string              `json:"title"`
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

// do sends one request and fails the test unless it answered want, decoding
// the body into out when out is not nil.
func do(t *testing.T, c *modtest.Client, method, path string, body any, want int, out any, opts ...modtest.RequestOption) {
	t.Helper()
	r := c.Do(method, path, body, opts...)
	if r.Status != want {
		t.Fatalf("%s %s %v: status %d body %s, want %d", method, path, body, r.Status, r.Body, want)
	}
	if out != nil {
		r.JSON(out)
	}
}
