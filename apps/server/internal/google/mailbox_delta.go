package google

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// This file is the mailbox provider's reads of the primary calendar (Google
// Workspace design G31; the calendar Outlook sync design's O9): Delta, the
// changes a page at a time through a sync token, and Marked, the marker read
// of single events. Neither asks Google for, decodes or logs anything of what
// an event is about: the delta's fields are an event's id, status, times,
// transparency, type, the person's own answer and its series.

// The delta's figures (G31).
const (
	// deltaPages is the most pages one Delta call reads; a delta longer is
	// answered cut, its link to continue with.
	deltaPages = 40
	// deltaLinkPrefix starts every link the provider answers: its version.
	deltaLinkPrefix = "g1."
	// maxDeltaLink bounds a link the caller hands back.
	maxDeltaLink = 8192
	// deltaFields is what a delta page asks of each event.
	deltaFields = "items(id,status,start,end,transparency,eventType,recurringEventId,attendees(self,responseStatus)),nextPageToken,nextSyncToken"
	// markedFields is what Marked asks of an event.
	markedFields = "id,status,extendedProperties(private)"
)

// deltaLink is the provider's link (G31): the mailbox it is for, the window,
// the sync token the next read starts from, and — for a delta cut before its
// end — the next page's token and whether the listing paged is the windowed
// first one. A link with neither token nor page is a delta read to its end
// with no sync token (U7): its next read starts again.
type deltaLink struct {
	Mailbox  string    `json:"m"`
	From     time.Time `json:"f"`
	To       time.Time `json:"t"`
	Sync     string    `json:"s,omitempty"`
	Page     string    `json:"p,omitempty"`
	Windowed bool      `json:"w,omitempty"`
}

// encode is the link as the contract carries it: g1. and the base64url of
// its JSON.
func (l deltaLink) encode() string {
	b, _ := json.Marshal(l)
	return deltaLinkPrefix + base64.RawURLEncoding.EncodeToString(b)
}

// parseDeltaLink reads a link the provider answered for mailbox, false for
// anything else — another mailbox's, another provider's, or none at all.
func parseDeltaLink(link, mailbox string) (deltaLink, bool) {
	rest, ok := strings.CutPrefix(link, deltaLinkPrefix)
	if !ok || len(link) > maxDeltaLink {
		return deltaLink{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(rest)
	if err != nil {
		return deltaLink{}, false
	}
	var l deltaLink
	if err := json.Unmarshal(raw, &l); err != nil || l.Mailbox != mailbox || l.From.IsZero() || !l.To.After(l.From) {
		return deltaLink{}, false
	}
	return l, true
}

// loggedOnce remembers the keys logged once in the process.
type loggedOnce struct {
	mu   sync.Mutex
	seen map[string]bool
}

// first reports whether key is new, remembering it.
func (o *loggedOnce) first(key string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.seen[key] {
		return false
	}
	if o.seen == nil {
		o.seen = map[string]bool{}
	}
	o.seen[key] = true
	return true
}

// Delta reads the changes to mailbox's primary calendar (G31). link "" starts
// afresh: events.list with singleEvents, showDeleted, timeMin and timeMax
// over from..to, paged; the last page's nextSyncToken is kept in the link
// answered Done. A link the provider answered continues it — the sync token
// (its increments are not windowed, U7), or the next page of a cut read — and
// is followed only when it is this mailbox's (parseDeltaLink), else refused
// with a failure, unsent. At most deltaPages pages of 250 a call; a longer
// read is answered cut with its link. 410 is ErrMailboxDeltaExpired, and so
// is a link read to its end with no sync token, which logs once per mailbox in
// the process. The window is enforced here: an event outside from..to, deleted
// or cancelled is answered Removed. Refused under cleanup.
func (m *mailboxCalendar) Delta(ctx context.Context, mailbox string, from, to time.Time, link string) (contracts.MailboxDelta, error) {
	var l deltaLink
	if link == "" {
		if from.IsZero() || !to.After(from) {
			return contracts.MailboxDelta{}, errors.New("google: a delta's window ends before it starts")
		}
		l = deltaLink{Mailbox: mailbox, From: from.UTC(), To: to.UTC(), Windowed: true}
	} else {
		var ok bool
		if l, ok = parseDeltaLink(link, mailbox); !ok {
			return contracts.MailboxDelta{}, errors.New("google: a delta link that is not this mailbox's")
		}
	}
	s, err := m.open(ctx, mailbox, false, false)
	if err != nil {
		return contracts.MailboxDelta{}, err
	}
	if link != "" && l.Sync == "" && l.Page == "" {
		if m.expiredLogged.first(mailbox) {
			m.c.logger.InfoContext(ctx, "google: a delta read with no sync token starts again; each sync of the mailbox re-reads the window")
		}
		return contracts.MailboxDelta{}, fmt.Errorf("google: no sync token to continue with: %w", contracts.ErrMailboxDeltaExpired)
	}
	out := contracts.MailboxDelta{Changes: []contracts.MailboxChange{}}
	for page := 1; ; page++ {
		if err := ctx.Err(); err != nil {
			return contracts.MailboxDelta{}, err
		}
		params := url.Values{"singleEvents": {"true"}, "showDeleted": {"true"}}
		if l.Windowed {
			params.Set("timeMin", l.From.Format(time.RFC3339))
			params.Set("timeMax", l.To.Format(time.RFC3339))
		} else {
			params.Set("syncToken", l.Sync)
		}
		if l.Page != "" {
			params.Set("pageToken", l.Page)
		}
		var answer eventsPage
		if err := m.get(ctx, s, s.withQuota(eventsPath(""), listParams(deltaFields, params)), &answer); err != nil {
			var g *googleError
			if errors.As(err, &g) && g.Status == http.StatusGone {
				return contracts.MailboxDelta{}, fmt.Errorf("%w: %w", contracts.ErrMailboxDeltaExpired, err)
			}
			return contracts.MailboxDelta{}, m.primaryRefused(ctx, s, err)
		}
		skipped := 0
		for _, ev := range answer.Items {
			change, ok := changeOf(ev, l.From, l.To)
			if !ok {
				skipped++
				continue
			}
			out.Changes = append(out.Changes, change)
		}
		if skipped > 0 {
			m.c.logger.DebugContext(ctx, "google: a delta page held events whose ids or times do not read", "skipped", skipped)
		}
		if answer.NextPageToken != "" {
			l.Page = answer.NextPageToken
			if page == deltaPages {
				out.Link = l.encode()
				return out, nil
			}
			continue
		}
		done := deltaLink{Mailbox: l.Mailbox, From: l.From, To: l.To, Sync: answer.NextSyncToken}
		out.Link, out.Done = done.encode(), true
		return out, nil
	}
}

// changeOf is ev as the contract answers it, false for one whose id is no
// valid id or whose times do not read — left out rather than failing the
// page, which would hold the delta at it for ever. A deleted or cancelled
// event, or one outside from..to, is its id alone, Removed.
func changeOf(ev readEvent, from, to time.Time) (contracts.MailboxChange, bool) {
	if !validEventID(ev.ID) {
		return contracts.MailboxChange{}, false
	}
	if ev.cancelled() {
		return contracts.MailboxChange{EventID: ev.ID, Removed: true}, true
	}
	allDay, startDate, endDate, startsAt, endsAt, ok := ev.times()
	if !ok {
		return contracts.MailboxChange{}, false
	}
	begins, ends := startsAt, endsAt
	if allDay {
		begins, ends = startDate, endDate.AddDate(0, 0, 1)
	}
	if !ends.After(from) || !begins.Before(to) {
		return contracts.MailboxChange{EventID: ev.ID, Removed: true}, true
	}
	return contracts.MailboxChange{
		EventID:   ev.ID,
		Series:    ev.RecurringEventID != "",
		AllDay:    allDay,
		StartDate: startDate,
		EndDate:   endDate,
		StartsAt:  startsAt,
		EndsAt:    endsAt,
		ShowAs:    ev.showAs(),
	}, true
}

// Marked answers, among eventIDs, the events of the primary calendar carrying
// this installation's marker with their keys, in the order asked (G30):
// events.get of each in batches of fifty. 200 with the marker is answered;
// 200 without it, with another installation's, or cancelled, and 404 or 410
// are not; a usage limit is errThrottled (a rate limit sent again once
// first); a 403 refusing the calendar fails the whole call
// (ErrMailboxDenied), and so does any other refusal. At most
// MailboxMarkedMax ids, each a valid id; refused under cleanup.
func (m *mailboxCalendar) Marked(ctx context.Context, mailbox string, eventIDs []string) ([]contracts.MailboxEventRef, error) {
	if len(eventIDs) > contracts.MailboxMarkedMax {
		return nil, fmt.Errorf("google: Marked takes at most %d ids", contracts.MailboxMarkedMax)
	}
	for _, id := range eventIDs {
		if !validEventID(id) {
			return nil, errors.New("google: an event id that is no id")
		}
	}
	s, err := m.open(ctx, mailbox, false, false)
	if err != nil {
		return nil, err
	}
	keys := make([]string, len(eventIDs))
	err = m.readEach(ctx, s, "", eventIDs, markedFields, func(i int, r batchReply) error {
		switch r.status {
		case http.StatusOK:
			var ev readEvent
			if err := json.Unmarshal(r.body, &ev); err != nil {
				return fmt.Errorf("%w: an event read is not the JSON expected", errGoogleUnreachable)
			}
			if !ev.cancelled() && ev.ID == eventIDs[i] {
				keys[i] = ev.key(s.marker)
			}
			return nil
		case http.StatusNotFound, http.StatusGone:
			return nil
		}
		return m.partRefusal(ctx, s, r)
	})
	if err != nil {
		return nil, err
	}
	out := []contracts.MailboxEventRef{}
	for i, key := range keys {
		if key != "" {
			out = append(out, contracts.MailboxEventRef{EventID: eventIDs[i], Key: key})
		}
	}
	return out, nil
}

// readEach reads the events ids of calendarID with fields, in batches of
// fifty, handing each reply to each with its index; each's error ends the
// read.
func (m *mailboxCalendar) readEach(ctx context.Context, s mailboxSession, calendarID string, ids []string, fields string, each func(i int, r batchReply) error) error {
	all := make([]int, len(ids))
	for i := range all {
		all[i] = i
	}
	for chunk := range slices.Chunk(all, calendarBatchSize) {
		if err := ctx.Err(); err != nil {
			return err
		}
		parts := make([]batchPart, len(chunk))
		for n, i := range chunk {
			parts[n] = batchPart{method: http.MethodGet, path: s.withQuota(eventPath(calendarID, ids[i]), url.Values{"fields": {fields}})}
		}
		replies, err := m.c.batch(ctx, s.src, s.gid, parts, false)
		if err != nil {
			return m.refused(ctx, s, err)
		}
		for n, i := range chunk {
			if err := each(i, replies[n]); err != nil {
				return err
			}
		}
	}
	return nil
}

// partRefusal is a read's part refused as the whole call answers it: a usage
// limit errThrottled, a 401 the person's connection, a 403 refusing the
// calendar itself (mailboxDenied) ErrMailboxDenied, anything else Google's
// refusal — logged by its status and reason alone.
func (m *mailboxCalendar) partRefusal(ctx context.Context, s mailboxSession, r batchReply) error {
	g := r.refusal()
	m.c.logger.DebugContext(ctx, "google: Google refused a calendar read", "status", g.Status, "reason", g.Reason)
	switch {
	case r.throttled():
		return errThrottled
	case r.status == http.StatusUnauthorized:
		return s.src.answer(m.unauthorized(ctx, s))
	case mailboxDenied(g):
		return fmt.Errorf("%w: %w", contracts.ErrMailboxDenied, g)
	}
	return g
}
