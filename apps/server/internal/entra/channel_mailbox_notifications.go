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

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/entra/store"
)

// This file is the channel mailboxes' change notifications (Outlook connection
// and Microsoft 365 mail design M11, M14, M17; delivery E): one Graph
// subscription per watched channel mailbox on
// /users/{oid}/mailFolders('inbox')/messages with changeType "created",
// created, renewed and deleted with the application's own token and sharing
// entra.mailbox_subscriptions (resource = 'messages') and the anonymous
// webhook (/api/v1/entra/mailbox-notifications) with calendar events.

const mailSubscriptionChangeType = "created"

// mailSubscriptionResource is the Graph resource path for a channel mailbox's
// Inbox messages subscription.
func mailSubscriptionResource(oid uuid.UUID) string {
	return userPath(oid, "/mailFolders('inbox')/messages")
}

// Watch makes sure mailbox's folder ("inbox") has a live Graph subscription
// for new messages. It is a no-op when notifications are disabled or
// PUBLIC_BASE_URL is unset, while a live subscription has more than three days
// left for the current webhook URL, while a handshake failed within the last
// hour, or while another installation holds a conflicting subscription on the
// mailbox's Inbox within the last day.
func (m *channelMailbox) Watch(ctx context.Context, mailbox, folder string) error {
	oid, err := m.recorded(ctx, mailbox)
	if err != nil {
		return err
	}
	if !strings.EqualFold(folder, "inbox") {
		return errors.New("entra: only the inbox may be watched")
	}
	cred, err := m.ready(ctx)
	if err != nil {
		return err
	}
	target, ok := notificationsURLOf(m.deps)
	if !ok {
		return nil
	}
	q := store.New(m.deps.Pool)
	now := m.client.now()
	row, err := readSubscription(ctx, q, oid, subscriptionResourceMessages)
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
	conn, err := q.GetConnection(ctx)
	if err != nil {
		return fmt.Errorf("entra: read the connection: %w", err)
	}
	if conn.NotificationsUnreachableAt != nil && now.Sub(*conn.NotificationsUnreachableAt) < notificationsUnreachableHold {
		return nil
	}
	if moved && row.State != subscriptionRemoved {
		sid, ok := subscriptionID(*row.SubscriptionID)
		if !ok {
			return fmt.Errorf("%w: a stored subscription id that is no GUID", errTenantUnreachable)
		}
		if err := m.client.del(ctx, cred, subscriptionPath(sid), noWords); err != nil && !isStatus(err, http.StatusNotFound) {
			return channelRefusal(err)
		}
	}
	if live {
		renewed, err := m.renewMailSubscription(ctx, q, cred, oid, *row.SubscriptionID, now)
		if err != nil || renewed {
			return err
		}
	}
	return m.createMailSubscription(ctx, q, cred, oid, target, conn.NotificationsUnreachableAt != nil, true)
}

func (m *channelMailbox) renewMailSubscription(ctx context.Context, q *store.Queries, cred graphCredential, oid uuid.UUID, id string, now time.Time) (bool, error) {
	sid, ok := subscriptionID(id)
	if !ok {
		return false, fmt.Errorf("%w: a stored subscription id that is no GUID", errTenantUnreachable)
	}
	asked := now.Add(subscriptionLifetime).UTC()
	var answer graphSubscription
	err := m.client.do(ctx, cred, http.MethodPatch, subscriptionPath(sid),
		map[string]string{"expirationDateTime": asked.Format(time.RFC3339)}, &answer, noWords)
	switch {
	case isStatus(err, http.StatusNotFound), isStatus(err, http.StatusForbidden):
		if err := q.DeleteMailboxSubscription(ctx, store.DeleteMailboxSubscriptionParams{
			ObjectID: oid, Resource: subscriptionResourceMessages,
		}); err != nil {
			return false, fmt.Errorf("entra: forget the mailbox's subscription: %w", err)
		}
		return false, nil
	case err != nil:
		return false, channelRefusal(err)
	}
	if err := q.RenewMailboxSubscription(ctx, store.RenewMailboxSubscriptionParams{
		ExpiresAt:      expiryOf(answer.Expiration, asked, now),
		Now:            m.client.now(),
		ObjectID:       oid,
		SubscriptionID: sid.String(),
	}); err != nil {
		return false, fmt.Errorf("entra: record the subscription's renewal: %w", err)
	}
	return true, nil
}

func (m *channelMailbox) createMailSubscription(ctx context.Context, q *store.Queries, cred graphCredential, oid uuid.UUID, target string, unreachable, retry bool) error {
	secret := make([]byte, clientStateBytes)
	if _, err := rand.Read(secret); err != nil {
		return fmt.Errorf("entra: draw a clientState: %w", err)
	}
	clientState := base64.RawURLEncoding.EncodeToString(secret)
	hash := sha256.Sum256([]byte(clientState))
	now := m.client.now()
	if _, err := q.UpsertMailboxSubscription(ctx, store.UpsertMailboxSubscriptionParams{
		ObjectID:        oid,
		Resource:        subscriptionResourceMessages,
		ClientStateHash: hash[:],
		NotificationUrl: target,
		Now:             now,
	}); err != nil {
		return fmt.Errorf("entra: record the subscription's create: %w", err)
	}
	asked := now.Add(subscriptionLifetime).UTC()
	body := map[string]string{
		"changeType":               mailSubscriptionChangeType,
		"notificationUrl":          target,
		"lifecycleNotificationUrl": target,
		"resource":                 mailSubscriptionResource(oid),
		"expirationDateTime":       asked.Format(time.RFC3339),
		"clientState":              clientState,
	}
	var answer graphSubscription
	err := withValidationWindow(ctx, q, now, func() error {
		return m.client.post(ctx, cred, "/subscriptions", body, &answer, noRetry)
	})
	g, refused := asGraphError(err)
	switch {
	case err == nil:
	case refused && g.Status == http.StatusBadRequest && g.Validation && !mailboxGone(g):
		m.client.logger.WarnContext(ctx, "entra: Microsoft could not reach the change notifications' webhook; no subscription is made for an hour",
			"tenant", cred.tenantID.String())
		if err := q.SetNotificationsUnreachable(ctx, &now); err != nil {
			return fmt.Errorf("entra: record the webhook unreachable: %w", err)
		}
		if err := q.DeleteMailboxSubscription(ctx, store.DeleteMailboxSubscriptionParams{
			ObjectID: oid, Resource: subscriptionResourceMessages,
		}); err != nil {
			return fmt.Errorf("entra: forget the mailbox's subscription: %w", err)
		}
		return nil
	case refused && g.Status == http.StatusConflict && retry:
		foreign, err := m.clearMailConflict(ctx, cred, oid, target)
		if err != nil {
			return err
		}
		if foreign {
			m.client.logger.WarnContext(ctx, "entra: another installation's subscription holds a channel mailbox's messages; it is not watched for a day",
				"tenant", cred.tenantID.String())
			if err := q.SetMailboxSubscriptionState(ctx, store.SetMailboxSubscriptionStateParams{
				State:    subscriptionConflict,
				Now:      m.client.now(),
				ObjectID: oid,
				Resource: subscriptionResourceMessages,
			}); err != nil {
				return fmt.Errorf("entra: record the subscription's conflict: %w", err)
			}
			return nil
		}
		return m.createMailSubscription(ctx, q, cred, oid, target, unreachable, false)
	default:
		return channelRefusal(err)
	}
	sid, ok := subscriptionID(answer.ID)
	if !ok {
		return fmt.Errorf("%w: a subscription made with an id that is no GUID", errTenantUnreachable)
	}
	id := sid.String()
	expires := expiryOf(answer.Expiration, asked, now)
	if _, err := q.UpsertMailboxSubscription(ctx, store.UpsertMailboxSubscriptionParams{
		ObjectID:        oid,
		Resource:        subscriptionResourceMessages,
		SubscriptionID:  &id,
		ClientStateHash: hash[:],
		NotificationUrl: target,
		ExpiresAt:       &expires,
		Now:             m.client.now(),
	}); err != nil {
		return fmt.Errorf("entra: record the subscription: %w", err)
	}
	if unreachable {
		if err := q.SetNotificationsUnreachable(ctx, nil); err != nil {
			return fmt.Errorf("entra: clear the webhook unreachable: %w", err)
		}
	}
	return nil
}

func (m *channelMailbox) clearMailConflict(ctx context.Context, cred graphCredential, oid uuid.UUID, target string) (foreign bool, err error) {
	resource := strings.ToLower(strings.TrimPrefix(mailSubscriptionResource(oid), "/"))
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
		if err := m.client.get(ctx, cred, path, &answer, noWords); err != nil {
			return false, channelRefusal(err)
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
		if err := m.client.del(ctx, cred, subscriptionPath(id), noWords); err != nil && !isStatus(err, http.StatusNotFound) {
			return false, channelRefusal(err)
		}
	}
	return path == "" && len(ours) == 0, nil
}

// Unwatch deletes mailbox's Inbox subscription. Idempotent: a mailbox not
// watched, or a subscription already expired or gone in Graph, answers nil.
func (m *channelMailbox) Unwatch(ctx context.Context, mailbox, folder string) error {
	oid, ok := objectID(mailbox)
	if !ok {
		return fmt.Errorf("entra: a mailbox that is no object id: %w", contracts.ErrMailboxUnknown)
	}
	if !strings.EqualFold(folder, "inbox") {
		return errors.New("entra: only the inbox may be unwatched")
	}
	q := store.New(m.deps.Pool)
	row, err := readSubscription(ctx, q, oid, subscriptionResourceMessages)
	if err != nil || row == nil {
		return err
	}
	now := m.client.now()
	forget := func() error {
		if err := q.DeleteMailboxSubscription(ctx, store.DeleteMailboxSubscriptionParams{
			ObjectID: oid, Resource: subscriptionResourceMessages,
		}); err != nil {
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
	cred, err := m.ready(ctx)
	if err != nil {
		return err
	}
	err = m.client.del(ctx, cred, subscriptionPath(sid), noWords)
	switch {
	case err == nil, isStatus(err, http.StatusNotFound), isStatus(err, http.StatusForbidden):
		return forget()
	}
	if serr := q.SetMailboxSubscriptionState(ctx, store.SetMailboxSubscriptionStateParams{
		State:    subscriptionDeleting,
		Now:      m.client.now(),
		ObjectID: oid,
		Resource: subscriptionResourceMessages,
	}); serr != nil {
		return fmt.Errorf("entra: record the subscription's delete owed: %w", serr)
	}
	return channelRefusal(err)
}

// Notified answers the channel mailboxes notified for mail after since, oldest
// first, at most contracts.MailboxNoticesMax: the provider's own rows, no
// network.
func (m *channelMailbox) Notified(ctx context.Context, since time.Time) ([]contracts.MailboxNotice, error) {
	rows, err := store.New(m.deps.Pool).ListMailboxNotified(ctx, store.ListMailboxNotifiedParams{
		Resource: subscriptionResourceMessages,
		Since:    since,
		RowLimit: contracts.MailboxNoticesMax,
	})
	if err != nil {
		return nil, fmt.Errorf("entra: read the notified channel mailboxes: %w", err)
	}
	out := make([]contracts.MailboxNotice, 0, len(rows))
	for _, r := range rows {
		out = append(out, contracts.MailboxNotice{Mailbox: r.ObjectID.String(), At: r.NotifiedAt})
	}
	return out, nil
}
