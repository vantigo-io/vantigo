package openapi

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/gorillamux"
)

func init() {
	// kin-openapi v0.149.0 registers a decoder for application/problem+json
	// but has no decoder for the SCIM (RFC 7644) media type and no generic
	// "+json" suffix fallback — the body-decoder lookup is exact. SCIM
	// bodies are plain JSON on the wire, so reuse the JSON decoder.
	openapi3filter.RegisterBodyDecoder("application/scim+json", openapi3filter.JSONBodyDecoder)
}

// Exchange is one request/response pair recorded from the .NET integration
// suites (packages/contract-recording). Bodies are nil when they were not
// text or were too large to record.
type Exchange struct {
	Method              string  `json:"method"`
	Path                string  `json:"path"`
	Query               string  `json:"query"`
	RequestContentType  *string `json:"requestContentType"`
	RequestBody         *string `json:"requestBody"`
	Status              int     `json:"status"`
	ResponseContentType *string `json:"responseContentType"`
	ResponseBody        *string `json:"responseBody"`
}

// routerCache holds one gorillamux router per document, keyed by pointer, so
// TestRecordedExchangesMatchTheContract does not rebuild a router for every
// one of the thousands of corpus exchanges.
var routerCache sync.Map // *openapi3.T -> routers.Router

func routerFor(doc *openapi3.T) (routers.Router, error) {
	if r, ok := routerCache.Load(doc); ok {
		return r.(routers.Router), nil
	}
	r, err := gorillamux.NewRouter(doc)
	if err != nil {
		return nil, err
	}
	actual, _ := routerCache.LoadOrStore(doc, r)
	return actual.(routers.Router), nil
}

// Validate checks ex against doc and returns the operationId it matched (the
// path when it matched none). Undocumented response statuses are errors.
func Validate(ctx context.Context, doc *openapi3.T, ex Exchange) (string, error) {
	router, err := routerFor(doc)
	if err != nil {
		return ex.Path, err
	}
	req := httptest.NewRequest(ex.Method, ex.Path+ex.Query, strings.NewReader(deref(ex.RequestBody)))
	if ex.RequestContentType != nil {
		req.Header.Set("Content-Type", *ex.RequestContentType)
	}
	route, params, err := router.FindRoute(req)
	if err != nil {
		return ex.Method + " " + ex.Path, fmt.Errorf("no operation matches: %w", err)
	}

	options := &openapi3filter.Options{
		AuthenticationFunc:    openapi3filter.NoopAuthenticationFunc,
		IncludeResponseStatus: true,
		MultiError:            true,
		ExcludeRequestBody:    ex.RequestBody == nil,
		ExcludeResponseBody:   ex.ResponseBody == nil,
	}
	input := &openapi3filter.RequestValidationInput{Request: req, PathParams: params, Route: withoutUnrecordedParameters(route), Options: options}
	id := route.Operation.OperationID
	// The .NET suites send invalid requests on purpose. A request the contract
	// rejects is consistent only when the server rejected it too (4xx); the
	// rejection response must then still match what the contract documents.
	// Never loosen a schema to make such an exchange pass.
	if err := openapi3filter.ValidateRequest(ctx, input); err != nil && (ex.Status < 400 || ex.Status >= 500) {
		return id, fmt.Errorf("request the contract rejects was answered %d: %w", ex.Status, err)
	}
	header := http.Header{}
	if ex.ResponseContentType != nil {
		header.Set("Content-Type", *ex.ResponseContentType)
	}
	if err := openapi3filter.ValidateResponse(ctx, &openapi3filter.ResponseValidationInput{
		RequestValidationInput: input,
		Status:                 ex.Status,
		Header:                 header,
		Body:                   io.NopCloser(bytes.NewReader([]byte(deref(ex.ResponseBody)))),
		Options:                options,
	}); err != nil {
		return id, fmt.Errorf("response %d: %w", ex.Status, err)
	}
	return id, nil
}

// withoutUnrecordedParameters returns a copy of route whose operation and path
// item lack header and cookie parameters. The recorder
// (packages/contract-recording) keeps no request headers, so such a
// parameter — the required Idempotency-Key, say — cannot be checked against a
// recording, just as an unrecorded body is excluded rather than read as empty.
func withoutUnrecordedParameters(route *routers.Route) *routers.Route {
	strip := func(params openapi3.Parameters) openapi3.Parameters {
		var kept openapi3.Parameters
		for _, p := range params {
			if p != nil && p.Value != nil && (p.Value.In == openapi3.ParameterInHeader || p.Value.In == openapi3.ParameterInCookie) {
				continue
			}
			kept = append(kept, p)
		}
		return kept
	}
	op, item, copied := *route.Operation, *route.PathItem, *route
	op.Parameters, item.Parameters = strip(op.Parameters), strip(item.Parameters)
	copied.Operation, copied.PathItem = &op, &item
	return &copied
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
