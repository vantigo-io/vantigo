package bankfile_test

import (
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/invoices/bankfile"
)

// An account is 11 digits however it is punctuated, or a Norwegian IBAN
// whose mod-97 check holds, read as its BBAN; nothing else.
func TestNormaliseAccount(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		in, bban string
		ok       bool
	}{
		{"12345678903", "12345678903", true},
		{"1234.56.78903", "12345678903", true},
		{"1234 56 78903", "12345678903", true},
		{" 1234.56.78903 ", "12345678903", true},
		{"NO9386011117947", "86011117947", true},
		{"NO93 8601 1117 947", "86011117947", true},
		{"NO9486011117947", "", false},          // the check digits do not hold
		{"NO938601111794", "", false},           // a BBAN of 10
		{"SE4550000000058398257466", "", false}, // a Swedish IBAN, valid
		{"DK5000400440116243", "", false},       // a Danish one, valid
		{"1234567890", "", false},
		{"123456789012", "", false},
		{"1234567890A", "", false},
		{"1234-56-78903", "", false},
		{"", "", false},
	} {
		bban, ok := bankfile.NormaliseAccount(c.in)
		if bban != c.bban || ok != c.ok {
			t.Errorf("NormaliseAccount(%q) = %q, %v; want %q, %v", c.in, bban, ok, c.bban, c.ok)
		}
	}
}
