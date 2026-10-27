// Package calendar is the calendar module: one view of every time-bound thing
// the other modules know about one person, beside the appointments they
// schedule themselves, serving openapi/calendar.yaml under /api/v1/calendar/
// (docs/superpowers/specs/2026-10-19-calendar-design.md).
//
// It depends on nobody but identity. What it shows from the other modules it
// never copies: each enabled module projects its own rows at read time through
// contracts.CalendarSource (module.Deps.CalendarSources), under its own
// permission gate. The one thing the module owns is the appointment. Nothing
// here imports another module — depguard enforces it, tests included — and no
// SQL of this module crosses a schema.
//
// Each area is a file of its own: meta.go, items.go (the window and the
// sources), appointments.go, occurrences.go, responses.go, colleagues.go and
// users.go, with recurrence.go for the rule, authorize.go for who the caller
// is, contractscalls.go for every call out of the module, values.go for the
// bounds and errors.go for the refusals. The invitations (calendar
// invitations design) are attendees.go (the three kinds), ics.go (the
// iCalendar object), invitations.go (the mail and the resend), notices.go
// (the colleagues' notices), words.go (both languages), contacts.go (the
// picker), rsvp.go (the answer by link), cancellation.go (a delete that
// becomes a cancellation) and cancellations_worker.go (its sweeper). The
// feed (calendar feed design) is feed_tokens.go (the addresses), feed.go
// (the anonymous GET) and feed_ics.go (its iCalendar object); the
// subscriptions are subscriptions.go (the rows), subscriptions_fetch.go (the
// guarded fetch), subscriptions_parse.go and windowszones.go (parsed on read)
// and subscriptions_worker.go (the poll). The Outlook sync (calendar Outlook
// sync design) is mailbox_sync.go (the person's setting and the nudge),
// mailbox_push.go (what is pushed, and how it looks) and mailbox_worker.go
// (the calendar-mailbox-sync worker and the clean-up).
package calendar

import (
	"net/http"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/calendar/gen"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/ratelimit"
)

// permissions is the module's permission catalog (calendar design D3). Every
// operation requires calendar:access; a colleague's calendar requires
// calendar:view-colleagues beside it — the one thing not everyone should have
// by default. Neither is sensitive, and both are delegable, as every other
// module's are. There is no manage key: the module has no settings.
var permissions = []contracts.Permission{
	{
		Key: "calendar:access", Display: "Use Calendar",
		Description: "Use the Calendar app: your own calendar, your appointments and your answers to them, and the dashboard's agenda.",
		Category:    "Calendar", Sensitive: false, Delegable: true,
	},
	{
		Key: "calendar:view-colleagues", Display: "See colleagues' calendars",
		Description: "See a colleague's appointments as busy or free, with the title when it is shared.",
		Category:    "Calendar", Sensitive: false, Delegable: true,
	},
}

// limits maps each rate-limited operationId to its policy; the router hits
// it per client address before access is checked, and a bucket is the
// policy's Name and the address, so operations that share a Name share a
// bucket. The module's three anonymous operations, each a bucket of its own:
// the RSVP page's read 60 and answer 30 per client per 10 minutes (calendar
// invitations design D5), and the feed 120 (calendar feed design D2) — a
// phone asks hourly, and an office of phones behind one NAT is dozens of
// requests an hour. And the three that fetch a stranger's address inline —
// a subscription's POST, its PUT and Refresh now — one bucket between them,
// calendar-subscription-fetch, 30 per client per 10 minutes: each is an
// outbound request of up to twenty seconds made on a click, and nobody
// saving their own calendars clicks thirty times in ten minutes, while a
// script turning the server into a fetcher would. The Outlook sync's PUT,
// calendar-mailbox-sync-write, 10 per client per 10 minutes, and its Sync
// now, calendar-mailbox-run, 6 a minute: each makes the person's sync due,
// and the worker syncs one person at a time (calendar Outlook sync design
// O7), so nobody's clicking keeps it busy for everyone else.
var limits = map[string]ratelimit.Policy{
	"postCalendarRsvpRead":                 {Name: "calendar-rsvp-read", Limit: 60, Window: 10 * time.Minute},
	"postCalendarRsvp":                     {Name: "calendar-rsvp", Limit: 30, Window: 10 * time.Minute},
	"getCalendarFeedByFile":                {Name: "calendar-feed", Limit: 120, Window: 10 * time.Minute},
	"postCalendarSubscriptions":            subscriptionFetchLimit,
	"putCalendarSubscriptionsById":         subscriptionFetchLimit,
	"postCalendarSubscriptionsByIdRefresh": subscriptionFetchLimit,
	"putCalendarMailboxSync":               {Name: "calendar-mailbox-sync-write", Limit: 10, Window: 10 * time.Minute},
	"postCalendarMailboxSyncRun":           {Name: "calendar-mailbox-run", Limit: 6, Window: time.Minute},
}

// subscriptionFetchLimit is the one policy of the three operations that
// fetch inline.
var subscriptionFetchLimit = ratelimit.Policy{Name: "calendar-subscription-fetch", Limit: 30, Window: 10 * time.Minute}

// Module is calendar as a platform module: its contract mounted under
// /api/v1/calendar/, its two permissions in the composed catalog and its
// workers — calendar-cancellations, calendar-subscriptions and
// calendar-mailbox-sync. It provides no contract to anyone; it reads
// Deps.CalendarSources, Deps.Users, Deps.Calendar and, when wired,
// Deps.Directory, Deps.Projects and Deps.Workforce, mails through
// Deps.SMTPSend and writes people's mailboxes through Deps.MailboxCalendar.
func Module() module.Module {
	return module.Module{
		Name:        "calendar",
		Permissions: permissions,
		Mount:       mount,
		Workers:     workers,
	}
}

// mount registers every contract operation on the platform router, which wraps
// each in its access rule and request-body cap before the generated wrapper
// decodes it. It fails when the router reports a problem — an operation never
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
