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

// attachmentBodySlack is added to Config.CommunicationsAttachmentMaxBytes to
// get the staging operation's request-body cap: room for the multipart
// framing and the form's other fields (contentId, isInline) on top of the
// file part itself, the same margin identity's avatarBodyLimits (64 KiB)
// adds over its own image bound.
const attachmentBodySlack = 64 * 1024

// bodyLimits is this module's module.RouterOptions.BodyLimits: every
// operation but attachment staging is capped at module.DefaultMaxBodyBytes
// (1 MiB, plenty for this module's JSON bodies), and staging alone is raised
// to the configured attachment limit plus attachmentBodySlack. The router
// puts its http.MaxBytesReader in place before the generated strict
// server's multipart decoder (r.MultipartReader(), called on the raw body)
// ever reads it, so this is the one cap on an upload — see
// readAttachmentForm's comment for how a cap trip during the read
// surfaces as this module's own 413 attachment_too_large rather than a
// generic decode error.
func bodyLimits(d module.Deps) map[string]int64 {
	max := int64(10 * 1024 * 1024)
	if d.Config != nil {
		max = d.Config.CommunicationsAttachmentMaxBytes
	}
	return map[string]int64{
		"postCommunicationsConversationsByIdAttachments": max + attachmentBodySlack,
	}
}

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
//
// BaseRouter is patchBodyMux, not router itself: task 5 fix round 1, item 1
// found that an earlier version of this file wrapped the *returned handler*
// with the PATCH raw-body capture — outside module.Router entirely, so a
// forbidden or rate-limited PATCH still buffered its whole body before
// anyone checked whether the caller could do anything at all, contradicting
// router.go's own ordering guarantee (wrap: rate limit, then Access.Check,
// then the body cap, only then the handler). patchBodyMux instead injects
// the capture at HandleFunc-registration time, so it becomes part of the
// handler router.wrap calls *after* all three — see patchBodyMux's comment.
func mount(d module.Deps) (http.Handler, error) {
	router := module.NewRouter(module.RouterOptions{
		Doc:        d.Doc,
		Access:     d.Access,
		Limiter:    d.Limiter,
		Limits:     limits,
		Catalog:    d.Catalog,
		BodyLimits: bodyLimits(d),
	})
	srv, err := newServer(d)
	if err != nil {
		return nil, err
	}
	strict := gen.NewStrictHandlerWithOptions(srv, nil, gen.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  module.DecodeError(writeDecodeError),
		ResponseErrorHandlerFunc: module.ResponseError(),
	})
	handler := gen.HandlerWithOptions(strict, gen.StdHTTPServerOptions{
		BaseRouter:       patchBodyMux{router},
		ErrorHandlerFunc: module.DecodeError(writeDecodeError),
	})
	if err := router.Err(); err != nil {
		return nil, err
	}
	return handler, nil
}

// patchBodyPattern is the exact "METHOD /path" gen.HandlerWithOptions
// registers PATCH /conversations/{id} under (gen/api.gen.go's own
// `m.HandleFunc(http.MethodPatch+" "+options.BaseURL+"/api/v1/communications/conversations/{id}", ...)`
// call) — the only pattern patchBodyMux wraps. A future second PATCH
// operation would need its own deliberate decision, not silently inherit
// this one by matching on method alone.
const patchBodyPattern = "PATCH /api/v1/communications/conversations/{id}"

// patchBodyMux is module.Router wrapped so PATCH /conversations/{id}'s raw
// JSON body is captured *inside* module.Router's own per-route wrap chain
// (router.go's wrap: rate limit -> Access.Check -> the operation's
// MaxBytesReader cap -> the handler), never before it.
// gen.HandlerWithOptions builds every operation's final handler by calling
// BaseRouter.HandleFunc(pattern, h) once per operation, and Router.HandleFunc
// itself wraps whatever h it is given with router.wrap before registering
// it (router.go's HandleFunc: `handler: r.wrap(op, rule, ..., h)`) — so
// intercepting h here, before it reaches Router.HandleFunc, means the
// capture ends up *inside* r.wrap's returned closure, running only after
// wrap's own rate-limit/Access.Check/MaxBytesReader steps have already
// passed. A forbidden or rate-limited PATCH is rejected by those steps
// before withRawPatchBody ever runs, and the read it does perform is
// bounded by the MaxBytesReader this same request already carries — this
// operation's real limit (a BodyLimits override included), not a value
// duplicated from module.DefaultMaxBodyBytes.
type patchBodyMux struct {
	*module.Router
}

func (m patchBodyMux) HandleFunc(pattern string, h func(http.ResponseWriter, *http.Request)) {
	if pattern == patchBodyPattern {
		h = withRawPatchBody(h)
	}
	m.Router.HandleFunc(pattern, h)
}

// rawPatchBodyContextKey is unexported so only withRawPatchBody and
// conversations_patch.go's PATCH handler share it.
type rawPatchBodyContextKey struct{}

// withRawPatchBody captures PATCH /conversations/{id}'s raw JSON body into
// the request context before the generated decoder consumes it, then
// restores r.Body so decoding proceeds exactly as it otherwise would.
//
// It exists for one reason: this handler must tell "customerId omitted"
// from "customerId: null" apart, and "assignedUserId omitted" from
// "assignedUserId: null". encoding/json collapses all three into the same
// nil *interface{}: its documented rule for unmarshaling a JSON null into a
// pointer field is to set that pointer to nil, applied identically whether
// the key was absent or present-and-null, and unmarshaling a top-level null
// into a non-pointer struct (PatchCommunicationsConversationsByIdJSONRequestBody
// itself) is a silent no-op rather than a nil request. .NET tells every one
// of these apart natively (JsonElement.ValueKind: Undefined vs Null, and a
// JSON null body binding a nullable record parameter to an actual C# null).
// This is the smallest way to recover the same distinctions in Go without
// changing the generated contract code or its request-body type.
//
// next is called by patchBodyMux only after module.Router's wrap has
// already run rate limiting, Access.Check and applied this operation's
// MaxBytesReader — see patchBodyMux's own comment for why that ordering
// matters and how it is achieved.
func withRawPatchBody(next func(http.ResponseWriter, *http.Request)) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Body == nil || r.Body == http.NoBody {
			next(w, r)
			return
		}
		raw, err := io.ReadAll(r.Body)
		_ = r.Body.Close()
		if err != nil {
			// task 5 fix round 1, item 5: a read failure — including the
			// MaxBytesReader's own *http.MaxBytesError for a body over
			// this operation's limit — is not "the caller sent nothing".
			// Treating it as an empty body would let an oversized or
			// truncated request masquerade as PATCH's null-body 400
			// instead of the decode-error response the router gives an
			// unreadable body everywhere else in this module.
			writeDecodeError(w, r)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
		next(w, r.WithContext(context.WithValue(r.Context(), rawPatchBodyContextKey{}, raw)))
	}
}

// rawPatchBodyFrom returns the raw JSON bytes withRawPatchBody captured for
// this request, if any.
func rawPatchBodyFrom(ctx context.Context) ([]byte, bool) {
	b, ok := ctx.Value(rawPatchBodyContextKey{}).([]byte)
	return b, ok
}
