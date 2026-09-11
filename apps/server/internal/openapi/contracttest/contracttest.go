// Package contracttest wraps an http.RoundTripper so every request/response
// pair a test sends through it is validated against an OpenAPI contract with
// openapi.Validate, and tracks which operations a test run exercised
// successfully. It is the test client every identity integration test runs
// through.
package contracttest

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/vantigo-io/vantigo/server/internal/openapi"
)

// maxBodyBytes is the recording cap the .NET recorder applies
// (packages/contract-recording/ContractRecording.cs:22), mirrored here so a
// live exchange is capped the same way the recorded corpus was.
const maxBodyBytes = 256 * 1024

// SkipHeader on a request bypasses contract validation for that exchange.
// Its value is the reason a test opts out (a deliberate off-contract probe);
// the header is stripped from a clone before the request reaches base, so
// the server never sees it and the caller's request is never mutated.
const SkipHeader = "X-Contract-Skip"

// Recorder tracks, for one OpenAPI document, which operations a test run has
// exercised: an operation counts once a contract-conforming exchange for it
// answered with a status below 400. A conforming error response (the access
// layer's 401, a documented 400) is validated like any other but does not
// count, so an operation cannot be covered without ever having succeeded. It
// is meant to be package-level and shared by every test in the package
// (parallel tests included), so its exercised set is guarded by a mutex.
type Recorder struct {
	doc *openapi3.T

	mu        sync.Mutex
	exercised map[string]bool
}

// New returns a Recorder for doc.
func New(doc *openapi3.T) *Recorder {
	return &Recorder{doc: doc, exercised: map[string]bool{}}
}

// Transport wraps base so every exchange that passes through it is validated
// against the Recorder's contract. A validation failure calls t.Errorf,
// naming the operation and the reason; the exchange still completes, body
// and all, so the test can also assert on the response. A validated exchange
// with a status below 400 marks its operation exercised. A request carrying
// SkipHeader is not validated at all, and never counts.
func (r *Recorder) Transport(t testing.TB, base http.RoundTripper) http.RoundTripper {
	return &transport{t: t, base: base, rec: r}
}

type transport struct {
	t    testing.TB
	base http.RoundTripper
	rec  *Recorder
}

func (rt *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.t.Helper()

	if req.Header.Get(SkipHeader) != "" {
		clone := req.Clone(req.Context())
		clone.Header.Del(SkipHeader)
		return rt.base.RoundTrip(clone)
	}

	reqContentType, reqBody, err := captureRequestBody(req)
	if err != nil {
		return nil, err
	}

	resp, err := rt.base.RoundTrip(req)
	if err != nil {
		return resp, err
	}

	respContentType, respBody, err := captureResponseBody(resp)
	if err != nil {
		return nil, err
	}

	query := ""
	if req.URL.RawQuery != "" {
		query = "?" + req.URL.RawQuery
	}

	ex := openapi.Exchange{
		Method:              req.Method,
		Path:                req.URL.Path,
		Query:               query,
		RequestContentType:  reqContentType,
		RequestBody:         reqBody,
		Status:              resp.StatusCode,
		ResponseContentType: respContentType,
		ResponseBody:        respBody,
	}

	id, verr := openapi.Validate(req.Context(), rt.rec.doc, ex)
	if verr != nil {
		rt.t.Errorf("contract: operation %s: %v", id, verr)
		return resp, nil
	}
	// Only a success counts as coverage. A documented error (the access
	// layer's 401, a validation 400) still validates, but it proves nothing
	// about the operation's success path, so it must not let an operation
	// leave the pending list without ever having worked.
	if resp.StatusCode < 400 {
		rt.rec.markExercised(id)
	}
	return resp, nil
}

// captureRequestBody reads req.Body, then restores it — both req.Body and
// req.GetBody — so base still receives exactly what the caller sent. It
// returns the content type the caller declared and the body to record under
// the .NET recorder's text-and-256-KiB rule.
func captureRequestBody(req *http.Request) (contentType, body *string, err error) {
	contentType = headerPtr(req.Header)
	if req.Body == nil || req.Body == http.NoBody {
		return contentType, nil, nil
	}
	b, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, nil, err
	}
	_ = req.Body.Close()
	req.Body = io.NopCloser(bytes.NewReader(b))
	req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(b)), nil }
	return contentType, recordable(deref(contentType), b), nil
}

// captureResponseBody reads resp.Body fully, then replaces it with a
// re-readable copy so the caller can still consume the response. It returns
// the content type the server sent and the body to record.
func captureResponseBody(resp *http.Response) (contentType, body *string, err error) {
	contentType = headerPtr(resp.Header)
	if resp.Body == nil {
		return contentType, nil, nil
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		// RoundTrip then returns no response, so no caller will ever
		// close this body: close it here, or the connection leaks.
		_ = resp.Body.Close()
		return nil, nil, err
	}
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(b))
	return contentType, recordable(deref(contentType), b), nil
}

// recordable applies the .NET recorder's rule
// (packages/contract-recording/ContractRecording.cs:95-125): a body is kept
// only when its content type is text, it is not empty, and it is at most
// maxBodyBytes; otherwise nil, exactly as the recorder writes null for a
// binary or oversized body.
func recordable(contentType string, body []byte) *string {
	if !isText(contentType) || len(body) == 0 || len(body) > maxBodyBytes {
		return nil
	}
	s := string(body)
	return &s
}

// isText mirrors ContractRecording.IsText: a content type is text when it
// contains "json" or "x-www-form-urlencoded" (case-insensitively, so it
// still matches with a charset parameter) or starts with "text/".
func isText(contentType string) bool {
	lower := strings.ToLower(contentType)
	return strings.Contains(lower, "json") ||
		strings.Contains(lower, "x-www-form-urlencoded") ||
		strings.HasPrefix(lower, "text/")
}

func headerPtr(h http.Header) *string {
	if v := h.Get("Content-Type"); v != "" {
		return &v
	}
	return nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (r *Recorder) markExercised(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.exercised[id] = true
}

func (r *Recorder) wasExercised(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.exercised[id]
}

// Missing returns the operationIds of the Recorder's contract that no
// successful (status < 400) contract-conforming exchange has exercised yet,
// sorted.
func (r *Recorder) Missing() []string {
	r.mu.Lock()
	exercised := make(map[string]bool, len(r.exercised))
	for id, ok := range r.exercised {
		exercised[id] = ok
	}
	r.mu.Unlock()

	var missing []string
	for _, id := range operationIDs(r.doc) {
		if !exercised[id] {
			missing = append(missing, id)
		}
	}
	return missing
}

// operationIDs returns every operationId documented in doc, sorted.
func operationIDs(doc *openapi3.T) []string {
	var ids []string
	for _, item := range doc.Paths.Map() {
		for _, op := range item.Operations() {
			if op.OperationID != "" {
				ids = append(ids, op.OperationID)
			}
		}
	}
	sort.Strings(ids)
	return ids
}

// RequireCoverage runs m.Run(), then, when the run passed and no -run/-skip
// filter narrowed it, requires every operation of the Recorder's contract to
// have been exercised by a successful (status < 400) contract-conforming
// exchange — except those named in pending, its allow-list for operations
// later tasks still owe coverage for.
//
// A pending operation that was nonetheless exercised, and a pending entry
// that names no operation in the contract, both fail the run: the allow-list
// is only ever allowed to shrink honestly. Intended for TestMain:
//
//	func TestMain(m *testing.M) {
//		os.Exit(contracttest.RequireCoverage(m, rec, "postFoo"))
//	}
func RequireCoverage(m *testing.M, rec *Recorder, pending ...string) int {
	code := m.Run()
	missing, stale := pendingReport(rec, pending)
	return coverageExit(code, hasTestFilter(), missing, stale, os.Stderr)
}

// pendingReport computes RequireCoverage's missing and stale lists from rec
// and pending, apart from m.Run and the exit decision, so the allow-list
// handling can be unit-tested without a testing.M.
//
// missing is rec.Missing() with pending's entries removed. stale reports the
// two ways pending can be wrong: an entry naming no operation in rec's
// contract, and an entry that a successful contract-conforming exchange has
// in fact exercised — the allow-list is only ever allowed to shrink honestly.
func pendingReport(rec *Recorder, pending []string) (missing, stale []string) {
	documented := map[string]bool{}
	for _, id := range operationIDs(rec.doc) {
		documented[id] = true
	}

	pendingSet := make(map[string]bool, len(pending))
	for _, p := range pending {
		pendingSet[p] = true
		if !documented[p] {
			stale = append(stale, fmt.Sprintf("%s is not an operation in the contract", p))
			continue
		}
		if rec.wasExercised(p) {
			stale = append(stale, fmt.Sprintf("remove %s from pendingOperations", p))
		}
	}

	for _, id := range rec.Missing() {
		if !pendingSet[id] {
			missing = append(missing, id)
		}
	}
	return missing, stale
}

// hasTestFilter reports whether the run was narrowed with -run, -skip or
// -list, in which case operation coverage is necessarily incomplete (a
// -list run executes no tests at all) and the check is skipped rather than
// reported as a failure.
func hasTestFilter() bool {
	for _, name := range []string{"test.run", "test.skip", "test.list"} {
		if f := flag.Lookup(name); f != nil && f.Value.String() != "" {
			return true
		}
	}
	return false
}

// coverageExit decides RequireCoverage's process exit code. It is a plain
// function of m.Run's result and the coverage report so it can be
// table-tested without a testing.M: a failed run passes code through
// untouched; a filtered run skips the check and returns 0; otherwise it
// prints missing and stale operations to w, one per line under a heading,
// and returns 1 if either is non-empty.
func coverageExit(code int, filtered bool, missing, stale []string, w io.Writer) int {
	if code != 0 {
		return code
	}
	if filtered {
		return 0
	}
	if len(missing) == 0 && len(stale) == 0 {
		return 0
	}
	if len(missing) > 0 {
		_, _ = fmt.Fprintln(w, "operations never exercised by a contract-validated exchange:")
		for _, id := range missing {
			_, _ = fmt.Fprintln(w, "  "+id)
		}
	}
	if len(stale) > 0 {
		_, _ = fmt.Fprintln(w, "pendingOperations entries that no longer belong there:")
		for _, s := range stale {
			_, _ = fmt.Fprintln(w, "  "+s)
		}
	}
	return 1
}
