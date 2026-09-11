package openapi

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
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

// Validate checks ex against doc and returns the operationId it matched
// ("METHOD path" when it matched none). Undocumented response statuses are
// errors.
//
// kin-openapi is lenient about what the contract does not declare, so
// Validate adds the checks it skips: every recorded query key (exact case)
// must be a declared query parameter; a request body sent to an operation
// without a requestBody, or a response body under a response without
// content, is an error; and when a body was not recorded because it was
// binary, its content type must still be one the contract documents.
func Validate(ctx context.Context, doc *openapi3.T, ex Exchange) (string, error) {
	key := ex.Method + " " + ex.Path
	router, err := routerFor(doc)
	if err != nil {
		return key, err
	}
	req := httptest.NewRequest(ex.Method, ex.Path+ex.Query, strings.NewReader(deref(ex.RequestBody)))
	if ex.RequestContentType != nil {
		req.Header.Set("Content-Type", *ex.RequestContentType)
	}
	route, params, err := router.FindRoute(req)
	if err != nil {
		return key, fmt.Errorf("no operation matches: %w", err)
	}
	id := route.Operation.OperationID

	// The server ignores what it does not bind, so these are contract gaps
	// whatever the status: the client sends something the contract lacks.
	if err := checkQueryKeys(route, ex.Query); err != nil {
		return id, err
	}
	if route.Operation.RequestBody == nil && deref(ex.RequestBody) != "" {
		return id, fmt.Errorf("request body sent to an operation that declares no requestBody")
	}

	options := &openapi3filter.Options{
		AuthenticationFunc:    openapi3filter.NoopAuthenticationFunc,
		IncludeResponseStatus: true,
		MultiError:            true,
		ExcludeRequestBody:    ex.RequestBody == nil,
		ExcludeResponseBody:   ex.ResponseBody == nil,
	}
	input := &openapi3filter.RequestValidationInput{Request: req, PathParams: params, Route: withoutUnrecordedParameters(route), Options: options}
	// The .NET suites send invalid requests on purpose. A request the contract
	// rejects is consistent only when the server rejected it too (4xx); the
	// rejection response must then still match what the contract documents.
	// Never loosen a schema to make such an exchange pass.
	requestErr := openapi3filter.ValidateRequest(ctx, input)
	if requestErr == nil && ex.RequestBody == nil && ex.RequestContentType != nil && route.Operation.RequestBody != nil {
		// kin-openapi skips an excluded body, content type and all.
		requestErr = checkContentType("request", route.Operation.RequestBody.Value.Content, *ex.RequestContentType)
	}
	if requestErr != nil && (ex.Status < 400 || ex.Status >= 500) {
		return id, fmt.Errorf("request the contract rejects was answered %d: %w", ex.Status, requestErr)
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
	response := route.Operation.Responses.Status(ex.Status)
	if response == nil {
		response = route.Operation.Responses.Default()
	}
	if response == nil || response.Value == nil {
		return id, nil // ValidateResponse has already rejected an undocumented status
	}
	content := response.Value.Content
	switch {
	case len(content) == 0 && deref(ex.ResponseBody) != "":
		return id, fmt.Errorf("response %d carries a body, but the contract documents none", ex.Status)
	case len(content) > 0 && ex.ResponseBody == nil && ex.ResponseContentType != nil:
		// kin-openapi returns before the content type when the body is excluded.
		if err := checkContentType(fmt.Sprintf("response %d", ex.Status), content, *ex.ResponseContentType); err != nil {
			return id, err
		}
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

// checkQueryKeys rejects every recorded query key (exact case) that is not a
// declared query parameter of the route's operation or path item.
func checkQueryKeys(route *routers.Route, query string) error {
	values, err := url.ParseQuery(strings.TrimPrefix(query, "?"))
	if err != nil {
		return fmt.Errorf("query %q: %w", query, err)
	}
	declared := map[string]bool{}
	for _, params := range []openapi3.Parameters{route.PathItem.Parameters, route.Operation.Parameters} {
		for _, p := range params {
			if p != nil && p.Value != nil && p.Value.In == openapi3.ParameterInQuery {
				declared[p.Value.Name] = true
			}
		}
	}
	var undeclared []string
	for name := range values {
		if !declared[name] {
			undeclared = append(undeclared, strconv.Quote(name))
		}
	}
	if len(undeclared) == 0 {
		return nil
	}
	sort.Strings(undeclared)
	return fmt.Errorf("query key(s) %s not declared as query parameters", strings.Join(undeclared, ", "))
}

// checkContentType checks a recorded content type against the documented
// media types the way kin-openapi does for a text body: exact, then without
// parameters, then type/*, then */*.
func checkContentType(what string, content openapi3.Content, contentType string) error {
	if content.Get(contentType) == nil {
		return fmt.Errorf("%s content type %q is not documented", what, contentType)
	}
	return nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
