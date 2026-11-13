package manufacturing

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
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

// This file holds the one place a request body is judged by what the
// contract does NOT name — Orders' and Inventory's mechanism
// (internal/orders/strictbody.go), copied because no module imports
// another. A schema declared with additionalProperties false refuses a
// property it does not name as a 400 naming that property, rather than
// dropping it on the floor as the generated decoder does: a client that
// misspells scrapPercent on a line must learn it was not written, not
// believe it was.
//
// The generated decoder reads the body once, into the typed request, so the
// raw bytes are kept for the handler by keepRawBody, which every operation
// runs through after the router has authenticated, authorised and capped it.
// Every JSON body is kept, capped by the router; a check's photo, the one
// multipart upload, is not copied — its handler reads the parts itself and no
// closed schema judges it.

// rawBodyKey is the context key keepRawBody stores a request's body under.
type rawBodyKey struct{}

// keepRawBody keeps a copy of the request's body in its context and hands the
// generated decoder an identical reader. The router has already capped the
// body, so the copy is bounded; a read that fails is handed on as the same
// failure, which the decoder answers as it would have.
func keepRawBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body == nil || r.Body == http.NoBody || strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "multipart/") {
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
// item schemas of their arrays.
const (
	workCenterInputSchema   = "ManufacturingWorkCenterInput"
	workCenterUpdateSchema  = "ManufacturingWorkCenterUpdate"
	weekPatternSchema       = "ManufacturingWeekPattern"
	intervalSchema          = "ManufacturingInterval"
	revisionInputSchema     = "ManufacturingRevisionInput"
	closureInputSchema      = "ManufacturingClosureInput"
	bomInputSchema          = "ManufacturingBomInput"
	bomUpdateSchema         = "ManufacturingBomUpdate"
	bomOperationInputSchema = "ManufacturingBomOperationInput"
	bomLineInputSchema      = "ManufacturingBomLineInput"

	orderInputSchema          = "ManufacturingOrderInput"
	orderUpdateSchema         = "ManufacturingOrderUpdate"
	componentInputSchema      = "ManufacturingComponentInput"
	componentUpdateSchema     = "ManufacturingComponentUpdate"
	productionInputSchema     = "ManufacturingProductionInput"
	productionLineInputSchema = "ManufacturingProductionLineInput"
	productionLotInputSchema  = "ManufacturingProductionLotInput"
	outputLotInputSchema      = "ManufacturingOutputLotInput"
	outputSerialsInputSchema  = "ManufacturingOutputSerialsInput"
	closeInputSchema          = "ManufacturingCloseInput"

	startInputSchema     = "ManufacturingStartInput"
	finishInputSchema    = "ManufacturingFinishInput"
	scrapInputSchema     = "ManufacturingScrapInput"
	scrapLineInputSchema = "ManufacturingScrapLineInput"
	blockInputSchema     = "ManufacturingBlockInput"
	correctionSchema     = "ManufacturingTimeLogCorrection"
	timeLogInputSchema   = "ManufacturingTimeLogInput"

	checkTemplateInputSchema  = "ManufacturingCheckTemplateInput"
	checkResultInputSchema    = "ManufacturingCheckResultInput"
	deviationInputSchema      = "ManufacturingQualityDeviationInput"
	deviationCloseInputSchema = "ManufacturingDeviationCloseInput"

	groupInputSchema    = "ManufacturingWorkCenterGroupInput"
	groupUpdateSchema   = "ManufacturingWorkCenterGroupUpdate"
	quantityInputSchema = "ManufacturingQuantityInput"

	bomOutputInputSchema        = "ManufacturingBomOutputInput"
	productionOutputInputSchema = "ManufacturingProductionOutputInput"
	unbuildInputSchema          = "ManufacturingUnbuildInput"
	unbuildComponentInputSchema = "ManufacturingUnbuildComponentInput"
	splitInputSchema            = "ManufacturingOrderSplitInput"
	mergeInputSchema            = "ManufacturingOrderMergeInput"
	mergeItemSchema             = "ManufacturingOrderMergeItem"
)

// closedSchemas are the request schemas whose handlers refuse a property the
// contract does not name, and the item schemas closedItems and closedObjects
// judge inside them.
var closedSchemas = []string{
	workCenterInputSchema, workCenterUpdateSchema, weekPatternSchema, intervalSchema, revisionInputSchema,
	closureInputSchema, bomInputSchema, bomUpdateSchema, bomOperationInputSchema, bomLineInputSchema,
	orderInputSchema, orderUpdateSchema, componentInputSchema, componentUpdateSchema, productionInputSchema,
	productionLineInputSchema, productionLotInputSchema, outputLotInputSchema, outputSerialsInputSchema, closeInputSchema, startInputSchema, finishInputSchema, scrapInputSchema, scrapLineInputSchema,
	blockInputSchema, correctionSchema, timeLogInputSchema,
	checkTemplateInputSchema, checkResultInputSchema, deviationInputSchema, deviationCloseInputSchema,
	groupInputSchema, groupUpdateSchema, quantityInputSchema,
	bomOutputInputSchema, productionOutputInputSchema, unbuildInputSchema, unbuildComponentInputSchema, splitInputSchema, mergeInputSchema,
	mergeItemSchema,
}

// weekPatternDays is a week pattern's arrays, one per weekday, each of
// intervals.
var weekPatternDays = map[string]string{
	"monday": intervalSchema, "tuesday": intervalSchema, "wednesday": intervalSchema, "thursday": intervalSchema,
	"friday": intervalSchema, "saturday": intervalSchema, "sunday": intervalSchema,
}

// closedItems is, per closed schema, its array properties whose items are
// closed schemas of their own: a key the contract does not name inside an
// operation or a line is refused on operations[i].<key> or lines[i].<key>.
var closedItems = map[string]map[string]string{
	bomInputSchema:            {"operations": bomOperationInputSchema, "lines": bomLineInputSchema, "outputs": bomOutputInputSchema},
	bomUpdateSchema:           {"operations": bomOperationInputSchema, "lines": bomLineInputSchema, "outputs": bomOutputInputSchema},
	weekPatternSchema:         weekPatternDays,
	productionInputSchema:     {"lines": productionLineInputSchema, "outputs": productionOutputInputSchema},
	unbuildInputSchema:        {"components": unbuildComponentInputSchema},
	mergeInputSchema:          {"orders": mergeItemSchema},
	productionLineInputSchema: {"lots": productionLotInputSchema},
	scrapInputSchema:          {"lines": scrapLineInputSchema},
}

// closedObjects is, per closed schema, its object properties that are closed
// schemas of their own: a work center's weekly pattern (M9), a finish's
// production (M29), a production's output lot and serials (manufacturing
// phase 2 M80, M81).
var closedObjects = map[string]map[string]string{
	workCenterInputSchema:  {"pattern": weekPatternSchema},
	workCenterUpdateSchema: {"pattern": weekPatternSchema},
	finishInputSchema:      {"production": productionInputSchema},
	productionInputSchema:  {"outputLot": outputLotInputSchema, "outputSerials": outputSerialsInputSchema},
	// A by-product's lot and a free-standing unbuild's new lot (manufacturing
	// phase 2 M121, M123).
	productionOutputInputSchema: {"lot": outputLotInputSchema},
	unbuildComponentInputSchema: {"lot": outputLotInputSchema},
}

// checkClosedSchemas fails the mount unless the contract has every schema of
// closedSchemas and closes each (additionalProperties false): the refusal
// reads exactly that, so a contract that lost the schema, or opened it, would
// otherwise let a property be sent and silently dropped.
func checkClosedSchemas(doc *openapi3.T) error {
	if doc == nil || doc.Components == nil {
		return errors.New("manufacturing: no contract to check the closed request schemas against")
	}
	for _, name := range closedSchemas {
		ref := doc.Components.Schemas[name]
		if ref == nil || ref.Value == nil {
			return fmt.Errorf("manufacturing: the contract has no %s schema", name)
		}
		if admits := ref.Value.AdditionalProperties.Has; admits == nil || *admits {
			return fmt.Errorf("manufacturing: the contract's %s must declare additionalProperties: false", name)
		}
	}
	return nil
}

// hasProperty reports whether the request body names a top-level property,
// for a nullable field whose absence keeps what is stored and whose null
// clears it — which the generated decoder reads alike.
func hasProperty(ctx context.Context, name string) bool {
	body, _ := ctx.Value(rawBodyKey{}).([]byte)
	var properties map[string]json.RawMessage
	if json.Unmarshal(body, &properties) != nil {
		return false
	}
	_, ok := properties[name]
	return ok
}
