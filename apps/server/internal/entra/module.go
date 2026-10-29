// Package entra is the entra module: Microsoft Entra ID as the first provider
// of contracts.IdentityProvisioner, serving openapi/entra.yaml under
// /api/v1/entra/ (docs/superpowers/specs/2026-10-19-entra-design.md, D11–D15;
// the plan, docs/superpowers/plans/2026-10-20-entra-module.md).
//
// An Owner registers the tenant and the application, adds its client secrets
// and tests the connection; entra:manage sets the account rules; and the
// module's provisioner executes Staff's decisions against the tenant through
// Microsoft Graph. Staff's accounts worker is the provisioner's only caller,
// which is why MODULES refuses entra without staff. The module depends on
// nobody but identity, and reads of identity only what
// contracts.SSOConnections answers an Owner — the users left to connect and
// who holds a directory key — for the directory's exact matches (phase 2,
// delivery B); the Owner's and entra:manage's checks are the router's. It
// also fills contracts.MailboxCalendar for the calendar's Outlook sync
// (docs/superpowers/specs/2026-10-27-calendar-outlook-sync-design.md, O2–O4):
// behind the Owner's Outlook connection switch and Calendars.ReadWrite, it
// writes the calendar's copies to the mailboxes of the accounts it manages,
// reads them back and, on an installation Microsoft can reach, keeps their
// changes notified through Graph's subscriptions and an anonymous webhook
// (O11). It writes nothing outside its own schema; nothing here imports another module —
// depguard enforces it, tests included — and no SQL of this module crosses a
// schema.
//
// module.go is the module and its permission, server.go the operations'
// receiver, errors.go the refusals, provisioner.go the
// contracts.IdentityProvisioner, mailbox.go the contracts.MailboxCalendar —
// its reads in mailbox_delta.go and mailbox_writeback.go, its change
// notifications' subscriptions in mailbox_notifications.go and their webhook
// in mailbox_notifications_endpoint.go — and graphtest the fake Graph its
// tests run against.
package entra

import (
	"net/http"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/entra/gen"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/ratelimit"
)

// permissions is the module's permission catalog (Entra design D11): one key,
// entra:manage — the connection's status and events, the account rules and
// the pass settings. It is sensitive and not delegable: what it sets decides
// what happens to every account in the tenant. The connection itself — the
// tenant, the client, the secrets and the test — is not under it but under
// the Owner policy, as identity's single sign-on settings are.
var permissions = []contracts.Permission{
	{
		Key: "entra:manage", Display: "Manage Microsoft Entra ID",
		Description: "See the Microsoft Entra ID connection's status, accounts and events, and set the account rules: the address pattern, the usage location, what a leaver loses and the Temporary Access Pass.",
		Category:    "Administration", Sensitive: true, Delegable: false,
	},
}

// ownerLimit is the one policy of the module's Owner operations — the
// registration, the secrets and the test — with identity's UserManagement
// figures: 30 per client address a minute, one bucket between them. Each
// writes a tenant-wide credential or reaches the tenant on a click; nobody
// registering their tenant clicks thirty times a minute, while a script
// guessing secrets would.
var ownerLimit = ratelimit.Policy{Name: "entra-owner", Limit: 30, Window: time.Minute}

// skusLimit is the tenant's licences' read, entra:manage's: the same figures
// in a bucket of its own. It reaches the tenant on every request, but the
// wizard's licence list must not spend the Owner's budget for registering
// the tenant, nor the Owner's clicks the list's.
var skusLimit = ratelimit.Policy{Name: "entra-skus", Limit: 30, Window: time.Minute}

// directoryLimit is the directory search's, the picker's: 60 per client
// address a minute, a bucket of its own. Every request reaches the tenant,
// and a debounced picker asks once per pause in typing; a script walking
// the tenant through it would ask far more.
var directoryLimit = ratelimit.Policy{Name: "entra-directory", Limit: 60, Window: time.Minute}

// matchesLimit is the exact matches' read: 6 per client address a minute,
// a bucket of its own. One read asks the tenant for every user not yet
// connected, in chunks — a review an Owner opens, not a list they refresh.
var matchesLimit = ratelimit.Policy{Name: "entra-matches", Limit: 6, Window: time.Minute}

// notificationsLimit is Microsoft Graph's webhook's, anonymous (calendar
// Outlook sync design O11.5): 600 per client address a minute, a bucket of
// its own. Graph posts a tenant's notifications from a handful of
// addresses, batched, and retries a 429 for up to four hours, so a burst
// of changes is delayed rather than lost; a stranger posting at the
// webhook is held to the same figure, and each request costs the database
// at most a few indexed statements.
var notificationsLimit = ratelimit.Policy{Name: "entra-mailbox-notifications", Limit: 600, Window: time.Minute}

// outlookLimit is the person's connect and complete (Outlook connection
// design M4): 30 per client address a minute, one bucket between them. Each
// mints or spends a sealed cookie; nobody connects their Outlook thirty
// times a minute, while a script minting cookies would.
var outlookLimit = ratelimit.Policy{Name: "entra-outlook", Limit: 30, Window: time.Minute}

// outlookCallbackLimit is the connect's redirect URI's, anonymous (M4): 60
// per client address a minute, a bucket of its own. Microsoft sends a
// browser back once per connect; a stranger replaying the callback is held
// to the same figure, and each request opens a cookie before it reads a
// row.
var outlookCallbackLimit = ratelimit.Policy{Name: "entra-outlook-callback", Limit: 60, Window: time.Minute}

// limits maps each rate-limited operationId to its policy; the router hits it
// per client address before access is checked, and a bucket is the policy's
// Name and the address, so the four Owner operations share one.
var limits = map[string]ratelimit.Policy{
	"putEntraConnection":                   ownerLimit,
	"postEntraConnectionCredentials":       ownerLimit,
	"deleteEntraConnectionCredentialsById": ownerLimit,
	"postEntraConnectionTest":              ownerLimit,
	"getEntraConnectionSkus":               skusLimit,
	"getEntraDirectoryUsers":               directoryLimit,
	"getEntraDirectoryMatches":             matchesLimit,
	"postEntraMailboxNotifications":        notificationsLimit,
	"postEntraOutlookConnect":              outlookLimit,
	"postEntraOutlookComplete":             outlookLimit,
	"getEntraOutlookCallback":              outlookCallbackLimit,
}

// Module is entra as a platform module: its contract mounted under
// /api/v1/entra/, its one permission in the composed catalog, its
// contracts.IdentityProvisioner, which Compose and Workers collect into
// Deps.IdentityProvisioners for Staff, and its contracts.MailboxCalendar,
// which they resolve into Deps.MailboxCalendar for the calendar. It has no
// worker of its own.
func Module() module.Module {
	return module.Module{
		Name:                "entra",
		Permissions:         permissions,
		Mount:               mount,
		IdentityProvisioner: newProvisioner,
		MailboxCalendar:     newMailboxCalendar,
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
