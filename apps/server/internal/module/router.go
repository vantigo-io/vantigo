package module

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/httpx"
	"github.com/vantigo-io/vantigo/server/internal/ratelimit"
)

// DefaultMaxBodyBytes is the request-body cap of every operation whose
// module sets neither RouterOptions.MaxBodyBytes nor a BodyLimits entry
// for it: 1 MiB, far above any JSON document a contract describes, and
// far below what an anonymous caller could make a decoder buffer without
// one.
const DefaultMaxBodyBytes int64 = 1 << 20

// RouterOptions configures a Router.
type RouterOptions struct {
	Doc     *openapi3.T
	Access  contracts.Access
	Limiter *ratelimit.Limiter
	Limits  map[string]ratelimit.Policy // operationId → policy
	Catalog map[string]contracts.Permission
	// MaxBodyBytes caps every operation's request body; zero means
	// DefaultMaxBodyBytes.
	MaxBodyBytes int64
	// BodyLimits overrides MaxBodyBytes for single operations, keyed by
	// operationId (a multipart upload that needs more, say).
	BodyLimits map[string]int64
}

// Router implements every generated package's ServeMux interface
// (HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request))
// plus http.Handler). It is the BaseRouter every module's
// HandlerWithOptions mounts on.
type Router struct {
	opts RouterOptions

	routes map[string][]*route // HTTP method → every route registered for it, each split into segments once, in registration order

	ops          map[string]*openapi3.Operation // "METHOD /path" → operation, from opts.Doc
	operationIDs map[string]bool                // every known operationId, from opts.Doc
	registered   map[string]bool                // "METHOD /path" → HandleFunc found a contract operation for it
	normalized   map[string]string              // normalizeKey(method, segments) → the first pattern registered with that route shape (see HandleFunc's duplicate check)

	problems []string // recorded as HandleFunc runs; Err adds the two checks it can only make once registration is done
}

// route is one pattern HandleFunc registered, split into path segments once
// so ServeHTTP never re-parses a pattern.
type route struct {
	segments []routeSegment
	handler  http.HandlerFunc
}

// routeSegment is one segment of a registered pattern: either a literal that
// a request's decoded segment must equal exactly, or a "{name}" that matches
// any single decoded segment and is bound under name (see (*Request).SetPathValue)
// for the generated handler to read with r.PathValue(name).
type routeSegment struct {
	literal string
	name    string // param name; empty for a literal segment
	param   bool
}

// splitSegments splits a contract path ("/api/v1/x/{id}") into its segments.
// The contract has no root path and no trailing slash, so path always starts
// with "/" and TrimPrefix always has something to split.
func splitSegments(path string) []routeSegment {
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	segments := make([]routeSegment, len(parts))
	for i, p := range parts {
		if strings.HasPrefix(p, "{") && strings.HasSuffix(p, "}") {
			segments[i] = routeSegment{name: p[1 : len(p)-1], param: true}
		} else {
			segments[i] = routeSegment{literal: p}
		}
	}
	return segments
}

// decodeSegments splits an EscapedPath into segments and decodes each one,
// exactly as stdlib http.ServeMux does: splitting the still-escaped path
// first keeps a %2F inside one segment from being mistaken for the
// separator between two, and decoding each segment afterwards is what lets a
// literal route segment match a request whose matching text arrived percent-
// encoded. (r.URL.Path, by contrast, is already fully decoded before a mux
// ever sees segment boundaries, which is exactly what would let a %2F split
// one segment into two.) A segment that fails to decode is left as-is, so it
// can still match a route by coincidence but never panics — the same
// fallback stdlib's private pathUnescape uses.
func decodeSegments(escapedPath string) []string {
	parts := strings.Split(strings.TrimPrefix(escapedPath, "/"), "/")
	for i, p := range parts {
		if s, err := url.PathUnescape(p); err == nil {
			parts[i] = s
		}
	}
	return parts
}

// match returns the most specific route in routes whose segment count equals
// segments' and whose literal segments all equal it. "Most specific" is
// literal-before-parameter precedence: at the first segment where two
// matching routes differ between a literal and a {param}, the literal one
// wins, whatever the segments around it were — see moreSpecific. HandleFunc
// rejects two operations registering the same route shape (see normalizeKey),
// so among survivors that precedence is a strict order with a single
// greatest element; match returns it, or nil if no route survives.
//
// This is a linear scan — cost is O(routes registered for this method ×
// segment count), not bounded by depth alone — which is the right trade for
// a module's route count (tens of operations, not thousands); a trie is not
// warranted.
func match(routes []*route, segments []string) *route {
	var best *route
	for _, rt := range routes {
		if !matches(rt, segments) {
			continue
		}
		if best == nil || moreSpecific(rt, best) {
			best = rt
		}
	}
	return best
}

// matches reports whether rt's segment count equals segments' and every one
// of rt's literal segments equals the corresponding one; a {param} segment
// matches any non-empty segment. An empty segment — the trailing slash on a
// {param}-ended route, or a doubled "//" anywhere — matches nothing: there
// is no trailing-slash redirect or path-cleaning to fold it away (see
// ServeHTTP), so it must fall through to the 404 problem rather than bind a
// {param} to "".
func matches(rt *route, segments []string) bool {
	if len(rt.segments) != len(segments) {
		return false
	}
	for i, seg := range rt.segments {
		if seg.param {
			if segments[i] == "" {
				return false
			}
			continue
		}
		if seg.literal != segments[i] {
			return false
		}
	}
	return true
}

// moreSpecific reports whether a takes precedence over b. Both already match
// the same request and have the same segment count, so the first segment at
// which one is a literal and the other a {param} decides: the literal one
// wins, regardless of the segments before or after it.
func moreSpecific(a, b *route) bool {
	for i, seg := range a.segments {
		if seg.param == b.segments[i].param {
			continue
		}
		return !seg.param
	}
	return false
}

// normalizeKey reduces method and segments to the route's shape: method plus
// every segment, with a {param} collapsed to the same placeholder regardless
// of its name. Two patterns with the same shape — "GET /a/{x}" and
// "GET /a/{y}" — match exactly the same requests, so HandleFunc keys its
// duplicate check on this rather than the raw pattern string: keying on the
// string alone would let such a pair both register, with the first
// registered always winning and the second silently unreachable.
func normalizeKey(method string, segments []routeSegment) string {
	var sb strings.Builder
	sb.WriteString(method)
	for _, seg := range segments {
		sb.WriteByte('/')
		if seg.param {
			sb.WriteString("{}")
		} else {
			sb.WriteString(seg.literal)
		}
	}
	return sb.String()
}

// NewRouter returns a router that implements every generated package's
// ServeMux interface. Each pattern the generated server registers is looked
// up in Doc; the handler is wrapped: rate limit (if Limits has the
// operationId) → Access.Check(rule) → Reject on ErrUnauthenticated/
// ErrForbidden → the request-body cap → the generated wrapper with the
// Principal in the context.
func NewRouter(o RouterOptions) *Router {
	r := &Router{
		opts:         o,
		routes:       map[string][]*route{},
		ops:          map[string]*openapi3.Operation{},
		operationIDs: map[string]bool{},
		registered:   map[string]bool{},
		normalized:   map[string]string{},
	}
	if o.Doc != nil && o.Doc.Paths != nil {
		for path, item := range o.Doc.Paths.Map() {
			for method, op := range item.Operations() {
				r.ops[method+" "+path] = op
				if op.OperationID != "" {
					r.operationIDs[op.OperationID] = true
				}
			}
		}
	}
	return r
}

// HandleFunc registers h for pattern, exactly as StdHTTPServerOptions.BaseRouter
// does. pattern must be the contract's method and path ("METHOD /path",
// oapi-codegen's BaseURL is always ""); anything else — including a pattern
// whose route shape (see normalizeKey) was already registered, whether by
// the identical pattern or one differing only in a {param}'s name — is
// recorded as a problem (see Err) and not mounted.
func (r *Router) HandleFunc(pattern string, h func(http.ResponseWriter, *http.Request)) {
	op, ok := r.ops[pattern]
	if !ok {
		r.problems = append(r.problems, fmt.Sprintf("module: pattern %q matches no contract operation", pattern))
		return
	}

	method, path, _ := strings.Cut(pattern, " ") // pattern is a key of r.ops, so it always has the "METHOD /path" shape NewRouter built
	segments := splitSegments(path)
	key := normalizeKey(method, segments)
	if other, dup := r.normalized[key]; dup {
		r.problems = append(r.problems, fmt.Sprintf("module: pattern %q has the same route as already-registered %q", pattern, other))
		return
	}

	access, _ := op.Extensions["x-vantigo-access"].(string)
	rule, err := contracts.ParseRule(access)
	if err != nil {
		r.problems = append(r.problems, fmt.Sprintf("module: %s (%s): x-vantigo-access %q is invalid: %v", pattern, op.OperationID, access, err))
		return
	}
	if rule.Kind == contracts.RulePermission {
		for _, key := range rule.Names {
			if _, ok := r.opts.Catalog[key]; !ok {
				r.problems = append(r.problems, fmt.Sprintf("module: %s (%s): permission %q is not in the catalog", pattern, op.OperationID, key))
				return
			}
		}
	}

	r.routes[method] = append(r.routes[method], &route{
		segments: segments,
		handler:  r.wrap(op, rule, r.bodyLimit(op.OperationID), h),
	})
	r.normalized[key] = pattern
	r.registered[pattern] = true
}

// bodyLimit is the request-body cap for operationID: its BodyLimits entry,
// else MaxBodyBytes, else DefaultMaxBodyBytes.
func (r *Router) bodyLimit(operationID string) int64 {
	if n, ok := r.opts.BodyLimits[operationID]; ok {
		return n
	}
	if r.opts.MaxBodyBytes > 0 {
		return r.opts.MaxBodyBytes
	}
	return DefaultMaxBodyBytes
}

// wrap orders the checks: rate limit (if opts.Limits names op's
// operationId) before Access.Check, Access.Reject on
// ErrUnauthenticated/ErrForbidden, httpx.WriteError on any other Access
// error, then the body cap, and finally h with the Principal attached to
// the request context.
//
// The cap is an http.MaxBytesReader of maxBody bytes over the body, put in
// place only once the rate limit and Check have passed, so neither ever
// sees a wrapped body and nothing reads a body for a request they refuse.
// It is the only bound on what the generated wrapper's decoder buffers: a
// read past it fails with *http.MaxBytesError, which the generated
// wrapper hands to the module's RequestErrorHandlerFunc like any other
// undecodable body (DecodeError: the operation's own documented 400).
func (r *Router) wrap(op *openapi3.Operation, rule contracts.Rule, maxBody int64, h func(http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if policy, ok := r.opts.Limits[op.OperationID]; ok {
			d, err := r.opts.Limiter.Hit(req.Context(), policy, httpx.ClientIP(req))
			if err != nil {
				httpx.WriteError(w, req, err)
				return
			}
			if !d.Allowed {
				ratelimit.Reject(w, req, policy, d)
				return
			}
		}

		p, err := r.opts.Access.Check(req, rule)
		if err != nil {
			if errors.Is(err, contracts.ErrUnauthenticated) || errors.Is(err, contracts.ErrForbidden) {
				r.opts.Access.Reject(w, req, rule, err)
				return
			}
			httpx.WriteError(w, req, err)
			return
		}

		if req.Body != nil && req.Body != http.NoBody {
			req.Body = http.MaxBytesReader(w, req.Body, maxBody)
		}
		// The principal goes on the request first, then the request itself
		// goes on the context: a handler that pulls the request back out
		// finds the principal on it, as it would on the request it was
		// dispatched with.
		req = req.WithContext(contracts.WithPrincipal(req.Context(), p))
		h(w, req.WithContext(contracts.WithRequest(req.Context(), w, req)))
	}
}

// ServeHTTP dispatches to the handler of the most specific route registered
// for req's method and path (see match), falling back to a route registered
// for GET when req's method is HEAD — the same built-in match stdlib
// http.ServeMux makes. The contract documents no 405: an unknown path and a
// wrong method on a known one both answer the same 404 problem httpx.NotFound
// writes everywhere else, never a bare stdlib default. There is no trailing-
// slash redirect and no path cleaning — the contract has no route that needs
// either — so a path that doesn't match any route exactly falls straight
// through to that same 404, including a trailing slash or a doubled "//"
// that would otherwise need an empty segment to bind to a {param} (matches
// refuses that).
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	segments := decodeSegments(req.URL.EscapedPath())

	rt := match(r.routes[req.Method], segments)
	if rt == nil && req.Method == http.MethodHead {
		rt = match(r.routes[http.MethodGet], segments)
	}
	if rt == nil {
		httpx.NotFound(w, req)
		return
	}

	for i, seg := range rt.segments {
		if seg.param {
			req.SetPathValue(seg.name, segments[i])
		}
	}
	rt.handler(w, req)
}

// Err reports every registration problem: a nil Access, a nil Limiter when
// Limits is non-empty, a pattern with no contract operation, an operation
// without a valid x-vantigo-access, a permission missing from Catalog, a
// route shape registered more than once (the identical pattern, or two
// patterns differing only in a {param}'s name), a Limits key naming no
// operation, and contract operations that were never registered. Call it
// after every HandleFunc call (HandlerWithOptions registers everything in
// one call, so Mount calls Err right after it).
func (r *Router) Err() error {
	var problems []string
	if r.opts.Access == nil {
		problems = append(problems, "module: RouterOptions.Access is nil")
	}
	if r.opts.Limiter == nil && len(r.opts.Limits) > 0 {
		problems = append(problems, "module: RouterOptions.Limits is set but RouterOptions.Limiter is nil")
	}
	problems = append(problems, r.problems...)

	var unregistered []string
	for pattern, op := range r.ops {
		if !r.registered[pattern] {
			unregistered = append(unregistered, fmt.Sprintf("module: contract operation %s (%s) was never registered", pattern, op.OperationID))
		}
	}
	sort.Strings(unregistered)
	problems = append(problems, unregistered...)

	var badLimits []string
	for id := range r.opts.Limits {
		if !r.operationIDs[id] {
			badLimits = append(badLimits, fmt.Sprintf("module: Limits names unknown operation %q", id))
		}
	}
	if r.opts.MaxBodyBytes < 0 {
		badLimits = append(badLimits, "module: RouterOptions.MaxBodyBytes is negative")
	}
	for id, n := range r.opts.BodyLimits {
		if !r.operationIDs[id] {
			badLimits = append(badLimits, fmt.Sprintf("module: BodyLimits names unknown operation %q", id))
		}
		if n <= 0 {
			badLimits = append(badLimits, fmt.Sprintf("module: BodyLimits[%q] is not positive", id))
		}
	}
	sort.Strings(badLimits)
	problems = append(problems, badLimits...)

	if len(problems) == 0 {
		return nil
	}
	errs := make([]error, len(problems))
	for i, p := range problems {
		errs[i] = errors.New(p)
	}
	return errors.Join(errs...)
}
