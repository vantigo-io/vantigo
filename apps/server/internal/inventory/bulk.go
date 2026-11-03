package inventory

import (
	"fmt"
	"regexp"
	"strconv"

	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
)

// Bulk creation (design I5): one request creates a grid under a parent, up to
// three levels, each a kind and a range of codes — an aisle A, racks 01–12,
// shelves 1–5. A digit range is padded to the width of its start, so 01–12
// reads 01, 02 … 12; a letter range is one letter to another. Every location
// of a level is created under every location of the level before. At most
// 1000 locations per request, and all or nothing: the transaction refuses the
// whole grid when one code is taken.

// maxBulkLocations is the most a bulk create makes.
const maxBulkLocations = 1000

// maxBulkLevels is the most levels a bulk create has.
const maxBulkLevels = 3

var (
	digitsPattern = regexp.MustCompile(`^[0-9]+$`)
	letterPattern = regexp.MustCompile(`^[A-Z]$`)
	prefixPattern = regexp.MustCompile(`^[A-Z0-9]*$`)
)

// bulkLevel is one level expanded: its kind and its codes, in order.
type bulkLevel struct {
	kind  string
	codes []string
}

// plannedLocation is one location a bulk create will make: its parent's full
// code, its kind, its code and its own full code.
type plannedLocation struct {
	parent   string
	kind     string
	code     string
	fullCode string
}

// expandLevels judges a bulk create's levels and expands each into its codes,
// with every failure as a field error on levels[i].<field>: a kind that is not
// one (or is site), a prefix or range that makes no code, and a grid of more
// than 1000 locations (bulk_too_large).
func expandLevels(levels []gen.InventoryBulkLevel) ([]bulkLevel, fieldErrors) {
	var f fieldErrors
	if len(levels) == 0 || len(levels) > maxBulkLevels {
		f.add("levels", "A bulk create has one to three levels")
		return nil, f
	}
	out := make([]bulkLevel, 0, len(levels))
	for i, l := range levels {
		field := func(name string) string { return fmt.Sprintf("levels[%d].%s", i, name) }
		kind := string(l.Kind)
		if !l.Kind.Valid() || l.Kind == gen.InventoryLocationKindSite {
			f.add(field("kind"), "The kind must be one of zone, area, sales_floor, back_room, aisle, rack, shelf, bin, display, dock or other")
		}
		codes, name, msg := expandRange(l)
		if msg != "" {
			f.addCoded(field(name), codeCodeInvalid, msg)
			continue
		}
		if len(codes) > maxBulkLocations {
			f.addCoded(field("to"), codeBulkTooLarge, fmt.Sprintf("A bulk create makes at most %d locations", maxBulkLocations))
			continue
		}
		for _, c := range codes {
			if m := codeMessage(c); m != "" {
				f.addCoded(field("from"), codeCodeInvalid, fmt.Sprintf("%s makes the code %q: %s", rangeText(l), c, m))
				break
			}
		}
		out = append(out, bulkLevel{kind: kind, codes: codes})
	}
	if f.failed() {
		return nil, f
	}
	if n := bulkCount(out); n > maxBulkLocations {
		f.addCoded("levels", codeBulkTooLarge, fmt.Sprintf("The levels make %d locations; a bulk create makes at most %d", n, maxBulkLocations))
		return nil, f
	}
	return out, f
}

// expandRange is one level's codes, prefix included; or the field that does
// not make a range and why.
func expandRange(l gen.InventoryBulkLevel) ([]string, string, string) {
	prefix := ""
	if l.Prefix != nil {
		prefix = normalizeCode(*l.Prefix)
	}
	if !prefixPattern.MatchString(prefix) || len(prefix) >= maxCodeLength {
		return nil, "prefix", "A prefix is up to 15 characters A–Z and 0–9"
	}
	from := normalizeCode(l.From)
	if from == "" {
		return nil, "from", "A level needs the code to start from"
	}
	if l.To == nil || normalizeCode(*l.To) == "" {
		return []string{prefix + from}, "", ""
	}
	to := normalizeCode(*l.To)
	switch {
	case digitsPattern.MatchString(from) && digitsPattern.MatchString(to):
		start, err1 := strconv.ParseInt(from, 10, 64)
		end, err2 := strconv.ParseInt(to, 10, 64)
		if err1 != nil || err2 != nil {
			return nil, "to", "A digit range is at most 16 digits"
		}
		if end < start {
			return nil, "to", "A range runs from the lower code to the higher"
		}
		if end-start >= maxBulkLocations {
			return make([]string, maxBulkLocations+1), "", ""
		}
		codes := make([]string, 0, end-start+1)
		for n := start; n <= end; n++ {
			codes = append(codes, prefix+fmt.Sprintf("%0*d", len(from), n))
		}
		return codes, "", ""
	case letterPattern.MatchString(from) && letterPattern.MatchString(to):
		if to[0] < from[0] {
			return nil, "to", "A range runs from the lower code to the higher"
		}
		codes := make([]string, 0, to[0]-from[0]+1)
		for c := from[0]; c <= to[0]; c++ {
			codes = append(codes, prefix+string(c))
		}
		return codes, "", ""
	}
	return nil, "to", "A range is digits to digits (01–12) or one letter to another (A–D)"
}

// rangeText is a level's range as a person wrote it, for a message.
func rangeText(l gen.InventoryBulkLevel) string {
	if l.To == nil {
		return l.From
	}
	return l.From + "–" + *l.To
}

// bulkCount is how many locations the levels make: every code of a level
// under every location of the level before.
func bulkCount(levels []bulkLevel) int {
	total, width := 0, 1
	for _, l := range levels {
		width *= len(l.codes)
		if width > maxBulkLocations {
			return maxBulkLocations + 1
		}
		total += width
	}
	return total
}

// planBulk is the locations the levels make under a parent with the full code
// parent, level by level: each level's locations under every location of the
// level before, in code order.
func planBulk(parent string, levels []bulkLevel) [][]plannedLocation {
	out := make([][]plannedLocation, 0, len(levels))
	parents := []string{parent}
	for _, l := range levels {
		level := make([]plannedLocation, 0, len(parents)*len(l.codes))
		next := make([]string, 0, len(parents)*len(l.codes))
		for _, p := range parents {
			for _, c := range l.codes {
				full := joinFullCode(p, c)
				level = append(level, plannedLocation{parent: p, kind: l.kind, code: c, fullCode: full})
				next = append(next, full)
			}
		}
		out = append(out, level)
		parents = next
	}
	return out
}
