package brandscale_test

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/brandscale"
)

// update rewrites the golden file from this package's output:
// go test ./internal/brandscale -run TestGolden -update-golden
// and then format it with the repository's formatter (bunx biome check --write).
// Go is the truth; the TypeScript port is tested against what this writes.
var update = flag.Bool("update-golden", false, "rewrite brand-scale.golden.json")

// goldenPath is the one file both suites read, from this package's directory.
var goldenPath = filepath.Join("..", "..", "..", "..", "packages", "frontend-shell", "src", "brand", "brand-scale.golden.json")

var themeCSS = filepath.Join("..", "..", "..", "..", "packages", "frontend-shell", "src", "theme.css")

// goldenInputs are the golden file's accepted inputs: Vantigo's indigo, one
// near each end of the accepted lightness range, a saturated blue, a grey, a
// near-grey, a brand green, a red and a yellow.
var goldenInputs = []struct{ name, hex string }{
	{"indigo", "#4f46e5"},
	{"very light", "#e2cc5a"},
	{"very dark", "#3d2b85"},
	{"saturated", "#0066ff"},
	{"grey", "#808080"},
	{"near grey", "#7c8088"},
	{"green", "#16a34a"},
	{"red", "#dc2626"},
	{"yellow", "#eab308"},
	{"teal", "#0d9488"},
	{"magenta", "#ff00ff"},
	{"pure blue", "#0000ff"},
	{"pure red", "#ff0000"},
	{"upper case", "#4F46E5"},
	{"just white text", "#757575"},
	{"just black text", "#767676"},
	// Common brand colours whose black filled text sat on a darker hover that
	// failed AA before algorithm 2 (B3.9).
	{"brand red", "#ef4444"},
	{"brand orange", "#f97316"},
	{"brand blue", "#3b82f6"},
}

// goldenRefused are inputs outside the accepted range, and the error code.
var goldenRefused = []struct{ hex, code string }{
	{"#ffffff", "brand_primary_range"},
	{"#000000", "brand_primary_range"},
	{"#fde047", "brand_primary_range"},
	{"#1e1b4b", "brand_primary_range"},
	{"4f46e5", "brand_hex"},
	{"#fff", "brand_hex"},
	{"#000", "brand_hex"},
	{"#4f46e5ff", "brand_hex"},
	{"#ggg000", "brand_hex"},
	{"#4f46e5 ", "brand_hex"},
}

func errCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, brandscale.ErrPrimaryRange):
		return "brand_primary_range"
	case errors.Is(err, brandscale.ErrInvalidHex):
		return "brand_hex"
	}
	return err.Error()
}

type goldenCase struct {
	Name       string               `json:"name"`
	Input      string               `json:"input"`
	Scale      []string             `json:"scale"`
	FilledText string               `json:"filledText"`
	Contrast   map[string][]float64 `json:"contrast"`
	Suggested  *string              `json:"suggested"`
}

type goldenFile struct {
	Algorithm int          `json:"algorithm"`
	Pairs     []string     `json:"pairs"`
	Cases     []goldenCase `json:"cases"`
	Refused   []struct {
		Input string `json:"input"`
		Error string `json:"error"`
	} `json:"refused"`
}

func round2(v float64) float64 { return math.Floor(v*100+0.5) / 100 }

func quoted(list []string) string {
	q := make([]string, len(list))
	for i, s := range list {
		q[i] = fmt.Sprintf("%q", s)
	}
	return "[" + strings.Join(q, ", ") + "]"
}

func ratios(checks []brandscale.Check, scheme brandscale.Scheme) []float64 {
	var out []float64
	for _, c := range checks {
		if c.Scheme == scheme {
			out = append(out, round2(c.Ratio))
		}
	}
	return out
}

func numbers(v []float64) string {
	s := make([]string, len(v))
	for i, f := range v {
		s[i] = fmt.Sprintf("%g", f)
	}
	return "[" + strings.Join(s, ", ") + "]"
}

// render is the golden file's text: one case per few lines, the scale on one.
func render(t *testing.T) string {
	t.Helper()
	pairs := make([]string, len(brandscale.Pairs))
	for i, p := range brandscale.Pairs {
		pairs[i] = string(p)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "{\n  \"algorithm\": %d,\n  \"pairs\": %s,\n  \"cases\": [\n", brandscale.Algorithm, quoted(pairs))
	for i, in := range goldenInputs {
		res, err := brandscale.Generate(in.hex)
		if err != nil {
			t.Fatalf("%s: %v", in.name, err)
		}
		checks := brandscale.Checks(res)
		suggested := "null"
		if s, ok := brandscale.Suggest(in.hex); ok {
			suggested = fmt.Sprintf("%q", s)
		}
		fmt.Fprintf(&b, "    {\n      \"name\": %q,\n      \"input\": %q,\n      \"scale\": %s,\n      \"filledText\": %q,\n",
			in.name, in.hex, quoted(res.Scale[:]), res.FilledText)
		fmt.Fprintf(&b, "      \"contrast\": { \"light\": %s, \"dark\": %s },\n      \"suggested\": %s\n    }",
			numbers(ratios(checks, brandscale.Light)), numbers(ratios(checks, brandscale.Dark)), suggested)
		if i < len(goldenInputs)-1 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString("  ],\n  \"refused\": [\n")
	for i, r := range goldenRefused {
		_, err := brandscale.Generate(r.hex)
		if errCode(err) != r.code {
			t.Fatalf("%s: error %q, want %q", r.hex, errCode(err), r.code)
		}
		fmt.Fprintf(&b, "    { \"input\": %q, \"error\": %q }", r.hex, r.code)
		if i < len(goldenRefused)-1 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString("  ]\n}\n")
	return b.String()
}

// The golden file is what this package produces, and what the TypeScript port
// is tested against, so the two cannot drift.
func TestGolden(t *testing.T) {
	want := render(t)
	if *update {
		if err := os.WriteFile(goldenPath, []byte(want), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatal(err)
	}
	// Compared as data, not text: the repository's formatter lays the file out
	// its own way, so after -update-golden run `bunx biome check --write` on it.
	var gotData, wantData any
	if err := json.Unmarshal(got, &gotData); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(want), &wantData); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotData, wantData) {
		t.Errorf("%s is out of date; run go test ./internal/brandscale -run TestGolden -update-golden", goldenPath)
	}
	var f goldenFile
	if err := json.Unmarshal(got, &f); err != nil {
		t.Fatal(err)
	}
	if f.Algorithm != brandscale.Algorithm || len(f.Cases) != len(goldenInputs) {
		t.Fatalf("golden algorithm %d with %d cases", f.Algorithm, len(f.Cases))
	}
	for _, c := range f.Cases {
		res, err := brandscale.Generate(c.Input)
		if err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
		if strings.Join(res.Scale[:], ",") != strings.Join(c.Scale, ",") || res.FilledText != c.FilledText {
			t.Errorf("%s: %v %s, golden %v %s", c.Name, res.Scale, res.FilledText, c.Scale, c.FilledText)
		}
	}
}

func mustFilled(t *testing.T, bg string) string {
	t.Helper()
	got, err := brandscale.FilledText(bg)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func mustContrast(t *testing.T, a, b string) float64 {
	t.Helper()
	got, err := brandscale.Contrast(a, b)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func mustTint(t *testing.T, shade9 string) string {
	t.Helper()
	got, err := brandscale.DarkTint(shade9)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// The helpers refuse what is not #rrggbb, as the TypeScript port throws.
func TestHelpers_RefuseInvalidHex(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{"", "#fff", "4f46e5", "#4f46e5 ", "#ggg000", "#4f46e5ff"} {
		if _, err := brandscale.FilledText(bad); !errors.Is(err, brandscale.ErrInvalidHex) {
			t.Errorf("FilledText(%q) = %v", bad, err)
		}
		if _, err := brandscale.Contrast(bad, "#ffffff"); !errors.Is(err, brandscale.ErrInvalidHex) {
			t.Errorf("Contrast(%q, white) = %v", bad, err)
		}
		if _, err := brandscale.Contrast("#ffffff", bad); !errors.Is(err, brandscale.ErrInvalidHex) {
			t.Errorf("Contrast(white, %q) = %v", bad, err)
		}
		if _, err := brandscale.DarkTint(bad); !errors.Is(err, brandscale.ErrInvalidHex) {
			t.Errorf("DarkTint(%q) = %v", bad, err)
		}
	}
}

func mustGenerate(t *testing.T, hex string) brandscale.Result {
	t.Helper()
	res, err := brandscale.Generate(hex)
	if err != nil {
		t.Fatalf("%s: %v", hex, err)
	}
	return res
}

// lab returns the OKLab components of a colour via the exported lightness and
// a hue from the scale, for the property tests: hue in degrees and chroma.
func hueChroma(t *testing.T, hex string) (hue, chroma float64) {
	t.Helper()
	l, a, b := brandscale.OKLab(hex)
	_ = l
	return math.Mod(math.Atan2(b, a)*180/math.Pi+360, 360), math.Hypot(a, b)
}

// The properties of B3 hold for every golden input.
func TestGenerate_Properties(t *testing.T) {
	t.Parallel()
	for _, in := range goldenInputs {
		res := mustGenerate(t, in.hex)
		if res.Algorithm != 2 {
			t.Errorf("%s: algorithm %d", in.name, res.Algorithm)
		}
		if res.Scale[6] != strings.ToLower(in.hex) {
			t.Errorf("%s: shade 6 = %s, want the input byte for byte", in.name, res.Scale[6])
		}
		hue6, chroma6 := hueChroma(t, in.hex)
		// Every shade is in gamut before rounding: no channel is outside
		// [0, 1] by more than the 1e-6 tolerance.
		for i, rgb := range brandscale.ShadeLinear(in.hex) {
			for _, v := range rgb {
				if v < -1e-6 || v > 1+1e-6 {
					t.Errorf("%s: shade %d has a linear channel %v out of gamut", in.name, i, v)
				}
			}
		}
		prev := 2.0
		for i, shade := range res.Scale {
			if !regexp.MustCompile(`^#[0-9a-f]{6}$`).MatchString(shade) {
				t.Errorf("%s: shade %d = %q", in.name, i, shade)
			}
			l, _, _ := brandscale.OKLab(shade)
			// Strictly decreasing from 0 to 9, to the precision 8-bit rounding
			// allows between neighbours (shade 6 is the exact input).
			if l >= prev {
				t.Errorf("%s: shade %d has lightness %.4f, not below shade %d's %.4f", in.name, i, l, i-1, prev)
			}
			prev = l
			if chroma6 >= 0.02 && i != 0 && i != 9 {
				h, c := hueChroma(t, shade)
				if c > 0.02 {
					d := math.Abs(h - hue6)
					d = min(d, 360-d)
					if d > 2 {
						t.Errorf("%s: shade %d hue %.2f is %.2f degrees from the input's %.2f", in.name, i, h, d, hue6)
					}
				}
			}
		}
	}
}

// A primary outside 0.35 <= L <= 0.85 is refused, one inside is not, and the
// input must be #rrggbb.
func TestGenerate_Range(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ hex, code string }{
		{"#ffffff", "brand_primary_range"},
		{"#000000", "brand_primary_range"},
		{"#4f46e5", ""},
		{"#4F46E5", ""},
		{"#4f46e", "brand_hex"},
		{"#4f46e5ff", "brand_hex"},
		{"4f46e5", "brand_hex"},
		{"#4f46eg", "brand_hex"},
		{"", "brand_hex"},
	} {
		_, err := brandscale.Generate(c.hex)
		if errCode(err) != c.code {
			t.Errorf("Generate(%q) = %q, want %q", c.hex, errCode(err), c.code)
		}
	}
	if res := mustGenerate(t, "#4F46E5"); res.Scale[6] != "#4f46e5" {
		t.Errorf("an upper-case input gives shade 6 %q", res.Scale[6])
	}
}

// Every shade is the in-gamut colour of its step: the golden inputs include a
// saturated blue and a red, whose chroma does not fit at every lightness.
func TestGenerate_Deterministic(t *testing.T) {
	t.Parallel()
	for _, in := range goldenInputs {
		a, b := mustGenerate(t, in.hex), mustGenerate(t, in.hex)
		if a != b {
			t.Errorf("%s: two runs differ", in.name)
		}
	}
}

// FilledText is white up to the luminance threshold of 0.179 and black above
// it, and one of the two always passes AA on the shade.
func TestFilledText_FlipsAtTheThreshold(t *testing.T) {
	t.Parallel()
	// Grey levels around the threshold: luminance of #767676 is 0.181, of
	// #757575 is 0.178.
	for _, c := range []struct{ bg, want string }{
		{"#000000", brandscale.White},
		{"#4f46e5", brandscale.White},
		{"#757575", brandscale.White},
		{"#767676", brandscale.Black},
		{"#ffffff", brandscale.Black},
		{"#eab308", brandscale.Black},
	} {
		if got := mustFilled(t, c.bg); got != c.want {
			t.Errorf("FilledText(%s) = %s, want %s", c.bg, got, c.want)
		}
		if mustContrast(t, mustFilled(t, c.bg), c.bg) < brandscale.MinContrast {
			t.Errorf("the filled text on %s fails AA", c.bg)
		}
	}
	// Every grey passes, so no shade 6 of any lightness can fail its own text.
	for v := 0; v < 256; v++ {
		bg := fmt.Sprintf("#%02x%02x%02x", v, v, v)
		if mustContrast(t, mustFilled(t, bg), bg) < brandscale.MinContrast {
			t.Errorf("the filled text on %s fails AA", bg)
		}
	}
}

// WCAG's own anchors: 21:1 for black on white, 1:1 for a colour on itself.
func TestContrast_Anchors(t *testing.T) {
	t.Parallel()
	if got := mustContrast(t, "#000000", "#ffffff"); math.Abs(got-21) > 1e-9 {
		t.Errorf("black on white = %v", got)
	}
	if got := mustContrast(t, "#4f46e5", "#4f46e5"); got != 1 {
		t.Errorf("a colour on itself = %v", got)
	}
	if a, b := mustContrast(t, "#4f46e5", "#ffffff"), mustContrast(t, "#ffffff", "#4f46e5"); a != b {
		t.Errorf("contrast is not symmetric: %v %v", a, b)
	}
}

func TestChecks_ThePairs(t *testing.T) {
	t.Parallel()
	res := mustGenerate(t, "#4f46e5")
	checks := brandscale.Checks(res)
	if len(checks) != 10 {
		t.Fatalf("%d checks, want 10", len(checks))
	}
	want := []struct {
		scheme brandscale.Scheme
		pair   brandscale.Pair
		fg, bg string
	}{
		{brandscale.Light, brandscale.PairFilled, "#ffffff", res.Scale[6]},
		{brandscale.Light, brandscale.PairFilledHover, "#ffffff", res.Scale[7]},
		{brandscale.Light, brandscale.PairLinkPage, res.Scale[8], "#f7f8fc"},
		{brandscale.Light, brandscale.PairLinkCard, res.Scale[8], "#ffffff"},
		{brandscale.Light, brandscale.PairLinkTint, res.Scale[8], res.Scale[0]},
		{brandscale.Dark, brandscale.PairFilled, "#ffffff", res.Scale[6]},
		{brandscale.Dark, brandscale.PairFilledHover, "#ffffff", res.Scale[7]},
		{brandscale.Dark, brandscale.PairLinkPage, res.Scale[3], "#15182a"},
		{brandscale.Dark, brandscale.PairLinkCard, res.Scale[3], "#1c1f32"},
		{brandscale.Dark, brandscale.PairLinkTint, res.Scale[3], mustTint(t, res.Scale[9])},
	}
	for i, w := range want {
		c := checks[i]
		if c.Scheme != w.scheme || c.Pair != w.pair || c.Foreground != w.fg || c.Background != w.bg {
			t.Errorf("check %d = %+v, want %+v", i, c, w)
		}
		if c.Pass != (c.Ratio >= 4.5) || c.Ratio != mustContrast(t, w.fg, w.bg) {
			t.Errorf("check %d ratio %v pass %v", i, c.Ratio, c.Pass)
		}
	}
}

// Mantine halves shade 9 (each channel, rounded half-up) for the dark tint.
func TestDarkTint(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{"#000000": "#000000", "#ffffff": "#808080", "#0102ff": "#010180", "#4f46e5": "#282373"} {
		if got := mustTint(t, in); got != want {
			t.Errorf("DarkTint(%s) = %s, want %s", in, got, want)
		}
	}
}

// The suggestion is the input when it already passes, and otherwise a primary
// that does pass, close to the input in lightness and with its hue.
func TestSuggest(t *testing.T) {
	t.Parallel()
	got, ok := brandscale.Suggest("#4f46e5")
	if !ok || got != "#4f46e5" {
		t.Errorf("a passing primary suggests %q %v", got, ok)
	}
	for _, in := range goldenInputs {
		got, ok := brandscale.Suggest(in.hex)
		if !ok {
			t.Errorf("%s: no suggestion", in.name)
			continue
		}
		res, err := brandscale.Generate(got)
		if err != nil || !brandscale.Passes(brandscale.Checks(res)) {
			t.Errorf("%s: the suggestion %s does not pass (%v)", in.name, got, err)
		}
		l0, _, _ := brandscale.OKLab(in.hex)
		l1, _, _ := brandscale.OKLab(got)
		if in.hex != got && math.Abs(l1-l0) > 0.3 {
			t.Errorf("%s: the suggestion %s moved lightness %.2f", in.name, got, l1-l0)
		}
	}
	// Since algorithm 2 every accepted primary passes (B3.9), so a suggestion is
	// the input itself: the common brand colours that used to fail the hover.
	for _, hex := range []string{"#ef4444", "#f97316", "#3b82f6", "#808080", "#16a34a"} {
		if got, ok := brandscale.Suggest(hex); !ok || got != hex {
			t.Errorf("%s suggests %s (%v), want itself", hex, got, ok)
		}
	}
	if _, ok := brandscale.Suggest("#ffffff"); ok {
		t.Error("a primary out of range has no suggestion")
	}
}

// The page, card and text colours in the Go constants are the token layer's.
func TestTokens_MatchThemeCSS(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(themeCSS)
	if err != nil {
		t.Fatal(err)
	}
	css := string(raw)
	dark := strings.Index(css, `[data-mantine-color-scheme="dark"]`)
	if dark < 0 {
		t.Fatal("no dark block in theme.css")
	}
	value := func(block, name string) string {
		m := regexp.MustCompile(`--vantigo-` + name + `:\s*(#[0-9a-fA-F]{6})\s*;`).FindStringSubmatch(block)
		if m == nil {
			t.Fatalf("no --vantigo-%s in the block", name)
		}
		return strings.ToLower(m[1])
	}
	for name, want := range map[string]string{
		"body-bg": brandscale.PageLight, "surface": brandscale.CardLight, "text": brandscale.TextLight,
	} {
		if got := value(css[:dark], name); got != want {
			t.Errorf("light --vantigo-%s = %s in theme.css, %s in brandscale", name, got, want)
		}
	}
	for name, want := range map[string]string{
		"body-bg": brandscale.PageDark, "surface": brandscale.CardDark, "text": brandscale.TextDark,
	} {
		if got := value(css[dark:], name); got != want {
			t.Errorf("dark --vantigo-%s = %s in theme.css, %s in brandscale", name, got, want)
		}
	}
}

// B3.9: the filled text passes on shade 6 and on the hover (shade 7) in every
// golden input, and the three common brand colours are accepted for publish.
func TestGenerate_HoverKeepsTheFilledTextReadable(t *testing.T) {
	t.Parallel()
	for _, in := range goldenInputs {
		res := mustGenerate(t, in.hex)
		for _, i := range []int{6, 7} {
			ratio, err := brandscale.Contrast(res.FilledText, res.Scale[i])
			if err != nil || ratio < brandscale.MinContrast {
				t.Errorf("%s: %s on shade %d = %.2f, want at least 4.5", in.name, res.FilledText, i, ratio)
			}
		}
	}
	for _, hex := range []string{"#ef4444", "#f97316", "#3b82f6"} {
		res := mustGenerate(t, hex)
		if !brandscale.Passes(brandscale.Checks(res)) {
			t.Errorf("%s: a common brand colour fails publish: %+v", hex, brandscale.Checks(res))
		}
		if res.FilledText != brandscale.Black {
			t.Errorf("%s: filled text %s, want black", hex, res.FilledText)
		}
	}
}

// A white-text brand keeps the generated shade 7, which only gets darker.
func TestGenerate_WhiteTextBrandsKeepTheirHover(t *testing.T) {
	t.Parallel()
	res := mustGenerate(t, "#4f46e5")
	if res.FilledText != brandscale.White || res.Scale[7] != "#3e2ec4" {
		t.Errorf("shade 7 of Vantigo's indigo = %s with %s text", res.Scale[7], res.FilledText)
	}
}
