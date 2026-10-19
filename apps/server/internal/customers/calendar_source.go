package customers

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/customers/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// The kinds this module projects onto the calendar (calendar design D1).
const (
	calendarKindFollowUp    = "customers.follow-up"
	calendarKindInteraction = "customers.interaction"
)

// calendarSource is this module's contracts.CalendarSource (calendar design
// D1): the follow-ups assigned to the caller and the interactions the caller
// wrote down, each a row of the customer timeline. It is its own gatekeeper:
// both kinds are timeline entries about a customer, so the caller needs the
// timeline and the customer both — customers:timeline-view and
// customers:view, the pair the timeline itself is read with — and without
// them it answers nothing, not an error.
type calendarSource struct {
	q      *store.Queries
	access contracts.Access
}

var _ contracts.CalendarSource = (*calendarSource)(nil)

// newCalendarSource is Module's CalendarSource.
func newCalendarSource(d module.Deps) contracts.CalendarSource {
	return &calendarSource{q: store.New(d.Pool), access: d.Access}
}

func (s *calendarSource) CalendarKinds() []contracts.CalendarKind {
	return []contracts.CalendarKind{
		{Key: calendarKindFollowUp, Module: "customers"},
		{Key: calendarKindInteraction, Module: "customers"},
	}
}

func (s *calendarSource) CalendarItems(ctx context.Context, q contracts.CalendarQuery) ([]contracts.CalendarItem, error) {
	if !contracts.HasPermissions(ctx, s.access, timelineView, customersView) {
		return nil, nil
	}
	from, to := pgtype.Date{Time: q.From, Valid: true}, pgtype.Date{Time: q.To, Valid: true}

	followUps, err := s.q.CalendarFollowUps(ctx, store.CalendarFollowUpsParams{UserID: q.UserID, FromDay: from, ToDay: to})
	if err != nil {
		return nil, fmt.Errorf("customers: calendar follow-ups: %w", err)
	}
	interactions, err := s.q.CalendarInteractions(ctx, store.CalendarInteractionsParams{UserID: q.UserID, FromDay: from, ToDay: to})
	if err != nil {
		return nil, fmt.Errorf("customers: calendar interactions: %w", err)
	}

	items := make([]contracts.CalendarItem, 0, len(followUps)+len(interactions))
	for _, r := range followUps {
		items = append(items, contracts.CalendarItem{
			Kind:      calendarKindFollowUp,
			ID:        strconv.Itoa(int(r.ID)),
			Title:     calendarTitle(r.Summary, r.EventType),
			Subtitle:  r.CustomerName,
			AllDay:    true,
			StartDate: r.FollowUpOn.Time,
			EndDate:   r.FollowUpOn.Time,
			Done:      r.FollowUpDoneAt != nil,
			Path:      calendarPath(r.CustomerID),
		})
	}
	for _, r := range interactions {
		item := contracts.CalendarItem{
			Kind:     calendarKindInteraction,
			ID:       strconv.Itoa(int(r.ID)),
			Title:    calendarTitle(r.Summary, r.EventType),
			Subtitle: r.CustomerName,
			Path:     calendarPath(r.CustomerID),
		}
		if r.OccurredAt != nil {
			// A point in time: the view draws its default length.
			item.StartsAt, item.EndsAt = r.OccurredAt.UTC(), r.OccurredAt.UTC()
		} else {
			item.AllDay, item.StartDate, item.EndDate = true, r.OccurredOn.Time, r.OccurredOn.Time
		}
		items = append(items, item)
	}
	return items, nil
}

// calendarTitle is an entry's summary, which a manual entry always has (it is
// derived from the note); the event type stands in should it ever be blank.
func calendarTitle(summary, eventType string) string {
	if strings.TrimSpace(summary) != "" {
		return summary
	}
	return eventType
}

func calendarPath(customerID int32) string {
	return "/customers/" + strconv.Itoa(int(customerID))
}
