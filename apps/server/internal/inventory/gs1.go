package inventory

import (
	"strings"

	"github.com/boombuler/barcode/code128"

	"github.com/vantigo-io/vantigo/server/internal/inventory/store"
)

// GS1-128 lot labels (manufacturing phase 2 M93). The element string opens
// with FNC1, which makes a Code 128 a GS1-128, then carries the application
// identifiers in this order: (01) the GTIN-14 when the variant's barcode is a
// valid GTIN-8, -12, -13 or -14, left-padded with zeros, its check digit
// verified; (11) the production date; (17) the expiry; (15) the best-before
// only when there is no expiry; and (10) the lot number, or (21) the serial,
// last — a variable-length field needs an FNC1 after it unless it ends the
// string, and the fixed-length ones (01, 11, 15, 17) never do, so the string
// holds one FNC1 only, at its start. FNC1 is the rune boombuler's encoder
// reads it as (code128.FNC1, 'ñ'), in the text maroto hands it. (10) and (21)
// are only valid with an item identifier, so a lot whose variant has no GTIN
// prints M73's internal label, marked as not a GS1 label.

// noGTINNote is the line an internal label carries in place of a GS1 one.
const noGTINNote = "No GTIN — not a GS1 label"

// gtin14 is barcode as a GTIN-14 — a GTIN-8, -12, -13 or -14 of digits only,
// left-padded with zeros — when its check digit is right.
func gtin14(barcode string) (string, bool) {
	code := strings.TrimSpace(barcode)
	switch len(code) {
	case 8, 12, 13, 14:
	default:
		return "", false
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			return "", false
		}
	}
	code = strings.Repeat("0", 14-len(code)) + code
	sum := 0
	for i := range 13 {
		d := int(code[i] - '0')
		if i%2 == 0 {
			d *= 3 // from the right, every other digit before the check digit weighs 3
		}
		sum += d
	}
	if (10-sum%10)%10 != int(code[13]-'0') {
		return "", false
	}
	return code, true
}

// gs1Element is one application identifier and its data.
type gs1Element struct{ ai, data string }

// gs1Elements is a lot's element string's fields in M93's order.
func gs1Elements(gtin string, l store.InventoryLot) []gs1Element {
	out := []gs1Element{{"01", gtin}}
	yymmdd := "060102"
	if l.ProductionDate.Valid {
		out = append(out, gs1Element{"11", l.ProductionDate.Time.Format(yymmdd)})
	}
	if l.Expiry.Valid {
		out = append(out, gs1Element{"17", l.Expiry.Time.Format(yymmdd)})
	} else if l.BestBefore.Valid {
		out = append(out, gs1Element{"15", l.BestBefore.Time.Format(yymmdd)})
	}
	if l.Serial {
		out = append(out, gs1Element{"21", l.Number})
	} else {
		out = append(out, gs1Element{"10", l.Number})
	}
	return out
}

// gs1Strings is a lot's element string as the barcode encodes it — FNC1
// first, the fields run together — and as the label prints it below the
// barcode, each AI in brackets.
func gs1Strings(gtin string, l store.InventoryLot) (data, readable string) {
	var d, r strings.Builder
	d.WriteRune(code128.FNC1)
	for _, e := range gs1Elements(gtin, l) {
		d.WriteString(e.ai + e.data)
		r.WriteString("(" + e.ai + ")" + e.data)
	}
	return d.String(), r.String()
}

// gs1LabelOf is a lot's GS1-128 label: its element string as the barcode
// and its AIs in brackets below, then the number, the SKU and name, and the
// dates as the internal label prints them. A variant without a valid GTIN
// gets the internal label, marked.
func gs1LabelOf(l store.InventoryLot, barcode *string) label {
	out := lotLabelOf(l)
	gtin, ok := "", false
	if barcode != nil {
		gtin, ok = gtin14(*barcode)
	}
	if !ok {
		out.readable = noGTINNote
		return out
	}
	out.token, out.readable = gs1Strings(gtin, l)
	out.gs1 = true
	return out
}
