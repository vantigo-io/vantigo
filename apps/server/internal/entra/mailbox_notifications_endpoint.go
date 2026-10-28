package entra

import (
	"context"

	"github.com/vantigo-io/vantigo/server/internal/entra/gen"
)

// This file is Microsoft Graph's webhook for the change notifications
// (calendar Outlook sync design O11.5), served by the generated strict
// wrapper. While the provider makes no subscription it holds no handshake
// window open and knows no subscription: a handshake is answered 404, and
// any notification 202, as one naming an unknown subscription is.

// PostEntraMailboxNotifications answers Graph's handshake and its
// notifications. (POST /api/v1/entra/mailbox-notifications)
func (s *server) PostEntraMailboxNotifications(_ context.Context, req gen.PostEntraMailboxNotificationsRequestObject) (gen.PostEntraMailboxNotificationsResponseObject, error) {
	if req.Params.ValidationToken != nil {
		return gen.PostEntraMailboxNotifications404Response{}, nil
	}
	return gen.PostEntraMailboxNotifications202Response{}, nil
}
