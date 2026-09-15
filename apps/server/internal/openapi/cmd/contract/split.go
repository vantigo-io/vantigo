package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/oasdiff/yaml"

	"github.com/vantigo-io/vantigo/server/internal/openapi"
)

// modules are the contract files that carry paths, the one list
// internal/openapi owns.
var modules = openapi.Modules

// dropped are removed while splitting: the Go port has no tenancy, no
// antiforgery token endpoint and no inbound Mailgun webhook.
var dropped = []*regexp.Regexp{
	regexp.MustCompile(`^/api/v1/identity/admin/tenants(/|$)`),
	regexp.MustCompile(`^/api/v1/identity/tenants/current/capabilities$`),
	regexp.MustCompile(`^/api/v1/identity/session/tenant$`),
	regexp.MustCompile(`^/api/v1/identity/antiforgery$`),
	regexp.MustCompile(`^/api/v1/communications/inbound/mailgun/`),
}

var modulePath = regexp.MustCompile(`^/api/v1/([a-z-]+)(/|$)`)

const localRef = "#/components/schemas/"

type obj = map[string]any

// split turns the .NET dump into common.yaml plus one file per module.
func split(dump []byte) (map[string]string, error) {
	var doc obj
	if err := json.Unmarshal(dump, &doc); err != nil {
		return nil, fmt.Errorf("parse dump: %w", err)
	}
	// Restore what the 3.1 -> 3.0 downgrade lost, so a future re-split stays
	// right: numeric `type` and the pointer-shaped nullable-ref idiom.
	normalizeNumbers(doc)
	normalizeNullableRefs(doc)
	schemas, _ := dig(doc, "components", "schemas").(obj)

	moduleOf := map[string]string{}
	paths := map[string]obj{}
	for _, m := range modules {
		paths[m] = obj{}
	}
	for p, item := range asObj(doc["paths"]) {
		if isDropped(p) {
			continue
		}
		match := modulePath.FindStringSubmatch(p)
		if match == nil || paths[match[1]] == nil {
			return nil, fmt.Errorf("path %s belongs to no known module", p)
		}
		paths[match[1]][p] = asObj(item)
	}

	// Which modules reference each schema (transitively)?
	users := map[string]map[string]bool{}
	for _, m := range modules {
		for name := range closure(paths[m], schemas) {
			if users[name] == nil {
				users[name] = map[string]bool{}
			}
			users[name][m] = true
		}
	}
	for name, schema := range schemas {
		switch {
		case len(users[name]) > 1:
			moduleOf[name] = "common"
		case len(users[name]) == 1:
			for m := range users[name] {
				moduleOf[name] = m
			}
		default:
			moduleOf[name] = sourceModule(asObj(schema))
		}
		if fromContracts(asObj(schema)) {
			moduleOf[name] = "common"
		}
	}
	// A schema a common schema depends on must be common too.
	for changed := true; changed; {
		changed = false
		for name, m := range moduleOf {
			if m != "common" {
				continue
			}
			for dep := range refsIn(schemas[name]) {
				if moduleOf[dep] != "common" {
					moduleOf[dep] = "common"
					changed = true
				}
			}
		}
	}

	files := map[string]string{}
	for _, target := range append([]string{"common"}, modules...) {
		own := obj{}
		for name, m := range moduleOf {
			if m == target {
				own[name] = rewrite(schemas[name], target, moduleOf)
			}
		}
		out := obj{
			"openapi": "3.0.3",
			"info":    obj{"title": title(target), "version": "1"},
			"servers": []any{obj{"url": "/"}},
			"paths":   obj{},
		}
		if target != "common" {
			rewritten := obj{}
			for p, item := range paths[target] {
				rewritten[p] = rewrite(item, target, moduleOf)
			}
			out["paths"] = rewritten
		}
		if len(own) > 0 {
			out["components"] = obj{"schemas": own}
		}
		data, err := json.Marshal(out)
		if err != nil {
			return nil, err
		}
		y, err := yaml.JSONToYAML(data)
		if err != nil {
			return nil, err
		}
		files[target+".yaml"] = string(y)
	}
	return files, nil
}

func isDropped(p string) bool {
	for _, re := range dropped {
		if re.MatchString(p) {
			return true
		}
	}
	return false
}

func title(module string) string {
	if module == "common" {
		return "Vantigo API — shared components"
	}
	return "Vantigo " + strings.ToUpper(module[:1]) + module[1:] + " API"
}

// fromContracts reports whether schema is sourced from Vantigo.Contracts,
// the .NET project shared by every module — such schemas always belong in
// common.yaml, regardless of how many modules currently reference them.
func fromContracts(schema obj) bool {
	source, _ := schema["x-vantigo-source"].(string)
	return source == "Vantigo.Contracts"
}

func sourceModule(schema obj) string {
	source, _ := schema["x-vantigo-source"].(string)
	name := strings.ToLower(strings.TrimPrefix(source, "Vantigo."))
	for _, m := range modules {
		if m == name {
			return m
		}
	}
	return "common"
}

// closure returns every schema name reachable from v.
func closure(v any, schemas obj) map[string]bool {
	seen := map[string]bool{}
	var walk func(any)
	walk = func(v any) {
		for name := range refsIn(v) {
			if !seen[name] {
				seen[name] = true
				walk(schemas[name])
			}
		}
	}
	walk(v)
	return seen
}

func refsIn(v any) map[string]bool {
	found := map[string]bool{}
	var walk func(any)
	walk = func(v any) {
		switch t := v.(type) {
		case obj:
			if ref, ok := t["$ref"].(string); ok && strings.HasPrefix(ref, localRef) {
				found[strings.TrimPrefix(ref, localRef)] = true
			}
			for _, child := range t {
				walk(child)
			}
		case []any:
			for _, child := range t {
				walk(child)
			}
		}
	}
	walk(v)
	return found
}

// rewrite deep-copies v, turning references to schemas that live in another
// file into cross-file references.
func rewrite(v any, target string, moduleOf map[string]string) any {
	switch t := v.(type) {
	case obj:
		out := obj{}
		for k, child := range t {
			if k == "$ref" {
				if ref, ok := child.(string); ok && strings.HasPrefix(ref, localRef) {
					name := strings.TrimPrefix(ref, localRef)
					if home := moduleOf[name]; home != target {
						child = home + ".yaml" + localRef + name
					}
				}
				out[k] = child
				continue
			}
			out[k] = rewrite(child, target, moduleOf)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, child := range t {
			out[i] = rewrite(child, target, moduleOf)
		}
		return out
	default:
		return v
	}
}

func dig(v any, keys ...string) any {
	for _, k := range keys {
		v = asObj(v)[k]
	}
	return v
}

func asObj(v any) obj {
	o, _ := v.(obj)
	return o
}
