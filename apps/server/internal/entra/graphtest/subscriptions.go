package graphtest

import (
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// This file is the change notifications' subscriptions (calendar Outlook
// sync, delivery D's plan reading 9; research §3, Change notifications, as
// checked for D): POST /subscriptions, PATCH and DELETE
// /subscriptions/{id}, and GET /subscriptions, the application's
// subscriptions the caller created in pages of SubscriptionPageSize with an
// @odata.nextLink. Every one is reached with a delegated token alone
// (graph.go's scopeRefusal): a create for another user's events is 403
// ErrorAccessDenied, the subscription's creator the token's user
// (CreatorID); a read, a renewal or a delete of a subscription another user
// created — or the application, before delivery B moved the subscriptions
// to the person's token (CreatorID "") — is 403 too.
//
// What it keeps faithful: a create names a resource of a user's events
// (/users/{id}/events, its leading slash optional), the change types, an
// https notificationUrl, a clientState of at most 128 characters and an
// expiry at most 10 080 minutes ahead (raised to 45 minutes when nearer),
// 400 otherwise; the webhook is validated before the 201 — each distinct
// URL, notificationUrl and lifecycleNotificationUrl, through Handshake,
// which must answer 200 with the very token — and a failed handshake is
// Graph's 400 "Subscription validation request failed…"; a second
// subscription by the same creator on the same resource and change types is
// 409 — one the application made beside one the person makes is not; a mailbox outside the application's scope is 403 ErrorAccessDenied
// (MailboxDenied), a user with none 404 MailboxNotEnabledForRESTAPI, an
// unknown user 404 Request_ResourceNotFound; an unknown or expired
// subscription is 404 ResourceNotFound, and one past its expiry is gone
// from the list, as Graph deletes it. Times are answered with Graph's seven
// fractional digits.

// The subscriptions' figures, Graph's.
const (
	subscriptionMaxMinutes  = 10080
	subscriptionMinMinutes  = 45
	subscriptionClientState = 128
	subscriptionPageDefault = 100
)

// graphTime is how Graph writes an instant: UTC, seven fractional digits.
const graphTime = "2006-01-02T15:04:05.0000000Z"

// subscriptionEventsResource is a user's calendar events; subscriptionMessagesResource
// is a mailbox's Inbox messages.
var (
	subscriptionEventsResource   = regexp.MustCompile(`(?i)^/?users/([^/]+)/events$`)
	subscriptionMessagesResource = regexp.MustCompile(`(?i)^/?users/([^/]+)/mailfolders(?:\('inbox'\)|/inbox)/messages$`)
)

// Subscription is one of the application's subscriptions.
type Subscription struct {
	ID                       string
	Resource                 string
	ChangeType               string
	NotificationURL          string
	LifecycleNotificationURL string
	// ClientState is the secret the create sent, as it was sent — a test
	// signs its notifications with it.
	ClientState string
	Expiration  time.Time
	// CreatorID is the object id of the user whose delegated token made it,
	// in lower case; "" is the application, with its own token — every
	// subscription before delivery B — which no person's token may read,
	// renew or delete.
	CreatorID string
}

// Subscriptions is the application's subscriptions Graph still holds —
// none past its expiry — in the order made.
func (g *FakeGraph) Subscriptions() []Subscription {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []Subscription
	for _, s := range g.liveSubscriptions() {
		out = append(out, *s)
	}
	return out
}

// AddSubscription seeds a subscription as if it had been made before —
// one whose row the module lost, by s.CreatorID, or the application's when
// that is "" — and answers it, with a fresh id when s has none.
func (g *FakeGraph) AddSubscription(s Subscription) Subscription {
	g.mu.Lock()
	defer g.mu.Unlock()
	if s.ID == "" {
		s.ID = uuid.NewString()
	}
	s.CreatorID = strings.ToLower(s.CreatorID)
	g.subscriptions = append(g.subscriptions, &s)
	return s
}

// RemoveSubscription deletes the subscription id as Graph does on its own —
// the mailbox's access withdrawn, say — and reports whether it was there.
func (g *FakeGraph) RemoveSubscription(id string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	for i, s := range g.subscriptions {
		if strings.EqualFold(s.ID, id) {
			g.subscriptions = append(g.subscriptions[:i], g.subscriptions[i+1:]...)
			return true
		}
	}
	return false
}

// liveSubscriptions are the subscriptions not past their expiry; the
// expired are dropped, as Graph deletes them.
func (g *FakeGraph) liveSubscriptions() []*Subscription {
	now := g.now()
	live := g.subscriptions[:0]
	for _, s := range g.subscriptions {
		if s.Expiration.After(now) {
			live = append(live, s)
		}
	}
	g.subscriptions = live
	return live
}

// subscription is the live subscription id, nil when there is none.
func (g *FakeGraph) subscription(id string) *Subscription {
	for _, s := range g.liveSubscriptions() {
		if strings.EqualFold(s.ID, id) {
			return s
		}
	}
	return nil
}

func subscriptionNotFound() answer {
	return graphError(http.StatusNotFound, "ResourceNotFound", "The object was not found.")
}

// subscriptionJSON is s as Graph answers it.
func subscriptionJSON(s *Subscription) map[string]any {
	return map[string]any{
		"id": s.ID, "resource": s.Resource, "changeType": s.ChangeType,
		"notificationUrl": s.NotificationURL, "lifecycleNotificationUrl": nullable(s.LifecycleNotificationURL),
		"clientState": nullable(s.ClientState), "expirationDateTime": s.Expiration.UTC().Format(graphTime),
		"latestSupportedTlsVersion": "v1_2",
	}
}

// subscriptionUser is the user whose events or Inbox messages resource names,
// whether the resource is Inbox messages, and the refusal when there is none
// or its mailbox is refused.
func (g *FakeGraph) subscriptionUser(resource string) (*User, bool, answer, bool) {
	isMessages := false
	m := subscriptionEventsResource.FindStringSubmatch(resource)
	if m == nil {
		m = subscriptionMessagesResource.FindStringSubmatch(resource)
		isMessages = m != nil
	}
	if m == nil {
		return nil, false, graphError(http.StatusBadRequest, "ExtensionError", "Operation: Create; Exception: [Status Code: BadRequest; Reason: The resource is not supported.]"), false
	}
	u := g.userByID(m[1], false)
	if u == nil {
		return nil, false, notFound(m[1]), false
	}
	if a, refused := mailboxRefused(u); refused {
		return nil, false, a, false
	}
	return u, isMessages, answer{}, true
}

// expiration reads a subscription's expirationDateTime: 400 when it does
// not parse, is past or beyond subscriptionMaxMinutes; raised to
// subscriptionMinMinutes when nearer.
func (g *FakeGraph) expiration(v string) (time.Time, answer, bool) {
	t, err := time.Parse(time.RFC3339Nano, v)
	now := g.now()
	switch {
	case err != nil || !t.After(now):
		return time.Time{}, graphError(http.StatusBadRequest, "InvalidRequest", "Subscription expiration date time is not valid."), false
	case t.After(now.Add(subscriptionMaxMinutes * time.Minute)):
		return time.Time{}, graphError(http.StatusBadRequest, "InvalidRequest",
			"Subscription expiration can only be "+strconv.Itoa(subscriptionMaxMinutes)+" minutes in the future."), false
	case t.Before(now.Add(subscriptionMinMinutes * time.Minute)):
		t = now.Add(subscriptionMinMinutes * time.Minute)
	}
	return t.UTC(), answer{}, true
}

// subscriptionsRoute answers /subscriptions and /subscriptions/{id}.
func (g *FakeGraph) subscriptionsRoute(method string, seg []string, query url.Values, body []byte) answer {
	if len(seg) == 0 {
		switch method {
		case http.MethodGet:
			return g.listSubscriptions(query)
		case http.MethodPost:
			return g.createSubscription(body)
		}
		return methodNotAllowed()
	}
	if len(seg) != 1 {
		return graphError(http.StatusBadRequest, "BadRequest", "Unsupported request: "+method+" subscriptions/"+strings.Join(seg, "/")+".")
	}
	s := g.subscription(seg[0])
	if s == nil {
		return subscriptionNotFound()
	}
	if !g.createdByCaller(s) {
		return graphError(http.StatusForbidden, "ErrorAccessDenied", "Access is denied. Check credentials and try again.")
	}
	if u := g.userOfSubscription(s); u != nil && u.MailboxDenied {
		a, _ := mailboxRefused(u)
		return a
	}
	switch method {
	case http.MethodGet:
		return answer{status: http.StatusOK, body: subscriptionJSON(s)}
	case http.MethodPatch:
		var patch struct {
			Expiration string `json:"expirationDateTime"`
		}
		if err := json.Unmarshal(body, &patch); err != nil {
			return graphError(http.StatusBadRequest, "BadRequest", "The request body is not valid.")
		}
		t, a, ok := g.expiration(patch.Expiration)
		if !ok {
			return a
		}
		s.Expiration = t
		return answer{status: http.StatusOK, body: subscriptionJSON(s)}
	case http.MethodDelete:
		for i, x := range g.subscriptions {
			if x == s {
				g.subscriptions = append(g.subscriptions[:i], g.subscriptions[i+1:]...)
				break
			}
		}
		return answer{status: http.StatusNoContent}
	}
	return methodNotAllowed()
}

// createdByCaller reports whether the request's token is s's creator's:
// CreatorID "" for the application's token, or the delegated token's oid.
// g.mu is held.
func (g *FakeGraph) createdByCaller(s *Subscription) bool {
	if g.caller == nil {
		return s.CreatorID == ""
	}
	return s.CreatorID != "" && strings.EqualFold(s.CreatorID, g.caller.oid)
}

// userOfSubscription is the user s's resource names, nil when none.
func (g *FakeGraph) userOfSubscription(s *Subscription) *User {
	m := subscriptionEventsResource.FindStringSubmatch(s.Resource)
	if m == nil {
		m = subscriptionMessagesResource.FindStringSubmatch(s.Resource)
	}
	if m == nil {
		return nil
	}
	return g.userByID(m[1], true)
}

// createSubscription is POST /subscriptions.
func (g *FakeGraph) createSubscription(body []byte) answer {
	var req struct {
		ChangeType               string `json:"changeType"`
		NotificationURL          string `json:"notificationUrl"`
		LifecycleNotificationURL string `json:"lifecycleNotificationUrl"`
		Resource                 string `json:"resource"`
		Expiration               string `json:"expirationDateTime"`
		ClientState              string `json:"clientState"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return graphError(http.StatusBadRequest, "BadRequest", "The request body is not valid.")
	}
	if !validChangeType(req.ChangeType) {
		return graphError(http.StatusBadRequest, "InvalidRequest", "The changeType is not valid.")
	}
	for _, u := range []string{req.NotificationURL, req.LifecycleNotificationURL} {
		if p, err := url.Parse(u); u != "" && (err != nil || p.Scheme != "https" || p.Host == "") {
			return graphError(http.StatusBadRequest, "InvalidRequest", "The notification URL must be an absolute https URL.")
		}
	}
	if req.NotificationURL == "" {
		return graphError(http.StatusBadRequest, "InvalidRequest", "The notificationUrl is required.")
	}
	if len(req.ClientState) > subscriptionClientState {
		return graphError(http.StatusBadRequest, "InvalidRequest", "The clientState is longer than 128 characters.")
	}
	expires, a, ok := g.expiration(req.Expiration)
	if !ok {
		return a
	}
	u, isMessages, a, ok := g.subscriptionUser(req.Resource)
	if !ok {
		return a
	}
	if isMessages {
		if g.caller != nil && !g.callerIs(u.ID) {
			return graphError(http.StatusForbidden, "ErrorAccessDenied", "Access is denied. Check credentials and try again.")
		}
	} else if !g.callerIs(u.ID) {
		return graphError(http.StatusForbidden, "ErrorAccessDenied", "Access is denied. Check credentials and try again.")
	}
	resource := strings.ToLower(strings.TrimPrefix(req.Resource, "/"))
	for _, s := range g.liveSubscriptions() {
		if strings.ToLower(strings.TrimPrefix(s.Resource, "/")) == resource && s.ChangeType == req.ChangeType && g.createdByCaller(s) {
			return graphError(http.StatusConflict, "ExtensionError",
				"Subscription Id "+s.ID+" already exists for the requested combination")
		}
	}
	urls := []string{req.NotificationURL}
	if req.LifecycleNotificationURL != "" && req.LifecycleNotificationURL != req.NotificationURL {
		urls = append(urls, req.LifecycleNotificationURL)
	}
	for _, u := range urls {
		token := "Validation: Testing client application reachability for subscription Request-Id: " + uuid.NewString()
		if g.Handshake == nil {
			continue
		}
		if status, answered := g.Handshake(u, token); status != http.StatusOK || answered != token {
			return graphError(http.StatusBadRequest, "InvalidRequest",
				"Subscription validation request failed. Notification endpoint must respond with 200 OK to validation request.")
		}
	}
	creatorID := ""
	if g.caller != nil {
		creatorID = g.caller.oid
	}
	s := &Subscription{
		ID: uuid.NewString(), Resource: req.Resource, ChangeType: req.ChangeType,
		NotificationURL: req.NotificationURL, LifecycleNotificationURL: req.LifecycleNotificationURL,
		ClientState: req.ClientState, Expiration: expires, CreatorID: creatorID,
	}
	g.subscriptions = append(g.subscriptions, s)
	return answer{status: http.StatusCreated, body: subscriptionJSON(s)}
}

// validChangeType reports whether v is a comma-separated set of created,
// updated and deleted.
func validChangeType(v string) bool {
	if v == "" {
		return false
	}
	for _, c := range strings.Split(v, ",") {
		if c != "created" && c != "updated" && c != "deleted" {
			return false
		}
	}
	return true
}

// listSubscriptions is GET /subscriptions: the live subscriptions in pages
// of SubscriptionPageSize, a $skiptoken in the next link.
func (g *FakeGraph) listSubscriptions(query url.Values) answer {
	size := g.SubscriptionPageSize
	if size <= 0 {
		size = subscriptionPageDefault
	}
	skip := 0
	if v := query.Get("$skiptoken"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return graphError(http.StatusBadRequest, "BadRequest", "Invalid skip token.")
		}
		skip = n
	}
	var all []*Subscription
	for _, s := range g.liveSubscriptions() {
		if g.createdByCaller(s) {
			all = append(all, s)
		}
	}
	out := []any{}
	for i := skip; i < len(all) && i < skip+size; i++ {
		out = append(out, subscriptionJSON(all[i]))
	}
	body := map[string]any{"value": out}
	if skip+size < len(all) {
		body["@odata.nextLink"] = g.nextLink("https://" + GraphHost + "/v1.0/subscriptions?$skiptoken=" + strconv.Itoa(skip+size))
	}
	return answer{status: http.StatusOK, body: body}
}
