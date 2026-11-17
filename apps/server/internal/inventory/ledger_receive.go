package inventory

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// errReceiveUnbuilt is the seam's answer until the receive is built.
var errReceiveUnbuilt = errors.New("inventory: the ledger's receive is not built yet")

// Receive posts a receipt for another module's document (purchasing design P3).
func (l ledger) Receive(context.Context, pgx.Tx, contracts.StockSource, contracts.StockReceipt) (contracts.StockPosted, error) {
	return contracts.StockPosted{}, errReceiveUnbuilt
}

// ReverseReceipt takes a receipt back (purchasing design A5).
func (l ledger) ReverseReceipt(context.Context, pgx.Tx, contracts.StockSource, contracts.StockReversal) (contracts.StockPosted, error) {
	return contracts.StockPosted{}, errReceiveUnbuilt
}
