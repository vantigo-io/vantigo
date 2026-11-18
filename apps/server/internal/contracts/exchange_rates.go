package contracts

import (
	"context"
	"time"
)

// ExchangeRates is Invoices' exchange rates (invoices phase 6 design I18,
// I28; plan C2 and reading 10): Norges Bank's middle rates of the currencies
// a workspace turned on, and the manual rates a manager entered. A single
// provider, Invoices; Accounting and Orders may read it, both optionally,
// and neither does yet — the journal carries each document's own rate.
type ExchangeRates interface {
	// Rate is currency's rate on the day the instant day falls on in the
	// workspace's business time zone: the latest rate on or before it and no
	// more than seven days before, a manual one being its day's. ok is false
	// when none is found. NOK is always 1.
	Rate(ctx context.Context, currency string, day time.Time) (ExchangeRate, bool, error)
}

// ExchangeRate is one rate: NOK per one unit of Currency on Day, a decimal
// string with ten decimals ("11.7234000000"), and where it came from —
// norges_bank, manual, or nok for NOK itself.
type ExchangeRate struct {
	Currency string
	Day      time.Time
	Rate     string
	Source   string
}
