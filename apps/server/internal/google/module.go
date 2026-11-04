// Package google is the google module: the connection to a Google Workspace
// organisation, serving openapi/google.yaml under /api/v1/google/ (Google
// Workspace design G12–G16, docs/superpowers/specs/2026-11-03-google-workspace-design.md;
// the plan, docs/superpowers/plans/2026-11-03-google-foundation.md).
//
// An Owner uploads the service account's JSON key, names the admin the
// Admin SDK calls impersonate, enters the Internal web client and tests the
// connection: Vantigo asks Google's token endpoint for a domain-wide
// delegation token per scope it needs, and per scope it never uses, then
// reads the customer and its verified domains. google:manage reads the
// connection and the events. Delivery A fills no slot: the accounts
// (contracts.IdentityProvisioner), the person's connection, the calendar and
// Gmail come in the deliveries after it, each through tokenSource, the one
// seam every call to Google takes its token from. MODULES refuses google
// without staff, whose accounts worker will be the provisioner's caller.
// Nothing here imports another module — depguard enforces it, tests
// included — and no SQL of this module crosses a schema.
//
// module.go is the module and its permission, server.go the operations'
// receiver, errors.go the refusals, client.go the guarded client and the
// hosts it may reach, token.go the delegated tokens and their cache,
// credentials.go the keys and the web client's secret, connection.go the
// connection's read and writes, test.go Test connection, events.go the
// trail, and googletest the fake Google its tests run against.
package google

import (
	"net/http"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/google/gen"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/ratelimit"
)

// permissions is the module's permission catalog (Google Workspace design
// G16): one key, google:manage — the connection's status, accounts and
// events, and the account rules — Entra's entra:manage twin. It is sensitive
// and not delegable. The connection itself — the key, the admin, the web
// client and the test — is not under it but under the Owner policy, as
// identity's single sign-on settings are.
var permissions = []contracts.Permission{
	{
		Key: "google:manage", Display: "Manage Google Workspace",
		Description: "See the Google Workspace connection's status, accounts and events, and set the account rules.",
		Category:    "Administration", Sensitive: true, Delegable: false,
	},
}

// ownerLimit is the one policy of the module's Owner operations — the
// admin, the keys, the web client and the test — with identity's
// UserManagement figures: 30 per client address a minute, one bucket
// between them. Each writes an organisation-wide credential or reaches
// Google on a click.
var ownerLimit = ratelimit.Policy{Name: "google-owner", Limit: 30, Window: time.Minute}

// limits maps each rate-limited operationId to its policy; the router hits it
// per client address before access is checked, and a bucket is the policy's
// Name and the address, so the Owner operations share one.
var limits = map[string]ratelimit.Policy{
	"putGoogleConnection":                   ownerLimit,
	"postGoogleConnectionCredentials":       ownerLimit,
	"deleteGoogleConnectionCredentialsById": ownerLimit,
	"putGoogleConnectionOauthClient":        ownerLimit,
	"postGoogleConnectionTest":              ownerLimit,
}

// Module is google as a platform module: its contract mounted under
// /api/v1/google/ and its one permission in the composed catalog. It fills
// no slot yet.
func Module() module.Module {
	return module.Module{
		Name:        "google",
		Permissions: permissions,
		Mount:       mount,
	}
}

// mount registers every contract operation on the platform router, which
// wraps each in its access rule, its rate limit and its request-body cap
// before the generated wrapper decodes it. It fails when the router reports
// a problem — an operation never registered, a rule that does not parse, or
// a permission missing from the catalog.
func mount(d module.Deps) (http.Handler, error) {
	router := module.NewRouter(module.RouterOptions{
		Doc:     d.Doc,
		Access:  d.Access,
		Limiter: d.Limiter,
		Limits:  limits,
		Catalog: d.Catalog,
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
