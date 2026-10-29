package communications

import (
	"context"

	"github.com/vantigo-io/vantigo/server/internal/communications/gen"
)

// GetCommunicationsMessagesByIdRaw Download a message as it was received
// (GET /api/v1/communications/messages/{id}/raw)
//
// The contract's operation ahead of the receiving it serves: no message has
// an original until the mailbox sync stores one, so every id answers 404.
func (s *server) GetCommunicationsMessagesByIdRaw(ctx context.Context, req gen.GetCommunicationsMessagesByIdRawRequestObject) (gen.GetCommunicationsMessagesByIdRawResponseObject, error) {
	return gen.GetCommunicationsMessagesByIdRaw404Response{}, nil
}
