package accounting

// The seam of delivery D: the VAT operations its lanes fill in.

import (
	"context"
	"errors"

	"github.com/vantigo-io/vantigo/server/internal/accounting/gen"
)

var errNotYet = errors.New("accounting: not built yet")

func (s *server) ListAccountingVatPostings(context.Context, gen.ListAccountingVatPostingsRequestObject) (gen.ListAccountingVatPostingsResponseObject, error) {
	return nil, errNotYet
}

func (s *server) GetAccountingVatSpecification(context.Context, gen.GetAccountingVatSpecificationRequestObject) (gen.GetAccountingVatSpecificationResponseObject, error) {
	return nil, errNotYet
}

func (s *server) ListAccountingVatTerms(context.Context, gen.ListAccountingVatTermsRequestObject) (gen.ListAccountingVatTermsResponseObject, error) {
	return nil, errNotYet
}

func (s *server) GetAccountingVatTerm(context.Context, gen.GetAccountingVatTermRequestObject) (gen.GetAccountingVatTermResponseObject, error) {
	return nil, errNotYet
}

func (s *server) CloseAccountingVatTerm(context.Context, gen.CloseAccountingVatTermRequestObject) (gen.CloseAccountingVatTermResponseObject, error) {
	return nil, errNotYet
}

func (s *server) FileAccountingVatTerm(context.Context, gen.FileAccountingVatTermRequestObject) (gen.FileAccountingVatTermResponseObject, error) {
	return nil, errNotYet
}

func (s *server) ReopenAccountingVatTerm(context.Context, gen.ReopenAccountingVatTermRequestObject) (gen.ReopenAccountingVatTermResponseObject, error) {
	return nil, errNotYet
}

func (s *server) UnfileAccountingVatTerm(context.Context, gen.UnfileAccountingVatTermRequestObject) (gen.UnfileAccountingVatTermResponseObject, error) {
	return nil, errNotYet
}

func (s *server) DownloadAccountingVatReturn(context.Context, gen.DownloadAccountingVatReturnRequestObject) (gen.DownloadAccountingVatReturnResponseObject, error) {
	return nil, errNotYet
}
