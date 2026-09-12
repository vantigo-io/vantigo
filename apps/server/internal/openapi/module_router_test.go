package openapi_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/openapi"
)

// noopAccess satisfies contracts.Access without ever being asked to decide
// anything: this file only registers routes, it never serves a request.
type noopAccess struct{}

func (noopAccess) Check(*http.Request, contracts.Rule) (contracts.Principal, error) {
	return contracts.Principal{}, nil
}

func (noopAccess) Reject(http.ResponseWriter, *http.Request, contracts.Rule, error) {}

// TestKnownServeMuxConflictsMountOnModuleRouter proves the precedence-aware
// matcher internal/module/router.go builds resolves every pair
// openapi.KnownServeMuxConflicts pins: two operations stdlib http.ServeMux
// itself refuses to register together (TestServeMuxConflictsArePinned) must
// still both mount cleanly — no problem from Err() — on the router that
// actually serves the customers and products contracts.
func TestKnownServeMuxConflictsMountOnModuleRouter(t *testing.T) {
	for _, pair := range openapi.KnownServeMuxConflicts {
		a, b, ok := strings.Cut(pair, " ⟷ ")
		if !ok {
			t.Fatalf("pair %q has no ⟷ separator", pair)
		}
		t.Run(pair, func(t *testing.T) {
			r := module.NewRouter(module.RouterOptions{Doc: twoOperationDoc(t, a, b), Access: noopAccess{}})
			r.HandleFunc(a, func(http.ResponseWriter, *http.Request) {})
			r.HandleFunc(b, func(http.ResponseWriter, *http.Request) {})
			if err := r.Err(); err != nil {
				t.Errorf("module.NewRouter refused to mount %s and %s together: %v", a, b, err)
			}
		})
	}
}

// twoOperationDoc builds a minimal contract with exactly the two operations
// named by patterns ("METHOD /path" each), every one anonymous, so
// registering them on a module.Router is the only thing under test.
func twoOperationDoc(t *testing.T, patterns ...string) *openapi3.T {
	t.Helper()
	var sb strings.Builder
	sb.WriteString("openapi: 3.0.3\ninfo: {title: t, version: \"1\"}\npaths:\n")
	for i, pattern := range patterns {
		method, path, ok := strings.Cut(pattern, " ")
		if !ok {
			t.Fatalf("pattern %q has no method", pattern)
		}
		fmt.Fprintf(&sb, "  %s:\n    %s:\n      operationId: op%d\n      x-vantigo-access: anonymous\n      responses: {\"204\": {description: ok}}\n", path, strings.ToLower(method), i)
	}
	doc, err := openapi3.NewLoader().LoadFromData([]byte(sb.String()))
	if err != nil {
		t.Fatalf("build contract:\n%s\n%v", sb.String(), err)
	}
	return doc
}
