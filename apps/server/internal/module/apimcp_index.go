package module

import (
	"container/list"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/getkin/kin-openapi/openapi3"
	"golang.org/x/sync/singleflight"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/openapi/integrator"
	"github.com/vantigo-io/vantigo/server/internal/operations"
)

// The MCP endpoint's catalogue (MCP server design M10, M23, M24): for each key
// scope, the operations of the integrator's document cut to it, parsed once
// into what the tools read, and kept in an LRU of its own, so no request
// parses a document.

// mcpIndexes is how many scopes' catalogues are kept, as filteredDocuments.
const mcpIndexes = 64

// The kinds of operation (M24): a read the read tool calls, and a write.
const (
	mcpKindRead  = "read"
	mcpKindWrite = "write"
)

// mcpReadPosts are the POST operations marked x-vantigo-api: read that MCP's
// read tool may call (MCP server design M35): each reviewed to change nothing,
// call nothing outside Vantigo and answer only what the caller may already
// read. Every other POST marked read is a write to MCP — estimateShipping
// asks Bring, printInventoryLabels renders a label run, and
// postStaffPeopleSimilarErased matches against erased people — so a new one
// is not reachable in read mode until it is reviewed and listed here.
var mcpReadPosts = map[string]bool{
	"previewAccountingVoucher":                true, // expands a journal; writes nothing
	"previewAccountingBankCsv":                true, // parses an uploaded statement; writes nothing (multipart, so not in the catalogue)
	"postExpensesClaimsByIdPerDiemSuggestion": true, // prices a visible claim's days; writes nothing
	"resolveKnowledgeLinks":                   true, // titles of articles the caller may read
}

// mcpOperation is one operation of a catalogue.
type mcpOperation struct {
	ID, Method, Path, Summary, Description, Tag, Kind string

	params             []*openapi3.Parameter
	hasBody            bool
	bodyRequired       bool
	body               *openapi3.SchemaRef
	successStatus      string
	successDescription string
	success            *openapi3.SchemaRef

	searchSummary, searchTag, searchPath, searchDescription string
}

// mcpIndex is one scope's catalogue, in operation id order.
type mcpIndex struct {
	ops  []*mcpOperation
	byID map[string]*mcpOperation
}

// mcpCatalogue builds and keeps the catalogues of one composition.
type mcpCatalogue struct {
	docs *apiDocuments
	// kinds is every operation id of the unstripped contract a key could
	// call, with its kind by the router's own rule (setAPIRule).
	kinds func() (map[string]string, error)

	mu      sync.Mutex
	order   *list.List // of *mcpCatalogueEntry, the most recently used first
	entries map[string]*list.Element
	// building lets one caller build a scope's catalogue while every other
	// asking for the same scope waits for it, so a burst of first requests
	// costs one build, not one each.
	building singleflight.Group
}

type mcpCatalogueEntry struct {
	key   string
	index *mcpIndex
}

func newMCPCatalogue(docs *apiDocuments) *mcpCatalogue {
	c := &mcpCatalogue{docs: docs, order: list.New(), entries: map[string]*list.Element{}}
	c.kinds = sync.OnceValues(func() (map[string]string, error) {
		contract, err := docs.contract()
		if err != nil {
			return nil, err
		}
		return operationKinds(contract)
	})
	return c
}

// operationKinds is the kind of every operation of contract an API key could
// call: read when the router's rule makes it contracts.APIRead, write
// otherwise. Neither x-vantigo-* mark survives into the integrator's
// document, so the kind is read here, from the contract the router mounts.
func operationKinds(contract *openapi3.T) (map[string]string, error) {
	kinds := map[string]string{}
	if contract.Paths == nil {
		return kinds, nil
	}
	for path, item := range contract.Paths.Map() {
		for method, op := range item.Operations() {
			access, _ := op.Extensions["x-vantigo-access"].(string)
			rule, err := contracts.ParseRule(access)
			if err != nil {
				return nil, fmt.Errorf("module: %s %s: x-vantigo-access %q: %w", method, path, access, err)
			}
			if err := setAPIRule(method, op, &rule); err != nil {
				return nil, fmt.Errorf("module: %s %s: %w", method, path, err)
			}
			switch {
			case rule.API == contracts.APIRead && (method == http.MethodGet || method == http.MethodHead || mcpReadPosts[op.OperationID]):
				kinds[op.OperationID] = mcpKindRead
			case rule.API == contracts.APIRead, rule.API == contracts.APIAllowed:
				// A POST marked read for keys that is not in mcpReadPosts is a
				// write to MCP: it may act outside Vantigo or reveal more than
				// an agent should reach by reading.
				kinds[op.OperationID] = mcpKindWrite
			}
		}
	}
	return kinds, nil
}

// scoped answers the catalogue of a key that reaches permissions and, when
// readOnly, only reads: the operations of the integrator's document for that
// scope, from the LRU when it is there.
func (c *mcpCatalogue) scoped(permissions []string, readOnly bool) (*mcpIndex, error) {
	permissions = slices.Clone(permissions)
	slices.Sort(permissions)
	permissions = slices.Compact(permissions)
	key := strings.Join(permissions, " ")
	if readOnly {
		key += " read-only"
	}

	c.mu.Lock()
	if el, ok := c.entries[key]; ok {
		c.order.MoveToFront(el)
		index := el.Value.(*mcpCatalogueEntry).index
		c.mu.Unlock()
		return index, nil
	}
	c.mu.Unlock()

	// Built outside the lock, once per scope however many ask at once.
	v, err, _ := c.building.Do(key, func() (any, error) {
		index, err := c.build(&integrator.Filter{Permissions: permissions, ReadOnly: readOnly})
		if err != nil {
			return nil, err
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		if el, ok := c.entries[key]; ok {
			c.order.MoveToFront(el)
			return el.Value.(*mcpCatalogueEntry).index, nil
		}
		c.entries[key] = c.order.PushFront(&mcpCatalogueEntry{key: key, index: index})
		for c.order.Len() > mcpIndexes {
			oldest := c.order.Back()
			c.order.Remove(oldest)
			delete(c.entries, oldest.Value.(*mcpCatalogueEntry).key)
		}
		return index, nil
	})
	if err != nil {
		return nil, err
	}
	return v.(*mcpIndex), nil
}

// cached reports how many catalogues are kept, for the tests.
func (c *mcpCatalogue) cached() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.order.Len()
}

// build parses the integrator's document for filter into a catalogue. An
// operation whose request body is not JSON (a file upload) is left out: a
// tool's arguments are JSON.
func (c *mcpCatalogue) build(filter *integrator.Filter) (*mcpIndex, error) {
	kinds, err := c.kinds()
	if err != nil {
		return nil, err
	}
	contract, err := c.docs.contract()
	if err != nil {
		return nil, err
	}
	doc, err := integrator.Build(contract, c.docs.catalog, filter)
	if err != nil {
		return nil, err
	}
	index := &mcpIndex{byID: map[string]*mcpOperation{}}
	if doc.Paths != nil {
		for path, item := range doc.Paths.Map() {
			for method, op := range item.Operations() {
				kind, ok := kinds[op.OperationID]
				if !ok || op.OperationID == "" || !operations.JSONRequest(op) {
					continue
				}
				e := newMCPOperation(path, method, item, op, kind)
				index.ops = append(index.ops, e)
				index.byID[e.ID] = e
			}
		}
	}
	sort.Slice(index.ops, func(i, j int) bool { return index.ops[i].ID < index.ops[j].ID })
	return index, nil
}

// newMCPOperation reads what the tools need of one operation, as the
// operations dispatcher's entry does.
func newMCPOperation(path, method string, item *openapi3.PathItem, op *openapi3.Operation, kind string) *mcpOperation {
	e := &mcpOperation{
		ID: op.OperationID, Method: method, Path: path, Kind: kind,
		Summary: strings.TrimSpace(op.Summary), Description: strings.TrimSpace(op.Description),
	}
	if len(op.Tags) > 0 {
		e.Tag = op.Tags[0]
	}

	// Path-level parameters first; an operation's own override them.
	seen := map[string]int{}
	for _, ref := range append(append(openapi3.Parameters{}, item.Parameters...), op.Parameters...) {
		if ref == nil || ref.Value == nil {
			continue
		}
		p := ref.Value
		key := p.In + ":" + strings.ToLower(p.Name)
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
			if operations.IsJSON(contentType) && media != nil {
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
				if operations.IsJSON(contentType) && media != nil {
					schema = media.Schema
					break
				}
			}
			if e.successStatus == "" || (schema != nil && e.success == nil) {
				e.successStatus, e.success = code, schema
				if ref.Value.Description != nil {
					e.successDescription = operations.Shorten(*ref.Value.Description)
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
