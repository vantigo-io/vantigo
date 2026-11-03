package inventory

import (
	"regexp"
	"strconv"
	"strings"
)

// The code rules (design I1, I2, I4, I7): a site's or a location's code is
// 1–16 characters A–Z and 0–9, upper-cased before it is judged, and never of
// the form LOC followed by digits — the label token's form — so a full code
// can never equal a token. Codes cannot contain the separator, which is why a
// full code, the codes from the root down joined with "-", is unique in the
// deployment when site codes are and sibling codes are.

// maxCodeLength is a code's longest length.
const maxCodeLength = 16

// maxDepth is how many levels a tree may have below its root (I2).
const maxDepth = 8

// fullCodeSeparator joins the codes of a full code.
const fullCodeSeparator = "-"

// tokenPrefix starts a location's label token, LOC + its id (I7).
const tokenPrefix = "LOC"

var (
	codePattern  = regexp.MustCompile(`^[A-Z0-9]{1,16}$`)
	tokenPattern = regexp.MustCompile(`^LOC[0-9]+$`)
)

// normalizeCode is a code as it is judged and stored: trimmed and
// upper-cased.
func normalizeCode(raw string) string { return strings.ToUpper(strings.TrimSpace(raw)) }

// codeMessage is why a normalised code is not one, "" when it is.
func codeMessage(code string) string {
	switch {
	case code == "":
		return "A code is required"
	case len(code) > maxCodeLength || !codePattern.MatchString(code):
		return "A code is 1–16 characters A–Z and 0–9"
	case tokenPattern.MatchString(code):
		return "A code may not be LOC followed by digits; labels use that form"
	}
	return ""
}

// joinFullCode is the full code of a location coded code under a parent whose
// full code is parent.
func joinFullCode(parent, code string) string { return parent + fullCodeSeparator + code }

// locationToken is the token a location's label encodes (I7).
func locationToken(id int64) string { return tokenPrefix + strconv.FormatInt(id, 10) }

// parseLocationToken is the location id a scanned token names; ok false when
// text is not a token. text is normalised first.
func parseLocationToken(text string) (int64, bool) {
	t := normalizeCode(text)
	if !tokenPattern.MatchString(t) {
		return 0, false
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(t, tokenPrefix), 10, 64)
	if err != nil || id < 1 {
		return 0, false
	}
	return id, true
}

// rePrefixed is fullCode with its leading oldPrefix replaced by newPrefix —
// how a descendant's full code follows a re-code or a re-parent above it.
func rePrefixed(fullCode, oldPrefix, newPrefix string) string {
	return newPrefix + strings.TrimPrefix(fullCode, oldPrefix)
}
