package calendar

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/calendar/gen"
	"github.com/vantigo-io/vantigo/server/internal/calendar/store"
	"github.com/vantigo-io/vantigo/server/internal/netguard"
)

// This file is the person's subscriptions (calendar feed design D6): the
// calendars from elsewhere they subscribe to, at most ten, all
// calendar:access's and on the caller's own rows — another person's is the
// 404 an unknown id gets. There is no revision and no If-Match: the row is
// the person's own, small, and last write wins. A POST, a PUT that changes
// the address and a refresh fetch once inline (subscriptions_fetch.go) before
// they answer, so a wrong address is seen at once; the row is kept whatever
// the fetch answered, its error set when it failed. Nothing here runs inside
// a transaction, so no call out of the module ever could.

// The bounds of a subscription.
const (
	maxSubscriptions       = 10
	maxSubscriptionName    = 100
	maxSubscriptionAddress = 2048
)

// subscriptionInput is a CalendarSubscriptionWrite that passed every rule,
// its address as stored.
type subscriptionInput struct {
	name     string
	url      string
	colour   string
	busyOnly bool
}

// readSubscription holds body to D6's rules, each a field error.
func readSubscription(body gen.CalendarSubscriptionWrite) (subscriptionInput, fieldErrors) {
	var f fieldErrors
	in := subscriptionInput{name: strings.TrimSpace(body.Name), colour: string(body.Colour), busyOnly: body.BusyOnly}
	switch {
	case in.name == "":
		f.add("name", "A name is required")
	case utf8.RuneCountInString(in.name) > maxSubscriptionName:
		f.add("name", fmt.Sprintf("A name is at most %d characters", maxSubscriptionName))
	}
	var msg string
	in.url, msg = subscriptionAddress(body.Url)
	f.add("url", msg)
	if !body.Colour.Valid() {
		f.add("colour", "colour is one of blue, cyan, teal, green, lime, yellow, orange, red, pink, grape, violet and indigo")
	}
	return in, f
}

// subscriptionAddress is raw as stored — webcal and webcals rewritten to
// https, the rest as typed — or why it is refused: an address at most 2048
// characters, absolute, of https, http, webcal or webcals, naming a host
// that is not an address the guard refuses, with no user name or password,
// which would be written to the row and the log.
func subscriptionAddress(raw string) (string, string) {
	v := strings.TrimSpace(raw)
	switch {
	case v == "":
		return "", "An address is required"
	case len(v) > maxSubscriptionAddress:
		return "", fmt.Sprintf("An address is at most %d characters", maxSubscriptionAddress)
	}
	u, err := url.Parse(v)
	if err != nil || !u.IsAbs() {
		return "", "This is not a whole address, such as https://example.com/calendar.ics"
	}
	scheme := strings.ToLower(u.Scheme)
	switch scheme {
	case "https", "http":
	case "webcal", "webcals":
		v = "https" + v[len(u.Scheme):]
	default:
		return "", "An address starts with https://, http://, webcal:// or webcals://"
	}
	if u.User != nil {
		return "", "An address carries no user name or password"
	}
	host := u.Hostname()
	if host == "" {
		return "", "An address names a host, such as https://example.com/calendar.ics"
	}
	if ip, err := netip.ParseAddr(host); err == nil && netguard.Disallowed(ip) {
		return "", "This is not an address Vantigo may fetch"
	}
	return v, ""
}

// toSubscription is a row on the wire. The body and the validators stay in
// the database.
func toSubscription(row store.CalendarSubscription) gen.CalendarSubscription {
	return gen.CalendarSubscription{
		Id: row.ID, Name: row.Name, Url: row.Url, Colour: gen.CalendarSubscriptionColour(row.Colour), BusyOnly: row.BusyOnly,
		FetchedAt: row.FetchedAt, AttemptedAt: row.AttemptedAt, Error: row.Error,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}

// GetCalendarSubscriptions answers the caller's subscriptions.
func (s *server) GetCalendarSubscriptions(ctx context.Context, _ gen.GetCalendarSubscriptionsRequestObject) (gen.GetCalendarSubscriptionsResponseObject, error) {
	rows, err := s.queries().ListSubscriptions(ctx, callerID(ctx))
	if err != nil {
		return nil, fmt.Errorf("calendar: list the subscriptions: %w", err)
	}
	out := gen.GetCalendarSubscriptions200JSONResponse{}
	for _, r := range rows {
		out = append(out, toSubscription(r))
	}
	return out, nil
}

// PostCalendarSubscriptions subscribes the caller to a calendar, and
// fetches it once before it answers.
func (s *server) PostCalendarSubscriptions(ctx context.Context, req gen.PostCalendarSubscriptionsRequestObject) (gen.PostCalendarSubscriptionsResponseObject, error) {
	if req.Body == nil {
		return gen.PostCalendarSubscriptions400ApplicationProblemPlusJSONResponse(invalid(invalidSubscriptionTitle, fieldErrors{})), nil
	}
	in, f := readSubscription(*req.Body)
	if f.failed() {
		return gen.PostCalendarSubscriptions400ApplicationProblemPlusJSONResponse(invalid(invalidSubscriptionTitle, f)), nil
	}
	userID := callerID(ctx)
	q := s.queries()
	n, err := q.CountSubscriptions(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("calendar: count the subscriptions: %w", err)
	}
	if n >= maxSubscriptions {
		return gen.PostCalendarSubscriptions409ApplicationProblemPlusJSONResponse(subscriptionLimit(maxSubscriptions)), nil
	}
	row, err := q.InsertSubscription(ctx, store.InsertSubscriptionParams{
		UserID: userID, Name: in.name, Url: in.url, Colour: in.colour, BusyOnly: in.busyOnly, Now: s.deps.Clock(),
	})
	if err != nil {
		return nil, fmt.Errorf("calendar: insert the subscription: %w", err)
	}
	if row, err = s.refreshSubscription(ctx, q, targetOf(row)); err != nil {
		return nil, err
	}
	return gen.PostCalendarSubscriptions201JSONResponse(toSubscription(row)), nil
}

// PutCalendarSubscriptionsById replaces one of the caller's subscriptions.
// A new address forgets every fetch of the old one and fetches the new.
func (s *server) PutCalendarSubscriptionsById(ctx context.Context, req gen.PutCalendarSubscriptionsByIdRequestObject) (gen.PutCalendarSubscriptionsByIdResponseObject, error) {
	if req.Body == nil {
		return gen.PutCalendarSubscriptionsById400ApplicationProblemPlusJSONResponse(invalid(invalidSubscriptionTitle, fieldErrors{})), nil
	}
	in, f := readSubscription(*req.Body)
	if f.failed() {
		return gen.PutCalendarSubscriptionsById400ApplicationProblemPlusJSONResponse(invalid(invalidSubscriptionTitle, f)), nil
	}
	userID := callerID(ctx)
	q := s.queries()
	before, err := q.GetSubscription(ctx, store.GetSubscriptionParams{ID: req.Id, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PutCalendarSubscriptionsById404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("calendar: read subscription %d: %w", req.Id, err)
	}
	row, err := q.UpdateSubscription(ctx, store.UpdateSubscriptionParams{
		Name: in.name, Url: in.url, Colour: in.colour, BusyOnly: in.busyOnly, Now: s.deps.Clock(), ID: req.Id, UserID: userID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PutCalendarSubscriptionsById404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("calendar: update subscription %d: %w", req.Id, err)
	}
	if row.Url != before.Url {
		if row, err = q.ResetSubscriptionFetch(ctx, row.ID); err != nil {
			return nil, fmt.Errorf("calendar: reset subscription %d's fetch: %w", req.Id, err)
		}
		if row, err = s.refreshSubscription(ctx, q, targetOf(row)); err != nil {
			return nil, err
		}
	}
	return gen.PutCalendarSubscriptionsById200JSONResponse(toSubscription(row)), nil
}

// DeleteCalendarSubscriptionsById removes one of the caller's subscriptions;
// its items are gone from the next read.
func (s *server) DeleteCalendarSubscriptionsById(ctx context.Context, req gen.DeleteCalendarSubscriptionsByIdRequestObject) (gen.DeleteCalendarSubscriptionsByIdResponseObject, error) {
	n, err := s.queries().DeleteSubscription(ctx, store.DeleteSubscriptionParams{ID: req.Id, UserID: callerID(ctx)})
	if err != nil {
		return nil, fmt.Errorf("calendar: delete subscription %d: %w", req.Id, err)
	}
	if n == 0 {
		return gen.DeleteCalendarSubscriptionsById404Response{}, nil
	}
	return gen.DeleteCalendarSubscriptionsById204Response{}, nil
}

// PostCalendarSubscriptionsByIdRefresh fetches one of the caller's
// subscriptions now — the screen's Refresh now.
func (s *server) PostCalendarSubscriptionsByIdRefresh(ctx context.Context, req gen.PostCalendarSubscriptionsByIdRefreshRequestObject) (gen.PostCalendarSubscriptionsByIdRefreshResponseObject, error) {
	q := s.queries()
	row, err := q.GetSubscription(ctx, store.GetSubscriptionParams{ID: req.Id, UserID: callerID(ctx)})
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PostCalendarSubscriptionsByIdRefresh404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("calendar: read subscription %d: %w", req.Id, err)
	}
	if row, err = s.refreshSubscription(ctx, q, targetOf(row)); err != nil {
		return nil, err
	}
	return gen.PostCalendarSubscriptionsByIdRefresh200JSONResponse(toSubscription(row)), nil
}
