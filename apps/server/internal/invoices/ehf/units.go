package ehf

import (
	"strings"
	"unicode"
)

// unitCodes is D5's table: the common Norwegian and English words for a
// line's free-text unit, lowercased, each to its UNECE Recommendation 20 (or
// 21, for XPK) code. "t" is deliberately absent — hour or tonne — and
// anything not here, the empty unit included, is C62 (one): the line's
// description carries the meaning. No wider table in this phase (D13).
var unitCodes = map[string]string{
	"stk": "C62", "pcs": "C62", "piece": "C62",
	"time": "HUR", "timer": "HUR", "h": "HUR", "hour": "HUR", "hours": "HUR",
	"min": "MIN",
	"dag": "DAY", "day": "DAY",
	"uke": "WEE", "week": "WEE",
	"mnd": "MON", "month": "MON",
	"år": "ANN", "year": "ANN",
	"kg": "KGM",
	"g":  "GRM",
	"m":  "MTR",
	"m2": "MTK", "m²": "MTK",
	"m3": "MTQ", "m³": "MTQ",
	"l": "LTR", "liter": "LTR", "litre": "LTR",
	"km":    "KMT",
	"pakke": "XPK", "pack": "XPK", "pk": "XPK",
	"sett": "SET", "set": "SET",
	"kwh": "KWH",
}

// fallbackUnitCode is the code of a unit the table does not know.
const fallbackUnitCode = "C62"

// UnitCode is a line's unit as the code EHF needs on every quantity:
// trimmed, lowercased and with trailing punctuation stripped ("Stk." is
// "stk"), then looked up; C62 when the table has no entry.
func UnitCode(unit string) string {
	word := strings.ToLower(strings.TrimRightFunc(strings.TrimSpace(unit), func(r rune) bool {
		return unicode.IsPunct(r) || unicode.IsSpace(r)
	}))
	if code, ok := unitCodes[word]; ok {
		return code
	}
	return fallbackUnitCode
}

// knownUnitCode reports whether code is one UnitCode can answer — the
// invariant a rendered document's every unitCode is held to (D11).
func knownUnitCode(code string) bool {
	if code == fallbackUnitCode {
		return true
	}
	for _, c := range unitCodes {
		if c == code {
			return true
		}
	}
	return false
}
