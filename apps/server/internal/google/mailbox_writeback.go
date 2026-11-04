package google

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// This file is the mailbox provider's write-back reads (Google Workspace
// design G30; the calendar Outlook sync design's O10.1, O10.2): Versions, the
// copies' ids and etags a page at a time, and Events, the events the
// write-back takes read whole in batches of fifty. Versions asks Google for an
// event's id, etag, series and marker and nothing else; Events for the fields
// of contracts.MailboxEventState and no other — the attendees as the person's
// own flag and answer, never an address — and neither logs, nor wraps in an
// error, a word of what an event says.

// The reads' fields.
const (
	// versionsMarkedFields is what Versions asks of a copy on the primary
	// calendar.
	versionsMarkedFields = "id,etag,status,start,end,recurringEventId,extendedProperties(private)"
	// versionsViewFields is what Versions asks of an event of a secondary
	// calendar.
	versionsViewFields = "items(id,etag,status,recurringEventId),nextPageToken"
	// eventsFields is what Events asks of an event.
	eventsFields = "id,etag,status,summary,description,location,start,end,transparency,eventType,visibility,recurringEventId,recurrence,attendees(self,responseStatus),extendedProperties(private)"
)

// htmlTag finds a tag in a description: Google's web editor writes HTML, which
// the write-back never takes.
var htmlTag = regexp.MustCompile(`<\s*/?\s*[A-Za-z][A-Za-z0-9]*(\s[^<>]*)?/?\s*>`)

// Versions answers the versions of the calendar's events (G30). calendarID ""
// is the primary calendar: Find's list — the copies carrying this
// installation's marker that start at or after from — each with its Key and
// etag; to is not used. A secondary calendar is events.list over from..to
// with singleEvents, every event of the view with no Key. At most
// MailboxVersionsMax are answered, Complete false when the list was cut. An
// event whose id is no valid id is left out. Refused under cleanup.
func (m *mailboxCalendar) Versions(ctx context.Context, mailbox, calendarID string, from, to time.Time) (contracts.MailboxVersions, error) {
	if calendarID != "" {
		if !validCalendarID(calendarID) {
			return contracts.MailboxVersions{}, errors.New("google: a calendar id that is no id")
		}
		if from.IsZero() || !to.After(from) {
			return contracts.MailboxVersions{}, errors.New("google: a calendar view's window ends before it starts")
		}
	}
	s, err := m.open(ctx, mailbox, false, false)
	if err != nil {
		return contracts.MailboxVersions{}, err
	}
	out := contracts.MailboxVersions{Events: []contracts.MailboxEventVersion{}, Complete: true}
	if calendarID == "" {
		err := m.markedList(ctx, s, "", from, versionsMarkedFields, func(ev readEvent) bool {
			if len(out.Events) == contracts.MailboxVersionsMax {
				out.Complete = false
				return false
			}
			out.Events = append(out.Events, contracts.MailboxEventVersion{
				EventID: ev.ID, Key: ev.key(s.marker), ChangeKey: ev.Etag, Series: ev.series(),
			})
			return true
		})
		if err != nil {
			return contracts.MailboxVersions{}, err
		}
		return out, nil
	}
	params := listParams(versionsViewFields, url.Values{
		"singleEvents": {"true"},
		"timeMin":      {from.UTC().Format(time.RFC3339)},
		"timeMax":      {to.UTC().Format(time.RFC3339)},
	})
	for {
		if err := ctx.Err(); err != nil {
			return contracts.MailboxVersions{}, err
		}
		var page eventsPage
		if err := m.get(ctx, s, s.withQuota(eventsPath(calendarID), params), &page); err != nil {
			return contracts.MailboxVersions{}, m.refused(ctx, s, err)
		}
		for _, ev := range page.Items {
			if !validEventID(ev.ID) || ev.cancelled() {
				continue
			}
			if len(out.Events) == contracts.MailboxVersionsMax {
				out.Complete = false
				return out, nil
			}
			out.Events = append(out.Events, contracts.MailboxEventVersion{EventID: ev.ID, ChangeKey: ev.Etag, Series: ev.series()})
		}
		if page.NextPageToken == "" {
			return out, nil
		}
		params.Set("pageToken", page.NextPageToken)
	}
}

// stateOf is ev as the contract answers it, false when its times do not
// read: Key this installation's key, Foreign another's marker; the times as
// Delta's; ShowAs as G30 reads it; Private a private or confidential
// visibility; TextBody a description with no HTML tag in it; TimeZone "".
func stateOf(ev readEvent, marker string) (contracts.MailboxEventState, bool) {
	allDay, startDate, endDate, startsAt, endsAt, ok := ev.times()
	if !ok {
		return contracts.MailboxEventState{}, false
	}
	return contracts.MailboxEventState{
		EventID:   ev.ID,
		Key:       ev.key(marker),
		ChangeKey: ev.Etag,
		Foreign:   ev.foreign(marker),
		Series:    ev.series(),
		Meeting:   ev.meeting(),
		TextBody:  !htmlTag.MatchString(ev.Description),
		Event: contracts.MailboxEvent{
			Subject:   ev.Summary,
			Body:      ev.Description,
			Location:  ev.Location,
			AllDay:    allDay,
			StartDate: startDate,
			EndDate:   endDate,
			StartsAt:  startsAt,
			EndsAt:    endsAt,
			ShowAs:    ev.showAs(),
			Private:   ev.Visibility == "private" || ev.Visibility == "confidential",
		},
	}, true
}

// Events reads whole, among eventIDs, the events of the calendar (calendarID
// "" the primary one), in the order asked (G30): events.get of each in
// batches of fifty. 200 is answered (stateOf), unless the event answered is
// not the one asked, its body is not the JSON expected or its times do not
// read — skipped, counted at debug level; a cancelled event, 404 and 410 are
// answered in Gone; a 403 of one event is skipped (a refusal of that event
// alone — the mailbox's own refusal is the batch's); a usage limit is sent
// again once, then errThrottled; any other refusal fails the whole call. A
// skipped id is neither answered nor Gone. At most MailboxMarkedMax ids, each
// a valid id; refused under cleanup.
func (m *mailboxCalendar) Events(ctx context.Context, mailbox, calendarID string, eventIDs []string) (contracts.MailboxEventsRead, error) {
	if len(eventIDs) > contracts.MailboxMarkedMax {
		return contracts.MailboxEventsRead{}, fmt.Errorf("google: Events takes at most %d ids", contracts.MailboxMarkedMax)
	}
	if calendarID != "" && !validCalendarID(calendarID) {
		return contracts.MailboxEventsRead{}, errors.New("google: a calendar id that is no id")
	}
	for _, id := range eventIDs {
		if !validEventID(id) {
			return contracts.MailboxEventsRead{}, errors.New("google: an event id that is no id")
		}
	}
	s, err := m.open(ctx, mailbox, false, false)
	if err != nil {
		return contracts.MailboxEventsRead{}, err
	}
	states := make([]*contracts.MailboxEventState, len(eventIDs))
	gone := make([]bool, len(eventIDs))
	skipped := 0
	err = m.readEach(ctx, s, calendarID, eventIDs, eventsFields, func(i int, r batchReply) error {
		switch r.status {
		case http.StatusOK:
			var ev readEvent
			if err := json.Unmarshal(r.body, &ev); err != nil || ev.ID != eventIDs[i] {
				skipped++
				return nil
			}
			if ev.cancelled() {
				gone[i] = true
				return nil
			}
			st, ok := stateOf(ev, s.marker)
			if !ok {
				skipped++
				return nil
			}
			states[i] = &st
			return nil
		case http.StatusNotFound, http.StatusGone:
			gone[i] = true
			return nil
		case http.StatusForbidden:
			if !r.throttled() {
				skipped++
				return nil
			}
		}
		return m.partRefusal(ctx, s, r)
	})
	if err != nil {
		return contracts.MailboxEventsRead{}, err
	}
	if skipped > 0 {
		m.c.logger.DebugContext(ctx, "google: an events read skipped events it could not read", "skipped", skipped)
	}
	out := contracts.MailboxEventsRead{Events: []contracts.MailboxEventState{}, Gone: []string{}}
	for i, st := range states {
		switch {
		case st != nil:
			out.Events = append(out.Events, *st)
		case gone[i]:
			out.Gone = append(out.Gone, eventIDs[i])
		}
	}
	return out, nil
}
