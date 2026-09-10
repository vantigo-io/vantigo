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
// rewritten under basePath, window.__VANTIGO_APP__ is injected as the first
// element of <head>, and <title> is replaced.
func NewIndex(assets fs.FS, basePath string, b config.Branding) (*Index, error) {
	raw, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		return nil, fmt.Errorf("web: read index.html: %w", err)
	}
	script := runtimeConfigScript(basePath, b)
	sum := sha256.Sum256([]byte(script))
	return &Index{
		HTML:             []byte(render(string(raw), basePath, title(b), script)),
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
// (see apps/*/frontend/src/lib/app-config.ts). Unset values are null.
type runtimeConfig struct {
	BasePath string         `json:"basePath"`
	Title    string         `json:"title"`
	LogoURL  *string        `json:"logoUrl"`
	Support  runtimeSupport `json:"support"`
}

type runtimeSupport struct {
	Email *string `json:"email"`
	Phone *string `json:"phone"`
	URL   *string `json:"url"`
}

// runtimeConfigScript is the exact text of the injected script. Both the
// document and the CSP hash come from this one function. encoding/json escapes
// <, > and &, so no value can close the <script> element.
func runtimeConfigScript(basePath string, b config.Branding) string {
	data, err := json.Marshal(runtimeConfig{
		BasePath: basePath + "/",
		Title:    title(b),
		LogoURL:  optional(b.LogoURL),
		Support: runtimeSupport{
			Email: optional(b.SupportEmail),
			Phone: optional(b.SupportPhone),
			URL:   optional(b.SupportURL),
		},
	})
	if err != nil {
		panic("web: marshal runtime config: " + err.Error()) // impossible for this type
	}
	return "window.__VANTIGO_APP__=" + string(data) + ";"
}

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
