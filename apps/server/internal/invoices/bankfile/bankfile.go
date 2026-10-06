// Package bankfile reads the bank files a seller imports (payments and
// reminders design D3): OCR giro from Mastercard Payment Services and
// camt.054 notifications, each checked all or nothing against its own
// control totals before a line is kept, and each line given the fingerprint
// that recognises it again in a later or overlapping file.
//
// A leaf: it imports the standard library and invoices/kid only
// (purity_test.go enforces it), never the store or the module, so the
// importer decides what a line means and this package only what it says.
// Amounts are øre (int64) here and leave it for numeric(14,2).
package bankfile

import (
	"slices"
	"time"
)

// Format is a bank file's format, as invoices.bank_files stores it.
type Format string

// The two formats.
const (
	FormatOCR     Format = "ocr"
	FormatCamt054 Format = "camt054"
)

// IgnoredKind is why a line of a file did not become a transaction; the
// import counts them (invoices.bank_files.ignored_kinds).
type IgnoredKind string

// The four kinds a file's ignored lines are counted under.
const (
	IgnoredDebit           IgnoredKind = "debit"            // camt: a DBIT entry that is not a reversal
	IgnoredNotBooked       IgnoredKind = "not_booked"       // camt: an entry not BOOKed
	IgnoredCardInformation IgnoredKind = "card_information" // OCR: types 18–21, checked and summed
	IgnoredZeroAmount      IgnoredKind = "zero_amount"      // a transaction of 0.00
)

// The limits on a file: its size (the upload's BodyLimits) and the
// transactions it may hold, each later matched in a database transaction
// of its own.
const (
	MaxTransactions = 5000
	MaxBytes        = 10 << 20
)

// File is a parsed bank file.
type File struct {
	Format       Format
	Version      string // "" for OCR; "camt.054.001.02" or "camt.054.001.08"
	Identity     string // OCR "sender:transmission:recipient"; camt "MsgId|CreDtTm"
	Accounts     []string
	Transactions []Transaction
	Ignored      map[IgnoredKind]int

	// FirstBookedOn and LastBookedOn bound the transactions' booking dates;
	// zero without transactions.
	FirstBookedOn, LastBookedOn time.Time
}

// Transaction is one line of a file that may become a payment.
type Transaction struct {
	LineRef   string // OCR "<oppdragsnummer>/<transaksjonsnummer>"; camt "<Ntfctn/Id>/<entry n>/<tx n>", 1-based
	Account   string // the receiving account, an 11-digit BBAN
	Debit     bool   // camt: a reversal (D3)
	Negative  bool   // OCR: Fortegn '-'
	BookedOn  time.Time
	ValueOn   *time.Time
	OrderedOn *time.Time // OCR item 2's Oppdragsdato; nil for camt (reading 32)

	AmountMinor int64  // øre, > 0; the sign is Negative or Debit
	Currency    string // "NOK"
	KID         string // as the bank wrote it, trimmed; "" when none

	RemittanceText, DebtorName, DebtorAccount, ArchiveRef, BankCode string

	Fingerprint string // hex SHA-256, filled by Fingerprints
	Ordinal     int    // 1-based, filled by Fingerprints
}

// Error is a file refused, all or nothing: the 400 on "file". Where names
// the place — "file", "record 7", "Ntry[2]/NtryDtls/TxDtls[1]".
type Error struct{ Where, Message string }

func (e *Error) Error() string { return e.Where + ": " + e.Message }

// Parse detects b's format and parses it under that format's rules; today
// is the request's Oslo day, after which no booking date may lie.
func Parse(b []byte, today time.Time) (*File, error) {
	if len(b) > MaxBytes {
		return nil, &Error{Where: "file", Message: tooLarge}
	}
	format, err := Detect(b)
	if err != nil {
		return nil, err
	}
	switch format {
	case FormatOCR:
		return ParseOCR(b, today)
	default:
		return nil, &Error{Where: "file", Message: "camt.054 files cannot be read yet"}
	}
}

// newFile is a file of format with every ignored kind counted at zero, so
// the import stores all four.
func newFile(format Format) *File {
	return &File{Format: format, Ignored: map[IgnoredKind]int{
		IgnoredDebit: 0, IgnoredNotBooked: 0, IgnoredCardInformation: 0, IgnoredZeroAmount: 0,
	}}
}

// finish is what every format's parser does last: the accounts distinct and
// ascending, the booking dates' bounds, and the fingerprints.
func (f *File) finish() {
	slices.Sort(f.Accounts)
	f.Accounts = slices.Compact(f.Accounts)
	for i, tx := range f.Transactions {
		if i == 0 || tx.BookedOn.Before(f.FirstBookedOn) {
			f.FirstBookedOn = tx.BookedOn
		}
		if i == 0 || tx.BookedOn.After(f.LastBookedOn) {
			f.LastBookedOn = tx.BookedOn
		}
	}
	Fingerprints(f.Transactions)
}

const tooLarge = "The file is larger than 10 MiB"

// dateOf is t's calendar day at UTC midnight, as businessDay answers it.
func dateOf(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// earliest is the first booking date a file may carry (D3).
var earliest = time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC)

// checkBooked is D3's rule on every booking date: on or before today, the
// request's Oslo day, and not before 2000-01-01. It answers the refusal's
// words, or "".
func checkBooked(d, today time.Time) string {
	switch {
	case d.After(today):
		return "the booking date " + d.Format("2006-01-02") + " is after today, " + today.Format("2006-01-02")
	case d.Before(earliest):
		return "the booking date " + d.Format("2006-01-02") + " is before 2000-01-01"
	}
	return ""
}
