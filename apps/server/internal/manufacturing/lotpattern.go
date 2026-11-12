package manufacturing

// The output lot's number (manufacturing phase 2 M80, M81). A BoM names the
// pattern its productions' lots are numbered by; the order keeps it at
// confirm. {MO} is the order's display number, {YYYY} {YY} {MM} {DD} and
// {YYMMDD} the production date, {SKU} the product's SKU and {N} a counter per
// rendered prefix, kept in manufacturing.counters as 'lot:<prefix>'. Anything
// else is literal. A serial product's units are numbered one each, by default
// {MO}-0001 upwards, continuing from the order's serials so far.

import (
	"context"
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/vantigo-io/vantigo/server/internal/manufacturing/store"
)

const (
	defaultLotPattern = "{MO}"
	maxLotPattern     = 40
	lotCounterPrefix  = "lot:"
	maxSerialOutputs  = 500
	counterToken      = "{N}"
)

// lotNumberPattern is a lot number's shape (M52): Inventory's rule, 1–20
// characters, GS1's limit for AI (10) and (21).
var lotNumberPattern = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,20}$`)

// lotToken is a pattern's token.
var lotToken = regexp.MustCompile(`\{[A-Z]+\}`)

// lotPrefix is a pattern rendered for one production with its {N} left as
// the token: the counter's key, and the number once {N} is put in.
func lotPrefix(pattern, mo string, day time.Time, sku string) string {
	return lotToken.ReplaceAllStringFunc(pattern, func(token string) string {
		switch token {
		case "{MO}":
			return mo
		case "{YYYY}":
			return fmt.Sprintf("%04d", day.Year())
		case "{YY}":
			return fmt.Sprintf("%02d", day.Year()%100)
		case "{MM}":
			return fmt.Sprintf("%02d", int(day.Month()))
		case "{DD}":
			return fmt.Sprintf("%02d", day.Day())
		case "{YYMMDD}":
			return fmt.Sprintf("%02d%02d%02d", day.Year()%100, int(day.Month()), day.Day())
		case "{SKU}":
			return sku
		}
		return token
	})
}

// withCounter is a rendered prefix with n in place of every {N}.
func withCounter(prefix string, n int64) string {
	return strings.ReplaceAll(prefix, counterToken, strconv.FormatInt(n, 10))
}

// sampleLotNumber is the pattern rendered for the BoM save's judgement: an
// order number and a counter of the widths a busy shop reaches, and the SKU
// as the catalog has it.
func sampleLotNumber(pattern, sku string) string {
	return withCounter(lotPrefix(pattern, "MO-99999-9", time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC), sku), 9999)
}

// judgeLotPattern is the 400 lot_pattern_invalid of a pattern whose sample
// render is not a lot number (M80).
func judgeLotPattern(j *judgement, pattern, sku string) {
	if j.f.errs["lotPattern"] != nil {
		return
	}
	if sample := sampleLotNumber(pattern, sku); !lotNumberPattern.MatchString(sample) {
		j.f.addCoded("lotPattern", codeLotPatternInvalid,
			fmt.Sprintf("The pattern renders %q, which is no lot number: 1–20 characters A–Z, a–z, 0–9 and . _ / -.", sample))
	}
}

// renderLotNumber is the number of a production's lot: the order's pattern
// on the production day, taking the next {N} of its prefix under the
// counter's row lock when it has one — counted then reports it, a number
// that names a new lot or none (M57: a counted number that names another
// document's lot is skipped). A number that is no lot number is the 400
// lot_pattern_invalid at prefix's outputLot.number, before any counter is
// taken when the prefix alone is too long.
func renderLotNumber(ctx context.Context, txq *store.Queries, pattern, mo string, day time.Time, sku, at string) (number string, counted bool, err error) {
	prefix := lotPrefix(pattern, mo, day, sku)
	invalid := func(number string) error {
		return refuseField(productionTitle, at+"outputLot.number", codeLotPatternInvalid,
			fmt.Sprintf("The order's lot pattern %q renders %q, which is no lot number. Send the number in outputLot.", pattern, number))
	}
	if !strings.Contains(prefix, counterToken) {
		if !lotNumberPattern.MatchString(prefix) {
			return "", false, invalid(prefix)
		}
		return prefix, false, nil
	}
	if bare := strings.ReplaceAll(prefix, counterToken, "1"); !lotNumberPattern.MatchString(bare) {
		return "", false, invalid(bare)
	}
	n, err := txq.NextLotCounter(ctx, lotCounterPrefix+prefix)
	if err != nil {
		return "", false, fmt.Errorf("manufacturing: count lot %q: %w", prefix, err)
	}
	number = withCounter(prefix, n)
	if !lotNumberPattern.MatchString(number) {
		return "", false, invalid(number)
	}
	return number, true, nil
}

// previewLotNumber is renderLotNumber for the production draft, on the pool:
// {N} is the next the counter would hand out, nothing is taken, and a number
// that is no lot number is nil.
func previewLotNumber(ctx context.Context, q *store.Queries, pattern, mo string, day time.Time, sku string) (*string, error) {
	prefix := lotPrefix(pattern, mo, day, sku)
	number := prefix
	if strings.Contains(prefix, counterToken) {
		n, err := q.PeekLotCounter(ctx, lotCounterPrefix+prefix)
		if err != nil {
			return nil, err
		}
		number = withCounter(prefix, n)
	}
	if !lotNumberPattern.MatchString(number) {
		return nil, nil
	}
	return &number, nil
}

// countUpSerials is count serial numbers from first, counting up its
// trailing digits and keeping their width — SN0099, SN0100 (M57, M81) — or
// why not: a first that ends in no digits, or numbers that outgrow twenty
// characters.
func countUpSerials(first string, count int) ([]string, string) {
	end := len(first)
	start := end
	for start > 0 && first[start-1] >= '0' && first[start-1] <= '9' {
		start--
	}
	if start == end || !lotNumberPattern.MatchString(first) {
		return nil, fmt.Sprintf("The first serial %q must be a lot number ending in digits, which are counted up.", first)
	}
	prefix, digits := first[:start], first[start:]
	n, _ := new(big.Int).SetString(digits, 10)
	out := make([]string, 0, count)
	for range count {
		t := n.String()
		if len(t) < len(digits) {
			t = strings.Repeat("0", len(digits)-len(t)) + t
		}
		number := prefix + t
		if utf8.RuneCountInString(number) > 20 {
			return nil, fmt.Sprintf("Counting up from %q passes twenty characters.", first)
		}
		out = append(out, number)
		n.Add(n, big.NewInt(1))
	}
	return out, ""
}

// defaultSerialFirst is the first serial a production makes by default: the
// order's number, then four digits continuing after the highest {MO}-<digits>
// serial the order's productions made before (M81) — not after their count,
// which a serial typed in that shape would make collide.
func defaultSerialFirst(mo string, before []string) string {
	highest := new(big.Int)
	for _, n := range before {
		digits, ok := strings.CutPrefix(n, mo+"-")
		if !ok || digits == "" || strings.Trim(digits, "0123456789") != "" {
			continue
		}
		if v, ok := new(big.Int).SetString(digits, 10); ok && v.Cmp(highest) > 0 {
			highest = v
		}
	}
	next := highest.Add(highest, big.NewInt(1)).String()
	if len(next) < 4 {
		next = strings.Repeat("0", 4-len(next)) + next
	}
	return mo + "-" + next
}
