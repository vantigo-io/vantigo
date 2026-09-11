package module

import (
	"errors"
	"fmt"
	"net/http"
	"sort"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/httpx"
	"github.com/vantigo-io/vantigo/server/internal/ratelimit"
)

// RouterOptions configures a Router.
type RouterOptions struct {
	Doc     *openapi3.T
	Access  contracts.Access
	Limiter *ratelimit.Limiter
	Limits  map[string]ratelimit.Policy // operationId → policy
	Catalog map[string]contracts.Permission
}

// Router implements every generated package's ServeMux interface
// (HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request))
// plus http.Handler). It is the BaseRouter every module's
// HandlerWithOptions mounts on.
type Router struct {
	opts RouterOptions
	mux  *http.ServeMux

	ops          map[string]*openapi3.Operation // "METHOD /path" → operation, from opts.Doc
	operationIDs map[string]bool                // every known operationId, from opts.Doc
	registered   map[string]bool                // "METHOD /path" → HandleFunc found a contract operation for it

	problems []string // recorded as HandleFunc runs; Err adds the two checks it can only make once registration is done
}

// NewRouter returns a router that implements every generated package's
// ServeMux interface. Each pattern the generated server registers is looked
// up in Doc; the handler is wrapped: rate limit (if Limits has the
// operationId) → Access.Check(rule) → Reject on ErrUnauthenticated/
// ErrForbidden → the generated wrapper with the Principal in the context.
func NewRouter(o RouterOptions) *Router {
	r := &Router{
		opts:         o,
		mux:          http.NewServeMux(),
		ops:          map[string]*openapi3.Operation{},
		operationIDs: map[string]bool{},
		registered:   map[string]bool{},
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
	// The contract documents no 405: an unknown path and a wrong method on a
	// known one both answer the same 404 problem, never the inner
	// http.ServeMux's plain-text default. This is the least specific
	// pattern, so it only ever catches what nothing else matched — including
	// a GET pattern's built-in HEAD match, which stdlib ServeMux still
	// resolves first.
	r.mux.HandleFunc("/", httpx.NotFound)
	return r
}

// HandleFunc registers h for pattern, exactly as StdHTTPServerOptions.BaseRouter
// does. pattern must be the contract's method and path ("METHOD /path",
// oapi-codegen's BaseURL is always ""); anything else is recorded as a
// problem (see Err) and not mounted, rather than panicking.
func (r *Router) HandleFunc(pattern string, h func(http.ResponseWriter, *http.Request)) {
	op, ok := r.ops[pattern]
	if !ok {
		r.problems = append(r.problems, fmt.Sprintf("module: pattern %q matches no contract operation", pattern))
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

	if r.mount(pattern, r.wrap(op, rule, h)) {
		r.registered[pattern] = true
	}
}

// mount registers h for pattern on the inner mux, recovering from the panic
// http.ServeMux.HandleFunc raises on a duplicate or ambiguous route (real
// contracts have these — see openapi.knownServeMuxConflicts — and letting
// one crash Mount/Compose would take the whole process down). A recovered
// pattern is recorded as a problem and left unregistered, so it also shows
// up under Err's "never registered" check.
func (r *Router) mount(pattern string, h http.HandlerFunc) (ok bool) {
	defer func() {
		if v := recover(); v != nil {
			r.problems = append(r.problems, fmt.Sprintf("module: %s: conflicts with an existing route: %v", pattern, v))
			ok = false
		}
	}()
	r.mux.HandleFunc(pattern, h)
	return true
}

// wrap orders the checks: rate limit (if opts.Limits names op's
// operationId) before Access.Check, Access.Reject on
// ErrUnauthenticated/ErrForbidden, httpx.WriteError on any other Access
// error, and finally h with the Principal attached to the request context.
func (r *Router) wrap(op *openapi3.Operation, rule contracts.Rule, h func(http.ResponseWriter, *http.Request)) http.HandlerFunc {
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

		h(w, req.WithContext(contracts.WithPrincipal(req.Context(), p)))
	}
}

// ServeHTTP dispatches to the handlers HandleFunc registered.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mux.ServeHTTP(w, req)
}

// Err reports every registration problem: a nil Access, a nil Limiter when
// Limits is non-empty, a pattern with no contract operation, an operation
// without a valid x-vantigo-access, a permission missing from Catalog, a
// route that conflicts with one already registered, a Limits key naming no
// operation, and contract operations that were never registered (a
// conflicting route counts as never registered). Call it after every
// HandleFunc call (HandlerWithOptions registers everything in one call, so
// Mount calls Err right after it).
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
