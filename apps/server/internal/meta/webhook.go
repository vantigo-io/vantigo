package meta

import (
	"context"
	"errors"

	"github.com/vantigo-io/vantigo/server/internal/meta/gen"
)

// maxWebhookBody is the most of a post's body read (Messenger design N11).
const maxWebhookBody int64 = 1 << 20

var errNotBuilt = errors.New("meta: not built yet")

// GetMetaWebhook answers Meta's handshake. (GET /api/v1/meta/webhook)
func (s *server) GetMetaWebhook(context.Context, gen.GetMetaWebhookRequestObject) (gen.GetMetaWebhookResponseObject, error) {
	return nil, errNotBuilt
}

// PostMetaWebhook stores Meta's event. (POST /api/v1/meta/webhook)
func (s *server) PostMetaWebhook(context.Context, gen.PostMetaWebhookRequestObject) (gen.PostMetaWebhookResponseObject, error) {
	return nil, errNotBuilt
}
