package staff_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
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
	"github.com/vantigo-io/vantigo/server/internal/storage"
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
//
// The object store is a fake in memory (fakeObjectStore), set as
// Deps.ObjectStore, so no test depends on a filesystem and each of its three
// operations can be made to fail on demand.
type harness struct {
	*modtest.Harness
	mail    *mailbox
	objects *fakeObjectStore
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
	objects := newFakeObjectStore()
	base := []modtest.Option{
		modtest.WithRecorder(recorder),
		modtest.WithObjectStore(objects),
		modtest.WithModule(staff.Module()),
		modtest.WithSMTPSend(box.send),
		modtest.WithEnv("MAIL_DRIVER", "smtp"),
		modtest.WithEnv("SMTP_HOST", "smtp.example.invalid"),
		modtest.WithEnv("SMTP_FROM", "hr@example.invalid"),
	}
	before := lockedContractCalls.count()
	h := &harness{Harness: modtest.New(t, append(base, opts...)...), mail: box, objects: objects}
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

// fakeObjectStore is storage.ObjectStore in memory: the documents this module
// uploads go here rather than to a filesystem. Every harness installs one
// (modtest.WithObjectStore). The module's accessors (objects.go) report every
// call to the contract-call hook before it reaches the store, so the
// whole-suite lock rule covers the object store as it covers the directory.
//
// Put, Get and Delete can each be made to fail on demand (failPut and its
// siblings): the only way to drive a store failure — a write that fails
// before the row, a read of an object that is gone, a delete the store
// refuses — deterministically.
type fakeObjectStore struct {
	mu        sync.Mutex
	objects   map[string][]byte
	putErr    error
	getErr    error
	deleteErr error
	deletes   []string
}

var _ storage.ObjectStore = (*fakeObjectStore)(nil)

func newFakeObjectStore() *fakeObjectStore {
	return &fakeObjectStore{objects: map[string][]byte{}}
}

func (s *fakeObjectStore) Put(_ context.Context, key string, r io.Reader, _ string) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.putErr != nil {
		return s.putErr
	}
	s.objects[key] = data
	return nil
}

func (s *fakeObjectStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.getErr != nil {
		return nil, s.getErr
	}
	data, ok := s.objects[key]
	if !ok {
		return nil, fmt.Errorf("staff test store: %q: %w", key, storage.ErrNotExist)
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (s *fakeObjectStore) Exists(_ context.Context, key string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.objects[key]
	return ok, nil
}

func (s *fakeObjectStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deletes = append(s.deletes, key)
	if s.deleteErr != nil {
		return s.deleteErr
	}
	delete(s.objects, key)
	return nil
}

func (s *fakeObjectStore) failPut(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.putErr = err
}

func (s *fakeObjectStore) failGet(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getErr = err
}

func (s *fakeObjectStore) failDelete(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleteErr = err
}

// keys is every object the store holds, sorted.
func (s *fakeObjectStore) keys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.objects))
	for k := range s.objects {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// has reports whether the store holds key.
func (s *fakeObjectStore) has(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.objects[key]
	return ok
}

// documentCategoryID is the id of the category keyed key.
func documentCategoryID(t *testing.T, h *harness, key string) int64 {
	t.Helper()
	return modtest.One[int64](t, h.Harness, `SELECT id FROM staff.document_categories WHERE key = $1`, key)
}

// seedDocument files one document directly — its row in the category keyed
// category and its object in the fake store — for a test whose subject is who
// reads or changes it rather than the upload, and answers its id.
func seedDocument(t *testing.T, h *harness, personID int64, category string) int64 {
	t.Helper()
	key := fmt.Sprintf("people/%d/%s", personID, uuid.New())
	data := testPDF(16)
	if err := h.objects.Put(context.Background(), key, bytes.NewReader(data), "application/pdf"); err != nil {
		t.Fatalf("seed the object: %v", err)
	}
	return modtest.One[int64](t, h.Harness, `
		INSERT INTO staff.documents (person_id, category_id, object_key, file_name, content_type, size_bytes,
			uploaded_by_user_id, created_at, updated_at)
		SELECT $1, id, $2, $3, 'application/pdf', $4, $5, $6, $6 FROM staff.document_categories WHERE key = $7
		RETURNING id`, personID, key, category+".pdf", len(data), uuid.New(), h.Now(), category)
}

// The document bytes every test uploads. No binary fixture is committed: the
// images come from the standard library's own encoders, and the PDF and the
// HEIC file are the magic bytes each format is sniffed by (Expenses' own).
func testPNG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatalf("encode PNG: %v", err)
	}
	return buf.Bytes()
}

func testJPEG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 8, 8)), &jpeg.Options{Quality: 80}); err != nil {
		t.Fatalf("encode JPEG: %v", err)
	}
	return buf.Bytes()
}

// testPDF is the smallest thing http.DetectContentType calls a PDF, padded.
func testPDF(padding int) []byte {
	return append([]byte("%PDF-1.7\n1 0 obj\n<< /Type /Catalog >>\nendobj\n"), bytes.Repeat([]byte("x"), padding)...)
}

// testHEIC is an ISO base media file whose major brand is "heic".
func testHEIC() []byte {
	box := []byte{0, 0, 0, 24}
	box = append(box, []byte("ftypheic")...)
	box = append(box, 0, 0, 0, 0)
	box = append(box, []byte("mif1")...)
	return append(box, []byte("meta and the picture itself")...)
}

// upload is one document upload's form: the file part and the fields beside
// it, each sent only when set.
type upload struct {
	fileName, contentType string
	data                  []byte
	categoryID            int64
	validTo, signedAt     string
	noFile                bool
	// fieldsAfterFile sends the text fields after the file part rather than
	// before it.
	fieldsAfterFile bool
}

// form is u as multipart/form-data: its content type and its body.
func (u upload) form(t *testing.T) (string, []byte) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	field := func(name, value string) {
		if err := w.WriteField(name, value); err != nil {
			t.Fatalf("write the %q field: %v", name, err)
		}
	}
	fields := func() {
		if u.categoryID != 0 {
			field("categoryId", strconv.FormatInt(u.categoryID, 10))
		}
		if u.validTo != "" {
			field("validTo", u.validTo)
		}
		if u.signedAt != "" {
			field("signedAt", u.signedAt)
		}
	}
	if !u.fieldsAfterFile {
		fields()
	}
	if !u.noFile {
		header := textproto.MIMEHeader{}
		header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, u.fileName))
		if u.contentType != "" {
			header.Set("Content-Type", u.contentType)
		}
		part, err := w.CreatePart(header)
		if err != nil {
			t.Fatalf("create the file part: %v", err)
		}
		if _, err := part.Write(u.data); err != nil {
			t.Fatalf("write the file part: %v", err)
		}
	}
	if u.fieldsAfterFile {
		fields()
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close the multipart writer: %v", err)
	}
	return w.FormDataContentType(), buf.Bytes()
}

// pdfUpload is a small PDF filed under categoryID.
func pdfUpload(categoryID int64) upload {
	return upload{fileName: "avtale.pdf", contentType: "application/pdf", data: testPDF(32), categoryID: categoryID}
}

// postDocument sends one upload and answers the whole response.
func postDocument(t *testing.T, c *modtest.Client, personID int64, u upload) *modtest.Response {
	t.Helper()
	ct, body := u.form(t)
	return c.Do(http.MethodPost, documentsPath(personID), nil, modtest.RawBody(ct, body))
}

// uploadDocument files one document and fails the test unless it was created.
func uploadDocument(t *testing.T, c *modtest.Client, personID int64, u upload) gen.StaffDocument {
	t.Helper()
	r := postDocument(t, c, personID, u)
	if r.Status != http.StatusCreated {
		t.Fatalf("upload %s for person %d: status %d body %s, want 201", u.fileName, personID, r.Status, r.Body)
	}
	var out gen.StaffDocument
	r.JSON(&out)
	return out
}

// The documents' paths.
const (
	documentsBase          = "/api/v1/staff/documents"
	documentCategoriesPath = "/api/v1/staff/document-categories"
	meDocumentsPath        = "/api/v1/staff/me/documents"
)

func documentsPath(personID int64) string { return rowPath(peoplePath, personID) + "/documents" }
func documentPath(id int64) string        { return rowPath(documentsBase, id) }
