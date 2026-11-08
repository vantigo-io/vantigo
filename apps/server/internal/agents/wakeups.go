package agents

// The agents-wakeups worker (virtual employees design V29): what turns a moment
// into work. It writes a wakeup for each occurrence of a role's schedule, in the
// business time zone, and turns the wakeups that came due (a follow-up an agent
// asked for, a quote sent N days ago, a schedule's occurrence) into run requests
// for the runner. It needs only the database. A paused agent's wakeups wait until
// it is active again, and a conversation handed to the people is not woken.

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/agents/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

const (
	wakeupsWorkerName     = "agents-wakeups"
	wakeupsWorkerInterval = 30 * time.Second
	wakeupBatch           = 50

	scheduleWeekly  = "weekly"
	scheduleMonthly = "monthly"

	// scheduleGrace is how long after its time a schedule's occurrence still wakes
	// the role: a worker that was down over the weekend does not send Monday's
	// digest on Friday.
	scheduleGrace = 12 * time.Hour
)

type wakeupsWorker struct{ s *server }

func (w *wakeupsWorker) Name() string            { return wakeupsWorkerName }
func (w *wakeupsWorker) Interval() time.Duration { return wakeupsWorkerInterval }

func (w *wakeupsWorker) Run(ctx context.Context) error {
	t := time.NewTicker(wakeupsWorkerInterval)
	defer t.Stop()
	for {
		if err := w.cycle(ctx); err != nil && ctx.Err() == nil {
			w.s.warn("agents: the wakeups worker failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// WakeupsCycle is one pass of the agents-wakeups worker over d, for a test that
// runs no workers (as GateFor serves one that binds a dispatcher of its own).
func WakeupsCycle(d module.Deps) error {
	return (&wakeupsWorker{s: newServer(d)}).cycle(context.Background())
}

// cycle does one pass: the schedules that came due, then the wakeups.
func (w *wakeupsWorker) cycle(ctx context.Context) error {
	if err := w.schedules(ctx); err != nil {
		return err
	}
	return w.due(ctx)
}

// schedules writes the wakeup of each role's latest occurrence, once. The
// occurrence is written and the role marked in one transaction, so a second
// replica, or a second pass, never wakes it twice.
func (w *wakeupsWorker) schedules(ctx context.Context) error {
	now := w.s.deps.Clock()
	loc := w.s.zone(ctx)
	return withTx(ctx, w.s.deps.Pool, func(q *store.Queries) error {
		roles, err := q.ListScheduledRoles(ctx)
		if err != nil {
			return fmt.Errorf("agents: find the scheduled roles: %w", err)
		}
		for _, r := range roles {
			if r.ScheduleKind == nil || r.ScheduleDay == nil || r.ScheduleTime == nil {
				continue
			}
			occ, ok := lastOccurrence(now, loc, *r.ScheduleKind, int(*r.ScheduleDay), *r.ScheduleTime)
			if !ok || (r.ScheduleFiredAt != nil && !occ.After(*r.ScheduleFiredAt)) {
				continue
			}
			if now.Sub(occ) <= scheduleGrace {
				if _, err := q.InsertScheduleWakeup(ctx, store.InsertScheduleWakeupParams{
					ID: uuid.New(), RoleID: r.ID, SubjectID: occ.In(loc).Format(time.DateOnly), At: occ, Reason: "schedule", Now: now,
				}); err != nil {
					return fmt.Errorf("agents: write a schedule's wakeup: %w", err)
				}
			}
			if err := q.MarkScheduleFired(ctx, store.MarkScheduleFiredParams{ID: r.ID, FiredAt: occ}); err != nil {
				return err
			}
		}
		return nil
	})
}

// due turns the wakeups that came due into run requests.
func (w *wakeupsWorker) due(ctx context.Context) error {
	return withTx(ctx, w.s.deps.Pool, func(q *store.Queries) error {
		now := w.s.deps.Clock()
		list, err := q.LockDueWakeups(ctx, store.LockDueWakeupsParams{Now: now, N: wakeupBatch})
		if err != nil {
			return fmt.Errorf("agents: find the wakeups that came due: %w", err)
		}
		for _, wk := range list {
			if err := w.wake(ctx, q, wk, now); err != nil {
				return err
			}
			if err := q.TakeWakeup(ctx, store.TakeWakeupParams{ID: wk.ID, Now: now}); err != nil {
				return err
			}
		}
		return nil
	})
}

// wake makes the run request of one wakeup, unless it is stale or its
// conversation was handed to the people.
func (w *wakeupsWorker) wake(ctx context.Context, q *store.Queries, wk store.LockDueWakeupsRow, now time.Time) error {
	if now.Sub(wk.At) > wakeupStale {
		return nil
	}
	if wk.SubjectType == subjectConversation {
		conv, err := uuid.Parse(wk.SubjectID)
		if err != nil {
			return nil
		}
		if _, err := q.GetHandedOff(ctx, conv); err == nil {
			return nil // a colleague has it: a follow-up does not take it back
		} else if !isNoRows(err) {
			return fmt.Errorf("agents: read the hand-off: %w", err)
		}
		return q.UpsertRunRequest(ctx, store.UpsertRunRequestParams{
			RoleID: wk.RoleID, ConversationID: conv, Reason: reasonWakeup, Note: wk.Reason, NotBefore: now, DueBy: now.Add(maxWait), Now: now,
		})
	}
	return q.InsertSubjectRequest(ctx, store.InsertSubjectRequestParams{
		RoleID: wk.RoleID, SubjectType: wk.SubjectType, SubjectID: wk.SubjectID, CustomerID: wk.CustomerID, Reason: reasonWakeup, Note: wk.Reason, Now: now,
	})
}

// lastOccurrence is the latest moment at or before now at which a schedule wakes,
// in the zone: weekly on an ISO weekday (1 Monday to 7 Sunday), or monthly on a
// day of the month (1 to 28, so that every month has it), at hh:mm. A local time
// the zone skips (the hour a clock jumps over) is the moment after the jump, and
// one it repeats is the later of the two, as Go's time.Date reads them: the role
// wakes once either way.
func lastOccurrence(now time.Time, loc *time.Location, kind string, day int, hhmm string) (time.Time, bool) {
	hh, mm, ok := strings.Cut(hhmm, ":")
	if !ok {
		return time.Time{}, false
	}
	h, err1 := strconv.Atoi(hh)
	m, err2 := strconv.Atoi(mm)
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return time.Time{}, false
	}
	local := now.In(loc)
	y, mo, d := local.Date()
	switch kind {
	case scheduleWeekly:
		if day < 1 || day > 7 {
			return time.Time{}, false
		}
		wd := int(local.Weekday())
		if wd == 0 {
			wd = 7
		}
		back := (wd - day + 7) % 7
		occ := time.Date(y, mo, d-back, h, m, 0, 0, loc)
		if occ.After(now) {
			occ = time.Date(y, mo, d-back-7, h, m, 0, 0, loc)
		}
		return occ, true
	case scheduleMonthly:
		if day < 1 || day > 28 {
			return time.Time{}, false
		}
		occ := time.Date(y, mo, day, h, m, 0, 0, loc)
		if occ.After(now) {
			occ = time.Date(y, mo-1, day, h, m, 0, 0, loc)
		}
		return occ, true
	}
	return time.Time{}, false
}

var _ worker.Worker = (*wakeupsWorker)(nil)
