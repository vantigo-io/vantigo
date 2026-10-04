package invoices

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// A reason goes on the wire and into last_error without a person's e-mail
// address or a participant identifier, and at most 500 runes — cut on a rune,
// never inside one (D9).
func TestRedactReason(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"", ""},
		{"  Rejected by the receiver  ", "Rejected by the receiver"},
		{"Receiver 0192:923609016 refused it", "Receiver <participant> refused it"},
		{"9908:NO923609016MVA and 0088:7080000000001 both", "<participant> and <participant> both"},
		{"Contact ola.nordmann+faktura@acme.example or POST@ACME.NO", "Contact <e-mail> or <e-mail>"},
		{"[BR-CO-10] Sum of Invoice line net amount at 12:30", "[BR-CO-10] Sum of Invoice line net amount at 12:30"},
		{"Due 2026-10-04T12:30:00Z", "Due 2026-10-04T12:30:00Z"},
	} {
		if got := redactReason(c.in); got != c.want {
			t.Errorf("redactReason(%q) = %q, want %q", c.in, got, c.want)
		}
	}

	long := strings.Repeat("æ", 499) + "øå" + strings.Repeat("x", 10)
	got := redactReason(long)
	if n := utf8.RuneCountInString(got); n != 500 || !utf8.ValidString(got) || !strings.HasSuffix(got, "æø") {
		t.Errorf("a 511-rune reason = %d runes (valid %v, ends %q), want the first 500 runes, whole", n, utf8.ValidString(got), got[len(got)-4:])
	}
	if got := redactReason(strings.Repeat("a", 500)); utf8.RuneCountInString(got) != 500 {
		t.Errorf("a 500-rune reason was cut to %d", utf8.RuneCountInString(got))
	}
	// Redacted first, then cut: an address straddling the cut never leaves half of itself.
	straddle := strings.Repeat("x", 490) + " ola@acme.example"
	if got := redactReason(straddle); strings.Contains(got, "ola@") || strings.Contains(got, "acme") {
		t.Errorf("an address at the cut left %q", got[480:])
	}
}
