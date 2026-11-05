package orders

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
// contract does NOT name — Inventory's mechanism
// (internal/inventory/strictbody.go), copied because no module imports
// another. A schema declared with additionalProperties false refuses a
// property it does not name as a 400 naming that property, rather than
// dropping it on the floor as the generated decoder does: a client that
// misspells discountPercent on a line must learn it was not written, not
// believe it was.
//
// The generated decoder reads the body once, into the typed request, so the
// raw bytes are kept for the handler by keepRawBody, which every operation
// runs through after the router has authenticated, authorised and capped it.
// Every body is kept; every one is JSON and capped by the router.

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
// item schemas of their lines.
const (
	quoteInputSchema         = "OrdersQuoteInput"
	quoteUpdateSchema        = "OrdersQuoteUpdate"
	lineInputSchema          = "OrdersLineInput"
	acceptInputSchema        = "OrdersAcceptInput"
	applyTemplateInputSchema = "OrdersApplyTemplateInput"
	declineInputSchema       = "OrdersDeclineInput"
	markSentInputSchema      = "OrdersMarkSentInput"
	revisionInputSchema      = "OrdersRevisionInput"
	sendInputSchema          = "OrdersSendInput"
	settingsInputSchema      = "OrdersSettingsInput"
	mailTextSchema           = "OrdersMailText"
	templateInputSchema      = "OrdersTemplateInput"
	templateLineInputSchema  = "OrdersTemplateLineInput"

	// The orders lane's.
	orderInputSchema     = "OrdersOrderInput"
	orderUpdateSchema    = "OrdersOrderUpdate"
	versionInputSchema   = "OrdersVersionInput"
	confirmInputSchema   = "OrdersConfirmInput"
	holdInputSchema      = "OrdersHoldInput"
	deliveredInputSchema = "OrdersDeliveredInput"
	deliveredLineSchema  = "OrdersDeliveredLine"

	// The warnings' (O33).
	warningInputSchema = "OrdersWarningInput"

	// The subscriptions' (O42–O44).
	subscriptionInputSchema  = "OrdersSubscriptionInput"
	subscriptionUpdateSchema = "OrdersSubscriptionUpdate"
	subscriptionPlanSchema   = "OrdersSubscriptionPlan"
	pauseInputSchema         = "OrdersPauseInput"
	resumeInputSchema        = "OrdersResumeInput"
	endInputSchema           = "OrdersEndInput"

	// The deliveries' (O47).
	deliveryInputSchema     = "OrdersDeliveryInput"
	deliveryUpdateSchema    = "OrdersDeliveryUpdate"
	deliveryLineInputSchema = "OrdersDeliveryLineInput"

	// Shipping's (O49–O53).
	carrierAccountInputSchema   = "OrdersCarrierAccountInput"
	carrierCredentialsSchema    = "OrdersCarrierCredentials"
	carrierSenderSchema         = "OrdersCarrierSender"
	siteDefaultsSchema          = "OrdersSiteCarrierDefaults"
	siteDefaultSchema           = "OrdersSiteCarrierDefault"
	shipmentInputSchema         = "OrdersShipmentInput"
	shipmentUpdateSchema        = "OrdersShipmentUpdate"
	shipmentPackageInputSchema  = "OrdersShipmentPackageInput"
	shippingEstimateInputSchema = "OrdersShippingEstimateInput"
)

// closedSchemas are the request schemas whose handlers refuse a property the
// contract does not name, and the item schemas closedItems judges inside them.
var closedSchemas = []string{
	quoteInputSchema, quoteUpdateSchema, lineInputSchema, acceptInputSchema, applyTemplateInputSchema,
	declineInputSchema, markSentInputSchema, revisionInputSchema, sendInputSchema, settingsInputSchema,
	mailTextSchema, templateInputSchema, templateLineInputSchema,
	orderInputSchema, orderUpdateSchema, versionInputSchema, confirmInputSchema, holdInputSchema, deliveredInputSchema, deliveredLineSchema,
	warningInputSchema,
	subscriptionInputSchema, subscriptionUpdateSchema, subscriptionPlanSchema, pauseInputSchema, resumeInputSchema, endInputSchema,
	deliveryInputSchema, deliveryUpdateSchema, deliveryLineInputSchema,
	carrierAccountInputSchema, carrierCredentialsSchema, carrierSenderSchema, siteDefaultsSchema, siteDefaultSchema,
	shipmentInputSchema, shipmentUpdateSchema, shipmentPackageInputSchema, shippingEstimateInputSchema,
}

// closedItems is, per closed request schema, its array properties whose
// items are closed schemas of their own: a key the contract does not name
// inside a line is refused on lines[i].<key>.
var closedItems = map[string]map[string]string{
	quoteInputSchema:         {"lines": lineInputSchema},
	quoteUpdateSchema:        {"lines": lineInputSchema},
	orderInputSchema:         {"lines": lineInputSchema},
	orderUpdateSchema:        {"lines": lineInputSchema},
	settingsInputSchema:      {"mailTexts": mailTextSchema},
	templateInputSchema:      {"lines": templateLineInputSchema},
	deliveredInputSchema:     {"lines": deliveredLineSchema},
	subscriptionInputSchema:  {"lines": lineInputSchema},
	subscriptionUpdateSchema: {"lines": lineInputSchema},
	deliveryUpdateSchema:     {"lines": deliveryLineInputSchema},
	siteDefaultsSchema:       {"items": siteDefaultSchema},
	shipmentUpdateSchema:     {"packages": shipmentPackageInputSchema},
}

// closedObjects is, per closed schema, its object properties that are closed
// schemas of their own: an accept's subscription plan (O20).
var closedObjects = map[string]map[string]string{
	acceptInputSchema:         {"plan": subscriptionPlanSchema},
	carrierAccountInputSchema: {"credentials": carrierCredentialsSchema, "sender": carrierSenderSchema},
}

// checkClosedSchemas fails the mount unless the contract has every schema of
// closedSchemas and closes each (additionalProperties false): the refusal
// reads exactly that, so a contract that lost the schema, or opened it, would
// otherwise let a property be sent and silently dropped.
func checkClosedSchemas(doc *openapi3.T) error {
	if doc == nil || doc.Components == nil {
		return errors.New("orders: no contract to check the closed request schemas against")
	}
	for _, name := range closedSchemas {
		ref := doc.Components.Schemas[name]
		if ref == nil || ref.Value == nil {
			return fmt.Errorf("orders: the contract has no %s schema", name)
		}
		if admits := ref.Value.AdditionalProperties.Has; admits == nil || *admits {
			return fmt.Errorf("orders: the contract's %s must declare additionalProperties: false", name)
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
