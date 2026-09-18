package timetracking

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/time/gen"
	"github.com/vantigo-io/vantigo/server/internal/time/store"
)

// The people overview's window (§4.2): four weeks unless the caller asks for
// between one and twelve.
const (
	peopleDefaultWeeks = 4
	peopleMaxWeeks     = 12
)

// GetTimePeople Get the people overview
// (GET /api/v1/time/people)
//
// time:view-all only, which the contract's access rule enforces. The window
// is the current week — the Monday on or before today, dates in UTC, as the
// harness clock and the week endpoints read them — and the weeks before it.
// Everyone with an entry dated in the window or a week submission for one of
// its weeks is listed, with every week of the window, oldest first; nobody
// else is, since identity's users are not this module's to enumerate.
func (s *server) GetTimePeople(ctx context.Context, req gen.GetTimePeopleRequestObject) (gen.GetTimePeopleResponseObject, error) {
	weeks := int32(peopleDefaultWeeks)
	if w := req.Params.Weeks; w != nil {
		if *w < 1 || *w > peopleMaxWeeks {
			return gen.GetTimePeople400ApplicationProblemPlusJSONResponse(apicommon.Problem(invalidQueryTitle,
				fmt.Sprintf("'weeks' must be between 1 and %d, but was %d.", peopleMaxWeeks, *w))), nil
		}
		weeks = *w
	}
	now := s.deps.Clock().UTC()
	current := mondayOf(time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC))
	windowStart := current.AddDate(0, 0, -daysInWeek*int(weeks-1))
	window := store.PeopleWeekTotalsParams{WindowStart: pgDate(windowStart), WindowEnd: pgDate(weekEnd(current))}

	q := store.New(s.deps.Pool)
	totals, err := q.PeopleWeekTotals(ctx, window)
	if err != nil {
		return nil, fmt.Errorf("time: sum the people's weeks: %w", err)
	}
	submissions, err := q.PeopleWeekSubmissions(ctx, store.PeopleWeekSubmissionsParams(window))
	if err != nil {
		return nil, fmt.Errorf("time: read the people's week submissions: %w", err)
	}

	byUser := map[uuid.UUID][]gen.TimePersonWeek{}
	var userIDs []uuid.UUID
	weeksOf := func(userID uuid.UUID) []gen.TimePersonWeek {
		if w, ok := byUser[userID]; ok {
			return w
		}
		w := make([]gen.TimePersonWeek, weeks)
		for i := range w {
			w[i].WeekStart = openapi_types.Date{Time: windowStart.AddDate(0, 0, daysInWeek*i)}
		}
		byUser[userID] = w
		userIDs = append(userIDs, userID)
		return w
	}
	weekIndex := func(weekStart time.Time) int {
		return int(weekStart.Sub(windowStart) / (daysInWeek * 24 * time.Hour))
	}
	for _, t := range totals {
		hours, err := centsFromNumeric(t.Hours)
		if err != nil {
			return nil, err
		}
		approved, err := centsFromNumeric(t.ApprovedHours)
		if err != nil {
			return nil, err
		}
		w := &weeksOf(t.UserID)[weekIndex(t.WeekStart.Time)]
		w.Hours = float64(hours) / 100
		w.ApprovedHours = float64(approved) / 100
		w.RejectedCount = t.RejectedCount
	}
	for _, sub := range submissions {
		at := sub.SubmittedAt
		weeksOf(sub.UserID)[weekIndex(sub.WeekStart.Time)].SubmittedAt = &at
	}

	users, err := s.userEntries(ctx, userIDs)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(userIDs, func(a, b uuid.UUID) int { return compareNames(users[a], users[b]) })
	resp := make(gen.GetTimePeople200JSONResponse, 0, len(userIDs))
	for _, id := range userIDs {
		resp = append(resp, gen.TimePersonOverview{UserId: id, DisplayName: users[id].DisplayName, Weeks: byUser[id]})
	}
	return resp, nil
}
