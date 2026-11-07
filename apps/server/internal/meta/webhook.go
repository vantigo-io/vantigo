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
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/httpx"
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

// PostMetaWebhook stores Meta's event. The contract declares the body as raw
// octet-stream, so the strict wrapper hands over r.Body undecoded whatever
// Content-Type Meta sent (it sends application/json): the signature is over
// the bytes as Meta sent them, and a body decoded and encoded again is other
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
		s.warnRefused(ctx, httpx.ClientIP(r))
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
		if !unstorableJSON(err) {
			s.logger.WarnContext(ctx, "meta: a webhook event could not be stored", "err", err)
			return gen.PostMetaWebhook503Response{}, nil
		}
		// Valid JSON that Postgres refuses as jsonb (an escaped NUL): Meta would
		// retry it for ever on a 503, so the bytes are kept as they are, in a row
		// that is born failed. The body is not logged.
		s.logger.WarnContext(ctx, "meta: a webhook body that jsonb cannot hold was kept as bytes", "bytes", len(body))
		if err := s.q.InsertRejectedWebhookEvent(dbCtx, store.InsertRejectedWebhookEventParams{Now: s.now(), Raw: body}); err != nil {
			s.logger.WarnContext(ctx, "meta: a webhook event could not be stored", "err", err)
			return gen.PostMetaWebhook503Response{}, nil
		}
	}
	now := s.now()
	olderThan := now.Add(-touchEvery)
	if err := s.q.TouchWebhook(dbCtx, store.TouchWebhookParams{Now: &now, OlderThan: &olderThan}); err != nil {
		s.logger.WarnContext(ctx, "meta: the last webhook time could not be noted", "err", err)
	}
	return gen.PostMetaWebhook200Response{}, nil
}

// unstorableJSON is whether err is Postgres refusing the text as jsonb:
// untranslatable character (22P05) or invalid byte sequence (22021).
func unstorableJSON(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && (pgErr.Code == "22P05" || pgErr.Code == "22021")
}

// refusedLog rate-limits the warning for a refused signature: a bad actor
// posting in a loop must not fill the log. One warning per client address per
// minute; the ones held back are counted and the count goes in the next.
type refusedLog struct {
	mu      sync.Mutex
	clients map[string]*refusedClient
}

type refusedClient struct {
	last       time.Time
	suppressed int
}

const (
	refusedLogEvery = time.Minute
	// refusedLogMax bounds the map; an address idle for a minute is dropped, and
	// at the bound all of them are, which only lets a warning through early.
	refusedLogMax = 4096
)

// allow reports whether a warning for client may be written now, and how many
// were held back since the last one.
func (l *refusedLog) allow(client string, now time.Time) (bool, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.clients == nil {
		l.clients = map[string]*refusedClient{}
	}
	c, ok := l.clients[client]
	if ok && now.Sub(c.last) < refusedLogEvery {
		c.suppressed++
		return false, 0
	}
	if !ok {
		if len(l.clients) >= refusedLogMax {
			for k, v := range l.clients {
				if now.Sub(v.last) >= refusedLogEvery {
					delete(l.clients, k)
				}
			}
			if len(l.clients) >= refusedLogMax {
				clear(l.clients)
			}
		}
		c = &refusedClient{}
		l.clients[client] = c
	}
	held := c.suppressed
	c.last, c.suppressed = now, 0
	return true, held
}

// warnRefused logs a refused signature, at most once a minute per client.
func (s *server) warnRefused(ctx context.Context, client string) {
	if ok, held := s.refused.allow(client, s.now()); ok {
		s.logger.WarnContext(ctx, "meta: a webhook post was refused: its signature did not hold", "suppressed", held)
	}
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
