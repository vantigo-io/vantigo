package google

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/google/store"
)

// This file is the mailbox calendar's change notifications (Google Workspace
// design G32): one push channel per synced person on their primary calendar's
// events (events.watch), whose messages Google posts to the module's
// anonymous endpoint (mailbox_notifications_endpoint.go), which only marks the
// channel notified. A message is a nudge; the calendar's poll stands, since
// Google's push is not 100 % reliable.
//
// The provider offers notifications only while
// CALENDAR_MAILBOX_NOTIFICATIONS_ENABLED is on and PUBLIC_BASE_URL is set
// (notificationsURL); otherwise Watch answers nil and the poll alone runs.
// Watch checks the status and the account as every call, then reads the row:
// no network while a channel made for the present address has more than
// min(3 days, half its granted life) left (Google does not document the life
// it grants, U5); else a new channel — its token 32 random bytes, the row
// keeping its SHA-256 alone — and the old one stopped. Unwatch stops the
// channel. Every call to Google is on the person's own token, and no
// transaction is held across one. Google's answers are read by their status
// and reason; none is logged.

// The channels' figures (G32).
const (
	// calendarNotificationsPath is the endpoint's path after PUBLIC_BASE_URL.
	calendarNotificationsPath = "/api/v1/google/calendar-notifications"
	// channelTTL is the life a channel asks for: Google grants what it will
	// (U5), the expiry it answers is kept.
	channelTTL = 30 * 24 * time.Hour
	// channelRenewWithin is the most a live channel may have left and be kept
	// as it is: renewed when less than min(this, half its granted life)
	// remains.
	channelRenewWithin = 3 * 24 * time.Hour
)

// notificationsURL is the endpoint's address, PUBLIC_BASE_URL +
// calendarNotificationsPath, and false when the provider offers no
// notifications: CALENDAR_MAILBOX_NOTIFICATIONS_ENABLED off, PUBLIC_BASE_URL
// unset, or no configuration at hand.
func (m *mailboxCalendar) notificationsURL() (string, bool) {
	cfg := m.deps.Config
	if cfg == nil || !cfg.CalendarMailboxNotificationsEnabled || cfg.PublicBaseURL == "" {
		return "", false
	}
	return cfg.PublicBaseURL + calendarNotificationsPath, true
}

// channelAnswer is a channel as Google answers events.watch, in the fields
// the provider reads: the id it was given, the resource's id, and the expiry
// in milliseconds since the epoch, as a string.
type channelAnswer struct {
	ID         string `json:"id"`
	ResourceID string `json:"resourceId"`
	Expiration string `json:"expiration"`
}

// renewAt is when a channel that expires at expires, granted granted
// seconds, is renewed: min(channelRenewWithin, half its life) before it
// expires.
func renewAt(expires time.Time, granted int32) time.Time {
	within := min(channelRenewWithin, time.Duration(granted)*time.Second/2)
	return expires.Add(-within)
}

// Watch makes sure the mailbox's changes are notified (G32):
//
//   - the status and the account checked first, as every call: refused under
//     cleanup with ErrMailboxUnavailable, ErrMailboxUnknown for an account the
//     module does not manage;
//   - nil, with no network, when the provider offers no notifications, and
//     while a channel made for the present address is held and not yet due
//     for renewal (renewAt);
//   - otherwise a new channel, events.watch on the primary calendar with a
//     fresh id and token, recorded with the expiry and the life Google
//     granted — replacing the row — and then the old channel, if any,
//     stopped: a refusal there is logged and forgotten, as the channel lapses
//     on its own and its messages name no row.
//
// Every call is on the person's token: a connection that cannot serve it
// answers contracts.ErrConnectionNeeded and leaves the row as it is. A 403
// refusing the calendar is ErrMailboxDenied, and Google having no primary
// calendar for the account (404) ErrMailboxUnknown.
func (m *mailboxCalendar) Watch(ctx context.Context, mailbox string) error {
	s, err := m.checked(ctx, mailbox, false)
	if err != nil {
		return err
	}
	target, ok := m.notificationsURL()
	if !ok {
		return nil
	}
	now := m.c.now()
	var old *store.GoogleCalendarChannel
	row, err := m.q.GetChannel(ctx, s.gid)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return fmt.Errorf("google: read the mailbox's channel: %w", err)
	default:
		if row.Address == target && now.Before(renewAt(row.ExpiresAt, row.GrantedSeconds)) {
			return nil
		}
		old = &row
	}
	m.withToken(&s, false)

	secret := make([]byte, channelTokenBytes)
	_, _ = rand.Read(secret) // crypto/rand.Read never returns an error
	token := base64.RawURLEncoding.EncodeToString(secret)
	hash := sha256.Sum256([]byte(token))
	id := uuid.New()
	body := map[string]any{
		"id":      id.String(),
		"type":    "web_hook",
		"address": target,
		"token":   token,
		"params":  map[string]string{"ttl": strconv.Itoa(int(channelTTL / time.Second))},
	}
	var answer channelAnswer
	// Sent once: a 5xx may have opened the channel, which lapses on its own
	// and whose messages name no row.
	if err := m.send(ctx, s, http.MethodPost, s.withQuota(eventsPath("")+"/watch", nil), body, &answer, noRetry()); err != nil {
		return m.primaryRefused(ctx, s, err)
	}
	if answer.ID != id.String() || answer.ResourceID == "" || len(answer.ResourceID) > maxResourceID {
		return fmt.Errorf("%w: a channel opened with another id or no resource", errGoogleUnreachable)
	}
	expires := now.Add(channelTTL)
	if ms, err := strconv.ParseInt(answer.Expiration, 10, 64); err == nil {
		if at := time.UnixMilli(ms).UTC(); at.After(now) {
			expires = at
		}
	}
	granted := int32(min(expires.Sub(now)/time.Second, time.Duration(1<<31-1)))
	if err := m.q.UpsertChannel(ctx, store.UpsertChannelParams{
		GoogleUserID: s.gid, ChannelID: id, ResourceID: answer.ResourceID, TokenHash: hash[:], Address: target,
		ExpiresAt: expires, GrantedSeconds: granted, Now: now,
	}); err != nil {
		return fmt.Errorf("google: record the mailbox's channel: %w", err)
	}
	if old != nil && old.ExpiresAt.After(now) {
		if err := m.stop(ctx, s, old.ChannelID, old.ResourceID); err != nil {
			m.c.logger.InfoContext(ctx, "google: an old calendar channel could not be stopped; it lapses at its expiry")
		}
	}
	return nil
}

// stop is channels.stop of the channel id on resource: 2xx and 404 are done.
func (m *mailboxCalendar) stop(ctx context.Context, s mailboxSession, id uuid.UUID, resource string) error {
	err := m.send(ctx, s, http.MethodPost, s.withQuota(calendarPathPrefix+"/channels/stop", nil),
		map[string]string{"id": id.String(), "resourceId": resource}, nil)
	if err == nil || itemMissing(err) {
		return nil
	}
	return m.refused(ctx, s, err)
}

// Unwatch stops the mailbox's channel (G32). No row — the mailbox never
// watched, or forgotten already — answers nil with no network, and so does a
// row past its expiry, forgotten first, whatever the status. Then the
// status: allowed under cleanup, ErrMailboxUnavailable otherwise. The account
// is not checked: the row is the module's own, made by a Watch that checked
// it. channels.stop on the person's token, asked for as a removal: 2xx or 404
// forget the row, and so does a connection holding no token for it (closing
// with none, or gone) — nothing could stop it, and it lapses at its expiry
// with its messages naming no row; anything else keeps the row and answers
// the error — the stop stays owed.
func (m *mailboxCalendar) Unwatch(ctx context.Context, mailbox string) error {
	if !validUserID(mailbox) {
		return fmt.Errorf("google: a mailbox that is no Google id: %w", contracts.ErrMailboxUnknown)
	}
	row, err := m.q.GetChannel(ctx, mailbox)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil
	case err != nil:
		return fmt.Errorf("google: read the mailbox's channel: %w", err)
	}
	forget := func() error {
		if _, err := m.q.DeleteChannel(ctx, store.DeleteChannelParams{GoogleUserID: mailbox, ChannelID: row.ChannelID}); err != nil {
			return fmt.Errorf("google: forget the mailbox's channel: %w", err)
		}
		return nil
	}
	if !row.ExpiresAt.After(m.c.now()) {
		return forget()
	}
	conn, st, err := m.state(ctx)
	if err != nil {
		return err
	}
	if st.State != contracts.MailboxAvailable && st.State != contracts.MailboxCleanup {
		return fmt.Errorf("google: the mailbox provider is %s (%s): %w", st.State, st.Reason, contracts.ErrMailboxUnavailable)
	}
	s := mailboxSession{gid: mailbox, marker: conn.Marker, cleanup: st.State == contracts.MailboxCleanup}
	m.withToken(&s, true)
	err = m.stop(ctx, s, row.ChannelID, row.ResourceID)
	switch {
	case err == nil:
		return forget()
	case errors.Is(err, errNoRemovalToken):
		m.c.logger.InfoContext(ctx, "google: a calendar channel is forgotten; no Google connection holds a token to stop it")
		return forget()
	}
	return err
}

// Notified answers the mailboxes notified after since, oldest first, at most
// contracts.MailboxNoticesMax (G32): the rows and no network. A mailbox is
// the account's Google id, as the other calls take it.
func (m *mailboxCalendar) Notified(ctx context.Context, since time.Time) ([]contracts.MailboxNotice, error) {
	rows, err := m.q.ListChannelsNotified(ctx, store.ListChannelsNotifiedParams{Since: since, RowLimit: contracts.MailboxNoticesMax})
	if err != nil {
		return nil, fmt.Errorf("google: read the notified mailboxes: %w", err)
	}
	out := make([]contracts.MailboxNotice, 0, len(rows))
	for _, r := range rows {
		out = append(out, contracts.MailboxNotice{Mailbox: r.GoogleUserID, At: r.NotifiedAt})
	}
	return out, nil
}

// Connections answers the people's connections that moved after since,
// oldest first, at most contracts.ConnectionChangesMax (G30): the rows and no
// network, whatever the switch (connectionChanges).
func (m *mailboxCalendar) Connections(ctx context.Context, since time.Time) ([]contracts.ConnectionChange, error) {
	return connectionChanges(ctx, m.q, since)
}
