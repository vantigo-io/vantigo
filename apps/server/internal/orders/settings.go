package orders

// The settings operations (orders design §9, O12, O25). Each answers 501
// until delivery A's quotes lane implements it.

import (
	"context"

	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
)

// GetOrdersSettings is not implemented yet.
func (s *server) GetOrdersSettings(_ context.Context, _ gen.GetOrdersSettingsRequestObject) (gen.GetOrdersSettingsResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// PutOrdersSettings is not implemented yet.
func (s *server) PutOrdersSettings(_ context.Context, _ gen.PutOrdersSettingsRequestObject) (gen.PutOrdersSettingsResponseObject, error) {
	return nil, module.ErrNotImplemented
}
