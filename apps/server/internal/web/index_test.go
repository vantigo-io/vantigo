package web

import (
	"crypto/sha256"
	"encoding/base64"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/vantigo-io/vantigo/server/internal/config"
)

const builtIndexHTML = `<!doctype html>
<html lang="en">
  <head>
    <meta charset="UTF-8" />
    <link rel="icon" type="image/png" href="/favicon.png" />
    <title>Vantigo</title>
    <script type="module" crossorigin src="/assets/index-abc123.js"></script>
    <link rel="stylesheet" crossorigin href="/assets/index-def456.css">
  </head>
  <body>
    <div id="root"></div>
  </body>
</html>`

func renderIndex(t *testing.T, basePath string, b config.Branding, html string, modules []string) (*Index, string) {
	t.Helper()
	idx, err := NewIndex(fstest.MapFS{"index.html": {Data: []byte(html)}}, basePath, b, modules)
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}
	return idx, string(idx.HTML)
}

func assertContains(t *testing.T, html string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(html, want) {
			t.Errorf("document is missing %q:\n%s", want, html)
		}
	}
}

func assertNotContains(t *testing.T, html string, unwanted ...string) {
	t.Helper()
	for _, s := range unwanted {
		if strings.Contains(html, s) {
			t.Errorf("document contains %q:\n%s", s, html)
		}
	}
}

func TestNewIndex_AtTheRootKeepsAssetURLs(t *testing.T) {
	_, html := renderIndex(t, "", config.Branding{}, builtIndexHTML, nil)
	assertContains(t, html, `src="/assets/index-abc123.js"`, `href="/favicon.png"`, `"basePath":"/"`)
}

func TestNewIndex_UnderABasePathRewritesEveryAssetURL(t *testing.T) {
	_, html := renderIndex(t, "/crm", config.Branding{}, builtIndexHTML, nil)
	assertContains(t, html,
		`src="/crm/assets/index-abc123.js"`,
		`href="/crm/assets/index-def456.css"`,
		`href="/crm/favicon.png"`,
		`"basePath":"/crm/"`)
	assertNotContains(t, html, `src="/assets/`, `href="/favicon.png"`)
}

func TestNewIndex_InjectsTheConfigBeforeAnyModuleScript(t *testing.T) {
	_, html := renderIndex(t, "", config.Branding{}, builtIndexHTML, nil)
	head := strings.Index(html, "<head>")
	script := strings.Index(html, "window.__VANTIGO_APP__")
	module := strings.Index(html, `type="module"`)
	if head < 0 || script < head || script > module {
		t.Errorf("config script at %d is not between <head> at %d and the module script at %d", script, head, module)
	}
}

func TestNewIndex_DefaultBrandingInjectsNulls(t *testing.T) {
	_, html := renderIndex(t, "", config.Branding{}, builtIndexHTML, nil)
	assertContains(t, html,
		`window.__VANTIGO_APP__={"basePath":"/","title":"Vantigo","logoUrl":null,"support":{"email":null,"phone":null,"url":null},"modules":[]};`)
}

func TestNewIndex_InjectsTheEnabledModules(t *testing.T) {
	idx, err := NewIndex(fstest.MapFS{"index.html": {Data: []byte(builtIndexHTML)}}, "", config.Branding{}, []string{"customers", "energy"})
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}
	assertContains(t, string(idx.HTML), `"modules":["customers","energy"]`)
}

func TestNewIndex_FullBrandingInjectsEveryValue(t *testing.T) {
	_, html := renderIndex(t, "", config.Branding{
		Title:        "Acme ERP",
		LogoURL:      "https://cdn.acme.test/logo.svg",
		SupportEmail: "help@acme.test",
		SupportPhone: "+47 123 45 678",
		SupportURL:   "https://support.acme.test",
	}, builtIndexHTML, nil)
	assertContains(t, html,
		`"title":"Acme ERP"`,
		`"logoUrl":"https://cdn.acme.test/logo.svg"`,
		`"email":"help@acme.test"`,
		`"phone":"+47 123 45 678"`,
		`"url":"https://support.acme.test"`)
}

func TestNewIndex_ReplacesTheDocumentTitle(t *testing.T) {
	_, html := renderIndex(t, "", config.Branding{Title: "Acme ERP"}, builtIndexHTML, nil)
	assertContains(t, html, "<title>Acme ERP</title>")
	assertNotContains(t, html, "<title>Vantigo</title>")
}

func TestNewIndex_WithoutATitleElementStillInjectsTheConfig(t *testing.T) {
	_, html := renderIndex(t, "/crm", config.Branding{}, `<html><head><script src="/assets/a.js"></script></head></html>`, nil)
	assertContains(t, html, "window.__VANTIGO_APP__", `src="/crm/assets/a.js"`)
	assertNotContains(t, html, "<title>")
}

func TestNewIndex_HostileTitleCannotBreakOutOfTheScriptOrTitle(t *testing.T) {
	_, html := renderIndex(t, "", config.Branding{Title: `</script><script>alert(1)</script>`}, builtIndexHTML, nil)
	assertNotContains(t, html, "<script>alert(1)</script>")
	assertContains(t, html,
		`\u003c/script\u003e`,
		"<title>&lt;/script&gt;&lt;script&gt;alert(1)&lt;/script&gt;</title>")
}

func TestNewIndex_QuotesAndUnicodeProduceValidJSONAndHTML(t *testing.T) {
	_, html := renderIndex(t, "", config.Branding{Title: `Møller "Bil" & Co`}, builtIndexHTML, nil)
	assertContains(t, html,
		`"title":"Møller \"Bil\" \u0026 Co"`,
		"<title>Møller &#34;Bil&#34; &amp; Co</title>")
}

func TestNewIndex_HostileLogoURLCannotBreakOutOfTheScript(t *testing.T) {
	// config rejects this value; the renderer must be safe on its own anyway.
	_, html := renderIndex(t, "", config.Branding{LogoURL: `x"};</script><script>alert(1)//`}, builtIndexHTML, nil)
	assertNotContains(t, html, "</script><script>alert(1)")
}

func TestNewIndex_ScriptHashCoversExactlyTheInjectedScript(t *testing.T) {
	idx, html := renderIndex(t, "/crm", config.Branding{Title: "Acme"}, builtIndexHTML, nil)

	start := strings.Index(html, "<head><script>") + len("<head><script>")
	end := start + strings.Index(html[start:], "</script>")
	sum := sha256.Sum256([]byte(html[start:end]))
	want := "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"

	if idx.InlineScriptHash != want {
		t.Errorf("InlineScriptHash = %s, want %s (the hash of the script actually injected)", idx.InlineScriptHash, want)
	}
}

func TestNewIndex_LeavesUnrelatedContentUntouched(t *testing.T) {
	_, html := renderIndex(t, "/crm", config.Branding{}, builtIndexHTML, nil)
	assertContains(t, html, `<div id="root"></div>`, `<meta charset="UTF-8" />`)
}

func TestNewIndex_MissingIndexIsAnError(t *testing.T) {
	if _, err := NewIndex(fstest.MapFS{}, "", config.Branding{}, nil); err == nil {
		t.Fatal("NewIndex succeeded without an index.html")
	}
}

func TestAssets_ContainsAnIndexDocument(t *testing.T) {
	if _, err := fs.ReadFile(Assets(), "index.html"); err != nil {
		t.Fatalf("embedded build has no index.html: %v", err)
	}
}
