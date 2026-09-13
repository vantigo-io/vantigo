// Package communications is the communications module: channels (SMTP
// only), conversations and their messages, the composer, staged
// attachments, delivery and its event log, tags, suppressions, retention
// and the outbox, and the AI draft and customer-suggestion features. Serves
// openapi/communications.yaml under /api/v1/communications/.
package communications

import (
	"bytes"
	"context"
	"io"
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
	return withRawPatchBody(handler), nil
}

// rawPatchBodyContextKey is unexported so only withRawPatchBody and
// conversations.go's PATCH handler share it.
type rawPatchBodyContextKey struct{}

// withRawPatchBody captures every PATCH request's raw JSON body into the
// request context before the generated decoder consumes it, then restores
// r.Body so decoding proceeds exactly as it otherwise would.
//
// It exists for one reason: PATCH /conversations/{id} — the only PATCH
// operation this contract declares (communications.yaml has exactly one
// `patch:` block, so gating on method alone can never catch a different
// operation) — must tell "customerId omitted" from "customerId: null"
// apart, and from "assignedUserId omitted" vs "assignedUserId: null".
// encoding/json collapses all three into the same nil *interface{}: its
// documented rule for unmarshaling a JSON null into a pointer field is to
// set that pointer to nil, applied identically whether the key was absent
// or present-and-null, and unmarshaling a top-level null into a non-pointer
// struct (PatchCommunicationsConversationsByIdJSONRequestBody itself) is a
// silent no-op rather than a nil request. .NET tells every one of these
// apart natively (JsonElement.ValueKind: Undefined vs Null, and a JSON null
// body binding a nullable record parameter to an actual C# null). This is
// the smallest way to recover the same distinctions in Go without changing
// the generated contract code or its request-body type.
func withRawPatchBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.Body == nil || r.Body == http.NoBody {
			next.ServeHTTP(w, r)
			return
		}
		// Bounded by the same ceiling module.Router's own MaxBytesReader
		// enforces for this operation (no BodyLimits override in this
		// module's RouterOptions, so every operation uses
		// module.DefaultMaxBodyBytes): a body the router would reject as
		// too large is truncated here too, and the generated decoder still
		// rejects it downstream exactly as it would without this wrapper —
		// this capture never makes an oversized body succeed.
		raw, err := io.ReadAll(io.LimitReader(r.Body, module.DefaultMaxBodyBytes+1))
		_ = r.Body.Close()
		if err != nil {
			r.Body = io.NopCloser(bytes.NewReader(nil))
			next.ServeHTTP(w, r)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), rawPatchBodyContextKey{}, raw)))
	})
}

// rawPatchBodyFrom returns the raw JSON bytes withRawPatchBody captured for
// this request, if any.
func rawPatchBodyFrom(ctx context.Context) ([]byte, bool) {
	b, ok := ctx.Value(rawPatchBodyContextKey{}).([]byte)
	return b, ok
}
