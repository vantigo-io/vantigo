package identity

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/brandscale"
)

const (
	scaleGoldenPath  = "../../../../packages/frontend-shell/src/brand/brand-scale.golden.json"
	tokensGoldenPath = "../../../../packages/frontend-shell/src/brand/brand-tokens.golden.json"
)

// goldenBrand is one brand of the tokens golden file: its inputs and what the
// server generates from them, which the frontend test compares with what a
// MantineProvider derives (brands design B36).
type goldenBrand struct {
	Name    string          `json:"name"`
	Primary string          `json:"primary"`
	Radius  string          `json:"radius"`
	Tokens  json.RawMessage `json:"tokens"`
}

func goldenInputs(t *testing.T) []goldenBrand {
	t.Helper()
	raw, err := os.ReadFile(scaleGoldenPath)
	if err != nil {
		t.Fatalf("read the scale golden file: %v", err)
	}
	var file struct {
		Cases []struct {
			Name  string `json:"name"`
			Input string `json:"input"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatalf("decode the scale golden file: %v", err)
	}
	radii := []string{"md", "xs", "sm", "lg", "xl"}
	var out []goldenBrand
	for i, c := range file.Cases {
		if c.Input == "" {
			continue
		}
		out = append(out, goldenBrand{Name: c.Name, Primary: c.Input, Radius: radii[i%len(radii)]})
	}
	return out
}

// The tokens golden file is what the server generates for the scale golden
// file's brands. Run with UPDATE_GOLDEN=1 to rewrite it; the frontend test
// reads it, so server and Mantine cannot drift.
func TestBrandTokens_GoldenFileMatches(t *testing.T) {
	brands := goldenInputs(t)
	var kept []goldenBrand
	for _, b := range brands {
		tokens, _, err := computeTokens(strings.ToLower(b.Primary), b.Radius)
		if err != nil {
			continue // a primary the range rule refuses is not a brand
		}
		raw, err := json.Marshal(tokens)
		if err != nil {
			t.Fatal(err)
		}
		b.Tokens = raw
		kept = append(kept, b)
	}
	if len(kept) < 5 {
		t.Fatalf("only %d golden brands", len(kept))
	}
	want, err := json.MarshalIndent(map[string]any{"algorithm": brandscale.Algorithm, "brands": kept}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	want = append(want, '\n')
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(tokensGoldenPath, want, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(tokensGoldenPath)
	if err != nil {
		t.Fatalf("read the tokens golden file (UPDATE_GOLDEN=1 writes it): %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("brand-tokens.golden.json is stale: run UPDATE_GOLDEN=1 go test ./internal/identity -run TestBrandTokens_GoldenFileMatches")
	}
}

func TestBrandTokens_VantigoIndigoKeepsItsOwnScale(t *testing.T) {
	tokens, res, err := computeTokens("#4F46E5", "md")
	if err != nil {
		t.Fatal(err)
	}
	if tokens.Light.Shades != builtinShades || tokens.Dark.Shades != builtinShades {
		t.Errorf("the built-in primary does not carry Vantigo's own scale: %v", tokens.Light.Shades)
	}
	if res.FilledText != brandscale.White || tokens.Light.Link != "#3730a3" || tokens.Dark.Link != "#a5b4fc" || tokens.Light.SubtleBg != "#eef2ff" {
		t.Errorf("the built-in brand's link and tint are not the D-A literals: %+v", tokens.Light)
	}
	if !BrandChecksPass("#4f46e5") {
		t.Error("Vantigo's own brand must pass every contrast check")
	}
}

func TestBrandTokens_AnotherPrimaryIsGenerated(t *testing.T) {
	tokens, _, err := computeTokens("#0d9488", "lg")
	if err != nil {
		t.Fatal(err)
	}
	if tokens.Light.Shades[6] != "#0d9488" || tokens.Light.Shades == builtinShades {
		t.Errorf("shades = %v", tokens.Light.Shades)
	}
	if tokens.Radius != "calc(1rem * var(--mantine-scale))" {
		t.Errorf("radius = %q", tokens.Radius)
	}
	// Mantine's alpha() and darken(), as the dark scheme's light variants use.
	if got := tokens.Dark.Derived["--mantine-color-vantigo-outline-hover"]; !strings.HasPrefix(got, "rgba(") || !strings.HasSuffix(got, ", 0.05)") {
		t.Errorf("outline-hover = %q", got)
	}
	if got := overCard("#6366f1", brandscale.DarkTintPercent); got != "#2b2d55" {
		t.Errorf("the dark tint of #6366f1 = %q, want shade 5 over the card at 24 percent", got)
	}
}

func TestBrandTokens_RefuseBadInputs(t *testing.T) {
	for _, c := range []struct{ primary, radius string }{
		{"#ffffff", "md"}, {"#000000", "md"}, {"not-a-colour", "md"}, {"#4f46e5", "huge"},
	} {
		if _, _, err := computeTokens(c.primary, c.radius); err == nil {
			t.Errorf("computeTokens(%q, %q) = nil, want an error", c.primary, c.radius)
		}
	}
}

// B37: the editor and the import share one validation, and each of these is a
// support contact that must be refused.
func TestBrandSupport_RefusesWhatConfigRefuses(t *testing.T) {
	bad := []brandSupport{
		{URL: "javascript:alert(1)"},
		{URL: "//evil.example"},
		{URL: `/\evil.example`},
		{URL: "help/page"},
		{URL: "ftp://example.com"},
		{URL: "/help\tpage"}, {URL: "/help\rpage"}, {URL: "/help\npage"}, {URL: "https://example.com/a\nb"},
		{URL: "java\tscript:alert(1)"}, {URL: "/\t/evil.example"},
		{Email: `"a?cc=x@evil.example"@example.com`}, {Email: `"a&b"@example.com`}, {Email: `"a%40b"@example.com`},
		{Email: "Support <support@example.com>"},
		{Email: "not an address"},
		{Phone: "+47 22 33 44 55 ext"},
		{Phone: "tel:+4722334455"},
		{Phone: "1234567890123456789012345678901"}, // 31 characters
	}
	for _, s := range bad {
		if _, got := validateSupport(s); got == nil {
			t.Errorf("validateSupport(%+v) accepted it", s)
		}
	}
	good := []brandSupport{
		{}, {URL: "/help"}, {URL: "https://help.example.com/a?b=c"}, {URL: "http://example.com"},
		{Email: "support@example.com"}, {Phone: "+47 (22) 33.44-55"}, {Phone: "123456789012345678901234567890"},
	}
	for _, s := range good {
		if _, got := validateSupport(s); got != nil {
			t.Errorf("validateSupport(%+v) = %v, want valid", s, got)
		}
	}
}

// B16: the stylesheet is built only from generated values.
func TestBrandCSS_ContainsOnlyGeneratedValues(t *testing.T) {
	v, refusal := newBrandVersion(brandInput{Name: `x"}</style><script>`, Title: "<b>t</b>", Primary: "#0d9488", Radius: "sm",
		Support: brandSupport{URL: "/help"}}, nil)
	if refusal != nil {
		t.Fatal(refusal.message)
	}
	css := brandCSS(v)
	for _, bad := range []string{"<", ">", "script", "x\"", "help", "t</b>"} {
		if strings.Contains(css, bad) {
			t.Errorf("the stylesheet contains %q:\n%s", bad, css)
		}
	}
	for _, want := range []string{":root{--vantigo-brand-loaded:1;", "--mantine-color-vantigo-6:#0d9488;", `:root[data-mantine-color-scheme="dark"]{`, "--mantine-radius-default:calc(0.25rem * var(--mantine-scale));"} {
		if !strings.Contains(css, want) {
			t.Errorf("the stylesheet is missing %q:\n%s", want, css)
		}
	}
}

func TestBrandVersion_DefaultsTitleToNameAndTrims(t *testing.T) {
	v, refusal := newBrandVersion(brandInput{Name: "  Acme  ", Primary: "#0D9488"}, nil)
	if refusal != nil {
		t.Fatal(refusal.message)
	}
	if v.Name != "Acme" || v.Title != "Acme" || v.Primary != "#0d9488" || v.Radius != "md" {
		t.Errorf("version = %+v", v)
	}
	for _, in := range []brandInput{
		{Name: "", Primary: "#0d9488"},
		{Name: strings.Repeat("n", 101), Primary: "#0d9488"},
		{Name: "a", Title: strings.Repeat("t", 61), Primary: "#0d9488"},
		{Name: "a", Primary: "red"},
	} {
		if _, refusal := newBrandVersion(in, nil); refusal == nil {
			t.Errorf("newBrandVersion(%+v) was accepted", in)
		}
	}
}
