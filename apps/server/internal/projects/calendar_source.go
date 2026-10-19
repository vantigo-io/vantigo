package projects

import (
	"context"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/projects/store"
)

// calendarKindTask is the one kind this module projects onto the calendar
// (calendar design D1).
const calendarKindTask = "projects.task"

// calendarSource is this module's contracts.CalendarSource (calendar design
// D1): the tasks assigned to the caller, by due date. It is its own
// gatekeeper: projects:access, the module's door, and then projects.visible
// in the query with the caller's view-all/manage-all (globalAccess), the one
// rule every list in the module answers "may this caller see this project"
// by. Without the door it answers nothing, not an error.
type calendarSource struct {
	srv *server
	q   *store.Queries
}

var _ contracts.CalendarSource = (*calendarSource)(nil)

// newCalendarSource is Module's CalendarSource.
func newCalendarSource(d module.Deps) contracts.CalendarSource {
	return &calendarSource{srv: newServer(d), q: store.New(d.Pool)}
}

func (s *calendarSource) CalendarKinds() []contracts.CalendarKind {
	return []contracts.CalendarKind{{Key: calendarKindTask, Module: "projects"}}
}

func (s *calendarSource) CalendarItems(ctx context.Context, q contracts.CalendarQuery) ([]contracts.CalendarItem, error) {
	if !contracts.HasPermission(ctx, s.srv.deps.Access, "projects:access") {
		return nil, nil
	}
	rows, err := s.q.CalendarTasks(ctx, store.CalendarTasksParams{
		UserID:  q.UserID,
		FromDay: pgtype.Date{Time: q.From, Valid: true},
		ToDay:   pgtype.Date{Time: q.To, Valid: true},
		SeeAll:  s.srv.globalAccess(ctx).CanSee,
	})
	if err != nil {
		return nil, fmt.Errorf("projects: calendar tasks: %w", err)
	}
	items := make([]contracts.CalendarItem, 0, len(rows))
	for _, r := range rows {
		items = append(items, contracts.CalendarItem{
			Kind:      calendarKindTask,
			ID:        strconv.Itoa(int(r.ID)),
			Title:     r.Title,
			Subtitle:  r.ProjectCode,
			AllDay:    true,
			StartDate: r.DueDate.Time,
			EndDate:   r.DueDate.Time,
			Done:      r.Status == taskStatusDone,
			// The task sheet over the project's task list, the frontend's
			// taskUrl (apps/projects/frontend/src/lib/tasks.ts).
			Path: fmt.Sprintf("/projects/%d/tasks?task=%d", r.ProjectID, r.ID),
		})
	}
	return items, nil
}
