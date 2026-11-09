package timetracking

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/events"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/time/store"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

// This file is the time-weeks worker (virtual employees design V30): from the
// Tuesday after a week, in the business time zone, it announces each person who
// did not submit that week with time.week.unsubmitted, once. A person is one who
// has draft entries in the week, or who submitted one of the eight weeks before
// it: Time knows no one else to expect a week from. Only the week just ended is
// looked at, so a worker that was down for a fortnight does not announce old weeks.
// The event is published in the transaction that records the announcement. Each person and
// week is announced once: week_announced's primary key (user_id, week_start) and
// INSERT ... ON CONFLICT DO NOTHING make a second replica, or a second pass, find nothing to
// write (zero rows), and only the pass that wrote the row publishes.

const (
	weeksWorkerName = "time-weeks"
	weeksInterval   = 30 * time.Minute
	weeksPerCycle   = 500
)

// WeeksWorker announces the weeks nobody submitted. It implements worker.Worker.
type WeeksWorker struct{ deps module.Deps }

var _ worker.Worker = (*WeeksWorker)(nil)

// NewWeeksWorker builds the worker over worker mode's Deps.
func NewWeeksWorker(d module.Deps) *WeeksWorker { return &WeeksWorker{deps: d} }

func (w *WeeksWorker) Name() string            { return weeksWorkerName }
func (w *WeeksWorker) Interval() time.Duration { return weeksInterval }

func (w *WeeksWorker) Run(ctx context.Context) error {
	t := time.NewTicker(weeksInterval)
	defer t.Stop()
	for {
		if _, err := w.RunCycle(ctx); err != nil && ctx.Err() == nil {
			logger := w.deps.Logger
			if logger == nil {
				logger = slog.Default()
			}
			logger.Error("time: a weeks cycle failed", "worker", weeksWorkerName, "error", err.Error())
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// lastWeek is the Monday of the week just ended, when the business day is the
// Tuesday after it or later in the week that follows; false on a Monday, when the
// week has only just ended and people still submit it.
func lastWeek(today time.Time) (time.Time, bool) {
	wd := int(today.Weekday())
	if wd == 0 {
		wd = 7
	}
	if wd < 2 {
		return time.Time{}, false
	}
	thisMonday := today.AddDate(0, 0, -(wd - 1))
	return thisMonday.AddDate(0, 0, -7), true
}

// RunCycle announces the people who did not submit last week and answers how many.
func (w *WeeksWorker) RunCycle(ctx context.Context) (int, error) {
	now := w.deps.Clock()
	today, err := w.deps.Calendar.Today(ctx, now)
	if err != nil {
		return 0, fmt.Errorf("time: read the business day: %w", err)
	}
	week, ok := lastWeek(today)
	if !ok {
		return 0, nil
	}
	announced := 0
	err = db.WithTx(ctx, w.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := store.New(tx)
		rows, err := q.UnsubmittedWeeks(ctx, store.UnsubmittedWeeksParams{Monday: pgDate(week), N: weeksPerCycle})
		if err != nil {
			return fmt.Errorf("time: find the people who did not submit: %w", err)
		}
		for _, r := range rows {
			n, err := q.MarkWeekAnnounced(ctx, store.MarkWeekAnnouncedParams{UserID: r.UserID, Monday: pgDate(week), Now: now})
			if err != nil {
				return fmt.Errorf("time: record a week's announcement: %w", err)
			}
			if n == 0 {
				continue
			}
			hours, err := centsFromNumeric(r.Hours)
			if err != nil {
				return err
			}
			if err := events.Publish(ctx, tx, "time", contracts.WeekUnsubmitted{
				UserID: r.UserID, WeekStart: week.Format(time.DateOnly), Hours: fmt.Sprintf("%d.%02d", hours/100, hours%100),
			}, now); err != nil {
				return err
			}
			announced++
		}
		return nil
	})
	return announced, err
}
