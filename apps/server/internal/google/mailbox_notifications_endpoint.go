package google

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/google/gen"
	"github.com/vantigo-io/vantigo/server/internal/google/store"
)

// This file is Google Calendar's push notifications endpoint (Google
// Workspace design G32), anonymous and on the public host's allowlist: Google
// posts a message for each change on a channel the module opened with
// events.watch (mailbox_notifications.go), its facts in headers and no body
// worth reading. The endpoint reads the headers alone and answers 204 to
// every request, whatever it named and whatever happened, so it tells a
// caller nothing of which channels exist. A message is a nudge: a valid one
// marks its channel notified, which makes the person's sync due through
// Notified, and nothing it says is kept. The logs carry no channel id, no
// token, no resource id and no Google id — a count at most.

// The endpoint's figures.
const (
	// notificationsDBBudget bounds the database work of one message.
	notificationsDBBudget = 2 * time.Second
	// maxResourceID bounds the resource id a message may carry.
	maxResourceID = 255
	// channelTokenBytes is a channel token's randomness: 32 bytes, 43
	// characters of base64url without padding.
	channelTokenBytes = 32
)

// The resource states a message names (Google's X-Goog-Resource-State): sync
// opens a channel and says nothing changed; exists and not_exists are a
// change.
const (
	resourceStateExists    = "exists"
	resourceStateNotExists = "not_exists"
)

// PostGoogleCalendarNotifications records Google's push message: 204 always.
// (POST /api/v1/google/calendar-notifications)
func (s *server) PostGoogleCalendarNotifications(ctx context.Context, req gen.PostGoogleCalendarNotificationsRequestObject) (gen.PostGoogleCalendarNotificationsResponseObject, error) {
	marked, err := s.notifiedChannel(ctx, req.Params)
	switch {
	case err != nil:
		s.keys.c.logger.WarnContext(ctx, "google: a calendar notification could not be recorded")
	case !marked:
		s.keys.c.logger.DebugContext(ctx, "google: a calendar notification was ignored")
	}
	return gen.PostGoogleCalendarNotifications204Response{}, nil
}

// notifiedChannel marks the channel the message names notified, and reports
// whether it did. A message is taken only when its channel id is a UUID a row
// holds, its token's SHA-256 is the row's (compared in constant time), its
// resource id the row's and its state a change; anything else marks nothing,
// with no error. A request with no plausible header reads nothing.
func (s *server) notifiedChannel(ctx context.Context, p gen.PostGoogleCalendarNotificationsParams) (bool, error) {
	state := deref(p.XGoogResourceState)
	if state != resourceStateExists && state != resourceStateNotExists {
		return false, nil
	}
	channel, ok := channelID(deref(p.XGoogChannelID))
	token := deref(p.XGoogChannelToken)
	resource := deref(p.XGoogResourceID)
	if !ok || !plausibleChannelToken(token) || resource == "" || len(resource) > maxResourceID {
		return false, nil
	}
	ctx, cancel := context.WithTimeout(ctx, notificationsDBBudget)
	defer cancel()
	row, err := s.q.GetChannelByID(ctx, channel)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return false, nil
	case err != nil:
		return false, err
	}
	hash := sha256.Sum256([]byte(token))
	if subtle.ConstantTimeCompare(hash[:], row.TokenHash) != 1 ||
		subtle.ConstantTimeCompare([]byte(resource), []byte(row.ResourceID)) != 1 {
		return false, nil
	}
	n, err := s.q.MarkChannelNotified(ctx, store.MarkChannelNotifiedParams{Now: s.keys.c.now(), ChannelID: channel})
	return n > 0, err
}

// channelID parses a channel id strictly: a UUID in its canonical 36
// characters, as Watch made it.
func channelID(s string) (uuid.UUID, bool) {
	if len(s) != 36 {
		return uuid.UUID{}, false
	}
	id, err := uuid.Parse(s)
	return id, err == nil && id != uuid.Nil
}

// plausibleChannelToken reports whether token has the shape every token Watch
// makes has: channelTokenBytes random bytes in base64url without padding.
func plausibleChannelToken(token string) bool {
	if len(token) != base64.RawURLEncoding.EncodedLen(channelTokenBytes) {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	return err == nil && len(raw) == channelTokenBytes
}
