// Package communications is the communications module: channels (SMTP
// only), conversations and their messages, the composer, staged
// attachments, delivery and its event log, tags, suppressions, retention
// and the outbox, and the AI draft and customer-suggestion features. Serves
// openapi/communications.yaml under /api/v1/communications/.
package communications

import (
	"net/http"

	"github.com/vantigo-io/vantigo/server/internal/communications/gen"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/ratelimit"
)

// permissions is the module's permission catalog
// (AZ/CommunicationsPermissionCatalog.cs, communications inventory
// "Permissions" in the document preamble), ported verbatim: five keys, all
// sharing the category "Communications", all Delegable=true because the
// .NET contributor never passes that flag (PermissionDescriptor's own
// default), and exactly one — conversations-view — Sensitive=true: its
// description explicitly covers "bodies" and "participants"
// (TS/Authorization/CommunicationsPermissionCatalogTests.cs:22-24). The
// entries are written in the alphabetical-by-key order that test's
// Assert.Equal pins (channels-manage < conversations-manage <
// conversations-reply < conversations-view < suppressions-manage), the same
// stable ordering discipline energy and products already follow.
var permissions = []contracts.Permission{
	{Key: "communications:channels-manage", Display: "Manage communications channels", Description: "Create, update, and verify configured communication channels.", Category: "Communications", Sensitive: false, Delegable: true},
	{Key: "communications:conversations-manage", Display: "Manage communications conversations", Description: "Assign, close, tag, and add internal notes to conversations.", Category: "Communications", Sensitive: false, Delegable: true},
	{Key: "communications:conversations-reply", Display: "Reply to communications conversations", Description: "Create outbound conversation messages and queue them for delivery.", Category: "Communications", Sensitive: false, Delegable: true},
	{Key: "communications:conversations-view", Display: "View communications conversations", Description: "View conversations, messages, participants, bodies, attachments, and tags.", Category: "Communications", Sensitive: true, Delegable: true},
	{Key: "communications:suppressions-manage", Display: "Manage communications suppressions", Description: "View, create, and remove suppressed email addresses.", Category: "Communications", Sensitive: false, Delegable: true},
}

// limits maps each rate-limited operationId to its policy. It is empty and
// stays empty: no Communications endpoint calls RequireRateLimiting
// (communications inventory §1), the same as products and energy.
var limits = map[string]ratelimit.Policy{}

// Module is communications as a platform module: its contract mounted under
// /api/v1/communications/ and its five permissions in the composed catalog.
// Like products and energy, communications publishes no
// contracts.CustomerDirectory of its own — it only ever *reads* one, to
// validate a conversation's customerId — so Directory is left nil.
func Module() module.Module {
	return module.Module{
		Name:        "communications",
		Permissions: permissions,
		Mount:       mount,
	}
}

// mount registers every contract operation on the platform router, which wraps
// each in its access rule and request-body cap before the generated wrapper
// decodes it. It fails when the router reports a problem: an operation never
// registered, a rule that does not parse, or a permission missing from the
// catalog.
func mount(d module.Deps) (http.Handler, error) {
	router := module.NewRouter(module.RouterOptions{
		Doc:     d.Doc,
		Access:  d.Access,
		Limiter: d.Limiter,
		Limits:  limits,
		Catalog: d.Catalog,
	})
	strict := gen.NewStrictHandlerWithOptions(newServer(d), nil, gen.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  module.DecodeError(writeDecodeError),
		ResponseErrorHandlerFunc: module.ResponseError(),
	})
	handler := gen.HandlerWithOptions(strict, gen.StdHTTPServerOptions{
		BaseRouter:       router,
		ErrorHandlerFunc: module.DecodeError(writeDecodeError),
	})
	if err := router.Err(); err != nil {
		return nil, err
	}
	return handler, nil
}
