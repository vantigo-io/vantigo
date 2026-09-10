package main

import (
	"strings"
	"testing"
)

const dump = `{
 "openapi": "3.0.4",
 "info": {"title": "Vantigo API", "version": "1"},
 "paths": {
  "/api/v1/customers/{id}": {"get": {"operationId": "getCustomersById", "x-vantigo-access": "permission:customers:view",
    "responses": {"200": {"description": "OK", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Customer"}}}},
                  "404": {"description": "NF", "content": {"application/problem+json": {"schema": {"$ref": "#/components/schemas/ProblemDetails"}}}}}}},
  "/api/v1/products": {"get": {"operationId": "getProducts", "x-vantigo-access": "permission:products:view",
    "responses": {"400": {"description": "Bad", "content": {"application/problem+json": {"schema": {"$ref": "#/components/schemas/ProblemDetails"}}}}}}},
  "/api/v1/identity/antiforgery": {"get": {"operationId": "getIdentityAntiforgery", "x-vantigo-access": "anonymous", "responses": {"200": {"description": "OK"}}}},
  "/api/v1/identity/admin/tenants/{id}": {"get": {"operationId": "getIdentityAdminTenantsById", "x-vantigo-access": "policy:SystemAdmin", "responses": {"200": {"description": "OK"}}}}
 },
 "components": {"schemas": {
  "Customer": {"type": "object", "properties": {"address": {"$ref": "#/components/schemas/Address"}}},
  "Address": {"type": "object"},
  "ProblemDetails": {"type": "object"},
  "AuthUserResponse": {"type": "object", "x-vantigo-source": "Vantigo.Identity"},
  "PagedMeta": {"type": "object", "x-vantigo-source": "Vantigo.Contracts"}
 }}
}`

func TestSplit(t *testing.T) {
	files, err := split([]byte(dump))
	if err != nil {
		t.Fatal(err)
	}

	has := func(file, fragment string) {
		t.Helper()
		if !strings.Contains(files[file], fragment) {
			t.Errorf("%s is missing %q:\n%s", file, fragment, files[file])
		}
	}
	lacks := func(file, fragment string) {
		t.Helper()
		if strings.Contains(files[file], fragment) {
			t.Errorf("%s must not contain %q:\n%s", file, fragment, files[file])
		}
	}

	for _, name := range []string{"common.yaml", "identity.yaml", "customers.yaml", "products.yaml", "energy.yaml", "communications.yaml"} {
		has(name, "openapi: 3.0.3")
		has(name, "url: /")
	}
	has("customers.yaml", "/api/v1/customers/{id}:")
	has("customers.yaml", "title: Vantigo Customers API")
	// Used by one module: stays in it, with its nested schema.
	has("customers.yaml", "  Customer:")
	has("customers.yaml", "  Address:")
	has("customers.yaml", "$ref: '#/components/schemas/Customer'")
	// Used by two modules: moves to common and is referenced across files.
	has("common.yaml", "  ProblemDetails:")
	has("customers.yaml", "$ref: common.yaml#/components/schemas/ProblemDetails")
	has("products.yaml", "$ref: common.yaml#/components/schemas/ProblemDetails")
	lacks("customers.yaml", "  ProblemDetails:")
	// Unreferenced record schemas follow x-vantigo-source.
	has("identity.yaml", "  AuthUserResponse:")
	has("common.yaml", "  PagedMeta:")
	// Dropped operations never appear.
	lacks("identity.yaml", "antiforgery")
	lacks("identity.yaml", "admin/tenants")
	// Access and operationIds survive.
	has("customers.yaml", "x-vantigo-access: permission:customers:view")
	has("customers.yaml", "operationId: getCustomersById")
}

func TestSplitRejectsAnUnknownModule(t *testing.T) {
	_, err := split([]byte(`{"openapi":"3.0.4","info":{"title":"x","version":"1"},"paths":{"/api/v1/warehouse/x":{"get":{"responses":{"200":{"description":"OK"}}}}}}`))
	if err == nil || !strings.Contains(err.Error(), "warehouse") {
		t.Fatalf("err = %v, want it to name the unknown module", err)
	}
}
