package ehf_test

import (
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/invoices/ehf"
)

// The EAS table is the full list PEPPOL-EN16931-CL008 checks against: the
// Norwegian organisation number, GLN and the Belgian enterprise number are
// in it; a made-up scheme and a malformed one are not (EHF and KID design
// D11).
func TestEASScheme(t *testing.T) {
	t.Parallel()
	for _, scheme := range []string{"0192", "0088", "0208", "0007", "0002", "9959", "0245", "9910"} {
		if !ehf.EASScheme(scheme) {
			t.Errorf("EASScheme(%q) = false, want true", scheme)
		}
	}
	for _, scheme := range []string{"9999", "", "192", "01920", "0192 ", "NO:ORG", "9901", "0000"} {
		if ehf.EASScheme(scheme) {
			t.Errorf("EASScheme(%q) = true, want false", scheme)
		}
	}
}

// The table holds the release's whole list: 94 codes in v3.0.20.
func TestEASScheme_TheWholeList(t *testing.T) {
	t.Parallel()
	if got := ehf.EASCodeCount(); got != 94 {
		t.Errorf("EASCodeCount() = %d, want 94 (peppol-bis-invoice-3 v3.0.20)", got)
	}
}
