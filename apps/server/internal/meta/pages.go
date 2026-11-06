package meta

import (
	"context"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/meta/gen"
)

// PostMetaPages connects a Page. (POST /api/v1/meta/pages)
func (s *server) PostMetaPages(context.Context, gen.PostMetaPagesRequestObject) (gen.PostMetaPagesResponseObject, error) {
	return nil, errNotBuilt
}

// DeleteMetaPagesByPageId removes a Page. (DELETE /api/v1/meta/pages/{pageId})
func (s *server) DeleteMetaPagesByPageId(context.Context, gen.DeleteMetaPagesByPageIdRequestObject) (gen.DeleteMetaPagesByPageIdResponseObject, error) {
	return nil, errNotBuilt
}

// Endpoints is the Pages connected, each with its own status.
func (m *messenger) Endpoints(context.Context) ([]contracts.MessengerEndpoint, error) {
	return nil, errNotBuilt
}

// Check resolves a Page's id to a connected Page.
func (m *messenger) Check(context.Context, string) (contracts.MessengerEndpoint, error) {
	return contracts.MessengerEndpoint{}, errNotBuilt
}
