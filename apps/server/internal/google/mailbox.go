package google

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/google/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// This file is the module's contracts.MailboxCalendar (Google Workspace
// design G30–G32; delivery E, docs/superpowers/plans/2026-11-03-google-calendar.md):
// the calendar's copies of a person's appointments and items written to their
// Google Calendar through Calendar v3 — every call on the person's own Google
// connection with the calendar feature (personal_token.go), never on the
// service account: no calendar scope is ever delegated (Q3).
//
// Status reads the connection's row, the credentials and the connections and
// nothing else. Every other call checks the status first — anything but a
// removal needs available; DeleteCalendar, Find, Unwatch and an Apply of
// deletes alone run under cleanup too —, then the mailbox: a Directory id
// that is a row of google.accounts the module made or whose adoption is
// confirmed, and not deleted (ErrMailboxUnknown otherwise), so a caller can
// never point the provider at an account the module does not manage. Then the
// person's token (mailboxSource): a connection that cannot serve the call
// answers contracts.ErrConnectionNeeded with its reason; the removals are
// served by a closing connection while it holds its token, and answer
// ErrMailboxGone once none can.
//
// Every event the module writes carries the installation's marker in its
// private extended properties (vantigo = google.connection.marker, vantigoKey
// = the calendar's key), and a calendar the module makes is recorded in
// google.calendars — Google admits two calendars of one name and keeps no
// property on a calendar, so the row is the calendar's marker. A calendar or
// an event without this installation's is never the module's: never adopted,
// deleted or answered by Find or Marked. Writes go fifty to a batch, one
// batch after another (mailbox_batch.go). quotaUser is the person's Google id
// on every call, so one busy person does not spend the organisation's quota.
//
// The reads (mailbox_delta.go, mailbox_writeback.go) ask Google for the
// fields they decode and no other; the delta and the versions never ask for a
// title, a description, a location or an attendee's address. Nothing an
// event says is logged, nor any address, nor Google's words: a refusal is
// logged by its status and reason alone.

// The calendar's figures and names (G30).
const (
	// calendarPathPrefix is calendarBase's path, which a batch part's path
	// starts with.
	calendarPathPrefix = "/calendar/v3"
	// calendarFeature is the feature of the person's connection every call
	// asks for (G26).
	calendarFeature = "calendar"
	// markerProperty and keyProperty are the private extended properties a
	// copy carries: the installation's marker and the calendar's key.
	markerProperty = "vantigo"
	keyProperty    = "vantigoKey"
	// primaryCalendar is the calendar id of a person's default calendar.
	primaryCalendar = "primary"
	// mailboxFindMax is the most events Find answers.
	mailboxFindMax = 5000
	// maxEventID and maxCalendarID bound an id the caller hands back.
	maxEventID    = 1024
	maxCalendarID = 255
	// maxCalendarName bounds a calendar's name.
	maxCalendarName = 255
)

// reasonSwitchOff is the status reason while Connection for people is off.
const reasonSwitchOff = "switch_off"

// The ids Google answers: an event's is base32hex, an instance's with
// _<time>Z after it; a calendar's is primary, an address, or a group
// calendar's address.
var (
	eventIDPattern    = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	calendarIDPattern = regexp.MustCompile(`^[A-Za-z0-9_.@+-]+$`)
)

// validEventID reports whether id is one Google could have answered for an
// event.
func validEventID(id string) bool {
	return id != "" && len(id) <= maxEventID && eventIDPattern.MatchString(id)
}

// validCalendarID reports whether id is one Google could have answered for
// a calendar — never a dot segment.
func validCalendarID(id string) bool {
	return id != "" && len(id) <= maxCalendarID && calendarIDPattern.MatchString(id) && id != "." && id != ".."
}

// mailboxCalendar is the module's contracts.MailboxCalendar: the people's
// tokens and the module's client and rows.
type mailboxCalendar struct {
	deps   module.Deps
	c      *googleClient
	q      *store.Queries
	tokens *personalTokens
	// expiredLogged are the mailboxes whose delta was restarted for want of
	// a sync token, logged once each in the process (G31).
	expiredLogged loggedOnce
}

var _ contracts.MailboxCalendar = (*mailboxCalendar)(nil)

// newMailboxCalendar builds the module's mailbox provider over d, for
// Compose and Workers alike.
func newMailboxCalendar(d module.Deps) contracts.MailboxCalendar {
	t := newPersonalTokens(d)
	return &mailboxCalendar{deps: d, c: t.c, q: t.q, tokens: t}
}

// Key is "google", with no context and no network.
func (m *mailboxCalendar) Key() string { return providerKey }

// DisplayName is "Google Calendar", the calendar service a person reads in
// the calendar's labels, with no context and no network.
func (m *mailboxCalendar) DisplayName() string { return "Google Calendar" }

// Status is whether the provider may write, from the module's rows with no
// network (G30).
func (m *mailboxCalendar) Status(ctx context.Context) (contracts.MailboxStatus, error) {
	_, st, err := m.state(ctx)
	return st, err
}

// state is the connection's row and the status Status answers from it:
//
//   - available while Connection for people is on and the web client ready
//     (personalOffered): each person's call then answers
//     contracts.ErrConnectionNeeded when their own connection cannot serve
//     it;
//   - cleanup (switch_off) while the switch is off, the web client is set and
//     a closing connection still holds its token: the switch's turning off
//     closed every connection keeping its token, so a person's copies can
//     still be removed;
//   - else unavailable: switch_off while the switch is off, else the web
//     client's reason (no_web_client, not_tested).
func (m *mailboxCalendar) state(ctx context.Context) (store.GoogleConnection, contracts.MailboxStatus, error) {
	conn, creds, err := readFooting(ctx, m.q)
	if err != nil {
		return store.GoogleConnection{}, contracts.MailboxStatus{}, err
	}
	switch {
	case personalOffered(conn, creds):
		return conn, contracts.MailboxStatus{State: contracts.MailboxAvailable}, nil
	case conn.PersonalConnection:
		return conn, contracts.MailboxStatus{State: contracts.MailboxUnavailable, Reason: webClientReason(conn, creds)}, nil
	}
	if conn.OauthClientID != nil && liveSecret(creds) != nil {
		holds, err := m.q.ClosingPersonalHoldsToken(ctx)
		if err != nil {
			return store.GoogleConnection{}, contracts.MailboxStatus{}, fmt.Errorf("google: read the closing connections: %w", err)
		}
		if holds {
			return conn, contracts.MailboxStatus{State: contracts.MailboxCleanup, Reason: reasonSwitchOff}, nil
		}
	}
	return conn, contracts.MailboxStatus{State: contracts.MailboxUnavailable, Reason: reasonSwitchOff}, nil
}

// mailboxSession is one call's footing: the account's Google id, the
// installation's marker, whether the provider is in cleanup, and the
// person's token once withToken set it.
type mailboxSession struct {
	gid     string
	marker  string
	cleanup bool
	src     *mailboxSource
}

// checked is a call's footing for mailbox with no token yet, and so no
// network: the status checked — ErrMailboxUnavailable unless available, or
// cleanup when cleanupAllowed —, then the account (ErrMailboxUnknown unless
// it is a row the module made or whose adoption is confirmed, and not
// deleted).
func (m *mailboxCalendar) checked(ctx context.Context, mailbox string, cleanupAllowed bool) (mailboxSession, error) {
	conn, st, err := m.state(ctx)
	if err != nil {
		return mailboxSession{}, err
	}
	switch {
	case st.State == contracts.MailboxAvailable:
	case st.State == contracts.MailboxCleanup && cleanupAllowed:
	default:
		return mailboxSession{}, fmt.Errorf("google: the mailbox provider is %s (%s): %w", st.State, st.Reason, contracts.ErrMailboxUnavailable)
	}
	if !validUserID(mailbox) {
		return mailboxSession{}, fmt.Errorf("google: a mailbox that is no Google id: %w", contracts.ErrMailboxUnknown)
	}
	row, err := m.q.GetAccount(ctx, mailbox)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return mailboxSession{}, fmt.Errorf("google: no account %s: %w", mailbox, contracts.ErrMailboxUnknown)
	case err != nil:
		return mailboxSession{}, fmt.Errorf("google: read the account: %w", err)
	case !trusted(row):
		return mailboxSession{}, fmt.Errorf("google: the account %s is an unconfirmed adoption: %w", mailbox, contracts.ErrMailboxUnknown)
	case row.State == stateDeleted:
		return mailboxSession{}, fmt.Errorf("google: the account %s is deleted: %w", mailbox, contracts.ErrMailboxUnknown)
	}
	return mailboxSession{gid: mailbox, marker: conn.Marker, cleanup: st.State == contracts.MailboxCleanup}, nil
}

// open is a call's footing for mailbox: checked, then the person's token,
// asked for as a removal when removal says so.
func (m *mailboxCalendar) open(ctx context.Context, mailbox string, cleanupAllowed, removal bool) (mailboxSession, error) {
	s, err := m.checked(ctx, mailbox, cleanupAllowed)
	if err != nil {
		return mailboxSession{}, err
	}
	m.withToken(&s, removal)
	return s, nil
}

// withToken sets s's token, the person's for the calendar feature, asked for
// as a removal when removal says so. No network until the first call.
func (m *mailboxCalendar) withToken(s *mailboxSession, removal bool) {
	s.src = &mailboxSource{personalSource: m.tokens.source(s.gid, calendarFeature, removal)}
}

// errNoRemovalToken is a removal no connection can make: the person's is
// closing with no token, or gone (not_connected). It is ErrMailboxGone to
// the contract.
var errNoRemovalToken = errors.New("google: no Google connection holds a token for the removal")

// mailboxSource is the person's token as a mailbox call answers its errors
// (the Outlook design's M9): a connection that cannot serve the call is
// contracts.ErrConnectionNeeded as it is, except a removal whose connection
// is closing with no token or gone — nothing can ever remove those copies —,
// which is ErrMailboxGone; the web client not set up or refused is
// ErrMailboxUnavailable; anything else passes.
type mailboxSource struct {
	*personalSource
}

func (s *mailboxSource) token(ctx context.Context) (string, error) {
	tok, err := s.personalSource.token(ctx)
	return tok, s.answer(err)
}

// answer is err as the contract answers it.
func (s *mailboxSource) answer(err error) error {
	var needed *contracts.ConnectionNeededError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &needed):
		if s.removal && (needed.Reason == contracts.ConnectionClosingReason || needed.Reason == contracts.ConnectionNotConnected) {
			return fmt.Errorf("%w: %w (%s)", contracts.ErrMailboxGone, errNoRemovalToken, needed.Reason)
		}
		return err
	case errors.Is(err, errWebClient):
		return fmt.Errorf("%w: %w", contracts.ErrMailboxUnavailable, err)
	}
	return err
}

// refused is err, a call's failure, as the contract answers it:
//
//   - a 401 — Google refused the person's token after the client fetched a
//     fresh one — moves the connection to reconnect (sign_in_again) at the
//     revision the token came from, through D's guarded reconnect, and is
//     contracts.ErrConnectionNeeded (ErrMailboxGone for a removal whose
//     closing connection's token is forgotten so);
//   - a 403 refusing the person's calendar itself (mailboxDenied) is
//     ErrMailboxDenied;
//   - anything else is itself — a usage limit is errThrottled already, and
//     any other 403 Google's refusal of that one call.
func (m *mailboxCalendar) refused(ctx context.Context, s mailboxSession, err error) error {
	var g *googleError
	if !errors.As(err, &g) {
		return err
	}
	switch {
	case g.Status == http.StatusUnauthorized:
		return s.src.answer(m.unauthorized(ctx, s))
	case mailboxDenied(g):
		return fmt.Errorf("%w: %w", contracts.ErrMailboxDenied, err)
	}
	return err
}

// deniedReasons are the reasons of a 403 that refuses the person's calendar
// itself: the grant or the account may not reach it, or the Calendar API is
// off in the project. Any other 403 — forbiddenForNonOrganizer, say — is
// Google's refusal of that one call; a usage limit (rateLimitReasons,
// quotaReasons) is a throttle.
var deniedReasons = []string{"forbidden", "insufficientPermissions", "accessNotConfigured"}

// mailboxDenied reports whether g is a 403 refusing the person's calendar,
// ErrMailboxDenied to the contract.
func mailboxDenied(g *googleError) bool {
	return g.Status == http.StatusForbidden && slices.Contains(deniedReasons, g.Reason)
}

// primaryRefused is err, a call's failure on the person's primary calendar
// itself (a list of it, its watch), as the contract answers it: Google's 404
// of the primary calendar is ErrMailboxUnknown (G30) — the account has no
// calendar the module can reach —, anything else as refused answers it.
func (m *mailboxCalendar) primaryRefused(ctx context.Context, s mailboxSession, err error) error {
	if errors.Is(err, errNotFound) {
		return fmt.Errorf("google: Google has no primary calendar for the account: %w: %w", contracts.ErrMailboxUnknown, err)
	}
	return m.refused(ctx, s, err)
}

// unauthorized records Google's refusal of the person's token: the row read
// now, moved to reconnect when it is still at the revision the token came
// from (or its closing token forgotten); a row moved on since is a failure
// to retry.
func (m *mailboxCalendar) unauthorized(ctx context.Context, s mailboxSession) error {
	return m.tokens.unauthorized(ctx, s.gid, s.src.lastRevision())
}

// isForbidden reports whether err is Google's 403.
func isForbidden(err error) bool {
	var g *googleError
	return errors.As(err, &g) && g.Status == http.StatusForbidden
}

// itemMissing reports whether err is Google's 404 or 410 for the calendar or
// the event asked for.
func itemMissing(err error) bool {
	var g *googleError
	return errors.As(err, &g) && (g.Status == http.StatusNotFound || g.Status == http.StatusGone)
}

// calendarOf is calendarID as Google names it: "" is the person's primary
// calendar.
func calendarOf(calendarID string) string {
	if calendarID == "" {
		return primaryCalendar
	}
	return calendarID
}

// calendarPath is /calendar/v3/calendars/{id} and more after it.
func calendarPath(calendarID string, rest ...string) string {
	return calendarPathPrefix + "/calendars/" + url.PathEscape(calendarOf(calendarID)) + strings.Join(rest, "")
}

// eventsPath is /calendar/v3/calendars/{id}/events.
func eventsPath(calendarID string) string { return calendarPath(calendarID, "/events") }

// eventPath is /calendar/v3/calendars/{id}/events/{event}.
func eventPath(calendarID, eventID string) string {
	return calendarPath(calendarID, "/events/", url.PathEscape(eventID))
}

// withQuota is path with params and the person's quotaUser as its query.
func (s mailboxSession) withQuota(path string, params url.Values) string {
	if params == nil {
		params = url.Values{}
	}
	params.Set("quotaUser", s.gid)
	return path + "?" + params.Encode()
}

// get reads path (under /calendar/v3, with its query) into into on s's token.
func (m *mailboxCalendar) get(ctx context.Context, s mailboxSession, path string, into any) error {
	return m.c.get(ctx, s.src, "https://www.googleapis.com"+path, into, shownAs(pathOnly(path)))
}

// send writes body to path (under /calendar/v3, with its query) on s's
// token.
func (m *mailboxCalendar) send(ctx context.Context, s mailboxSession, method, path string, body, into any, opts ...callOption) error {
	return m.c.send(ctx, s.src, method, "https://www.googleapis.com"+path, body, into, append(opts, shownAs(pathOnly(path)))...)
}

// pathOnly is a path without its query, for the log: an id is in the path,
// and the query may carry a token.
func pathOnly(path string) string {
	p, _, _ := strings.Cut(path, "?")
	return p
}

// The events as Calendar v3 writes and reads them, in the fields the
// provider asks for and no other.
type (
	// eventTime is an event's start or end: a date for an all-day event, an
	// instant (RFC 3339) and its zone for a timed one.
	eventTime struct {
		Date     string `json:"date,omitempty"`
		DateTime string `json:"dateTime,omitempty"`
		TimeZone string `json:"timeZone,omitempty"`
	}
	// attendee is an attendee as the provider decodes one: whether it is
	// the person, and their answer — never an address.
	attendee struct {
		Self           bool   `json:"self"`
		ResponseStatus string `json:"responseStatus"`
	}
	// extended is an event's extended properties, the private ones alone.
	extended struct {
		Private map[string]string `json:"private"`
	}
	// readEvent is an event as a read decodes it; each read asks for a part
	// of these fields only.
	readEvent struct {
		ID                 string            `json:"id"`
		Etag               string            `json:"etag"`
		Status             string            `json:"status"`
		Summary            string            `json:"summary"`
		Description        string            `json:"description"`
		Location           string            `json:"location"`
		Start              eventTime         `json:"start"`
		End                eventTime         `json:"end"`
		Transparency       string            `json:"transparency"`
		EventType          string            `json:"eventType"`
		Visibility         string            `json:"visibility"`
		RecurringEventID   string            `json:"recurringEventId"`
		Recurrence         []json.RawMessage `json:"recurrence"`
		Attendees          []attendee        `json:"attendees"`
		ExtendedProperties extended          `json:"extendedProperties"`
	}
)

// cancelled reports whether ev is deleted or cancelled: Google answers a
// deleted event, or a cancelled instance, with status cancelled.
func (ev readEvent) cancelled() bool { return ev.Status == "cancelled" }

// series reports whether ev is an instance of a recurring event, or its
// master.
func (ev readEvent) series() bool { return ev.RecurringEventID != "" || len(ev.Recurrence) > 0 }

// meeting reports whether ev has attendees besides the person.
func (ev readEvent) meeting() bool {
	return slices.ContainsFunc(ev.Attendees, func(a attendee) bool { return !a.Self })
}

// key is the key this installation's marker on ev carries, "" when it
// carries none.
func (ev readEvent) key(marker string) string {
	p := ev.ExtendedProperties.Private
	if p[markerProperty] != marker || marker == "" {
		return ""
	}
	return p[keyProperty]
}

// foreign reports whether ev carries another installation's marker.
func (ev readEvent) foreign(marker string) bool {
	v, ok := ev.ExtendedProperties.Private[markerProperty]
	return ok && v != "" && v != marker
}

// showAs is ev's ShowAs as G30 reads it: transparent → free; an
// out-of-office event → out_of_office; a working location → working
// elsewhere (when opaque: a transparent one is free by the first rule); the
// person's own declined answer → free; their tentative answer → tentative;
// otherwise busy.
func (ev readEvent) showAs() string {
	var self string
	for _, a := range ev.Attendees {
		if a.Self {
			self = a.ResponseStatus
		}
	}
	switch {
	case ev.Transparency == "transparent":
		return contracts.ShowAsFree
	case ev.EventType == "outOfOffice":
		return contracts.ShowAsOutOfOffice
	case ev.EventType == "workingLocation":
		return contracts.ShowAsWorkingElsewhere
	case self == "declined":
		return contracts.ShowAsFree
	case self == "tentative":
		return contracts.ShowAsTentative
	}
	return contracts.ShowAsBusy
}

// times are ev's times as the contract carries them: an all-day event's
// civil dates at midnight UTC, the end inclusive; a timed event's instants
// in UTC. false when they do not read — the event is then left out, never
// placed wrong.
func (ev readEvent) times() (allDay bool, startDate, endDate, startsAt, endsAt time.Time, ok bool) {
	if ev.Start.Date != "" {
		first, err1 := time.Parse(time.DateOnly, ev.Start.Date)
		after, err2 := time.Parse(time.DateOnly, ev.End.Date)
		if err1 != nil || err2 != nil {
			return false, time.Time{}, time.Time{}, time.Time{}, time.Time{}, false
		}
		last := after.AddDate(0, 0, -1)
		if last.Before(first) {
			last = first
		}
		return true, first, last, time.Time{}, time.Time{}, true
	}
	start, err1 := time.Parse(time.RFC3339, ev.Start.DateTime)
	end, err2 := time.Parse(time.RFC3339, ev.End.DateTime)
	if err1 != nil || err2 != nil {
		return false, time.Time{}, time.Time{}, time.Time{}, time.Time{}, false
	}
	start, end = start.UTC(), end.UTC()
	if end.Before(start) {
		end = start
	}
	return false, time.Time{}, time.Time{}, start, end, true
}

// startInstant is when ev starts, for Find's bound: a timed event's instant,
// an all-day event's first day at midnight UTC; false when it does not read.
func (ev readEvent) startInstant() (time.Time, bool) {
	allDay, first, _, start, _, ok := ev.times()
	if allDay {
		return first, ok
	}
	return start, ok
}

// EnsureCalendar answers a calendar the module made in mailbox (G30):
// known when google.calendars holds it for the mailbox and calendars.get
// still finds it; else another calendar the module made there that still
// exists, adopted; else a new one, calendars.insert named name, recorded. A
// calendar no row names is never adopted. A row whose calendar Google no
// longer has is dropped. calendars.insert refused 403 is ErrMailboxDenied:
// the connection's grant may not make a secondary calendar (U8). Refused
// under cleanup.
func (m *mailboxCalendar) EnsureCalendar(ctx context.Context, mailbox, known, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > maxCalendarName {
		return "", errors.New("google: a calendar's name is 1 to 255 characters")
	}
	if known != "" && !validCalendarID(known) {
		return "", errors.New("google: a calendar id that is no id")
	}
	s, err := m.open(ctx, mailbox, false, false)
	if err != nil {
		return "", err
	}
	rows, err := m.q.ListCalendars(ctx, s.gid)
	if err != nil {
		return "", fmt.Errorf("google: read the calendars: %w", err)
	}
	// known first, then the others, oldest first.
	slices.SortStableFunc(rows, func(a, b store.GoogleCalendar) int {
		switch {
		case a.CalendarID == known && b.CalendarID != known:
			return -1
		case b.CalendarID == known && a.CalendarID != known:
			return 1
		}
		return 0
	})
	for _, r := range rows {
		if !validCalendarID(r.CalendarID) {
			continue
		}
		var cal struct {
			ID string `json:"id"`
		}
		err := m.get(ctx, s, s.withQuota(calendarPath(r.CalendarID), url.Values{"fields": {"id"}}), &cal)
		switch {
		case err == nil && cal.ID == r.CalendarID:
			return r.CalendarID, nil
		case err == nil:
			return "", fmt.Errorf("%w: a calendar read answered another id", errGoogleUnreachable)
		case itemMissing(err):
			if err := m.q.DeleteCalendarRow(ctx, store.DeleteCalendarRowParams{GoogleUserID: s.gid, CalendarID: r.CalendarID}); err != nil {
				return "", fmt.Errorf("google: forget a calendar gone: %w", err)
			}
		default:
			return "", m.refused(ctx, s, err)
		}
	}
	var made struct {
		ID string `json:"id"`
	}
	if err := m.send(ctx, s, http.MethodPost, s.withQuota(calendarPathPrefix+"/calendars", url.Values{"fields": {"id"}}),
		map[string]string{"summary": name}, &made, noRetry()); err != nil {
		return "", m.refused(ctx, s, err)
	}
	if !validCalendarID(made.ID) || made.ID == primaryCalendar {
		return "", fmt.Errorf("%w: a calendar made with no id", errGoogleUnreachable)
	}
	if err := m.q.InsertCalendar(ctx, store.InsertCalendarParams{GoogleUserID: s.gid, CalendarID: made.ID, Now: m.c.now()}); err != nil {
		return "", fmt.Errorf("google: record the calendar made: %w", err)
	}
	return made.ID, nil
}

// DeleteCalendar deletes the calendar and its events, calendars.delete —
// only a calendar google.calendars names for the mailbox; any other is
// ErrCalendarNotOurs, deleted never (a calendar the grant cannot read, 403,
// included). A calendar already gone (404, 410), with or without its row, is
// done. The row is dropped once the calendar is. Allowed under cleanup.
func (m *mailboxCalendar) DeleteCalendar(ctx context.Context, mailbox, calendarID string) error {
	if !validCalendarID(calendarID) {
		return errors.New("google: a calendar id that is no id")
	}
	if calendarID == primaryCalendar {
		return fmt.Errorf("google: the primary calendar is the person's own: %w", contracts.ErrCalendarNotOurs)
	}
	s, err := m.open(ctx, mailbox, true, true)
	if err != nil {
		return err
	}
	_, err = m.q.GetCalendar(ctx, store.GetCalendarParams{GoogleUserID: s.gid, CalendarID: calendarID})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// No row: a calendar already deleted — its row dropped — is done; any
		// other is not the module's.
		var cal struct {
			ID string `json:"id"`
		}
		err := m.get(ctx, s, s.withQuota(calendarPath(calendarID), url.Values{"fields": {"id"}}), &cal)
		switch {
		case itemMissing(err):
			return nil
		case err != nil && !isForbidden(err):
			return m.refused(ctx, s, err)
		}
		return fmt.Errorf("google: the calendar is not one the module made: %w", contracts.ErrCalendarNotOurs)
	case err != nil:
		return fmt.Errorf("google: read the calendar: %w", err)
	}
	err = m.send(ctx, s, http.MethodDelete, s.withQuota(calendarPath(calendarID), nil), nil, nil)
	if err != nil && !itemMissing(err) {
		return m.refused(ctx, s, err)
	}
	if err := m.q.DeleteCalendarRow(ctx, store.DeleteCalendarRowParams{GoogleUserID: s.gid, CalendarID: calendarID}); err != nil {
		return fmt.Errorf("google: forget the calendar deleted: %w", err)
	}
	return nil
}

// writeEvent is op's event as Calendar v3 takes it: the title, the
// description, the location (on an update always, so a location dropped is
// cleared), a timed event's instants in RFC 3339 with its zone (the event's
// IANA zone, else UTC), an all-day event's dates (the end the day after the
// last), the other kind's field null so a patch from one kind to the other
// clears it; transparent for free and opaque otherwise — Google keeps no
// tentative or out-of-office on an event written so —; private or default
// visibility; no reminder; and the marker carrying the key — on a create
// always, on an update when the op names a Key.
func writeEvent(op contracts.MailboxOp, marker string) (map[string]any, error) {
	e := op.Event
	if e == nil {
		return nil, errors.New("google: a " + op.Kind + " with no event")
	}
	switch e.ShowAs {
	case "", contracts.ShowAsFree, contracts.ShowAsTentative, contracts.ShowAsBusy, contracts.ShowAsOutOfOffice, contracts.ShowAsWorkingElsewhere:
	default:
		return nil, fmt.Errorf("google: an event shown as %q", e.ShowAs)
	}
	out := map[string]any{
		"summary":      e.Subject,
		"description":  e.Body,
		"transparency": "opaque",
		"visibility":   "default",
		"reminders":    map[string]any{"useDefault": false, "overrides": []any{}},
	}
	if e.ShowAs == contracts.ShowAsFree {
		out["transparency"] = "transparent"
	}
	if e.Private {
		out["visibility"] = "private"
	}
	if e.Location != "" || op.Kind == contracts.MailboxOpUpdate {
		out["location"] = e.Location
	}
	if e.AllDay {
		first := civilDay(e.StartDate)
		after := civilDay(e.EndDate).AddDate(0, 0, 1)
		if !after.After(first) {
			return nil, errors.New("google: an all-day event that ends before it starts")
		}
		out["start"] = map[string]any{"date": first.Format(time.DateOnly), "dateTime": nil, "timeZone": nil}
		out["end"] = map[string]any{"date": after.Format(time.DateOnly), "dateTime": nil, "timeZone": nil}
	} else {
		if e.StartsAt.IsZero() || e.EndsAt.Before(e.StartsAt) {
			return nil, errors.New("google: a timed event that ends before it starts")
		}
		loc, zone := time.UTC, "UTC"
		if e.TimeZone != "" {
			if l, err := time.LoadLocation(e.TimeZone); err == nil {
				loc, zone = l, e.TimeZone
			}
		}
		out["start"] = map[string]any{"dateTime": e.StartsAt.In(loc).Format(time.RFC3339), "timeZone": zone, "date": nil}
		out["end"] = map[string]any{"dateTime": e.EndsAt.In(loc).Format(time.RFC3339), "timeZone": zone, "date": nil}
	}
	if op.Key != "" {
		out["extendedProperties"] = map[string]any{"private": map[string]string{markerProperty: marker, keyProperty: op.Key}}
	}
	return out, nil
}

// civilDay is t's civil date (its UTC fields) at midnight.
func civilDay(t time.Time) time.Time {
	y, mo, d := t.UTC().Date()
	return time.Date(y, mo, d, 0, 0, 0, 0, time.UTC)
}

// writeFields is what a write asks Google to answer: the id, the version and
// the status — nothing of what the event says.
const writeFields = "id,etag,status"

// partOf is op's batch part against calendarID: POST of a create to the
// calendar's events, PATCH of an update and DELETE of a delete to the event.
func (s mailboxSession) partOf(op contracts.MailboxOp, calendarID string) (batchPart, error) {
	q := url.Values{"fields": {writeFields}}
	switch op.Kind {
	case contracts.MailboxOpCreate:
		if op.Key == "" {
			return batchPart{}, errors.New("google: a create with no key")
		}
		ev, err := writeEvent(op, s.marker)
		if err != nil {
			return batchPart{}, err
		}
		return batchPart{method: http.MethodPost, path: s.withQuota(eventsPath(calendarID), q), body: ev}, nil
	case contracts.MailboxOpUpdate:
		if !validEventID(op.EventID) {
			return batchPart{}, errors.New("google: an update of an event that is no id")
		}
		ev, err := writeEvent(op, s.marker)
		if err != nil {
			return batchPart{}, err
		}
		return batchPart{method: http.MethodPatch, path: s.withQuota(eventPath(calendarID, op.EventID), q), body: ev}, nil
	case contracts.MailboxOpDelete:
		if !validEventID(op.EventID) {
			return batchPart{}, errors.New("google: a delete of an event that is no id")
		}
		return batchPart{method: http.MethodDelete, path: s.withQuota(eventPath(calendarID, op.EventID), nil)}, nil
	}
	return batchPart{}, fmt.Errorf("google: an op of kind %q", op.Kind)
}

// Apply runs ops against calendarID ("" the primary calendar) in batches of
// fifty, one after another, and answers one result per op, in order (G30). A
// create writes the marker with its Key, an update only when it names one
// (writeEvent). Under cleanup an op other than a delete answers
// ErrMailboxUnavailable unsent. Each part's answer is judged on its own: 2xx
// done (a create's id, the etag as the change key; an update Google answers
// cancelled is Gone), 404 or 410 on an update or a delete Gone (a delete is
// then done), a 403 refusing the calendar itself (mailboxDenied)
// ErrMailboxDenied, a rate limit sent again once then errThrottled, a quota
// spent errThrottled unsent again, anything else — an event's own 403, such
// as forbiddenForNonOrganizer, included — that op's own refusal. A 401 on
// several parts of a batch asks the person's connection once, its answer
// given to each. Once an op is refused for its mailbox, the ops not yet sent
// answer the same, unsent. A batch that fails whole — Google unreachable, a
// 5xx, the context done — fails the call, and no result is answered; a batch
// holding a create is sent once.
func (m *mailboxCalendar) Apply(ctx context.Context, mailbox, calendarID string, ops []contracts.MailboxOp) ([]contracts.MailboxResult, error) {
	if calendarID != "" && !validCalendarID(calendarID) {
		return nil, errors.New("google: a calendar id that is no id")
	}
	s, err := m.checked(ctx, mailbox, true)
	if err != nil {
		return nil, err
	}
	m.withToken(&s, s.cleanup || !slices.ContainsFunc(ops, func(op contracts.MailboxOp) bool {
		return op.Kind != contracts.MailboxOpDelete
	}))
	results := make([]contracts.MailboxResult, len(ops))
	parts := make([]batchPart, len(ops))
	var pending []int
	for i, op := range ops {
		if s.cleanup && op.Kind != contracts.MailboxOpDelete {
			results[i].Err = fmt.Errorf("google: only a delete is taken in cleanup: %w", contracts.ErrMailboxUnavailable)
			continue
		}
		p, err := s.partOf(op, calendarID)
		if err != nil {
			results[i].Err = err
			continue
		}
		parts[i] = p
		pending = append(pending, i)
	}
	var refused error
	for chunk := range slices.Chunk(pending, calendarBatchSize) {
		if refused != nil {
			for _, i := range chunk {
				results[i].Err = refused
			}
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		send := make([]batchPart, len(chunk))
		once := false
		for n, i := range chunk {
			send[n] = parts[i]
			once = once || ops[i].Kind == contracts.MailboxOpCreate
		}
		replies, err := m.c.batch(ctx, s.src, s.gid, send, once)
		if err != nil {
			return nil, m.refused(ctx, s, err)
		}
		// The first 401's answer, given to every part answered 401: the
		// reconnect moves the row once, and a second ask would read it moved.
		var lost error
		unauthorized := func() error {
			if lost == nil {
				lost = s.src.answer(m.unauthorized(ctx, s))
			}
			return lost
		}
		for n, i := range chunk {
			results[i] = m.result(ctx, ops[i], replies[n], unauthorized)
			if errors.Is(results[i].Err, contracts.ErrMailboxDenied) || errors.Is(results[i].Err, contracts.ErrConnectionNeeded) ||
				errors.Is(results[i].Err, contracts.ErrMailboxGone) {
				refused = results[i].Err
			}
		}
	}
	return results, nil
}

// result is the result of op answered r; unauthorized answers a 401.
func (m *mailboxCalendar) result(ctx context.Context, op contracts.MailboxOp, r batchReply, unauthorized func() error) contracts.MailboxResult {
	if r.status >= http.StatusOK && r.status < http.StatusMultipleChoices {
		var ev readEvent
		if len(r.body) > 0 {
			_ = json.Unmarshal(r.body, &ev)
		}
		out := contracts.MailboxResult{EventID: op.EventID, ChangeKey: ev.Etag}
		switch op.Kind {
		case contracts.MailboxOpCreate:
			if !validEventID(ev.ID) {
				return contracts.MailboxResult{Err: fmt.Errorf("%w: an event made with no id", errGoogleUnreachable)}
			}
			out.EventID = ev.ID
		case contracts.MailboxOpUpdate:
			if ev.cancelled() {
				return contracts.MailboxResult{EventID: op.EventID, Gone: true}
			}
		case contracts.MailboxOpDelete:
			out.ChangeKey = ""
		}
		return out
	}
	g := r.refusal()
	m.c.logger.DebugContext(ctx, "google: Google refused a calendar write", "kind", op.Kind, "status", g.Status, "reason", g.Reason)
	switch {
	case r.throttled():
		return contracts.MailboxResult{Err: errThrottled}
	case r.status == http.StatusUnauthorized:
		return contracts.MailboxResult{Err: unauthorized()}
	case mailboxDenied(g):
		return contracts.MailboxResult{Err: fmt.Errorf("%w: %w", contracts.ErrMailboxDenied, g)}
	case (r.status == http.StatusNotFound || r.status == http.StatusGone) && op.Kind != contracts.MailboxOpCreate:
		return contracts.MailboxResult{EventID: op.EventID, Gone: true}
	}
	return contracts.MailboxResult{Err: g}
}

// listParams are a list's query: the fields it asks, maxResults, and the
// rest given.
func listParams(fields string, rest url.Values) url.Values {
	q := url.Values{"fields": {fields}, "maxResults": {"250"}}
	for k, v := range rest {
		q[k] = v
	}
	return q
}

// eventsPage is one page of events.list as the provider decodes it.
type eventsPage struct {
	Items         []readEvent `json:"items"`
	NextPageToken string      `json:"nextPageToken"`
	NextSyncToken string      `json:"nextSyncToken"`
}

// markedList lists the events of calendarID carrying this installation's
// marker (privateExtendedProperty=vantigo=<marker>) that start at or after
// from when it is set, fields asking what the caller decodes; each page
// handed to each, at most limit events in all. false from each stops it.
// A secondary calendar that is gone answers no events; the primary calendar
// missing (404) is ErrMailboxUnknown.
func (m *mailboxCalendar) markedList(ctx context.Context, s mailboxSession, calendarID string, from time.Time, fields string, each func(readEvent) bool) error {
	params := listParams("items("+fields+"),nextPageToken", url.Values{
		"privateExtendedProperty": {markerProperty + "=" + s.marker},
		"showDeleted":             {"false"},
	})
	if !from.IsZero() {
		// timeMin bounds an event's end; the start is checked below.
		params.Set("timeMin", from.UTC().Format(time.RFC3339))
	}
	first := true
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		var page eventsPage
		if err := m.get(ctx, s, s.withQuota(eventsPath(calendarID), params), &page); err != nil {
			if calendarID != "" && first && itemMissing(err) {
				return nil
			}
			if calendarID == "" {
				return m.primaryRefused(ctx, s, err)
			}
			return m.refused(ctx, s, err)
		}
		first = false
		for _, ev := range page.Items {
			if !validEventID(ev.ID) || ev.cancelled() || ev.key(s.marker) == "" {
				continue
			}
			if !from.IsZero() {
				if start, ok := ev.startInstant(); !ok || start.Before(from) {
					continue
				}
			}
			if !each(ev) {
				return nil
			}
		}
		if page.NextPageToken == "" {
			return nil
		}
		params.Set("pageToken", page.NextPageToken)
	}
}

// Find answers the events of the calendar — calendarID "" the primary one —
// carrying this installation's marker, each with its key and Meeting when it
// has attendees besides the person (their own flag alone decoded, never an
// address), starting at or after from when it is set (G30): events.list with
// privateExtendedProperty=vantigo=<marker>, pages of 250, at most
// mailboxFindMax. An event another installation wrote is never answered. A
// secondary calendar that is gone answers no events. Allowed under cleanup.
func (m *mailboxCalendar) Find(ctx context.Context, mailbox, calendarID string, from time.Time) ([]contracts.MailboxEventRef, error) {
	if calendarID != "" && !validCalendarID(calendarID) {
		return nil, errors.New("google: a calendar id that is no id")
	}
	s, err := m.open(ctx, mailbox, true, true)
	if err != nil {
		return nil, err
	}
	out := []contracts.MailboxEventRef{}
	err = m.markedList(ctx, s, calendarID, from, "id,status,start,end,attendees(self),extendedProperties(private)", func(ev readEvent) bool {
		out = append(out, contracts.MailboxEventRef{EventID: ev.ID, Key: ev.key(s.marker), Meeting: ev.meeting()})
		return len(out) < mailboxFindMax
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
