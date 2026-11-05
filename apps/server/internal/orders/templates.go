package orders

// The quote template operations (orders design O23). Each answers 501
// until delivery A's quotes lane implements it.

import (
	"context"

	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
)

// ListQuoteTemplates is not implemented yet.
func (s *server) ListQuoteTemplates(_ context.Context, _ gen.ListQuoteTemplatesRequestObject) (gen.ListQuoteTemplatesResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// CreateQuoteTemplate is not implemented yet.
func (s *server) CreateQuoteTemplate(_ context.Context, _ gen.CreateQuoteTemplateRequestObject) (gen.CreateQuoteTemplateResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// DeleteQuoteTemplate is not implemented yet.
func (s *server) DeleteQuoteTemplate(_ context.Context, _ gen.DeleteQuoteTemplateRequestObject) (gen.DeleteQuoteTemplateResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// GetQuoteTemplate is not implemented yet.
func (s *server) GetQuoteTemplate(_ context.Context, _ gen.GetQuoteTemplateRequestObject) (gen.GetQuoteTemplateResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// UpdateQuoteTemplate is not implemented yet.
func (s *server) UpdateQuoteTemplate(_ context.Context, _ gen.UpdateQuoteTemplateRequestObject) (gen.UpdateQuoteTemplateResponseObject, error) {
	return nil, module.ErrNotImplemented
}
