package calendar

import (
	"context"

	"github.com/vantigo-io/vantigo/server/internal/calendar/gen"
)

// This file is the person's subscriptions (calendar feed design D6): the
// calendars from elsewhere they subscribe to, at most ten, all
// calendar:access's and on the caller's own rows.

// GetCalendarSubscriptions answers the caller's subscriptions.
func (s *server) GetCalendarSubscriptions(context.Context, gen.GetCalendarSubscriptionsRequestObject) (gen.GetCalendarSubscriptionsResponseObject, error) {
	return nil, errNotImplemented
}

// PostCalendarSubscriptions subscribes the caller to a calendar.
func (s *server) PostCalendarSubscriptions(context.Context, gen.PostCalendarSubscriptionsRequestObject) (gen.PostCalendarSubscriptionsResponseObject, error) {
	return nil, errNotImplemented
}

// PutCalendarSubscriptionsById replaces one of the caller's subscriptions.
func (s *server) PutCalendarSubscriptionsById(context.Context, gen.PutCalendarSubscriptionsByIdRequestObject) (gen.PutCalendarSubscriptionsByIdResponseObject, error) {
	return nil, errNotImplemented
}

// DeleteCalendarSubscriptionsById removes one of the caller's subscriptions.
func (s *server) DeleteCalendarSubscriptionsById(context.Context, gen.DeleteCalendarSubscriptionsByIdRequestObject) (gen.DeleteCalendarSubscriptionsByIdResponseObject, error) {
	return nil, errNotImplemented
}

// PostCalendarSubscriptionsByIdRefresh fetches one of the caller's
// subscriptions now.
func (s *server) PostCalendarSubscriptionsByIdRefresh(context.Context, gen.PostCalendarSubscriptionsByIdRefreshRequestObject) (gen.PostCalendarSubscriptionsByIdRefreshResponseObject, error) {
	return nil, errNotImplemented
}
