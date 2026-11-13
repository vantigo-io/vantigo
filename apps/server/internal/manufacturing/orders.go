package manufacturing

// Manufacturing orders (manufacturing design M17–M26, M17a, M17b, M26a):
// the order's rows, its lock order, its timeline and live notifications, and
// the operations that create, change and read it. Reservations and the
// lifecycle are orderstock.go's; productions are productions.go's.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/gen"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/store"
)

// An order's states (M19).
const (
	orderDraft      = "draft"
	orderConfirmed  = "confirmed"
	orderInProgress = "in_progress"
	orderDone       = "done"
	orderCancelled  = "cancelled"
)

// openStatuses are the states the list shows by default and the holder
// counts: not done and not cancelled.
var openStatuses = []string{orderDraft, orderConfirmed, orderInProgress}

// The stock sources an order names (M20, M22): the order reserves and is the
// production's src; each production is the movement's document.
const (
	sourceOrder      = "order"
	sourceProduction = "production"
)

const (
	orderWhat       = "manufacturing order"
	orderTitle      = "The manufacturing order was not saved"
	maxOrderNote    = 2000
	defaultPageSize = 50
	maxPageSize     = 200
	maxPage         = 100000 // so (page − 1) × pageSize stays within int32
	priorityNormal  = "normal"
	originManual    = "manual"
	maxLockAttempts = 4
)

func orderSource(id int64) contracts.StockSource {
	return contracts.StockSource{Module: moduleName, Kind: sourceOrder, ID: id}
}

func productionSource(id int64) contracts.StockSource {
	return contracts.StockSource{Module: moduleName, Kind: sourceProduction, ID: id}
}

// orderNumber is an order's display number (M17b): MO-<n> for a root,
// MO-<n>-<suffix> for a backorder.
func orderNumber(number int64, suffix int32) string {
	if suffix == 0 {
		return "MO-" + strconv.FormatInt(number, 10)
	}
	return fmt.Sprintf("MO-%d-%d", number, suffix)
}

// isOpen reports whether an order takes productions and line edits: it is
// confirmed or in progress.
func isOpen(status string) bool { return status == orderConfirmed || status == orderInProgress }

// orderNotOpen is the 409 order_not_open.
func orderNotOpen(status string) error {
	return conflict(codeOrderNotOpen, "The manufacturing order is not open",
		fmt.Sprintf("The order is %s; only a confirmed or in-progress order takes this.", status))
}

// orderNotDraft is the 409 order_not_draft.
func orderNotDraft(status, what string) error {
	return conflict(codeOrderNotDraft, "The manufacturing order is not a draft",
		fmt.Sprintf("The order is %s; %s only on a draft.", status, what))
}

// actor is who wrote, as the timeline and the production keep it.
type actor struct {
	id      *uuid.UUID
	display string
}

// actorOf is the caller with their display name, read before any
// transaction.
func (s *server) actorOf(ctx context.Context) (actor, error) {
	id := callerID(ctx)
	users, err := s.users(ctx, []uuid.UUID{id})
	if err != nil {
		return actor{}, err
	}
	return actor{id: &id, display: users[id].DisplayName}, nil
}

// event appends one event to the order's timeline (M26).
func event(ctx context.Context, txq *store.Queries, orderID int64, kind gen.ManufacturingOrderEventKind, who actor, now time.Time, payload map[string]any) error {
	if payload == nil {
		payload = map[string]any{}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return txq.InsertOrderEvent(ctx, store.InsertOrderEventParams{
		OrderID: orderID, Kind: string(kind), Payload: body, ActorUserID: who.id, ActorDisplay: who.display, Now: now,
	})
}

// errRelock is a locked write's answer when the order's component variants
// grew between the plain read and the lock (M26a step 3): the transaction
// rolls back and the write starts again from LockVariants.
var errRelock = errors.New("manufacturing: the order's variants changed; lock again")

// lockedOrder is an order under M26a's lock: its row FOR UPDATE and the
// catalog's answer for every variant locked.
type lockedOrder struct {
	order  store.ManufacturingOrder
	locked map[int32]contracts.VariantEntry
}

// orderVariants is the variants a write on the order locks: its product,
// its component lines' and by-products' variants and, while it is a draft,
// its BoM's lines' and by-products' (which confirm snapshots; manufacturing
// phase 2 M121).
func orderVariants(ctx context.Context, q *store.Queries, o store.ManufacturingOrder) ([]int32, error) {
	ids := []int32{o.VariantID}
	components, err := q.ComponentVariantsOfOrder(ctx, o.ID)
	if err != nil {
		return nil, err
	}
	ids = append(ids, components...)
	outputs, err := q.OutputVariantsOfOrder(ctx, o.ID)
	if err != nil {
		return nil, err
	}
	ids = append(ids, outputs...)
	if o.Status == orderDraft {
		lines, err := q.LinesOfBoms(ctx, []int64{o.BomID})
		if err != nil {
			return nil, err
		}
		for _, l := range lines {
			ids = append(ids, l.VariantID)
		}
		more, err := bomOutputsOf(ctx, q, o.BomID)
		if err != nil {
			return nil, err
		}
		ids = append(ids, more...)
	}
	slices.Sort(ids)
	return slices.Compact(ids), nil
}

// relockExhausted is the 409 of a write whose variants kept changing under
// it for maxLockAttempts attempts: the caller reloads and tries again.
func relockExhausted() error {
	return conflict(codeRevisionConflict, "Revision conflict",
		"The order's bill of materials or its lines changed while it was being saved. Reload it and try again.")
}

// withOrder runs fn on order id under the canonical lock order (M26a):
// withOrders on the one order.
func (s *server) withOrder(ctx context.Context, id int64, extra func(context.Context, *store.Queries, store.ManufacturingOrder) ([]int32, error),
	fn func(ctx context.Context, tx pgx.Tx, txq *store.Queries, o *lockedOrder) error) error {
	return s.withOrders(ctx, func(context.Context, *store.Queries) ([]int64, error) { return []int64{id}, nil }, extra,
		func(ctx context.Context, tx pgx.Tx, txq *store.Queries, orders map[int64]*lockedOrder) error {
			return fn(ctx, tx, txq, orders[id])
		})
}

// withOrders runs fn on the orders idsOf names under the canonical lock
// order (M26a): LockVariants once on every order's product and components,
// taken from a plain read, together with extra; the orders FOR UPDATE in id
// order; for a draft, or a write with extra variants (a backorder's BoM),
// the order's BoM FOR SHARE, so its lines stay as re-read until the commit;
// a re-read of the variants, starting again from the lock when the set
// grew. fn takes the work orders, the work center and the time logs after
// (steps 4–6), and its ledger calls come last, on the same transaction.
//
// idsOf is read again on every attempt, on the pool: a write whose orders
// come from a row — the order of the caller's open log elsewhere, the orders
// of the logs at a work center — names them there, and fn returns errRelock
// when the row moved to an order not locked, so the write starts again.
// extra is read again on every attempt too. Locking every order of a write
// together, in id order, before any work order, is what keeps two writes
// across each other's orders from deadlocking. A set that keeps changing is
// 409 revision_conflict. An id idsOf names that is not an order is 404.
func (s *server) withOrders(ctx context.Context, idsOf func(context.Context, *store.Queries) ([]int64, error),
	extra func(context.Context, *store.Queries, store.ManufacturingOrder) ([]int32, error),
	fn func(ctx context.Context, tx pgx.Tx, txq *store.Queries, orders map[int64]*lockedOrder) error) error {
	return s.withScheduledOrders(ctx, nil, idsOf, extra, fn)
}

// withScheduledOrders is withOrders for a write of the schedule (M35a):
// centersOf names, from the orders idsOf named on that attempt, every work
// center whose slots the write touches, and the transaction takes the
// schedule's advisory lock of each, in id order, before any row lock —
// before LockVariants — and only then follows the canonical order. fn
// returns errRelock when a work order names a center not locked, so the
// write starts again with it. A nil centersOf takes no advisory lock.
func (s *server) withScheduledOrders(ctx context.Context,
	centersOf func(context.Context, *store.Queries, []int64) ([]int64, error),
	idsOf func(context.Context, *store.Queries) ([]int64, error),
	extra func(context.Context, *store.Queries, store.ManufacturingOrder) ([]int32, error),
	fn func(ctx context.Context, tx pgx.Tx, txq *store.Queries, orders map[int64]*lockedOrder) error) error {
	pool := store.New(s.deps.Pool)
	variantsOf := func(ctx context.Context, q *store.Queries, o store.ManufacturingOrder) ([]int32, error) {
		ids, err := orderVariants(ctx, q, o)
		if err != nil || extra == nil {
			return ids, err
		}
		more, err := extra(ctx, q, o)
		return append(ids, more...), err
	}
	for attempt := 1; ; attempt++ {
		orderIDs, err := idsOf(ctx, pool)
		if err != nil {
			return err
		}
		orderIDs = slices.Compact(slices.Sorted(slices.Values(orderIDs)))
		var ids []int32
		for _, id := range orderIDs {
			pre, err := pool.GetOrder(ctx, id)
			if noRows(err) {
				return notFound()
			}
			if err != nil {
				return err
			}
			more, err := variantsOf(ctx, pool, pre)
			if err != nil {
				return err
			}
			ids = append(ids, more...)
		}
		ids = slices.Compact(slices.Sorted(slices.Values(ids)))
		var centers []int64
		if centersOf != nil {
			if centers, err = centersOf(ctx, pool, orderIDs); err != nil {
				return err
			}
			centers = slices.Compact(slices.Sorted(slices.Values(centers)))
		}
		err = s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
			for _, c := range centers {
				if err := txq.LockScheduleCenter(ctx, int32(c)); err != nil {
					return err
				}
			}
			locked, err := s.lockVariants(ctx, tx, ids)
			if err != nil {
				return err
			}
			orders := make(map[int64]*lockedOrder, len(orderIDs))
			for _, id := range orderIDs {
				o, err := txq.GetOrderForUpdate(ctx, id)
				if noRows(err) {
					return notFound()
				}
				if err != nil {
					return err
				}
				if o.Status == orderDraft || extra != nil {
					if _, err := txq.GetBomForShare(ctx, o.BomID); err != nil {
						return err
					}
				}
				orders[id] = &lockedOrder{order: o, locked: locked}
			}
			for _, id := range orderIDs {
				now, err := variantsOf(ctx, txq, orders[id].order)
				if err != nil {
					return err
				}
				for _, v := range now {
					if !slices.Contains(ids, v) {
						return errRelock
					}
				}
			}
			return fn(context.WithValue(ctx, scheduleCentersKey{}, centers), tx, txq, orders)
		})
		if errors.Is(err, errRelock) {
			if attempt < maxLockAttempts {
				continue
			}
			return relockExhausted()
		}
		return err
	}
}

// checkRevision is the 409 revision_conflict of a stale revision.
func checkRevision(o store.ManufacturingOrder, revision int32) error {
	if o.Revision != revision {
		return revisionConflict(orderWhat, o.Revision, revision)
	}
	return nil
}

// orderFields is an order's input, judged: what is stored.
type orderFields struct {
	quantity            *big.Rat
	siteID              *int64
	componentLocationID *int64
	outputLocationID    *int64
	dueDate             pgtype.Date
	requestedStart      pgtype.Date
	priority            string
	responsible         *uuid.UUID
	note                string
}

// orderInput is the fields both the create and the update send.
type orderInput struct {
	quantity            string
	componentLocationID *int64
	outputLocationID    *int64
	dueDate             *openapi_types.Date
	priority            *gen.ManufacturingPriority
	responsible         *uuid.UUID
	note                *string
}

// judgeOrder is the request's shape: every rule that needs no row (M17).
func (s *server) judgeOrder(ctx context.Context, in orderInput, schema string) (orderFields, fieldErrors) {
	f := s.unknownProperties(ctx, schema)
	v := orderFields{componentLocationID: in.componentLocationID, outputLocationID: in.outputLocationID,
		responsible: in.responsible, priority: priorityNormal}
	quantity, msg := parsePositive(in.quantity, quantityPlaces, maxQuantity)
	f.addCoded("quantity", codeQuantityInvalid, msg)
	v.quantity = quantity
	if in.dueDate != nil {
		v.dueDate = pgtype.Date{Time: in.dueDate.Time, Valid: true}
	}
	if in.priority != nil {
		switch *in.priority {
		case gen.ManufacturingPriorityNormal, gen.ManufacturingPriorityUrgent:
			v.priority = string(*in.priority)
		default:
			f.add("priority", "normal or urgent")
		}
	}
	if in.note != nil {
		v.note = strings.TrimSpace(*in.note)
		if utf8.RuneCountInString(v.note) > maxOrderNote {
			f.add("note", "At most 2000 characters")
		}
	}
	return v, f
}

// orderPlace is where an order is made, judged against Inventory and the
// BoM before the transaction (M17a): the site, its root and its locations.
type orderPlace struct {
	site      contracts.StockSite
	locations map[int64]contracts.StockSiteLocation
}

// bomSite is the site of the BoM's operations' work centers, nil when it has
// none (M12a keeps them on one).
func bomSite(ctx context.Context, q *store.Queries, bomID int64) (*int64, error) {
	ops, err := q.OperationsOfBoms(ctx, []int64{bomID})
	if err != nil {
		return nil, err
	}
	for _, o := range ops {
		site := o.WorkCenterSiteID
		return &site, nil
	}
	return nil, nil
}

// judgePlace settles the order's site and locations (M17, M17a): the site
// defaults to the BoM's work centers' site, else the first active site; one
// at another site than the operations is work_center_other_site; a location
// must be an active location of the site (location_unknown); the output
// location defaults to the site's root.
func (s *server) judgePlace(ctx context.Context, bomID int64, v *orderFields) (orderPlace, error) {
	var place orderPlace
	sites, err := s.activeSites(ctx)
	if err != nil {
		return place, err
	}
	operations, err := bomSite(ctx, store.New(s.deps.Pool), bomID)
	if err != nil {
		return place, err
	}
	switch {
	case v.siteID != nil && operations != nil && *v.siteID != *operations:
		p := problem(http.StatusBadRequest, codeWorkCenterOtherSite, orderTitle,
			"An order is made at the site its operations' work centers stand at.")
		p.Errors = &map[string][]string{"siteId": {"Not the site of the BoM's work centers"}}
		return place, refuse(p)
	case v.siteID == nil && operations != nil:
		v.siteID = operations
	case v.siteID == nil && len(sites) > 0:
		v.siteID = &sites[0].ID
	}
	site, ok := contracts.StockSite{}, false
	if v.siteID != nil {
		site, ok = siteOf(sites, *v.siteID)
	}
	if !ok {
		return place, refuseField(orderTitle, "siteId", codeSiteUnknown, "Not an active Inventory site")
	}
	place.site = site
	if v.outputLocationID == nil {
		root := site.RootLocationID
		v.outputLocationID = &root
	}
	place.locations, err = s.judgeLocations(ctx, site.ID, v)
	return place, err
}

// judgeLocations judges the order's two locations against the site's
// (M17a): each must be an active location of it (location_unknown); an
// output location left out is the site's root.
func (s *server) judgeLocations(ctx context.Context, siteID int64, v *orderFields) (map[int64]contracts.StockSiteLocation, error) {
	locations, err := s.siteLocations(ctx, siteID)
	if err != nil {
		return nil, err
	}
	byID := map[int64]contracts.StockSiteLocation{}
	for _, l := range locations {
		byID[l.ID] = l
		if l.Root && v.outputLocationID == nil {
			root := l.ID
			v.outputLocationID = &root
		}
	}
	var f fieldErrors
	if v.outputLocationID == nil {
		f.addCoded("outputLocationId", codeLocationUnknown, "The order's site has no root location")
	}
	for field, id := range map[string]*int64{"componentLocationId": v.componentLocationID, "outputLocationId": v.outputLocationID} {
		if id == nil {
			continue
		}
		if l, ok := byID[*id]; !ok || !l.Active {
			f.addCoded(field, codeLocationUnknown, "Not an active location of the order's site")
		}
	}
	if f.failed() {
		return nil, refuseInvalid(orderTitle, f)
	}
	return byID, nil
}

// judgeResponsible refuses a responsible user the directory does not know,
// and a disabled one when the responsible changes from was (a disabled user
// already responsible stays so through an edit of the other fields).
func (s *server) judgeResponsible(ctx context.Context, id, was *uuid.UUID) error {
	if id == nil {
		return nil
	}
	users, err := s.users(ctx, []uuid.UUID{*id})
	if err != nil {
		return err
	}
	if s.deps.Users == nil {
		return nil
	}
	u, ok := users[*id]
	switch {
	case !ok:
		return refuseField(orderTitle, "responsibleUserId", "", "No such user")
	case !u.Active && (was == nil || *was != *id):
		return refuseField(orderTitle, "responsibleUserId", "", "The user is disabled")
	}
	return nil
}

// orderBom is the BoM a new order or a draft names: the one asked for, of
// the variant and active, else the variant's default active BoM
// (bom_required when it has none, M14a).
func (s *server) orderBom(ctx context.Context, variantID int32, bomID *int64) (store.ManufacturingBom, error) {
	q := store.New(s.deps.Pool)
	if bomID == nil {
		defaults, err := q.DefaultActiveBomsOfVariants(ctx, []int32{variantID})
		if err != nil {
			return store.ManufacturingBom{}, err
		}
		if len(defaults) == 0 {
			return store.ManufacturingBom{}, refuseField(orderTitle, "variantId", codeBomRequired, "The variant has no active bill of materials")
		}
		bomID = &defaults[0].ID
	}
	b, err := q.GetBom(ctx, *bomID)
	if noRows(err) || (err == nil && b.VariantID != variantID) {
		return b, refuseField(orderTitle, "bomId", "", "Not a bill of materials of the variant")
	}
	if err != nil {
		return b, err
	}
	if b.Status != statusActive {
		return b, notActive()
	}
	return b, nil
}

// bomVariants is the BoM's line variants, for a write that snapshots them.
func bomVariants(ctx context.Context, q *store.Queries, bomID int64) ([]int32, error) {
	lines, err := q.LinesOfBoms(ctx, []int64{bomID})
	if err != nil {
		return nil, err
	}
	out := make([]int32, 0, len(lines))
	for _, l := range lines {
		out = append(out, l.VariantID)
	}
	outputs, err := bomOutputsOf(ctx, q, bomID)
	if err != nil {
		return nil, err
	}
	return append(out, outputs...), nil
}

// createOrderAnswers are the create's typed refusals.
func createOrderAnswers() answers[gen.CreateManufacturingOrderResponseObject] {
	return answers[gen.CreateManufacturingOrderResponseObject]{
		badRequest: func(p gen.ManufacturingProblem) gen.CreateManufacturingOrderResponseObject {
			return gen.CreateManufacturingOrder400ApplicationProblemPlusJSONResponse(p)
		},
		conflict: func(p gen.ManufacturingProblem) gen.CreateManufacturingOrderResponseObject {
			return gen.CreateManufacturingOrder409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.ManufacturingProblem) gen.CreateManufacturingOrderResponseObject {
			return gen.CreateManufacturingOrder503ApplicationProblemPlusJSONResponse(p)
		},
	}
}

// CreateManufacturingOrder creates a draft numbered MO-<n> (M17, M17a,
// M17b), with the requested start it is given (M35a). LockVariants on the product and the BoM's components comes first
// (M10a), then the counter.
func (s *server) CreateManufacturingOrder(ctx context.Context, req gen.CreateManufacturingOrderRequestObject) (gen.CreateManufacturingOrderResponseObject, error) {
	in := req.Body
	v, f := s.judgeOrder(ctx, orderInput{
		quantity: in.Quantity, componentLocationID: in.ComponentLocationId, outputLocationID: in.OutputLocationId, dueDate: in.DueDate,
		priority: in.Priority, responsible: in.ResponsibleUserId, note: in.Note,
	}, orderInputSchema)
	if f.failed() {
		return answer(refuseInvalid(orderTitle, f), createOrderAnswers())
	}
	b, err := s.orderBom(ctx, in.VariantId, in.BomId)
	if err != nil {
		return answer(err, createOrderAnswers())
	}
	// Made for a sales order line (M45): the line is read through Orders
	// before the transaction, and names the origin.
	origin, originOrderID, originLineID, reference := originManual, (*int64)(nil), (*int64)(nil), (*string)(nil)
	if in.SalesOrderLineId != nil {
		l, err := s.judgeSalesOrderLine(ctx, *in.SalesOrderLineId, in.VariantId)
		if err != nil {
			return answer(err, createOrderAnswers())
		}
		ref := salesOrderReference(l.OrderNumber)
		origin, originOrderID, originLineID, reference = originSalesOrder, &l.OrderID, &l.LineID, &ref
	}
	v.siteID = in.SiteId
	if _, err := s.judgePlace(ctx, b.ID, &v); err != nil {
		return answer(err, createOrderAnswers())
	}
	if err := s.judgeResponsible(ctx, v.responsible, nil); err != nil {
		return answer(err, createOrderAnswers())
	}
	who, err := s.actorOf(ctx)
	if err != nil {
		return nil, err
	}
	var id int64
	for attempt := 1; ; attempt++ {
		components, err := bomVariants(ctx, store.New(s.deps.Pool), b.ID)
		if err != nil {
			return nil, err
		}
		ids := lockedVariants(in.VariantId, components)
		err = s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
			locked, err := s.lockVariants(ctx, tx, ids)
			if err != nil {
				return err
			}
			if e, ok := locked[in.VariantId]; !ok {
				return refuseField(orderTitle, "variantId", codeVariantNotFound, "No such variant in the catalog")
			} else if e.ProductType != goodsType {
				return refuseField(orderTitle, "variantId", codeVariantNotGoods, e.SKU+" is not goods; only goods are made")
			}
			bom, err := txq.GetBom(ctx, b.ID)
			if err != nil {
				return err
			}
			if bom.Status != statusActive {
				return notActive()
			}
			now, err := bomVariants(ctx, txq, b.ID)
			if err != nil {
				return err
			}
			for _, c := range now {
				if !slices.Contains(ids, c) {
					return errRelock
				}
			}
			v.requestedStart = requestedStartValue(in.RequestedStart)
			o, err := s.insertOrder(ctx, txq, b.ID, in.VariantId, v, origin, originOrderID, originLineID, reference, nil, who)
			if err != nil {
				return err
			}
			id = o.ID
			return notifyOrder(ctx, tx, o.ID)
		})
		if errors.Is(err, errRelock) {
			if attempt < maxLockAttempts {
				continue
			}
			err = relockExhausted()
		}
		if err != nil {
			return answer(err, createOrderAnswers())
		}
		break
	}
	out, err := s.orderAnswer(ctx, id)
	if err != nil {
		return answer(err, createOrderAnswers())
	}
	return gen.CreateManufacturingOrder201JSONResponse(out), nil
}

// updateOrderAnswers are the update's typed refusals.
func updateOrderAnswers() answers[gen.UpdateManufacturingOrderResponseObject] {
	return answers[gen.UpdateManufacturingOrderResponseObject]{
		badRequest: func(p gen.ManufacturingProblem) gen.UpdateManufacturingOrderResponseObject {
			return gen.UpdateManufacturingOrder400ApplicationProblemPlusJSONResponse(p)
		},
		notFound: func() gen.UpdateManufacturingOrderResponseObject { return gen.UpdateManufacturingOrder404Response{} },
		conflict: func(p gen.ManufacturingProblem) gen.UpdateManufacturingOrderResponseObject {
			return gen.UpdateManufacturingOrder409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.ManufacturingProblem) gen.UpdateManufacturingOrderResponseObject {
			return gen.UpdateManufacturingOrder503ApplicationProblemPlusJSONResponse(p)
		},
	}
}

// UpdateManufacturingOrder replaces an order's fields under its revision
// (M19): a draft changes everything; a confirmed or in-progress order only
// its dates, priority, responsible, note and locations (order_not_draft for
// the rest); a done or cancelled one nothing (order_not_open).
func (s *server) UpdateManufacturingOrder(ctx context.Context, req gen.UpdateManufacturingOrderRequestObject) (gen.UpdateManufacturingOrderResponseObject, error) {
	in := req.Body
	v, f := s.judgeOrder(ctx, orderInput{
		quantity: in.Quantity, componentLocationID: in.ComponentLocationId, outputLocationID: in.OutputLocationId, dueDate: in.DueDate,
		priority: &in.Priority, responsible: in.ResponsibleUserId, note: in.Note,
	}, orderUpdateSchema)
	if f.failed() {
		return answer(refuseInvalid(orderTitle, f), updateOrderAnswers())
	}
	pre, err := store.New(s.deps.Pool).GetOrder(ctx, req.Id)
	if noRows(err) {
		return gen.UpdateManufacturingOrder404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	if pre.Status == orderDone || pre.Status == orderCancelled {
		return answer(orderNotOpen(pre.Status), updateOrderAnswers())
	}
	bomID := in.BomId
	if pre.Status == orderDraft && bomID != pre.BomID {
		if _, err := s.orderBom(ctx, pre.VariantID, &bomID); err != nil {
			return answer(err, updateOrderAnswers())
		}
	}
	v.siteID = &in.SiteId
	if pre.Status == orderDraft {
		if _, err := s.judgePlace(ctx, bomID, &v); err != nil {
			return answer(err, updateOrderAnswers())
		}
	} else {
		// A confirmed order keeps its BoM and site whatever happened to them
		// since — the BoM's work centers moved, the site archived — so only
		// its locations are judged, against its own site's.
		if bomID != pre.BomID || in.SiteId != pre.SiteID || v.quantity.Cmp(ratOrZero(pre.Quantity)) != 0 {
			return answer(orderNotDraft(pre.Status, "the BoM, the quantity and the site change"), updateOrderAnswers())
		}
		if _, err := s.judgeLocations(ctx, pre.SiteID, &v); err != nil {
			return answer(err, updateOrderAnswers())
		}
	}
	if err := s.judgeResponsible(ctx, v.responsible, pre.ResponsibleUserID); err != nil {
		return answer(err, updateOrderAnswers())
	}
	who, err := s.actorOf(ctx)
	if err != nil {
		return nil, err
	}
	err = s.withOrder(ctx, req.Id, func(ctx context.Context, q *store.Queries, o store.ManufacturingOrder) ([]int32, error) {
		if o.Status != orderDraft || bomID == o.BomID {
			return nil, nil
		}
		return bomVariants(ctx, q, bomID)
	}, func(ctx context.Context, tx pgx.Tx, txq *store.Queries, lo *lockedOrder) error {
		o := lo.order
		if err := checkRevision(o, in.Revision); err != nil {
			return err
		}
		switch o.Status {
		case orderDone, orderCancelled:
			return orderNotOpen(o.Status)
		case orderDraft:
			if bomID != o.BomID {
				b, err := txq.GetBom(ctx, bomID)
				if err != nil {
					return err
				}
				if b.Status != statusActive {
					return notActive()
				}
			}
		default:
			if bomID != o.BomID || in.SiteId != o.SiteID || v.quantity.Cmp(ratOrZero(o.Quantity)) != 0 {
				return orderNotDraft(o.Status, "the BoM, the quantity and the site change")
			}
		}
		at := s.deps.Clock()
		if _, err := txq.UpdateOrder(ctx, store.UpdateOrderParams{
			ID: o.ID, BomID: bomID, Quantity: numeric(v.quantity, quantityPlaces), SiteID: in.SiteId,
			ComponentLocationID: v.componentLocationID, OutputLocationID: *v.outputLocationID, DueDate: v.dueDate,
			Priority: v.priority, ResponsibleUserID: v.responsible,
			Note: v.note, RequestedStart: requestedStartValue(in.RequestedStart), Now: at,
		}); err != nil {
			return err
		}
		if err := event(ctx, txq, o.ID, gen.ManufacturingOrderEventKindUpdated, who, at, nil); err != nil {
			return err
		}
		return notifyOrder(ctx, tx, o.ID)
	})
	if err != nil {
		return answer(err, updateOrderAnswers())
	}
	out, err := s.orderAnswer(ctx, req.Id)
	if err != nil {
		return answer(err, updateOrderAnswers())
	}
	return gen.UpdateManufacturingOrder200JSONResponse(out), nil
}

// requestedStartValue is a create's or an update's requested start (M35a) as the
// column's value: none clears it, so planning takes the default.
func requestedStartValue(d *openapi_types.Date) pgtype.Date {
	if d == nil {
		return pgtype.Date{}
	}
	return dateValue(d.Time)
}

// GetManufacturingOrder is one order with its lines and their live
// availability (M21a).
func (s *server) GetManufacturingOrder(ctx context.Context, req gen.GetManufacturingOrderRequestObject) (gen.GetManufacturingOrderResponseObject, error) {
	out, err := s.orderAnswer(ctx, req.Id)
	if err != nil {
		return answer(err, answers[gen.GetManufacturingOrderResponseObject]{
			notFound: func() gen.GetManufacturingOrderResponseObject { return gen.GetManufacturingOrder404Response{} },
			unavailable: func(p gen.ManufacturingProblem) gen.GetManufacturingOrderResponseObject {
				return gen.GetManufacturingOrder503ApplicationProblemPlusJSONResponse(p)
			},
		})
	}
	return gen.GetManufacturingOrder200JSONResponse(out), nil
}

// orderRows is what an order's answer reads of this module, in one snapshot.
type orderRows struct {
	order      store.ManufacturingOrder
	bom        store.ManufacturingBom
	components []store.ManufacturingOrderComponent
	bomLines   []store.ManufacturingBomLine
	links      []store.ManufacturingOrder
	related    map[int64]store.ManufacturingOrder
	workOrders []store.ManufacturingWorkOrder // once confirmed, for the cost and the plan
	logs       []store.ManufacturingTimeLog
	outputs    []store.ManufacturingOrderOutput // the by-products; a draft's as its BoM makes them (M121)
	unbuilt    *big.Rat                         // what unbuilds took apart of its output (M123)
}

func (s *server) readOrder(ctx context.Context, id int64) (orderRows, error) {
	var r orderRows
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		o, err := q.GetOrder(ctx, id)
		if noRows(err) {
			return notFound()
		}
		if err != nil {
			return err
		}
		r.order = o
		if r.bom, err = q.GetBom(ctx, o.BomID); err != nil {
			return err
		}
		if r.components, err = q.ListOrderComponents(ctx, o.ID); err != nil {
			return err
		}
		if o.Status == orderDraft {
			if r.bomLines, err = q.LinesOfBoms(ctx, []int64{o.BomID}); err != nil {
				return err
			}
			outs, err := q.OutputsOfBoms(ctx, []int64{o.BomID})
			if err != nil {
				return err
			}
			r.outputs = draftOutputs(o, r.bom, outs)
		} else if r.outputs, err = q.OutputsOfOrders(ctx, []int64{o.ID}); err != nil {
			return err
		}
		unbuilt, err := q.UnbuiltOfOrders(ctx, []int64{o.ID})
		if err != nil {
			return err
		}
		r.unbuilt = new(big.Rat)
		for _, u := range unbuilt {
			r.unbuilt.Add(r.unbuilt, ratOrZero(u.Quantity))
		}
		if r.links, err = q.OrderLinks(ctx, &o.ID); err != nil {
			return err
		}
		if o.Status != orderDraft {
			if r.workOrders, err = q.WorkOrdersOfOrder(ctx, o.ID); err != nil {
				return err
			}
			if r.logs, err = q.LogsOfWorkOrders(ctx, workOrderIDs(r.workOrders)); err != nil {
				return err
			}
		}
		var ids []int64
		for _, p := range []*int64{o.BackorderOfID, o.ParentOrderID, o.SplitFromID, o.MergedIntoID} {
			if p != nil {
				ids = append(ids, *p)
			}
		}
		related, err := q.OrdersByIDs(ctx, ids)
		if err != nil {
			return err
		}
		r.related = map[int64]store.ManufacturingOrder{}
		for _, x := range related {
			r.related[x.ID] = x
		}
		return nil
	})
	return r, err
}

// requiredOf is a BoM line's requirement on an order (M18): line quantity ×
// order quantity / output quantity × (1 + scrap/100), rounded up to three
// decimals.
func requiredOf(lineQuantity, scrapPercent, orderQuantity, outputQuantity *big.Rat) *big.Rat {
	v := new(big.Rat).Mul(lineQuantity, orderQuantity)
	v.Quo(v, outputQuantity)
	factor := new(big.Rat).Add(big.NewRat(1, 1), new(big.Rat).Quo(scrapPercent, hundred))
	return ceilPlaces(v.Mul(v, factor), quantityPlaces)
}

// roundPlaces is v rounded half-up to places decimals (Rounding and codes).
func roundPlaces(v *big.Rat, places int) *big.Rat { return mustRat(v.FloatString(places)) }

// positive is v when it is more than 0, else 0.
func positive(v *big.Rat) *big.Rat {
	if v.Sign() < 0 {
		return new(big.Rat)
	}
	return v
}

// remainingOf is a line's requirement not yet consumed or scrapped, at least
// 0.
func remainingOf(c store.ManufacturingOrderComponent) *big.Rat {
	r := new(big.Rat).Sub(ratOrZero(c.Required), ratOrZero(c.Consumed))
	return positive(r.Sub(r, ratOrZero(c.Scrapped)))
}

// componentRef is a component line's variant: the catalog's answer, else the
// line's snapshot; the standard cost is the snapshot once confirmed (M23a).
func componentRef(entries map[int32]contracts.VariantEntry, c store.ManufacturingOrderComponent, costs bool) gen.ManufacturingVariantRef {
	ref := refOf(entries, c.VariantID, costs)
	if ref.Sku == "" {
		ref.Sku, ref.ProductName, ref.Unit = c.Sku, c.ProductName, c.Unit
	}
	if r := ratOf(c.StandardCost); r != nil {
		cost := text(r, amountPlaces)
		ref.StandardCost = costJSON(costs, &cost)
	}
	return ref
}

// draftComponents is a draft's lines as its BoM makes them now: it follows
// its BoM until confirmed (M17), so the lines have no id and nothing
// reserved.
func draftComponents(o store.ManufacturingOrder, bom store.ManufacturingBom, lines []store.ManufacturingBomLine) []store.ManufacturingOrderComponent {
	out := make([]store.ManufacturingOrderComponent, 0, len(lines))
	for _, l := range lines {
		required := requiredOf(ratOrZero(l.Quantity), ratOrZero(l.ScrapPercent), ratOrZero(o.Quantity), ratOrZero(bom.OutputQuantity))
		out = append(out, store.ManufacturingOrderComponent{VariantID: l.VariantID, Required: numeric(required, quantityPlaces),
			OperationID: l.OperationID, SortOrder: l.SortOrder, Note: l.Note})
	}
	return out
}

// draftOutputs is a draft's by-products as its BoM makes them now (M121):
// they follow the BoM until confirmed, so they have no id.
func draftOutputs(o store.ManufacturingOrder, bom store.ManufacturingBom, outputs []store.ManufacturingBomOutput) []store.ManufacturingOrderOutput {
	out := make([]store.ManufacturingOrderOutput, 0, len(outputs))
	for _, x := range outputs {
		out = append(out, store.ManufacturingOrderOutput{VariantID: x.VariantID, Quantity: x.Quantity, CostShare: x.CostShare,
			Expected:  numeric(expectedOutput(ratOrZero(x.Quantity), ratOrZero(o.Quantity), ratOrZero(bom.OutputQuantity)), quantityPlaces),
			SortOrder: x.SortOrder})
	}
	return out
}

// outputAnswer is a by-product as the order answers it.
func outputAnswer(x store.ManufacturingOrderOutput, entries map[int32]contracts.VariantEntry, costs bool) gen.ManufacturingOrderOutput {
	ref := refOf(entries, x.VariantID, costs)
	if ref.Sku == "" {
		ref.Sku, ref.ProductName, ref.Unit = x.Sku, x.ProductName, x.Unit
	}
	out := gen.ManufacturingOrderOutput{Variant: ref, Expected: numericText(x.Expected, quantityPlaces), Produced: numericText(x.Produced, quantityPlaces),
		CostShare: numericText(x.CostShare, percentPlaces), Tracking: trackingOf(entries[x.VariantID])}
	if x.ID != 0 {
		id := x.ID
		out.Id = &id
	}
	return out
}

// coverage is M21a's availability of an order's lines: each line's missing
// quantity, the site's available shared across the lines of one variant in
// line order, and the order's status.
type coverage struct {
	missing   []*big.Rat
	available map[int32]*big.Rat
	status    gen.ManufacturingAvailability
}

func coverageOf(components []store.ManufacturingOrderComponent, available map[int32]contracts.StockQuantity) coverage {
	cov := coverage{available: map[int32]*big.Rat{}}
	left := map[int32]*big.Rat{}
	for _, c := range components {
		if _, ok := left[c.VariantID]; ok {
			continue
		}
		a := new(big.Rat)
		if q, ok := available[c.VariantID]; ok {
			if r, ok := new(big.Rat).SetString(q.Quantity); ok {
				a = r
			}
		}
		cov.available[c.VariantID] = a
		left[c.VariantID] = new(big.Rat).Set(positive(new(big.Rat).Set(a)))
	}
	needing, covered := 0, 0
	for _, c := range components {
		need := positive(new(big.Rat).Sub(remainingOf(c), ratOrZero(c.Reserved)))
		take := new(big.Rat).Set(need)
		if take.Cmp(left[c.VariantID]) > 0 {
			take.Set(left[c.VariantID])
		}
		left[c.VariantID].Sub(left[c.VariantID], take)
		missing := new(big.Rat).Sub(need, take)
		cov.missing = append(cov.missing, missing)
		if remainingOf(c).Sign() > 0 {
			needing++
			if missing.Sign() == 0 {
				covered++
			}
		}
	}
	switch covered {
	case needing:
		cov.status = gen.ManufacturingAvailabilityReady
	case 0:
		cov.status = gen.ManufacturingAvailabilityWaiting
	default:
		cov.status = gen.ManufacturingAvailabilityPartlyAvailable
	}
	return cov
}

// orderAnswer is the order as an answer, read after any transaction: its
// rows in one snapshot, then the catalog, the users and the site's
// available stock (M21a) on the pool.
func (s *server) orderAnswer(ctx context.Context, id int64) (gen.ManufacturingOrder, error) {
	r, err := s.readOrder(ctx, id)
	if err != nil {
		return gen.ManufacturingOrder{}, err
	}
	o := r.order
	components := r.components
	if o.Status == orderDraft {
		components = draftComponents(o, r.bom, r.bomLines)
	}
	ids := []int32{o.VariantID}
	for _, c := range components {
		ids = append(ids, c.VariantID)
	}
	componentIDs := slices.Clone(ids[1:])
	for _, x := range r.outputs {
		ids = append(ids, x.VariantID)
	}
	entries, err := s.catalogVariants(ctx, slices.Compact(slices.Sorted(slices.Values(ids))))
	if err != nil {
		return gen.ManufacturingOrder{}, err
	}
	var available map[int32]contracts.StockQuantity
	if len(components) > 0 {
		site := o.SiteID
		if available, err = s.stockAvailable(ctx, componentIDs, &site); err != nil {
			return gen.ManufacturingOrder{}, err
		}
	}
	today, _, err := s.today(ctx)
	if err != nil {
		return gen.ManufacturingOrder{}, err
	}
	zone, err := s.businessZone(ctx)
	if err != nil {
		return gen.ManufacturingOrder{}, err
	}
	people, err := s.people(ctx, o.ResponsibleUserID)
	if err != nil {
		return gen.ManufacturingOrder{}, err
	}
	costs := s.has(ctx, permCostsView)
	cov := coverageOf(components, available)
	componentBoms, err := s.componentBoms(ctx, components)
	if err != nil {
		return gen.ManufacturingOrder{}, err
	}
	sum := summaryOf(o, entries, people, today, costs, reservationStatusOf(o, r.components))
	out := gen.ManufacturingOrder{
		Id: sum.Id, Number: sum.Number, Variant: sum.Variant, BomId: o.BomID, BomCode: r.bom.Code,
		BomOutputQuantity: numericText(r.bom.OutputQuantity, quantityPlaces), Quantity: sum.Quantity, Produced: sum.Produced,
		OutputScrap: numericText(o.OutputScrap, quantityPlaces), Status: sum.Status, SiteId: o.SiteID,
		ComponentLocationId: o.ComponentLocationID, OutputLocationId: o.OutputLocationID, DueDate: sum.DueDate,
		PlannedStart: o.PlannedStart, PlannedFinish: o.PlannedFinish, Priority: sum.Priority, Responsible: sum.Responsible,
		Note: o.Note, LotPattern: o.LotPattern, Origin: sum.Origin, ReservationStatus: sum.ReservationStatus, Late: sum.Late, BackorderOfId: o.BackorderOfID,
		Backorders: []gen.ManufacturingOrderLink{}, Children: []gen.ManufacturingOrderLink{},
		Components: make([]gen.ManufacturingOrderComponent, 0, len(components)), Availability: cov.status,
		Overrun:     text(positive(new(big.Rat).Sub(ratOrZero(o.Produced), ratOrZero(o.Quantity))), quantityPlaces),
		ConfirmedAt: o.ConfirmedAt, ClosedAt: o.ClosedAt, Revision: o.Revision, CreatedAt: o.CreatedAt, UpdatedAt: o.UpdatedAt,
		Warnings:   warningsOf(o, entries, components),
		LateByDays: lateByDays(o.PlannedFinish, dateOf(o.DueDate), zone),
		Outputs:    make([]gen.ManufacturingOrderOutput, 0, len(r.outputs)), SplitFromId: o.SplitFromID, MergedIntoId: o.MergedIntoID,
		SplitParts: []gen.ManufacturingOrderLink{}, MergedFrom: []gen.ManufacturingOrderLink{}, Unbuilt: text(r.unbuilt, quantityPlaces),
	}
	for _, x := range r.outputs {
		out.Outputs = append(out.Outputs, outputAnswer(x, entries, costs))
	}
	if d := dateOf(o.RequestedStart); d != nil {
		out.RequestedStart = &openapi_types.Date{Time: *d}
	}
	if isOpen(o.Status) {
		d := defaultRequestedStart(dateOf(o.DueDate), expectedOf(openWork(r.workOrders)), today)
		out.RequestedStartDefault = &openapi_types.Date{Time: d}
	}
	if costs {
		out.Cost = orderCostOf(o, r.components, r.workOrders, r.logs, s.deps.Clock(), r.outputs, entries)
	}
	if o.BackorderOfID != nil {
		if x, ok := r.related[*o.BackorderOfID]; ok {
			link := linkOf(x)
			out.BackorderOf = &link
		}
	}
	if o.ParentOrderID != nil {
		if x, ok := r.related[*o.ParentOrderID]; ok {
			link := linkOf(x)
			out.Parent = &link
		}
	}
	if o.SplitFromID != nil {
		if x, ok := r.related[*o.SplitFromID]; ok {
			link := linkOf(x)
			out.SplitFrom = &link
		}
	}
	if o.MergedIntoID != nil {
		if x, ok := r.related[*o.MergedIntoID]; ok {
			link := linkOf(x)
			out.MergedInto = &link
		}
	}
	for _, x := range r.links {
		if x.BackorderOfID != nil && *x.BackorderOfID == o.ID {
			out.Backorders = append(out.Backorders, linkOf(x))
		}
		if x.ParentOrderID != nil && *x.ParentOrderID == o.ID {
			out.Children = append(out.Children, linkOf(x))
		}
		if x.SplitFromID != nil && *x.SplitFromID == o.ID {
			out.SplitParts = append(out.SplitParts, linkOf(x))
		}
		if x.MergedIntoID != nil && *x.MergedIntoID == o.ID {
			out.MergedFrom = append(out.MergedFrom, linkOf(x))
		}
	}
	for i, c := range components {
		line := componentAnswer(c, entries, costs, cov.available[c.VariantID], cov.missing[i])
		if id, ok := componentBoms[c.VariantID]; ok {
			line.DefaultBomId = &id
		}
		out.Components = append(out.Components, line)
	}
	s.withOrigin(ctx, &out)
	return out, nil
}

// componentBoms is each component variant's default active BoM, for the
// sub-assembly orders (M47).
func (s *server) componentBoms(ctx context.Context, components []store.ManufacturingOrderComponent) (map[int32]int64, error) {
	out := map[int32]int64{}
	if len(components) == 0 {
		return out, nil
	}
	ids := make([]int32, 0, len(components))
	for _, c := range components {
		ids = append(ids, c.VariantID)
	}
	rows, err := store.New(s.deps.Pool).DefaultActiveBomsOfVariants(ctx, slices.Compact(slices.Sorted(slices.Values(ids))))
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.VariantID] = r.ID
	}
	return out, nil
}

// componentAnswer is a component line as an answer, with the site's
// available quantity of its variant and what it misses (M21a).
func componentAnswer(c store.ManufacturingOrderComponent, entries map[int32]contracts.VariantEntry, costs bool, available, missing *big.Rat) gen.ManufacturingOrderComponent {
	var cid *int64
	if c.ID != 0 {
		id := c.ID
		cid = &id
	}
	return gen.ManufacturingOrderComponent{
		Id: cid, Variant: componentRef(entries, c, costs), Required: numericText(c.Required, quantityPlaces),
		Reserved: numericText(c.Reserved, quantityPlaces), Available: text(available, quantityPlaces),
		Consumed: numericText(c.Consumed, quantityPlaces), Scrapped: numericText(c.Scrapped, quantityPlaces),
		Remaining: text(remainingOf(c), quantityPlaces), Missing: text(missing, quantityPlaces), Covered: missing.Sign() == 0,
		OperationId: c.OperationID, Added: c.Added, Note: c.Note, SortOrder: c.SortOrder,
	}
}

// productDiscontinued is the catalog's status of a product being used up.
const productDiscontinued = "Discontinued"

// warningsOf is what an order not done or cancelled is allowed with but
// should be looked at (§11): variant_discontinued when its product or a
// component line's variant is of a Discontinued product.
func warningsOf(o store.ManufacturingOrder, entries map[int32]contracts.VariantEntry, components []store.ManufacturingOrderComponent) []gen.ManufacturingOrderWarning {
	out := []gen.ManufacturingOrderWarning{}
	if o.Status == orderDone || o.Status == orderCancelled {
		return out
	}
	discontinued := entries[o.VariantID].ProductStatus == productDiscontinued
	for _, c := range components {
		discontinued = discontinued || entries[c.VariantID].ProductStatus == productDiscontinued
	}
	if discontinued {
		out = append(out, gen.VariantDiscontinued)
	}
	return out
}

// people is the responsible users named, by id.
func (s *server) people(ctx context.Context, ids ...*uuid.UUID) (map[uuid.UUID]contracts.UserEntry, error) {
	var list []uuid.UUID
	for _, id := range ids {
		if id != nil {
			list = append(list, *id)
		}
	}
	return s.users(ctx, list)
}

func linkOf(o store.ManufacturingOrder) gen.ManufacturingOrderLink {
	return gen.ManufacturingOrderLink{Id: o.ID, Number: orderNumber(o.Number, o.Suffix), Status: gen.ManufacturingOrderStatus(o.Status),
		Quantity: numericText(o.Quantity, quantityPlaces), Produced: numericText(o.Produced, quantityPlaces), VariantId: o.VariantID}
}

// reservationStatusOf is the list's reservation status (M21a) from the
// stored copies, nil unless the order is confirmed or in progress — the same
// rule ListOrders computes in SQL.
func reservationStatusOf(o store.ManufacturingOrder, components []store.ManufacturingOrderComponent) *string {
	if !isOpen(o.Status) {
		return nil
	}
	short, any := false, false
	for _, c := range components {
		if remainingOf(c).Cmp(ratOrZero(c.Reserved)) > 0 {
			short = true
		}
		if ratOrZero(c.Reserved).Sign() > 0 {
			any = true
		}
	}
	status := string(gen.ManufacturingReservationStatusReserved)
	switch {
	case short && any:
		status = string(gen.ManufacturingReservationStatusPartlyReserved)
	case short:
		status = string(gen.ManufacturingReservationStatusNotReserved)
	}
	return &status
}

// summaryOf is an order as the list shows it.
func summaryOf(o store.ManufacturingOrder, entries map[int32]contracts.VariantEntry, people map[uuid.UUID]contracts.UserEntry,
	today time.Time, costs bool, reservation *string) gen.ManufacturingOrderSummary {
	out := gen.ManufacturingOrderSummary{
		Id: o.ID, Number: orderNumber(o.Number, o.Suffix), Variant: refOf(entries, o.VariantID, costs), BomId: o.BomID,
		Quantity: numericText(o.Quantity, quantityPlaces), Produced: numericText(o.Produced, quantityPlaces),
		Status: gen.ManufacturingOrderStatus(o.Status), SiteId: o.SiteID, PlannedStart: o.PlannedStart, PlannedFinish: o.PlannedFinish,
		Priority: gen.ManufacturingPriority(o.Priority), BackorderOfId: o.BackorderOfID, CreatedAt: o.CreatedAt,
		Origin: gen.ManufacturingOrderOriginInfo{Kind: gen.ManufacturingOrigin(o.OriginKind), OrderId: o.OriginOrderID,
			LineId: o.OriginLineID, Reference: o.OriginReference, Cancelled: o.OriginCancelled},
	}
	if o.DueDate.Valid {
		out.DueDate = &openapi_types.Date{Time: o.DueDate.Time}
		out.Late = o.DueDate.Time.Before(today) && slices.Contains(openStatuses, o.Status)
	}
	if o.ResponsibleUserID != nil {
		out.Responsible = &gen.ManufacturingPerson{Id: *o.ResponsibleUserID, DisplayName: people[*o.ResponsibleUserID].DisplayName}
	}
	if reservation != nil {
		r := gen.ManufacturingReservationStatus(*reservation)
		out.ReservationStatus = &r
	}
	return out
}

// listAnswers are the list's typed refusals.
func listOrdersAnswers() answers[gen.ListManufacturingOrdersResponseObject] {
	return answers[gen.ListManufacturingOrdersResponseObject]{
		badRequest: func(p gen.ManufacturingProblem) gen.ListManufacturingOrdersResponseObject {
			return gen.ListManufacturingOrders400ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.ManufacturingProblem) gen.ListManufacturingOrdersResponseObject {
			return gen.ListManufacturingOrders503ApplicationProblemPlusJSONResponse(p)
		},
	}
}

// numberQuery is the order number a search names, "MO-12", "mo-12-2" or
// "12", or 0.
func numberQuery(q string) int64 {
	q = strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(q)), "MO-")
	head, _, _ := strings.Cut(q, "-")
	n, err := strconv.ParseInt(head, 10, 64)
	if err != nil || n < 1 {
		return 0
	}
	return n
}

// ListManufacturingOrders is the list (M24, M21a), a page at a time, as
// Orders pages its list.
func (s *server) ListManufacturingOrders(ctx context.Context, req gen.ListManufacturingOrdersRequestObject) (gen.ListManufacturingOrdersResponseObject, error) {
	p := req.Params
	params := store.ListOrdersParams{Statuses: openStatuses, SiteID: p.SiteId, WorkCenterID: p.WorkCenterId, VariantID: p.VariantId,
		Responsible: p.ResponsibleUserId, Sort: string(gen.DueDate), PageSize: defaultPageSize, VariantIds: []int32{}}
	if p.Status != nil && len(*p.Status) > 0 {
		params.Statuses = nil
		for _, st := range *p.Status {
			params.Statuses = append(params.Statuses, string(st))
		}
	}
	if p.DueFrom != nil {
		params.DueFrom = pgtype.Date{Time: p.DueFrom.Time, Valid: true}
	}
	if p.DueTo != nil {
		params.DueTo = pgtype.Date{Time: p.DueTo.Time, Valid: true}
	}
	if p.Origin != nil {
		origin := string(*p.Origin)
		params.Origin = &origin
	}
	if p.Reservation != nil {
		r := string(*p.Reservation)
		params.Reservation = &r
	}
	if p.Sort != nil {
		params.Sort = string(*p.Sort)
	}
	page := int32(1)
	if p.Page != nil {
		page = *p.Page
	}
	if p.PageSize != nil {
		params.PageSize = *p.PageSize
	}
	if page < 1 || page > maxPage || params.PageSize < 1 || params.PageSize > maxPageSize {
		return answer(refuseField("The list was not read", "page", "", "page 1–100000, pageSize 1–200"), listOrdersAnswers())
	}
	params.PageOffset = (page - 1) * params.PageSize
	today, _, err := s.today(ctx)
	if err != nil {
		return nil, err
	}
	params.Today = pgtype.Date{Time: today, Valid: true}
	params.LateOnly = p.Late != nil && *p.Late
	if p.Q != nil && strings.TrimSpace(*p.Q) != "" {
		params.Search = true
		params.NumberMatch = numberQuery(*p.Q)
		found, err := s.searchVariants(ctx, strings.TrimSpace(*p.Q), contracts.VariantSearch{Types: []string{goodsType}, Limit: maxVariantLimit, IncludeInactive: true})
		if err != nil {
			return answer(err, listOrdersAnswers())
		}
		for _, v := range found {
			params.VariantIds = append(params.VariantIds, v.ID)
		}
	}
	rows, err := store.New(s.deps.Pool).ListOrders(ctx, params)
	if err != nil {
		return nil, err
	}
	var variantIDs []int32
	var users []*uuid.UUID
	for _, r := range rows {
		variantIDs = append(variantIDs, r.VariantID)
		users = append(users, r.ResponsibleUserID)
	}
	entries, err := s.catalogVariants(ctx, slices.Compact(slices.Sorted(slices.Values(variantIDs))))
	if err != nil {
		return answer(err, listOrdersAnswers())
	}
	people, err := s.people(ctx, users...)
	if err != nil {
		return nil, err
	}
	costs := s.has(ctx, permCostsView)
	out := gen.ListManufacturingOrders200JSONResponse{Items: make([]gen.ManufacturingOrderSummary, 0, len(rows)), Page: page, PageSize: params.PageSize}
	for _, r := range rows {
		out.Total = r.Total
		o := store.ManufacturingOrder{ID: r.ID, Number: r.Number, Suffix: r.Suffix, VariantID: r.VariantID, BomID: r.BomID, Quantity: r.Quantity,
			Produced: r.Produced, SiteID: r.SiteID, DueDate: r.DueDate, PlannedStart: r.PlannedStart, PlannedFinish: r.PlannedFinish,
			Priority: r.Priority, ResponsibleUserID: r.ResponsibleUserID, OriginKind: r.OriginKind, OriginOrderID: r.OriginOrderID,
			OriginLineID: r.OriginLineID, OriginReference: r.OriginReference, OriginCancelled: r.OriginCancelled, BackorderOfID: r.BackorderOfID,
			Status: r.Status, CreatedAt: r.CreatedAt}
		var reservation *string
		if r.ReservationStatus != "" {
			reservation = &r.ReservationStatus
		}
		out.Items = append(out.Items, summaryOf(o, entries, people, today, costs, reservation))
	}
	return out, nil
}

// ListManufacturingOrderEvents is the order's timeline, oldest first (M26).
func (s *server) ListManufacturingOrderEvents(ctx context.Context, req gen.ListManufacturingOrderEventsRequestObject) (gen.ListManufacturingOrderEventsResponseObject, error) {
	q := store.New(s.deps.Pool)
	if _, err := q.GetOrder(ctx, req.Id); noRows(err) {
		return gen.ListManufacturingOrderEvents404Response{}, nil
	} else if err != nil {
		return nil, err
	}
	rows, err := q.ListOrderEvents(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	out := gen.ListManufacturingOrderEvents200JSONResponse{Items: make([]gen.ManufacturingOrderEvent, 0, len(rows))}
	for _, r := range rows {
		payload := map[string]interface{}{}
		if err := json.Unmarshal(r.Payload, &payload); err != nil {
			return nil, fmt.Errorf("manufacturing: event %d's payload: %w", r.ID, err)
		}
		out.Items = append(out.Items, gen.ManufacturingOrderEvent{Id: r.ID, Kind: gen.ManufacturingOrderEventKind(r.Kind), Payload: payload,
			ActorUserId: r.ActorUserID, ActorDisplay: r.ActorDisplay, At: r.At})
	}
	return out, nil
}

// SearchManufacturingPeople is the user directory's search, for an order's
// responsible.
func (s *server) SearchManufacturingPeople(ctx context.Context, req gen.SearchManufacturingPeopleRequestObject) (gen.SearchManufacturingPeopleResponseObject, error) {
	limit := defaultVariantLimit
	if req.Params.Limit != nil {
		limit = min(max(int(*req.Params.Limit), 1), maxVariantLimit)
	}
	q := ""
	if req.Params.Q != nil {
		q = strings.TrimSpace(*req.Params.Q)
	}
	found, err := s.searchUsers(ctx, q, limit)
	if err != nil {
		return nil, err
	}
	out := gen.SearchManufacturingPeople200JSONResponse{Items: make([]gen.ManufacturingPerson, 0, len(found))}
	for _, u := range found {
		out.Items = append(out.Items, gen.ManufacturingPerson{Id: u.ID, DisplayName: u.DisplayName})
	}
	return out, nil
}

// ListManufacturingSiteLocations is every location of a site, by full code
// (M4a).
func (s *server) ListManufacturingSiteLocations(ctx context.Context, req gen.ListManufacturingSiteLocationsRequestObject) (gen.ListManufacturingSiteLocationsResponseObject, error) {
	locations, err := s.siteLocations(ctx, req.Id)
	if err != nil {
		return answer(err, answers[gen.ListManufacturingSiteLocationsResponseObject]{
			unavailable: func(p gen.ManufacturingProblem) gen.ListManufacturingSiteLocationsResponseObject {
				return gen.ListManufacturingSiteLocations503ApplicationProblemPlusJSONResponse(p)
			},
		})
	}
	out := gen.ListManufacturingSiteLocations200JSONResponse{Items: make([]gen.ManufacturingLocation, 0, len(locations))}
	for _, l := range locations {
		out.Items = append(out.Items, gen.ManufacturingLocation{Id: l.ID, ParentId: l.ParentID, Kind: l.Kind, FullCode: l.FullCode,
			Active: l.Active, Root: l.Root})
	}
	return out, nil
}
