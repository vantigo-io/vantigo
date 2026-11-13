package manufacturing

// Work orders and the shop floor (manufacturing design M27–M33, M27a, M28a,
// M29a, M26a): the work orders snapshotted at confirm, their states, the
// time logs opened and ended by start, pause and finish, the work center's
// block, and the answers the shop floor reads. timelogs.go holds the
// supervisor's corrections, the log lists and my work.
//
// Every write follows the canonical lock order (M26a) through withOrders:
// the variants, every order the write touches in id order, then here the
// work orders in id order (LockWorkOrders), the work center (FOR SHARE for a
// start, FOR UPDATE for a block or an unblock), the time logs in id order
// (LockTimeLogs), and the ledger last. A write whose orders come from a row
// — the order of the caller's open log on another work order, the orders of
// the logs at a work center — reads them before the transaction, and starts
// again (errRelock) when the row moved meanwhile to an order it did not
// lock: so two operators starting across each other's orders, or a block
// beside a start, never wait on each other in a circle.

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"math/big"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/gen"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/store"
)

// A work order's states (M27).
const (
	workOrderPending    = "pending"
	workOrderReady      = "ready"
	workOrderInProgress = "in_progress"
	workOrderPaused     = "paused"
	workOrderDone       = "done"
	workOrderCancelled  = "cancelled"
)

// A time log's kinds and a work center's working states (M29, M30).
const (
	logProductive = "productive"
	logDowntime   = "downtime"
	stateNormal   = "normal"
	stateBlocked  = "blocked"
)

const (
	// longRunningAfter is how long an open log runs before the shop floor
	// warns (M29): nothing closes it.
	longRunningAfter = 12 * time.Hour
	// openLogIndex is the one open log per user (M29a's log_conflict).
	openLogIndex = "ux_time_logs_open_user"
	maxLogNote   = 2000
	blockTitle   = "The work center was not blocked"
)

// errAlreadyDone is finish's answer on a work order already done by the
// finish that posted the same production: the replay answers 200.
var errAlreadyDone = errors.New("manufacturing: the work order was finished by this request before")

// snapshotWorkOrders creates the order's work orders from its BoM's
// operations (M27, M27a, M28a): one per operation by sequence, each with the
// operation's and its work center's figures as they are now, and its expected
// minutes — computed here, and again only when a group work order moves to
// another member before any time is logged (manufacturing phase 2 M118). An
// operation that targets a group takes the group's first active member by
// code. The operations' graph is copied between the work orders (M113): those
// with no predecessor are ready, the rest pending. A figure past
// maxExpectedMinutes is held at it, as the overview refuses past it. Their
// work centers are then taken FOR SHARE (M26a step 5), so an archive waits for
// the confirm and counts them.
func snapshotWorkOrders(ctx context.Context, txq *store.Queries, o store.ManufacturingOrder, bom store.ManufacturingBom) ([]store.ManufacturingWorkOrder, error) {
	ops, err := txq.OperationsOfBoms(ctx, []int64{bom.ID})
	if err != nil {
		return nil, err
	}
	edges, err := txq.DependenciesOfBoms(ctx, []int64{bom.ID})
	if err != nil {
		return nil, err
	}
	// A group operation snapshots its first active member; a group with none
	// has nowhere to run (manufacturing phase 2 M118).
	for _, op := range ops {
		if op.WorkCenterGroupID != nil && op.WorkCenterStatus != statusActive {
			return nil, conflict(codeWorkCenterGroupEmpty, "The work center group has no active work center",
				fmt.Sprintf("Operation %s targets work center group %s, and none of its work centers is active. Reactivate one or add one to the group.",
					op.Name, deref(op.GroupCode)))
		}
	}
	hasPred := map[int64]bool{}
	for _, e := range edges {
		hasPred[e.OperationID] = true
	}
	out := make([]store.ManufacturingWorkOrder, 0, len(ops))
	byOperation := map[int64]int64{}
	for _, op := range ops {
		minutes := expectedMinutes(op, ratOrZero(o.Quantity), ratOrZero(bom.OutputQuantity))
		if minutes.Cmp(maxExpectedMinutes) > 0 {
			minutes = maxExpectedMinutes
		}
		setup := op.SetupMinutes
		if !setup.Valid {
			setup = op.WorkCenterSetupMinutes
		}
		status := workOrderReady
		if hasPred[op.ID] {
			status = workOrderPending
		}
		operationID := op.ID
		w, err := txq.InsertWorkOrder(ctx, store.InsertWorkOrderParams{
			OrderID: o.ID, OperationID: &operationID, Sequence: op.Sequence, Name: op.Name, WorkCenterID: op.CenterID,
			WorkCenterGroupID: op.WorkCenterGroupID, SetupMinutes: setup, CleanupMinutes: op.WorkCenterCleanupMinutes,
			MinutesPerCycle: op.MinutesPerCycle, Capacity: op.WorkCenterCapacity, TimeEfficiency: op.WorkCenterTimeEfficiency,
			MachineRate: op.WorkCenterMachineCostPerHour, LabourRate: op.WorkCenterLabourCostPerHour, Instructions: op.Instructions,
			ExpectedMinutes: minutes.Int64(), Status: status, SendAheadQuantity: op.SendAheadQuantity,
			BomOutputQuantity: bom.OutputQuantity, OwnSetup: op.SetupMinutes.Valid,
		})
		if err != nil {
			return nil, err
		}
		byOperation[op.ID] = w.ID
		// The operation's checks, copied as the operation is (M96).
		if err := txq.SnapshotWorkOrderChecks(ctx, store.SnapshotWorkOrderChecksParams{WorkOrderID: w.ID, OperationID: op.ID}); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	for _, e := range edges {
		if err := txq.InsertWorkOrderDependency(ctx, store.InsertWorkOrderDependencyParams{
			WorkOrderID: byOperation[e.OperationID], DependsOnID: byOperation[e.DependsOnID],
		}); err != nil {
			return nil, err
		}
	}
	centers := make([]int64, 0, len(out))
	for _, w := range out {
		centers = append(centers, w.WorkCenterID)
	}
	if _, err := txq.LockWorkCentersForShare(ctx, slices.Compact(slices.Sorted(slices.Values(centers)))); err != nil {
		return nil, err
	}
	return out, nil
}

// endOrderWork ends every open log of the order's work orders at now and
// cancels every work order not done (M19, M23, M29a): cancel and close, on
// the locked order, before the ledger.
func endOrderWork(ctx context.Context, txq *store.Queries, orderID int64, now time.Time, n *notices) error {
	ids, err := txq.WorkOrderIDsOfOrders(ctx, []int64{orderID})
	if err != nil || len(ids) == 0 {
		return err
	}
	if _, err := txq.LockWorkOrders(ctx, ids); err != nil {
		return err
	}
	open, err := txq.OpenLogsOfOrder(ctx, orderID)
	if err != nil {
		return err
	}
	if err := endLogs(ctx, txq, open, now, nil, nil, n); err != nil {
		return err
	}
	cancelled, err := txq.CancelOpenWorkOrders(ctx, orderID)
	if err != nil {
		return err
	}
	for _, w := range cancelled {
		n.workOrder(w.ID, w.OrderID, w.WorkCenterID)
	}
	return nil
}

// endLogs locks logs in id order and ends each one still open at now; the
// quantity, when set, goes on the log of user quantityOf. Every work center
// they were at is noted.
func endLogs(ctx context.Context, txq *store.Queries, logs []store.ManufacturingTimeLog, now time.Time, quantityOf *uuid.UUID, quantity *big.Rat, n *notices) error {
	if len(logs) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(logs))
	for _, l := range logs {
		ids = append(ids, l.ID)
	}
	locked, err := txq.LockTimeLogs(ctx, ids)
	if err != nil {
		return err
	}
	for _, l := range locked {
		if l.EndedAt != nil {
			continue
		}
		p := store.EndTimeLogParams{ID: l.ID, Now: now}
		if quantity != nil && quantityOf != nil && l.UserID != nil && *l.UserID == *quantityOf {
			p.Quantity = numeric(quantity, quantityPlaces)
		}
		if err := txq.EndTimeLog(ctx, p); err != nil {
			return err
		}
		n.workCenters[l.WorkCenterID] = true
	}
	return nil
}

// lockedWorkOrders is the work orders ids names, locked in id order (M26a
// step 4), by id.
func lockedWorkOrders(ctx context.Context, txq *store.Queries, ids []int64) (map[int64]store.ManufacturingWorkOrder, error) {
	rows, err := txq.LockWorkOrders(ctx, slices.Compact(slices.Sorted(slices.Values(ids))))
	if err != nil {
		return nil, err
	}
	out := make(map[int64]store.ManufacturingWorkOrder, len(rows))
	for _, w := range rows {
		out[w.ID] = w
	}
	return out, nil
}

// openLogOf is the user's one open log, nil when none.
func openLogOf(ctx context.Context, q *store.Queries, user *uuid.UUID) (*store.ManufacturingTimeLog, error) {
	l, err := q.OpenLogOfUser(ctx, user)
	if noRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &l, nil
}

// workOrderOrNotFound reads a work order on the pool, 404 when none.
func (s *server) workOrderOrNotFound(ctx context.Context, id int64) (store.ManufacturingWorkOrder, error) {
	w, err := store.New(s.deps.Pool).GetWorkOrder(ctx, id)
	if noRows(err) {
		return w, notFound()
	}
	return w, err
}

// workOrderClosed is the 409 work_order_closed.
func workOrderClosed(status string) error {
	return conflict(codeWorkOrderClosed, "The work order is closed",
		fmt.Sprintf("The work order is %s; nothing more is logged on it.", status))
}

// startAnswers are start's typed refusals.
func startAnswers() answers[gen.StartManufacturingWorkOrderResponseObject] {
	return answers[gen.StartManufacturingWorkOrderResponseObject]{
		badRequest: func(p gen.ManufacturingProblem) gen.StartManufacturingWorkOrderResponseObject {
			return gen.StartManufacturingWorkOrder400ApplicationProblemPlusJSONResponse(p)
		},
		forbidden: func(p gen.ManufacturingProblem) gen.StartManufacturingWorkOrderResponseObject {
			return gen.StartManufacturingWorkOrder403ApplicationProblemPlusJSONResponse(p)
		},
		notFound: func() gen.StartManufacturingWorkOrderResponseObject {
			return gen.StartManufacturingWorkOrder404Response{}
		},
		conflict: func(p gen.ManufacturingProblem) gen.StartManufacturingWorkOrderResponseObject {
			return gen.StartManufacturingWorkOrder409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.ManufacturingProblem) gen.StartManufacturingWorkOrderResponseObject {
			return gen.StartManufacturingWorkOrder503ApplicationProblemPlusJSONResponse(p)
		},
	}
}

// StartManufacturingWorkOrder opens a productive log for the caller (M29,
// M29a): the caller's open log elsewhere is ended first in the same
// transaction, so one caller has one open log; on a work order the caller
// already runs it is a no-op. A pending work order needs force (the log is
// marked forced), a done or cancelled one is work_order_closed, and a
// blocked work center refuses (work_center_blocked). The order's first start
// moves it to in progress.
//
// The orders locked are the work order's and, when the caller's open log is
// on another order's work order, that order too, both in id order; then both
// work orders, then the work center FOR SHARE (a block waits for the start,
// or the start for the block, and sees it), then the caller's log.
func (s *server) StartManufacturingWorkOrder(ctx context.Context, req gen.StartManufacturingWorkOrderRequestObject) (gen.StartManufacturingWorkOrderResponseObject, error) {
	if err := s.requireShopFloor(ctx, "Starting a work order"); err != nil {
		return answer(err, startAnswers())
	}
	if f := s.unknownProperties(ctx, startInputSchema); f.failed() {
		return answer(refuseInvalid("The work order was not started", f), startAnswers())
	}
	force := req.Body != nil && req.Body.Force != nil && *req.Body.Force
	var at *int64
	if req.Body != nil {
		at = req.Body.WorkCenterId
	}
	pre, err := s.workOrderOrNotFound(ctx, req.Id)
	if err != nil {
		return answer(err, startAnswers())
	}
	who, err := s.actorOf(ctx)
	if err != nil {
		return nil, err
	}
	idsOf := func(ctx context.Context, q *store.Queries) ([]int64, error) {
		ids := []int64{pre.OrderID}
		mine, err := openLogOf(ctx, q, who.id)
		if err != nil || mine == nil || mine.WorkOrderID == nil {
			return ids, err
		}
		other, err := q.GetWorkOrder(ctx, *mine.WorkOrderID)
		if err != nil {
			return nil, err
		}
		return append(ids, other.OrderID), nil
	}
	var closedID int64
	err = s.withOrders(ctx, idsOf, nil, func(ctx context.Context, tx pgx.Tx, txq *store.Queries, orders map[int64]*lockedOrder) error {
		closedID = 0
		n := newNotices()
		o := orders[pre.OrderID].order
		if !isOpen(o.Status) {
			return orderNotOpen(o.Status)
		}
		mine, err := openLogOf(ctx, txq, who.id)
		if err != nil {
			return err
		}
		ids := []int64{req.Id}
		if mine != nil && mine.WorkOrderID != nil {
			ids = append(ids, *mine.WorkOrderID)
		}
		wos, err := lockedWorkOrders(ctx, txq, ids)
		if err != nil {
			return err
		}
		if mine != nil && mine.WorkOrderID != nil {
			if _, ok := orders[wos[*mine.WorkOrderID].OrderID]; !ok {
				return errRelock
			}
		}
		w := wos[req.Id]
		switch {
		case w.Status == workOrderDone || w.Status == workOrderCancelled:
			return workOrderClosed(w.Status)
		case w.Status == workOrderPending && !force:
			return conflict(codeWorkOrderPending, "An earlier work order is not done",
				"A work order this one depends on is not finished. Send force to start this one anyway; the log is marked as forced.")
		}
		if at != nil && *at != w.WorkCenterID {
			moved, err := startAtMember(ctx, txq, w, *at, o)
			if err != nil {
				return err
			}
			n.workCenters[w.WorkCenterID] = true
			w = moved
		}
		center, err := txq.GetWorkCenterForShare(ctx, w.WorkCenterID)
		if err != nil {
			return err
		}
		if center.WorkingState == stateBlocked {
			return conflict(codeWorkCenterBlocked, "The work center is blocked",
				"Nothing is started at a blocked work center. Unblock it first.")
		}
		now := s.deps.Clock()
		if mine != nil {
			locked, err := txq.LockTimeLogs(ctx, []int64{mine.ID})
			if err != nil {
				return err
			}
			if len(locked) == 1 && locked[0].EndedAt == nil {
				if locked[0].WorkOrderID != nil && *locked[0].WorkOrderID == w.ID && locked[0].Kind == logProductive {
					return nil // already running it (M29a): a no-op
				}
				if err := txq.EndTimeLog(ctx, store.EndTimeLogParams{ID: mine.ID, Now: now}); err != nil {
					return err
				}
				closedID = mine.ID
				n.workCenters[mine.WorkCenterID] = true
				if mine.WorkOrderID != nil && *mine.WorkOrderID != w.ID {
					if _, err := txq.SettleWorkOrder(ctx, *mine.WorkOrderID); err != nil && !noRows(err) {
						return err
					}
					other := wos[*mine.WorkOrderID]
					n.workOrder(other.ID, other.OrderID, other.WorkCenterID)
				}
			}
		}
		if _, err := txq.InsertTimeLog(ctx, store.InsertTimeLogParams{
			WorkOrderID: &w.ID, WorkCenterID: w.WorkCenterID, UserID: who.id, UserDisplay: who.display, Kind: logProductive,
			StartedAt: now, Forced: w.Status == workOrderPending,
		}); err != nil {
			return err
		}
		if _, err := txq.StartWorkOrder(ctx, store.StartWorkOrderParams{ID: w.ID, Now: now}); err != nil {
			return err
		}
		if err := txq.SetOrderInProgress(ctx, store.SetOrderInProgressParams{ID: o.ID, Now: now}); err != nil {
			return err
		}
		n.workOrder(w.ID, w.OrderID, w.WorkCenterID)
		// Started, a group work order leaves every member's Group work (M119).
		if err := n.groupWork(ctx, txq, w); err != nil {
			return err
		}
		return n.send(ctx, tx)
	})
	if db.IsUniqueViolation(err, openLogIndex) {
		err = conflict(codeLogConflict, "Another start of yours came first",
			"You started work somewhere else at the same moment. Reload and try again.")
	}
	if err != nil {
		return answer(err, startAnswers())
	}
	out, err := s.shopFloorResult(ctx, req.Id, closedID, 0)
	if err != nil {
		return answer(err, startAnswers())
	}
	return gen.StartManufacturingWorkOrder200JSONResponse(out), nil
}

// startAtMember moves a group work order to the member it is started at, from
// a stale or a current Group work list (manufacturing phase 2 M119): a center
// that is not an active member of its group, or any other center once the
// group is archived, is work_center_not_in_group; once a time log exists, or
// the work order is planned at its member, the member is fixed
// (work_order_member_fixed, naming it). Otherwise the member's figures are
// taken again (M118).
func startAtMember(ctx context.Context, txq *store.Queries, w store.ManufacturingWorkOrder, at int64, o store.ManufacturingOrder) (store.ManufacturingWorkOrder, error) {
	const title = "The work order was not started"
	if w.WorkCenterGroupID == nil {
		return w, notInGroup(title)
	}
	members, err := choosableMembers(ctx, txq, []int64{*w.WorkCenterGroupID})
	if err != nil {
		return w, err
	}
	i := slices.IndexFunc(members[*w.WorkCenterGroupID], func(c store.ManufacturingWorkCenter) bool {
		return c.ID == at && c.Status == statusActive
	})
	if i < 0 {
		return w, notInGroup(title)
	}
	logged, err := withLogs(ctx, txq, []int64{w.ID})
	if err != nil {
		return w, err
	}
	if logged[w.ID] || w.PlannedStart != nil {
		current, err := txq.GetWorkCenter(ctx, w.WorkCenterID)
		if err != nil {
			return w, err
		}
		return w, conflict(codeWorkOrderMemberFixed, "The work order is fixed at another work center",
			fmt.Sprintf("The work order runs at %s now; start it there.", current.Code))
	}
	return txq.SetWorkOrderMember(ctx, memberFigures(w, members[*w.WorkCenterGroupID][i], ratOrZero(o.Quantity)))
}

// pauseAnswers are pause's typed refusals.
func pauseAnswers() answers[gen.PauseManufacturingWorkOrderResponseObject] {
	return answers[gen.PauseManufacturingWorkOrderResponseObject]{
		forbidden: func(p gen.ManufacturingProblem) gen.PauseManufacturingWorkOrderResponseObject {
			return gen.PauseManufacturingWorkOrder403ApplicationProblemPlusJSONResponse(p)
		},
		notFound: func() gen.PauseManufacturingWorkOrderResponseObject {
			return gen.PauseManufacturingWorkOrder404Response{}
		},
		conflict: func(p gen.ManufacturingProblem) gen.PauseManufacturingWorkOrderResponseObject {
			return gen.PauseManufacturingWorkOrder409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.ManufacturingProblem) gen.PauseManufacturingWorkOrderResponseObject {
			return gen.PauseManufacturingWorkOrder503ApplicationProblemPlusJSONResponse(p)
		},
	}
}

// PauseManufacturingWorkOrder ends the caller's open log on the work order,
// productive or a block's downtime (M29, M29a); with no productive log left
// open it is paused. With none of the caller's there it is a no-op.
func (s *server) PauseManufacturingWorkOrder(ctx context.Context, req gen.PauseManufacturingWorkOrderRequestObject) (gen.PauseManufacturingWorkOrderResponseObject, error) {
	if err := s.requireShopFloor(ctx, "Pausing a work order"); err != nil {
		return answer(err, pauseAnswers())
	}
	pre, err := s.workOrderOrNotFound(ctx, req.Id)
	if err != nil {
		return answer(err, pauseAnswers())
	}
	who, err := s.actorOf(ctx)
	if err != nil {
		return nil, err
	}
	err = s.withOrder(ctx, pre.OrderID, nil, func(ctx context.Context, tx pgx.Tx, txq *store.Queries, lo *lockedOrder) error {
		n := newNotices()
		if !isOpen(lo.order.Status) {
			return orderNotOpen(lo.order.Status)
		}
		if _, err := lockedWorkOrders(ctx, txq, []int64{req.Id}); err != nil {
			return err
		}
		open, err := txq.OpenLogsOfWorkOrders(ctx, []int64{req.Id})
		if err != nil {
			return err
		}
		open = slices.DeleteFunc(open, func(l store.ManufacturingTimeLog) bool { return l.UserID == nil || *l.UserID != *who.id })
		if len(open) == 0 {
			return nil
		}
		if err := endLogs(ctx, txq, open, s.deps.Clock(), nil, nil, n); err != nil {
			return err
		}
		if _, err := txq.SettleWorkOrder(ctx, req.Id); err != nil && !noRows(err) {
			return err
		}
		n.workOrder(pre.ID, pre.OrderID, pre.WorkCenterID)
		return n.send(ctx, tx)
	})
	if err != nil {
		return answer(err, pauseAnswers())
	}
	out, err := s.shopFloorResult(ctx, req.Id, 0, 0)
	if err != nil {
		return answer(err, pauseAnswers())
	}
	return gen.PauseManufacturingWorkOrder200JSONResponse(out), nil
}

// finishAnswers are finish's typed refusals.
func finishAnswers() answers[gen.FinishManufacturingWorkOrderResponseObject] {
	return answers[gen.FinishManufacturingWorkOrderResponseObject]{
		badRequest: func(p gen.ManufacturingProblem) gen.FinishManufacturingWorkOrderResponseObject {
			return gen.FinishManufacturingWorkOrder400ApplicationProblemPlusJSONResponse(p)
		},
		forbidden: func(p gen.ManufacturingProblem) gen.FinishManufacturingWorkOrderResponseObject {
			return gen.FinishManufacturingWorkOrder403ApplicationProblemPlusJSONResponse(p)
		},
		notFound: func() gen.FinishManufacturingWorkOrderResponseObject {
			return gen.FinishManufacturingWorkOrder404Response{}
		},
		conflict: func(p gen.ManufacturingProblem) gen.FinishManufacturingWorkOrderResponseObject {
			return gen.FinishManufacturingWorkOrder409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.ManufacturingProblem) gen.FinishManufacturingWorkOrderResponseObject {
			return gen.FinishManufacturingWorkOrder503ApplicationProblemPlusJSONResponse(p)
		},
	}
}

// FinishManufacturingWorkOrder ends every open log on the work order, sets
// it done and readies the order's first work order not done or cancelled,
// when it is pending (M27, M29). A
// production in the body is posted in the same transaction, after the work
// order's own writes and with the ledger last (M26a), through the
// production's own rules (M22, M22a): a refusal changes nothing. A finish
// sent again with the production it posted answers 200 with it. A quantity
// is registered on the caller's open log: with none it is 400 on quantity.
func (s *server) FinishManufacturingWorkOrder(ctx context.Context, req gen.FinishManufacturingWorkOrderRequestObject) (gen.FinishManufacturingWorkOrderResponseObject, error) {
	if err := s.requireShopFloor(ctx, "Finishing a work order"); err != nil {
		return answer(err, finishAnswers())
	}
	f := s.unknownProperties(ctx, finishInputSchema)
	var body gen.ManufacturingFinishInput
	if req.Body != nil {
		body = *req.Body
	}
	var quantity *big.Rat
	if body.Quantity != nil {
		q, msg := parseDecimal(*body.Quantity, quantityPlaces, maxQuantity)
		f.addCoded("quantity", codeQuantityInvalid, msg)
		quantity = q
	}
	pre, err := s.workOrderOrNotFound(ctx, req.Id)
	if err != nil {
		return answer(err, finishAnswers())
	}
	var prep *preparedProduction
	var replayed int64
	if body.Production != nil {
		if prep, err = s.prepareProduction(ctx, pre.OrderID, *body.Production, f, "production."); err != nil {
			return answer(err, finishAnswers())
		}
		if prep.replay != nil {
			if replayed, err = replayOf(pre.OrderID, *prep.replay, prep.v.hash); err != nil {
				return answer(err, finishAnswers())
			}
			prep = nil
		}
	} else if f.failed() {
		return answer(refuseInvalid("The work order was not finished", f), finishAnswers())
	}
	who, err := s.actorOf(ctx)
	if err != nil {
		return nil, err
	}
	productionID, err := s.runProduction(ctx, pre.OrderID, nil, prep, who, func(ctx context.Context, tx pgx.Tx, txq *store.Queries, orders map[int64]*lockedOrder) error {
		n := newNotices()
		o := orders[pre.OrderID].order
		if !isOpen(o.Status) {
			return orderNotOpen(o.Status)
		}
		ids, err := txq.WorkOrderIDsOfOrders(ctx, []int64{o.ID})
		if err != nil {
			return err
		}
		wos, err := lockedWorkOrders(ctx, txq, ids)
		if err != nil {
			return err
		}
		w := wos[req.Id]
		switch w.Status {
		case workOrderDone:
			if replayed != 0 {
				return errAlreadyDone
			}
			return workOrderClosed(w.Status)
		case workOrderCancelled:
			return workOrderClosed(w.Status)
		case workOrderPending:
			return conflict(codeWorkOrderPending, "An earlier work order is not done",
				"A work order this one depends on is not finished. Start this one with force first.")
		}
		open, err := txq.OpenLogsOfWorkOrders(ctx, []int64{w.ID})
		if err != nil {
			return err
		}
		// The quantity goes on the caller's open log; without one it would
		// be lost, so it is refused on the field.
		if quantity != nil && !slices.ContainsFunc(open, func(l store.ManufacturingTimeLog) bool {
			return l.UserID != nil && *l.UserID == *who.id
		}) {
			return refuseField("The work order was not finished", "quantity", "", "You have no open time log on this work order to register it on")
		}
		// Every check of the work order has its result (M98).
		if err := finishGate(ctx, txq, w.ID); err != nil {
			return err
		}
		now := s.deps.Clock()
		if err := endLogs(ctx, txq, open, now, who.id, quantity, n); err != nil {
			return err
		}
		if _, err := txq.FinishWorkOrder(ctx, store.FinishWorkOrderParams{ID: w.ID, Now: now}); err != nil {
			return err
		}
		next, err := txq.ReadyWorkOrders(ctx, o.ID)
		if err != nil {
			return err
		}
		for _, x := range next {
			n.workOrder(x.ID, x.OrderID, x.WorkCenterID)
		}
		if err := n.groupWork(ctx, txq, next...); err != nil {
			return err
		}
		if err := txq.SetOrderInProgress(ctx, store.SetOrderInProgressParams{ID: o.ID, Now: now}); err != nil {
			return err
		}
		n.workOrder(w.ID, w.OrderID, w.WorkCenterID)
		return n.send(ctx, tx)
	})
	switch {
	case errors.Is(err, errAlreadyDone):
		productionID, err = replayed, nil
	case errors.Is(err, errReplayed):
		err = nil
	case err == nil && replayed != 0:
		productionID = replayed
	}
	if err != nil {
		return answer(err, finishAnswers())
	}
	out, err := s.shopFloorResult(ctx, req.Id, 0, productionID)
	if err != nil {
		return answer(err, finishAnswers())
	}
	return gen.FinishManufacturingWorkOrder200JSONResponse(out), nil
}

// quantityAnswers are Register quantity's typed refusals.
func quantityAnswers() answers[gen.RegisterManufacturingWorkOrderQuantityResponseObject] {
	return answers[gen.RegisterManufacturingWorkOrderQuantityResponseObject]{
		badRequest: func(p gen.ManufacturingProblem) gen.RegisterManufacturingWorkOrderQuantityResponseObject {
			return gen.RegisterManufacturingWorkOrderQuantity400ApplicationProblemPlusJSONResponse(p)
		},
		forbidden: func(p gen.ManufacturingProblem) gen.RegisterManufacturingWorkOrderQuantityResponseObject {
			return gen.RegisterManufacturingWorkOrderQuantity403ApplicationProblemPlusJSONResponse(p)
		},
		notFound: func() gen.RegisterManufacturingWorkOrderQuantityResponseObject {
			return gen.RegisterManufacturingWorkOrderQuantity404Response{}
		},
		conflict: func(p gen.ManufacturingProblem) gen.RegisterManufacturingWorkOrderQuantityResponseObject {
			return gen.RegisterManufacturingWorkOrderQuantity409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.ManufacturingProblem) gen.RegisterManufacturingWorkOrderQuantityResponseObject {
			return gen.RegisterManufacturingWorkOrderQuantity503ApplicationProblemPlusJSONResponse(p)
		},
	}
}

// quantityIndex is a registration's clientRequestId, once.
const quantityIndex = "ux_work_order_quantities_request"

// replayQuantity judges a registration sent again: the same work order and
// quantity is a replay (true), anything else client_request_reused.
func replayQuantity(r store.ManufacturingWorkOrderQuantity, workOrderID int64, quantity *big.Rat) (bool, error) {
	if r.WorkOrderID == workOrderID && ratOrZero(r.Quantity).Cmp(quantity) == 0 {
		return true, nil
	}
	return false, conflict(codeClientRequestReused, "The request id was used for another registration",
		"This clientRequestId registered another quantity or another work order before. Send a new one.")
}

// RegisterManufacturingWorkOrderQuantity is Register quantity (manufacturing
// phase 2 M116): good units the work order passes on to its successors, which
// posts nothing to stock. Each successor whose send-ahead quantity is now
// reached is readied (M114). The output work order — the last that is not
// cancelled — passes nothing on (work_order_is_output), and what a work order
// has passed on stays within the order's quantity (quantity_over_order). The
// work order's first-piece checks must have their results first (M98). Sent again with its clientRequestId it registers
// nothing more.
func (s *server) RegisterManufacturingWorkOrderQuantity(ctx context.Context, req gen.RegisterManufacturingWorkOrderQuantityRequestObject) (gen.RegisterManufacturingWorkOrderQuantityResponseObject, error) {
	const title = "The quantity was not registered"
	if err := s.requireShopFloor(ctx, "Registering a quantity"); err != nil {
		return answer(err, quantityAnswers())
	}
	f := s.unknownProperties(ctx, quantityInputSchema)
	quantity, msg := parsePositive(req.Body.Quantity, quantityPlaces, maxQuantity)
	f.addCoded("quantity", codeQuantityInvalid, msg)
	if f.failed() {
		return answer(refuseInvalid(title, f), quantityAnswers())
	}
	pre, err := s.workOrderOrNotFound(ctx, req.Id)
	if err != nil {
		return answer(err, quantityAnswers())
	}
	requestID := uuid.UUID(req.Body.ClientRequestId)
	replayed := false
	if r, err := store.New(s.deps.Pool).QuantityRegistration(ctx, requestID); err == nil {
		if replayed, err = replayQuantity(r, req.Id, quantity); err != nil {
			return answer(err, quantityAnswers())
		}
	} else if !noRows(err) {
		return nil, err
	}
	if !replayed {
		who, err := s.actorOf(ctx)
		if err != nil {
			return nil, err
		}
		err = s.withOrder(ctx, pre.OrderID, nil, func(ctx context.Context, tx pgx.Tx, txq *store.Queries, lo *lockedOrder) error {
			if !isOpen(lo.order.Status) {
				return orderNotOpen(lo.order.Status)
			}
			wos, err := lockedWorkOrders(ctx, txq, []int64{req.Id})
			if err != nil {
				return err
			}
			w := wos[req.Id]
			switch w.Status {
			case workOrderDone, workOrderCancelled:
				return workOrderClosed(w.Status)
			case workOrderPending:
				return conflict(codeWorkOrderPending, "The work order is not ready",
					"A work order this one depends on is not finished, and this one was not started. Nothing is passed on from it yet.")
			}
			all, err := txq.WorkOrdersOfOrder(ctx, w.OrderID)
			if err != nil {
				return err
			}
			if i := slices.IndexFunc(all, func(x store.ManufacturingWorkOrder) bool { return x.ID == w.ID }); i >= 0 &&
				!slices.ContainsFunc(all[i+1:], func(x store.ManufacturingWorkOrder) bool { return x.Status != workOrderCancelled }) {
				return conflict(codeWorkOrderIsOutput, "The work order makes the output",
					"The last work order passes nothing on: its good units are a production. Register a production instead.")
			}
			if total := new(big.Rat).Add(ratOrZero(w.QuantityDone), quantity); total.Cmp(ratOrZero(lo.order.Quantity)) > 0 {
				var f fieldErrors
				f.addCoded("quantity", codeQuantityOverOrder, fmt.Sprintf("At most %s more: the work order has passed on %s of the order's %s",
					numericText(numeric(new(big.Rat).Sub(ratOrZero(lo.order.Quantity), ratOrZero(w.QuantityDone)), quantityPlaces), quantityPlaces),
					numericText(w.QuantityDone, quantityPlaces), numericText(lo.order.Quantity, quantityPlaces)))
				return refuseInvalid(title, f)
			}
			missing, err := txq.MissingChecksOfWorkOrders(ctx, store.MissingChecksOfWorkOrdersParams{WorkOrderIds: []int64{w.ID},
				Frequencies: []string{frequencyFirstPiece}})
			if err != nil {
				return err
			}
			if len(missing) > 0 {
				return checksIncomplete("Checks are missing",
					fmt.Sprintf("%d first-piece check(s) of the work order have no result; record them before passing quantity on.", len(missing)), missing)
			}
			now := s.deps.Clock()
			if _, err := txq.InsertQuantityRegistration(ctx, store.InsertQuantityRegistrationParams{
				WorkOrderID: w.ID, ClientRequestID: requestID, Quantity: numeric(quantity, quantityPlaces),
				UserID: who.id, UserDisplay: who.display, Now: now,
			}); err != nil {
				return err
			}
			if _, err := txq.AddQuantityDone(ctx, store.AddQuantityDoneParams{ID: w.ID, Quantity: numeric(quantity, quantityPlaces)}); err != nil {
				return err
			}
			n := newNotices()
			readied, err := txq.ReadyWorkOrders(ctx, w.OrderID)
			if err != nil {
				return err
			}
			for _, x := range readied {
				n.workOrder(x.ID, x.OrderID, x.WorkCenterID)
			}
			if err := n.groupWork(ctx, txq, readied...); err != nil {
				return err
			}
			n.workOrder(w.ID, w.OrderID, w.WorkCenterID)
			return n.send(ctx, tx)
		})
		if db.IsUniqueViolation(err, quantityIndex) {
			// A replay that raced the first: judged against what it wrote.
			r, rerr := store.New(s.deps.Pool).QuantityRegistration(ctx, requestID)
			if rerr != nil {
				return nil, rerr
			}
			_, err = replayQuantity(r, req.Id, quantity)
		}
		if err != nil {
			return answer(err, quantityAnswers())
		}
	}
	out, err := s.shopFloorResult(ctx, req.Id, 0, 0)
	if err != nil {
		return answer(err, quantityAnswers())
	}
	return gen.RegisterManufacturingWorkOrderQuantity200JSONResponse(out), nil
}

// scrapAnswers are scrap's typed refusals.
func scrapAnswers() answers[gen.ReportManufacturingWorkOrderScrapResponseObject] {
	return answers[gen.ReportManufacturingWorkOrderScrapResponseObject]{
		badRequest: func(p gen.ManufacturingProblem) gen.ReportManufacturingWorkOrderScrapResponseObject {
			return gen.ReportManufacturingWorkOrderScrap400ApplicationProblemPlusJSONResponse(p)
		},
		forbidden: func(p gen.ManufacturingProblem) gen.ReportManufacturingWorkOrderScrapResponseObject {
			return gen.ReportManufacturingWorkOrderScrap403ApplicationProblemPlusJSONResponse(p)
		},
		notFound: func() gen.ReportManufacturingWorkOrderScrapResponseObject {
			return gen.ReportManufacturingWorkOrderScrap404Response{}
		},
		conflict: func(p gen.ManufacturingProblem) gen.ReportManufacturingWorkOrderScrapResponseObject {
			return gen.ReportManufacturingWorkOrderScrap409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.ManufacturingProblem) gen.ReportManufacturingWorkOrderScrapResponseObject {
			return gen.ReportManufacturingWorkOrderScrap503ApplicationProblemPlusJSONResponse(p)
		},
	}
}

// ReportManufacturingWorkOrderScrap posts a production of the work order's
// order with no good output, only scrap (M22, M31), through the
// production's own post: idempotent by clientRequestId.
func (s *server) ReportManufacturingWorkOrderScrap(ctx context.Context, req gen.ReportManufacturingWorkOrderScrapRequestObject) (gen.ReportManufacturingWorkOrderScrapResponseObject, error) {
	if err := s.requireShopFloor(ctx, "Reporting scrap"); err != nil {
		return answer(err, scrapAnswers())
	}
	f := s.unknownProperties(ctx, scrapInputSchema)
	in := gen.ManufacturingProductionInput{ClientRequestId: req.Body.ClientRequestId, Quantity: "0", OutputScrap: req.Body.OutputScrap,
		Lines: make([]gen.ManufacturingProductionLineInput, 0, len(req.Body.Lines))}
	for i, l := range req.Body.Lines {
		_, msg := parsePositive(l.Scrap, quantityPlaces, maxQuantity)
		f.addCoded(fmt.Sprintf("lines[%d].scrap", i), codeQuantityInvalid, msg)
		scrap := l.Scrap
		in.Lines = append(in.Lines, gen.ManufacturingProductionLineInput{ComponentId: l.ComponentId, Scrap: &scrap})
	}
	if f.failed() {
		return answer(refuseInvalid(productionTitle, f), scrapAnswers())
	}
	pre, err := s.workOrderOrNotFound(ctx, req.Id)
	if err != nil {
		return answer(err, scrapAnswers())
	}
	prep, err := s.prepareProduction(ctx, pre.OrderID, in, fieldErrors{}, "")
	if err != nil {
		return answer(err, scrapAnswers())
	}
	replayed := prep.replay != nil
	var id int64
	if replayed {
		id, err = replayOf(pre.OrderID, *prep.replay, prep.v.hash)
	} else {
		var who actor
		if who, err = s.actorOf(ctx); err != nil {
			return nil, err
		}
		id, err = s.runProduction(ctx, pre.OrderID, nil, prep, who, func(ctx context.Context, tx pgx.Tx, _ *store.Queries, _ map[int64]*lockedOrder) error {
			n := newNotices()
			n.workOrder(pre.ID, pre.OrderID, pre.WorkCenterID)
			return n.send(ctx, tx)
		})
		if errors.Is(err, errReplayed) {
			replayed, err = true, nil
		}
	}
	if err != nil {
		return answer(err, scrapAnswers())
	}
	out, err := s.productionAnswer(ctx, id)
	if err != nil {
		return nil, err
	}
	if replayed {
		return gen.ReportManufacturingWorkOrderScrap200JSONResponse(out), nil
	}
	return gen.ReportManufacturingWorkOrderScrap201JSONResponse(out), nil
}

// centerLogOrders is idsOf for a block or an unblock: the orders of the work
// orders with an open log at the work center, read on q.
func centerLogOrders(centerID int64) func(context.Context, *store.Queries) ([]int64, error) {
	return func(ctx context.Context, q *store.Queries) ([]int64, error) {
		open, err := q.OpenLogsAtWorkCenter(ctx, centerID)
		if err != nil {
			return nil, err
		}
		var ids []int64
		for _, l := range open {
			if l.WorkOrderID == nil {
				continue
			}
			w, err := q.GetWorkOrder(ctx, *l.WorkOrderID)
			if err != nil {
				return nil, err
			}
			ids = append(ids, w.OrderID)
		}
		return ids, nil
	}
}

// withCenter runs fn on work center id under the lock order (M26a): the
// orders of the work orders with an open log at the center, those work
// orders, then the center FOR UPDATE, then a re-read of its open logs —
// errRelock when a log is now on a work order not locked, so the write
// starts again — and the logs locked in id order. A start needs the center
// FOR SHARE, so once the center is held no log opens at it.
func (s *server) withCenter(ctx context.Context, id int64,
	fn func(ctx context.Context, tx pgx.Tx, txq *store.Queries, center store.ManufacturingWorkCenter, open []store.ManufacturingTimeLog, wos map[int64]store.ManufacturingWorkOrder) error) error {
	return s.withOrders(ctx, centerLogOrders(id), nil, func(ctx context.Context, tx pgx.Tx, txq *store.Queries, orders map[int64]*lockedOrder) error {
		before, err := txq.OpenLogsAtWorkCenter(ctx, id)
		if err != nil {
			return err
		}
		var ids []int64
		for _, l := range before {
			if l.WorkOrderID != nil {
				ids = append(ids, *l.WorkOrderID)
			}
		}
		wos, err := lockedWorkOrders(ctx, txq, ids)
		if err != nil {
			return err
		}
		for _, w := range wos {
			if _, ok := orders[w.OrderID]; !ok {
				return errRelock
			}
		}
		center, err := txq.GetWorkCenterForUpdate(ctx, id)
		if noRows(err) {
			return notFound()
		}
		if err != nil {
			return err
		}
		open, err := txq.OpenLogsAtWorkCenter(ctx, id)
		if err != nil {
			return err
		}
		logIDs := make([]int64, 0, len(open))
		for _, l := range open {
			if l.WorkOrderID != nil {
				if _, ok := wos[*l.WorkOrderID]; !ok {
					return errRelock
				}
			}
			logIDs = append(logIDs, l.ID)
		}
		locked, err := txq.LockTimeLogs(ctx, logIDs)
		if err != nil {
			return err
		}
		locked = slices.DeleteFunc(locked, func(l store.ManufacturingTimeLog) bool { return l.EndedAt != nil })
		return fn(ctx, tx, txq, center, locked, wos)
	})
}

// blockAnswers are block's typed refusals.
func blockAnswers() answers[gen.BlockManufacturingWorkCenterResponseObject] {
	return answers[gen.BlockManufacturingWorkCenterResponseObject]{
		badRequest: func(p gen.ManufacturingProblem) gen.BlockManufacturingWorkCenterResponseObject {
			return gen.BlockManufacturingWorkCenter400ApplicationProblemPlusJSONResponse(p)
		},
		forbidden: func(p gen.ManufacturingProblem) gen.BlockManufacturingWorkCenterResponseObject {
			return gen.BlockManufacturingWorkCenter403ApplicationProblemPlusJSONResponse(p)
		},
		notFound: func() gen.BlockManufacturingWorkCenterResponseObject {
			return gen.BlockManufacturingWorkCenter404Response{}
		},
		conflict: func(p gen.ManufacturingProblem) gen.BlockManufacturingWorkCenterResponseObject {
			return gen.BlockManufacturingWorkCenter409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.ManufacturingProblem) gen.BlockManufacturingWorkCenterResponseObject {
			return gen.BlockManufacturingWorkCenter503ApplicationProblemPlusJSONResponse(p)
		},
	}
}

// downtimeReasons are M29's reasons.
var downtimeReasons = []gen.ManufacturingDowntimeReason{gen.Breakdown, gen.MaterialMissing, gen.Setup, gen.WaitingForOperator, gen.Quality, gen.Other}

// BlockManufacturingWorkCenter blocks a work center (M30, M29a): every
// productive log at it ends now and becomes one downtime log per operator
// from now with the reason, and the center's own downtime log, with no user,
// opens. Every one of them records the blocker as note_by: the note is their
// words, and the center's log says who blocked it. Its work orders are left paused. Blocking a blocked center is a
// no-op.
func (s *server) BlockManufacturingWorkCenter(ctx context.Context, req gen.BlockManufacturingWorkCenterRequestObject) (gen.BlockManufacturingWorkCenterResponseObject, error) {
	if err := s.requireShopFloor(ctx, "Blocking a work center"); err != nil {
		return answer(err, blockAnswers())
	}
	f := s.unknownProperties(ctx, blockInputSchema)
	if !slices.Contains(downtimeReasons, req.Body.Reason) {
		f.add("reason", "One of breakdown, material_missing, setup, waiting_for_operator, quality, other")
	}
	note := ""
	if req.Body.Note != nil {
		note = strings.TrimSpace(*req.Body.Note)
		if utf8.RuneCountInString(note) > maxLogNote {
			f.add("note", "At most 2000 characters")
		}
	}
	if f.failed() {
		return answer(refuseInvalid(blockTitle, f), blockAnswers())
	}
	if _, err := store.New(s.deps.Pool).GetWorkCenter(ctx, req.Id); noRows(err) {
		return gen.BlockManufacturingWorkCenter404Response{}, nil
	} else if err != nil {
		return nil, err
	}
	reason := string(req.Body.Reason)
	who, err := s.actorOf(ctx)
	if err != nil {
		return nil, err
	}
	err = s.withCenter(ctx, req.Id, func(ctx context.Context, tx pgx.Tx, txq *store.Queries, center store.ManufacturingWorkCenter,
		open []store.ManufacturingTimeLog, wos map[int64]store.ManufacturingWorkOrder) error {
		if center.WorkingState == stateBlocked {
			return nil
		}
		n := newNotices()
		n.workCenters[center.ID] = true
		n.sites[center.SiteID] = true // the board greys a blocked center (M36)
		now := s.deps.Clock()
		for _, l := range open {
			if l.Kind != logProductive || l.UserID == nil {
				continue
			}
			if err := txq.EndTimeLog(ctx, store.EndTimeLogParams{ID: l.ID, Now: now}); err != nil {
				return err
			}
			if _, err := txq.InsertTimeLog(ctx, store.InsertTimeLogParams{
				WorkOrderID: l.WorkOrderID, WorkCenterID: center.ID, UserID: l.UserID, UserDisplay: l.UserDisplay, Kind: logDowntime,
				Reason: &reason, Note: note, NoteBy: who.id, NoteByDisplay: who.display, StartedAt: now,
			}); err != nil {
				return err
			}
		}
		if _, err := txq.InsertTimeLog(ctx, store.InsertTimeLogParams{
			WorkCenterID: center.ID, Kind: logDowntime, Reason: &reason, Note: note, NoteBy: who.id, NoteByDisplay: who.display, StartedAt: now,
		}); err != nil {
			return err
		}
		if _, err := txq.SetWorkingState(ctx, store.SetWorkingStateParams{ID: center.ID, WorkingState: stateBlocked, Now: now}); err != nil {
			return err
		}
		for _, id := range slices.Sorted(maps.Keys(wos)) {
			if _, err := txq.SettleWorkOrder(ctx, id); err != nil && !noRows(err) {
				return err
			}
			n.workOrder(id, wos[id].OrderID, wos[id].WorkCenterID)
		}
		return n.send(ctx, tx)
	})
	if err != nil {
		return answer(err, blockAnswers())
	}
	out, err := s.queueAnswer(ctx, req.Id)
	if err != nil {
		return answer(err, blockAnswers())
	}
	return gen.BlockManufacturingWorkCenter200JSONResponse(out), nil
}

// unblockAnswers are unblock's typed refusals.
func unblockAnswers() answers[gen.UnblockManufacturingWorkCenterResponseObject] {
	return answers[gen.UnblockManufacturingWorkCenterResponseObject]{
		forbidden: func(p gen.ManufacturingProblem) gen.UnblockManufacturingWorkCenterResponseObject {
			return gen.UnblockManufacturingWorkCenter403ApplicationProblemPlusJSONResponse(p)
		},
		notFound: func() gen.UnblockManufacturingWorkCenterResponseObject {
			return gen.UnblockManufacturingWorkCenter404Response{}
		},
		conflict: func(p gen.ManufacturingProblem) gen.UnblockManufacturingWorkCenterResponseObject {
			return gen.UnblockManufacturingWorkCenter409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.ManufacturingProblem) gen.UnblockManufacturingWorkCenterResponseObject {
			return gen.UnblockManufacturingWorkCenter503ApplicationProblemPlusJSONResponse(p)
		},
	}
}

// UnblockManufacturingWorkCenter unblocks a work center (M30, M29a): every
// open downtime log at it ends now, the operators' and its own. Unblocking a
// center that is not blocked is a no-op.
func (s *server) UnblockManufacturingWorkCenter(ctx context.Context, req gen.UnblockManufacturingWorkCenterRequestObject) (gen.UnblockManufacturingWorkCenterResponseObject, error) {
	if err := s.requireShopFloor(ctx, "Unblocking a work center"); err != nil {
		return answer(err, unblockAnswers())
	}
	if _, err := store.New(s.deps.Pool).GetWorkCenter(ctx, req.Id); noRows(err) {
		return gen.UnblockManufacturingWorkCenter404Response{}, nil
	} else if err != nil {
		return nil, err
	}
	err := s.withCenter(ctx, req.Id, func(ctx context.Context, tx pgx.Tx, txq *store.Queries, center store.ManufacturingWorkCenter,
		open []store.ManufacturingTimeLog, wos map[int64]store.ManufacturingWorkOrder) error {
		if center.WorkingState != stateBlocked {
			return nil
		}
		n := newNotices()
		n.workCenters[center.ID] = true
		n.sites[center.SiteID] = true
		now := s.deps.Clock()
		for _, l := range open {
			if l.Kind != logDowntime {
				continue
			}
			if err := txq.EndTimeLog(ctx, store.EndTimeLogParams{ID: l.ID, Now: now}); err != nil {
				return err
			}
		}
		if _, err := txq.SetWorkingState(ctx, store.SetWorkingStateParams{ID: center.ID, WorkingState: stateNormal, Now: now}); err != nil {
			return err
		}
		for _, w := range wos {
			n.workOrder(w.ID, w.OrderID, w.WorkCenterID)
		}
		return n.send(ctx, tx)
	})
	if err != nil {
		return answer(err, unblockAnswers())
	}
	out, err := s.queueAnswer(ctx, req.Id)
	if err != nil {
		return answer(err, unblockAnswers())
	}
	return gen.UnblockManufacturingWorkCenter200JSONResponse(out), nil
}

// ---- the answers --------------------------------------------------------

// workOrderRead is what the work orders' answers read of this module, in one
// snapshot.
type workOrderRead struct {
	rows       []store.WorkOrderRowsRow                      // in the order asked for
	components map[int64][]store.ManufacturingOrderComponent // by order
	logs       map[int64][]store.ManufacturingTimeLog        // by work order, by start
	last       map[int64]int64                               // by order: its last work order not cancelled
	centers    map[int64]store.ManufacturingWorkCenter
	checks     map[int64][]gen.ManufacturingWorkOrderCheck // by work order, by sequence (M96)
	graph      workOrderGraph                              // the orders' work order graphs (M113)
	groups     map[int64]store.ManufacturingWorkCenterGroup
}

// readWorkOrders reads the work orders ids names, in that order, with their
// orders' component lines, their logs, each order's last work order and
// their work centers.
func readWorkOrders(ctx context.Context, q *store.Queries, ids []int64) (workOrderRead, error) {
	r := workOrderRead{components: map[int64][]store.ManufacturingOrderComponent{}, logs: map[int64][]store.ManufacturingTimeLog{},
		last: map[int64]int64{}, centers: map[int64]store.ManufacturingWorkCenter{}, groups: map[int64]store.ManufacturingWorkCenterGroup{},
		graph: graphOf(nil)}
	if len(ids) == 0 {
		return r, nil
	}
	rows, err := q.WorkOrderRows(ctx, ids)
	if err != nil {
		return r, err
	}
	byID := map[int64]store.WorkOrderRowsRow{}
	var orderIDs, centerIDs, groupIDs []int64
	for _, row := range rows {
		byID[row.ManufacturingWorkOrder.ID] = row
		orderIDs = append(orderIDs, row.ManufacturingWorkOrder.OrderID)
		centerIDs = append(centerIDs, row.ManufacturingWorkOrder.WorkCenterID)
		if g := row.ManufacturingWorkOrder.WorkCenterGroupID; g != nil {
			groupIDs = append(groupIDs, *g)
		}
	}
	for _, id := range ids {
		if row, ok := byID[id]; ok {
			r.rows = append(r.rows, row)
		}
	}
	orderIDs = slices.Compact(slices.Sorted(slices.Values(orderIDs)))
	centerIDs = slices.Compact(slices.Sorted(slices.Values(centerIDs)))
	components, err := q.ComponentsOfOrders(ctx, orderIDs)
	if err != nil {
		return r, err
	}
	for _, c := range components {
		r.components[c.OrderID] = append(r.components[c.OrderID], c)
	}
	logs, err := q.LogsOfWorkOrders(ctx, ids)
	if err != nil {
		return r, err
	}
	for _, l := range logs {
		r.logs[*l.WorkOrderID] = append(r.logs[*l.WorkOrderID], l)
	}
	last, err := q.LastWorkOrderOfOrders(ctx, orderIDs)
	if err != nil {
		return r, err
	}
	for _, l := range last {
		r.last[l.OrderID] = l.ID
	}
	centers, err := q.WorkCentersByIDs(ctx, centerIDs)
	if err != nil {
		return r, err
	}
	for _, c := range centers {
		r.centers[c.ID] = c
	}
	checks, err := q.ChecksOfWorkOrders(ctx, ids)
	if err != nil {
		return r, err
	}
	r.checks = workOrderChecksOf(checks)
	if r.graph, err = readGraph(ctx, q, orderIDs); err != nil {
		return r, err
	}
	groups, err := q.WorkCenterGroupsByIDs(ctx, orEmpty(groupIDs))
	if err != nil {
		return r, err
	}
	for _, g := range groups {
		r.groups[g.ID] = g
	}
	return r, nil
}

// workOrdersOf is r's work orders as answers, with the catalog read after
// the snapshot — the orders' variants and more — whose entries it returns.
func (s *server) workOrdersOf(ctx context.Context, r workOrderRead, more []int32) ([]gen.ManufacturingWorkOrder, map[int32]contracts.VariantEntry, error) {
	ids := slices.Clone(more)
	for _, row := range r.rows {
		ids = append(ids, row.OrderVariantID)
	}
	entries, err := s.catalogVariants(ctx, slices.Compact(slices.Sorted(slices.Values(ids))))
	if err != nil {
		return nil, nil, err
	}
	costs := s.has(ctx, permCostsView)
	now := s.deps.Clock()
	out := make([]gen.ManufacturingWorkOrder, 0, len(r.rows))
	for _, row := range r.rows {
		out = append(out, workOrderOf(row, r, entries, costs, now))
	}
	return out, entries, nil
}

// workOrderOf is one work order as an answer.
func workOrderOf(row store.WorkOrderRowsRow, r workOrderRead, entries map[int32]contracts.VariantEntry, costs bool, now time.Time) gen.ManufacturingWorkOrder {
	w := row.ManufacturingWorkOrder
	order := store.ManufacturingOrder{ID: w.OrderID, Status: row.OrderStatus}
	ref := gen.ManufacturingWorkOrderOrder{Id: w.OrderID, Number: orderNumber(row.OrderNumber, row.OrderSuffix),
		Variant: refOf(entries, row.OrderVariantID, costs), Quantity: numericText(row.OrderQuantity, quantityPlaces),
		Produced:  numericText(row.OrderProduced, quantityPlaces),
		Remaining: text(positive(new(big.Rat).Sub(ratOrZero(row.OrderQuantity), ratOrZero(row.OrderProduced))), quantityPlaces),
		Priority:  gen.ManufacturingPriority(row.OrderPriority), Status: gen.ManufacturingOrderStatus(row.OrderStatus)}
	if row.OrderDueDate.Valid {
		ref.DueDate = &openapi_types.Date{Time: row.OrderDueDate.Time}
	}
	if status := reservationStatusOf(order, r.components[w.OrderID]); status != nil {
		rs := gen.ManufacturingReservationStatus(*status)
		ref.ReservationStatus = &rs
	}
	out := gen.ManufacturingWorkOrder{Id: w.ID, OrderId: w.OrderID, Order: ref, Sequence: w.Sequence, Name: w.Name,
		WorkCenter: centerRefOf(r.centers[w.WorkCenterID], w.WorkCenterID), SetupMinutes: numericText(w.SetupMinutes, minutePlaces),
		CleanupMinutes: numericText(w.CleanupMinutes, minutePlaces), MinutesPerCycle: numericText(w.MinutesPerCycle, minutePlaces),
		Capacity: w.Capacity, TimeEfficiency: w.TimeEfficiency, ExpectedMinutes: w.ExpectedMinutes, PlannedStart: w.PlannedStart,
		PlannedFinish: w.PlannedFinish, Status: gen.ManufacturingWorkOrderStatus(w.Status), StartedAt: w.StartedAt,
		FinishedAt: w.FinishedAt, Last: r.last[w.OrderID] == w.ID, Operators: []gen.ManufacturingWorkOrderOperator{}, Revision: w.Revision}
	var logged time.Duration
	for _, l := range r.logs[w.ID] {
		if l.Kind == logProductive {
			end := now
			if l.EndedAt != nil {
				end = *l.EndedAt
			}
			if end.After(l.StartedAt) {
				logged += end.Sub(l.StartedAt)
			}
		}
		if l.EndedAt == nil && l.UserID != nil {
			out.Operators = append(out.Operators, gen.ManufacturingWorkOrderOperator{
				User: gen.ManufacturingPerson{Id: *l.UserID, DisplayName: l.UserDisplay}, Kind: gen.ManufacturingTimeLogKind(l.Kind), StartedAt: l.StartedAt})
		}
	}
	checks := r.checks[w.ID]
	if checks == nil {
		checks = []gen.ManufacturingWorkOrderCheck{}
	}
	out.Checks = &checks
	depends := slices.Clone(r.graph.preds[w.ID])
	if depends == nil {
		depends = []int64{}
	}
	slices.Sort(depends)
	out.DependsOn = &depends
	done := numericText(w.QuantityDone, quantityPlaces)
	out.QuantityDone = &done
	if q := ratOf(w.SendAheadQuantity); q != nil {
		t := text(q, quantityPlaces)
		out.SendAheadQuantity = &t
	}
	fixed := false
	if w.WorkCenterGroupID != nil {
		g := r.groups[*w.WorkCenterGroupID]
		out.WorkCenterGroup = &gen.ManufacturingWorkCenterGroupRef{Id: *w.WorkCenterGroupID, Code: g.Code, Name: g.Name}
		fixed = len(r.logs[w.ID]) > 0
	}
	out.MemberFixed = &fixed
	out.LoggedMinutes = int64(logged / time.Minute)
	out.ActualMinutes, out.DeviationMinutes, out.DeviationPercent = workOrderDuration(w, r.logs[w.ID], now)
	return out
}

// centerRefOf is a work center as a work order names it.
func centerRefOf(c store.ManufacturingWorkCenter, id int64) gen.ManufacturingWorkCenterRef {
	return gen.ManufacturingWorkCenterRef{Id: id, Code: c.Code, Name: c.Name, SiteId: c.SiteID,
		WorkingState: gen.ManufacturingWorkingState(cmp.Or(c.WorkingState, stateNormal))}
}

// timeLogOf is a log as an answer: longRunning when it has been open for
// more than 12 hours (M29).
func timeLogOf(l store.ManufacturingTimeLog, now time.Time) gen.ManufacturingTimeLog {
	out := gen.ManufacturingTimeLog{Id: l.ID, WorkOrderId: l.WorkOrderID, WorkCenterId: l.WorkCenterID,
		Kind: gen.ManufacturingTimeLogKind(l.Kind), Note: l.Note, StartedAt: l.StartedAt, EndedAt: l.EndedAt, Forced: l.Forced,
		CorrectedAt: l.CorrectedAt, LongRunning: l.EndedAt == nil && now.Sub(l.StartedAt) > longRunningAfter}
	if l.UserID != nil {
		out.User = &gen.ManufacturingPerson{Id: *l.UserID, DisplayName: l.UserDisplay}
	}
	if l.Reason != nil {
		reason := gen.ManufacturingDowntimeReason(*l.Reason)
		out.Reason = &reason
	}
	if q := ratOf(l.Quantity); q != nil {
		t := text(q, quantityPlaces)
		out.Quantity = &t
	}
	if l.CorrectedBy != nil {
		out.CorrectedBy = &gen.ManufacturingPerson{Id: *l.CorrectedBy, DisplayName: l.CorrectedByDisplay}
	}
	return out
}

// timeLogsOf is logs as answers.
func timeLogsOf(logs []store.ManufacturingTimeLog, now time.Time) []gen.ManufacturingTimeLog {
	out := make([]gen.ManufacturingTimeLog, 0, len(logs))
	for _, l := range logs {
		out = append(out, timeLogOf(l, now))
	}
	return out
}

// blockOf is the work center's block from its open logs: its own downtime
// log, with neither a user nor a work order (M30), and who blocked it, its
// note_by; nil when none is open.
func blockOf(open []store.ManufacturingTimeLog) *gen.ManufacturingWorkCenterBlock {
	for _, l := range open {
		if l.UserID == nil && l.WorkOrderID == nil && l.Reason != nil {
			b := &gen.ManufacturingWorkCenterBlock{Reason: gen.ManufacturingDowntimeReason(*l.Reason), Note: l.Note, Since: l.StartedAt}
			if l.NoteBy != nil {
				b.By = &gen.ManufacturingPerson{Id: *l.NoteBy, DisplayName: l.NoteByDisplay}
			}
			return b
		}
	}
	return nil
}

// queueAnswer is a work center's queue (M31).
func (s *server) queueAnswer(ctx context.Context, id int64) (gen.ManufacturingWorkCenterQueue, error) {
	var center store.ManufacturingWorkCenter
	var r workOrderRead
	var open []store.ManufacturingTimeLog
	var groupWork map[int64]bool
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		var err error
		if center, err = q.GetWorkCenter(ctx, id); noRows(err) {
			return notFound()
		} else if err != nil {
			return err
		}
		ids, err := q.WorkCenterQueue(ctx, id)
		if err != nil {
			return err
		}
		group, err := q.GroupWorkQueue(ctx, id)
		if err != nil {
			return err
		}
		groupWork = map[int64]bool{}
		for _, g := range group {
			groupWork[g] = true
		}
		if r, err = readWorkOrders(ctx, q, append(ids, group...)); err != nil {
			return err
		}
		open, err = q.OpenLogsAtWorkCenter(ctx, id)
		return err
	})
	if err != nil {
		return gen.ManufacturingWorkCenterQueue{}, err
	}
	all, _, err := s.workOrdersOf(ctx, r, nil)
	if err != nil {
		return gen.ManufacturingWorkCenterQueue{}, err
	}
	items := make([]gen.ManufacturingWorkOrder, 0, len(all))
	group := []gen.ManufacturingWorkOrder{}
	for _, w := range all {
		if groupWork[w.Id] {
			group = append(group, w)
		} else {
			items = append(items, w)
		}
	}
	return gen.ManufacturingWorkCenterQueue{WorkCenter: centerRefOf(center, center.ID), Block: blockOf(open), Items: items, GroupWork: &group}, nil
}

// GetManufacturingWorkCenterQueue is the work center page's queue (M31).
func (s *server) GetManufacturingWorkCenterQueue(ctx context.Context, req gen.GetManufacturingWorkCenterQueueRequestObject) (gen.GetManufacturingWorkCenterQueueResponseObject, error) {
	out, err := s.queueAnswer(ctx, req.Id)
	if err != nil {
		return answer(err, answers[gen.GetManufacturingWorkCenterQueueResponseObject]{
			notFound: func() gen.GetManufacturingWorkCenterQueueResponseObject {
				return gen.GetManufacturingWorkCenterQueue404Response{}
			},
			unavailable: func(p gen.ManufacturingProblem) gen.GetManufacturingWorkCenterQueueResponseObject {
				return gen.GetManufacturingWorkCenterQueue503ApplicationProblemPlusJSONResponse(p)
			},
		})
	}
	return gen.GetManufacturingWorkCenterQueue200JSONResponse(out), nil
}

// workOrderView is the work order view (M31): the work order, its
// instructions, the components consumed at its operation with the order's
// live availability (M21a), its open logs, the caller's own open log and the
// work center's block.
func (s *server) workOrderView(ctx context.Context, id int64) (gen.ManufacturingWorkOrderView, error) {
	var r workOrderRead
	var mine *store.ManufacturingTimeLog
	var centerOpen []store.ManufacturingTimeLog
	caller := callerID(ctx)
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		var err error
		if r, err = readWorkOrders(ctx, q, []int64{id}); err != nil {
			return err
		}
		if len(r.rows) == 0 {
			return notFound()
		}
		if mine, err = openLogOf(ctx, q, &caller); err != nil {
			return err
		}
		centerOpen, err = q.OpenLogsAtWorkCenter(ctx, r.rows[0].ManufacturingWorkOrder.WorkCenterID)
		return err
	})
	if err != nil {
		return gen.ManufacturingWorkOrderView{}, err
	}
	row := r.rows[0]
	w := row.ManufacturingWorkOrder
	components := r.components[w.OrderID]
	variants := make([]int32, 0, len(components))
	for _, c := range components {
		variants = append(variants, c.VariantID)
	}
	items, entries, err := s.workOrdersOf(ctx, r, variants)
	if err != nil {
		return gen.ManufacturingWorkOrderView{}, err
	}
	var available map[int32]contracts.StockQuantity
	if len(components) > 0 {
		site := row.OrderSiteID
		if available, err = s.stockAvailable(ctx, variants, &site); err != nil {
			return gen.ManufacturingWorkOrderView{}, err
		}
	}
	now := s.deps.Clock()
	costs := s.has(ctx, permCostsView)
	cov := coverageOf(components, available)
	out := gen.ManufacturingWorkOrderView{WorkOrder: items[0], Instructions: w.Instructions, Components: []gen.ManufacturingOrderComponent{},
		OpenLogs: []gen.ManufacturingTimeLog{}, Block: blockOf(centerOpen)}
	for i, c := range components {
		if c.OperationID != nil && w.OperationID != nil && *c.OperationID == *w.OperationID {
			out.Components = append(out.Components, componentAnswer(c, entries, costs, cov.available[c.VariantID], cov.missing[i]))
		}
	}
	for _, l := range r.logs[w.ID] {
		if l.EndedAt == nil {
			out.OpenLogs = append(out.OpenLogs, timeLogOf(l, now))
		}
	}
	if mine != nil {
		l := timeLogOf(*mine, now)
		out.MyOpenLog = &l
	}
	return out, nil
}

// GetManufacturingWorkOrder is the work order view (M31).
func (s *server) GetManufacturingWorkOrder(ctx context.Context, req gen.GetManufacturingWorkOrderRequestObject) (gen.GetManufacturingWorkOrderResponseObject, error) {
	out, err := s.workOrderView(ctx, req.Id)
	if err != nil {
		return answer(err, answers[gen.GetManufacturingWorkOrderResponseObject]{
			notFound: func() gen.GetManufacturingWorkOrderResponseObject { return gen.GetManufacturingWorkOrder404Response{} },
			unavailable: func(p gen.ManufacturingProblem) gen.GetManufacturingWorkOrderResponseObject {
				return gen.GetManufacturingWorkOrder503ApplicationProblemPlusJSONResponse(p)
			},
		})
	}
	return gen.GetManufacturingWorkOrder200JSONResponse(out), nil
}

// shopFloorResult is a shop-floor action's answer, read after its
// transaction: the work order view, the log a start ended elsewhere and the
// production a finish posted.
func (s *server) shopFloorResult(ctx context.Context, id, closedLogID, productionID int64) (gen.ManufacturingShopFloorResult, error) {
	view, err := s.workOrderView(ctx, id)
	if err != nil {
		return gen.ManufacturingShopFloorResult{}, err
	}
	out := gen.ManufacturingShopFloorResult{WorkOrder: view}
	if closedLogID != 0 {
		l, err := store.New(s.deps.Pool).GetTimeLog(ctx, closedLogID)
		if err != nil {
			return out, err
		}
		answer := timeLogOf(l, s.deps.Clock())
		out.ClosedLog = &answer
	}
	if productionID != 0 {
		p, err := s.productionAnswer(ctx, productionID)
		if err != nil {
			return out, err
		}
		out.Production = &p
	}
	return out, nil
}

// ListManufacturingOrderWorkOrders is the order's work orders by sequence
// (M27).
func (s *server) ListManufacturingOrderWorkOrders(ctx context.Context, req gen.ListManufacturingOrderWorkOrdersRequestObject) (gen.ListManufacturingOrderWorkOrdersResponseObject, error) {
	var r workOrderRead
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		if _, err := q.GetOrder(ctx, req.Id); noRows(err) {
			return notFound()
		} else if err != nil {
			return err
		}
		wos, err := q.WorkOrdersOfOrder(ctx, req.Id)
		if err != nil {
			return err
		}
		ids := make([]int64, 0, len(wos))
		for _, w := range wos {
			ids = append(ids, w.ID)
		}
		r, err = readWorkOrders(ctx, q, ids)
		return err
	})
	reply := answers[gen.ListManufacturingOrderWorkOrdersResponseObject]{
		notFound: func() gen.ListManufacturingOrderWorkOrdersResponseObject {
			return gen.ListManufacturingOrderWorkOrders404Response{}
		},
		unavailable: func(p gen.ManufacturingProblem) gen.ListManufacturingOrderWorkOrdersResponseObject {
			return gen.ListManufacturingOrderWorkOrders503ApplicationProblemPlusJSONResponse(p)
		},
	}
	if err != nil {
		return answer(err, reply)
	}
	items, _, err := s.workOrdersOf(ctx, r, nil)
	if err != nil {
		return answer(err, reply)
	}
	return gen.ListManufacturingOrderWorkOrders200JSONResponse{Items: items}, nil
}
