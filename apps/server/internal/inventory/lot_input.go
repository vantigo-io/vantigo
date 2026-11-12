package inventory

import (
	"fmt"
	"math/big"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
)

// The lots a receipt, a move or an adjustment line names (manufacturing
// phase 2 M57, M59): an existing lot by id, a lot by number to be found or
// made, or — on a receipt of a serial variant — the serial numbers, expanded
// into one line of 1 per serial.

// maxSerials is the most serials one receipt line may expand into.
const maxSerials = 500

// readNewLot is a line's lot named by number, as the engine takes it, and
// the field errors of its shape under field. A failing line is refused whole.
func readNewLot(f *fieldErrors, field string, in *gen.InventoryNewLot) *lotRequest {
	if in == nil {
		return nil
	}
	r := &lotRequest{compareProduction: true}
	if in.Number != nil {
		n := strings.TrimSpace(*in.Number)
		if n != "" && !lotNumberPattern.MatchString(n) {
			f.add(field+".number", "A lot number is 1–20 characters A–Z, a–z, 0–9 and . _ / -")
		}
		r.number = n
	}
	r.supplierLot = optionalText(f, field+".supplierLot", in.SupplierLot, 40)
	r.note = optionalText(f, field+".note", in.Note, 2000)
	if in.ProductionDate != nil {
		r.production = ptr(in.ProductionDate.Time)
	}
	if in.BestBefore != nil {
		r.bestBefore = ptr(in.BestBefore.Time)
	}
	if in.Expiry != nil {
		r.expiry = ptr(in.Expiry.Time)
	}
	checkLotDates(f, field+".", r.production, r.bestBefore, r.expiry)
	return r
}

// checkLotDates refuses a lot's dates stated out of order, under prefix's
// fields (lot_dates_invalid): an expiry before the production date, or a
// best-before date after the expiry. A date left out is not compared.
func checkLotDates(f *fieldErrors, prefix string, production, bestBefore, expiry *time.Time) {
	if expiry == nil {
		return
	}
	if production != nil && expiry.Before(*production) {
		f.addCoded(prefix+"expiry", codeLotDatesInvalid, "The expiry is before the production date")
	}
	if bestBefore != nil && bestBefore.After(*expiry) {
		f.addCoded(prefix+"bestBefore", codeLotDatesInvalid, "The best-before date is after the expiry")
	}
}

// serialNumbers is the serial numbers a receipt line's serials give, or the
// line failure's code and detail.
func serialNumbers(in *gen.InventorySerials) ([]string, string, string) {
	switch {
	case in.Numbers != nil && (in.First != nil || in.Count != nil):
		return nil, codeEnteredInvalid, "Serials are given as first and count, or as numbers, not both."
	case in.Numbers != nil:
		numbers := *in.Numbers
		if len(numbers) == 0 || len(numbers) > maxSerials {
			return nil, codeEnteredInvalid, fmt.Sprintf("A line names 1–%d serials.", maxSerials)
		}
		seen := map[string]bool{}
		out := make([]string, 0, len(numbers))
		for _, n := range numbers {
			n = strings.TrimSpace(n)
			if !lotNumberPattern.MatchString(n) {
				return nil, codeEnteredInvalid, fmt.Sprintf("%q is not a serial number: 1–20 characters A–Z, a–z, 0–9 and . _ / -.", n)
			}
			if seen[strings.ToLower(n)] {
				return nil, codeSerialQuantity, fmt.Sprintf("The serial %s is named twice.", n)
			}
			seen[strings.ToLower(n)] = true
			out = append(out, n)
		}
		return out, "", ""
	case in.First == nil || in.Count == nil:
		return nil, codeEnteredInvalid, "Serials are given as first and count, or as numbers."
	}
	count := *in.Count
	if count < 1 || count > maxSerials {
		return nil, codeEnteredInvalid, fmt.Sprintf("A line names 1–%d serials.", maxSerials)
	}
	return expandSerials(strings.TrimSpace(*in.First), count)
}

// expandSerials is count serial numbers from first, incrementing its trailing
// digits and keeping their width — SN0099, SN0100 (M57) — or the line's
// failure: a first with no trailing digits is serial_first_invalid, and a
// number that would outgrow twenty characters is refused.
func expandSerials(first string, count int) ([]string, string, string) {
	end := len(first)
	start := end
	for start > 0 && first[start-1] >= '0' && first[start-1] <= '9' {
		start--
	}
	if start == end || !lotNumberPattern.MatchString(first) {
		return nil, codeSerialFirstInvalid, fmt.Sprintf("The first serial %q must end in digits, which are counted up.", first)
	}
	prefix, digits := first[:start], first[start:]
	n, ok := new(big.Int).SetString(digits, 10)
	if !ok {
		return nil, codeSerialFirstInvalid, fmt.Sprintf("The first serial %q must end in digits, which are counted up.", first)
	}
	out := make([]string, 0, count)
	for range count {
		text := n.String()
		if len(text) < len(digits) {
			text = strings.Repeat("0", len(digits)-len(text)) + text
		}
		number := prefix + text
		if utf8.RuneCountInString(number) > 20 {
			return nil, codeSerialFirstInvalid, fmt.Sprintf("Counting up from %q passes twenty characters.", first)
		}
		out = append(out, number)
		n.Add(n, big.NewInt(1))
	}
	return out, "", ""
}

// lotIDOf is a line's lotId, 0 for none; an id below 1 is refused under
// field.
func lotIDOf(f *fieldErrors, field string, id *int64) int64 {
	if id == nil {
		return 0
	}
	if *id < 1 {
		f.add(field, "A lot id is 1 or more")
		return 0
	}
	return *id
}
