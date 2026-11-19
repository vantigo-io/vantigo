package module

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/httpx"
	"github.com/vantigo-io/vantigo/server/internal/operations"
)

// The general Idempotency-Key (idempotency design I1–I32): a caller that
// retries a creating POST with the same Idempotency-Key gets the first
// answer again instead of a second invoice, mail or shipment. The router
// installs it in wrap's serve, after authentication, the integration's rate
// limit and the request log's start, so a replay is counted and logged, and
// before the handler, on every operation idempotentOperation admits, when
// Access is a contracts.IdempotencyStore that is enabled (IDEMPOTENCY_ENABLED).
//
// A key is claimed in one transaction with the quota (I21, I27); the handler
// runs on a recorder that holds its answer back until the outcome is
// classified (I23): 2xx and 400/404/410/422 are stored and replayed; a 409,
// and a refusal that is not kept (no-store, Set-Cookie, not JSON), release the
// claim; anything else — 5xx, 408, 429, a panic, no answer written at all (a
// caller gone), an answer over 256 KB, no-store or Set-Cookie on a success, a
// non-JSON success — leaves the key unknown, which a retry is answered 409
// idempotency_outcome_unknown for, and the handler never runs again for it.
//
// A replay is the first answer as it was: the handler's own row-level checks
// do not run again for it, within the 24 hours a key lives (§10, accepted).

const (
	// idempotencyMaxBody is the largest answer body the store keeps (I5.3).
	idempotencyMaxBody = 256 << 10
	// idempotencyMaxKey is the longest key the header may carry (I2).
	idempotencyMaxKey = 255
	// idempotencyStoreTimeout bounds each statement the middleware makes, on
	// a context the request's cancellation does not reach (I26).
	idempotencyStoreTimeout = 5 * time.Second
	// idempotencyCanonicalMax is the largest body the fingerprint reads as
	// JSON; a larger one is hashed as its bytes, so reformatting it makes
	// another request (I24).
	idempotencyCanonicalMax = 64 << 10
)

// The refusals the middleware answers (I2, I5, I21).
const (
	codeIdempotencyKeyInvalid  = "idempotency_key_invalid"
	codeIdempotencyKeyReused   = "idempotency_key_reused"
	codeIdempotencyInProgress  = "idempotency_in_progress"
	codeIdempotencyUnknown     = "idempotency_outcome_unknown"
	codeIdempotencyQuota       = "idempotency_quota"
	idempotencyReplayedHeader  = "Idempotent-Replayed"
	idempotencyQuotaRetryAfter = "60"
)

// idempotencyReplayHeaders are the headers a stored answer keeps and a replay
// carries (I6).
var idempotencyReplayHeaders = []string{"Content-Type", "Location", "ETag", "Content-Language"}

// IdempotencyHeader is the request header a caller sends to make a creating
// POST safe to retry (I2).
const IdempotencyHeader = "Idempotency-Key"

// IdempotencyNever are the operations the router never honours an
// Idempotency-Key on although idempotentOperation's other tests would (I1,
// I26), each with the reason: one that can outlast the two-minute lease would
// answer a retry outcome_unknown while it still runs, and finish after.
var IdempotencyNever = map[string]string{
	"postStaffRetentionRun": "the retention run erases up to fifty people and walks the deletion ledger in the request itself, and can outlast the lease",
}

// Idempotent reports whether the router honours an Idempotency-Key on op, of
// method, under the composed permission catalog: idempotentOperation with the
// rule the router mounts op with. It is the one predicate the router, the
// integrator document and the MCP endpoint decide with (I19).
func Idempotent(method string, op *openapi3.Operation, catalog []contracts.Permission) bool {
	if op == nil {
		return false
	}
	access, _ := op.Extensions["x-vantigo-access"].(string)
	rule, err := contracts.ParseRule(access)
	if err != nil || setAPIRule(method, op, &rule) != nil {
		return false
	}
	known, never := idempotencyCatalog(catalog)
	return idempotentOperation(method, op, rule, known, never)
}

// idempotencyCatalog is what idempotentOperation reads of the catalog: every
// key it has, and contracts.APINever.
func idempotencyCatalog(catalog []contracts.Permission) (map[string]bool, []string) {
	known := make(map[string]bool, len(catalog))
	for _, p := range catalog {
		known[p.Key] = true
	}
	return known, contracts.APINever(catalog)
}

// idempotentOperation is I19 for op, of method, mounted under rule (its API
// mark already set, see setAPIRule), with known the catalog's keys and never
// its contracts.APINever: a POST behind a permission rule that a key could
// call — not marked x-vantigo-api: never, naming no APINever permission nor one
// the catalog lacks, as integrator.Build keeps it — that is not a read (marked
// x-vantigo-api: read), declares no Idempotency-Key of its own, takes JSON or
// nothing and answers JSON or nothing, is not in IdempotencyNever, and whose
// documented success does not set Cache-Control: no-store.
func idempotentOperation(method string, op *openapi3.Operation, rule contracts.Rule, known map[string]bool, never []string) bool {
	if method != http.MethodPost || rule.Kind != contracts.RulePermission || rule.API != contracts.APIAllowed {
		return false
	}
	if _, listed := IdempotencyNever[op.OperationID]; listed {
		return false
	}
	for _, key := range rule.Names {
		if !known[key] || slices.Contains(never, key) {
			return false
		}
	}
	for _, p := range op.Parameters {
		if p != nil && p.Value != nil && strings.EqualFold(p.Value.In, openapi3.ParameterInHeader) &&
			strings.EqualFold(p.Value.Name, IdempotencyHeader) {
			return false
		}
	}
	if !operations.JSONRequest(op) || !operations.JSONResponse(op) {
		return false
	}
	return !documentsNoStore(op)
}

// documentsNoStore reports whether a documented 2xx of op sets Cache-Control:
// no-store, which a one-time secret's answer does (I19, I20).
func documentsNoStore(op *openapi3.Operation) bool {
	if op.Responses == nil {
		return false
	}
	for code, ref := range op.Responses.Map() {
		if !strings.HasPrefix(code, "2") || ref == nil || ref.Value == nil {
			continue
		}
		for name, h := range ref.Value.Headers {
			if !strings.EqualFold(name, "Cache-Control") || h == nil || h.Value == nil {
				continue
			}
			if strings.Contains(strings.ToLower(h.Value.Description), "no-store") {
				return true
			}
			if s := h.Value.Schema; s != nil && s.Value != nil {
				for _, v := range append(append([]any{}, s.Value.Enum...), s.Value.Default, s.Value.Example) {
					if str, ok := v.(string); ok && strings.Contains(strings.ToLower(str), "no-store") {
						return true
					}
				}
			}
		}
	}
	return false
}

// idempotentRoute is one operation's Idempotency-Key middleware: the store, the
// operation and the names of its path parameters, in the route's order, which
// the fingerprint reads.
type idempotentRoute struct {
	store     contracts.IdempotencyStore
	operation string
	params    []string
}

// newIdempotentRoute is the middleware for op mounted at segments under rule,
// or nil when store is nil or op is out of scope.
func newIdempotentRoute(store contracts.IdempotencyStore, known map[string]bool, never []string, method string, op *openapi3.Operation, rule contracts.Rule, segments []routeSegment) *idempotentRoute {
	if store == nil || !idempotentOperation(method, op, rule, known, never) {
		return nil
	}
	var params []string
	for _, seg := range segments {
		if seg.param {
			params = append(params, seg.name)
		}
	}
	return &idempotentRoute{store: store, operation: op.OperationID, params: params}
}

// apiReasonKey carries the request log's reason holder (I22): keyGate.run puts
// a *string on the request's context and records what it holds once the
// request is answered; the middleware sets it on a replay.
type apiReasonKey struct{}

func withAPIReason(ctx context.Context, reason *string) context.Context {
	return context.WithValue(ctx, apiReasonKey{}, reason)
}

// setAPIReason sets the request log's reason for the request of ctx, when it
// is an API key's.
func setAPIReason(ctx context.Context, reason string) {
	if holder, ok := ctx.Value(apiReasonKey{}).(*string); ok && holder != nil {
		*holder = reason
	}
}

// serve runs next for req, an admitted request of principal p, under the
// key's rules; a request without the header runs next as it is.
func (m *idempotentRoute) serve(w http.ResponseWriter, req *http.Request, p contracts.Principal, next func(http.ResponseWriter, *http.Request)) {
	values := req.Header.Values(IdempotencyHeader)
	if len(values) == 0 || p.UserID == uuid.Nil {
		next(w, req)
		return
	}
	key, ok := parseIdempotencyKey(values)
	if !ok {
		writeIdempotencyError(w, http.StatusBadRequest, codeIdempotencyKeyInvalid,
			"The Idempotency-Key header must be one value of 1 to 255 printable ASCII characters.")
		return
	}

	scope, ok := idempotencyScope(req.Context(), p, m.operation, key)
	if !ok {
		// A grant's principal with no grant to scope the key by: the header is
		// not honoured, and the request runs as without it (fail closed).
		next(w, req)
		return
	}
	body, err := readBody(req)
	if err != nil {
		// The body cap or a broken read: no claim; the handler answers its
		// usual 400 from the same error (I24).
		next(w, req)
		return
	}
	digest := idempotencyFingerprint(req, m.params, body)

	ctx, cancel := idempotencyContext(req.Context())
	claim, err := m.store.ClaimIdempotencyKey(ctx, scope, digest)
	cancel()
	switch {
	case errors.Is(err, contracts.ErrIdempotencyQuota):
		w.Header().Set("Retry-After", idempotencyQuotaRetryAfter)
		writeIdempotencyError(w, http.StatusTooManyRequests, codeIdempotencyQuota,
			"Too many Idempotency-Keys are kept for this caller: retry later, or send fewer distinct keys.")
	case err != nil:
		httpx.WriteError(w, req, err)
	case claim.Claimed:
		m.run(w, req, scope, claim.Lease, next)
	default:
		answerExisting(w, req, claim.Existing)
	}
}

// answerExisting answers a request whose key is live, rec (I5.2): another
// request 422, a stored answer replayed, a running one 409 in progress, and
// one whose outcome cannot be told 409 outcome unknown.
func answerExisting(w http.ResponseWriter, req *http.Request, rec contracts.IdempotencyRecord) {
	switch {
	case !rec.SameRequest:
		writeIdempotencyError(w, http.StatusUnprocessableEntity, codeIdempotencyKeyReused,
			"This Idempotency-Key was used for a different request: use a new key for a new request.")
	case rec.State == contracts.IdempotencyDone:
		replay(w, req, rec.Response)
	case rec.State == contracts.IdempotencyInProgress && rec.LeaseLive:
		w.Header().Set("Retry-After", "1")
		writeIdempotencyError(w, http.StatusConflict, codeIdempotencyInProgress,
			"A request with this Idempotency-Key is still running: retry shortly.")
	default:
		writeIdempotencyError(w, http.StatusConflict, codeIdempotencyUnknown,
			"The first request with this Idempotency-Key may or may not have been applied: read the record, then use a new key.")
	}
}

// run runs next on a recorder for a key it claimed under lease, then stores,
// releases or marks unknown by the outcome (I23) and writes the answer.
func (m *idempotentRoute) run(w http.ResponseWriter, req *http.Request, scope contracts.IdempotencyKey, lease uuid.UUID, next func(http.ResponseWriter, *http.Request)) {
	rec := &idempotencyRecorder{w: w, header: http.Header{}}
	finished := false
	defer func() {
		if !finished {
			// A panic: the outcome cannot be told. The panic goes on to the
			// recovery further out, which answers 500.
			m.settle(req.Context(), scope, lease, outcomeUnknown, contracts.IdempotencyResponse{})
		}
	}()
	next(rec, req)
	finished = true
	outcome, resp := rec.outcome()
	m.settle(req.Context(), scope, lease, outcome, resp)
	rec.finish()
}

// The outcomes of a claimed request (I23).
type idempotencyOutcome int

const (
	outcomeDone idempotencyOutcome = iota
	outcomeRelease
	outcomeUnknown
)

// settle writes the claim's outcome, on a context of its own (I26). A failure
// is logged and leaves the key in progress, which runs out into
// outcome_unknown: never a second run.
func (m *idempotentRoute) settle(ctx context.Context, scope contracts.IdempotencyKey, lease uuid.UUID, outcome idempotencyOutcome, resp contracts.IdempotencyResponse) {
	ctx, cancel := idempotencyContext(ctx)
	defer cancel()
	var err error
	switch outcome {
	case outcomeDone:
		err = m.store.CompleteIdempotencyKey(ctx, scope, lease, resp)
	case outcomeRelease:
		err = m.store.ReleaseIdempotencyKey(ctx, scope, lease)
	default:
		err = m.store.MarkIdempotencyKeyUnknown(ctx, scope, lease)
	}
	if err != nil {
		slog.Default().ErrorContext(ctx, "module: settle an idempotency claim", "operation", m.operation, "error", err)
	}
}

// idempotencyContext is ctx without its cancellation, bounded by the store's
// own timeout.
func idempotencyContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), idempotencyStoreTimeout)
}

// replay writes a stored answer (I6), marked Idempotent-Replayed, and sets the
// request log's reason.
func replay(w http.ResponseWriter, req *http.Request, resp contracts.IdempotencyResponse) {
	setAPIReason(req.Context(), contracts.APIReasonIdempotentReplay)
	for _, name := range idempotencyReplayHeaders {
		if v, ok := resp.Headers[name]; ok {
			w.Header().Set(name, v)
		}
	}
	w.Header().Set(idempotencyReplayedHeader, "true")
	w.WriteHeader(resp.Status)
	if len(resp.Body) > 0 {
		_, _ = w.Write(resp.Body)
	}
}

// writeIdempotencyError answers one of the middleware's refusals in the
// common error shape.
func writeIdempotencyError(w http.ResponseWriter, status int, code, message string) {
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	body.Error.Code, body.Error.Message = code, message
	data, _ := json.Marshal(body) // plain strings: Marshal cannot fail
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(append(data, '\n'))
}

// parseIdempotencyKey is the header's one value (I2): trimmed, a surrounding
// pair of double quotes (the draft's String form) removed, then 1 to 255
// printable ASCII characters.
func parseIdempotencyKey(values []string) (string, bool) {
	if len(values) != 1 {
		return "", false
	}
	v := strings.TrimSpace(values[0])
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		v = v[1 : len(v)-1]
	}
	if v == "" || len(v) > idempotencyMaxKey {
		return "", false
	}
	for i := 0; i < len(v); i++ {
		if v[i] < 0x20 || v[i] > 0x7e {
			return "", false
		}
	}
	return v, true
}

// readBody reads req's body through the router's cap and puts back a reader
// that replays it. On a read error it puts back a reader that replays what was
// read and then fails with the same error, so the handler answers as it would
// have (I24).
func readBody(req *http.Request) ([]byte, error) {
	if req.Body == nil || req.Body == http.NoBody {
		return nil, nil
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		req.Body = io.NopCloser(io.MultiReader(bytes.NewReader(body), failingReader{err}))
		return nil, err
	}
	req.Body = io.NopCloser(bytes.NewReader(body))
	return body, nil
}

// failingReader answers err on every read.
type failingReader struct{ err error }

func (f failingReader) Read([]byte) (int, error) { return 0, f.err }

// idempotencyScope is the key's scope (I3, I28): the integration for an API
// key, across its keys; for anyone else the person or agent, and the way in —
// the session, a station, or the grant of the Assistant or of an agent's run
// with the customer it is scoped to — so an answer is never replayed to a
// caller the handler would have judged differently. A grant's principal with
// no grant on the context has no scope: false.
func idempotencyScope(ctx context.Context, p contracts.Principal, operation, key string) (contracts.IdempotencyKey, bool) {
	scope := contracts.IdempotencyKey{Principal: p.UserID, Via: "session", Operation: operation, Key: key}
	if p.Via == nil {
		return scope, true
	}
	scope.Via = p.Via.Kind
	switch p.Via.Kind {
	case contracts.ViaAPIKey:
	case contracts.ViaStation:
		scope.Via += ":" + p.Via.StationID.String()
	default:
		ref, ok := contracts.GrantFrom(ctx)
		if !ok {
			return contracts.IdempotencyKey{}, false
		}
		scope.Via += ":" + ref.ID.String()
		if p.Via.CustomerID != nil {
			scope.Via += fmt.Sprintf(":customer:%d", *p.Via.CustomerID)
			scope.CustomerID = p.Via.CustomerID
		}
	}
	return scope, true
}

// idempotencyFingerprint is SHA-256 over the request (I24), which the store
// keys with an HMAC before it keeps or compares it: the method, the path
// parameters in the route's order, the query sorted by name with each name's
// values in order, the Content-Type's media type and charset, If-Match,
// If-None-Match, and the body — canonical JSON for a JSON body of at most
// 64 KiB in valid UTF-8, so another key order or other spacing is the same
// request, else its bytes. Every name, value and count is its own
// length-prefixed part.
func idempotencyFingerprint(req *http.Request, params []string, body []byte) []byte {
	h := sha256.New()
	var n [binary.MaxVarintLen64]byte
	count := func(c int) { h.Write(n[:binary.PutUvarint(n[:], uint64(c))]) }
	part := func(s []byte) {
		count(len(s))
		h.Write(s)
	}
	part([]byte(req.Method))
	count(len(params))
	for _, name := range params {
		part([]byte(name))
		part([]byte(req.PathValue(name)))
	}
	query, _ := url.ParseQuery(req.URL.RawQuery)
	names := make([]string, 0, len(query))
	for name := range query {
		names = append(names, name)
	}
	slices.Sort(names)
	count(len(names))
	for _, name := range names {
		part([]byte(name))
		count(len(query[name]))
		for _, v := range query[name] {
			part([]byte(v))
		}
	}
	contentType := req.Header.Values("Content-Type")
	count(len(contentType))
	for _, v := range contentType {
		part([]byte(normalMediaType(v)))
	}
	for _, header := range []string{"If-Match", "If-None-Match"} {
		values := req.Header.Values(header)
		count(len(values))
		for _, v := range values {
			part([]byte(v))
		}
	}
	if canonical, ok := canonicalJSON(body); ok {
		part([]byte("json"))
		part(canonical)
	} else {
		part([]byte("raw"))
		part(body)
	}
	return h.Sum(nil)
}

// normalMediaType is a Content-Type as the fingerprint reads it: the media
// type lower-cased, and its charset, lower-cased, unless it is utf-8, which a
// JSON body is anyway; another parameter is kept as it is.
func normalMediaType(v string) string {
	mt, params, err := mime.ParseMediaType(v)
	if err != nil {
		return strings.ToLower(strings.TrimSpace(v))
	}
	if cs, ok := params["charset"]; ok {
		if cs = strings.ToLower(cs); cs == "utf-8" || cs == "utf8" {
			delete(params, "charset")
		} else {
			params["charset"] = cs
		}
	}
	return mime.FormatMediaType(mt, params)
}

// canonicalJSON is body decoded with UseNumber and marshalled again, which
// sorts every object's keys and drops the spacing; false when body is empty,
// over 64 KiB, not valid UTF-8 (which decoding would replace, so two bodies
// could meet) or not one JSON value. Nesting is bounded by the decoder's own
// depth limit.
func canonicalJSON(body []byte) ([]byte, bool) {
	if len(body) > idempotencyCanonicalMax || len(bytes.TrimSpace(body)) == 0 || !utf8.Valid(body) {
		return nil, false
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, false
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, false
	}
	out, err := json.Marshal(v)
	if err != nil {
		return nil, false
	}
	return out, true
}

// idempotencyRecorder holds a handler's answer back until the outcome is
// classified (I5.3). It replaces the response writer before
// contracts.WithRequest, so a handler that writes headers through
// contracts.ResponseWriterFrom writes them here too (I32). A body over
// idempotencyMaxBody is written through from then on, and its outcome is
// unknown. Flush does nothing: every operation in scope answers JSON, and the
// answer is written once, whole.
type idempotencyRecorder struct {
	w           http.ResponseWriter
	header      http.Header
	status      int
	wroteHeader bool
	body        bytes.Buffer
	through     bool
}

func (r *idempotencyRecorder) Header() http.Header { return r.header }

func (r *idempotencyRecorder) WriteHeader(code int) {
	if r.wroteHeader || code < 200 {
		return
	}
	r.status, r.wroteHeader = code, true
}

func (r *idempotencyRecorder) Write(p []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	if r.through {
		return r.w.Write(p)
	}
	if r.body.Len()+len(p) > idempotencyMaxBody {
		r.writeThrough()
		return r.w.Write(p)
	}
	return r.body.Write(p)
}

// Flush satisfies http.Flusher; the answer is written once it is classified.
func (r *idempotencyRecorder) Flush() {}

// Unwrap lets http.ResponseController reach the connection's writer.
func (r *idempotencyRecorder) Unwrap() http.ResponseWriter { return r.w }

// writeThrough writes what is held and passes every later write through.
func (r *idempotencyRecorder) writeThrough() {
	r.copyHeader()
	r.w.WriteHeader(r.statusCode())
	_, _ = r.w.Write(r.body.Bytes())
	r.body.Reset()
	r.through = true
}

func (r *idempotencyRecorder) statusCode() int {
	if !r.wroteHeader {
		return http.StatusOK
	}
	return r.status
}

func (r *idempotencyRecorder) copyHeader() {
	dst := r.w.Header()
	for k, v := range r.header {
		dst[k] = v
	}
}

// finish writes the held answer to the client.
func (r *idempotencyRecorder) finish() {
	if r.through {
		return
	}
	r.copyHeader()
	r.w.WriteHeader(r.statusCode())
	if r.body.Len() > 0 {
		_, _ = r.w.Write(r.body.Bytes())
	}
}

// outcome classifies the answer (I20, I23) and, for one to store, answers it.
// A handler that wrote nothing at all — the caller gone, whose error
// httpx.WriteError answers with nothing, which covers every request whose
// caller went before a status was written — is unknown: never an empty 200.
func (r *idempotencyRecorder) outcome() (idempotencyOutcome, contracts.IdempotencyResponse) {
	if !r.wroteHeader {
		return outcomeUnknown, contracts.IdempotencyResponse{}
	}
	status := r.statusCode()
	success := status >= 200 && status < 300
	deterministic := status == http.StatusBadRequest || status == http.StatusNotFound ||
		status == http.StatusGone || status == http.StatusUnprocessableEntity
	switch {
	case r.through:
		return outcomeUnknown, contracts.IdempotencyResponse{}
	case status == http.StatusConflict:
		return outcomeRelease, contracts.IdempotencyResponse{}
	case !success && !deterministic:
		return outcomeUnknown, contracts.IdempotencyResponse{}
	}
	secret := len(r.header.Values("Set-Cookie")) > 0 ||
		strings.Contains(strings.ToLower(strings.Join(r.header.Values("Cache-Control"), ",")), "no-store")
	jsonBody := r.body.Len() == 0 || operations.IsJSON(r.header.Get("Content-Type"))
	switch {
	case secret && success, !jsonBody && success:
		// A success that must not be kept, or that is not JSON: what it did
		// cannot be answered again.
		return outcomeUnknown, contracts.IdempotencyResponse{}
	case secret, !jsonBody:
		// A refusal applied nothing: it is not kept, and a retry runs again.
		return outcomeRelease, contracts.IdempotencyResponse{}
	}
	headers := map[string]string{}
	for _, name := range idempotencyReplayHeaders {
		if v := r.header.Get(name); v != "" {
			headers[name] = v
		}
	}
	return outcomeDone, contracts.IdempotencyResponse{Status: status, Headers: headers, Body: bytes.Clone(r.body.Bytes())}
}
