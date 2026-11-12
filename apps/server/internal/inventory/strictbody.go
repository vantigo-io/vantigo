package inventory

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
// contract does NOT name — Planning's mechanism (internal/planning/strictbody.go),
// copied because no module imports another. A schema declared with
// additionalProperties false refuses a property it does not name as a 400
// naming that property, rather than dropping it on the floor as the
// generated decoder does: a client that misspells loose in a line's entry
// must learn it was not written, not believe it was.
//
// The generated decoder reads the body once, into the typed request, so the
// raw bytes are kept for the handler by keepRawBody, which every operation
// runs through after the router has authenticated, authorised and capped it.
// Every body is kept: the one upload, the opening-balance import's CSV, is
// capped at maxImportRequestBytes and read whole by its handler anyway.

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
// item schema, as lines[i].<name>, and each object property closedObjects
// names, as lines[i].entered.<name> — so a key the generated decoder would
// drop anywhere in the body is refused as one at the top level is.
func (s *server) unknownProperties(ctx context.Context, schema string) fieldErrors {
	var f fieldErrors
	body, _ := ctx.Value(rawBodyKey{}).([]byte)
	if len(body) == 0 || s.deps.Doc == nil {
		return f
	}
	var properties map[string]json.RawMessage
	if json.Unmarshal(body, &properties) != nil {
		return f
	}
	s.judgeProperties(&f, schema, properties, "")
	return f
}

// judgeProperties adds a field error, under prefix, for every property the
// closed schema named schema does not name, then judges the closed arrays
// and objects inside it.
func (s *server) judgeProperties(f *fieldErrors, schema string, properties map[string]json.RawMessage, prefix string) {
	closed := s.closedSchema(schema)
	if closed == nil {
		return
	}
	for _, name := range unknownNames(closed, properties) {
		f.add(prefix+name, unknownPropertyMessage)
	}
	for _, array := range slices.Sorted(maps.Keys(closedItems[schema])) {
		var items []map[string]json.RawMessage
		if json.Unmarshal(properties[array], &items) != nil {
			continue
		}
		for i, item := range items {
			s.judgeProperties(f, closedItems[schema][array], item, fmt.Sprintf("%s%s[%d].", prefix, array, i))
		}
	}
	for _, object := range slices.Sorted(maps.Keys(closedObjects[schema])) {
		var inner map[string]json.RawMessage
		if json.Unmarshal(properties[object], &inner) != nil {
			continue
		}
		s.judgeProperties(f, closedObjects[schema][object], inner, prefix+object+".")
	}
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

// The contract's names for the request schemas this module closes, and the
// item schemas of their lines and levels.
const (
	siteInputSchema       = "InventorySiteInput"
	siteUpdateSchema      = "InventorySiteUpdate"
	locationInputSchema   = "InventoryLocationInput"
	locationUpdateSchema  = "InventoryLocationUpdate"
	bulkInputSchema       = "InventoryBulkCreateInput"
	bulkLevelSchema       = "InventoryBulkLevel"
	adjustmentInputSchema = "InventoryAdjustmentInput"
	adjustmentLineSchema  = "InventoryAdjustmentLine"
	moveInputSchema       = "InventoryMoveInput"
	moveLineSchema        = "InventoryMoveLine"
	receiptInputSchema    = "InventoryReceiptInput"
	receiptLineSchema     = "InventoryReceiptLine"
	newLotSchema          = "InventoryNewLot"
	serialsSchema         = "InventorySerials"

	enteredSchema          = "InventoryEntered"
	enteredPackagingSchema = "InventoryEnteredPackaging"
	countInputSchema       = "InventoryCountInput"
	countLinesInputSchema  = "InventoryCountLinesInput"
	countLineInputSchema   = "InventoryCountLineInput"
	countPostInputSchema   = "InventoryCountPostInput"
	labelsInputSchema      = "InventoryLabelsInput"

	transferInputSchema        = "InventoryTransferInput"
	transferLineInputSchema    = "InventoryTransferLineInput"
	transferSuggestionSchema   = "InventoryTransferSuggestion"
	transferSendInputSchema    = "InventoryTransferSendInput"
	transferSendLineSchema     = "InventoryTransferSendLine"
	transferSendReceiveSchema  = "InventoryTransferSendReceive"
	transferDestinationSchema  = "InventoryTransferDestination"
	transferReceiveInputSchema = "InventoryTransferReceiveInput"
	transferReceiveLineSchema  = "InventoryTransferReceiveLine"
	transferCloseSchema        = "InventoryTransferClose"
	transferCancelInputSchema  = "InventoryTransferCancelInput"

	placementInputSchema = "InventoryPlacementInput"

	lotUpdateSchema      = "InventoryLotUpdate"
	lotHoldInputSchema   = "InventoryLotHoldInput"
	lotHoldReleaseSchema = "InventoryLotHoldRelease"
	lotBlockInputSchema  = "InventoryLotBlockInput"
)

// closedSchemas are the request schemas whose handlers refuse a property the
// contract does not name, and the item and object schemas closedItems and
// closedObjects judge inside them.
var closedSchemas = []string{
	siteInputSchema, siteUpdateSchema, locationInputSchema, locationUpdateSchema, bulkInputSchema, bulkLevelSchema,
	adjustmentInputSchema, adjustmentLineSchema, moveInputSchema, moveLineSchema, receiptInputSchema, receiptLineSchema,
	newLotSchema, serialsSchema,
	enteredSchema, enteredPackagingSchema, countInputSchema, countLinesInputSchema, countLineInputSchema, countPostInputSchema,
	labelsInputSchema,
	transferInputSchema, transferLineInputSchema, transferSuggestionSchema, transferSendInputSchema, transferSendLineSchema,
	transferSendReceiveSchema, transferDestinationSchema, transferReceiveInputSchema, transferReceiveLineSchema,
	transferCloseSchema, transferCancelInputSchema,
	placementInputSchema,
	lotUpdateSchema, lotHoldInputSchema, lotHoldReleaseSchema, lotBlockInputSchema,
	variantRulesInputSchema,
}

// closedItems is, per closed request schema, its array properties whose
// items are closed schemas of their own: a key the contract does not name
// inside a line is refused on lines[i].<key>.
var closedItems = map[string]map[string]string{
	bulkInputSchema:       {"levels": bulkLevelSchema},
	adjustmentInputSchema: {"lines": adjustmentLineSchema},
	moveInputSchema:       {"lines": moveLineSchema},
	receiptInputSchema:    {"lines": receiptLineSchema},
	countLinesInputSchema: {"lines": countLineInputSchema},
	enteredSchema:         {"packagings": enteredPackagingSchema},

	transferInputSchema:        {"lines": transferLineInputSchema},
	transferSendInputSchema:    {"lines": transferSendLineSchema},
	transferSendReceiveSchema:  {"lines": transferDestinationSchema},
	transferReceiveInputSchema: {"lines": transferReceiveLineSchema},
}

// closedObjects is, per closed schema, its object properties that are closed
// schemas of their own: a line's entry (I25), a transfer line's suggestion
// (I57), a send's receive (I43) and a receive's close (I42).
var closedObjects = map[string]map[string]string{
	adjustmentLineSchema: {"entered": enteredSchema, "lot": newLotSchema},
	moveLineSchema:       {"entered": enteredSchema},
	receiptLineSchema:    {"entered": enteredSchema, "lot": newLotSchema, "serials": serialsSchema},
	countLineInputSchema: {"entered": enteredSchema},

	transferLineInputSchema:    {"entered": enteredSchema, "suggestion": transferSuggestionSchema},
	transferSendInputSchema:    {"receive": transferSendReceiveSchema},
	transferReceiveLineSchema:  {"entered": enteredSchema},
	transferReceiveInputSchema: {"close": transferCloseSchema},
}

// checkClosedSchemas fails the mount unless the contract has every schema of
// closedSchemas and closes each (additionalProperties false): the refusal
// reads exactly that, so a contract that lost the schema, or opened it, would
// otherwise let a property be sent and silently dropped.
func checkClosedSchemas(doc *openapi3.T) error {
	if doc == nil || doc.Components == nil {
		return errors.New("inventory: no contract to check the closed request schemas against")
	}
	for _, name := range closedSchemas {
		ref := doc.Components.Schemas[name]
		if ref == nil || ref.Value == nil {
			return fmt.Errorf("inventory: the contract has no %s schema", name)
		}
		if admits := ref.Value.AdditionalProperties.Has; admits == nil || *admits {
			return fmt.Errorf("inventory: the contract's %s must declare additionalProperties: false", name)
		}
	}
	return nil
}
