package web

import (
	"encoding/json"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/vantigo-io/vantigo/server/internal/brandscale"
	"github.com/vantigo-io/vantigo/server/internal/config"
)

const brandIndexHTML = `<!doctype html>
<html lang="en">
  <head>
    <meta charset="UTF-8" />
    <link rel="stylesheet" href="/api/v1/identity/brand.css" />
    <title>Vantigo</title>
    <script type="module" crossorigin src="/assets/index-abc123.js"></script>
  </head>
  <body><div id="root"></div></body>
</html>`

func TestNewIndex_TheBrandStylesheetIsRewrittenUnderTheBasePath(t *testing.T) {
	_, html := renderIndex(t, "/crm", config.Branding{}, brandIndexHTML, nil)
	assertContains(t, html, `<link rel="stylesheet" href="/crm/api/v1/identity/brand.css"`)
	_, root := renderIndex(t, "", config.Branding{}, brandIndexHTML, nil)
	assertContains(t, root, `href="/api/v1/identity/brand.css"`)
}

// B17: the environment's branding stays injected until the default brand has
// imported it, and is dropped after.
func TestNewImportedIndex_DropsTheEnvironmentsBranding(t *testing.T) {
	b := config.Branding{Title: "Acme CRM", LogoURL: "https://cdn.acme.test/logo.png", SupportEmail: "help@acme.test"}
	_, before := renderIndex(t, "", b, brandIndexHTML, []string{"customers"})
	assertContains(t, before, `"title":"Acme CRM"`, `"logoUrl":"https://cdn.acme.test/logo.png"`, `"email":"help@acme.test"`, "<title>Acme CRM</title>")

	idx, err := NewImportedIndex(fstest.MapFS{"index.html": {Data: []byte(brandIndexHTML)}}, "/crm", []string{"customers"})
	if err != nil {
		t.Fatal(err)
	}
	after := string(idx.HTML)
	assertContains(t, after, `"basePath":"/crm/"`, `"modules":["customers"]`, "<title>Vantigo</title>", `href="/crm/api/v1/identity/brand.css"`)
	assertNotContains(t, after, `"title"`, `"logoUrl"`, `"support"`, "Acme")
	if idx.InlineScriptHash == "" || strings.Count(after, "<script>") != 1 {
		t.Errorf("want exactly one inline script with a hash:\n%s", after)
	}
}

func TestSnippet_StaysOneScriptAndNamesTheMirrorsContract(t *testing.T) {
	for _, want := range []string{
		`localStorage.getItem("vantigo-brand")`, `"--vantigo-brand-loaded"`, `m.v!==1`, `m.algorithm<=` + strconv.Itoa(brandscale.Algorithm),
		`typeof m.userId!=="string"`, `addEventListener("error"`, `addEventListener("DOMContentLoaded"`,
	} {
		if !strings.Contains(colorSchemeScript, want) {
			t.Errorf("the snippet is missing %q", want)
		}
	}
	if strings.Contains(colorSchemeScript, "`") || strings.Contains(colorSchemeScript, "</script") {
		t.Error("the snippet cannot carry a backtick or close the script element")
	}
}

// runSnippet runs the pre-paint snippet in node against a fake document and
// local storage and answers what it did. Skipped where node is not installed
// (the frontend gate always has it).
func runSnippet(t *testing.T, storage map[string]any, markerLoaded bool, extra string) map[string]any {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("node is required to test the pre-paint snippet in CI")
		}
		t.Skip("node is not installed")
	}
	store, _ := json.Marshal(storage)
	script := `
const store = ` + string(store) + `;
const listeners = {};
const props = {};
const attrs = {};
globalThis.localStorage = { getItem: (k) => { if (store.__throw) throw new Error("blocked"); return k in store ? (typeof store[k] === "string" ? store[k] : JSON.stringify(store[k])) : null; } };
globalThis.matchMedia = () => ({ matches: false });
globalThis.getComputedStyle = () => ({ getPropertyValue: (n) => (n === "--vantigo-brand-loaded" && ` + boolJS(markerLoaded) + ` ? "1" : "") });
globalThis.document = {
  title: "Vantigo",
  documentElement: { setAttribute: (k, v) => { attrs[k] = v; }, style: { setProperty: (k, v) => { props[k] = v; } } },
  addEventListener: (type, fn) => { listeners[type] = fn; },
};
` + colorSchemeScript + `;
` + extra + `
console.log(JSON.stringify({ props, attrs, title: document.title }));
`
	out, err := exec.Command(node, "-e", script).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	var res map[string]any
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	return res
}

func boolJS(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func mirror(mod map[string]any) map[string]any {
	m := map[string]any{
		"v": 1, "algorithm": brandscale.Algorithm, "userId": "u-1", "title": "Acme",
		"light": map[string]any{"--mantine-color-vantigo-6": "#0f766e", "--vantigo-brand-loaded": "1"},
		"dark":  map[string]any{"--mantine-color-vantigo-6": "#14b8a6"},
	}
	for k, v := range mod {
		if v == nil {
			delete(m, k)
		} else {
			m[k] = v
		}
	}
	return map[string]any{"vantigo-brand": m}
}

const fireDomReady = `listeners.DOMContentLoaded();`
const fireLinkError = `listeners.error({ target: { tagName: "LINK", href: "https://x.test/api/v1/identity/brand.css" } });`

func props(res map[string]any) map[string]any { return res["props"].(map[string]any) }

func TestSnippet_AppliesTheMirrorOnlyWhenTheStylesheetDidNotLoad(t *testing.T) {
	t.Run("the stylesheet loaded: nothing is applied", func(t *testing.T) {
		res := runSnippet(t, mirror(nil), true, fireDomReady)
		if len(props(res)) != 0 || res["title"] != "Vantigo" {
			t.Errorf("result = %v", res)
		}
	})
	t.Run("the stylesheet is missing once the document is parsed: the mirror paints", func(t *testing.T) {
		res := runSnippet(t, mirror(nil), false, fireDomReady)
		if props(res)["--mantine-color-vantigo-6"] != "#0f766e" || res["title"] != "Acme" {
			t.Errorf("result = %v", res)
		}
	})
	t.Run("the link's error event paints the dark values in dark", func(t *testing.T) {
		res := runSnippet(t, map[string]any{
			"vantigo-color-scheme": "dark", "vantigo-brand": mirror(nil)["vantigo-brand"],
		}, false, fireLinkError)
		if props(res)["--mantine-color-vantigo-6"] != "#14b8a6" || res["attrs"].(map[string]any)["data-mantine-color-scheme"] != "dark" {
			t.Errorf("result = %v", res)
		}
	})
	t.Run("an error event of another element is ignored", func(t *testing.T) {
		res := runSnippet(t, mirror(nil), false, `listeners.error({ target: { tagName: "IMG", href: "x/brand.css" } });`)
		if len(props(res)) != 0 {
			t.Errorf("result = %v", res)
		}
	})
}

func TestSnippet_IgnoresAMirrorItDoesNotUnderstand(t *testing.T) {
	for name, mod := range map[string]map[string]any{
		"a higher v":                        {"v": 2},
		"a newer algorithm":                 {"algorithm": brandscale.Algorithm + 1},
		"no user id":                        {"userId": nil},
		"an empty user id":                  {"userId": ""},
		"values that are not css":           {"light": map[string]any{"--x": "url(javascript:alert(1))"}},
		"a property that is not a variable": {"light": map[string]any{"color": "red"}},
	} {
		t.Run(name, func(t *testing.T) {
			res := runSnippet(t, mirror(mod), false, fireDomReady)
			if len(props(res)) != 0 {
				t.Errorf("applied %v", props(res))
			}
		})
	}
	t.Run("not json", func(t *testing.T) {
		res := runSnippet(t, map[string]any{"vantigo-brand": "{nope"}, false, fireDomReady)
		if len(props(res)) != 0 {
			t.Errorf("applied %v", props(res))
		}
	})
}

func TestSnippet_SurvivesAThrowingLocalStorage(t *testing.T) {
	res := runSnippet(t, map[string]any{"__throw": true}, false, fireDomReady)
	if len(props(res)) != 0 || res["title"] != "Vantigo" {
		t.Errorf("result = %v", res)
	}
}
