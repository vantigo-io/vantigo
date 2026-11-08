package invoices

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/events"
	"github.com/vantigo-io/vantigo/server/internal/holidays"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

// This file is the invoices-overdue-events worker (virtual employees design
// V30): it announces each invoice that became overdue, once, with
// invoices.invoice.overdue. "Overdue" is the state the overdue list and the stats
// read (invoices.document_state on the business day), so the three never
// disagree. The event is published in the transaction that records the
// announcement, while the invoice's row is held FOR UPDATE; several replicas
// skip each other's rows.

const (
	overdueEventsWorkerName = "invoices-overdue-events"
	overdueEventsInterval   = 15 * time.Minute
	// overdueEventsPerCycle bounds one pass; the rest is announced by the next.
	overdueEventsPerCycle = 200
)

// OverdueEventsWorker announces overdue invoices. It implements worker.Worker.
type OverdueEventsWorker struct{ deps module.Deps }

var _ worker.Worker = (*OverdueEventsWorker)(nil)

// NewOverdueEventsWorker builds the worker over worker mode's Deps.
func NewOverdueEventsWorker(d module.Deps) *OverdueEventsWorker { return &OverdueEventsWorker{deps: d} }

func (w *OverdueEventsWorker) Name() string            { return overdueEventsWorkerName }
func (w *OverdueEventsWorker) Interval() time.Duration { return overdueEventsInterval }

func (w *OverdueEventsWorker) Run(ctx context.Context) error {
	t := time.NewTicker(overdueEventsInterval)
	defer t.Stop()
	for {
		if _, err := w.RunCycle(ctx); err != nil && ctx.Err() == nil {
			logger := w.deps.Logger
			if logger == nil {
				logger = slog.Default()
			}
			logger.Error("invoices: an overdue events cycle failed", "worker", overdueEventsWorkerName, "error", err.Error())
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// RunCycle announces the invoices that are overdue today and not yet announced,
// and answers how many it announced.
func (w *OverdueEventsWorker) RunCycle(ctx context.Context) (int, error) {
	loc, err := w.deps.Calendar.Zone(ctx)
	if err != nil {
		return 0, fmt.Errorf("invoices: read the business time zone: %w", err)
	}
	now := w.deps.Clock()
	today := holidays.DayIn(now, loc)
	announced := 0
	err = db.WithTx(ctx, w.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := store.New(tx)
		rows, err := q.InvoicesToAnnounceOverdue(ctx, store.InvoicesToAnnounceOverdueParams{Today: pgDate(today), N: overdueEventsPerCycle})
		if err != nil {
			return fmt.Errorf("invoices: find the invoices that became overdue: %w", err)
		}
		for _, r := range rows {
			n, err := q.MarkOverdueAnnounced(ctx, store.MarkOverdueAnnouncedParams{InvoiceID: r.ID, AnnouncedOn: pgDate(today), Now: now})
			if err != nil {
				return fmt.Errorf("invoices: record an overdue announcement: %w", err)
			}
			if n == 0 {
				continue
			}
			gross, err := ratFromNumeric(r.GrossTotal)
			if err != nil {
				return err
			}
			var number int64
			if r.Number != nil {
				number = *r.Number
			}
			if err := events.Publish(ctx, tx, "invoices", contracts.InvoiceOverdue{
				InvoiceID: r.ID, Number: number, CustomerID: r.CustomerID, DueDate: r.DueDate.Time.Format(time.DateOnly),
				Day: today.Format(time.DateOnly), Currency: r.Currency, GrossTotal: gross.FloatString(2),
			}, now); err != nil {
				return err
			}
			announced++
		}
		return nil
	})
	return announced, err
}
