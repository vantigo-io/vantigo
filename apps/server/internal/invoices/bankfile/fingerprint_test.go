package bankfile_test

import (
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/invoices/bankfile"
)

// The fingerprint (D3, R4 §3.6): two identical payments in one file stay
// two by their ordinals; the same file, or one overlapping it, reproduces
// them; the text is compared normalised; every other field counts.
func TestFingerprint_OrdinalsAndNormalisation(t *testing.T) {
	t.Parallel()

	f := mustParse(t, fixture(t, "identical-lines.ocr"))
	a, b := f.Transactions[0], f.Transactions[1]
	if a.Ordinal != 1 || b.Ordinal != 2 || a.Fingerprint == b.Fingerprint {
		t.Errorf("identical lines: ordinals %d, %d; fingerprints equal %v", a.Ordinal, b.Ordinal, a.Fingerprint == b.Fingerprint)
	}
	again := mustParse(t, fixture(t, "identical-lines.ocr"))
	if again.Transactions[0].Fingerprint != a.Fingerprint || again.Transactions[1].Fingerprint != b.Fingerprint {
		t.Error("the same file parsed twice gave other fingerprints")
	}

	// overlap-a and overlap-b are two transmissions sharing their first two
	// lines; each has one of its own.
	oa, ob := mustParse(t, fixture(t, "overlap-a.ocr")), mustParse(t, fixture(t, "overlap-b.ocr"))
	in := map[string]bool{}
	for _, tx := range oa.Transactions {
		in[tx.Fingerprint] = true
	}
	shared := 0
	for _, tx := range ob.Transactions {
		if in[tx.Fingerprint] {
			shared++
		}
	}
	if shared != 2 || oa.Transactions[0].Fingerprint != ob.Transactions[0].Fingerprint ||
		oa.Transactions[1].Fingerprint != ob.Transactions[1].Fingerprint {
		t.Errorf("overlapping files share %d fingerprints, want their two common lines", shared)
	}

	// The fingerprint is stored and compared across imports for good: a
	// change to how it is computed would let every earlier line in again.
	// R4's first line's is pinned.
	r4 := mustParse(t, fixture(t, "r4-example.ocr"))
	const pinned = "a0bce9bff660b6e22dfc4df28f84a1b0290bc2569f4531775cef2778c1b1fd21"
	if got := r4.Transactions[0].Fingerprint; got != pinned {
		t.Errorf("R4's first line's fingerprint = %s, pinned %s", got, pinned)
	}

	line := bankfile.Transaction{Account: accountA, BookedOn: day(6), AmountMinor: 125000,
		RemittanceText: "Faktura 1001", DebtorAccount: "98765432109", ArchiveRef: "123456789"}
	fp := func(txs ...bankfile.Transaction) []bankfile.Transaction {
		bankfile.Fingerprints(txs)
		return txs
	}
	base := fp(line)[0].Fingerprint
	spaced := line
	spaced.RemittanceText = "  FAKTURA \t 1001\n"
	if got := fp(spaced)[0].Fingerprint; got != base {
		t.Error("the text's case and spacing changed the fingerprint")
	}
	for name, change := range map[string]func(*bankfile.Transaction){
		"the account":           func(tx *bankfile.Transaction) { tx.Account = accountB },
		"the booking date":      func(tx *bankfile.Transaction) { tx.BookedOn = day(5) },
		"the amount":            func(tx *bankfile.Transaction) { tx.AmountMinor++ },
		"the sign":              func(tx *bankfile.Transaction) { tx.Negative = true },
		"a debit":               func(tx *bankfile.Transaction) { tx.Debit = true },
		"the text":              func(tx *bankfile.Transaction) { tx.RemittanceText = "Faktura 1002" },
		"a KID of the text":     func(tx *bankfile.Transaction) { tx.KID = "faktura 1001"; tx.RemittanceText = "" },
		"the debtor account":    func(tx *bankfile.Transaction) { tx.DebtorAccount = "" },
		"the archive reference": func(tx *bankfile.Transaction) { tx.ArchiveRef = "123456780" },
		"a field's boundary": func(tx *bankfile.Transaction) {
			tx.DebtorAccount, tx.ArchiveRef = "98765432109123456789", ""
		},
	} {
		other := line
		change(&other)
		if fp(other)[0].Fingerprint == base {
			t.Errorf("%s did not change the fingerprint", name)
		}
	}
	// With a KID, the text is not hashed.
	withKID := line
	withKID.KID = "0010017"
	otherText := withKID
	otherText.RemittanceText = "something else"
	if fp(withKID)[0].Fingerprint != fp(otherText)[0].Fingerprint {
		t.Error("the text changed a KID line's fingerprint")
	}

	// Ordinals count each set of identical lines apart, in file order.
	other := line
	other.AmountMinor = 1
	txs := fp(line, other, line, other, line)
	if got := []int{txs[0].Ordinal, txs[1].Ordinal, txs[2].Ordinal, txs[3].Ordinal, txs[4].Ordinal}; got[0] != 1 ||
		got[1] != 1 || got[2] != 2 || got[3] != 2 || got[4] != 3 {
		t.Errorf("ordinals %v, want [1 1 2 2 3]", got)
	}
	if txs[0].Fingerprint != base || txs[2].Fingerprint == base || txs[4].Fingerprint == txs[2].Fingerprint {
		t.Error("the ordinal is not in the fingerprint")
	}

	if got := bankfile.NormaliseText("  Faktura\t1001 \n ÆØÅ  "); got != "faktura 1001 æøå" {
		t.Errorf("NormaliseText = %q", got)
	}
}
