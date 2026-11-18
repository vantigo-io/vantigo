package integration_test

import (
	"net/http"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/openapi"
)

// webhooksPage is the reference page that lists every catalogue event.
const webhooksPage = "../../../../docs/src/content/docs/en/reference/webhooks.md"

// TestWebhookEvents_CatalogueMatchesTheCode holds the webhook catalogue
// (outgoing webhooks design W4, W26) to the code it names: each entry is a
// version 1 contract event whose payload has the IDField; its Path is a GET of
// the composed contract with one path parameter; that GET's access rule
// names exactly the entry's Permissions; each permission is in the composed
// catalog, delegable and not APINever; and the GET is not x-vantigo-api:
// never, so a receiver's key can fetch what it was told about.
func TestWebhookEvents_CatalogueMatchesTheCode(t *testing.T) {
	t.Parallel()
	catalog := map[string]contracts.Permission{}
	var all []contracts.Permission
	for _, m := range append([]module.Module{identityPermissions()}, everyModule()...) {
		for _, p := range m.Permissions {
			catalog[p.Key] = p
		}
		all = append(all, m.Permissions...)
	}
	never := contracts.APINever(all)

	gets := map[string]map[string]any{} // path -> the GET's extensions
	for _, name := range openapi.Modules {
		doc, err := openapi.Load(t.Context(), name)
		if err != nil {
			t.Fatal(err)
		}
		for path, item := range doc.Paths.Map() {
			if op := item.GetOperation(http.MethodGet); op != nil {
				gets[path] = op.Extensions
			}
		}
	}

	events := contracts.WebhookEvents()
	if len(events) == 0 {
		t.Fatal("the catalogue is empty")
	}
	seen := map[string]bool{}
	for _, e := range events {
		name := e.Name()
		if seen[name] {
			t.Errorf("%s is listed twice", name)
		}
		seen[name] = true
		if e.Version != 1 || e.Version != e.Event.EventVersion() {
			t.Errorf("%s: version %d, the event's is %d; only version 1 is exposed", name, e.Version, e.Event.EventVersion())
		}
		if e.Module() == "communications" || e.Module() == "expenses" || e.Module() == "time" {
			t.Errorf("%s concerns people and is left out (W5)", name)
		}
		if !hasJSONField(reflect.TypeOf(e.Event), e.IDField) {
			t.Errorf("%s: its payload has no field %q", name, e.IDField)
		}
		if strings.Count(e.Path, "{") != 1 {
			t.Errorf("%s: path %s must have exactly one parameter, the id", name, e.Path)
		}
		ext, ok := gets[e.Path]
		if !ok {
			t.Errorf("%s: no GET %s in the contract", name, e.Path)
			continue
		}
		access, _ := ext["x-vantigo-access"].(string)
		keys, ok := strings.CutPrefix(access, "permission:")
		if !ok {
			t.Errorf("%s: GET %s is %q, not a permission rule", name, e.Path, access)
			continue
		}
		rule := strings.Split(keys, "+")
		slices.Sort(rule)
		want := slices.Clone(e.Permissions)
		slices.Sort(want)
		if !slices.Equal(rule, want) {
			t.Errorf("%s: GET %s needs %v, the catalogue says %v", name, e.Path, rule, want)
		}
		for _, key := range e.Permissions {
			p, ok := catalog[key]
			switch {
			case !ok:
				t.Errorf("%s: %s is not in the permission catalog", name, key)
			case !p.Delegable:
				t.Errorf("%s: %s cannot be delegated", name, key)
			case slices.Contains(never, key):
				t.Errorf("%s: %s is one no API key holds", name, key)
			}
		}
		if ext[contracts.APIExtension] == contracts.APIExtensionNever {
			t.Errorf("%s: GET %s is x-vantigo-api: never, so no receiver could fetch it", name, e.Path)
		}
	}
	for _, left := range []string{contracts.SupplierInvoiceDeleted{}.EventName()} {
		if seen[left] {
			t.Errorf("%s is left out: its GET answers 404 for a deleted draft (W26)", left)
		}
	}
}

// TestWebhookEvents_TheReferenceListsEveryEvent holds reference/webhooks.md
// to the catalogue (W4): every event name is on the page, in code.
func TestWebhookEvents_TheReferenceListsEveryEvent(t *testing.T) {
	t.Parallel()
	page, err := os.ReadFile(webhooksPage)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range contracts.WebhookEvents() {
		if !strings.Contains(string(page), "`"+e.Name()+"`") {
			t.Errorf("reference/webhooks.md does not list `%s`", e.Name())
		}
	}
	if strings.Contains(string(page), "`"+contracts.SupplierInvoiceDeleted{}.EventName()+"`") &&
		!strings.Contains(string(page), "left out") {
		t.Errorf("reference/webhooks.md names %s without saying it is left out", contracts.SupplierInvoiceDeleted{}.EventName())
	}
}

// hasJSONField reports whether struct type t has a field whose JSON name is
// name.
func hasJSONField(t reflect.Type, name string) bool {
	if t.Kind() != reflect.Struct {
		return false
	}
	for f := range t.Fields() {
		tag, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if tag == name {
			return true
		}
	}
	return false
}
