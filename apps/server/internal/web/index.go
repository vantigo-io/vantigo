package web

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"io/fs"
	"regexp"
	"strings"

	"github.com/vantigo-io/vantigo/server/internal/config"
)

// DefaultTitle is the document title when APP_TITLE is not set.
const DefaultTitle = "Vantigo"

// Index is the SPA entry document, templated once at startup so one image
// serves any base path and branding without rebuilding the frontend.
type Index struct {
	HTML []byte
	// InlineScriptHash is the CSP source expression ('sha256-…') for the one
	// inline script the document carries. It is derived from the same string
	// that is injected, so the policy cannot drift from the script.
	InlineScriptHash string
}

// NewIndex reads index.html from assets and templates it: asset URLs are
// rewritten under basePath, window.__VANTIGO_APP__ (branding plus the enabled
// module names) is injected as the first element of <head>, and <title> is
// replaced.
//
// The environment's branding (APP_TITLE, APP_LOGO_URL, APP_SUPPORT_*) is
// injected exactly as it always was: it stays honoured until the default brand
// has imported it (brands design B17, B30). Once it has, NewImportedIndex is
// used instead.
func NewIndex(assets fs.FS, basePath string, b config.Branding, modules []string) (*Index, error) {
	return newIndex(assets, basePath, &b, modules)
}

// NewImportedIndex is NewIndex for an installation whose default brand has
// imported the environment's branding: title, logoUrl and support are dropped
// from the injected script and the static title is the product's own. The
// real values reach the page from the brand stylesheet and the brand payload
// (brands design B16, B17).
func NewImportedIndex(assets fs.FS, basePath string, modules []string) (*Index, error) {
	return newIndex(assets, basePath, nil, modules)
}

func newIndex(assets fs.FS, basePath string, b *config.Branding, modules []string) (*Index, error) {
	raw, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		return nil, fmt.Errorf("web: read index.html: %w", err)
	}
	script := runtimeConfigScript(basePath, b, modules)
	pageTitle := DefaultTitle
	if b != nil {
		pageTitle = title(*b)
	}
	sum := sha256.Sum256([]byte(script))
	return &Index{
		HTML:             []byte(render(string(raw), basePath, pageTitle, script)),
		InlineScriptHash: "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'",
	}, nil
}

var titleElement = regexp.MustCompile(`(?s)<title>.*?</title>`)

func render(doc, basePath, title, script string) string {
	if basePath != "" {
		doc = strings.ReplaceAll(doc, `src="/`, `src="`+basePath+`/`)
		doc = strings.ReplaceAll(doc, `href="/`, `href="`+basePath+`/`)
	}
	// The config must exist before any module script in <head> runs.
	doc = strings.Replace(doc, "<head>", "<head><script>"+script+"</script>", 1)
	if loc := titleElement.FindStringIndex(doc); loc != nil {
		doc = doc[:loc[0]] + "<title>" + html.EscapeString(title) + "</title>" + doc[loc[1]:]
	}
	return doc
}

// runtimeConfig is the shape the frontends read from window.__VANTIGO_APP__
// (see packages/frontend-shell/src/app-config.ts). Unset values are null;
// Modules is always an array so the SPA can tell "none enabled" ([]) from
// "not injected" (absent, as under the Vite dev server).
type runtimeConfig struct {
	BasePath string         `json:"basePath"`
	Title    string         `json:"title"`
	LogoURL  *string        `json:"logoUrl"`
	Support  runtimeSupport `json:"support"`
	Modules  []string       `json:"modules"`
}

// importedConfig is the shape once the default brand holds the branding: the
// title, logo and support are no longer injected (brands design B17).
type importedConfig struct {
	BasePath string   `json:"basePath"`
	Modules  []string `json:"modules"`
}

type runtimeSupport struct {
	Email *string `json:"email"`
	Phone *string `json:"phone"`
	URL   *string `json:"url"`
}

// runtimeConfigScript is the exact text of the injected script. Both the
// document and the CSP hash come from this one function. encoding/json escapes
// <, > and &, so no value can close the <script> element.
func runtimeConfigScript(basePath string, b *config.Branding, modules []string) string {
	var cfg any = importedConfig{BasePath: basePath + "/", Modules: append([]string{}, modules...)}
	if b != nil {
		cfg = runtimeConfig{
			BasePath: basePath + "/",
			Title:    title(*b),
			LogoURL:  optional(b.LogoURL),
			Support:  runtimeSupport{Email: optional(b.SupportEmail), Phone: optional(b.SupportPhone), URL: optional(b.SupportURL)},
			Modules:  append([]string{}, modules...),
		}
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		panic("web: marshal runtime config: " + err.Error()) // impossible for this type
	}
	return "window.__VANTIGO_APP__=" + string(data) + ";" + colorSchemeScript
}

// colorSchemeScript runs before the first paint, so the page never flashes
// light for someone who uses dark. It reads the scheme the SPA mirrors to
// local storage (Mantine's localStorageColorSchemeManager, the key set in
// packages/frontend-shell/src/theme.ts: "light", "dark" or "auto") and falls
// back to the computer's setting, then sets the attribute Mantine's
// stylesheet and the shell's tokens switch on. It is part of the one injected
// script, so the CSP hash covers it; it is wrapped in try/catch because local
// storage throws when a browser blocks it, and a failure must leave the page
// readable, not blank.
//
// It then arms the brand's fallback (brands design B16). The brand arrives as
// a render-blocking stylesheet whose first rule sets --vantigo-brand-loaded;
// only when that stylesheet could not load (offline, a failed request: the
// link's error event, caught on the way down because it does not bubble, or
// the marker still missing once the document is parsed) does the script paint
// what the browser last mirrored to localStorage["vantigo-brand"], and only a
// mirror of a version it understands: v 1, algorithm 1 or older, with a user
// id. The mirror is the SPA's to write and to clear on sign-out; the variables
// it sets here are removed again when the SPA has the brand from the server.
const colorSchemeScript = `try{var s=localStorage.getItem("vantigo-color-scheme");` +
	`if(s!=="light"&&s!=="dark"){s=matchMedia("(prefers-color-scheme: dark)").matches?"dark":"light"}` +
	`document.documentElement.setAttribute("data-mantine-color-scheme",s)}catch(e){}` +
	`var vb=function(){try{var r=document.documentElement;` +
	`if(getComputedStyle(r).getPropertyValue("--vantigo-brand-loaded"))return;` +
	`var m=JSON.parse(localStorage.getItem("vantigo-brand")),v=m&&m[s];` +
	`if(!v||m.v!==1||!(m.algorithm<=1)||typeof m.userId!=="string"||!m.userId)return;` +
	`for(var k in v){if(k.slice(0,2)==="--"&&/^[#a-z0-9(),.% *-]+$/i.test(v[k]))r.style.setProperty(k,v[k])}` +
	`if(typeof m.title==="string")document.title=m.title}catch(e){}};` +
	`document.addEventListener("error",function(e){var t=e.target;if(t&&t.tagName==="LINK"&&/brand\.css/.test(t.href))vb()},true);` +
	`document.addEventListener("DOMContentLoaded",vb)`

func title(b config.Branding) string {
	if t := strings.TrimSpace(b.Title); t != "" {
		return t
	}
	return DefaultTitle
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
