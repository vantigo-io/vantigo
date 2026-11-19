package module

import (
	"container/list"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/httpx"
	"github.com/vantigo-io/vantigo/server/internal/openapi/integrator"
	"github.com/vantigo-io/vantigo/server/internal/ratelimit"
)

// documentRule is GET /api/openapi.json's own check (API access design K28):
// anonymous, so a key passes as well as a session, and a read, so a read-only
// key does too (with APIAllowed identity refuses it api_key_read_only). A
// principal with no user is refused by the handler.
var documentRule = contracts.Rule{Kind: contracts.RuleAnonymous, API: contracts.APIRead}

// documentOperationID names GET /api/openapi.json in the request log, which
// records an operation, never a path. No contract declares the endpoint, so
// the name is the one the contract's convention would give it.
const documentOperationID = "getApiOpenapiJson"

// filteredDocuments is how many key scopes' documents are kept (K46).
const filteredDocuments = 64

// apiDocuments serves GET /api/openapi.json for one composition: the merged
// contract to a session, the integrator's document to a session asking
// ?view=api, and the integrator's document cut to its scope to an API key
// (K28, K36). Each is built once; the filtered ones are kept in an LRU keyed
// by the sorted permission set and the read-only flag.
type apiDocuments struct {
	access  contracts.Access
	keys    keyGate
	full    []byte
	catalog []contracts.Permission
	server  string

	contract   func() (*openapi3.T, error)
	integrator func() ([]byte, error)

	mu       sync.Mutex
	order    *list.List // of *filteredDocument, the most recently used first
	filtered map[string]*list.Element
}

type filteredDocument struct {
	key  string
	body []byte
}

// newAPIDocuments serves full, the merged contract, with catalog the composed
// permission catalog and server the document's servers URL (APP_URL; "" keeps
// the contract's own).
//
// An API key's request goes through the same keyGate as the router's: it is
// counted against its integration's rate limit and recorded in the request
// log, refusals included.
func newAPIDocuments(access contracts.Access, limiter *ratelimit.Limiter, full []byte, catalog map[string]contracts.Permission, server string) *apiDocuments {
	perms := make([]contracts.Permission, 0, len(catalog))
	for _, p := range catalog {
		perms = append(perms, p)
	}
	slices.SortFunc(perms, func(a, b contracts.Permission) int { return strings.Compare(a.Key, b.Key) })
	d := &apiDocuments{
		access:   access,
		keys:     newKeyGate(access, limiter),
		full:     full,
		catalog:  perms,
		server:   server,
		order:    list.New(),
		filtered: map[string]*list.Element{},
	}
	d.contract = sync.OnceValues(func() (*openapi3.T, error) {
		doc, err := openapi3.NewLoader().LoadFromData(full)
		if err != nil {
			return nil, fmt.Errorf("module: load the combined contract: %w", err)
		}
		return doc, nil
	})
	d.integrator = sync.OnceValues(func() ([]byte, error) { return d.build(nil) })
	return d
}

// build answers the integrator's document for filter (nil for every
// operation a key can call).
func (d *apiDocuments) build(filter *integrator.Filter) ([]byte, error) {
	contract, err := d.contract()
	if err != nil {
		return nil, err
	}
	doc, err := integrator.Build(contract, d.catalog, filter)
	if err != nil {
		return nil, err
	}
	if d.server != "" {
		doc.Servers = openapi3.Servers{{URL: d.server}}
	}
	body, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("module: marshal the integrator's document: %w", err)
	}
	return body, nil
}

// scoped answers the document for one key's scope, from the LRU when it is
// there.
func (d *apiDocuments) scoped(permissions []string, readOnly bool) ([]byte, error) {
	permissions = slices.Clone(permissions)
	slices.Sort(permissions)
	permissions = slices.Compact(permissions)
	key := strings.Join(permissions, " ")
	if readOnly {
		key += " read-only"
	}

	d.mu.Lock()
	if el, ok := d.filtered[key]; ok {
		d.order.MoveToFront(el)
		body := el.Value.(*filteredDocument).body
		d.mu.Unlock()
		return body, nil
	}
	d.mu.Unlock()

	// Built outside the lock: two first callers may both build it, and the
	// one stored last wins, which is the same document.
	body, err := d.build(&integrator.Filter{Permissions: permissions, ReadOnly: readOnly})
	if err != nil {
		return nil, err
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if el, ok := d.filtered[key]; ok {
		d.order.MoveToFront(el)
		return body, nil
	}
	d.filtered[key] = d.order.PushFront(&filteredDocument{key: key, body: body})
	for d.order.Len() > filteredDocuments {
		oldest := d.order.Back()
		d.order.Remove(oldest)
		delete(d.filtered, oldest.Value.(*filteredDocument).key)
	}
	return body, nil
}

// cached reports how many filtered documents are kept, for the tests.
func (d *apiDocuments) cached() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.order.Len()
}

func (d *apiDocuments) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	// The document is the caller's own (a key's scope, a person's view), so
	// no shared cache may keep it or answer it to someone else.
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Add("Vary", "Authorization, Cookie")

	p, err := d.access.Check(r, documentRule)
	if err == nil && p.UserID == uuid.Nil {
		// No credentials at all, or a SCIM token: neither is anyone the
		// document is for.
		err = contracts.ErrUnauthenticated
	}
	if err != nil {
		if errors.Is(err, contracts.ErrUnauthenticated) || errors.Is(err, contracts.ErrForbidden) {
			d.keys.refuse(w, r, d.access, documentRule, err, documentOperationID, start)
			return
		}
		httpx.WriteError(w, r, err)
		return
	}

	if p.Via != nil && p.Via.Kind == contracts.ViaAPIKey {
		d.keys.serve(w, r, p, documentOperationID, start, func(w http.ResponseWriter, _ *http.Request) { d.serveKey(w, r, p) })
		return
	}
	body := d.full
	if r.URL.Query().Get("view") == "api" {
		if body, err = d.integrator(); err != nil {
			httpx.WriteError(w, r, err)
			return
		}
	}
	writeDocument(w, body)
}

// serveKey answers an admitted key the integrator's document cut to what
// contracts.APIKeyScope says it reaches.
func (d *apiDocuments) serveKey(w http.ResponseWriter, r *http.Request, p contracts.Principal) {
	scope, ok := d.access.(contracts.APIKeyScope)
	if !ok {
		httpx.WriteError(w, r, errors.New("module: an API key was admitted by an Access that has no APIKeyScope"))
		return
	}
	permissions, readOnly, err := scope.Scope(r.Context(), p)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	body, err := d.scoped(permissions, readOnly)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeDocument(w, body)
}

func writeDocument(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

// IntegratorDocument answers the integrator's document, unfiltered, for mods
// with every one enabled, built from the embedded contracts and the catalog
// the modules declare: what `vantigo openapi integrator` writes for CI's
// breaking-change check (API access design K37). servers is the contract's
// own, since no installation is named.
func IntegratorDocument(mods ...Module) ([]byte, error) {
	catalog := make(map[string]contracts.Permission)
	order := make([]string, 0, len(mods))
	for _, mod := range mods {
		if slices.Contains(order, mod.Name) {
			return nil, fmt.Errorf("module: duplicate module name %q", mod.Name)
		}
		order = append(order, mod.Name)
		for _, perm := range mod.Permissions {
			if err := contracts.ValidatePermission(mod.Name, perm); err != nil {
				return nil, fmt.Errorf("module: %w", err)
			}
			if _, ok := catalog[perm.Key]; ok {
				return nil, fmt.Errorf("module: duplicate permission %q", perm.Key)
			}
			catalog[perm.Key] = perm
		}
	}
	full, err := embedded.combined(context.Background(), order)
	if err != nil {
		return nil, err
	}
	return newAPIDocuments(nil, nil, full, catalog, "").integrator()
}
