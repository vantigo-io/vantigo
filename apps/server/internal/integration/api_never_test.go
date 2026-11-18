package integration_test

import (
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/openapi"
)

// TestAPINever_NoKeyableOperationNamesOne holds the contracts to K7 of the API
// access design against the composed catalog: a permission rule is an AND, so
// an operation naming any permission no API key holds (contracts.APINever:
// identity's and the agents' keys, and every key that cannot be delegated) is
// one no key can call, and it must say so with x-vantigo-api: never, so the
// integrator's document and a key's 403 agree with the contract.
func TestAPINever_NoKeyableOperationNamesOne(t *testing.T) {
	t.Parallel()
	var catalog []contracts.Permission
	for _, m := range append([]module.Module{identityPermissions()}, everyModule()...) {
		catalog = append(catalog, m.Permissions...)
	}
	never := map[string]bool{}
	for _, key := range contracts.APINever(catalog) {
		never[key] = true
	}
	for _, name := range openapi.Modules {
		doc, err := openapi.Load(t.Context(), name)
		if err != nil {
			t.Fatal(err)
		}
		for path, item := range doc.Paths.Map() {
			for method, op := range item.Operations() {
				access, _ := op.Extensions["x-vantigo-access"].(string)
				keys, ok := strings.CutPrefix(access, "permission:")
				if !ok || op.Extensions[contracts.APIExtension] == contracts.APIExtensionNever {
					continue
				}
				for _, key := range strings.Split(keys, "+") {
					if never[key] {
						t.Errorf("%s: %s %s (%s) needs %s, which no API key holds: mark it x-vantigo-api: never", name, method, path, op.OperationID, key)
						break
					}
				}
			}
		}
	}
}
