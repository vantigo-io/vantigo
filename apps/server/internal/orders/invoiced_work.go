package orders

import (
	"context"
	"errors"
	"log/slog"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// This file is the module's contracts.InvoicedWorkHolder (orders design O38,
// module-boundaries rule 10): the invoices issue stamps the allocations an
// invoice bills as invoiced, inside its own transaction, and the issue of the
// credit note that returns an allocation's line in full releases it. It runs
// on the caller's transaction and touches nothing else: no pool, no
// directory, no clock.

// errInvoicedWorkNotYet answers both directions until delivery B's Orders
// lane writes them. It is a failure, never a *contracts.WorkSourceRefusal.
var errInvoicedWorkNotYet = errors.New("orders: stamping invoice allocations is not implemented yet")

// invoicedWorkHolder is this module's contracts.InvoicedWorkHolder. It holds
// a logger and nothing else: its constructor runs for a module that is
// switched off too, and every row it writes arrives through the caller's tx.
type invoicedWorkHolder struct {
	logger *slog.Logger
}

var _ contracts.InvoicedWorkHolder = (*invoicedWorkHolder)(nil)

// newInvoicedWorkHolder is Module's InvoicedWork. It keeps d.Logger only.
func newInvoicedWorkHolder(d module.Deps) contracts.InvoicedWorkHolder {
	logger := d.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &invoicedWorkHolder{logger: logger}
}

// Kinds is the one kind this module holds: an invoice allocation.
func (h *invoicedWorkHolder) Kinds() []contracts.WorkSourceKind {
	return []contracts.WorkSourceKind{contracts.WorkSourceOrderAllocation}
}

// MarkInvoiced is not written yet (errInvoicedWorkNotYet).
func (h *invoicedWorkHolder) MarkInvoiced(context.Context, pgx.Tx, contracts.InvoiceRef, []contracts.WorkSource) error {
	return errInvoicedWorkNotYet
}

// ReleaseInvoiced is not written yet (errInvoicedWorkNotYet).
func (h *invoicedWorkHolder) ReleaseInvoiced(context.Context, pgx.Tx, contracts.InvoiceRef, []contracts.WorkSource) error {
	return errInvoicedWorkNotYet
}
