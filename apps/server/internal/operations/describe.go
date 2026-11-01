package operations

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

const (
	// describeMaxBytes is the most Describe answers: a small local model has a
	// small context window.
	describeMaxBytes = 8 << 10
	// describeDescriptionMax bounds each description kept in a schema.
	describeDescriptionMax = 280
)

// describeDepths are the nesting levels Describe tries, deepest first: the
// first that fits describeMaxBytes is the answer.
var describeDepths = []int{8, 5, 4, 3, 2, 1}

// compactor renders a schema as plain JSON-Schema-like data a model can read:
// every $ref resolved in place to a depth limit, enums inline, descriptions
// kept, and nothing the model has no use for (examples, XML, vendor
// extensions, the validation keywords beyond the common few).
type compactor struct {
	maxDepth int
	// stack holds the component names being expanded right now, to cut a
	// cycle where it closes.
	stack map[string]bool
}

// refName is the component name a reference points at, "" for an inline schema.
func refName(ref string) string {
	if i := strings.LastIndex(ref, "/"); i >= 0 && strings.HasPrefix(ref, "#/components/") {
		return ref[i+1:]
	}
	return ""
}

func shorten(s string) string {
	s = strings.TrimSpace(s)
	if len([]rune(s)) <= describeDescriptionMax {
		return s
	}
	return string([]rune(s)[:describeDescriptionMax]) + "…"
}

func (c *compactor) schema(ref *openapi3.SchemaRef, depth int) any {
	if ref == nil || ref.Value == nil {
		return map[string]any{}
	}
	v := ref.Value
	name := refName(ref.Ref)
	if name != "" {
		if c.stack[name] {
			return map[string]any{"note": "recursive: same shape as the enclosing " + name}
		}
		c.stack[name] = true
		defer delete(c.stack, name)
	}

	out := map[string]any{}
	if v.Type != nil && !v.Type.IsEmpty() {
		if v.Type.IsSingle() {
			out["type"] = v.Type.Slice()[0]
		} else {
			out["type"] = v.Type.Slice()
		}
	}
	if v.Format != "" {
		out["format"] = v.Format
	}
	if v.Description != "" {
		out["description"] = shorten(v.Description)
	}
	if v.Nullable {
		out["nullable"] = true
	}
	if len(v.Enum) > 0 {
		out["enum"] = v.Enum
	}
	if v.Default != nil {
		out["default"] = v.Default
	}
	if v.Min != nil {
		out["minimum"] = *v.Min
	}
	if v.Max != nil {
		out["maximum"] = *v.Max
	}
	if v.MinLength > 0 {
		out["minLength"] = v.MinLength
	}
	if v.MaxLength != nil {
		out["maxLength"] = *v.MaxLength
	}
	if v.Pattern != "" {
		out["pattern"] = v.Pattern
	}
	if v.ReadOnly {
		out["readOnly"] = true
	}

	nested := len(v.Properties) > 0 || v.Items != nil || len(v.AllOf) > 0 || len(v.OneOf) > 0 || len(v.AnyOf) > 0 ||
		(v.AdditionalProperties.Schema != nil)
	if nested && depth >= c.maxDepth {
		out["note"] = "nested deeper than shown"
		if len(v.Properties) > 0 {
			out["properties"] = sortedKeys(v.Properties)
		}
		return out
	}

	if len(v.Properties) > 0 {
		props := make(map[string]any, len(v.Properties))
		for propName, prop := range v.Properties {
			props[propName] = c.schema(prop, depth+1)
		}
		out["properties"] = props
	}
	if len(v.Required) > 0 {
		req := slices.Clone(v.Required)
		sort.Strings(req)
		out["required"] = req
	}
	if v.Items != nil {
		out["items"] = c.schema(v.Items, depth+1)
	}
	if v.AdditionalProperties.Schema != nil {
		out["additionalProperties"] = c.schema(v.AdditionalProperties.Schema, depth+1)
	}
	for _, group := range []struct {
		key  string
		refs openapi3.SchemaRefs
	}{{"allOf", v.AllOf}, {"oneOf", v.OneOf}, {"anyOf", v.AnyOf}} {
		if len(group.refs) == 0 {
			continue
		}
		parts := make([]any, len(group.refs))
		for i, part := range group.refs {
			parts[i] = c.schema(part, depth+1)
		}
		out[group.key] = parts
	}
	return out
}

func sortedKeys(s openapi3.Schemas) []string {
	keys := make([]string, 0, len(s))
	for k := range s {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// parameterDoc is one parameter as Describe shows it.
type parameterDoc struct {
	Name        string `json:"name"`
	Required    bool   `json:"required,omitempty"`
	Description string `json:"description,omitempty"`
	Schema      any    `json:"schema,omitempty"`
}

// describeDoc is what Describe answers.
type describeDoc struct {
	ID          string         `json:"id"`
	Method      string         `json:"method"`
	Path        string         `json:"path"`
	Summary     string         `json:"summary"`
	Description string         `json:"description,omitempty"`
	Mode        Mode           `json:"mode"`
	PathParams  []parameterDoc `json:"pathParameters,omitempty"`
	Query       []parameterDoc `json:"queryParameters,omitempty"`
	Headers     []parameterDoc `json:"headerParameters,omitempty"`
	Body        *bodyDoc       `json:"requestBody,omitempty"`
	Response    *responseDoc   `json:"response,omitempty"`
	Note        string         `json:"note,omitempty"`
}

type bodyDoc struct {
	Required bool `json:"required,omitempty"`
	Schema   any  `json:"schema,omitempty"`
}

type responseDoc struct {
	Status      string `json:"status"`
	Description string `json:"description,omitempty"`
	Schema      any    `json:"schema,omitempty"`
}

// describe renders e within describeMaxBytes. It tries the schemas in full
// first, then at fewer levels of nesting, and last leaves the schemas out,
// naming their top-level fields; the answer is always valid JSON.
func (e *entry) describe() json.RawMessage {
	for _, depth := range describeDepths {
		doc := e.describeAt(depth)
		raw, err := json.Marshal(doc)
		if err == nil && len(raw) <= describeMaxBytes {
			return raw
		}
	}

	doc := describeDoc{
		ID: e.ID, Method: e.Method, Path: e.Path, Summary: e.Summary, Mode: e.Mode,
		Description: shorten(e.Description),
		Note:        "The parameters and schemas are too large to show here; they are described by name only.",
	}
	for _, p := range e.params {
		pd := parameterDoc{Name: p.Name, Required: p.Required}
		switch p.In {
		case "path":
			doc.PathParams = append(doc.PathParams, pd)
		case "query":
			doc.Query = append(doc.Query, pd)
		case "header":
			doc.Headers = append(doc.Headers, pd)
		}
	}
	if e.body != nil {
		doc.Body = &bodyDoc{Required: e.bodyRequired}
	}
	raw, _ := json.Marshal(doc)
	if len(raw) > describeMaxBytes {
		// Even the names are too many: keep the identity and say so.
		raw, _ = json.Marshal(describeDoc{
			ID: e.ID, Method: e.Method, Path: e.Path, Summary: e.Summary, Mode: e.Mode,
			Note: fmt.Sprintf("The description is larger than %d bytes and is not shown.", describeMaxBytes),
		})
	}
	return raw
}

func (e *entry) describeAt(depth int) describeDoc {
	c := &compactor{maxDepth: depth, stack: map[string]bool{}}
	doc := describeDoc{
		ID: e.ID, Method: e.Method, Path: e.Path, Summary: e.Summary, Mode: e.Mode,
		Description: shorten(e.Description),
	}
	for _, p := range e.params {
		pd := parameterDoc{Name: p.Name, Required: p.Required, Description: shorten(p.Description)}
		if p.Schema != nil {
			pd.Schema = c.schema(p.Schema, 0)
		}
		switch p.In {
		case "path":
			doc.PathParams = append(doc.PathParams, pd)
		case "query":
			doc.Query = append(doc.Query, pd)
		case "header":
			doc.Headers = append(doc.Headers, pd)
		}
	}
	if e.body != nil {
		doc.Body = &bodyDoc{Required: e.bodyRequired, Schema: c.schema(e.body, 0)}
	}
	if e.successStatus != "" {
		resp := &responseDoc{Status: e.successStatus, Description: e.successDescription}
		if e.success != nil {
			resp.Schema = c.schema(e.success, 0)
		}
		doc.Response = resp
	}
	return doc
}
