// Package bankstmt reads a bank statement file into lines (accounting design
// A27): camt.053, the ISO 20022 bank-to-customer statement every Norwegian
// bank sends (camt053.go), and CSV, read by a column mapping a person saves
// per bank account (csv.go). It is Accounting's own, beside the ledger: it
// imports only the standard library, holds no SQL and knows nothing of the
// books — what a line is matched to is the module's.
//
// Invoices reads camt.054 and OCR giro in its own internal/invoices/bankfile
// for the customer payments it registers; a module may not import another's
// packages, and a statement is another document (the account's whole
// movement, with balances), so the little the two share — reading a
// Norwegian account number — is written again here rather than lifted.
package bankstmt

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math/big"
	"strings"
	"time"
)

// Statement is one account's statement in a file: camt.053 holds one or
// more, a CSV file one.
type Statement struct {
	// Account is the account's 11-digit BBAN, as the file names it (a
	// Norwegian IBAN read as its BBAN); Raw is what the file wrote, for a
	// refusal that names an account read as no Norwegian account.
	Account, Raw string
	// Ref is the statement's own id in the file, "" when it has none.
	Ref      string
	Currency string
	// From and To are the period the statement covers, at UTC midnight.
	From, To time.Time
	// Opening and Closing are the booked balances the file gives, signed;
	// nil when it gives none.
	Opening, Closing *big.Rat
	Lines            []Line
	// NotBooked counts the entries left out: pending or information only.
	NotBooked int
}

// Line is one booked movement on the account.
type Line struct {
	BookedOn time.Time
	ValueOn  *time.Time
	// Amount is signed: money in positive.
	Amount *big.Rat
	Text   string
	// Reference is the payment's own reference (an end-to-end id), "" when
	// none.
	Reference string
	// KID is the structured creditor reference, "" when none.
	KID string
	// CounterpartyName and CounterpartyAccount are the payer of money in, the
	// payee of money out.
	CounterpartyName, CounterpartyAccount string
	// BankID is the bank's own id of the entry (its AcctSvcrRef, or its one
	// transaction's), "" when it gives none.
	BankID string
	// TxReference is a single transaction's own AcctSvcrRef, "" when none.
	TxReference string
	// Kind is transfer, card, internal or other.
	Kind string
	// Reversal is an entry the bank marks as the reversal of an earlier one.
	Reversal bool
	// Batch is a batch entry's transactions, nil for a single one.
	Batch []BatchItem
}

// BatchItem is one transaction of a batch entry.
type BatchItem struct {
	Amount                *big.Rat
	KID, Reference, Name  string
	Text, BankID, Account string
}

// The kinds of Line.Kind.
const (
	KindTransfer = "transfer"
	KindCard     = "card"
	KindInternal = "internal"
	KindOther    = "other"
)

// InvalidError is a file that cannot be read as what it claims to be, and
// why, in words a person can act on.
type InvalidError struct{ Reason string }

func (e *InvalidError) Error() string { return "bankstmt: " + e.Reason }

func invalid(reason string) error { return &InvalidError{Reason: reason} }

// ErrTooLarge is a file over the limits a reader is given.
var ErrTooLarge = errors.New("bankstmt: the file holds more than a statement may")

// Limits bound what a file may hold.
type Limits struct {
	// Bytes is the most the file may be; Lines the most lines it may hold
	// over all its statements; Statements the most statements.
	Bytes      int64
	Lines      int
	Statements int
}

// DefaultLimits are a statement file's bounds: 20 MB, 100 000 lines, 500
// statements.
var DefaultLimits = Limits{Bytes: 20 << 20, Lines: 100_000, Statements: 500}

// Fingerprint is a line's hash over the account and every field a bank
// writes for it: two lines with the same fingerprint are the same movement
// read twice — or two genuine identical movements, which the ordinal among a
// file's lines of that fingerprint tells apart.
func Fingerprint(account string, l Line) string {
	value := ""
	if l.ValueOn != nil {
		value = l.ValueOn.Format(time.DateOnly)
	}
	h := sha256.Sum256([]byte(strings.Join([]string{account, l.BookedOn.Format(time.DateOnly), value,
		l.Amount.FloatString(2), l.Text, l.Reference, l.KID, l.CounterpartyName, l.CounterpartyAccount}, "\x1f")))
	return hex.EncodeToString(h[:])
}

// NormaliseAccount reads a Norwegian account number as a bank file or a
// person writes it: its 11 digits, once spaces and dots are dropped, whose
// MOD11 check digit holds; or a Norwegian IBAN — NO, two check digits and
// the BBAN — whose ISO 13616 mod-97 check holds, read as that BBAN.
func NormaliseAccount(s string) (bban string, ok bool) {
	s = strings.ToUpper(strings.NewReplacer(" ", "", ".", "").Replace(strings.TrimSpace(s)))
	if len(s) == 11 && allDigits(s) {
		if !mod11(s) {
			return "", false
		}
		return s, true
	}
	if len(s) != 15 || !strings.HasPrefix(s, "NO") || !allDigits(s[2:]) {
		return "", false
	}
	n, _ := new(big.Int).SetString(s[4:]+"2324"+s[2:4], 10)
	if new(big.Int).Mod(n, big.NewInt(97)).Int64() != 1 || !mod11(s[4:]) {
		return "", false
	}
	return s[4:], true
}

// mod11 is the Norwegian kontonummer's check over 11 digits.
func mod11(digits string) bool {
	weights := [10]int{5, 4, 3, 2, 7, 6, 5, 4, 3, 2}
	sum := 0
	for i, w := range weights {
		sum += int(digits[i]-'0') * w
	}
	return int(digits[10]-'0') == (11-sum%11)%11
}

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return s != ""
}

// clip is s with its white space folded and cut to n runes.
func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
