package orders

// The sales order operations (orders design O18, O26, O28, O32): the CRUD of
// an order, confirm, hold, release, lock, unlock, cancel, cancel remaining,
// delivery marking, the change log, the PDFs, send and the lists of an
// order's stored documents and sends. Each answers 501 until delivery A's
// orders lane implements it.

import (
	"context"

	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
)

// ListSalesOrders is not implemented yet.
func (s *server) ListSalesOrders(_ context.Context, _ gen.ListSalesOrdersRequestObject) (gen.ListSalesOrdersResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// CreateSalesOrder is not implemented yet.
func (s *server) CreateSalesOrder(_ context.Context, _ gen.CreateSalesOrderRequestObject) (gen.CreateSalesOrderResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// DeleteSalesOrder is not implemented yet.
func (s *server) DeleteSalesOrder(_ context.Context, _ gen.DeleteSalesOrderRequestObject) (gen.DeleteSalesOrderResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// GetSalesOrder is not implemented yet.
func (s *server) GetSalesOrder(_ context.Context, _ gen.GetSalesOrderRequestObject) (gen.GetSalesOrderResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// UpdateSalesOrder is not implemented yet.
func (s *server) UpdateSalesOrder(_ context.Context, _ gen.UpdateSalesOrderRequestObject) (gen.UpdateSalesOrderResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// CancelSalesOrder is not implemented yet.
func (s *server) CancelSalesOrder(_ context.Context, _ gen.CancelSalesOrderRequestObject) (gen.CancelSalesOrderResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// CancelSalesOrderRemaining is not implemented yet.
func (s *server) CancelSalesOrderRemaining(_ context.Context, _ gen.CancelSalesOrderRemainingRequestObject) (gen.CancelSalesOrderRemainingResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// ListSalesOrderChanges is not implemented yet.
func (s *server) ListSalesOrderChanges(_ context.Context, _ gen.ListSalesOrderChangesRequestObject) (gen.ListSalesOrderChangesResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// ConfirmSalesOrder is not implemented yet.
func (s *server) ConfirmSalesOrder(_ context.Context, _ gen.ConfirmSalesOrderRequestObject) (gen.ConfirmSalesOrderResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// ListSalesOrderDocuments is not implemented yet.
func (s *server) ListSalesOrderDocuments(_ context.Context, _ gen.ListSalesOrderDocumentsRequestObject) (gen.ListSalesOrderDocumentsResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// HoldSalesOrder is not implemented yet.
func (s *server) HoldSalesOrder(_ context.Context, _ gen.HoldSalesOrderRequestObject) (gen.HoldSalesOrderResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// LockSalesOrder is not implemented yet.
func (s *server) LockSalesOrder(_ context.Context, _ gen.LockSalesOrderRequestObject) (gen.LockSalesOrderResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// ListSalesOrderMailings is not implemented yet.
func (s *server) ListSalesOrderMailings(_ context.Context, _ gen.ListSalesOrderMailingsRequestObject) (gen.ListSalesOrderMailingsResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// MarkSalesOrderDelivered is not implemented yet.
func (s *server) MarkSalesOrderDelivered(_ context.Context, _ gen.MarkSalesOrderDeliveredRequestObject) (gen.MarkSalesOrderDeliveredResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// GetSalesOrderPdf is not implemented yet.
func (s *server) GetSalesOrderPdf(_ context.Context, _ gen.GetSalesOrderPdfRequestObject) (gen.GetSalesOrderPdfResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// GetSalesOrderProforma is not implemented yet.
func (s *server) GetSalesOrderProforma(_ context.Context, _ gen.GetSalesOrderProformaRequestObject) (gen.GetSalesOrderProformaResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// ReleaseSalesOrder is not implemented yet.
func (s *server) ReleaseSalesOrder(_ context.Context, _ gen.ReleaseSalesOrderRequestObject) (gen.ReleaseSalesOrderResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// SendSalesOrder is not implemented yet.
func (s *server) SendSalesOrder(_ context.Context, _ gen.SendSalesOrderRequestObject) (gen.SendSalesOrderResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// UndoSalesOrderDelivered is not implemented yet.
func (s *server) UndoSalesOrderDelivered(_ context.Context, _ gen.UndoSalesOrderDeliveredRequestObject) (gen.UndoSalesOrderDeliveredResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// UnlockSalesOrder is not implemented yet.
func (s *server) UnlockSalesOrder(_ context.Context, _ gen.UnlockSalesOrderRequestObject) (gen.UnlockSalesOrderResponseObject, error) {
	return nil, module.ErrNotImplemented
}
