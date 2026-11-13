// Package bankfeed is the port between a business module and a bank that
// answers its account's transactions through an API (invoices bank feed
// design F1): what a module asks of a bank — check the credential, list the
// accounts it can read, and read one account's booked transactions over a
// range of days — in the bank's terms folded onto one shape.
//
// It is a platform package, Invoices' today and Expenses' later: it holds
// interfaces and values only, no SQL — every consumer keeps its own tables
// (module boundaries rule 4) — and it imports no module, only the standard
// library and internal/buildinfo. An adapter lives beside it (bankfeed/folio),
// and its fake beside the adapter (bankfeed/folio/foliotest) for tests only.
//
// The adapter decides a transaction's direction and kind; the module decides
// what a line means. Amounts cross the port in minor units only. An adapter
// never retries: a read is repeated by the consumer's next cycle.
package bankfeed

import (
	"context"
	"encoding/json"
	"time"
)

// Feed is one bank credential.
type Feed interface {
	// Verify makes the cheapest authenticated call, for the settings page.
	Verify(ctx context.Context) error
	// Accounts lists the accounts the credential can read.
	Accounts(ctx context.Context) ([]Account, error)
	// Transactions answers the booked transactions of account from..to, both
	// inclusive, each with the bytes the bank sent for it.
	Transactions(ctx context.Context, account string, from, to time.Time) ([]Transaction, error)
}

// AccountKind is what an account is for, as the bank names it.
type AccountKind string

const (
	// AccountOperational is the everyday account payments come in to.
	AccountOperational AccountKind = "operational"
	// AccountTax is the account withheld tax is kept on.
	AccountTax AccountKind = "tax"
	// AccountSavings is a savings account.
	AccountSavings AccountKind = "savings"
	// AccountEarmarks is an account set aside for a purpose.
	AccountEarmarks AccountKind = "earmarks"
	// AccountCard is a card's account: never one payments come in to.
	AccountCard AccountKind = "card"
	// AccountOther is a kind the adapter does not know.
	AccountOther AccountKind = "other"
)

// Account is one account the credential can read. Number is the account
// number as the bank writes it (a BBAN for a Norwegian bank).
type Account struct {
	Number, Name string
	Kind         AccountKind
}

// Direction is whether money came in to the account asked for or went out.
type Direction string

const (
	// DirectionCredit is money in.
	DirectionCredit Direction = "credit"
	// DirectionDebit is money out.
	DirectionDebit Direction = "debit"
	// DirectionUnknown is a transaction the adapter cannot place: the consumer
	// refuses it, never guesses.
	DirectionUnknown Direction = "unknown"
)

// Kind is what moved the money.
type Kind string

const (
	// KindTransfer is a payment between two accounts — a giro, a KID payment.
	KindTransfer Kind = "transfer"
	// KindCard is a card purchase.
	KindCard Kind = "card"
	// KindInternal is a transfer between two accounts of the same customer.
	KindInternal Kind = "internal"
)

// Transaction is one booked transaction as the bank answered it.
type Transaction struct {
	// ID is the bank's own id for it.
	ID string
	// BookedOn is the booking day, at UTC midnight.
	BookedOn time.Time
	// AmountMinor is the amount in minor units (øre for NOK), never signed:
	// Direction says which way it went.
	AmountMinor int64
	Currency    string
	Direction   Direction
	Kind        Kind
	// KID is the payment's KID as the bank wrote it, empty when it carried
	// none; Text the remittance text; DebtorName and DebtorAccount the payer,
	// as written. The adapter cuts none of them: lengths are the consumer's.
	KID, Text, DebtorName, DebtorAccount string
	// Raw is the bytes the bank sent for this transaction, as sent.
	Raw json.RawMessage
}
