package integration_test

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/openapi"
	"github.com/vantigo-io/vantigo/server/internal/openapi/contracttest"
)

// noNetwork refuses every request the installation would send to another
// host, so the walk never reaches a real provider.
type noNetwork struct{}

func (noNetwork) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("the contract walk sends nothing to another host")
}

// callsAProvider are the operations that answer from an external provider —
// Bring, Folio, Graph, Google, Meta, Peppol, Brønnøysund, an AI model — whose
// failure with no network is the provider's, not the key's. The walk skips
// them; each one's key path is the router's like any other's. None needs it
// today: with no network and no provider configured every one of them
// answers its own 503 (ai_unavailable, mail_unavailable, ehf_unavailable),
// which the walk accepts.
var callsAProvider = map[string]bool{}

// TestAPIKey_TheWholeContract walks every operation of every module the
// server mounts with an integration's key, the integration holding a role
// with every permission an API key may hold (API access design §11, K7,
// K9): an operation marked x-vantigo-api: never, or behind a rule a key never
// meets, is 403 api_not_available, and every other one gets past the access
// layer — never a 401, never a 500 (a provider's 503 is the provider's).
func TestAPIKey_TheWholeContract(t *testing.T) {
	mods := everyModule()
	opts := []modtest.Option{modtest.WithRecorder(contracttest.NewForModule("customers")), modtest.WithTransport(noNetwork{})}
	for _, m := range mods {
		opts = append(opts, modtest.WithModule(m))
	}
	h := modtest.New(t, opts...)

	var catalog []contracts.Permission
	for _, m := range append([]module.Module{identityPermissions()}, mods...) {
		catalog = append(catalog, m.Permissions...)
	}
	never := contracts.APINever(catalog)
	var keys []string
	for _, p := range catalog {
		if !slices.Contains(never, p.Key) {
			keys = append(keys, p.Key)
		}
	}
	c, _ := h.SignInAPIKey(t, modtest.APIKey{}, keys...)

	walked := 0
	for _, name := range openapi.Modules {
		doc, err := openapi.Load(context.Background(), name)
		if err != nil {
			t.Fatal(err)
		}
		for path, item := range doc.Paths.Map() {
			if strings.HasPrefix(path, "/api/v1/identity/scim/") {
				continue // the SCIM ingress answers before the router, to its own bearer token only
			}
			for method, op := range item.Operations() {
				if callsAProvider[op.OperationID] {
					continue
				}
				concrete := concretePath(path, append(item.Parameters, op.Parameters...))
				reqOpts := []modtest.RequestOption{modtest.SkipContract("an API key walking the contract")}
				var body any
				if method != http.MethodGet && method != http.MethodDelete && method != http.MethodHead {
					body = map[string]any{}
				}
				if op.RequestBody != nil && op.RequestBody.Value != nil && op.RequestBody.Value.Content.Get("multipart/form-data") != nil {
					body = nil
					reqOpts = append(reqOpts, modtest.RawBody("multipart/form-data; boundary=x", []byte("--x--\r\n")))
				}
				res := c.Do(method, concrete, body, reqOpts...)
				walked++
				access, _ := op.Extensions["x-vantigo-access"].(string)
				keyable := access == "anonymous" || strings.HasPrefix(access, "permission:")
				if op.Extensions[contracts.APIExtension] == contracts.APIExtensionNever || !keyable {
					if res.Status != http.StatusForbidden || (method != http.MethodHead && codeOf(res) != "api_not_available") {
						t.Errorf("%s %s (%s): %d %s, want 403 api_not_available", method, concrete, op.OperationID, res.Status, truncate(res.Body))
					}
					continue
				}
				if res.Status == http.StatusUnauthorized || res.Status == http.StatusInternalServerError ||
					(res.Status == http.StatusForbidden && slices.Contains([]string{"api_not_available", "forbidden", "api_key_read_only"}, codeOf(res))) {
					t.Errorf("%s %s (%s): %d %s, want past the access layer", method, concrete, op.OperationID, res.Status, truncate(res.Body))
				}
			}
		}
	}
	if walked < 1000 {
		t.Fatalf("walked %d operations; the walk is broken", walked)
	}
}
