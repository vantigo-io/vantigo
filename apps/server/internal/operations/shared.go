package operations

import "github.com/getkin/kin-openapi/openapi3"

// The pieces of the dispatcher the MCP endpoint (internal/module, MCP server
// design M30) uses as they are: it keeps a catalogue of its own, cut to an API
// key's scope rather than a person's grant, but searches and describes it the
// way Search and Describe do.

// Tokenize is the words of query Search matches: lower-cased, alphanumeric,
// the stop words of both interface languages dropped.
func Tokenize(query string) []string { return tokenize(query) }

// TokenMatches reports whether field, lower-cased, contains tok, or tok
// without a plural s, as Search scores a field.
func TokenMatches(field, tok string) bool { return matches(field, tok) }

// CompactSchema is ref as Describe renders a schema: every $ref resolved in
// place to maxDepth levels of nesting, a cycle cut with a note, descriptions
// shortened.
func CompactSchema(ref *openapi3.SchemaRef, maxDepth int) any {
	c := &compactor{maxDepth: maxDepth, stack: map[string]bool{}}
	return c.schema(ref, 0)
}

// Shorten clips a description to the length Describe keeps.
func Shorten(s string) string { return shorten(s) }

// IsJSON reports whether contentType is JSON (application/json or a +json
// type).
func IsJSON(contentType string) bool { return isJSON(contentType) }

// JSONRequest is false for an operation whose request body is a file upload:
// it takes a body, and none of its media types is JSON.
func JSONRequest(op *openapi3.Operation) bool { return jsonRequest(op) }
