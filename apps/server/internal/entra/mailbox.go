package entra

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/entra/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// This file is the module's contracts.MailboxCalendar (calendar Outlook sync
// design O3, O4; plan readings 10–12): the calendar's copies of a person's
// appointments and items written to their Exchange Online mailbox through
// the module's Graph client and its token, with Calendars.ReadWrite.
//
// Status reads the connection's row and the secrets and nothing else. Every
// other call checks the status first — anything but a delete needs
// available; a delete, DeleteCalendar and Find run under cleanup too — then
// the mailbox: an object id that is a row of entra.accounts the module made
// or whose adoption is confirmed, and not deleted (ErrMailboxUnknown
// otherwise), so a caller can never point the provider at a mailbox the
// module does not manage. The writes go in $batch requests of at most
// twenty, one after another (Outlook's four concurrent requests per
// mailbox); an event's marker, a single-value extended property, is written
// on create, and again only by an update naming the same key (design
// O10.2), and a calendar the module makes carries a marker of its own. Both markers' values start "vantigo:<client id>" — the
// Entra application's, which tells two installations sharing a tenant apart
// — and a calendar or an event without this installation's is never the
// module's: never adopted, deleted or answered by Find or Marked.
//
// It reads the mailbox too (design O9; mailbox_delta.go): Delta pages
// through Graph's calendarView/delta of the default calendar — its link
// checked to be this mailbox's before it is followed — decoding an event's
// id, type, times, showAs and cancelled flag and nothing of what it is
// about; Marked reads single events by id in $batch requests of twenty to
// tell this installation's copies by their marker; and Versions and Events
// (design O10; mailbox_writeback.go) read the copies' change keys, and the
// events the write-back takes, whole. All four are refused under cleanup.
// Watch, Unwatch and Notified (design O11; mailbox_notifications.go) keep a
// Graph subscription on the mailbox's events, whose notifications the
// module's webhook records.
// Graph's words are never logged — an event's subject or body may
// come back in them — nor is any subject, body or address: a refusal is
// logged by its status and codes alone.

// The mailbox's figures and names (the design's O4).
const (
	// mailboxMarker is the extended property every event the module writes
	// carries, its value the session's keyPrefix and the calendar's key.
	mailboxMarker = "String {7b1c5e3a-6f0d-4d5e-9a40-3c7a5e2b9d11} Name VantigoKey"
	// calendarMarker is the extended property every calendar the module
	// makes carries, its value the session's calendarMark.
	calendarMarker = "String {7b1c5e3a-6f0d-4d5e-9a40-3c7a5e2b9d11} Name VantigoCalendar"
	// mailboxKeyPrefix starts both markers' values, the client id after it.
	mailboxKeyPrefix = "vantigo:"
	// mailboxCalendarNames is how many names EnsureCalendar tries — name,
	// then "name (2)" up to "name (5)" — past calendars not the module's.
	mailboxCalendarNames = 5
	// mailboxMaxRetryAfter is the longest Retry-After a throttled op of a
	// $batch is waited for; a longer one is the op's failure, so one mailbox
	// never holds the calendar's worker.
	mailboxMaxRetryAfter = 30 * time.Second
	// mailboxBatchSize is the most requests one $batch holds, Graph's limit.
	mailboxBatchSize = 20
	// mailboxFindPage is the page size Find asks for.
	mailboxFindPage = 100
	// mailboxFindMax is the most events Find answers.
	mailboxFindMax = 5000
	// mailboxMaxID bounds an id the caller hands back (a calendar's, an
	// event's); Graph's are about 150 characters.
	mailboxMaxID = 512
	// mailboxMaxName bounds a calendar's name.
	mailboxMaxName = 255
)

// reasonSwitchOff is the status reason the provider answers besides the
// connection's own.
const reasonSwitchOff = "switch_off"

// mailboxGoneCodes are the codes of Graph's refusal for a mailbox that is no
// longer there: a user with no mailbox in Exchange Online (soft-deleted,
// inactive or on-premises), or no such user — Exchange's ErrorInvalidUser,
// the directory's Request_ResourceNotFound. A missing calendar or event is
// ErrorItemNotFound, never one of these: Graph's bare ResourceNotFound is
// left out on purpose, so an event gone is never read as its mailbox gone.
var mailboxGoneCodes = []string{
	"MailboxNotEnabledForRESTAPI", "ErrorInvalidUser", "ErrorNonExistentMailbox", "Request_ResourceNotFound",
}

// The Graph event's values the contract's ShowAs names.
var showAsValues = []string{
	contracts.ShowAsFree, contracts.ShowAsTentative, contracts.ShowAsBusy,
	contracts.ShowAsOOF, contracts.ShowAsWorkingElsewhere,
}

// mailboxCalendar is the module's contracts.MailboxCalendar over client.
type mailboxCalendar struct {
	deps   module.Deps
	client *graphClient
}

var _ contracts.MailboxCalendar = (*mailboxCalendar)(nil)

// newMailboxCalendar builds the module's mailbox provider over d, for
// Compose and Workers alike.
func newMailboxCalendar(d module.Deps) contracts.MailboxCalendar {
	return &mailboxCalendar{deps: d, client: newGraphClient(d)}
}

// Key is "entra", with no context and no network.
func (m *mailboxCalendar) Key() string { return providerKey }

// Status is whether the provider may write, from the connection's row and
// the secrets with no network (plan reading 11).
func (m *mailboxCalendar) Status(ctx context.Context) (contracts.MailboxStatus, error) {
	q := store.New(m.deps.Pool)
	conn, err := q.GetConnection(ctx)
	if err != nil {
		return contracts.MailboxStatus{}, fmt.Errorf("entra: read the connection: %w", err)
	}
	creds, err := q.ListCredentials(ctx)
	if err != nil {
		return contracts.MailboxStatus{}, fmt.Errorf("entra: read the secrets: %w", err)
	}
	return mailboxStatusOf(conn, creds, m.client.now()), nil
}

// mailboxStatusOf is the provider's state at now:
//
//   - available while the switch is on and the connection connected
//     (statusOf), whether or not the last test's token carried
//     Calendars.ReadWrite: a grant made with Exchange RBAC for Applications
//     never shows in the token, so the role is advisory (GET /connection's
//     permissionsMissing) and a mailbox outside the grant answers its own
//     403, ErrMailboxDenied (calendar Outlook sync design O2);
//   - cleanup while the switch is off (switch_off) and a credential still
//     works — the tenant and the client set, a secret unrevoked and
//     unexpired, and the tenant not refusing it (failing
//     credential_invalid): a person's copies can still be removed;
//   - else unavailable: switch_off while the switch is off, else the
//     connection's reason, or its status when it names none.
func mailboxStatusOf(conn store.EntraConnection, creds []store.EntraCredential, now time.Time) contracts.MailboxStatus {
	status, reason := statusOf(conn, creds, now)
	connected := status == contracts.ProviderConnected
	working := conn.TenantID != nil && conn.ClientID != nil && len(usableCredentials(creds, now)) > 0 &&
		(status != contracts.ProviderFailing || reason != reasonCredentialInvalid)
	switch {
	case conn.OutlookConnect && connected:
		return contracts.MailboxStatus{State: contracts.MailboxAvailable}
	case !conn.OutlookConnect && working:
		return contracts.MailboxStatus{State: contracts.MailboxCleanup, Reason: reasonSwitchOff}
	case !conn.OutlookConnect:
		return contracts.MailboxStatus{State: contracts.MailboxUnavailable, Reason: reasonSwitchOff}
	}
	if reason == "" {
		reason = status
	}
	if !reasonPattern.MatchString(reason) {
		reason = ""
	}
	return contracts.MailboxStatus{State: contracts.MailboxUnavailable, Reason: reason}
}

// mailboxSession is one call's footing: the queries, the credential, the
// mailbox's object id and whether the provider is in cleanup.
type mailboxSession struct {
	q       *store.Queries
	cred    graphCredential
	oid     uuid.UUID
	cleanup bool
}

// open is a call's footing for mailbox: checked (checked's rules), then a
// credential.
func (m *mailboxCalendar) open(ctx context.Context, mailbox string, cleanupAllowed bool) (mailboxSession, error) {
	s, err := m.checked(ctx, mailbox, cleanupAllowed)
	if err != nil {
		return mailboxSession{}, err
	}
	if err := m.withCredential(ctx, &s); err != nil {
		return mailboxSession{}, err
	}
	return s, nil
}

// withCredential sets s's credential, which may fetch a token: the network.
func (m *mailboxCalendar) withCredential(ctx context.Context, s *mailboxSession) error {
	cred, err := credentialFor(ctx, s.q, m.client)
	switch {
	case errors.Is(err, errNotConfigured), errors.Is(err, errNoCredential):
		return fmt.Errorf("%w: %w", contracts.ErrMailboxUnavailable, err)
	case err != nil:
		return err
	}
	s.cred = cred
	return nil
}

// checked is a call's footing for mailbox with no credential yet, and so
// no network: the status checked — ErrMailboxUnavailable unless available,
// or cleanup when cleanupAllowed —, then the account (ErrMailboxUnknown
// unless it is a row the module made or whose adoption is confirmed, and
// not deleted).
func (m *mailboxCalendar) checked(ctx context.Context, mailbox string, cleanupAllowed bool) (mailboxSession, error) {
	st, err := m.Status(ctx)
	if err != nil {
		return mailboxSession{}, err
	}
	switch {
	case st.State == contracts.MailboxAvailable:
	case st.State == contracts.MailboxCleanup && cleanupAllowed:
	default:
		return mailboxSession{}, fmt.Errorf("entra: the mailbox provider is %s (%s): %w", st.State, st.Reason, contracts.ErrMailboxUnavailable)
	}
	oid, ok := objectID(mailbox)
	if !ok {
		return mailboxSession{}, fmt.Errorf("entra: a mailbox that is no object id: %w", contracts.ErrMailboxUnknown)
	}
	q := store.New(m.deps.Pool)
	row, err := q.GetAccount(ctx, oid)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return mailboxSession{}, fmt.Errorf("entra: no account %s: %w", oid, contracts.ErrMailboxUnknown)
	case err != nil:
		return mailboxSession{}, fmt.Errorf("entra: read the account: %w", err)
	case !trusted(row):
		return mailboxSession{}, fmt.Errorf("entra: the account %s is an unconfirmed adoption: %w", oid, contracts.ErrMailboxUnknown)
	case row.State == stateDeleted:
		return mailboxSession{}, fmt.Errorf("entra: the account %s is deleted: %w", oid, contracts.ErrMailboxUnknown)
	}
	return mailboxSession{q: q, oid: oid, cleanup: st.State == contracts.MailboxCleanup}, nil
}

// keyPrefix starts the value of the marker of an event this installation
// writes: "vantigo:<client id>:", the client id a lower-case GUID.
func (s mailboxSession) keyPrefix() string {
	return mailboxKeyPrefix + s.cred.clientID.String() + ":"
}

// calendarMark is the value of the marker of a calendar this installation
// makes: "vantigo:<client id>".
func (s mailboxSession) calendarMark() string {
	return mailboxKeyPrefix + s.cred.clientID.String()
}

// validID reports whether id is one Graph could have answered: present,
// bounded, printable with no space, and no path segment of its own — "." or
// "..", bare or percent-encoded — which would move a $batch request's URL
// off the item it names.
func validID(id string) bool {
	if id == "" || len(id) > mailboxMaxID {
		return false
	}
	// Each decoding of a percent-encoded id is judged too ("%2e", "%252E"),
	// so no proxy or server decoding it again can find a dot segment.
	for v, n := id, 0; n < 4; n++ {
		if v == "." || v == ".." {
			return false
		}
		decoded, err := url.PathUnescape(v)
		if err != nil || decoded == v {
			break
		}
		v = decoded
	}
	for _, r := range id {
		if r <= ' ' || r == 0x7f {
			return false
		}
	}
	return true
}

// calendarPath is /users/{oid}/calendars/{id}, and more after it.
func (s mailboxSession) calendarPath(id string, rest ...string) string {
	return userPath(s.oid, "/calendars/", url.PathEscape(id), strings.Join(rest, ""))
}

// eventsPath is where an event of calendarID is made: the default
// calendar's /users/{oid}/events, else /users/{oid}/calendars/{id}/events.
func (s mailboxSession) eventsPath(calendarID string) string {
	if calendarID == "" {
		return userPath(s.oid, "/events")
	}
	return s.calendarPath(calendarID, "/events")
}

// eventPath is /users/{oid}/events/{id}.
func (s mailboxSession) eventPath(id string) string {
	return userPath(s.oid, "/events/", url.PathEscape(id))
}

// mailboxGone reports whether g is Graph saying the mailbox is gone.
func mailboxGone(g *graphError) bool {
	return slices.Contains(mailboxGoneCodes, g.Code) || slices.Contains(mailboxGoneCodes, g.Detail)
}

// mailboxRefusal is err as the contract answers it: a 403 ErrMailboxDenied,
// a mailbox gone ErrMailboxGone, anything else itself.
func mailboxRefusal(err error) error {
	g, ok := asGraphError(err)
	switch {
	case !ok:
		return err
	case mailboxGone(g):
		return fmt.Errorf("%w: %w", contracts.ErrMailboxGone, err)
	case g.Status == http.StatusForbidden:
		return fmt.Errorf("%w: %w", contracts.ErrMailboxDenied, err)
	}
	return err
}

// itemMissing reports whether err is Graph's 404 for the item asked for —
// a calendar or an event — rather than for the mailbox.
func itemMissing(err error) bool {
	g, ok := asGraphError(err)
	return ok && g.Status == http.StatusNotFound && !mailboxGone(g)
}

// graphCalendar is a calendar as Graph answers it, with the marker $expand
// asked for.
type graphCalendar struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Properties []graphProperty `json:"singleValueExtendedProperties"`
}

// ours reports whether c carries this installation's calendar marker.
func (s mailboxSession) ours(c graphCalendar) bool {
	for _, p := range c.Properties {
		if strings.EqualFold(p.ID, calendarMarker) && p.Value == s.calendarMark() {
			return true
		}
	}
	return false
}

// calendarExpand is the $expand that answers a calendar's marker.
var calendarExpand = "singleValueExtendedProperties($filter=id eq '" + calendarMarker + "')"

// EnsureCalendar answers a calendar the module made in mailbox (calendar
// Outlook sync design O4): known when GET /users/{oid}/calendars/{known}
// still finds it carrying this installation's marker, else a new one, POST
// /users/{oid}/calendars with the marker. A create the mailbox refuses as a
// name already there (409 ErrorFolderExists) adopts the calendar of that
// name only when it carries the marker — a create whose answer was lost —
// and otherwise tries the next name, "name (2)" up to "name (5)", then
// fails: a calendar of the person's own is never adopted. Refused under
// cleanup.
func (m *mailboxCalendar) EnsureCalendar(ctx context.Context, mailbox, known, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > mailboxMaxName-len(" (5)") {
		return "", errors.New("entra: a calendar's name is 1 to 251 characters")
	}
	if known != "" && !validID(known) {
		return "", errors.New("entra: a calendar id that is no id")
	}
	s, err := m.open(ctx, mailbox, false)
	if err != nil {
		return "", err
	}
	if known != "" {
		var cal graphCalendar
		err := m.client.get(ctx, s.cred, s.calendarPath(known, "?", query(url.Values{"$select": {"id,name"}, "$expand": {calendarExpand}})), &cal, noWords)
		switch {
		case err == nil && cal.ID != "" && s.ours(cal):
			return cal.ID, nil
		case err == nil, itemMissing(err):
		default:
			return "", mailboxRefusal(err)
		}
	}
	for n := 1; n <= mailboxCalendarNames; n++ {
		want := name
		if n > 1 {
			want = fmt.Sprintf("%s (%d)", name, n)
		}
		var made struct {
			ID string `json:"id"`
		}
		body := map[string]any{
			"name":                          want,
			"singleValueExtendedProperties": []graphProperty{{ID: calendarMarker, Value: s.calendarMark()}},
		}
		err = m.client.post(ctx, s.cred, userPath(s.oid, "/calendars"), body, &made, noRetry)
		switch {
		case isStatus(err, http.StatusConflict) || isCode(err, "ErrorFolderExists"):
			id, err := m.calendarNamed(ctx, s, want)
			if err != nil || id != "" {
				return id, err
			}
			continue
		case err != nil:
			return "", mailboxRefusal(err)
		case made.ID == "":
			return "", fmt.Errorf("%w: a calendar made with no id", errTenantUnreachable)
		}
		return made.ID, nil
	}
	return "", fmt.Errorf("entra: the mailbox holds calendars named %q to %q the module did not make", name, fmt.Sprintf("%s (%d)", name, mailboxCalendarNames))
}

// calendarNamed is the id of the mailbox's calendar named name that carries
// this installation's marker, "" when the one of that name does not.
func (m *mailboxCalendar) calendarNamed(ctx context.Context, s mailboxSession, name string) (string, error) {
	filter := "name eq '" + strings.ReplaceAll(name, "'", "''") + "'"
	var page struct {
		Value []graphCalendar `json:"value"`
	}
	params := url.Values{"$filter": {filter}, "$select": {"id,name"}, "$expand": {calendarExpand}}
	if err := m.client.get(ctx, s.cred, userPath(s.oid, "/calendars?", query(params)), &page, noWords); err != nil {
		return "", mailboxRefusal(err)
	}
	listed := false
	for _, c := range page.Value {
		if !strings.EqualFold(c.Name, name) || c.ID == "" {
			continue
		}
		listed = true
		if s.ours(c) {
			return c.ID, nil
		}
	}
	if !listed {
		return "", fmt.Errorf("%w: the calendar the mailbox says exists is not listed", errTenantUnreachable)
	}
	return "", nil
}

// DeleteCalendar deletes the calendar and its events, DELETE
// /users/{oid}/calendars/{id} — only once GET of it with its marker shows it
// carries this installation's; any other is ErrCalendarNotOurs, deleted
// never. A calendar already gone (404) is done. Allowed under cleanup.
func (m *mailboxCalendar) DeleteCalendar(ctx context.Context, mailbox, calendarID string) error {
	if !validID(calendarID) {
		return errors.New("entra: a calendar id that is no id")
	}
	s, err := m.open(ctx, mailbox, true)
	if err != nil {
		return err
	}
	var cal graphCalendar
	err = m.client.get(ctx, s.cred, s.calendarPath(calendarID, "?", query(url.Values{"$select": {"id,name"}, "$expand": {calendarExpand}})), &cal, noWords)
	switch {
	case itemMissing(err):
		return nil
	case err != nil:
		return mailboxRefusal(err)
	case !s.ours(cal):
		return fmt.Errorf("entra: the calendar carries no marker of this installation: %w", contracts.ErrCalendarNotOurs)
	}
	err = m.client.del(ctx, s.cred, s.calendarPath(calendarID), noWords)
	if err == nil || itemMissing(err) {
		return nil
	}
	return mailboxRefusal(err)
}

// The event as Graph takes it (research §3).
type (
	graphEvent struct {
		Subject      string            `json:"subject"`
		Body         graphItemBody     `json:"body"`
		Location     *graphLocation    `json:"location,omitempty"`
		IsAllDay     bool              `json:"isAllDay"`
		Start        graphDateTimeZone `json:"start"`
		End          graphDateTimeZone `json:"end"`
		ShowAs       string            `json:"showAs"`
		Sensitivity  string            `json:"sensitivity"`
		IsReminderOn bool              `json:"isReminderOn"`
		Properties   []graphProperty   `json:"singleValueExtendedProperties,omitempty"`
	}
	graphItemBody struct {
		ContentType string `json:"contentType"`
		Content     string `json:"content"`
	}
	graphLocation struct {
		DisplayName string `json:"displayName"`
	}
	graphDateTimeZone struct {
		DateTime string `json:"dateTime"`
		TimeZone string `json:"timeZone"`
	}
	graphProperty struct {
		ID    string `json:"id"`
		Value string `json:"value"`
	}
)

// graphLocalTime is the layout of a dateTime Graph takes beside its zone.
const graphLocalTime = "2006-01-02T15:04:05"

// eventOf is op's event as Graph takes it: the subject; the body as text;
// the location when there is one — on an update always, so a location
// dropped is cleared; a timed event's instants in UTC, an all-day event's
// days at midnight in the Windows name of its zone, the end the day after
// the last; showAs; sensitivity private or normal; no reminder; and the
// marker carrying prefix and the key — on a create always, on an update
// when the op names a Key (calendar Outlook sync design O10.2: an event the
// calendar took in from its own calendar is tagged by the push that
// rewrites it); an update without one leaves the marker as it is.
func eventOf(op contracts.MailboxOp, prefix string) (graphEvent, error) {
	e := op.Event
	if e == nil {
		return graphEvent{}, errors.New("entra: a " + op.Kind + " with no event")
	}
	showAs := e.ShowAs
	if showAs == "" {
		showAs = contracts.ShowAsBusy
	}
	if !slices.Contains(showAsValues, showAs) {
		return graphEvent{}, fmt.Errorf("entra: an event shown as %q", showAs)
	}
	out := graphEvent{
		Subject:     e.Subject,
		Body:        graphItemBody{ContentType: "text", Content: e.Body},
		IsAllDay:    e.AllDay,
		ShowAs:      showAs,
		Sensitivity: "normal",
	}
	if e.Private {
		out.Sensitivity = "private"
	}
	if e.Location != "" || op.Kind == contracts.MailboxOpUpdate {
		out.Location = &graphLocation{DisplayName: e.Location}
	}
	if e.AllDay {
		zone := windowsZoneOf(e.TimeZone)
		first := civilDay(e.StartDate)
		after := civilDay(e.EndDate).AddDate(0, 0, 1)
		if !after.After(first) {
			return graphEvent{}, errors.New("entra: an all-day event that ends before it starts")
		}
		out.Start = graphDateTimeZone{DateTime: first.Format(graphLocalTime), TimeZone: zone}
		out.End = graphDateTimeZone{DateTime: after.Format(graphLocalTime), TimeZone: zone}
	} else {
		if e.StartsAt.IsZero() || e.EndsAt.Before(e.StartsAt) {
			return graphEvent{}, errors.New("entra: a timed event that ends before it starts")
		}
		out.Start = graphDateTimeZone{DateTime: e.StartsAt.UTC().Format(graphLocalTime), TimeZone: "UTC"}
		out.End = graphDateTimeZone{DateTime: e.EndsAt.UTC().Format(graphLocalTime), TimeZone: "UTC"}
	}
	if op.Key != "" {
		out.Properties = []graphProperty{{ID: mailboxMarker, Value: prefix + op.Key}}
	}
	return out, nil
}

// civilDay is t's civil date (its UTC fields) at midnight.
func civilDay(t time.Time) time.Time {
	y, mo, d := t.UTC().Date()
	return time.Date(y, mo, d, 0, 0, 0, 0, time.UTC)
}

// mailboxRequest is one request of a mailbox $batch.
type mailboxRequest struct {
	ID      string            `json:"id"`
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Body    any               `json:"body,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// requestOf is op's request against calendarID: POST of a create to the
// calendar's events, PATCH of an update and DELETE of a delete to the
// event.
func (s mailboxSession) requestOf(op contracts.MailboxOp, calendarID string) (mailboxRequest, error) {
	switch op.Kind {
	case contracts.MailboxOpCreate:
		if op.Key == "" {
			return mailboxRequest{}, errors.New("entra: a create with no key")
		}
		ev, err := eventOf(op, s.keyPrefix())
		if err != nil {
			return mailboxRequest{}, err
		}
		return mailboxRequest{Method: http.MethodPost, URL: s.eventsPath(calendarID), Body: ev,
			Headers: map[string]string{"Content-Type": "application/json"}}, nil
	case contracts.MailboxOpUpdate:
		if !validID(op.EventID) {
			return mailboxRequest{}, errors.New("entra: an update of an event that is no id")
		}
		ev, err := eventOf(op, s.keyPrefix())
		if err != nil {
			return mailboxRequest{}, err
		}
		return mailboxRequest{Method: http.MethodPatch, URL: s.eventPath(op.EventID), Body: ev,
			Headers: map[string]string{"Content-Type": "application/json"}}, nil
	case contracts.MailboxOpDelete:
		if !validID(op.EventID) {
			return mailboxRequest{}, errors.New("entra: a delete of an event that is no id")
		}
		return mailboxRequest{Method: http.MethodDelete, URL: s.eventPath(op.EventID)}, nil
	}
	return mailboxRequest{}, fmt.Errorf("entra: an op of kind %q", op.Kind)
}

// Apply runs ops against calendarID ("" the default calendar) in $batch
// requests of at most twenty, one after another, and answers one result per
// op, in order. A create writes the marker with its Key, an update only
// when it names one (eventOf). Under cleanup an op other than a delete answers
// ErrMailboxUnavailable unsent. Each answer is judged on its own: 2xx done
// (a create's id and the change key read from its answer), 404 on an update
// or a delete Gone (a delete is then done), 403 ErrMailboxDenied, a mailbox
// gone ErrMailboxGone, 429 sent again once after its Retry-After — when
// that is at most mailboxMaxRetryAfter — then errThrottled, anything else
// Graph's refusal. Once an op is refused for its mailbox, the ops not yet
// sent answer the same, unsent. A $batch that fails whole — Graph
// unreachable, a 5xx, the context done — fails the call, and no result is
// answered.
func (m *mailboxCalendar) Apply(ctx context.Context, mailbox, calendarID string, ops []contracts.MailboxOp) ([]contracts.MailboxResult, error) {
	if calendarID != "" && !validID(calendarID) {
		return nil, errors.New("entra: a calendar id that is no id")
	}
	s, err := m.open(ctx, mailbox, true)
	if err != nil {
		return nil, err
	}
	results := make([]contracts.MailboxResult, len(ops))
	requests := make([]mailboxRequest, len(ops))
	var pending []int
	for i, op := range ops {
		if s.cleanup && op.Kind != contracts.MailboxOpDelete {
			results[i].Err = fmt.Errorf("entra: only a delete is taken in cleanup: %w", contracts.ErrMailboxUnavailable)
			continue
		}
		r, err := s.requestOf(op, calendarID)
		if err != nil {
			results[i].Err = err
			continue
		}
		r.ID = strconv.Itoa(i)
		requests[i] = r
		pending = append(pending, i)
	}
	var refused error
	for chunk := range slices.Chunk(pending, mailboxBatchSize) {
		if refused != nil {
			for _, i := range chunk {
				results[i].Err = refused
			}
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := m.batch(ctx, s, ops, requests, chunk, results); err != nil {
			return nil, err
		}
		for _, i := range chunk {
			if errors.Is(results[i].Err, contracts.ErrMailboxDenied) || errors.Is(results[i].Err, contracts.ErrMailboxGone) {
				refused = results[i].Err
				break
			}
		}
	}
	return results, nil
}

// batchAnswer is a $batch's answer: each request's id, status, headers and
// body. A header's value is kept raw — Graph answers strings, but a number
// must not fail the whole answer's decoding.
type batchAnswer struct {
	Responses []struct {
		ID      string                     `json:"id"`
		Status  int                        `json:"status"`
		Headers map[string]json.RawMessage `json:"headers"`
		Body    json.RawMessage            `json:"body"`
	} `json:"responses"`
}

// batchHeader is the value of the header name, whatever its case, in an
// answer's headers: a string as it is, a number in its digits, anything else
// "".
func batchHeader(h map[string]json.RawMessage, name string) string {
	for k, raw := range h {
		if !strings.EqualFold(k, name) {
			continue
		}
		var str string
		if err := json.Unmarshal(raw, &str); err == nil {
			return str
		}
		var num json.Number
		if err := json.Unmarshal(raw, &num); err == nil {
			return num.String()
		}
		return ""
	}
	return ""
}

// batch sends the ops at chunk (indices into ops) as one $batch and writes
// their results; the ones answered 429 are sent once more, alone, after the
// longest Retry-After among them. A batch holding a create is sent noRetry —
// a 5xx may have made the event, which the caller's reconcile finds —, any
// other noWords; both read the answer through the batch's cap. The error is
// the batch failing whole, or the wait cut short.
func (m *mailboxCalendar) batch(ctx context.Context, s mailboxSession, ops []contracts.MailboxOp, requests []mailboxRequest, chunk []int, results []contracts.MailboxResult) error {
	send := chunk
	for attempt := 1; ; attempt++ {
		body := make([]mailboxRequest, len(send))
		opts := []callOption{noWords, largeAnswer}
		for n, i := range send {
			body[n] = requests[i]
			if ops[i].Kind == contracts.MailboxOpCreate {
				opts[0] = noRetry
			}
		}
		var answer batchAnswer
		if err := m.client.post(ctx, s.cred, "/$batch", map[string]any{"requests": body}, &answer, opts...); err != nil {
			return err
		}
		byID := make(map[string]int, len(answer.Responses))
		for n, r := range answer.Responses {
			byID[r.ID] = n
		}
		var throttled []int
		var wait time.Duration
		for _, i := range send {
			n, ok := byID[requests[i].ID]
			if !ok {
				results[i] = contracts.MailboxResult{Err: fmt.Errorf("%w: the $batch did not answer an op", errTenantUnreachable)}
				continue
			}
			r := answer.Responses[n]
			if r.Status == http.StatusTooManyRequests {
				d, ok := retryAfter(http.Header{"Retry-After": {batchHeader(r.Headers, "Retry-After")}}, m.client.now())
				if attempt == 1 && ok && d <= mailboxMaxRetryAfter {
					throttled = append(throttled, i)
					wait = max(wait, d)
					continue
				}
				results[i] = contracts.MailboxResult{Err: errThrottled}
				continue
			}
			results[i] = m.result(ctx, s, ops[i], r.Status, r.Body)
		}
		if len(throttled) == 0 {
			return nil
		}
		if err := m.client.pause(ctx, attempt, wait); err != nil {
			return err
		}
		send = throttled
	}
}

// result is the result of op answered status with body.
func (m *mailboxCalendar) result(ctx context.Context, s mailboxSession, op contracts.MailboxOp, status int, body json.RawMessage) contracts.MailboxResult {
	if status >= http.StatusOK && status < http.StatusMultipleChoices {
		var ev struct {
			ID        string `json:"id"`
			ChangeKey string `json:"changeKey"`
		}
		if len(body) > 0 {
			_ = json.Unmarshal(body, &ev)
		}
		out := contracts.MailboxResult{EventID: op.EventID, ChangeKey: ev.ChangeKey}
		if op.Kind == contracts.MailboxOpCreate {
			if ev.ID == "" {
				return contracts.MailboxResult{Err: fmt.Errorf("%w: an event made with no id", errTenantUnreachable)}
			}
			out.EventID = ev.ID
		}
		return out
	}
	var refusal graphErrorBody
	_ = json.Unmarshal(body, &refusal)
	g := &graphError{Status: status, Code: refusal.Error.Code}
	if len(refusal.Error.Details) > 0 {
		g.Detail = refusal.Error.Details[0].Code
	}
	m.client.logger.DebugContext(ctx, "entra: Graph refused a mailbox write",
		"tenant", s.cred.tenantID.String(), "kind", op.Kind, "status", status, "code", g.Code, "detail", g.Detail)
	switch {
	case mailboxGone(g):
		return contracts.MailboxResult{Err: fmt.Errorf("%w: %w", contracts.ErrMailboxGone, g)}
	case status == http.StatusForbidden:
		return contracts.MailboxResult{Err: fmt.Errorf("%w: %w", contracts.ErrMailboxDenied, g)}
	case status == http.StatusNotFound && op.Kind != contracts.MailboxOpCreate:
		return contracts.MailboxResult{EventID: op.EventID, Gone: true}
	}
	return contracts.MailboxResult{Err: g}
}

// Find answers the events of the calendar — mailbox's default one,
// GET /users/{oid}/events, or the secondary calendarID's,
// GET /users/{oid}/calendars/{id}/events — filtered on this installation's
// marker (its value starting keyPrefix) and, when from is set, on a start at
// or after it, each with the key its marker carries and Meeting when it has
// attendees ($select=id,attendees — their count alone decoded, never an
// address), in pages of a hundred following @odata.nextLink, at most
// mailboxFindMax. An event whose marker another installation wrote is never
// answered. A secondary calendar that is gone (its list answered 404, not
// the mailbox gone) answers no events. Allowed under cleanup.
func (m *mailboxCalendar) Find(ctx context.Context, mailbox, calendarID string, from time.Time) ([]contracts.MailboxEventRef, error) {
	if calendarID != "" && !validID(calendarID) {
		return nil, errors.New("entra: a calendar id that is no id")
	}
	s, err := m.open(ctx, mailbox, true)
	if err != nil {
		return nil, err
	}
	prefix := s.keyPrefix()
	filter := "singleValueExtendedProperties/Any(ep: ep/id eq '" + mailboxMarker + "' and startswith(ep/value, '" + prefix + "'))"
	if !from.IsZero() {
		filter += " and start/dateTime ge '" + from.UTC().Format(graphLocalTime) + "'"
	}
	params := url.Values{
		"$expand": {"singleValueExtendedProperties($filter=id eq '" + mailboxMarker + "')"},
		"$filter": {filter},
		"$select": {"id,attendees"},
		"$top":    {strconv.Itoa(mailboxFindPage)},
	}
	path := s.eventsPath(calendarID) + "?" + query(params)
	out := []contracts.MailboxEventRef{}
	for path != "" && len(out) < mailboxFindMax {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var page struct {
			Value []struct {
				ID         string          `json:"id"`
				Attendees  []struct{}      `json:"attendees"`
				Properties []graphProperty `json:"singleValueExtendedProperties"`
			} `json:"value"`
			NextLink string `json:"@odata.nextLink"`
		}
		if err := m.client.get(ctx, s.cred, path, &page, noWords); err != nil {
			if calendarID != "" && len(out) == 0 && itemMissing(err) {
				// The calendar itself is gone: it holds nothing of ours.
				return out, nil
			}
			return nil, mailboxRefusal(err)
		}
		for _, ev := range page.Value {
			if ev.ID == "" {
				continue
			}
			key := ""
			for _, p := range ev.Properties {
				if v, ok := strings.CutPrefix(p.Value, prefix); ok && strings.EqualFold(p.ID, mailboxMarker) {
					key = v
				}
			}
			if key == "" {
				continue
			}
			out = append(out, contracts.MailboxEventRef{EventID: ev.ID, Key: key, Meeting: len(ev.Attendees) > 0})
			if len(out) == mailboxFindMax {
				break
			}
		}
		path = ""
		if page.NextLink != "" {
			rest, ok := strings.CutPrefix(page.NextLink, graphAPIBase)
			if !ok {
				return nil, fmt.Errorf("%w: a next page off Graph", errTenantUnreachable)
			}
			path = rest
		}
	}
	return out, nil
}
