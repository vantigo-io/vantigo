package contracts

import (
	"context"
	"errors"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/bankfeed"
)

// BankFeed is the one sanctioned way a module reads the bank feed Invoices
// holds (accounting design A29): Invoices owns the one connection to the bank
// and its sealed key (invoices bank feed design F2, F3), and serves what the
// port internal/bankfeed answers through it — every account the key reads,
// and an account's booked transactions in every direction and of every kind,
// where Invoices itself keeps only incoming payments. A single-provider,
// read-only, in-process port. Compose and Workers wire it into every
// module's Deps (see Module.BankFeed); it is nil when invoices is disabled or
// the instance switch INVOICES_BANK_FEED_ENABLED is off, so no module can
// reach a bank the operator switched off.
//
// Every method is a call to the bank for Accounts and Transactions: it is
// made with no transaction of the caller's open (module-boundaries rule 8's
// discipline, and the lease workers' rule), never retried, and answers the
// port's errors as they are — bankfeed.ErrUnauthorized when the bank refused
// the key (Invoices records the refusal, as for its own calls), a
// *bankfeed.ErrRateLimited with the wait the bank asked for, any other error
// for a read that failed — and ErrBankFeedNotConnected when no key is stored
// or it cannot be opened. It performs no authorization: a caller has checked
// its own permission.
type BankFeed interface {
	// Status is the connection as Invoices holds it, read from its own
	// tables with no call to the bank.
	Status(ctx context.Context) (BankFeedStatus, error)
	// Accounts lists the accounts the stored key can read, as the bank
	// answers them, cards and tax accounts included.
	Accounts(ctx context.Context) ([]bankfeed.Account, error)
	// Transactions answers the booked transactions of account from..to, both
	// inclusive, as the bank answers them: credits and debits, transfers,
	// card purchases and internal transfers. account is the account number
	// as Accounts answers it, or its 11 digits.
	Transactions(ctx context.Context, account string, from, to time.Time) ([]bankfeed.Transaction, error)
}

// BankFeedStatus is the bank connection as Invoices holds it.
type BankFeedStatus struct {
	// Connected is true when a key is stored.
	Connected bool
	// Refused is true when the bank refused the stored key: it must be
	// entered again in Invoices before anything is read.
	Refused bool
	// Provider is the bank ("folio"), "" when none is connected.
	Provider string
	// Interval is how often the feed is read: a reader of its own reads on
	// the same cadence.
	Interval time.Duration
}

// ErrBankFeedNotConnected is a BankFeed call with no key stored in Invoices,
// or one this server cannot open.
var ErrBankFeedNotConnected = errors.New("contracts: no bank is connected in Invoices")
