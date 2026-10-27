package invoices

import (
	"context"
	"errors"

	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
)

// errBillingRunsPending stands in for the billing runs' handlers until task 3
// of the billing runs plan writes them.
var errBillingRunsPending = errors.New("invoices: billing runs are not implemented yet")

func (s *server) GetInvoicesBillingRuns(context.Context, gen.GetInvoicesBillingRunsRequestObject) (gen.GetInvoicesBillingRunsResponseObject, error) {
	return nil, errBillingRunsPending
}

func (s *server) PostInvoicesBillingRuns(context.Context, gen.PostInvoicesBillingRunsRequestObject) (gen.PostInvoicesBillingRunsResponseObject, error) {
	return nil, errBillingRunsPending
}

func (s *server) GetInvoicesBillingRunsById(context.Context, gen.GetInvoicesBillingRunsByIdRequestObject) (gen.GetInvoicesBillingRunsByIdResponseObject, error) {
	return nil, errBillingRunsPending
}

func (s *server) PostInvoicesBillingRunsByIdCancel(context.Context, gen.PostInvoicesBillingRunsByIdCancelRequestObject) (gen.PostInvoicesBillingRunsByIdCancelResponseObject, error) {
	return nil, errBillingRunsPending
}
