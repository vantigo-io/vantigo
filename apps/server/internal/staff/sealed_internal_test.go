package staff

import (
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// The purposes the module keys the secrets box with (platform-packages.md's
// rule): a purpose either seals or MACs, never both, so the national id's
// sealing key and its index key are two keys, and the bank account's a
// third. A MAC purpose carries -index; a sealing purpose does not.
func TestPurposes_AreDistinct(t *testing.T) {
	t.Parallel()
	seal := []string{purposeNationalID, purposeBankAccount}
	index := []string{purposeNationalIDIndex}
	seen := map[string]bool{}
	for _, p := range append(append([]string{}, seal...), index...) {
		if seen[p] {
			t.Errorf("purpose %q is used twice", p)
		}
		seen[p] = true
		if !strings.HasPrefix(p, "staff/") {
			t.Errorf("purpose %q is not the module's own", p)
		}
	}
	for _, p := range seal {
		if strings.HasSuffix(p, "-index") {
			t.Errorf("the sealing purpose %q is named as an index", p)
		}
	}
	for _, p := range index {
		if !strings.HasSuffix(p, "-index") {
			t.Errorf("the MAC purpose %q does not end in -index", p)
		}
	}
	if purposeNationalID != "staff/national-id" || purposeNationalIDIndex != "staff/national-id-index" || purposeBankAccount != "staff/bank-account" {
		t.Error("a purpose changed its name; every value sealed or indexed under the old one is orphaned")
	}
}

// A unique violation that settles a race is answered as the rule's code,
// as if the check had seen it: the MAC index's as national_id_duplicate,
// the link's (provider, external_id) index as user_linked_elsewhere.
// Anything else is the error it was.
func TestWriteErrors_MapTheRacesToTheirCodes(t *testing.T) {
	t.Parallel()
	unique := func(constraint string) error { return &pgconn.PgError{Code: "23505", ConstraintName: constraint} }
	if err := nationalIDWriteError(unique(nationalIDMACIndex)); !refusedWithCode(err, codeNationalIDDuplicate) {
		t.Errorf("the MAC index's violation = %v, want national_id_duplicate", err)
	}
	if err := linkWriteError(unique(linkExternalIndex)); !refusedWithCode(err, codeUserLinkedElsewhere) {
		t.Errorf("the link index's violation = %v, want user_linked_elsewhere", err)
	}
	other := errors.New("boom")
	if err := nationalIDWriteError(other); !errors.Is(err, other) {
		t.Errorf("another error = %v, want it unchanged", err)
	}
	if err := linkWriteError(unique("some_other_index")); refusedWithCode(err, codeUserLinkedElsewhere) {
		t.Error("another index's violation was answered as user_linked_elsewhere")
	}
}

// The bank-change mail is in the person's language and names the last four
// digits only; a language the module does not know falls back to Norwegian,
// the column's default.
func TestBankChangeMail_Texts(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ language, phrase string }{{"en", "bank account"}, {"nb", "kontonummer"}, {"xx", "kontonummer"}} {
		subject, body := bankChangeMail(c.language, "Kari Nordmann", "8903")
		text := strings.ToLower(subject + "\n" + body)
		if !strings.Contains(text, c.phrase) || !strings.Contains(body, "8903") || !strings.Contains(body, "Kari Nordmann") {
			t.Errorf("%s: %q / %q, want %q, the name and the last four digits", c.language, subject, body, c.phrase)
		}
	}
}
