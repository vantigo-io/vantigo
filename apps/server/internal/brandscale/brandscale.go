// Package brandscale generates a brand's 10-shade colour scale from one hex
// colour and checks the contrast of the pairs the brand touches (brands design
// B3 and B4). The algorithm is a rule of the design, because the server (this
// package, the truth, which validates and stores) and the editor's live
// preview (packages/frontend-shell/src/brand/scale.ts, a port) must produce the
// same bytes: both are tested against one golden file,
// packages/frontend-shell/src/brand/brand-scale.golden.json.
//
// It is a platform package: pure functions over the standard library, no
// business module, and any module may import it (identity stores brands;
// invoices will print them).
//
// To keep the two languages identical the arithmetic is written in one fixed
// order, every product is rounded to float64 before it is added (Go may
// otherwise fuse a multiply and an add on some CPUs; JavaScript never does),
// and the hue is never put through trigonometry: chroma is scaled on the OKLab
// a and b components, which keeps the hue exactly.
package brandscale

import (
	"errors"
	"math"
)

// Algorithm is the version of the constants below. It is stored with every
// published token set; changing any constant is a new version, and old sets
// are recomputed only by an explicit admin action (B3.8). Version 2 keeps the
// filled text readable on the hover colour (shade 7, B3.9).
const Algorithm = 2

// The accepted lightness of the primary (B3.3) and the AA ratio for text.
const (
	MinLightness = 0.35
	MaxLightness = 0.85
	MinContrast  = 4.5

	// LuminanceThreshold is Mantine's autoContrast threshold, set explicitly in
	// the theme (Mantine's own default is 0.3): the filled text is white at or
	// below it and black above it. At the threshold white has 4.58 : 1 and
	// black 4.58 : 1, so one of the two always passes AA.
	LuminanceThreshold = 0.179

	White = "#ffffff"
	Black = "#000000"
)

// The two ways a primary is refused.
var (
	// ErrInvalidHex is an input that is not "#rrggbb".
	ErrInvalidHex = errors.New("brandscale: the colour is not #rrggbb")
	// ErrPrimaryRange is a primary whose OKLCH lightness is outside
	// [MinLightness, MaxLightness]: brand_primary_range.
	ErrPrimaryRange = errors.New("brandscale: the primary's lightness is outside 0.35 to 0.85")
)

// The scale's shape (B3.4 and B3.5): t and u place shades 0 to 5 and 7 to 9 on
// the lightness axis, k scales the chroma of all ten.
var (
	lightSteps = [6]float64{1.00, 0.86, 0.68, 0.50, 0.32, 0.16}
	darkSteps  = [3]float64{0.33, 0.66, 1.00}
	chromaK    = [10]float64{0.12, 0.25, 0.45, 0.68, 0.88, 1.00, 1.00, 0.95, 0.85, 0.72}
)

const (
	lightEnd       = 0.975
	darkEnd        = 0.28
	gamutTolerance = 1e-6
	gamutSteps     = 24
	suggestStep    = 0.005
	suggestMax     = 200
)

// Björn Ottosson's two published matrices, and their inverses (B3.1).
var (
	linearToLMS = [3][3]float64{
		{0.4122214708, 0.5363325363, 0.0514459929},
		{0.2119034982, 0.6806995451, 0.1073969566},
		{0.0883024619, 0.2817188376, 0.6299787005},
	}
	lmsToLab = [3][3]float64{
		{0.2104542553, 0.7936177850, -0.0040720468},
		{1.9779984951, -2.4285922050, 0.4505937099},
		{0.0259040371, 0.7827717662, -0.8086757660},
	}
	labToLMS = [3][3]float64{
		{1, 0.3963377774, 0.2158037573},
		{1, -0.1055613458, -0.0638541728},
		{1, -0.0894841775, -1.2914855480},
	}
	lmsToLinear = [3][3]float64{
		{4.0767416621, -3.3077115913, 0.2309699292},
		{-1.2684380046, 2.6097574011, -0.3413193965},
		{-0.0041960863, -0.7034186147, 1.7076147010},
	}
)

// dot is a row times a column, each product rounded before the sums.
func dot(m [3]float64, x, y, z float64) float64 {
	return float64(m[0]*x) + float64(m[1]*y) + float64(m[2]*z)
}

// oklab is a colour in OKLab: lightness, and the two opponent axes.
type oklab struct{ l, a, b float64 }

func parseHex(hex string) (r, g, b int, err error) {
	if len(hex) != 7 || hex[0] != '#' {
		return 0, 0, 0, ErrInvalidHex
	}
	var v [3]int
	for i := range v {
		hi, ok1 := hexDigit(hex[1+2*i])
		lo, ok2 := hexDigit(hex[2+2*i])
		if !ok1 || !ok2 {
			return 0, 0, 0, ErrInvalidHex
		}
		v[i] = hi<<4 | lo
	}
	return v[0], v[1], v[2], nil
}

func hexDigit(c byte) (int, bool) {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0'), true
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10, true
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10, true
	}
	return 0, false
}

func formatHex(r, g, b int) string {
	const digits = "0123456789abcdef"
	return string([]byte{'#',
		digits[r>>4], digits[r&15], digits[g>>4], digits[g&15], digits[b>>4], digits[b&15]})
}

// decode is an sRGB channel in [0, 1] as linear light (IEC 61966-2-1).
func decode(c float64) float64 {
	if c <= 0.04045 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

// encode is linear light as an sRGB channel.
func encode(c float64) float64 {
	if c <= 0.0031308 {
		return float64(12.92 * c)
	}
	return float64(1.055*math.Pow(c, 1/2.4)) - 0.055
}

func toOKLab(r, g, b int) oklab {
	lr, lg, lb := decode(float64(r)/255), decode(float64(g)/255), decode(float64(b)/255)
	l := math.Cbrt(dot(linearToLMS[0], lr, lg, lb))
	m := math.Cbrt(dot(linearToLMS[1], lr, lg, lb))
	s := math.Cbrt(dot(linearToLMS[2], lr, lg, lb))
	return oklab{dot(lmsToLab[0], l, m, s), dot(lmsToLab[1], l, m, s), dot(lmsToLab[2], l, m, s)}
}

// linear is the linear sRGB of c, unclipped.
func linear(c oklab) (r, g, b float64) {
	l := dot(labToLMS[0], c.l, c.a, c.b)
	m := dot(labToLMS[1], c.l, c.a, c.b)
	s := dot(labToLMS[2], c.l, c.a, c.b)
	l, m, s = l*l*l, m*m*m, s*s*s
	return dot(lmsToLinear[0], l, m, s), dot(lmsToLinear[1], l, m, s), dot(lmsToLinear[2], l, m, s)
}

func inGamut(c oklab) bool {
	r, g, b := linear(c)
	for _, v := range [3]float64{r, g, b} {
		if v < -gamutTolerance || v > 1+gamutTolerance {
			return false
		}
	}
	return true
}

// fit is c with its chroma scaled into sRGB (B3.6): c itself when it is in
// gamut, else exactly 24 bisections of a chroma scale in [0, 1], keeping the
// lower bound, the last scale that was in gamut. Channels are never clipped.
func fit(c oklab) oklab {
	if inGamut(c) {
		return c
	}
	lo, hi := 0.0, 1.0
	for range gamutSteps {
		mid := (lo + hi) / 2
		if inGamut(oklab{c.l, float64(c.a * mid), float64(c.b * mid)}) {
			lo = mid
		} else {
			hi = mid
		}
	}
	return oklab{c.l, float64(c.a * lo), float64(c.b * lo)}
}

// hexOf is an in-gamut colour as #rrggbb, each channel rounded half-up (B3.7).
// A channel within the gamut's tolerance of 0 or 1 is held there; that is
// rounding noise, not clipping.
func hexOf(c oklab) string {
	r, g, b := linear(c)
	return formatHex(channel(r), channel(g), channel(b))
}

func channel(v float64) int {
	v = min(max(v, 0), 1)
	return int(math.Floor(float64(encode(v)*255) + 0.5))
}

// Result is a generated scale.
type Result struct {
	// Algorithm is the version of the constants that produced it.
	Algorithm int
	// Scale is shades 0 (lightest) to 9 (darkest); shade 6 is the input.
	Scale [10]string
	// FilledText is the text colour on a filled primary: White or Black.
	FilledText string
}

// Lightness is the OKLCH lightness of hex, for the range rule.
func Lightness(hex string) (float64, error) {
	r, g, b, err := parseHex(hex)
	if err != nil {
		return 0, err
	}
	return toOKLab(r, g, b).l, nil
}

// Generate is the scale of a primary (B3). The input is "#rrggbb" in either
// case; shade 6 is it, lower-cased, with no round trip. A primary outside the
// accepted lightness is ErrPrimaryRange.
func Generate(hex string) (Result, error) {
	r, g, b, err := parseHex(hex)
	if err != nil {
		return Result{}, err
	}
	base := toOKLab(r, g, b)
	if base.l < MinLightness || base.l > MaxLightness {
		return Result{}, ErrPrimaryRange
	}
	res := Result{Algorithm: Algorithm}
	for i := range res.Scale {
		res.Scale[i] = hexOf(shade(base, i))
	}
	res.Scale[6] = formatHex(r, g, b)
	res.FilledText = filledText(res.Scale[6])
	if res.FilledText == Black {
		res.Scale[7] = readableHover(base, res.Scale[7], res.Scale[6])
	}
	return res, nil
}

// readableHover is shade 7 for a primary whose filled text is black (B3.9).
// Mantine paints one text colour on a filled button, chosen from shade 6, and
// the same one on its hover (shade 7); the generated shade 7 is darker than
// shade 6, so black text on it can fall below AA. When it does, shade 7 moves
// toward shade 6's lightness (its own chroma and hue kept): 24 bisections of
// the move in [0, 1], the smallest passing move kept, shade 6 itself if none
// passes. A shade 7 that already passes is left as it is.
func readableHover(base oklab, shade7, shade6 string) string {
	if contrast(Black, shade7) >= MinContrast {
		return shade7
	}
	c := shade(base, 7)
	at := func(t float64) string {
		return hexOf(fit(oklab{c.l + float64((base.l-c.l)*t), c.a, c.b}))
	}
	lo, hi := 0.0, 1.0
	for range gamutSteps {
		mid := (lo + hi) / 2
		if contrast(Black, at(mid)) >= MinContrast {
			hi = mid
		} else {
			lo = mid
		}
	}
	if h := at(hi); contrast(Black, h) >= MinContrast {
		return h
	}
	return shade6
}

// shade is shade i of the scale of base, in OKLab and fitted into sRGB.
func shade(base oklab, i int) oklab {
	var l float64
	switch {
	case i < 6:
		l = base.l + float64((lightEnd-base.l)*lightSteps[i])
	case i == 6:
		l = base.l
	default:
		l = base.l - float64((base.l-darkEnd)*darkSteps[i-7])
	}
	k := chromaK[i]
	return fit(oklab{l, float64(base.a * k), float64(base.b * k)})
}

// luminance is WCAG 2.x relative luminance of a colour parseHex accepts.
func luminance(hex string) float64 {
	r, g, b, _ := parseHex(hex)
	return float64(0.2126*decode(float64(r)/255)) + float64(0.7152*decode(float64(g)/255)) + float64(0.0722*decode(float64(b)/255))
}

// FilledText is the text colour on a filled background, as Mantine's
// autoContrast chooses it with LuminanceThreshold: White at or below the
// threshold, Black above it (B3.3). A colour that is not #rrggbb is
// ErrInvalidHex.
func FilledText(background string) (string, error) {
	if _, _, _, err := parseHex(background); err != nil {
		return "", err
	}
	return filledText(background), nil
}

func filledText(background string) string {
	if luminance(background) > LuminanceThreshold {
		return Black
	}
	return White
}

// Contrast is the WCAG 2.x contrast ratio of two colours, from 1 to 21. A
// colour that is not #rrggbb is ErrInvalidHex.
func Contrast(a, b string) (float64, error) {
	for _, c := range [2]string{a, b} {
		if _, _, _, err := parseHex(c); err != nil {
			return 0, err
		}
	}
	return contrast(a, b), nil
}

func contrast(a, b string) float64 {
	la, lb := luminance(a), luminance(b)
	return (max(la, lb) + 0.05) / (min(la, lb) + 0.05)
}

// Suggest is the nearest primary that passes every check of B4: the input's
// hue and chroma with its lightness moved, a step of 0.005 at a time, darker
// before lighter, inside the accepted range, the chroma fitted into sRGB. It is
// the input itself when that already passes, and false when none does.
func Suggest(hex string) (string, bool) {
	res, err := Generate(hex)
	if err != nil {
		return "", false
	}
	if Passes(Checks(res)) {
		return res.Scale[6], true
	}
	r, g, b, _ := parseHex(hex)
	base := toOKLab(r, g, b)
	for n := 1; n <= suggestMax; n++ {
		for _, sign := range [2]float64{-1, 1} {
			l := base.l + float64(sign*float64(suggestStep*float64(n)))
			if l < MinLightness || l > MaxLightness {
				continue
			}
			candidate := hexOf(fit(oklab{l, base.a, base.b}))
			if cand, err := Generate(candidate); err == nil && Passes(Checks(cand)) {
				return candidate, true
			}
		}
	}
	return "", false
}
