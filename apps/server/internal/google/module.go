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
// connection and the events, and sets the account rules. Delivery B
// (G17–G22, docs/superpowers/plans/2026-11-03-google-accounts.md) fills
// contracts.IdentityProvisioner under the key google: Staff's accounts
// worker prepares, activates, updates, suspends and deletes a person's
// Google account, keeps Staff's groups as Google groups with their
// licences, and pushes the photo; the module runs no worker of its own.
// Delivery C (G23–G24, docs/superpowers/plans/2026-11-03-google-link-users.md)
// fills contracts.DirectorySearch under the same key: identity's Owner
// endpoints search the organisation's users and propose exact matches for
// connecting existing users to Google sign-in. Delivery D (G25–G29,
// docs/superpowers/plans/2026-11-03-google-connection.md) adds the person's
// own Google connection: behind the Owner's Connection for people switch, a
// person whose Staff record names an account the module manages connects it
// through the organisation's Internal web client, and the module keeps the
// refresh token sealed; google-sweep, the module's one worker, deletes the
// spent connections and revokes their tokens. Delivery E (G30–G32,
// docs/superpowers/plans/2026-11-03-google-calendar.md) fills
// contracts.MailboxCalendar under the key google: the calendar's copies
// written to a person's Google Calendar, their busy time and their edits read
// back, all on their own connection, and a push channel per person whose
// messages the anonymous calendar notifications endpoint records. Delivery F
// (G33–G34, docs/superpowers/plans/2026-11-03-google-mail-channels.md) fills
// contracts.ChannelMailbox under the key google: behind the Owner's Mail
// channels switch, Communications' channels send from, read and label the
// organisation's licensed Gmail mailboxes through domain-wide delegation,
// polled, never pushed. Sending from a person's own Gmail comes in the
// delivery after it. Every call to Google takes its token from
// tokenSource. MODULES refuses
// google without staff. Nothing here imports another module — depguard
// enforces it, tests included — and no SQL of this module crosses a schema.
//
// module.go is the module and its permission, server.go the operations'
// receiver, errors.go the refusals, client.go the guarded client and the
// hosts it may reach, token.go the delegated tokens and their cache,
// credentials.go the keys and the web client's secret, connection.go the
// connection's read and writes, test.go Test connection, events.go the
// trail; provisioner.go the provisioner and its status, directory.go the
// user as the Directory has it, lifecycle_*.go the account's lifecycle,
// licences.go the licences, groups.go and groups_traits.go the groups,
// photo.go the photo, accounts.go the Accounts tab's operations;
// directory_search.go the directory search identity serves; personal*.go
// the person's own connection (personal_token.go its token,
// personal_sweep.go google-sweep); mailbox*.go the mailbox calendar
// (mailbox_batch.go the batch endpoint, mailbox_delta.go the delta,
// mailbox_writeback.go the write-back's reads, mailbox_notifications*.go the
// push channels and their endpoint); mail_channels.go the Mail channels
// switch, channel_mailbox*.go the channel mailboxes (channel_mailbox_inbound.go
// the delta and the raw read, channel_mailbox_marks.go the labels); and
// googletest the fake Google its tests run against.
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
	"getGoogleConnectionSkus":               skusLimit,
	"putGoogleConnectionPersonal":           ownerLimit,
	"putGoogleConnectionMailChannels":       ownerLimit,
	"postGoogleConnect":                     connectLimit,
	"postGoogleComplete":                    connectLimit,
	"getGoogleConnectCallback":              callbackLimit,
	"postGoogleCalendarNotifications":       notificationsLimit,
}

// connectLimit is the person's connect and complete, one bucket between
// them (G26, Entra's figures): 30 per client address a minute.
var connectLimit = ratelimit.Policy{Name: "google-connect", Limit: 30, Window: time.Minute}

// callbackLimit is the anonymous callback's: 60 per client address a minute.
var callbackLimit = ratelimit.Policy{Name: "google-connect-callback", Limit: 60, Window: time.Minute}

// notificationsLimit is the anonymous calendar notifications endpoint's
// (G32, Entra's webhook's figure): 600 per client address a minute, a bucket
// of its own, so Google's messages never spend a person's.
var notificationsLimit = ratelimit.Policy{Name: "google-calendar-notifications", Limit: 600, Window: time.Minute}

// skusLimit is GET /connection/skus's policy, a bucket of its own: each
// read reaches Google once per product Vantigo knows.
var skusLimit = ratelimit.Policy{Name: "google-skus", Limit: 30, Window: time.Minute}

// Module is google as a platform module: its contract mounted under
// /api/v1/google/, its one permission in the composed catalog, and its
// contracts.IdentityProvisioner (provisioner.go), which Compose and Workers
// collect into Deps.IdentityProvisioners for Staff's accounts worker, and its
// contracts.DirectorySearch (directory_search.go), which Compose collects
// into Deps.DirectorySearches for identity's directory endpoints; its
// contracts.MailboxCalendar (mailbox.go), which Compose and Workers collect
// into Deps.MailboxCalendars for the calendar; its contracts.ChannelMailbox
// (channel_mailbox.go), which they collect into Deps.ChannelMailboxes for
// Communications' channels; its contracts.PersonalMailbox
// (personal_mailbox.go), which they collect into Deps.PersonalMailboxes for
// sending from a person's own Gmail address; and its one worker, google-sweep
// (personal_sweep.go).
func Module() module.Module {
	return module.Module{
		Name:                "google",
		Permissions:         permissions,
		Mount:               mount,
		IdentityProvisioner: newProvisioner,
		DirectorySearch:     newDirectorySearch,
		MailboxCalendar:     newMailboxCalendar,
		ChannelMailbox:      newChannelMailbox,
		PersonalMailbox:     newPersonalMailbox,
		Workers:             workers,
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
