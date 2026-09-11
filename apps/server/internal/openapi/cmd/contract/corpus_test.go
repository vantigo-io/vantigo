package main

import (
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/openapi"
)

func TestSampleKeySeparatesShapesUnderOneStatus(t *testing.T) {
	str := func(s string) *string { return &s }
	ex := func(contentType, body *string) openapi.Exchange {
		return openapi.Exchange{Method: "POST", Path: "/api/v1/customers", Status: 400, ResponseContentType: contentType, ResponseBody: body}
	}
	validation := sampleKey("customers", "postCustomers", ex(str("application/problem+json"), str(`{"type":"t","title":"x","status":400,"errors":{}}`)))
	validationCharset := sampleKey("customers", "postCustomers", ex(str("application/problem+json; charset=utf-8"), str(`{"errors":{},"status":400,"title":"y","type":"u"}`)))
	antiforgery := sampleKey("customers", "postCustomers", ex(str("application/json; charset=utf-8"), str(`{"error":{"code":"csrf_validation_failed","message":"m","fields":null}}`)))
	empty := sampleKey("customers", "postCustomers", ex(nil, nil))
	array := sampleKey("customers", "postCustomers", ex(str("application/json"), str(`[{"a":1}]`)))

	if validation != validationCharset {
		t.Errorf("same shape, different values or charset: %q != %q", validation, validationCharset)
	}
	for _, other := range []string{antiforgery, empty, array} {
		if other == validation {
			t.Errorf("distinct shape %q collides with %q", other, validation)
		}
	}
	if want := "customers|postCustomers|400|application/json|{error}"; antiforgery != want {
		t.Errorf("antiforgery key = %q, want %q", antiforgery, want)
	}
	if want := "customers|postCustomers|400|-|-"; empty != want {
		t.Errorf("empty key = %q, want %q", empty, want)
	}
}
