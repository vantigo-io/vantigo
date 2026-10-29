package entra

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/entra/store"
)

// This file is the module's change notifications for the mailbox calendar
// (calendar Outlook sync design O11.2–O11.4; delivery D's plan reading 9):
// one Graph subscription per synced mailbox, on /users/{oid}/events, its
// notifications posted to the module's anonymous webhook
// (mailbox_notifications_endpoint.go), which only marks the row notified.
//
// The provider offers notifications only while
// CALENDAR_MAILBOX_NOTIFICATIONS_ENABLED is on and PUBLIC_BASE_URL is set
// (notificationsURL); otherwise Watch answers nil and the calendar's poll
// alone runs. Watch checks the status and the account as every call, then
// reads the row and the connection — no network while a live subscription
// has more than three days left, nor while a create's handshake failed
// within the hour — and renews (PATCH) or creates (POST /subscriptions).
// A create's clientState is 32 random bytes sent once and never stored: the
// row keeps its SHA-256, written before the POST with no subscription id —
// a notification racing the 201 names an id no row holds yet and is ignored
// like any unknown one; the person's next poll, and the next notification,
// cover the change it carried. The handshake's window on the connection row
// is opened for 30 seconds before the POST and closed after it. The row
// keeps the webhook's address the create gave Graph: a Watch after
// PUBLIC_BASE_URL moved deletes that subscription and creates one at the
// new address, never renewing one whose notifications go elsewhere. Unwatch
// deletes the subscription, keeping the row 'deleting' while Graph refuses.
// Notified reads the rows and nothing else. No transaction and no pool
// connection is held across a call to Graph: each query takes the pool and
// gives it back. Graph's answers are read by their status and codes; none
// is logged.

// The subscriptions' figures (design O11.2, O11.3, O11.7).
const (
	// mailboxNotificationsPath is the webhook's path after PUBLIC_BASE_URL.
	mailboxNotificationsPath = "/api/v1/entra/mailbox-notifications"
	// subscriptionLifetime is the expiry a create or a renewal asks for:
	// under Outlook's 10 080 minutes for a subscription without resource
	// data.
	subscriptionLifetime = 10000 * time.Minute
	// subscriptionRenewWithin is how close to its expiry a live
	// subscription is renewed.
	subscriptionRenewWithin = 3 * 24 * time.Hour
	// subscriptionValidationWindow is how long the webhook answers Graph's
	// handshake after a create opens the window.
	subscriptionValidationWindow = 30 * time.Second
	// notificationsUnreachableHold is how long a failed handshake holds
	// every create off.
	notificationsUnreachableHold = time.Hour
	// subscriptionListPages is the most pages of GET /subscriptions a
	// conflict's clean-up reads.
	subscriptionListPages = 10
	// subscriptionChangeTypes are the changes a subscription asks for.
	subscriptionChangeTypes = "created,updated,deleted"
	// clientStateBytes is the clientState's randomness: 32 bytes, 43
	// characters of base64url without padding.
	clientStateBytes = 32
	// subscriptionConflictHold is how long a mailbox whose events another
	// installation's subscription holds — the application shared, Graph
	// answering 409 with none of this installation's to clear — is not
	// tried again.
	subscriptionConflictHold = 24 * time.Hour
)

// The states of a subscription's row (O11.4).
const (
	subscriptionActive      = "active"
	subscriptionReauthorize = "reauthorize"
	subscriptionRemoved     = "removed"
	subscriptionDeleting    = "deleting"
	subscriptionConflict    = "conflict"
)

// notificationsURL is the webhook's address, PUBLIC_BASE_URL +
// mailboxNotificationsPath, and false when the provider offers no
// notifications: CALENDAR_MAILBOX_NOTIFICATIONS_ENABLED off, PUBLIC_BASE_URL
// unset, or no configuration at hand.
func (m *mailboxCalendar) notificationsURL() (string, bool) {
	cfg := m.deps.Config
	if cfg == nil || !cfg.CalendarMailboxNotificationsEnabled || cfg.PublicBaseURL == "" {
		return "", false
	}
	return cfg.PublicBaseURL + mailboxNotificationsPath, true
}

// subscriptionID parses a subscription's id strictly: a GUID in its
// canonical 36 characters, answered in its lower-case form, which is how
// the rows hold it.
func subscriptionID(s string) (uuid.UUID, bool) {
	if len(s) != 36 {
		return uuid.UUID{}, false
	}
	id, err := uuid.Parse(s)
	return id, err == nil && id != uuid.Nil
}

// subscriptionPath is /subscriptions/{id}.
func subscriptionPath(id uuid.UUID) string {
	return "/subscriptions/" + id.String()
}

// graphSubscription is a subscription as Graph answers it, in the fields
// the provider reads.
type graphSubscription struct {
	ID              string `json:"id"`
	Resource        string `json:"resource"`
	NotificationURL string `json:"notificationUrl"`
	Expiration      string `json:"expirationDateTime"`
}

// expiryOf is the expiry Graph answered, else asked when it does not read
// or is not after now.
func expiryOf(answered string, asked, now time.Time) time.Time {
	t, err := time.Parse(time.RFC3339Nano, answered)
	if err != nil || !t.After(now) {
		return asked
	}
	return t.UTC()
}

// readSubscription is the mailbox's row, nil when it has none.
func readSubscription(ctx context.Context, q *store.Queries, oid uuid.UUID) (*store.EntraMailboxSubscription, error) {
	row, err := q.GetMailboxSubscription(ctx, oid)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("entra: read the mailbox's subscription: %w", err)
	}
	return &row, nil
}

// Watch makes sure the mailbox's changes are notified (design O11.3):
//
//   - the status and the account checked first, as every call: refused
//     under cleanup with ErrMailboxUnavailable, ErrMailboxUnknown for a
//     mailbox the module does not manage;
//   - nil, with no network, when the provider offers no notifications
//     (notificationsURL), while a live subscription — an id, active, more
//     than three days left, made for the webhook's present address — is
//     held, while a create's handshake failed within the last hour (O11.7),
//     and while another installation's subscription held the mailbox
//     within the last day (the row 'conflict', subscriptionConflictHold);
//   - a renewal, PATCH /subscriptions/{id} with a new expiry, when the
//     subscription is live with three days or less left, or marked
//     reauthorize; a 404 there forgets the row and creates;
//   - a create otherwise: no row, one removed or deleting, one whose create
//     never answered, past its expiry, or made for another address — that
//     one deleted first (DELETE /subscriptions/{id}, a 404 done; any other
//     refusal answered, the row kept for the next Watch), so the old
//     address is never notified again.
//
// A 403 is ErrMailboxDenied, a mailbox gone ErrMailboxGone (mailboxRefusal).
func (m *mailboxCalendar) Watch(ctx context.Context, mailbox string) error {
	s, err := m.checked(ctx, mailbox, false)
	if err != nil {
		return err
	}
	target, ok := m.notificationsURL()
	if !ok {
		return nil
	}
	now := m.client.now()
	row, err := readSubscription(ctx, s.q, s.oid)
	if err != nil {
		return err
	}
	if row != nil && row.State == subscriptionConflict && now.Sub(row.UpdatedAt) < subscriptionConflictHold {
		return nil
	}
	held := row != nil && row.SubscriptionID != nil && row.ExpiresAt != nil && row.ExpiresAt.After(now)
	moved := held && (row.NotificationUrl == nil || *row.NotificationUrl != target)
	live := held && !moved && (row.State == subscriptionActive || row.State == subscriptionReauthorize)
	if live && row.State == subscriptionActive && row.ExpiresAt.After(now.Add(subscriptionRenewWithin)) {
		return nil
	}
	conn, err := s.q.GetConnection(ctx)
	if err != nil {
		return fmt.Errorf("entra: read the connection: %w", err)
	}
	if conn.NotificationsUnreachableAt != nil && now.Sub(*conn.NotificationsUnreachableAt) < notificationsUnreachableHold {
		return nil
	}
	if err := m.withCredential(ctx, &s); err != nil {
		return err
	}
	if moved && row.State != subscriptionRemoved {
		sid, ok := subscriptionID(*row.SubscriptionID)
		if !ok {
			return fmt.Errorf("%w: a stored subscription id that is no GUID", errTenantUnreachable)
		}
		if err := m.client.del(ctx, s.cred, subscriptionPath(sid), noWords); err != nil && !isStatus(err, http.StatusNotFound) {
			return mailboxRefusal(err)
		}
	}
	if live {
		renewed, err := m.renew(ctx, s, *row.SubscriptionID, now)
		if err != nil || renewed {
			return err
		}
	}
	return m.create(ctx, s, target, conn.NotificationsUnreachableAt != nil, true)
}

// renew is the renewal of the subscription id: PATCH of its expiry to now +
// subscriptionLifetime, which reauthorizes it too, and the row active with
// the expiry Graph answered — unless the row no longer holds id, or a
// lifecycle notification marked it removed meanwhile: left as it is, for
// the next Watch. false with no error when Graph no longer holds
// it (404): the row is forgotten, for the caller to create.
func (m *mailboxCalendar) renew(ctx context.Context, s mailboxSession, id string, now time.Time) (bool, error) {
	sid, ok := subscriptionID(id)
	if !ok {
		return false, fmt.Errorf("%w: a stored subscription id that is no GUID", errTenantUnreachable)
	}
	asked := now.Add(subscriptionLifetime).UTC()
	var answer graphSubscription
	err := m.client.do(ctx, s.cred, http.MethodPatch, subscriptionPath(sid),
		map[string]string{"expirationDateTime": asked.Format(time.RFC3339)}, &answer, noWords)
	switch {
	case isStatus(err, http.StatusNotFound):
		if err := s.q.DeleteMailboxSubscription(ctx, s.oid); err != nil {
			return false, fmt.Errorf("entra: forget the mailbox's subscription: %w", err)
		}
		return false, nil
	case err != nil:
		return false, mailboxRefusal(err)
	}
	if err := s.q.RenewMailboxSubscription(ctx, store.RenewMailboxSubscriptionParams{
		ExpiresAt: expiryOf(answer.Expiration, asked, now), Now: m.client.now(), ObjectID: s.oid, SubscriptionID: sid.String(),
	}); err != nil {
		return false, fmt.Errorf("entra: record the subscription's renewal: %w", err)
	}
	return true, nil
}

// create subscribes to the mailbox's events (design O11.2): a fresh
// clientState, its hash and target upserted on the row with no id before
// the POST, the
// handshake's window opened for subscriptionValidationWindow and closed
// after; 201 stores the id — a GUID, strictly — and the expiry, and clears
// the connection's unreachable mark when unreachable says one was set. A 400
// whose words say the handshake failed (graphError.Validation) marks the
// connection unreachable, forgets the row and answers nil: Microsoft could
// not reach the webhook, and no create is tried for an hour. A 409 — Graph
// holds a subscription of this application on the resource, its row lost —
// clears this installation's (clearConflict) and, when retry, creates once
// more; when clearConflict read the whole list and found none of this
// installation's, the
// subscription is another installation's sharing the application, and the
// row is marked 'conflict' and nil answered: no create is tried on that
// mailbox for subscriptionConflictHold, and the poll alone runs for it.
// Anything else through mailboxRefusal, the row kept for the next
// Watch.
func (m *mailboxCalendar) create(ctx context.Context, s mailboxSession, target string, unreachable, retry bool) error {
	secret := make([]byte, clientStateBytes)
	if _, err := rand.Read(secret); err != nil {
		return fmt.Errorf("entra: draw a clientState: %w", err)
	}
	clientState := base64.RawURLEncoding.EncodeToString(secret)
	hash := sha256.Sum256([]byte(clientState))
	now := m.client.now()
	if _, err := s.q.UpsertMailboxSubscription(ctx, store.UpsertMailboxSubscriptionParams{
		ObjectID: s.oid, ClientStateHash: hash[:], NotificationUrl: target, Now: now,
	}); err != nil {
		return fmt.Errorf("entra: record the subscription's create: %w", err)
	}
	asked := now.Add(subscriptionLifetime).UTC()
	body := map[string]string{
		"changeType":               subscriptionChangeTypes,
		"notificationUrl":          target,
		"lifecycleNotificationUrl": target,
		"resource":                 userPath(s.oid, "/events"),
		"expirationDateTime":       asked.Format(time.RFC3339),
		"clientState":              clientState,
	}
	var answer graphSubscription
	err := m.withValidationWindow(ctx, s.q, now, func() error {
		// noRetry: a 5xx may have made the subscription, which the next
		// create's 409 clears; and the body carries the clientState.
		return m.client.post(ctx, s.cred, "/subscriptions", body, &answer, noRetry)
	})
	g, refused := asGraphError(err)
	switch {
	case err == nil:
	case refused && g.Status == http.StatusBadRequest && g.Validation && !mailboxGone(g):
		m.client.logger.WarnContext(ctx, "entra: Microsoft could not reach the change notifications' webhook; no subscription is made for an hour",
			"tenant", s.cred.tenantID.String())
		if err := s.q.SetNotificationsUnreachable(ctx, &now); err != nil {
			return fmt.Errorf("entra: record the webhook unreachable: %w", err)
		}
		if err := s.q.DeleteMailboxSubscription(ctx, s.oid); err != nil {
			return fmt.Errorf("entra: forget the mailbox's subscription: %w", err)
		}
		return nil
	case refused && g.Status == http.StatusConflict && retry:
		foreign, err := m.clearConflict(ctx, s, target)
		if err != nil {
			return err
		}
		if foreign {
			m.client.logger.WarnContext(ctx, "entra: another installation's subscription holds a mailbox's changes; it is not watched for a day",
				"tenant", s.cred.tenantID.String())
			if err := s.q.SetMailboxSubscriptionState(ctx, store.SetMailboxSubscriptionStateParams{
				State: subscriptionConflict, Now: m.client.now(), ObjectID: s.oid,
			}); err != nil {
				return fmt.Errorf("entra: record the subscription's conflict: %w", err)
			}
			return nil
		}
		return m.create(ctx, s, target, unreachable, false)
	default:
		return mailboxRefusal(err)
	}
	sid, ok := subscriptionID(answer.ID)
	if !ok {
		return fmt.Errorf("%w: a subscription made with an id that is no GUID", errTenantUnreachable)
	}
	id := sid.String()
	expires := expiryOf(answer.Expiration, asked, now)
	if _, err := s.q.UpsertMailboxSubscription(ctx, store.UpsertMailboxSubscriptionParams{
		ObjectID: s.oid, SubscriptionID: &id, ClientStateHash: hash[:], NotificationUrl: target, ExpiresAt: &expires, Now: m.client.now(),
	}); err != nil {
		return fmt.Errorf("entra: record the subscription: %w", err)
	}
	if unreachable {
		if err := s.q.SetNotificationsUnreachable(ctx, nil); err != nil {
			return fmt.Errorf("entra: clear the webhook unreachable: %w", err)
		}
	}
	return nil
}

// withValidationWindow runs post with the handshake's window open: until
// now + subscriptionValidationWindow on the connection row before, closed
// after — whatever post answered, on a context the caller's ending does not
// cancel — unless a create started since opened its own.
func (m *mailboxCalendar) withValidationWindow(ctx context.Context, q *store.Queries, now time.Time, post func() error) error {
	until := now.Add(subscriptionValidationWindow).Truncate(time.Microsecond)
	if err := q.SetNotificationsValidating(ctx, &until); err != nil {
		return fmt.Errorf("entra: open the handshake's window: %w", err)
	}
	err := post()
	closing, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if cerr := q.CloseNotificationsValidating(closing, until); cerr != nil && err == nil {
		return fmt.Errorf("entra: close the handshake's window: %w", cerr)
	}
	return err
}

// clearConflict deletes this installation's subscriptions on the mailbox's
// events, which Graph holds though no row names them: GET /subscriptions —
// every subscription of the application — in at most subscriptionListPages
// pages, a next link followed only when it is a page of that very list
// (listPath); a subscription is deleted when its resource names this
// mailbox's events, whatever its case and its leading slash, and its
// notificationUrl is target exactly — another installation's sharing the
// application is left alone. A delete answered 404 is done. foreign is true
// when the whole list was read and none of this installation's was on the
// mailbox: the conflict is another installation's.
func (m *mailboxCalendar) clearConflict(ctx context.Context, s mailboxSession, target string) (foreign bool, err error) {
	resource := strings.ToLower(strings.TrimPrefix(userPath(s.oid, "/events"), "/"))
	var ours []uuid.UUID
	path := "/subscriptions"
	for page := 1; path != "" && page <= subscriptionListPages; page++ {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		var answer struct {
			Value    []graphSubscription `json:"value"`
			NextLink string              `json:"@odata.nextLink"`
		}
		if err := m.client.get(ctx, s.cred, path, &answer, noWords); err != nil {
			return false, mailboxRefusal(err)
		}
		for _, sub := range answer.Value {
			if strings.ToLower(strings.TrimPrefix(sub.Resource, "/")) != resource || sub.NotificationURL != target {
				continue
			}
			if id, ok := subscriptionID(sub.ID); ok {
				ours = append(ours, id)
			}
		}
		path = ""
		if answer.NextLink != "" {
			next, ok := listPath(answer.NextLink, "/subscriptions")
			if !ok {
				return false, fmt.Errorf("%w: a next page off the subscriptions' list", errTenantUnreachable)
			}
			path = next
		}
	}
	for _, id := range ours {
		if err := m.client.del(ctx, s.cred, subscriptionPath(id), noWords); err != nil && !isStatus(err, http.StatusNotFound) {
			return false, mailboxRefusal(err)
		}
	}
	return path == "" && len(ours) == 0, nil
}

// Unwatch deletes the mailbox's subscription (design O11.3). No row — the
// mailbox never watched, or forgotten already — answers nil with no
// network, and so does a row past its expiry (Graph deleted it) or one
// whose create never answered (no id to delete; Graph's, if it made one,
// lapses within seven days and its notifications name no row), each
// forgotten first, whatever the status. Then the status: allowed under
// cleanup, ErrMailboxUnavailable otherwise. The account is not checked: the
// row is the module's own, made by a Watch that checked it, and a deleted
// account's subscription is still the module's to remove. DELETE
// /subscriptions/{id}: 204 or 404 forget the row; anything else marks it
// deleting and answers the error, through mailboxRefusal — the delete stays
// owed.
func (m *mailboxCalendar) Unwatch(ctx context.Context, mailbox string) error {
	oid, ok := objectID(mailbox)
	if !ok {
		return fmt.Errorf("entra: a mailbox that is no object id: %w", contracts.ErrMailboxUnknown)
	}
	q := store.New(m.deps.Pool)
	row, err := readSubscription(ctx, q, oid)
	if err != nil || row == nil {
		return err
	}
	now := m.client.now()
	forget := func() error {
		if err := q.DeleteMailboxSubscription(ctx, oid); err != nil {
			return fmt.Errorf("entra: forget the mailbox's subscription: %w", err)
		}
		return nil
	}
	if row.SubscriptionID == nil || (row.ExpiresAt != nil && !row.ExpiresAt.After(now)) {
		return forget()
	}
	sid, ok := subscriptionID(*row.SubscriptionID)
	if !ok {
		return fmt.Errorf("%w: a stored subscription id that is no GUID", errTenantUnreachable)
	}
	st, err := m.Status(ctx)
	if err != nil {
		return err
	}
	if st.State != contracts.MailboxAvailable && st.State != contracts.MailboxCleanup {
		return fmt.Errorf("entra: the mailbox provider is %s (%s): %w", st.State, st.Reason, contracts.ErrMailboxUnavailable)
	}
	s := mailboxSession{q: q, oid: oid, cleanup: st.State == contracts.MailboxCleanup}
	if err := m.withCredential(ctx, &s); err != nil {
		return err
	}
	err = m.client.del(ctx, s.cred, subscriptionPath(sid), noWords)
	if err == nil || isStatus(err, http.StatusNotFound) {
		return forget()
	}
	if serr := q.SetMailboxSubscriptionState(ctx, store.SetMailboxSubscriptionStateParams{
		State: subscriptionDeleting, Now: m.client.now(), ObjectID: oid,
	}); serr != nil {
		return fmt.Errorf("entra: record the subscription's delete owed: %w", serr)
	}
	return mailboxRefusal(err)
}

// Notified answers the mailboxes notified after since, oldest first, at most
// contracts.MailboxNoticesMax (design O11.3): the rows and no network. A
// mailbox is its object id in lower case, as the other calls take it.
func (m *mailboxCalendar) Notified(ctx context.Context, since time.Time) ([]contracts.MailboxNotice, error) {
	rows, err := store.New(m.deps.Pool).ListMailboxNotified(ctx, store.ListMailboxNotifiedParams{
		Since: since, RowLimit: contracts.MailboxNoticesMax,
	})
	if err != nil {
		return nil, fmt.Errorf("entra: read the notified mailboxes: %w", err)
	}
	out := make([]contracts.MailboxNotice, 0, len(rows))
	for _, r := range rows {
		out = append(out, contracts.MailboxNotice{Mailbox: r.ObjectID.String(), At: r.NotifiedAt})
	}
	return out, nil
}

// Connections answers the people's connections that moved after since,
// oldest first, at most contracts.ConnectionChangesMax (Outlook connection
// design M9): the rows and no network, whatever the switch. A connect, a
// close and a reconnect's refusal move changed_at; a refresh, a forgotten
// token and the bookkeeping do not. A mailbox is its object id in lower
// case, as the other calls take it.
func (m *mailboxCalendar) Connections(ctx context.Context, since time.Time) ([]contracts.ConnectionChange, error) {
	rows, err := store.New(m.deps.Pool).ListOutlookConnectionChanges(ctx, store.ListOutlookConnectionChangesParams{
		Since: since, RowLimit: contracts.ConnectionChangesMax,
	})
	if err != nil {
		return nil, fmt.Errorf("entra: read the changed Outlook connections: %w", err)
	}
	out := make([]contracts.ConnectionChange, 0, len(rows))
	for _, r := range rows {
		out = append(out, contracts.ConnectionChange{Mailbox: r.ObjectID.String(), State: r.State, At: r.ChangedAt})
	}
	return out, nil
}
