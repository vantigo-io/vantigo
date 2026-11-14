package invoices

import (
	"context"
	"errors"

	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
)

// errWriteOffNotBuilt answers the write-off's operations until the next
// commit of this series builds them (accounting design A16).
var errWriteOffNotBuilt = errors.New("invoices: the write-off is not built yet")

// PostInvoicesByIdWriteOff Write off an invoice's open amount
// (POST /api/v1/invoices/{id}/write-off)
func (s *server) PostInvoicesByIdWriteOff(context.Context, gen.PostInvoicesByIdWriteOffRequestObject) (gen.PostInvoicesByIdWriteOffResponseObject, error) {
	return nil, errWriteOffNotBuilt
}

// PostInvoicesByIdWriteOffUndo Undo an invoice's write-off
// (POST /api/v1/invoices/{id}/write-off/undo)
func (s *server) PostInvoicesByIdWriteOffUndo(context.Context, gen.PostInvoicesByIdWriteOffUndoRequestObject) (gen.PostInvoicesByIdWriteOffUndoResponseObject, error) {
	return nil, errWriteOffNotBuilt
}
