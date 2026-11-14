package accounting

// The seam of delivery C: the operations its lanes fill in.

import (
	"context"
	"errors"

	"github.com/vantigo-io/vantigo/server/internal/accounting/gen"
)

var errNotYet = errors.New("accounting: not built yet")

func (s *server) ListAccountingSupplierInvoices(context.Context, gen.ListAccountingSupplierInvoicesRequestObject) (gen.ListAccountingSupplierInvoicesResponseObject, error) {
	return nil, errNotYet
}

func (s *server) CreateAccountingSupplierInvoice(context.Context, gen.CreateAccountingSupplierInvoiceRequestObject) (gen.CreateAccountingSupplierInvoiceResponseObject, error) {
	return nil, errNotYet
}

func (s *server) DeleteAccountingSupplierInvoice(context.Context, gen.DeleteAccountingSupplierInvoiceRequestObject) (gen.DeleteAccountingSupplierInvoiceResponseObject, error) {
	return nil, errNotYet
}

func (s *server) GetAccountingSupplierInvoice(context.Context, gen.GetAccountingSupplierInvoiceRequestObject) (gen.GetAccountingSupplierInvoiceResponseObject, error) {
	return nil, errNotYet
}

func (s *server) UpdateAccountingSupplierInvoice(context.Context, gen.UpdateAccountingSupplierInvoiceRequestObject) (gen.UpdateAccountingSupplierInvoiceResponseObject, error) {
	return nil, errNotYet
}

func (s *server) UploadAccountingSupplierInvoiceFile(context.Context, gen.UploadAccountingSupplierInvoiceFileRequestObject) (gen.UploadAccountingSupplierInvoiceFileResponseObject, error) {
	return nil, errNotYet
}

func (s *server) DeleteAccountingSupplierInvoiceFile(context.Context, gen.DeleteAccountingSupplierInvoiceFileRequestObject) (gen.DeleteAccountingSupplierInvoiceFileResponseObject, error) {
	return nil, errNotYet
}

func (s *server) GetAccountingSupplierInvoiceFile(context.Context, gen.GetAccountingSupplierInvoiceFileRequestObject) (gen.GetAccountingSupplierInvoiceFileResponseObject, error) {
	return nil, errNotYet
}

func (s *server) PayAccountingSupplierInvoice(context.Context, gen.PayAccountingSupplierInvoiceRequestObject) (gen.PayAccountingSupplierInvoiceResponseObject, error) {
	return nil, errNotYet
}

func (s *server) RemoveAccountingSupplierPayment(context.Context, gen.RemoveAccountingSupplierPaymentRequestObject) (gen.RemoveAccountingSupplierPaymentResponseObject, error) {
	return nil, errNotYet
}

func (s *server) PostAccountingSupplierInvoice(context.Context, gen.PostAccountingSupplierInvoiceRequestObject) (gen.PostAccountingSupplierInvoiceResponseObject, error) {
	return nil, errNotYet
}

func (s *server) ReverseAndCopyAccountingSupplierInvoice(context.Context, gen.ReverseAndCopyAccountingSupplierInvoiceRequestObject) (gen.ReverseAndCopyAccountingSupplierInvoiceResponseObject, error) {
	return nil, errNotYet
}

func (s *server) ListAccountingSuppliers(context.Context, gen.ListAccountingSuppliersRequestObject) (gen.ListAccountingSuppliersResponseObject, error) {
	return nil, errNotYet
}

func (s *server) CreateAccountingSupplier(context.Context, gen.CreateAccountingSupplierRequestObject) (gen.CreateAccountingSupplierResponseObject, error) {
	return nil, errNotYet
}

func (s *server) LookupAccountingSupplierBrreg(context.Context, gen.LookupAccountingSupplierBrregRequestObject) (gen.LookupAccountingSupplierBrregResponseObject, error) {
	return nil, errNotYet
}

func (s *server) DeleteAccountingSupplier(context.Context, gen.DeleteAccountingSupplierRequestObject) (gen.DeleteAccountingSupplierResponseObject, error) {
	return nil, errNotYet
}

func (s *server) GetAccountingSupplier(context.Context, gen.GetAccountingSupplierRequestObject) (gen.GetAccountingSupplierResponseObject, error) {
	return nil, errNotYet
}

func (s *server) UpdateAccountingSupplier(context.Context, gen.UpdateAccountingSupplierRequestObject) (gen.UpdateAccountingSupplierResponseObject, error) {
	return nil, errNotYet
}

func (s *server) ExportAccountingSupplier(context.Context, gen.ExportAccountingSupplierRequestObject) (gen.ExportAccountingSupplierResponseObject, error) {
	return nil, errNotYet
}

func (s *server) AcknowledgeAccountingUnposted(context.Context, gen.AcknowledgeAccountingUnpostedRequestObject) (gen.AcknowledgeAccountingUnpostedResponseObject, error) {
	return nil, errNotYet
}

func (s *server) EnterAccountingUnpostedNokAmounts(context.Context, gen.EnterAccountingUnpostedNokAmountsRequestObject) (gen.EnterAccountingUnpostedNokAmountsResponseObject, error) {
	return nil, errNotYet
}

func (s *server) MakeAccountingUnpostedSupplierInvoice(context.Context, gen.MakeAccountingUnpostedSupplierInvoiceRequestObject) (gen.MakeAccountingUnpostedSupplierInvoiceResponseObject, error) {
	return nil, errNotYet
}
