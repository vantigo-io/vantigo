package communications

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/communications/gen"
	"github.com/vantigo-io/vantigo/server/internal/communications/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/secrets"
	"github.com/vantigo-io/vantigo/server/internal/storage"
)

// This file is the public media route (Messenger design N18): Meta's generic
// template takes an image address that Meta fetches, so a card's image must be
// reachable from the internet, while storage has no public address by design.
// Communications serves a card attachment of an outbound message under a signed
// token, on the public host, and the outbox puts that address in the carousel.

// publicMediaPurpose is the secrets Box purpose the token's MAC is made under.
const publicMediaPurpose = "communications/public-media"

// publicMediaTokenLifetime is how long after the send a card's token holds
// (N18). Seven days is the design's figure until delivery 0 has shown whether
// Meta keeps a template's image: a longer lifetime is a change of this
// constant alone.
const publicMediaTokenLifetime = 7 * 24 * time.Hour

// The response headers of N18.
const (
	publicMediaCacheControl   = "private, max-age=86400"
	publicMediaReferrerPolicy = "no-referrer"
)

// publicMediaPath is the route's path beneath the instance's public base URL.
const publicMediaPath = "/api/v1/communications/public-media/"

// publicMediaMAC is the MAC of a token's first two parts: the attachment and
// the instant the token stops holding.
func publicMediaMAC(box *secrets.Box, attachmentID uuid.UUID, expires int64) ([]byte, error) {
	return box.MAC(publicMediaPurpose, []byte(attachmentID.String()+"."+strconv.FormatInt(expires, 10)))
}

// publicMediaToken is the token of a card attachment, <id>.<exp>.<mac>: expires
// is a Unix time in seconds, mac the unpadded base64url of the Box's MAC.
func publicMediaToken(box *secrets.Box, attachmentID uuid.UUID, expires time.Time) (string, error) {
	if box == nil {
		return "", errors.New("communications: the secrets box is required to sign a media token")
	}
	exp := expires.Unix()
	mac, err := publicMediaMAC(box, attachmentID, exp)
	if err != nil {
		return "", fmt.Errorf("communications: sign a media token: %w", err)
	}
	return attachmentID.String() + "." + strconv.FormatInt(exp, 10) + "." + base64.RawURLEncoding.EncodeToString(mac), nil
}

// publicMediaURL is the address Meta fetches a card's image from, at now: the
// instance's public base URL and a token that holds for publicMediaTokenLifetime.
// It is "" when the instance has no public host, which a carousel cannot be
// sent without.
func publicMediaURL(d module.Deps, attachmentID uuid.UUID, now time.Time) (string, error) {
	if d.Config == nil || d.Config.PublicBaseURL == "" {
		return "", nil
	}
	token, err := publicMediaToken(d.Secrets, attachmentID, now.Add(publicMediaTokenLifetime))
	if err != nil {
		return "", err
	}
	return d.Config.PublicBaseURL + publicMediaPath + token, nil
}

// parsePublicMediaToken is the attachment a token names when it is well formed,
// unexpired at now and signed by this instance; false for every other token, so
// the route answers all of them alike.
func parsePublicMediaToken(box *secrets.Box, token string, now time.Time) (uuid.UUID, bool) {
	if box == nil {
		return uuid.Nil, false
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(parts[0])
	if err != nil || id.String() != parts[0] {
		return uuid.Nil, false
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || strconv.FormatInt(exp, 10) != parts[1] {
		return uuid.Nil, false
	}
	given, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return uuid.Nil, false
	}
	want, err := publicMediaMAC(box, id, exp)
	if err != nil || !secrets.MACEqual(given, want) {
		return uuid.Nil, false
	}
	if !now.Before(time.Unix(exp, 0)) {
		return uuid.Nil, false
	}
	return id, true
}

// publicMedia is a card image the public route serves: its stored object and
// what is known of it.
type publicMedia struct {
	id          uuid.UUID
	storageKey  string
	contentType string
	size        int64
}

// publicMediaOf is the card image a token names, or nil for every refusal — an
// expired, wrong or foreign token, a file attachment, an inbound one, a provider
// that does not ask for public media — which the route answers alike, with a bare
// 404 (N18).
func (s *server) publicMediaOf(ctx context.Context, token string) (*publicMedia, error) {
	id, ok := parsePublicMediaToken(s.deps.Secrets, token, s.deps.Clock())
	if !ok {
		return nil, nil
	}
	row, err := store.New(s.deps.Pool).GetPublicCardAttachment(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("communications: get a public card attachment: %w", err)
	}
	provider, err := s.channelMessenger(ctx, row.Provider)
	if err != nil {
		return nil, err
	}
	if provider == nil {
		return nil, nil
	}
	caps, err := messengerCapabilities(ctx, provider)
	if err != nil {
		return nil, err
	}
	if !caps.PublicMedia {
		return nil, nil
	}
	return &publicMedia{id: id, storageKey: row.StorageKey, contentType: sanitizedAttachmentContentType(row.ContentType), size: row.SizeBytes}, nil
}

// publicMediaHeaders are the response headers of N18.
func publicMediaHeaders() (cache, referrer, nosniff *string) {
	return apicommon.Ptr(publicMediaCacheControl), apicommon.Ptr(publicMediaReferrerPolicy), apicommon.Ptr("nosniff")
}

// GetCommunicationsPublicMediaByToken A card image for Meta
// (GET /api/v1/communications/public-media/{token})
//
// It serves a card attachment of an outbound message on a channel whose
// provider asks for public media, and nothing else. An expired, wrong or
// foreign token, a file attachment, an inbound one and a missing object are the
// same bare 404, never a 401, so the route cannot be used to probe. A fetch is
// logged with the attachment id, never the token.
func (s *server) GetCommunicationsPublicMediaByToken(ctx context.Context, req gen.GetCommunicationsPublicMediaByTokenRequestObject) (gen.GetCommunicationsPublicMediaByTokenResponseObject, error) {
	notFound := gen.GetCommunicationsPublicMediaByToken404Response{}
	media, err := s.publicMediaOf(ctx, req.Token)
	if err != nil || media == nil {
		return notFound, err
	}
	content, err := s.store.Get(ctx, media.storageKey)
	if errors.Is(err, storage.ErrNotExist) {
		return notFound, nil
	}
	if err != nil {
		return nil, fmt.Errorf("communications: read a card image: %w", err)
	}
	s.deps.Logger.InfoContext(ctx, "communications: a card image was fetched", "attachment", media.id)
	cache, referrer, nosniff := publicMediaHeaders()
	return gen.GetCommunicationsPublicMediaByToken200AsteriskResponse{
		Body: content, ContentType: media.contentType, ContentLength: media.size,
		Headers: gen.GetCommunicationsPublicMediaByToken200ResponseHeaders{
			CacheControl: cache, ReferrerPolicy: referrer, XContentTypeOptions: nosniff,
		},
	}, nil
}

// HeadCommunicationsPublicMediaByToken A card image's headers for Meta
// (HEAD /api/v1/communications/public-media/{token})
//
// GET's answer without the body: the same checks and the same bare 404, the
// image's content type and length, and the same headers.
func (s *server) HeadCommunicationsPublicMediaByToken(ctx context.Context, req gen.HeadCommunicationsPublicMediaByTokenRequestObject) (gen.HeadCommunicationsPublicMediaByTokenResponseObject, error) {
	notFound := gen.HeadCommunicationsPublicMediaByToken404Response{}
	media, err := s.publicMediaOf(ctx, req.Token)
	if err != nil || media == nil {
		return notFound, err
	}
	cache, referrer, nosniff := publicMediaHeaders()
	return gen.HeadCommunicationsPublicMediaByToken200AsteriskResponse{
		Body: http.NoBody, ContentType: media.contentType, ContentLength: media.size,
		Headers: gen.HeadCommunicationsPublicMediaByToken200ResponseHeaders{
			CacheControl: cache, ReferrerPolicy: referrer, XContentTypeOptions: nosniff,
		},
	}, nil
}
