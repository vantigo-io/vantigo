package identity

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/vantigo-io/vantigo/server/internal/brandscale"
)

// This file is a brand's generated values (brands design B2, B3, B36): from the
// inputs (a primary colour and a radius) it yields, for each colour scheme,
// the ten shades of the `vantigo` colour, the filled-text colour, the link and
// tint colours and the variables Mantine derives from the shades (and, for the
// dark tint, the shell's own rule, which its cssVariablesResolver gives
// Mantine). The client
// never sends them and the server never trusts them: they are recomputed
// whenever a draft is saved. The TypeScript twin is
// packages/frontend-shell/src/brand/tokens.ts; both are tested against
// brand-tokens.golden.json, and a frontend test mounts a MantineProvider with
// the brand's theme and compares every variable, so a Mantine upgrade that
// changes a rule fails instead of drifting.

// builtinPrimary is Vantigo's own indigo. Its scale is the hand-tuned one the
// product shipped with (theme.ts), not the generated one, so that an
// installation that never edits its brand looks exactly as it did: the rule is
// a function of the input, mirrored in the editor's preview.
const builtinPrimary = "#4f46e5"

var builtinShades = [10]string{
	"#eef2ff", "#e0e7ff", "#c7d2fe", "#a5b4fc", "#818cf8",
	"#6366f1", "#4f46e5", "#4338ca", "#3730a3", "#312e81",
}

// brandRadii are the accepted radius inputs, with the value Mantine gives
// --mantine-radius-default for each (theme.radius, rem(2) .. rem(32)).
var brandRadii = map[string]string{
	"xs": "calc(0.125rem * var(--mantine-scale))",
	"sm": "calc(0.25rem * var(--mantine-scale))",
	"md": "calc(0.5rem * var(--mantine-scale))",
	"lg": "calc(1rem * var(--mantine-scale))",
	"xl": "calc(2rem * var(--mantine-scale))",
}

// defaultBrandRadius is Mantine's `md`, the theme's defaultRadius.
const defaultBrandRadius = "md"

// schemeTokens is one colour scheme's generated values.
type schemeTokens struct {
	Shades     [10]string `json:"shades"`
	FilledText string     `json:"filledText"`
	Link       string     `json:"link"`
	SubtleBg   string     `json:"subtleBg"`
	// Derived is the Mantine variables computed from the shades, as ready CSS
	// variable values, keyed by the variable's name (B36).
	Derived map[string]string `json:"derived"`
}

// brandTokens are both schemes' values plus the radius variable.
type brandTokens struct {
	Light  schemeTokens `json:"light"`
	Dark   schemeTokens `json:"dark"`
	Radius string       `json:"radius"`
}

// errBrandRadius is a radius input that is not one of the five.
var errBrandRadius = errors.New("identity: the radius is not xs, sm, md, lg or xl")

// scaleOf is the scale and filled-text colour of a primary: the product's own
// tuned scale for Vantigo's indigo, the generated one for anything else.
// Errors are brandscale's (ErrInvalidHex, ErrPrimaryRange).
func scaleOf(primary string) (brandscale.Result, error) {
	if strings.EqualFold(primary, builtinPrimary) {
		return brandscale.Result{Algorithm: brandscale.Algorithm, Scale: builtinShades, FilledText: brandscale.White}, nil
	}
	return brandscale.Generate(primary)
}

// computeTokens is everything a brand's inputs generate.
func computeTokens(primary, radius string) (brandTokens, brandscale.Result, error) {
	res, err := scaleOf(primary)
	if err != nil {
		return brandTokens{}, res, err
	}
	rv, ok := brandRadii[radius]
	if !ok {
		return brandTokens{}, res, errBrandRadius
	}
	s := res.Scale
	light := schemeTokens{
		Shades: s, FilledText: res.FilledText, Link: s[8], SubtleBg: s[0],
		Derived: map[string]string{
			"--mantine-color-vantigo-text":          "var(--mantine-color-vantigo-filled)",
			"--mantine-color-vantigo-filled":        "var(--mantine-color-vantigo-6)",
			"--mantine-color-vantigo-filled-hover":  "var(--mantine-color-vantigo-7)",
			"--mantine-color-vantigo-light":         "var(--mantine-color-vantigo-1)",
			"--mantine-color-vantigo-light-hover":   "var(--mantine-color-vantigo-2)",
			"--mantine-color-vantigo-light-color":   "var(--mantine-color-vantigo-9)",
			"--mantine-color-vantigo-outline":       "var(--mantine-color-vantigo-6)",
			"--mantine-color-vantigo-outline-hover": withAlpha(s[6], 0.05),
			"--mantine-primary-color-contrast":      cssContrast(res.FilledText),
		},
	}
	dark := schemeTokens{
		Shades: s, FilledText: res.FilledText, Link: s[3], SubtleBg: overCard(s[5], brandscale.DarkTintPercent),
		Derived: map[string]string{
			"--mantine-color-vantigo-text":          "var(--mantine-color-vantigo-4)",
			"--mantine-color-vantigo-filled":        "var(--mantine-color-vantigo-6)",
			"--mantine-color-vantigo-filled-hover":  "var(--mantine-color-vantigo-7)",
			"--mantine-color-vantigo-light":         overCard(s[5], brandscale.DarkTintPercent),
			"--mantine-color-vantigo-light-hover":   overCard(s[5], brandscale.DarkTintHoverPercent),
			"--mantine-color-vantigo-light-color":   "var(--mantine-color-vantigo-1)",
			"--mantine-color-vantigo-outline":       "var(--mantine-color-vantigo-2)",
			"--mantine-color-vantigo-outline-hover": withAlpha(s[2], 0.05),
			"--mantine-primary-color-contrast":      cssContrast(res.FilledText),
		},
	}
	return brandTokens{Light: light, Dark: dark, Radius: rv}, res, nil
}

// cssContrast is Mantine's --mantine-primary-color-contrast for a filled text.
func cssContrast(filledText string) string {
	if filledText == brandscale.Black {
		return "var(--mantine-color-black)"
	}
	return "var(--mantine-color-white)"
}

// channels parses "#rrggbb" (validated upstream: the scale produced it).
func channels(hex string) (r, g, b int) {
	v, _ := strconv.ParseUint(strings.TrimPrefix(hex, "#"), 16, 32)
	return int(v >> 16 & 255), int(v >> 8 & 255), int(v & 255)
}

// withAlpha is Mantine's alpha(): rgba(r, g, b, a).
func withAlpha(hex string, a float64) string {
	r, g, b := channels(hex)
	return fmt.Sprintf("rgba(%d, %d, %d, %s)", r, g, b, strconv.FormatFloat(a, 'f', -1, 64))
}

// overCard is the colour laid over the dark card (brandscale.OverCard): the
// dark scheme's tint and its hover. The scale produced the colour, so it parses.
func overCard(hex string, percent int) string {
	out, _ := brandscale.OverCard(hex, percent)
	return out
}
