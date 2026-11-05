package orders

// The quote operations (orders design O13–O25): the CRUD of a draft, send,
// mark as sent, revise, accept, decline, cancel, copy, apply a template, the
// PDFs and the lists of a quote's stored documents and sends. Each answers
// 501 until delivery A's quotes lane implements it.

import (
	"context"

	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
)

// ListQuotes is not implemented yet.
func (s *server) ListQuotes(_ context.Context, _ gen.ListQuotesRequestObject) (gen.ListQuotesResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// CreateQuote is not implemented yet.
func (s *server) CreateQuote(_ context.Context, _ gen.CreateQuoteRequestObject) (gen.CreateQuoteResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// DeleteQuote is not implemented yet.
func (s *server) DeleteQuote(_ context.Context, _ gen.DeleteQuoteRequestObject) (gen.DeleteQuoteResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// GetQuote is not implemented yet.
func (s *server) GetQuote(_ context.Context, _ gen.GetQuoteRequestObject) (gen.GetQuoteResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// UpdateQuote is not implemented yet.
func (s *server) UpdateQuote(_ context.Context, _ gen.UpdateQuoteRequestObject) (gen.UpdateQuoteResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// AcceptQuote is not implemented yet.
func (s *server) AcceptQuote(_ context.Context, _ gen.AcceptQuoteRequestObject) (gen.AcceptQuoteResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// CancelQuote is not implemented yet.
func (s *server) CancelQuote(_ context.Context, _ gen.CancelQuoteRequestObject) (gen.CancelQuoteResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// CopyQuote is not implemented yet.
func (s *server) CopyQuote(_ context.Context, _ gen.CopyQuoteRequestObject) (gen.CopyQuoteResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// DeclineQuote is not implemented yet.
func (s *server) DeclineQuote(_ context.Context, _ gen.DeclineQuoteRequestObject) (gen.DeclineQuoteResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// ListQuoteDocuments is not implemented yet.
func (s *server) ListQuoteDocuments(_ context.Context, _ gen.ListQuoteDocumentsRequestObject) (gen.ListQuoteDocumentsResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// ListQuoteMailings is not implemented yet.
func (s *server) ListQuoteMailings(_ context.Context, _ gen.ListQuoteMailingsRequestObject) (gen.ListQuoteMailingsResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// MarkQuoteSent is not implemented yet.
func (s *server) MarkQuoteSent(_ context.Context, _ gen.MarkQuoteSentRequestObject) (gen.MarkQuoteSentResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// GetQuotePdf is not implemented yet.
func (s *server) GetQuotePdf(_ context.Context, _ gen.GetQuotePdfRequestObject) (gen.GetQuotePdfResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// GetQuoteProforma is not implemented yet.
func (s *server) GetQuoteProforma(_ context.Context, _ gen.GetQuoteProformaRequestObject) (gen.GetQuoteProformaResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// ReviseQuote is not implemented yet.
func (s *server) ReviseQuote(_ context.Context, _ gen.ReviseQuoteRequestObject) (gen.ReviseQuoteResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// SendQuote is not implemented yet.
func (s *server) SendQuote(_ context.Context, _ gen.SendQuoteRequestObject) (gen.SendQuoteResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// ApplyQuoteTemplate is not implemented yet.
func (s *server) ApplyQuoteTemplate(_ context.Context, _ gen.ApplyQuoteTemplateRequestObject) (gen.ApplyQuoteTemplateResponseObject, error) {
	return nil, module.ErrNotImplemented
}
