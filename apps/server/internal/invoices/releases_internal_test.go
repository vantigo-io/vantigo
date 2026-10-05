package invoices

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

// A re-pull note is cut to the note's limit, counted in characters: whole
// sentences only, "…" when any was left out, never past the limit — with
// Norwegian letters in the sentences, where bytes and characters differ.
func TestJoinNote(t *testing.T) {
	t.Parallel()
	sentence := func(i int) string {
		return fmt.Sprintf("Erstatter faktura %d, kreditert med kreditnota %d — æøå", 2*i+1, 2*i+2)
	}
	var many []string
	for i := range 40 {
		many = append(many, sentence(i))
	}
	got := joinNote(many, maxNote)
	if n := utf8.RuneCountInString(got); n > maxNote || !strings.HasSuffix(got, "…") || len(got) <= maxNote {
		t.Errorf("forty sentences = %d characters (%d bytes), ending %q; want at most %d characters, more bytes, ending in …",
			n, len(got), got[len(got)-10:], maxNote)
	}
	kept := strings.Split(strings.TrimSuffix(got, "…"), ". ")
	for _, s := range kept {
		if !strings.HasPrefix(s, "Erstatter faktura ") || !strings.HasSuffix(s, "æøå") {
			t.Errorf("a sentence cut short: %q", s)
		}
	}
	// It stopped only where the next sentence would not have fitted, counted
	// in characters: one more, with its mark, passes the limit.
	next := ". " + sentence(len(kept))
	if utf8.RuneCountInString(strings.TrimSuffix(got, "…"))+utf8.RuneCountInString(next)+1 <= maxNote {
		t.Errorf("stopped after %d sentences with room for another (%q)", len(kept), next)
	}
	if got := joinNote(many[:2], maxNote); got != sentence(0)+". "+sentence(1) {
		t.Errorf("two sentences = %q, want both, joined, no mark", got)
	}
	// Exactly at the limit, the last sentence fits without a mark.
	exact := joinNote([]string{"abcd", "ef"}, 8)
	if exact != "abcd. ef" {
		t.Errorf("at the limit = %q", exact)
	}
	if got := joinNote([]string{"abcd", "efg"}, 8); got != "abcd…" {
		t.Errorf("one over = %q, want the first and the mark", got)
	}
	// The mark's room is kept while more sentences follow: "abcd. ef" fits 8
	// alone, but with "x" still to come it would leave no room for the "…".
	if got := joinNote([]string{"abcd", "ef", "x"}, 8); got != "abcd…" {
		t.Errorf("room for the mark = %q, want %q", got, "abcd…")
	}
	// 600 + 2 + 398 characters fit 1 000 exactly, though far more bytes.
	wide := []string{strings.Repeat("ø", 600), strings.Repeat("ø", 398)}
	if got := joinNote(wide, maxNote); got != wide[0]+". "+wide[1] {
		t.Errorf("1 000 characters of ø = %d characters, want both sentences whole", utf8.RuneCountInString(got))
	}
	if got := joinNote(nil, maxNote); got != "" {
		t.Errorf("none = %q", got)
	}
}
