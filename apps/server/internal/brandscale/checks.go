package brandscale

// The page, card and text colours of Vantigo's token layer, per colour scheme
// (shell/theme.css, D-A). A test reads theme.css and fails when they differ, so
// they cannot drift; the TypeScript port holds the same constants.
const (
	PageLight = "#f7f8fc"
	CardLight = "#ffffff"
	TextLight = "#182033"
	PageDark  = "#0d0f14"
	CardDark  = "#191b24"
	TextDark  = "#dfe2ea"
)

// Scheme is a colour scheme.
type Scheme string

const (
	Light Scheme = "light"
	Dark  Scheme = "dark"
)

// Pair names a pair of colours a brand touches (B4).
type Pair string

const (
	// PairFilled is the filled text on the filled primary, shade 6.
	PairFilled Pair = "filled"
	// PairFilledHover is the filled text on the hover colour, shade 7.
	PairFilledHover Pair = "filledHover"
	// PairLinkPage is a link on the page.
	PairLinkPage Pair = "linkPage"
	// PairLinkCard is a link on a card.
	PairLinkCard Pair = "linkCard"
	// PairLinkTint is a link on a tinted cell.
	PairLinkTint Pair = "linkTint"
)

// Pairs is the order the checks come in, within each scheme.
var Pairs = [5]Pair{PairFilled, PairFilledHover, PairLinkPage, PairLinkCard, PairLinkTint}

// Check is one pair's ratio in one scheme.
type Check struct {
	Pair       Pair
	Scheme     Scheme
	Foreground string
	Background string
	Ratio      float64
	Pass       bool
}

// How much of shade 5 the dark tint is, in percent, and its hover; the rest is
// the dark card.
const (
	DarkTintPercent      = 24
	DarkTintHoverPercent = 32
)

// OverCard is a colour laid over the dark card at percent strength, as an
// opaque colour: each channel (colour*percent + card*(100-percent) + 50) / 100
// in whole numbers, so the TypeScript port agrees to the byte. A colour that
// is not #rrggbb is ErrInvalidHex.
func OverCard(hex string, percent int) (string, error) {
	r, g, b, err := parseHex(hex)
	if err != nil {
		return "", err
	}
	cr, cg, cb, _ := parseHex(CardDark)
	mix := func(c, card int) int { return (c*percent + card*(100-percent) + 50) / 100 }
	return formatHex(mix(r, cr), mix(g, cg), mix(b, cb)), nil
}

// DarkTint is the `light` variant of the brand colour in the dark scheme (the
// active navigation link, an app tile, a light badge): shade 5 over the card
// at DarkTintPercent. It is opaque and always lighter than the card; the dark
// scheme's tinted cell (--vantigo-subtle-bg) is this. Mantine's own rule,
// shade 9 darkened by half, gave a tint darker than the card for a dark shade
// 9, so the shell's theme replaces it with this one (algorithm 3). A colour
// that is not #rrggbb is ErrInvalidHex.
func DarkTint(shade5 string) (string, error) {
	return OverCard(shade5, DarkTintPercent)
}

func darkTint(shade5 string) string {
	out, _ := OverCard(shade5, DarkTintPercent)
	return out
}

// Checks is the ten checks of B4 for a generated scale, light then dark, each in
// the order of Pairs (AA, 4.5 : 1, for text).
func Checks(res Result) []Check {
	s := res.Scale
	rows := []struct {
		scheme                 Scheme
		pair                   Pair
		foreground, background string
	}{
		{Light, PairFilled, res.FilledText, s[6]},
		{Light, PairFilledHover, res.FilledText, s[7]},
		{Light, PairLinkPage, s[8], PageLight},
		{Light, PairLinkCard, s[8], CardLight},
		{Light, PairLinkTint, s[8], s[0]},
		{Dark, PairFilled, res.FilledText, s[6]},
		{Dark, PairFilledHover, res.FilledText, s[7]},
		{Dark, PairLinkPage, s[3], PageDark},
		{Dark, PairLinkCard, s[3], CardDark},
		{Dark, PairLinkTint, s[3], darkTint(s[5])},
	}
	out := make([]Check, len(rows))
	for i, r := range rows {
		ratio := contrast(r.foreground, r.background)
		out[i] = Check{r.pair, r.scheme, r.foreground, r.background, ratio, ratio >= MinContrast}
	}
	return out
}

// Passes reports whether every check passes.
func Passes(checks []Check) bool {
	for _, c := range checks {
		if !c.Pass {
			return false
		}
	}
	return true
}
