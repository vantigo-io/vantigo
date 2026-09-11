package contracttest

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

// spec is a tiny contract exercising the shapes Validate cares about: a JSON
// request/response pair, a query-less GET, and a binary response.
const spec = `
openapi: 3.0.3
info: {title: t, version: "1"}
servers: [{url: /}]
paths:
  /things:
    post:
      operationId: postThings
      x-vantigo-access: session
      requestBody:
        required: true
        content:
          application/json:
            schema: {type: object, required: [name], properties: {name: {type: string}}}
      responses:
        "201":
          description: created
          content:
            application/json:
              schema: {type: object, required: [id], properties: {id: {type: integer}}}
    get:
      operationId: getThings
      x-vantigo-access: session
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema: {type: array, items: {type: object}}
        "401":
          description: unauthenticated
          content:
            application/json:
              schema: {type: object, required: [code], properties: {code: {type: string}}}
  /blob:
    get:
      operationId: getBlob
      x-vantigo-access: session
      responses:
        "200":
          description: binary
          content:
            application/octet-stream:
              schema: {type: string, format: binary}
`

func loadDoc(t testing.TB) *openapi3.T {
	t.Helper()
	doc, err := openapi3.NewLoader().LoadFromData([]byte(spec))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

// fakeTB embeds testing.TB so it satisfies the interface (including its
// unexported method) while overriding just Errorf and Helper to capture
// calls, so a test can assert on a failure the transport reports without
// failing itself.
type fakeTB struct {
	testing.TB
	errors []string
}

func (f *fakeTB) Helper() {}

func (f *fakeTB) Errorf(format string, args ...any) {
	f.errors = append(f.errors, fmt.Sprintf(format, args...))
}

// client wraps the server's transport in a recorder. httptest hands out one
// client per server, so it gets a client of its own rather than that shared
// one: tests calling this from several goroutines would otherwise race on
// the shared client's Transport field, and each call would wrap the previous
// recorder.
func client(t testing.TB, srv *httptest.Server, rec *Recorder) *http.Client {
	return &http.Client{Transport: rec.Transport(t, srv.Client().Transport)}
}

// TestTransportValidatesAConformingExchange proves the happy path: a request
// and response that match the contract pass validation and the operation
// counts as exercised.
func TestTransportValidatesAConformingExchange(t *testing.T) {
	doc := loadDoc(t)
	rec := New(doc)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	defer srv.Close()

	resp, err := client(t, srv, rec).Post(srv.URL+"/things", "application/json", strings.NewReader(`{"name":"a"}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusCreated)
	}
	if containsString(rec.Missing(), "postThings") {
		t.Errorf("Missing() = %v, still lists postThings after a conforming exchange", rec.Missing())
	}
}

// TestTransportFailsAnOffContractResponse proves a response the contract
// does not permit fails the test — via a fake testing.TB, since the real t
// must stay green.
func TestTransportFailsAnOffContractResponse(t *testing.T) {
	doc := loadDoc(t)
	rec := New(doc)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"not":"an array"}`)) // getThings documents an array
	}))
	defer srv.Close()

	fake := &fakeTB{}
	resp, err := client(fake, srv, rec).Get(srv.URL + "/things")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	if len(fake.errors) != 1 {
		t.Fatalf("Errorf calls = %d, want 1: %v", len(fake.errors), fake.errors)
	}
	if !strings.Contains(fake.errors[0], "getThings") {
		t.Errorf("error = %q, want it to name getThings", fake.errors[0])
	}
}

// TestSkipHeaderBypassesValidationAndIsStripped proves X-Contract-Skip both
// opts an exchange out of validation and never reaches the server, and that
// the caller's own request object is left untouched (Transport must clone
// before stripping the header).
func TestSkipHeaderBypassesValidationAndIsStripped(t *testing.T) {
	doc := loadDoc(t)
	rec := New(doc)

	seen := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Header.Get(SkipHeader)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"not":"an array"}`)) // would fail validation if it ran
	}))
	defer srv.Close()

	fake := &fakeTB{}
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/things", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(SkipHeader, "deliberate off-contract probe")

	resp, err := client(fake, srv, rec).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	if got := <-seen; got != "" {
		t.Errorf("server saw %s = %q, want it stripped before the request reached base", SkipHeader, got)
	}
	if len(fake.errors) != 0 {
		t.Errorf("Errorf calls = %v, want none: the skip header should bypass validation", fake.errors)
	}
	if got := req.Header.Get(SkipHeader); got == "" {
		t.Errorf("caller's request header was mutated; Transport must clone before stripping")
	}
}

// TestMissingListsUnexercisedOperations proves Missing starts by listing
// every operation and drops one once a conforming exchange exercises it.
func TestMissingListsUnexercisedOperations(t *testing.T) {
	doc := loadDoc(t)
	rec := New(doc)

	if got, want := rec.Missing(), []string{"getBlob", "getThings", "postThings"}; !equalStrings(got, want) {
		t.Fatalf("Missing() = %v, want %v", got, want)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	resp, err := client(t, srv, rec).Get(srv.URL + "/things")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	if got, want := rec.Missing(), []string{"getBlob", "postThings"}; !equalStrings(got, want) {
		t.Fatalf("Missing() after exercising getThings = %v, want %v", got, want)
	}
}

// TestOnlySuccessfulExchangesCountAsExercised proves the coverage gate needs
// a success: a documented 401 validates (a real t is used, so any Errorf
// would fail this test) yet leaves the operation missing, and only a
// validated 200 marks it exercised.
func TestOnlySuccessfulExchangesCountAsExercised(t *testing.T) {
	doc := loadDoc(t)
	rec := New(doc)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"code":"unauthenticated"}`))
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	c := client(t, srv, rec)

	resp, err := c.Get(srv.URL + "/things")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
	if !containsString(rec.Missing(), "getThings") {
		t.Fatalf("Missing() = %v, want getThings still listed after only a validated 401", rec.Missing())
	}

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/things", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer x")
	resp, err = c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if containsString(rec.Missing(), "getThings") {
		t.Errorf("Missing() = %v, still lists getThings after a validated 200", rec.Missing())
	}
}

// TestRequestBodyStillArrivesAfterCapture proves reading the request body to
// record it does not consume it: the server must still see it whole.
func TestRequestBodyStillArrivesAfterCapture(t *testing.T) {
	doc := loadDoc(t)
	rec := New(doc)

	received := make(chan []byte, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("server: read body: %v", err)
		}
		received <- b
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	defer srv.Close()

	const body = `{"name":"a"}`
	resp, err := client(t, srv, rec).Post(srv.URL+"/things", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	if got := string(<-received); got != body {
		t.Errorf("server received %q, want %q", got, body)
	}
}

// TestResponseBodyStillReadableByCaller proves reading the response body to
// record it does not consume it: the caller must still be able to read it.
func TestResponseBodyStillReadableByCaller(t *testing.T) {
	doc := loadDoc(t)
	rec := New(doc)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	resp, err := client(t, srv, rec).Get(srv.URL + "/things")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "[]" {
		t.Errorf("resp.Body = %q, want %q", got, "[]")
	}
}

// TestBinaryResponseRecordedWithNilBody proves an octet-stream response
// still validates (a real t is used, so any Errorf would fail this test)
// even though its body is garbage that would fail JSON decoding were it
// mistaken for text — the content type alone is checked — and that the
// caller still receives the exact bytes.
func TestBinaryResponseRecordedWithNilBody(t *testing.T) {
	doc := loadDoc(t)
	rec := New(doc)
	garbage := []byte{0xff, 0xfe, 0x00, 0x01, 'n', 'o', 't', ' ', 'j', 's', 'o', 'n'}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(garbage)
	}))
	defer srv.Close()

	resp, err := client(t, srv, rec).Get(srv.URL + "/blob")
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, garbage) {
		t.Errorf("caller read %v, want the original bytes back", got)
	}
}

// TestOversizedRequestBodyRecordedAsNilButContentTypeStillValidated proves
// the 256 KiB cap applies: the body sent is neither valid JSON nor small
// enough to record, so if the cap did not apply it would be validated
// against postThings' schema and fail. A real t is used, so the exchange
// passing proves the oversized body was excluded rather than checked.
func TestOversizedRequestBodyRecordedAsNilButContentTypeStillValidated(t *testing.T) {
	doc := loadDoc(t)
	rec := New(doc)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	defer srv.Close()

	huge := strings.Repeat("a", maxBodyBytes+1024) // not JSON, and over the cap
	resp, err := client(t, srv, rec).Post(srv.URL+"/things", "application/json", strings.NewReader(huge))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
}

// TestRecorderIsSafeForConcurrentUse guards the exercised set: an
// unsynchronized concurrent map write panics the whole process regardless
// of the race detector (unavailable on this host — no C compiler), so this
// catches a missing lock even without -race. Run with -count=10 for the
// best chance of tripping the race window.
func TestRecorderIsSafeForConcurrentUse(t *testing.T) {
	doc := loadDoc(t)
	rec := New(doc)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := client(t, srv, rec).Get(srv.URL + "/things")
			if err != nil {
				t.Error(err)
				return
			}
			_ = resp.Body.Close()
			rec.Missing()
		}()
	}
	wg.Wait()
}

// TestRecordableMirrorsTheDotNetRecorderRule unit-tests the rule mirrored
// from packages/contract-recording/ContractRecording.cs:95-125 (IsText plus
// the 256 KiB cap plus the empty-body case): a body is kept only when its
// content type is text, it is non-empty, and it is at most maxBodyBytes.
func TestRecordableMirrorsTheDotNetRecorderRule(t *testing.T) {
	atCap := strings.Repeat("a", maxBodyBytes)
	overCap := strings.Repeat("a", maxBodyBytes+1)
	cases := []struct {
		name        string
		contentType string
		body        string
		want        bool // whether recordable keeps the body
	}{
		{"json", "application/json", `{"a":1}`, true},
		{"json with charset", "application/json; charset=utf-8", `{}`, true},
		{"json is case-insensitive", "APPLICATION/JSON", `{}`, true},
		{"scim+json counts as json", "application/scim+json", `{}`, true},
		{"text/plain", "text/plain", "hello", true},
		{"text/plain with charset", "text/plain; charset=utf-8", "hello", true},
		{"form-urlencoded", "application/x-www-form-urlencoded", "a=1", true},
		{"octet-stream is not text", "application/octet-stream", "hello", false},
		{"pdf is not text", "application/pdf", "%PDF-1.4", false},
		{"empty content type", "", `{}`, false},
		{"empty body", "application/json", "", false},
		{"exactly the cap", "application/json", atCap, true},
		{"one byte over the cap", "application/json", overCap, false},
	}
	for _, c := range cases {
		got := recordable(c.contentType, []byte(c.body))
		if (got != nil) != c.want {
			t.Errorf("%s: recordable(%q, ...) kept = %v, want %v", c.name, c.contentType, got != nil, c.want)
		}
	}
}

// TestPendingReportExcludesPendingFromMissing proves Missing's report, once
// filtered by pendingReport, drops a pending operation even though it was
// never exercised.
func TestPendingReportExcludesPendingFromMissing(t *testing.T) {
	doc := loadDoc(t)
	rec := New(doc)

	missing, stale := pendingReport(rec, []string{"getBlob"})
	if containsString(missing, "getBlob") {
		t.Errorf("missing = %v, must not include the pending operation getBlob", missing)
	}
	if len(stale) != 0 {
		t.Errorf("stale = %v, want none: getBlob has not been exercised", stale)
	}
	if !containsString(missing, "getThings") || !containsString(missing, "postThings") {
		t.Errorf("missing = %v, want the non-pending operations still listed", missing)
	}
}

// TestPendingReportFlagsAnExercisedPendingOperationAsStale proves the
// allow-list can only shrink honestly: a pending operation a
// contract-conforming exchange did exercise is reported, not silently kept.
func TestPendingReportFlagsAnExercisedPendingOperationAsStale(t *testing.T) {
	doc := loadDoc(t)
	rec := New(doc)
	rec.markExercised("getBlob")

	missing, stale := pendingReport(rec, []string{"getBlob"})
	if containsString(missing, "getBlob") {
		t.Errorf("missing = %v, must not include the pending operation getBlob", missing)
	}
	want := "remove getBlob from pendingOperations"
	if !containsString(stale, want) {
		t.Errorf("stale = %v, want it to include %q", stale, want)
	}
}

// TestPendingReportFlagsAnUndocumentedPendingName proves a typo in the
// allow-list — a name that matches no operation in the contract — is
// reported rather than silently ignored.
func TestPendingReportFlagsAnUndocumentedPendingName(t *testing.T) {
	doc := loadDoc(t)
	rec := New(doc)

	_, stale := pendingReport(rec, []string{"notAnOperation"})
	if len(stale) != 1 || !strings.Contains(stale[0], "notAnOperation") {
		t.Errorf("stale = %v, want one entry naming notAnOperation", stale)
	}
}

// TestCoverageExit table-tests the exit-code decision in isolation from
// m.Run and TestMain.
func TestCoverageExit(t *testing.T) {
	cases := []struct {
		name       string
		code       int
		filtered   bool
		missing    []string
		stale      []string
		want       int
		wantReport bool // whether coverageExit must have written a report to w
	}{
		{"a failed run passes through", 1, false, nil, nil, 1, false},
		{"a failed run passes through even when filtered", 2, true, nil, nil, 2, false},
		{"a filtered run skips the check", 0, true, []string{"getThings"}, nil, 0, false},
		{"everything covered", 0, false, nil, nil, 0, false},
		{"missing operations fail the run", 0, false, []string{"getThings"}, nil, 1, true},
		{"stale pending entries fail the run", 0, false, nil, []string{"remove getThings from pendingOperations"}, 1, true},
	}
	for _, c := range cases {
		var buf bytes.Buffer
		got := coverageExit(c.code, c.filtered, c.missing, c.stale, &buf)
		if got != c.want {
			t.Errorf("%s: coverageExit(...) = %d, want %d", c.name, got, c.want)
		}
		if hasReport := buf.Len() > 0; hasReport != c.wantReport {
			t.Errorf("%s: report written = %v, want %v (output: %q)", c.name, hasReport, c.wantReport, buf.String())
		}
	}
}

// TestHasTestFilterRecognizesListRunAndSkip proves go test -list . no longer
// fails RequireCoverage's coverage gate: -test.list, like -test.run and
// -test.skip, must be treated as a filter, since a -list run (and `go test
// -list .` in particular) executes no tests at all, so every operation would
// otherwise be reported missing. A -run flag set to the empty pattern
// narrows nothing, and is not a filter. Every flag is cleared before each
// case, so the result does not depend on how this test itself was run.
func TestHasTestFilterRecognizesListRunAndSkip(t *testing.T) {
	names := []string{"test.list", "test.run", "test.skip"}
	for _, name := range names {
		f := flag.Lookup(name)
		if f == nil {
			t.Fatalf("flag %q is not registered", name)
		}
		original := f.Value.String()
		t.Cleanup(func() { _ = f.Value.Set(original) })
	}
	clearAll := func() {
		for _, name := range names {
			if err := flag.Lookup(name).Value.Set(""); err != nil {
				t.Fatalf("reset %s: %v", name, err)
			}
		}
	}

	clearAll()
	if hasTestFilter() {
		t.Errorf("hasTestFilter() = true with every flag empty, want false")
	}
	if err := flag.Lookup("test.run").Value.Set(""); err != nil {
		t.Fatalf("set an empty -run: %v", err)
	}
	if hasTestFilter() {
		t.Errorf("hasTestFilter() = true with an explicitly empty -run, want false")
	}
	for _, name := range names {
		clearAll()
		if err := flag.Lookup(name).Value.Set("Something"); err != nil {
			t.Fatalf("set %s: %v", name, err)
		}
		if !hasTestFilter() {
			t.Errorf("hasTestFilter() = false with -%s set, want true", name)
		}
	}
}

// TestAnUnmatchedRouteFailsAndNeverCounts proves an exchange no contract
// operation matches fails the test — through a fake testing.TB — and marks
// nothing exercised, although the server answered 200.
func TestAnUnmatchedRouteFailsAndNeverCounts(t *testing.T) {
	doc := loadDoc(t)
	rec := New(doc)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	fake := &fakeTB{}
	resp, err := client(fake, srv, rec).Get(srv.URL + "/nowhere")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	if len(fake.errors) != 1 {
		t.Errorf("Errorf calls = %v, want exactly one for the unmatched route", fake.errors)
	}
	if got, want := rec.Missing(), []string{"getBlob", "getThings", "postThings"}; !equalStrings(got, want) {
		t.Errorf("Missing() = %v, want %v: an unmatched exchange must count for nothing", got, want)
	}
}

// opaqueBody is a request body http.NewRequest cannot snapshot, so it sets
// no GetBody of its own: only the transport's capture can supply one.
type opaqueBody struct{ io.Reader }

// TestA307RedirectReplaysTheCapturedBody proves the GetBody the transport
// installs when it captures a request body: the client follows a 307 by
// replaying the body through GetBody, which the caller's opaque body never
// had, and the redirected request arrives whole and validates.
func TestA307RedirectReplaysTheCapturedBody(t *testing.T) {
	doc := loadDoc(t)
	rec := New(doc)

	received := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/old-things" {
			http.Redirect(w, r, "/things", http.StatusTemporaryRedirect)
			return
		}
		b, _ := io.ReadAll(r.Body)
		received <- string(b)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	defer srv.Close()

	const body = `{"name":"a"}`
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/old-things", opaqueBody{strings.NewReader(body)})
	if err != nil {
		t.Fatal(err)
	}
	if req.GetBody != nil {
		t.Fatal("http.NewRequest set GetBody for the opaque body; the test would prove nothing")
	}
	req.Header.Set("Content-Type", "application/json")

	fake := &fakeTB{} // the 307 itself is undocumented and fails validation
	resp, err := client(fake, srv, rec).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want the redirect followed to 201", resp.StatusCode)
	}
	if got := <-received; got != body {
		t.Errorf("the redirected request carried %q, want %q", got, body)
	}
	if len(fake.errors) != 1 {
		t.Errorf("Errorf calls = %v, want one, for the undocumented 307 only", fake.errors)
	}
	if containsString(rec.Missing(), "postThings") {
		t.Errorf("Missing() = %v, still lists postThings after the replayed request validated", rec.Missing())
	}
}

// failingBody is a response body whose every Read fails, recording Close.
type failingBody struct{ closed *bool }

func (failingBody) Read([]byte) (int, error) { return 0, errors.New("connection reset") }
func (b failingBody) Close() error           { *b.closed = true; return nil }

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestAFailedResponseReadClosesTheBody proves RoundTrip closes the body it
// could not read before returning the error: it returns no response, so
// the caller never could.
func TestAFailedResponseReadClosesTheBody(t *testing.T) {
	closed := false
	base := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: failingBody{closed: &closed}}, nil
	})
	req, err := http.NewRequest(http.MethodGet, "http://example.test/things", nil)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := New(loadDoc(t)).Transport(&fakeTB{}, base).RoundTrip(req)
	if err == nil || resp != nil {
		t.Fatalf("RoundTrip = %v, %v; want no response and the read error", resp, err)
	}
	if !closed {
		t.Error("the unreadable response body was never closed")
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
