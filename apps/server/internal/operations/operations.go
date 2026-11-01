// Package operations is the platform's dispatcher for the Assistant (the
// Assistant design S2, S4, S5): it knows every operation of the merged
// contract of the enabled modules, says which of them a person may call and
// how the assistant may call each (its mode), describes one in a form a small
// model can read, and calls one in-process through the composed router, as the
// person.
//
// A call goes through the same router the interface's own calls go through, on
// a request that carries an assistant grant on its context
// (contracts.WithAssistantGrant). The router rate-limits it, asks identity's
// Access.Check — which answers the grant's session principal — and runs the
// handler's own row-level rules, so the access rule, MFA, rate limits, body
// caps and every refusal behave exactly as they do for the browser. There is no
// second permission model and no hand-written tool per feature.
//
// This is a sanctioned exception to module boundaries rule 6 (a module never
// calls another module's HTTP endpoints in-process): the dispatcher is platform
// code, it calls only through the public contract, and only the module named
// assistant receives it (module.Deps.Operations, set by Compose; depguard
// forbids every other import of this package). No module learns the assistant
// exists.
//
// Compose builds the dispatcher unbound before any module mounts, hands it to
// the assistant module, and Binds it once the composed router and the merged
// contract exist, so it is for use while serving, never while mounting.
package operations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"unicode"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// Info is one operation as the assistant lists it.
type Info struct {
	// ID is the operationId.
	ID     string `json:"id"`
	Method string `json:"method"`
	// Path is the contract's path template, such as /api/v1/customers/{id}.
	Path        string `json:"path"`
	Tag         string `json:"tag,omitempty"`
	Summary     string `json:"summary"`
	Description string `json:"description,omitempty"`
	Mode        Mode   `json:"mode"`
}

// entry is one operation with what List, Describe and Call need.
type entry struct {
	Info
	rule   contracts.Rule
	ruleOK bool

	params             []*openapi3.Parameter
	body               *openapi3.SchemaRef
	bodyRequired       bool
	hasBody            bool
	success            *openapi3.SchemaRef
	successStatus      string
	successDescription string

	// search is the lower-cased text each field of the operation contributes.
	searchSummary, searchTag, searchPath, searchDescription string
}

// state is the bound dispatcher: immutable once built.
type state struct {
	entries []*entry
	byID    map[string]*entry
	access  contracts.Access
	handler http.Handler
	origin  string
}

// Options configure Bind.
type Options struct {
	// Origin is scheme://host[:port] the in-process requests claim, so a handler
	// that builds an absolute link builds the installation's own (APP_URL's
	// origin). It defaults to http://localhost.
	Origin string
}

// Dispatcher lists, describes and calls operations. The zero value is not
// usable; build it with New and Bind it.
type Dispatcher struct {
	st atomic.Pointer[state]
}

// New returns an unbound dispatcher. Every method but Bind answers an error
// (or nothing) until Bind has run.
func New() *Dispatcher { return &Dispatcher{} }

// Bind gives the dispatcher what it dispatches over: contract is the merged
// contract of the enabled modules as GET /api/openapi.json serves it (JSON,
// every reference internalised), catalog the composed permission catalog,
// access identity's Access, and handler the composed API handler the calls go
// through. It computes every operation's mode once.
func (d *Dispatcher) Bind(contract []byte, catalog map[string]contracts.Permission, access contracts.Access, handler http.Handler, opts Options) error {
	if access == nil || handler == nil {
		return errors.New("operations: Bind needs an Access and a handler")
	}
	doc, err := openapi3.NewLoader().LoadFromData(contract)
	if err != nil {
		return fmt.Errorf("operations: load the merged contract: %w", err)
	}
	origin := strings.TrimSuffix(opts.Origin, "/")
	if origin == "" {
		origin = "http://localhost"
	}

	st := &state{byID: map[string]*entry{}, access: access, handler: handler, origin: origin}
	if doc.Paths != nil {
		for path, item := range doc.Paths.Map() {
			for method, op := range item.Operations() {
				if op.OperationID == "" {
					continue
				}
				if _, dup := st.byID[op.OperationID]; dup {
					return fmt.Errorf("operations: operationId %q is declared twice", op.OperationID)
				}
				e := newEntry(path, method, item, op, catalog)
				st.entries = append(st.entries, e)
				st.byID[e.ID] = e
			}
		}
	}
	sort.Slice(st.entries, func(i, j int) bool {
		if st.entries[i].Path != st.entries[j].Path {
			return st.entries[i].Path < st.entries[j].Path
		}
		return st.entries[i].Method < st.entries[j].Method
	})
	d.st.Store(st)
	return nil
}

func newEntry(path, method string, item *openapi3.PathItem, op *openapi3.Operation, catalog map[string]contracts.Permission) *entry {
	access, _ := op.Extensions["x-vantigo-access"].(string)
	rule, err := contracts.ParseRule(access)
	e := &entry{rule: rule, ruleOK: err == nil}
	e.Info = Info{ID: op.OperationID, Method: method, Path: path, Summary: strings.TrimSpace(op.Summary), Description: strings.TrimSpace(op.Description)}
	if len(op.Tags) > 0 {
		e.Tag = op.Tags[0]
	}
	e.Mode = effectiveMode(method, path, op, rule, e.ruleOK, catalog)

	// Path-level parameters first; an operation's own override them.
	seen := map[string]int{}
	for _, ref := range append(append(openapi3.Parameters{}, item.Parameters...), op.Parameters...) {
		if ref == nil || ref.Value == nil {
			continue
		}
		p := ref.Value
		key := p.In + ":" + p.Name
		if i, ok := seen[key]; ok {
			e.params[i] = p
			continue
		}
		seen[key] = len(e.params)
		e.params = append(e.params, p)
	}

	if op.RequestBody != nil && op.RequestBody.Value != nil {
		e.hasBody = true
		e.bodyRequired = op.RequestBody.Value.Required
		for contentType, media := range op.RequestBody.Value.Content {
			if isJSON(contentType) && media != nil {
				e.body = media.Schema
				break
			}
		}
	}

	if op.Responses != nil {
		var codes []string
		for code := range op.Responses.Map() {
			if strings.HasPrefix(code, "2") {
				codes = append(codes, code)
			}
		}
		sort.Strings(codes)
		for _, code := range codes {
			ref := op.Responses.Map()[code]
			if ref == nil || ref.Value == nil {
				continue
			}
			var schema *openapi3.SchemaRef
			for contentType, media := range ref.Value.Content {
				if isJSON(contentType) && media != nil {
					schema = media.Schema
					break
				}
			}
			if e.successStatus == "" || (schema != nil && e.success == nil) {
				e.successStatus, e.success = code, schema
				if ref.Value.Description != nil {
					e.successDescription = shorten(*ref.Value.Description)
				}
			}
			if schema != nil {
				break
			}
		}
	}

	trimmed := strings.TrimPrefix(path, "/api/v1/")
	e.searchSummary = strings.ToLower(e.Summary)
	e.searchTag = strings.ToLower(e.Tag)
	e.searchPath = strings.ToLower(strings.NewReplacer("{", " ", "}", " ", "/", " ", "-", " ", "_", " ").Replace(trimmed))
	e.searchDescription = strings.ToLower(e.Description)
	return e
}

func (d *Dispatcher) bound() (*state, error) {
	if st := d.st.Load(); st != nil {
		return st, nil
	}
	return nil, &Error{Code: CodeNotReady, Message: "the operations dispatcher is not bound to a composed API yet"}
}

// All is every operation of the contract with its mode, never ones included.
// It is for the assistant's own bookkeeping and for tests; what the model is
// shown is List's.
func (d *Dispatcher) All() []Info {
	st, err := d.bound()
	if err != nil {
		return nil
	}
	out := make([]Info, len(st.entries))
	for i, e := range st.entries {
		out[i] = e.Info
	}
	return out
}

// Info is one operation, whatever its mode, and false when there is none by
// that id.
func (d *Dispatcher) Info(id string) (Info, bool) {
	st, err := d.bound()
	if err != nil {
		return Info{}, false
	}
	e, ok := st.byID[id]
	if !ok {
		return Info{}, false
	}
	return e.Info, true
}

// List is the operations userID may call, as the assistant sees them: those
// whose mode is not never and whose access rule the user meets. A permission
// rule is asked of Access.UserHolds (once per key per call); policy:ActiveAccount
// and policy:Business are listed, and the call decides, Business's MFA being
// the session's. The order is by path, then method.
func (d *Dispatcher) List(ctx context.Context, userID uuid.UUID) ([]Info, error) {
	st, err := d.bound()
	if err != nil {
		return nil, err
	}
	held := map[string]bool{}
	var out []Info
	for _, e := range st.entries {
		if e.Mode == ModeNever {
			continue
		}
		ok, err := st.meets(ctx, userID, e, held)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, e.Info)
		}
	}
	return out, nil
}

// meets reports whether the user meets e's access rule, as far as it can be
// known without a request.
func (s *state) meets(ctx context.Context, userID uuid.UUID, e *entry, held map[string]bool) (bool, error) {
	if !e.ruleOK {
		return false, nil
	}
	if e.rule.Kind != contracts.RulePermission {
		return true, nil
	}
	for _, key := range e.rule.Names {
		ok, known := held[key]
		if !known {
			var err error
			if ok, err = s.access.UserHolds(ctx, userID, key); err != nil {
				return false, fmt.Errorf("operations: check what the user may do: %w", err)
			}
			held[key] = ok
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

// Search is List narrowed to the operations the query matches, best first, at
// most limit of them (10 when limit is not positive, 50 at most). It scores a
// simple token match over the summary, the description, the tag and the path —
// never the operationId alone — and needs no index or dependency.
func (d *Dispatcher) Search(ctx context.Context, userID uuid.UUID, query string, limit int) ([]Info, error) {
	callable, err := d.List(ctx, userID)
	if err != nil {
		return nil, err
	}
	st, _ := d.bound()
	if limit <= 0 {
		limit = 10
	}
	limit = min(limit, 50)

	tokens := tokenize(query)
	if len(tokens) == 0 {
		return nil, nil
	}
	type scored struct {
		info  Info
		score int
	}
	var hits []scored
	for _, info := range callable {
		e := st.byID[info.ID]
		score, matched := 0, 0
		for _, tok := range tokens {
			s := 0
			if matches(e.searchSummary, tok) {
				s += 6
			}
			if matches(e.searchTag, tok) {
				s += 3
			}
			if matches(e.searchPath, tok) {
				s += 3
			}
			if matches(e.searchDescription, tok) {
				s++
			}
			if s > 0 {
				matched++
			}
			score += s
		}
		if score == 0 {
			continue
		}
		// An operation that answers every word of the query outranks one that
		// answers a single word well.
		score += matched * 4
		hits = append(hits, scored{info: info, score: score})
	}
	// Best first; of equal scores the shorter summary, which says less else.
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return len(hits[i].info.Summary) < len(hits[j].info.Summary)
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	out := make([]Info, len(hits))
	for i, h := range hits {
		out[i] = h.info
	}
	return out, nil
}

// stopWords are the words of a question that say nothing about which operation
// it is about, in the two languages the interface speaks.
var stopWords = map[string]bool{
	"a": true, "an": true, "the": true, "of": true, "to": true, "in": true, "on": true, "for": true, "and": true, "or": true,
	"how": true, "do": true, "does": true, "can": true, "i": true, "me": true, "my": true, "is": true, "are": true, "with": true,
	"en": true, "et": true, "ei": true, "og": true, "í": true, "på": true, "av": true, "til": true, "som": true,
	"jeg": true, "hvordan": true, "kan": true, "er": true, "det": true, "den": true, "med": true,
}

// tokenize lower-cases query and splits it into its alphanumeric words,
// dropping the stop words.
func tokenize(query string) []string {
	words := strings.FieldsFunc(strings.ToLower(query), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	return slices.DeleteFunc(words, func(w string) bool { return stopWords[w] })
}

// matches reports whether field contains tok, or tok without a plural s.
func matches(field, tok string) bool {
	if strings.Contains(field, tok) {
		return true
	}
	return len(tok) > 3 && strings.HasSuffix(tok, "s") && strings.Contains(field, strings.TrimSuffix(tok, "s"))
}

// Describe is the operation in the compact form a model reads: its summary,
// mode, parameters, request body and success response, with every $ref
// resolved to a depth limit (a cycle is cut with a note), enums inline and
// descriptions kept, in at most 8 KB of JSON. It answers an unknown id with
// CodeUnknownOperation, and does not check the mode: whether the operation is
// the model's to know about is the caller's decision, from List.
func (d *Dispatcher) Describe(id string) (json.RawMessage, error) {
	st, err := d.bound()
	if err != nil {
		return nil, err
	}
	e, ok := st.byID[id]
	if !ok {
		return nil, &Error{Code: CodeUnknownOperation, Message: fmt.Sprintf("there is no operation %q", id)}
	}
	return e.describe(), nil
}

// CallRequest is one call.
type CallRequest struct {
	OperationID string
	// PathParams fill the path template's {names}. Values are strings or
	// numbers.
	PathParams map[string]any
	// Query is the query string: a string, a number or a bool is one value, an
	// array repeats the key, null is left out.
	Query map[string]any
	// Headers are the operation's declared header parameters (If-Match,
	// Idempotency-Key); a header the operation does not declare is refused.
	Headers map[string]string
	// Body is the JSON request body, empty for none.
	Body json.RawMessage
	// Confirmed says the person confirmed this call. An operation whose mode
	// is confirm is refused without it.
	Confirmed bool
}

// CallResult is what the router answered.
type CallResult struct {
	Status int `json:"status"`
	// Body is the response as JSON: the JSON the handler wrote, or its text as
	// a JSON string when it wrote something else. It is empty for no body and
	// when Truncated.
	Body json.RawMessage `json:"body,omitempty"`
	// Truncated says the response was larger than the 1 MiB the call keeps and
	// Body was dropped: the caller should narrow the request.
	Truncated bool `json:"truncated,omitempty"`
}

// Call serves req through the composed router as the grant's principal.
//
// ctx must carry the assistant grant (contracts.WithAssistantGrant) and the
// user it is for (contracts.AsUser), which names the rate-limit bucket: the
// in-process request's RemoteAddr is "assistant-<user id>", so the router's
// per-client limits are per person. The request context is built afresh, from
// nothing but the grant and ctx's cancellation, so no principal, forwarded
// address or other value of the caller's leaks into it.
//
// A call is refused, with a typed *Error, before any dispatch when the
// operation is unknown, never, or confirm without Confirmed, when there is no
// grant, or when the request does not fit the operation. Once dispatched, the
// router's answer is a result: a 4xx or 5xx is a status and a body, not an
// error, so the model can read the refusal.
func (d *Dispatcher) Call(ctx context.Context, req CallRequest) (CallResult, error) {
	st, err := d.bound()
	if err != nil {
		return CallResult{}, err
	}
	e, ok := st.byID[req.OperationID]
	if !ok {
		return CallResult{}, &Error{Code: CodeUnknownOperation, Message: fmt.Sprintf("there is no operation %q", req.OperationID)}
	}
	switch e.Mode {
	case ModeNever:
		return CallResult{}, &Error{Code: CodeNever, Message: fmt.Sprintf("the assistant may never call %s", e.ID)}
	case ModeConfirm:
		if !req.Confirmed {
			return CallResult{}, &Error{Code: CodeConfirmationRequired, Message: fmt.Sprintf("%s changes something and needs the person's confirmation", e.ID)}
		}
	}
	grantID, ok := contracts.AssistantGrantFrom(ctx)
	if !ok {
		return CallResult{}, &Error{Code: CodeNoGrant, Message: "the call carries no assistant grant"}
	}
	userID, ok := contracts.UserID(ctx)
	if !ok {
		return CallResult{}, &Error{Code: CodeNoGrant, Message: "the call names no user (contracts.AsUser)"}
	}

	target, body, err := e.build(req)
	if err != nil {
		return CallResult{}, err
	}

	// Built from nothing, so none of the caller's values come along; its
	// cancellation does.
	rctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	rctx = contracts.WithAssistantGrant(rctx, grantID)

	var reader io.Reader // a nil *bytes.Reader in an io.Reader is not nil
	if body != nil {
		reader = body
	}
	hreq, err := http.NewRequestWithContext(rctx, e.Method, st.origin+target, reader)
	if err != nil {
		return CallResult{}, &Error{Code: CodeInvalidRequest, Message: "the request could not be built: " + err.Error()}
	}
	hreq.RemoteAddr = "assistant-" + userID.String()
	hreq.Header.Set("Accept", "application/json")
	if body != nil {
		hreq.Header.Set("Content-Type", "application/json")
	}
	for name, value := range req.Headers {
		hreq.Header.Set(name, value)
	}

	rec := newRecorder(resultMaxBytes)
	if err := serve(st.handler, rec, hreq); err != nil {
		return CallResult{}, err
	}
	return rec.result(), nil
}

// serve runs the handler, turning a panic into an error: the real server
// recovers a handler's panic per request, and an assistant's run must not die
// of one.
func serve(h http.Handler, w http.ResponseWriter, r *http.Request) (err error) {
	defer func() {
		if recover() != nil {
			err = &Error{Code: CodeCallFailed, Message: "the operation failed unexpectedly"}
		}
	}()
	h.ServeHTTP(w, r)
	return nil
}

// result is the recorded response as a CallResult.
func (r *recorder) result() CallResult {
	out := CallResult{Status: r.status}
	if r.truncated() {
		out.Truncated = true
		return out
	}
	raw := bytes.TrimSpace(r.body.Bytes())
	switch {
	case len(raw) == 0:
	case isJSON(r.header.Get("Content-Type")) && json.Valid(raw):
		out.Body = bytes.Clone(raw)
	default:
		// Not JSON (an error page, a file): its text, so the model can read it.
		quoted, err := json.Marshal(strings.ToValidUTF8(string(raw), "�"))
		if err == nil {
			out.Body = quoted
		}
	}
	return out
}

// build fills the path template, the query string and the body for req.
func (e *entry) build(req CallRequest) (target string, body *bytes.Reader, err error) {
	path := e.Path
	declared := map[string]bool{}
	for _, p := range e.params {
		if p.In != "path" {
			continue
		}
		declared[p.Name] = true
		raw, ok := req.PathParams[p.Name]
		value, scalarOK := scalar(raw)
		if !ok || !scalarOK || value == "" {
			return "", nil, &Error{Code: CodeInvalidRequest, Message: fmt.Sprintf("the path parameter %q is required", p.Name)}
		}
		path = strings.ReplaceAll(path, "{"+p.Name+"}", url.PathEscape(value))
	}
	if strings.ContainsAny(path, "{}") {
		return "", nil, &Error{Code: CodeInvalidRequest, Message: "the path has a parameter the contract does not declare"}
	}
	for name := range req.PathParams {
		if !declared[name] {
			return "", nil, &Error{Code: CodeInvalidRequest, Message: fmt.Sprintf("%s has no path parameter %q", e.ID, name)}
		}
	}

	queryDeclared := map[string]bool{}
	headerDeclared := map[string]bool{}
	for _, p := range e.params {
		switch p.In {
		case "query":
			queryDeclared[p.Name] = true
		case "header":
			headerDeclared[strings.ToLower(p.Name)] = true
		}
	}
	query := url.Values{}
	for name, raw := range req.Query {
		if !queryDeclared[name] {
			return "", nil, &Error{Code: CodeInvalidRequest, Message: fmt.Sprintf("%s has no query parameter %q", e.ID, name)}
		}
		values, ok := scalars(raw)
		if !ok {
			return "", nil, &Error{Code: CodeInvalidRequest, Message: fmt.Sprintf("the query parameter %q must be a string, a number, a boolean or a list of them", name)}
		}
		for _, v := range values {
			query.Add(name, v)
		}
	}
	for name := range req.Headers {
		if !headerDeclared[strings.ToLower(name)] {
			return "", nil, &Error{Code: CodeInvalidRequest, Message: fmt.Sprintf("%s has no header parameter %q", e.ID, name)}
		}
	}

	target = path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}

	trimmed := bytes.TrimSpace(req.Body)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return target, nil, nil
	}
	if !e.hasBody {
		return "", nil, &Error{Code: CodeInvalidRequest, Message: e.ID + " takes no request body"}
	}
	if !json.Valid(trimmed) {
		return "", nil, &Error{Code: CodeInvalidRequest, Message: "the request body is not valid JSON"}
	}
	return target, bytes.NewReader(trimmed), nil
}

// scalar is v as one query or path value.
func scalar(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, true
	case bool:
		return strconv.FormatBool(x), true
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64), true
	case float32:
		return strconv.FormatFloat(float64(x), 'f', -1, 32), true
	case int:
		return strconv.Itoa(x), true
	case int32:
		return strconv.FormatInt(int64(x), 10), true
	case int64:
		return strconv.FormatInt(x, 10), true
	case json.Number:
		return x.String(), true
	case uuid.UUID:
		return x.String(), true
	}
	return "", false
}

// scalars is v as the values of one query key: none for null, several for a list.
func scalars(v any) ([]string, bool) {
	if v == nil {
		return nil, true
	}
	if list, ok := v.([]any); ok {
		out := make([]string, 0, len(list))
		for _, item := range list {
			s, ok := scalar(item)
			if !ok {
				return nil, false
			}
			out = append(out, s)
		}
		return out, true
	}
	if list, ok := v.([]string); ok {
		return list, true
	}
	s, ok := scalar(v)
	if !ok {
		return nil, false
	}
	return []string{s}, true
}
