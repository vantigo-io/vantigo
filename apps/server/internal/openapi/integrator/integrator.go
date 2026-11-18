// Package integrator builds the integrator's document (API access design K28,
// K37): the merged contract cut down to the operations an integration's API
// key can call, with what an integrator needs to call them and none of the
// x-vantigo-* extensions the server reads.
//
// It lives beside internal/openapi rather than in it because it reads the
// access rules and the permission catalog through internal/contracts, and
// internal/contracts already depends on internal/openapi (through
// internal/config). It takes the catalog as a parameter, as K37 says, since
// whether a permission may be delegated is declared in each module's Go code.
package integrator

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// Filter cuts the document down to one key's scope: the permissions the key
// reaches (its integration's effective permissions less APINever, within its
// narrowing) and whether it only reads.
type Filter struct {
	Permissions []string
	ReadOnly    bool
}

// The links in info.description. They point at the public documentation,
// which every version's document can name, so the document is the same on
// every installation and in CI.
const (
	CompatibilityURL = "https://docs.vantigo.io/en/reference/api-compatibility/"
	APIAccessURL     = "https://docs.vantigo.io/en/admin/api-access/"
)

// BearerScheme is the security scheme the document declares.
const BearerScheme = "bearerAuth"

// The response components the document adds (K19's codes), and their body.
const (
	unauthenticatedName = "Unauthenticated"
	forbiddenName       = "Forbidden"
	rateLimitedName     = "RateLimited"
	authErrorSchema     = "AuthErrorResponse"
)

// Build answers the integrator's document for contract, the merged contract
// served at /api/openapi.json. contract is not changed.
//
// It keeps every operation whose x-vantigo-access is a permission or the
// anonymous rule, that is not marked x-vantigo-api: never, and that names no
// permission in contracts.APINever(catalog) nor one the catalog lacks: a key
// can call no other (K5, K7). With a filter it also drops a permission
// operation naming a key the filter does not list, and, for a read-only key,
// every operation that is not a read (a GET, a HEAD, or one marked
// x-vantigo-api: read), anonymous ones included, as identity's Check refuses
// them. An anonymous operation needs no permission, so the permission filter
// keeps it.
//
// To what it keeps it adds the bearerAuth scheme and a top-level security
// naming it (security: [] on an anonymous operation), the 401, 403 and 429
// answers with K19's codes, "Requires `key`" in each permission operation's
// description, and an info.description linking the compatibility policy and
// the page that makes keys. It strips every x-vantigo-* extension, and drops
// every schema, response, parameter, request body and header no kept
// operation refers to, directly or through another component. servers is
// the caller's: the composition sets APP_URL.
func Build(contract *openapi3.T, catalog []contracts.Permission, filter *Filter) (*openapi3.T, error) {
	doc, err := clone(contract)
	if err != nil {
		return nil, err
	}

	known := make(map[string]bool, len(catalog))
	for _, p := range catalog {
		known[p.Key] = true
	}
	never := contracts.APINever(catalog)

	if doc.Paths == nil {
		doc.Paths = openapi3.NewPaths()
	}
	for _, path := range doc.Paths.InMatchingOrder() {
		item := doc.Paths.Value(path)
		for method, op := range item.Operations() {
			rule, read, err := judge(op, method)
			if err != nil {
				return nil, fmt.Errorf("integrator: %s %s: %w", method, path, err)
			}
			if rule == nil || !callable(*rule, known, never) || !admitted(*rule, read, filter) {
				item.SetOperation(method, nil)
				continue
			}
			decorate(op, *rule)
		}
		if len(item.Operations()) == 0 {
			doc.Paths.Delete(path)
		}
	}

	addComponents(doc)
	doc.Security = openapi3.SecurityRequirements{{BearerScheme: []string{}}}
	if doc.Info == nil {
		doc.Info = &openapi3.Info{Title: "Vantigo API", Version: "1"}
	}
	doc.Info.Description = "The operations an integration's API key can call. Send the key as " +
		"`Authorization: Bearer vtg_…`. An Owner makes keys under Workspace admin › API access " +
		"([how](" + APIAccessURL + ")). Within /api/v1 every change is additive; the " +
		"[compatibility policy](" + CompatibilityURL + ") says what that promises."

	return strip(doc)
}

// judge parses op's access rule and API mark. It answers a nil rule for an
// operation no key can call: any rule but a permission or the anonymous one,
// or the never mark. read reports whether a read-only key may call it.
func judge(op *openapi3.Operation, method string) (*contracts.Rule, bool, error) {
	access, _ := op.Extensions["x-vantigo-access"].(string)
	rule, err := contracts.ParseRule(access)
	if err != nil {
		return nil, false, fmt.Errorf("x-vantigo-access %q: %w", access, err)
	}
	if rule.Kind != contracts.RulePermission && rule.Kind != contracts.RuleAnonymous {
		return nil, false, nil
	}
	read := method == http.MethodGet || method == http.MethodHead
	if v, present := op.Extensions[contracts.APIExtension]; present {
		switch v {
		case contracts.APIExtensionNever:
			return nil, false, nil
		case contracts.APIExtensionRead:
			read = true
		case contracts.APIExtensionAllowed:
		default:
			return nil, false, fmt.Errorf("%s %v must be %q, %q or %q", contracts.APIExtension, v,
				contracts.APIExtensionAllowed, contracts.APIExtensionRead, contracts.APIExtensionNever)
		}
	}
	return &rule, read, nil
}

// callable reports whether some key could call an operation behind rule: it
// names no permission a key never holds (K7), nor one the catalog lacks,
// which identity treats the same way.
func callable(rule contracts.Rule, known map[string]bool, never []string) bool {
	for _, key := range rule.Names {
		if !known[key] || slices.Contains(never, key) {
			return false
		}
	}
	return true
}

// admitted reports whether filter's key may call the operation.
func admitted(rule contracts.Rule, read bool, filter *Filter) bool {
	if filter == nil {
		return true
	}
	if filter.ReadOnly && !read {
		return false
	}
	for _, key := range rule.Names {
		if !slices.Contains(filter.Permissions, key) {
			return false
		}
	}
	return true
}

// decorate adds what an integrator reads on op: the permission it needs, the
// security it takes and K19's answers.
func decorate(op *openapi3.Operation, rule contracts.Rule) {
	if rule.Kind == contracts.RuleAnonymous {
		op.Security = &openapi3.SecurityRequirements{}
	} else {
		keys := make([]string, len(rule.Names))
		for i, key := range rule.Names {
			keys[i] = "`" + key + "`"
		}
		requires := "*Requires " + strings.Join(keys, " and ") + "*"
		if op.Description == "" {
			op.Description = requires
		} else {
			op.Description = strings.TrimRight(op.Description, "\n") + "\n\n" + requires
		}
	}
	if op.Responses == nil {
		op.Responses = openapi3.NewResponses()
	}
	op.Responses.Set("401", &openapi3.ResponseRef{Ref: "#/components/responses/" + unauthenticatedName})
	op.Responses.Set("403", &openapi3.ResponseRef{Ref: "#/components/responses/" + forbiddenName})
	op.Responses.Set("429", &openapi3.ResponseRef{Ref: "#/components/responses/" + rateLimitedName})
}

// addComponents declares the bearer scheme, the error body and the three
// answers decorate refers to.
func addComponents(doc *openapi3.T) {
	if doc.Components == nil {
		doc.Components = &openapi3.Components{}
	}
	c := doc.Components
	if c.SecuritySchemes == nil {
		c.SecuritySchemes = openapi3.SecuritySchemes{}
	}
	c.SecuritySchemes[BearerScheme] = &openapi3.SecuritySchemeRef{Value: &openapi3.SecurityScheme{
		Type:         "http",
		Scheme:       "bearer",
		BearerFormat: "vtg_…",
		Description:  "An integration's API key, made by an Owner under Workspace admin › API access.",
	}}

	if c.Schemas == nil {
		c.Schemas = openapi3.Schemas{}
	}
	if _, ok := c.Schemas[authErrorSchema]; !ok {
		c.Schemas[authErrorSchema] = &openapi3.SchemaRef{Value: authError()}
	}
	body := func(description string) *openapi3.Response {
		return openapi3.NewResponse().
			WithDescription(description).
			WithContent(openapi3.NewContentWithJSONSchemaRef(&openapi3.SchemaRef{Ref: "#/components/schemas/" + authErrorSchema}))
	}
	header := func(description string, schema *openapi3.Schema) *openapi3.HeaderRef {
		return &openapi3.HeaderRef{Value: &openapi3.Header{Parameter: openapi3.Parameter{
			Description: description,
			Schema:      &openapi3.SchemaRef{Value: schema},
		}}}
	}

	unauthenticated := body("Unauthorized. `error.code` is `unauthenticated` when the key is malformed, " +
		"unknown, revoked, disabled or archived, when the request comes from outside the key's networks, " +
		"or when it carries several Authorization headers; and `api_key_expired` when the key has expired.")
	unauthenticated.Headers = openapi3.Headers{
		"WWW-Authenticate": header(`Bearer realm="vantigo"`, openapi3.NewStringSchema()),
	}
	forbidden := body("Forbidden. `error.code` is `forbidden` when the key does not reach the permission " +
		"the operation requires, `api_not_available` when no key may call the operation, and " +
		"`api_key_read_only` when a read-only key calls an operation that is not a read.")
	rateLimited := body("Too Many Requests. `error.code` is `rate_limited`: the integration has used " +
		"every request its limit allows this minute, across all of its keys.")
	rateLimited.Headers = openapi3.Headers{
		"Retry-After":         header("Seconds until a request may be sent again.", openapi3.NewIntegerSchema()),
		"RateLimit-Limit":     header("The requests the integration may send per minute.", openapi3.NewIntegerSchema()),
		"RateLimit-Remaining": header("The requests left in this minute.", openapi3.NewIntegerSchema()),
		"RateLimit-Reset":     header("Seconds until the minute ends.", openapi3.NewIntegerSchema()),
	}

	if c.Responses == nil {
		c.Responses = openapi3.ResponseBodies{}
	}
	c.Responses[unauthenticatedName] = &openapi3.ResponseRef{Value: unauthenticated}
	c.Responses[forbiddenName] = &openapi3.ResponseRef{Value: forbidden}
	c.Responses[rateLimitedName] = &openapi3.ResponseRef{Value: rateLimited}
}

// authError is common.yaml's AuthErrorResponse, for a contract that has none.
func authError() *openapi3.Schema {
	fields := openapi3.NewObjectSchema().WithAdditionalProperties(openapi3.NewArraySchema().WithItems(openapi3.NewStringSchema()))
	fields.Nullable = true
	inner := openapi3.NewObjectSchema().
		WithProperty("code", openapi3.NewStringSchema()).
		WithProperty("message", openapi3.NewStringSchema()).
		WithPropertyRef("fields", &openapi3.SchemaRef{Value: fields})
	inner.Required = []string{"code", "message"}
	outer := openapi3.NewObjectSchema().WithProperty("error", inner)
	outer.Required = []string{"error"}
	return outer
}

// clone answers a document of the caller's own, through JSON, so Build never
// writes to the contract it was given.
func clone(contract *openapi3.T) (*openapi3.T, error) {
	data, err := json.Marshal(contract)
	if err != nil {
		return nil, fmt.Errorf("integrator: marshal the contract: %w", err)
	}
	return load(data)
}

func load(data []byte) (*openapi3.T, error) {
	loader := openapi3.NewLoader()
	loader.Context = context.Background()
	doc, err := loader.LoadFromData(data)
	if err != nil {
		return nil, fmt.Errorf("integrator: load the document: %w", err)
	}
	return doc, nil
}

// strip removes every x-vantigo-* key wherever it appears: they are the
// server's own reading of the contract (access rules, marks, sources) and
// none of them is for an integrator. A schema property or an example key
// named x-vantigo-* would go too; the contract has none.
func strip(doc *openapi3.T) (*openapi3.T, error) {
	data, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("integrator: marshal the document: %w", err)
	}
	var generic any
	if err := json.Unmarshal(data, &generic); err != nil {
		return nil, fmt.Errorf("integrator: read the document: %w", err)
	}
	generic = stripValue(generic)
	prune(generic)
	if data, err = json.Marshal(generic); err != nil {
		return nil, fmt.Errorf("integrator: marshal the document: %w", err)
	}
	return load(data)
}

// prunedKinds are the components prune drops when nothing kept refers to
// them. Security schemes, examples, links and callbacks stay as they are.
var prunedKinds = []string{"schemas", "responses", "parameters", "requestBodies", "headers"}

// prune deletes every component of prunedKinds that no kept operation
// reaches: it follows $ref transitively from everything outside components
// (the kept paths, with the responses decorate added), so a schema only a
// dropped operation used does not reveal itself to a key that cannot call
// that operation.
func prune(doc any) {
	root, ok := doc.(map[string]any)
	if !ok {
		return
	}
	components, _ := root["components"].(map[string]any)
	if components == nil {
		return
	}
	reached := map[string]bool{} // "kind/name"
	var queue []string
	var walk func(v any)
	walk = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			if ref, ok := v["$ref"].(string); ok {
				if rest, ok := strings.CutPrefix(ref, "#/components/"); ok {
					kind, name, _ := strings.Cut(rest, "/")
					key := kind + "/" + strings.NewReplacer("~1", "/", "~0", "~").Replace(name)
					if !reached[key] {
						reached[key] = true
						queue = append(queue, key)
					}
				}
			}
			for _, value := range v {
				walk(value)
			}
		case []any:
			for _, value := range v {
				walk(value)
			}
		}
	}
	for key, value := range root {
		if key != "components" {
			walk(value)
		}
	}
	if schemes, ok := components["securitySchemes"].(map[string]any); ok {
		walk(schemes)
	}
	for len(queue) > 0 {
		key := queue[0]
		queue = queue[1:]
		kind, name, _ := strings.Cut(key, "/")
		if entries, ok := components[kind].(map[string]any); ok {
			walk(entries[name])
		}
	}
	for _, kind := range prunedKinds {
		entries, ok := components[kind].(map[string]any)
		if !ok {
			continue
		}
		for name := range entries {
			if !reached[kind+"/"+name] {
				delete(entries, name)
			}
		}
		if len(entries) == 0 {
			delete(components, kind)
		}
	}
}

func stripValue(v any) any {
	switch v := v.(type) {
	case map[string]any:
		for key, value := range v {
			if strings.HasPrefix(key, "x-vantigo-") {
				delete(v, key)
				continue
			}
			v[key] = stripValue(value)
		}
	case []any:
		for i, value := range v {
			v[i] = stripValue(value)
		}
	}
	return v
}
