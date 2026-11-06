package meta

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/meta/gen"
	"github.com/vantigo-io/vantigo/server/internal/meta/store"
)

// maxWebhookBody is the most of a post's body read (Messenger design N11).
const maxWebhookBody int64 = 1 << 20

// The webhook's figures.
const (
	// webhookDBBudget bounds the database work of one post: Meta wants an
	// answer within five seconds, and a slow database is a 503 it retries.
	webhookDBBudget = 2 * time.Second
	// touchEvery is the least time between two writes of the status line's
	// "last webhook" and "last rejected" marks.
	touchEvery = 30 * time.Second
	// signaturePrefix is how Meta writes X-Hub-Signature-256.
	signaturePrefix = "sha256="
)

// GetMetaWebhook answers Meta's handshake: hub.challenge when hub.mode is
// subscribe and hub.verify_token is the stored token, else 403. Both tokens are
// hashed before they are compared, so the comparison is in constant time
// whatever their lengths. (GET /api/v1/meta/webhook)
func (s *server) GetMetaWebhook(ctx context.Context, req gen.GetMetaWebhookRequestObject) (gen.GetMetaWebhookResponseObject, error) {
	settings, err := s.settings(ctx)
	if err != nil {
		return nil, err
	}
	given := sha256.Sum256([]byte(hubParam(req.Params.HubVerifyToken)))
	want := sha256.Sum256([]byte(settings.VerifyToken))
	tokenOK := subtle.ConstantTimeCompare(given[:], want[:]) == 1
	modeOK := hubParam(req.Params.HubMode) == "subscribe"
	if !tokenOK || !modeOK || settings.VerifyToken == "" {
		return gen.GetMetaWebhook403Response{}, nil
	}
	return gen.GetMetaWebhook200TextResponse(hubParam(req.Params.HubChallenge)), nil
}

// PostMetaWebhook stores Meta's event. The contract declares no request body,
// so the raw bytes are read from the request itself: the signature is over the
// bytes as Meta sent them, and a body decoded and encoded again is other
// bytes. Nothing is parsed for routing before the answer, and nothing before
// the signature held. (POST /api/v1/meta/webhook)
func (s *server) PostMetaWebhook(ctx context.Context, _ gen.PostMetaWebhookRequestObject) (gen.PostMetaWebhookResponseObject, error) {
	r, ok := contracts.RequestFrom(ctx)
	if !ok {
		return nil, errors.New("meta: the webhook has no request behind it")
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return gen.PostMetaWebhook413Response{}, nil
		}
		return gen.PostMetaWebhook400Response{}, nil
	}

	dbCtx, cancel := context.WithTimeout(ctx, webhookDBBudget)
	defer cancel()
	settings, err := s.q.GetSettings(dbCtx)
	if err != nil {
		s.logger.WarnContext(ctx, "meta: the webhook could not read the settings", "err", err)
		return gen.PostMetaWebhook503Response{}, nil
	}
	secret, stored, err := s.appSecret(settings)
	if err != nil {
		s.logger.WarnContext(ctx, "meta: the webhook could not open the app secret", "err", err)
		return gen.PostMetaWebhook503Response{}, nil
	}
	if !stored {
		return gen.PostMetaWebhook503Response{}, nil
	}

	if !validSignature(secret, r.Header.Get("X-Hub-Signature-256"), body) {
		// Neither the body, the signature nor the secret is logged.
		s.logger.WarnContext(ctx, "meta: a webhook post was refused: its signature did not hold")
		now := s.now()
		olderThan := now.Add(-touchEvery)
		if err := s.q.TouchRejected(dbCtx, store.TouchRejectedParams{Now: &now, OlderThan: &olderThan}); err != nil {
			s.logger.WarnContext(ctx, "meta: the refused post could not be noted", "err", err)
		}
		return gen.PostMetaWebhook401Response{}, nil
	}
	if !json.Valid(body) {
		return gen.PostMetaWebhook400Response{}, nil
	}

	if _, err := s.q.InsertWebhookEvent(dbCtx, store.InsertWebhookEventParams{Now: s.now(), Payload: body}); err != nil {
		s.logger.WarnContext(ctx, "meta: a webhook event could not be stored", "err", err)
		return gen.PostMetaWebhook503Response{}, nil
	}
	now := s.now()
	olderThan := now.Add(-touchEvery)
	if err := s.q.TouchWebhook(dbCtx, store.TouchWebhookParams{Now: &now, OlderThan: &olderThan}); err != nil {
		s.logger.WarnContext(ctx, "meta: the last webhook time could not be noted", "err", err)
	}
	return gen.PostMetaWebhook200Response{}, nil
}

// validSignature reports whether header is "sha256=" and the hex HMAC-SHA256
// of body under secret. The digests are compared in constant time.
func validSignature(secret []byte, header string, body []byte) bool {
	hexSum, found := strings.CutPrefix(header, signaturePrefix)
	if !found {
		return false
	}
	got, err := hex.DecodeString(hexSum)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return hmac.Equal(got, mac.Sum(nil))
}

// hubParam is *p, or "" for nil.
func hubParam(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
