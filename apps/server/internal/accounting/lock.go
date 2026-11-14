package accounting

// The lock (A8, R10): one date, locked_through, a month end. Forward with
// accounting:close; back with a reason, which the lock's history keeps. The
// settings row is taken FOR UPDATE, so every post in flight is waited for and
// none passes the move.

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/accounting/gen"
	"github.com/vantigo-io/vantigo/server/internal/accounting/store"
)

func (s *server) lockView(ctx context.Context, q *store.Queries) (gen.AccountingLock, error) {
	settings, err := q.GetSettings(ctx)
	if err != nil {
		return gen.AccountingLock{}, err
	}
	events, err := q.ListLockEvents(ctx)
	if err != nil {
		return gen.AccountingLock{}, err
	}
	out := gen.AccountingLock{LockedThrough: apiDatePtr(settings.LockedThrough), BooksStart: apiDate(settings.BooksStart),
		Events: []gen.AccountingLockEvent{}}
	for _, e := range events {
		out.Events = append(out.Events, gen.AccountingLockEvent{LockedFrom: apiDatePtr(e.LockedFrom), LockedTo: apiDatePtr(e.LockedTo),
			Reason: e.Reason, ChangedAt: e.ChangedAt, ChangedByUserId: openapi_types.UUID(e.ChangedByUserID)})
	}
	return out, nil
}

// GetAccountingLock is the lock date and its history.
func (s *server) GetAccountingLock(ctx context.Context, _ gen.GetAccountingLockRequestObject) (gen.GetAccountingLockResponseObject, error) {
	out, err := s.lockView(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	return gen.GetAccountingLock200JSONResponse(out), nil
}

// PutAccountingLock moves the lock (A8). A month end on or after the books'
// first month; back, a reason. The VAT terms of delivery D will bound how far
// back it may move: not behind the last filed term.
func (s *server) PutAccountingLock(ctx context.Context, req gen.PutAccountingLockRequestObject) (gen.PutAccountingLockResponseObject, error) {
	if req.Body == nil {
		return nil, invalidField("The lock cannot be moved", "body", "", "A body is required.")
	}
	target := datePtr(req.Body.LockedThrough)
	reason := strings.TrimSpace(deref(req.Body.Reason))
	if target != nil && !monthEnd(*target).Equal(*target) {
		return nil, invalidField("The lock cannot be moved", "lockedThrough", codeLockNotMonthEnd, "The lock is a month's last day.")
	}
	if len([]rune(reason)) > 500 {
		return nil, invalidField("The lock cannot be moved", "reason", "", "At most 500 characters.")
	}
	var out gen.AccountingLock
	err := s.withTx(ctx, func(_ pgx.Tx, q *store.Queries) error {
		settings, err := q.GetSettingsForUpdate(ctx)
		if err != nil {
			return err
		}
		if target != nil && target.Before(settings.BooksStart) {
			return invalidField("The lock cannot be moved", "lockedThrough", codeLockBeforeBooksStart,
				"The books start on "+settings.BooksStart.Format(time.DateOnly)+".")
		}
		current := settings.LockedThrough
		back := current != nil && (target == nil || target.Before(*current))
		if back && reason == "" {
			return invalidField("The lock cannot be moved", "reason", codeLockReasonRequired,
				"Moving the lock back opens closed books: say why.")
		}
		same := (current == nil && target == nil) || (current != nil && target != nil && current.Equal(*target))
		if !same {
			if err := q.SetLockedThrough(ctx, store.SetLockedThroughParams{LockedThrough: target, Now: s.now()}); err != nil {
				return err
			}
			var why *string
			if reason != "" {
				why = &reason
			}
			if err := q.InsertLockEvent(ctx, store.InsertLockEventParams{LockedFrom: current, LockedTo: target, Reason: why,
				Now: s.now(), UserID: callerID(ctx)}); err != nil {
				return err
			}
		}
		out, err = s.lockView(ctx, q)
		return err
	})
	if err != nil {
		return nil, err
	}
	if out.LockedThrough != nil {
		s.logger().InfoContext(ctx, "accounting: the lock moved", "locked_through", out.LockedThrough.Format(time.DateOnly))
	} else {
		s.logger().InfoContext(ctx, "accounting: the lock was lifted")
	}
	return gen.PutAccountingLock200JSONResponse(out), nil
}
