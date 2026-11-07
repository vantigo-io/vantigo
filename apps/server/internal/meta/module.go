// Package meta is the meta module: the connection to Meta that fills
// contracts.ChannelMessenger under the key meta, Facebook Messenger through a
// Facebook Page the business owns, serving openapi/meta.yaml under
// /api/v1/meta/ (Messenger design N9–N12a,
// docs/superpowers/specs/2026-11-05-communications-messenger-design.md; the
// plan, docs/superpowers/plans/2026-11-05-communications-messenger-plan.md,
// delivery B).
//
// An Owner enters the Meta app's id and secret, pastes the webhook's address
// and verify token into Meta's developer portal and adds each Page by its
// access token; a Page's channel in Communications then sends through, and
// reads from, this module. Meta posts its events to the anonymous webhook, which
// checks the signature over the raw bytes and stores the body; the module's
// splitting worker turns each stored event into rows of meta.inbound, fetches
// the files a person sent from Meta's CDN at once, and Communications reads
// the rows from a cursor and acknowledges them. Nothing here imports another
// module — depguard enforces it, tests included — and no SQL of this module
// crosses a schema. MODULES refuses meta without communications.
//
// module.go is the module and its rate limits, server.go the operations'
// receiver, errors.go the refusals, core.go what the operations, the provider
// and the workers share, transport.go the guarded outbound HTTP; messenger.go
// the contracts.ChannelMessenger and settings.go its status, the capabilities
// and the settings' operations; webhook.go the handshake and the post,
// inbound.go what Communications reads (Notified, Pending, OpenAttachment,
// Ack), inbound_worker.go the splitting worker and retention.go retention;
// graph.go the Graph client, send.go Send and the error map, pages.go the
// Pages and profile.go the participant's name.
package meta

import (
	"net/http"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/meta/gen"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/ratelimit"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

// ownerLimit is the one policy of the module's Owner operations — the
// settings, the verify token and the Pages — with Google's figures: 30 per
// client address a minute, one bucket between them.
var ownerLimit = ratelimit.Policy{Name: "meta-owner", Limit: 30, Window: time.Minute}

// webhookLimit is the anonymous webhook's POST: 1200 per client address a
// minute, a bucket of its own, so Meta's events never spend an Owner's. A busy
// Page posts several events a second.
var webhookLimit = ratelimit.Policy{Name: "meta-webhook", Limit: 1200, Window: time.Minute}

// webhookVerifyLimit is the handshake's: 60 per client address a minute, its
// own bucket, since a guess at the verify token is what it is for.
var webhookVerifyLimit = ratelimit.Policy{Name: "meta-webhook-verify", Limit: 60, Window: time.Minute}

// limits maps each rate-limited operationId to its policy; the router hits it
// per client address before access is checked.
var limits = map[string]ratelimit.Policy{
	"getMetaSettings":                  ownerLimit,
	"putMetaSettings":                  ownerLimit,
	"postMetaSettingsVerifyToken":      ownerLimit,
	"postMetaPages":                    ownerLimit,
	"deleteMetaPagesByPageId":          ownerLimit,
	"postMetaPagesByPageIdResubscribe": ownerLimit,
	"getMetaWebhook":                   webhookVerifyLimit,
	"postMetaWebhook":                  webhookLimit,
}

// bodyLimits gives the webhook's POST the cap the spec names, 1 MiB, in so
// many words (it is the router's default too): a body above it is read as far
// as the cap and answered 413.
var bodyLimits = map[string]int64{"postMetaWebhook": maxWebhookBody}

// Module is meta as a platform module: its contract mounted under
// /api/v1/meta/, no permission of its own (the Owner policy gates every
// operation but the two anonymous webhook ones), its
// contracts.ChannelMessenger (messenger.go), which Compose and Workers collect
// into Deps.ChannelMessengers for Communications' channels, and its workers,
// meta-split and meta-retention.
func Module() module.Module {
	return module.Module{
		Name:             "meta",
		Mount:            mount,
		ChannelMessenger: newMessenger,
		Workers:          workers,
	}
}

// workers is the module's background work.
func workers(d module.Deps) []worker.Worker {
	return []worker.Worker{NewSplitWorker(d), NewRetentionWorker(d)}
}

// mount registers every contract operation on the platform router, which
// wraps each in its access rule, its rate limit and its request-body cap
// before the generated wrapper decodes it.
func mount(d module.Deps) (http.Handler, error) {
	router := module.NewRouter(module.RouterOptions{
		Doc:        d.Doc,
		Access:     d.Access,
		Limiter:    d.Limiter,
		Limits:     limits,
		BodyLimits: bodyLimits,
		Catalog:    d.Catalog,
	})
	strict := gen.NewStrictHandlerWithOptions(newServer(d), nil, gen.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  module.DecodeError(apicommon.WriteDecodeError),
		ResponseErrorHandlerFunc: module.ResponseError(),
	})
	handler := gen.HandlerWithOptions(strict, gen.StdHTTPServerOptions{
		BaseRouter:       router,
		ErrorHandlerFunc: module.DecodeError(apicommon.WriteDecodeError),
	})
	if err := router.Err(); err != nil {
		return nil, err
	}
	return handler, nil
}
