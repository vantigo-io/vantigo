package manufacturing

// Time logs read and corrected (manufacturing design M29, M29a, M31): the
// logs of an order and of a work order, a supervisor's correction of a log
// and addition of a missing one, and my work. The writes follow the lock
// order (M26a): the order, the work order, the log.

import (
	"context"
	"fmt"
	"math/big"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/manufacturing/gen"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/store"
)

const (
	// correctionWindow is how far back a log can be corrected or added
	// (M29's log_locked).
	correctionWindow = 60 * 24 * time.Hour
	correctionTitle  = "The time log was not saved"
)

// ListManufacturingOrderTimeLogs is every log on the order's work orders, by
// start (M29).
func (s *server) ListManufacturingOrderTimeLogs(ctx context.Context, req gen.ListManufacturingOrderTimeLogsRequestObject) (gen.ListManufacturingOrderTimeLogsResponseObject, error) {
	var logs []store.ManufacturingTimeLog
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		if _, err := q.GetOrder(ctx, req.Id); noRows(err) {
			return notFound()
		} else if err != nil {
			return err
		}
		ids, err := q.WorkOrderIDsOfOrders(ctx, []int64{req.Id})
		if err != nil {
			return err
		}
		logs, err = q.LogsOfWorkOrders(ctx, ids)
		return err
	})
	if err != nil {
		return answer(err, answers[gen.ListManufacturingOrderTimeLogsResponseObject]{
			notFound: func() gen.ListManufacturingOrderTimeLogsResponseObject {
				return gen.ListManufacturingOrderTimeLogs404Response{}
			},
		})
	}
	return gen.ListManufacturingOrderTimeLogs200JSONResponse{Items: timeLogsOf(logs, s.deps.Clock())}, nil
}

// ListManufacturingWorkOrderTimeLogs is every log on the work order, by
// start (M29).
func (s *server) ListManufacturingWorkOrderTimeLogs(ctx context.Context, req gen.ListManufacturingWorkOrderTimeLogsRequestObject) (gen.ListManufacturingWorkOrderTimeLogsResponseObject, error) {
	q := store.New(s.deps.Pool)
	if _, err := q.GetWorkOrder(ctx, req.Id); noRows(err) {
		return gen.ListManufacturingWorkOrderTimeLogs404Response{}, nil
	} else if err != nil {
		return nil, err
	}
	logs, err := q.LogsOfWorkOrders(ctx, []int64{req.Id})
	if err != nil {
		return nil, err
	}
	return gen.ListManufacturingWorkOrderTimeLogs200JSONResponse{Items: timeLogsOf(logs, s.deps.Clock())}, nil
}

// logTimes is a correction's or an addition's times and note, judged: the
// end after the start and not in the future, the note 1–2000 characters
// (M29).
func logTimes(f *fieldErrors, started, ended time.Time, note string, now time.Time) string {
	if !ended.After(started) {
		f.add("endedAt", "Must be after the start")
	}
	if ended.After(now) {
		f.add("endedAt", "Must not be in the future")
	}
	note = strings.TrimSpace(note)
	if n := utf8.RuneCountInString(note); n < 1 || n > maxLogNote {
		f.add("note", "Say why, in 1–2000 characters")
	}
	return note
}

// logQuantity is a log's quantity sent as text, judged: 0 or more with at
// most three decimals; nil is none.
func logQuantity(f *fieldErrors, q *string) *big.Rat {
	if q == nil {
		return nil
	}
	v, msg := parseDecimal(*q, quantityPlaces, maxQuantity)
	f.addCoded("quantity", codeQuantityInvalid, msg)
	return v
}

// judgeLogRules is the rules a correction or an addition is refused by on
// the locked order (M29, M29a): the order done or cancelled is
// order_closed; a log, or a start, more than 60 days back is log_locked; a
// time overlapping the user's other logs is log_overlaps, judged under the
// user's advisory lock so two at once cannot both pass.
func judgeLogRules(ctx context.Context, txq *store.Queries, o store.ManufacturingOrder, user *uuid.UUID, logID int64, starts []time.Time,
	started, ended, now time.Time) error {
	if o.Status == orderDone || o.Status == orderCancelled {
		return conflict(codeOrderClosed, "The manufacturing order is closed",
			fmt.Sprintf("The order is %s; its time logs are kept as they were.", o.Status))
	}
	for _, at := range starts {
		if at.Before(now.Add(-correctionWindow)) {
			return conflict(codeLogLocked, "The time log is locked",
				"A time log more than 60 days old cannot be corrected or added.")
		}
	}
	if user == nil {
		return nil
	}
	if err := txq.LockUserLogs(ctx, *user); err != nil {
		return err
	}
	overlaps, err := txq.UserLogOverlaps(ctx, store.UserLogOverlapsParams{UserID: user, ID: logID, StartedAt: started, EndedAt: ended})
	if err != nil {
		return err
	}
	if overlaps {
		p := problem(http.StatusBadRequest, codeLogOverlaps, correctionTitle,
			"The person has another time log in this time. One person logs one thing at a time.")
		p.Errors = &map[string][]string{"startedAt": {"Overlaps another time log of the person"}}
		return refuse(p)
	}
	return nil
}

// correctAnswers are a correction's typed refusals.
func correctAnswers() answers[gen.CorrectManufacturingTimeLogResponseObject] {
	return answers[gen.CorrectManufacturingTimeLogResponseObject]{
		badRequest: func(p gen.ManufacturingProblem) gen.CorrectManufacturingTimeLogResponseObject {
			return gen.CorrectManufacturingTimeLog400ApplicationProblemPlusJSONResponse(p)
		},
		notFound: func() gen.CorrectManufacturingTimeLogResponseObject {
			return gen.CorrectManufacturingTimeLog404Response{}
		},
		conflict: func(p gen.ManufacturingProblem) gen.CorrectManufacturingTimeLogResponseObject {
			return gen.CorrectManufacturingTimeLog409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.ManufacturingProblem) gen.CorrectManufacturingTimeLogResponseObject {
			return gen.CorrectManufacturingTimeLog503ApplicationProblemPlusJSONResponse(p)
		},
	}
}

// CorrectManufacturingTimeLog is a supervisor's correction of a work
// order's log (M29, M29a): its times, its quantity (absent keeps it, null
// clears it) and a note — the supervisor's words, on the log alone (note_by)
// — kept in the order's timeline. A log still open is closed at the end
// given.
func (s *server) CorrectManufacturingTimeLog(ctx context.Context, req gen.CorrectManufacturingTimeLogRequestObject) (gen.CorrectManufacturingTimeLogResponseObject, error) {
	in := req.Body
	now := s.deps.Clock()
	f := s.unknownProperties(ctx, correctionSchema)
	note := logTimes(&f, in.StartedAt, in.EndedAt, in.Note, now)
	quantity := logQuantity(&f, in.Quantity)
	if f.failed() {
		return answer(refuseInvalid(correctionTitle, f), correctAnswers())
	}
	keepQuantity := !hasProperty(ctx, "quantity")
	pool := store.New(s.deps.Pool)
	pre, err := pool.GetTimeLog(ctx, req.Id)
	if noRows(err) || (err == nil && pre.WorkOrderID == nil) {
		return gen.CorrectManufacturingTimeLog404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	w, err := pool.GetWorkOrder(ctx, *pre.WorkOrderID)
	if err != nil {
		return nil, err
	}
	who, err := s.actorOf(ctx)
	if err != nil {
		return nil, err
	}
	err = s.withOrder(ctx, w.OrderID, nil, func(ctx context.Context, tx pgx.Tx, txq *store.Queries, lo *lockedOrder) error {
		if _, err := lockedWorkOrders(ctx, txq, []int64{w.ID}); err != nil {
			return err
		}
		locked, err := txq.LockTimeLogs(ctx, []int64{req.Id})
		if err != nil {
			return err
		}
		l := locked[0]
		if err := judgeLogRules(ctx, txq, lo.order, l.UserID, l.ID, []time.Time{l.StartedAt, in.StartedAt}, in.StartedAt, in.EndedAt, now); err != nil {
			return err
		}
		q := numeric(quantity, quantityPlaces)
		if quantity == nil {
			q = l.Quantity
			if !keepQuantity {
				q.Valid = false
			}
		}
		ended := in.EndedAt
		if _, err := txq.CorrectTimeLog(ctx, store.CorrectTimeLogParams{ID: l.ID, StartedAt: in.StartedAt, EndedAt: &ended, Quantity: q,
			Note: note, CorrectedBy: who.id, CorrectedByDisplay: who.display, Now: now}); err != nil {
			return err
		}
		n := newNotices()
		if l.EndedAt == nil {
			if _, err := txq.SettleWorkOrder(ctx, w.ID); err != nil && !noRows(err) {
				return err
			}
		}
		payload := map[string]any{"logId": l.ID, "workOrderId": w.ID, "startedAt": in.StartedAt, "endedAt": in.EndedAt,
			"wasStartedAt": l.StartedAt, "wasEndedAt": l.EndedAt}
		if l.UserID != nil {
			payload["user"] = l.UserDisplay
		}
		if err := event(ctx, txq, w.OrderID, gen.ManufacturingOrderEventKindTimeLogCorrected, who, now, payload); err != nil {
			return err
		}
		n.workOrder(w.ID, w.OrderID, w.WorkCenterID)
		return n.send(ctx, tx)
	})
	if err != nil {
		return answer(err, correctAnswers())
	}
	l, err := pool.GetTimeLog(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	return gen.CorrectManufacturingTimeLog200JSONResponse(timeLogOf(l, s.deps.Clock())), nil
}

// addLogAnswers are an addition's typed refusals.
func addLogAnswers() answers[gen.AddManufacturingTimeLogResponseObject] {
	return answers[gen.AddManufacturingTimeLogResponseObject]{
		badRequest: func(p gen.ManufacturingProblem) gen.AddManufacturingTimeLogResponseObject {
			return gen.AddManufacturingTimeLog400ApplicationProblemPlusJSONResponse(p)
		},
		notFound: func() gen.AddManufacturingTimeLogResponseObject { return gen.AddManufacturingTimeLog404Response{} },
		conflict: func(p gen.ManufacturingProblem) gen.AddManufacturingTimeLogResponseObject {
			return gen.AddManufacturingTimeLog409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.ManufacturingProblem) gen.AddManufacturingTimeLogResponseObject {
			return gen.AddManufacturingTimeLog503ApplicationProblemPlusJSONResponse(p)
		},
	}
}

// AddManufacturingTimeLog is a supervisor's addition of a missing, closed
// log for a user on the work order (M29, M29a), kept in the order's
// timeline; its note is the supervisor's words (note_by). It changes no work order's state: the floor's starts and
// finishes do.
func (s *server) AddManufacturingTimeLog(ctx context.Context, req gen.AddManufacturingTimeLogRequestObject) (gen.AddManufacturingTimeLogResponseObject, error) {
	in := req.Body
	now := s.deps.Clock()
	f := s.unknownProperties(ctx, timeLogInputSchema)
	note := logTimes(&f, in.StartedAt, in.EndedAt, in.Note, now)
	quantity := logQuantity(&f, in.Quantity)
	kind := logProductive
	if in.Kind != nil {
		kind = string(*in.Kind)
	}
	var reason *string
	switch {
	case kind != logProductive && kind != logDowntime:
		f.add("kind", "productive or downtime")
	case kind == logDowntime && (in.Reason == nil || !containsReason(*in.Reason)):
		f.add("reason", "One of breakdown, material_missing, setup, waiting_for_operator, quality, other")
	case kind == logProductive && in.Reason != nil:
		f.add("reason", "Only downtime has a reason")
	case kind == logDowntime:
		r := string(*in.Reason)
		reason = &r
	}
	users, err := s.users(ctx, []uuid.UUID{in.UserId})
	if err != nil {
		return nil, err
	}
	person, known := users[in.UserId]
	if s.deps.Users != nil && !known {
		f.add("userId", "No such user")
	}
	if f.failed() {
		return answer(refuseInvalid(correctionTitle, f), addLogAnswers())
	}
	w, err := s.workOrderOrNotFound(ctx, req.Id)
	if err != nil {
		return answer(err, addLogAnswers())
	}
	who, err := s.actorOf(ctx)
	if err != nil {
		return nil, err
	}
	user := in.UserId
	var id int64
	err = s.withOrder(ctx, w.OrderID, nil, func(ctx context.Context, tx pgx.Tx, txq *store.Queries, lo *lockedOrder) error {
		if _, err := lockedWorkOrders(ctx, txq, []int64{w.ID}); err != nil {
			return err
		}
		if err := judgeLogRules(ctx, txq, lo.order, &user, 0, []time.Time{in.StartedAt}, in.StartedAt, in.EndedAt, now); err != nil {
			return err
		}
		ended := in.EndedAt
		l, err := txq.InsertTimeLog(ctx, store.InsertTimeLogParams{WorkOrderID: &w.ID, WorkCenterID: w.WorkCenterID, UserID: &user,
			UserDisplay: person.DisplayName, Kind: kind, Reason: reason, Note: note, NoteBy: who.id, NoteByDisplay: who.display,
			StartedAt: in.StartedAt, EndedAt: &ended,
			Quantity: numeric(quantity, quantityPlaces), CorrectedBy: who.id, CorrectedByDisplay: who.display, CorrectedAt: &now})
		if err != nil {
			return err
		}
		id = l.ID
		if err := event(ctx, txq, w.OrderID, gen.ManufacturingOrderEventKindTimeLogAdded, who, now, map[string]any{
			"logId": l.ID, "workOrderId": w.ID, "user": person.DisplayName, "kind": kind, "startedAt": in.StartedAt,
			"endedAt": in.EndedAt,
		}); err != nil {
			return err
		}
		n := newNotices()
		n.workOrder(w.ID, w.OrderID, w.WorkCenterID)
		return n.send(ctx, tx)
	})
	if err != nil {
		return answer(err, addLogAnswers())
	}
	l, err := store.New(s.deps.Pool).GetTimeLog(ctx, id)
	if err != nil {
		return nil, err
	}
	return gen.AddManufacturingTimeLog201JSONResponse(timeLogOf(l, now)), nil
}

// containsReason reports whether r is one of M29's reasons.
func containsReason(r gen.ManufacturingDowntimeReason) bool {
	for _, x := range downtimeReasons {
		if x == r {
			return true
		}
	}
	return false
}

// GetManufacturingMyWork is my work (M31): the caller's open log with its
// work order, and the work orders they logged time on today, in the business
// calendar's day.
func (s *server) GetManufacturingMyWork(ctx context.Context, req gen.GetManufacturingMyWorkRequestObject) (gen.GetManufacturingMyWorkResponseObject, error) {
	from, to, err := s.todayBounds(ctx)
	if err != nil {
		return nil, err
	}
	caller := callerID(ctx)
	var mine *store.ManufacturingTimeLog
	var r workOrderRead
	err = s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		var err error
		if mine, err = openLogOf(ctx, q, &caller); err != nil {
			return err
		}
		touched, err := q.WorkOrdersTouchedBy(ctx, store.WorkOrdersTouchedByParams{UserID: &caller, FromAt: from, ToAt: to})
		if err != nil {
			return err
		}
		ids := make([]int64, 0, len(touched)+1)
		if mine != nil && mine.WorkOrderID != nil {
			ids = append(ids, *mine.WorkOrderID)
		}
		for _, t := range touched {
			if mine == nil || mine.WorkOrderID == nil || t.WorkOrderID != *mine.WorkOrderID {
				ids = append(ids, t.WorkOrderID)
			}
		}
		r, err = readWorkOrders(ctx, q, ids)
		return err
	})
	if err != nil {
		return nil, err
	}
	items, _, err := s.workOrdersOf(ctx, r, nil)
	if err != nil {
		return answer(err, answers[gen.GetManufacturingMyWorkResponseObject]{
			unavailable: func(p gen.ManufacturingProblem) gen.GetManufacturingMyWorkResponseObject {
				return gen.GetManufacturingMyWork503ApplicationProblemPlusJSONResponse(p)
			},
		})
	}
	// A station's operator sees their work at the station's work centers
	// only (M131); the open log itself is theirs wherever it runs.
	served, atStation, err := s.stationCenters(ctx)
	if err != nil {
		return nil, err
	}
	if atStation {
		items = slices.DeleteFunc(items, func(w gen.ManufacturingWorkOrder) bool { return !slices.Contains(served, w.WorkCenter.Id) })
	}
	out := gen.GetManufacturingMyWork200JSONResponse{Today: items}
	if mine != nil {
		l := timeLogOf(*mine, s.deps.Clock())
		out.OpenLog = &l
		for i := range items {
			if mine.WorkOrderID != nil && items[i].Id == *mine.WorkOrderID {
				out.OpenWorkOrder = &items[i]
			}
		}
	}
	return out, nil
}

// todayBounds is today in the business calendar's zone as two instants: its
// start and the next day's start (UTC without a calendar).
func (s *server) todayBounds(ctx context.Context) (time.Time, time.Time, error) {
	now := s.deps.Clock()
	zone := time.UTC
	if s.deps.Calendar != nil {
		if err := noteContractCall(ctx, "Calendar.Zone"); err != nil {
			return time.Time{}, time.Time{}, err
		}
		z, err := s.deps.Calendar.Zone(ctx)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("manufacturing: read the business zone: %w", err)
		}
		zone = z
	}
	local := now.In(zone)
	from := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, zone)
	return from, time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, zone), nil
}
