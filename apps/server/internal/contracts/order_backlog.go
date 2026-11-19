package contracts

import "context"

// OrderBacklog is what a customer has on confirmed orders that is not yet
// invoiced, as the module that owns the orders reports it (invoices phase 6
// design I38): a read-only, in-process port the modules that judge a
// customer's credit read it through, without importing the owning module or
// reading its schema. Whichever enabled module owns orders implements it;
// Compose wires it into every module's Deps before any Mount runs (see
// Module.OrderBacklog). It is nil when no enabled module provides one (orders
// disabled), which a caller reads as "there are no orders", never as an
// error.
//
// It performs no authorization, never calls back into CustomerDirectory, and
// holds no transaction: a caller reads it before its own opens. A provider
// fails rather than under-reports.
type OrderBacklog interface {
	// UninvoicedForCustomer is the gross, VAT included, of what the
	// customer's confirmed orders hold that is neither cancelled nor
	// invoiced, per currency, by ISO 4217 code ascending. A customer with
	// none answers an empty list. There is no total across currencies.
	UninvoicedForCustomer(ctx context.Context, customerID int32) ([]CurrencyAmount, error)
}

// CurrencyAmount is an amount in one currency: decimal text, exact, two
// decimals, as every money figure crosses a contract.
type CurrencyAmount struct {
	Currency string
	Amount   string
}
