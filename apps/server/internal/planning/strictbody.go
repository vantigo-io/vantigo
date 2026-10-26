package planning

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"

	"github.com/getkin/kin-openapi/openapi3"
)

// This file holds the one place a request body is judged by what the
// contract does NOT name — Staff's mechanism (internal/staff/strictbody.go),
// copied because no module imports another. A schema declared with
// additionalProperties false refuses a property it does not name as a 400
// naming that property, rather than dropping it on the floor as the
// generated decoder does: a client that sends requestId to POST /allocations
// before delivery B must learn it was not written, not believe it was.
//
// The generated decoder reads the body once, into the typed request, so the
// raw bytes are kept for the handler by keepRawBody, which every operation
// runs through after the router has authenticated, authorised and capped it.
// This module takes no upload, so every body is kept.

// rawBodyKey is the context key keepRawBody stores a request's body under.
type rawBodyKey struct{}

// keepRawBody keeps a copy of the request's body in its context and hands the
// generated decoder an identical reader. The router has already capped the
// body, so the copy is bounded; a read that fails is handed on as the same
// failure, which the decoder answers as it would have.
func keepRawBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body == nil || r.Body == http.NoBody {
			next.ServeHTTP(w, r)
			return
		}
		body, err := io.ReadAll(r.Body)
		var rest io.Reader = bytes.NewReader(body)
		if err != nil {
			rest = io.MultiReader(rest, &failingReader{err: err})
		}
		r.Body = io.NopCloser(rest)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), rawBodyKey{}, body)))
	})
}

// failingReader answers err, for the part of a body that could not be read.
type failingReader struct{ err error }

func (f *failingReader) Read([]byte) (int, error) { return 0, f.err }

// unknownProperties is the request body's properties that the contract's
// schema does not name, sorted, each as a field error — nothing when the
// body is not a JSON object (the decoder has judged it already) or the
// schema admits any property. The top level is judged first, then each
// array property closedItems names for the schema, item by item against its
// item schema, as requirements[i].<name>: a key the generated decoder would
// drop inside an item is refused as one at the top level is.
func (s *server) unknownProperties(ctx context.Context, schema string) fieldErrors {
	var f fieldErrors
	body, _ := ctx.Value(rawBodyKey{}).([]byte)
	if len(body) == 0 || s.deps.Doc == nil {
		return f
	}
	closed := s.closedSchema(schema)
	if closed == nil {
		return f
	}
	var properties map[string]json.RawMessage
	if json.Unmarshal(body, &properties) != nil {
		return f
	}
	for _, name := range unknownNames(closed, properties) {
		f.add(name, unknownPropertyMessage)
	}
	for _, array := range slices.Sorted(maps.Keys(closedItems[schema])) {
		item := s.closedSchema(closedItems[schema][array])
		var items []map[string]json.RawMessage
		if item == nil || json.Unmarshal(properties[array], &items) != nil {
			continue
		}
		for i, properties := range items {
			for _, name := range unknownNames(item, properties) {
				f.add(fmt.Sprintf("%s[%d].%s", array, i, name), unknownPropertyMessage)
			}
		}
	}
	return f
}

// unknownPropertyMessage is the field error of a property a closed schema
// does not name.
const unknownPropertyMessage = "This property is not one this request takes; the request was not applied"

// closedSchema is the contract's schema named name when it closes itself
// (additionalProperties false), nil when the contract has no such schema or
// it admits any property.
func (s *server) closedSchema(name string) *openapi3.Schema {
	ref := s.deps.Doc.Components.Schemas[name]
	if ref == nil || ref.Value == nil {
		return nil
	}
	if admits := ref.Value.AdditionalProperties.Has; admits == nil || *admits {
		return nil
	}
	return ref.Value
}

// unknownNames is the names of properties schema does not name, sorted.
func unknownNames(schema *openapi3.Schema, properties map[string]json.RawMessage) []string {
	names := make([]string, 0, len(properties))
	for name := range properties {
		if _, ok := schema.Properties[name]; !ok {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

// The contract's names for the request schemas this module closes: the
// settings' change (S20), an allocation's fields (S1–S5), a request's fields,
// each of its requirements and a fill (S16), an allocation's terms (S14), a
// wish (S21).
const (
	settingsRequestSchema   = "PlanningSettingsRequest"
	allocationRequestSchema = "PlanningAllocationInput"
	requestRequestSchema    = "PlanningRequestInput"
	fillRequestSchema       = "PlanningFillInput"
	requirementInputSchema  = "PlanningRequirementInput"
	termsRequestSchema      = "PlanningTermsInput"
	wishRequestSchema       = "PlanningWishInput"
)

// closedSchemas are the request schemas whose handlers refuse a property the
// contract does not name, and the item schemas closedItems judges inside
// them.
var closedSchemas = []string{settingsRequestSchema, allocationRequestSchema, requestRequestSchema, fillRequestSchema, requirementInputSchema, termsRequestSchema, wishRequestSchema}

// closedItems is, per closed request schema, its array properties whose
// items are closed schemas of their own, each with that schema's name: a
// request's requirements are each a PlanningRequirementInput, so a key the
// contract does not name inside one is refused on requirements[i].<key>.
var closedItems = map[string]map[string]string{
	requestRequestSchema: {"requirements": requirementInputSchema},
}

// checkClosedSchemas fails the mount unless the contract has every schema of
// closedSchemas and closes each (additionalProperties false): the refusal
// reads exactly that, so a contract that lost the schema, or opened it, would
// otherwise let a property be sent and silently dropped.
func checkClosedSchemas(doc *openapi3.T) error {
	if doc == nil || doc.Components == nil {
		return errors.New("planning: no contract to check the closed request schemas against")
	}
	for _, name := range closedSchemas {
		ref := doc.Components.Schemas[name]
		if ref == nil || ref.Value == nil {
			return fmt.Errorf("planning: the contract has no %s schema", name)
		}
		if admits := ref.Value.AdditionalProperties.Has; admits == nil || *admits {
			return fmt.Errorf("planning: the contract's %s must declare additionalProperties: false", name)
		}
	}
	return nil
}
