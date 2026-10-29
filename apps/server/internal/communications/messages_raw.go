package communications

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/communications/gen"
	"github.com/vantigo-io/vantigo/server/internal/communications/store"
	"github.com/vantigo-io/vantigo/server/internal/storage"
)

// GetCommunicationsMessagesByIdRaw Download a message as it was received
// (GET /api/v1/communications/messages/{id}/raw)
//
// The original of an inbound message, whole, as the mailbox sync stored it —
// its HTML unsanitised and its attachments inside — answered only as a file to
// save, message-{id}.eml, never rendered (delivery D, reading 22). Scoped as
// the attachment download is, by conversations-view alone. A message with no
// original (every one but an inbound message), an unknown id, and an object
// gone from the store all answer the same bare 404; any other storage failure
// answers 503 attachment_storage_unavailable.
func (s *server) GetCommunicationsMessagesByIdRaw(ctx context.Context, req gen.GetCommunicationsMessagesByIdRawRequestObject) (gen.GetCommunicationsMessagesByIdRawResponseObject, error) {
	key, err := store.New(s.deps.Pool).GetMessageRawPayloadKey(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && key == nil) {
		return gen.GetCommunicationsMessagesByIdRaw404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("communications: get the message's original: %w", err)
	}
	content, err := s.store.Get(ctx, *key)
	if errors.Is(err, storage.ErrNotExist) {
		return gen.GetCommunicationsMessagesByIdRaw404Response{}, nil
	} else if err != nil {
		return gen.GetCommunicationsMessagesByIdRaw503JSONResponse(flatErrorBody(
			"attachment_storage_unavailable", "Attachment storage is unavailable.")), nil
	}
	return gen.GetCommunicationsMessagesByIdRaw200Messagerfc822Response{
		Body: content,
		Headers: gen.GetCommunicationsMessagesByIdRaw200ResponseHeaders{
			ContentDisposition: apicommon.Ptr(fmt.Sprintf(`attachment; filename="message-%s.eml"`, hexN(req.Id))),
		},
	}, nil
}
