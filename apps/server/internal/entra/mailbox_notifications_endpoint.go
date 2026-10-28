package entra

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/vantigo-io/vantigo/server/internal/entra/gen"
	"github.com/vantigo-io/vantigo/server/internal/entra/store"
)

// This file is Microsoft Graph's webhook for the change notifications
// (calendar Outlook sync design O11.5; delivery D's plan reading 10),
// anonymous and on the public host's allowlist, served by the generated
// strict wrapper: it reads a JSON body through the router's 1 MiB cap into
// a free-form object — a body that does not decode is the wrapper's 400 —
// and answers the handshake as text/plain.
//
// A notification is a nudge: a valid item marks its subscription's row
// notified (and, for a lifecycle event, reauthorize or removed) and nothing
// it says is kept. Every body that decodes is answered 202 alike, whatever
// it named, so the webhook tells a caller nothing of which subscriptions
// exist; only a database that could not be written within two seconds is
// answered 503, which Graph retries. The logs carry counts alone — never an
// id, a clientState, a resource or a tenant.

// The webhook's figures (design O11.5).
const (
	// notificationsMaxItems is the most items of one request read; the rest
	// are ignored.
	notificationsMaxItems = 1000
	// notificationsMaxToken is the longest validation token answered.
	notificationsMaxToken = 1024
	// notificationsDBBudget bounds the database work of one request: Graph
	// wants an answer within three seconds.
	notificationsDBBudget = 2 * time.Second
	// notificationsIgnoredWarnEvery is how often at most the ignored items
	// are counted at warning; between, at debug, so a caller posting
	// nonsense cannot fill the logs.
	notificationsIgnoredWarnEvery = time.Minute
)

// The lifecycle events (research §3), and the row's state each sets.
var lifecycleStates = map[string]string{
	"reauthorizationRequired": subscriptionReauthorize,
	"subscriptionRemoved":     subscriptionRemoved,
}

// PostEntraMailboxNotifications answers Graph's handshake and its
// notifications. (POST /api/v1/entra/mailbox-notifications)
func (s *server) PostEntraMailboxNotifications(ctx context.Context, req gen.PostEntraMailboxNotificationsRequestObject) (gen.PostEntraMailboxNotificationsResponseObject, error) {
	if req.Params.ValidationToken != nil {
		return s.handshake(ctx, *req.Params.ValidationToken), nil
	}
	items, ok := notificationItems(req.JSONBody)
	if !ok {
		title, detail, status := "Invalid notification", "The body is not a notification: {\"value\": [...]}.", int32(http.StatusBadRequest)
		return gen.PostEntraMailboxNotifications400ApplicationProblemPlusJSONResponse{Title: &title, Detail: &detail, Status: &status}, nil
	}
	if err := s.notified(ctx, items); err != nil {
		s.client.logger.WarnContext(ctx, "entra: a change notification could not be recorded", "error", err.Error())
		title, detail, status := "Service unavailable", "The notification could not be recorded. Send it again.", int32(http.StatusServiceUnavailable)
		return gen.PostEntraMailboxNotifications503ApplicationProblemPlusJSONResponse{Title: &title, Detail: &detail, Status: &status}, nil
	}
	return gen.PostEntraMailboxNotifications202Response{}, nil
}

// handshake answers Graph's validation of the webhook: 200 text/plain with
// the token — as the server decoded it from the query, which is what Graph
// compares — while a create's window is open on the connection row and the
// token is 1 to notificationsMaxToken bytes of UTF-8 with no control
// character; else 404 with no body. It reads that one row and nothing
// else.
func (s *server) handshake(ctx context.Context, token string) gen.PostEntraMailboxNotificationsResponseObject {
	if !validToken(token) {
		return gen.PostEntraMailboxNotifications404Response{}
	}
	ctx, cancel := context.WithTimeout(ctx, notificationsDBBudget)
	defer cancel()
	conn, err := s.q.GetConnection(ctx)
	if err != nil || conn.NotificationsValidatingUntil == nil || !s.deps.Clock().Before(*conn.NotificationsValidatingUntil) {
		return gen.PostEntraMailboxNotifications404Response{}
	}
	nosniff, noStore := "nosniff", "no-store"
	return gen.PostEntraMailboxNotifications200TextplainCharsetUtf8Response{
		Body:          strings.NewReader(token),
		ContentLength: int64(len(token)),
		Headers:       gen.PostEntraMailboxNotifications200ResponseHeaders{XContentTypeOptions: &nosniff, CacheControl: &noStore},
	}
}

// validToken reports whether token may be answered: present, at most
// notificationsMaxToken bytes, valid UTF-8 and no control character.
func validToken(token string) bool {
	if token == "" || len(token) > notificationsMaxToken || !utf8.ValidString(token) {
		return false
	}
	return strings.IndexFunc(token, unicode.IsControl) < 0
}

// notificationItem is one item of a notification as the webhook reads it:
// these fields and no other — the resource and its data are never read.
type notificationItem struct {
	subscriptionID string
	clientState    string
	tenantID       string
	lifecycleEvent string
}

// notificationItems is body's items, false when body is not
// {"value": [...]}. An item that is not an object, or whose fields are not
// strings, is kept with those fields empty, and so is invalid.
func notificationItems(body *gen.PostEntraMailboxNotificationsJSONRequestBody) ([]notificationItem, bool) {
	if body == nil {
		return nil, false
	}
	raw, ok := (*body)["value"].([]any)
	if !ok {
		return nil, false
	}
	items := make([]notificationItem, len(raw))
	for i, v := range raw {
		obj, ok := v.(map[string]any)
		if !ok {
			continue
		}
		str := func(key string) string {
			s, _ := obj[key].(string)
			return s
		}
		items[i] = notificationItem{
			subscriptionID: str("subscriptionId"),
			clientState:    str("clientState"),
			tenantID:       str("tenantId"),
			lifecycleEvent: str("lifecycleEvent"),
		}
	}
	return items, true
}

// plausible reports whether it may be one of this installation's
// notifications, read with no database access: its subscriptionId and its
// tenantId GUIDs in their canonical 36 characters, its clientState 43
// characters of the base64url alphabet — the shape every create's has
// (clientStateBytes). Only a request holding one is read further, so
// nonsense costs no query.
func plausible(it notificationItem) bool {
	if _, ok := subscriptionID(it.subscriptionID); !ok {
		return false
	}
	if _, ok := subscriptionID(it.tenantID); !ok {
		return false
	}
	if len(it.clientState) != base64.RawURLEncoding.EncodedLen(clientStateBytes) {
		return false
	}
	// The decoder skips line breaks: the bytes decoded are counted too.
	raw, err := base64.RawURLEncoding.DecodeString(it.clientState)
	return err == nil && len(raw) == clientStateBytes
}

// notified records the valid items among the first notificationsMaxItems,
// within notificationsDBBudget. An item is valid when it is plausible, a
// row holds its subscriptionId, its clientState's SHA-256 equals the row's
// (compared in constant time) and its tenantId is the connection's,
// whatever its case; a request with no plausible item reads nothing. The
// subscriptions named validly with no lifecycle event are marked in one
// statement (MarkMailboxesNotified); one named with a lifecycle event is
// marked alone (MarkMailboxLifecycle) with the strongest state among its
// items, removed over reauthorize. The connection's last notification is
// touched when any was valid. The ignored are counted at debug, and at
// warning at most once every notificationsIgnoredWarnEvery. The error is
// the database's.
func (s *server) notified(ctx context.Context, items []notificationItem) error {
	ctx, cancel := context.WithTimeout(ctx, notificationsDBBudget)
	defer cancel()
	total, ignored := len(items), 0
	if len(items) > notificationsMaxItems {
		ignored = len(items) - notificationsMaxItems
		items = items[:notificationsMaxItems]
	}
	var ids []string
	seen := map[string]bool{}
	kept := items[:0:0]
	for _, it := range items {
		if !plausible(it) {
			ignored++
			continue
		}
		kept = append(kept, it)
		if id, _ := subscriptionID(it.subscriptionID); !seen[id.String()] {
			seen[id.String()] = true
			ids = append(ids, id.String())
		}
	}
	rows := map[string]store.EntraMailboxSubscription{}
	tenant := ""
	if len(ids) > 0 {
		conn, err := s.q.GetConnection(ctx)
		if err != nil {
			return fmt.Errorf("entra: read the connection: %w", err)
		}
		if conn.TenantID != nil {
			tenant = conn.TenantID.String()
		}
		list, err := s.q.ListMailboxSubscriptionsByID(ctx, ids)
		if err != nil {
			return fmt.Errorf("entra: read the subscriptions: %w", err)
		}
		for _, r := range list {
			if r.SubscriptionID != nil {
				rows[*r.SubscriptionID] = r
			}
		}
	}
	marks := map[string]string{} // subscription id → lifecycle state, "" for none
	var order []string
	for _, it := range kept {
		id, _ := subscriptionID(it.subscriptionID)
		row, ok := rows[id.String()]
		hash := sha256.Sum256([]byte(it.clientState))
		if !ok || len(row.ClientStateHash) != len(hash) ||
			subtle.ConstantTimeCompare(hash[:], row.ClientStateHash) != 1 ||
			tenant == "" || len(it.tenantID) != len(tenant) || !strings.EqualFold(it.tenantID, tenant) {
			ignored++
			continue
		}
		state, marked := marks[id.String()]
		if !marked {
			order = append(order, id.String())
		}
		switch next := lifecycleStates[it.lifecycleEvent]; {
		case next == subscriptionRemoved, next == subscriptionReauthorize && state != subscriptionRemoved:
			state = next
		}
		marks[id.String()] = state
	}
	now := s.deps.Clock()
	written := int64(0)
	var plain []string
	for _, id := range order {
		state := marks[id]
		if state == "" {
			plain = append(plain, id)
			continue
		}
		n, err := s.q.MarkMailboxLifecycle(ctx, store.MarkMailboxLifecycleParams{Now: now, LifecycleState: state, SubscriptionID: id})
		if err != nil {
			return fmt.Errorf("entra: mark the subscription's lifecycle: %w", err)
		}
		written += n
	}
	if len(plain) > 0 {
		n, err := s.q.MarkMailboxesNotified(ctx, store.MarkMailboxesNotifiedParams{Now: now, SubscriptionIds: plain})
		if err != nil {
			return fmt.Errorf("entra: mark the subscriptions notified: %w", err)
		}
		written += n
	}
	if len(order) > 0 {
		if err := s.q.TouchLastNotification(ctx, now); err != nil {
			return fmt.Errorf("entra: record the last notification: %w", err)
		}
	}
	s.client.logger.DebugContext(ctx, "entra: change notifications received",
		"items", total, "subscriptions", len(order), "written", written, "ignored", ignored)
	if ignored > 0 && s.warnIgnored(now) {
		s.client.logger.WarnContext(ctx, "entra: change notifications ignored", "ignored", ignored)
	}
	return nil
}

// warnIgnored reports whether the ignored items may be counted at warning
// now: when notificationsIgnoredWarnEvery has passed since the last that
// was, in this process.
func (s *server) warnIgnored(now time.Time) bool {
	last := s.ignoredWarnedAt.Load()
	if last != 0 && now.Sub(time.Unix(0, last)) < notificationsIgnoredWarnEvery {
		return false
	}
	return s.ignoredWarnedAt.CompareAndSwap(last, now.UnixNano())
}
