package main

// The demo seed's receipts (purchasing design, delivery B): on the orders
// delivery A's seed sent, the steel tube order partly received (120 of
// 180 m, the screws still to come), the confirmed paint order received in
// full into a lot with its number and expiry, and the plywood order
// received and then undone — a miscount taken back. Every write goes through
// purchasing.Operations as the owner, so each receipt posts its Inventory
// movement through the real ledger. It runs right after the purchase orders
// and reruns cleanly part by part: an order that has a receipt is left as
// it is, so a part that failed is made by the next run.

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/identity"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/purchasing"
	"github.com/vantigo-io/vantigo/server/internal/purchasing/gen"
)

// seedDemoPurchasingReceipts seeds the receipts on the demo orders.
func seedDemoPurchasingReceipts(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger, cfg *config.Config, now time.Time) error {
	for _, m := range []string{"purchasing", "accounting", "inventory", "products"} {
		if !slices.Contains(cfg.Modules, m) {
			return nil
		}
	}
	var owner uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT u.id FROM identity.users u
		JOIN identity.user_roles ur ON ur.user_id = u.id
		JOIN identity.roles r ON r.id = ur.role_id
		WHERE r.name = 'Owner' ORDER BY u.created_at LIMIT 1
	`).Scan(&owner); err != nil {
		return fmt.Errorf("read the owner: %w", err)
	}
	deps := module.Deps{Config: cfg, Pool: pool, Logger: logger, Clock: func() time.Time { return now }}
	access := identity.NewAccess(deps)
	deps.Access = access
	deps = module.WorkerDeps(deps, businessModules(access)...)
	ops, err := purchasing.Operations(deps)
	if err != nil {
		return err
	}
	r := &demoReceipts{ctx: contracts.AsUser(contracts.WithPrincipal(ctx, contracts.Principal{UserID: owner}), owner), pool: pool, ops: ops, now: now}
	steps := []struct {
		name, sku string
		run       func(o gen.PurchasingOrder) error
	}{
		{"receive part of the steel tube", "VRK-ROR-40", r.partTube},
		{"receive the paint into a lot", "VRK-LAKK-7016", r.paintLot},
		{"receive the plywood and undo it", "VRK-FINER-18", r.plywoodUndone},
	}
	made := 0
	for _, step := range steps {
		o, ok, err := r.orderOf(step.sku)
		if err != nil {
			return fmt.Errorf("purchasing receipts demo: %s: %w", step.name, err)
		}
		if !ok {
			continue
		}
		if err := step.run(o); err != nil {
			return fmt.Errorf("purchasing receipts demo: %s: %w", step.name, err)
		}
		made++
	}
	logger.Info("purchasing receipts demo seeded", "parts", made)
	return nil
}

// demoReceipts is seedDemoPurchasingReceipts' state.
type demoReceipts struct {
	ctx  context.Context
	pool *pgxpool.Pool
	ops  gen.StrictServerInterface
	now  time.Time
}

// orderOf is the sent demo order with a line of the SKU and no receipt yet;
// ok is false when there is none — not seeded, or received already.
func (r *demoReceipts) orderOf(sku string) (gen.PurchasingOrder, bool, error) {
	var id int64
	err := r.pool.QueryRow(r.ctx, `
		SELECT coalesce((SELECT o.id FROM purchasing.orders o
			WHERE o.status IN ('sent', 'partially_received')
			  AND EXISTS (SELECT 1 FROM purchasing.order_lines l WHERE l.order_id = o.id AND l.sku = $1 AND l.closed_at IS NULL)
			  AND NOT EXISTS (SELECT 1 FROM purchasing.receipts rc WHERE rc.order_id = o.id)
			ORDER BY o.id LIMIT 1), 0)`, sku).Scan(&id)
	if err != nil || id == 0 {
		return gen.PurchasingOrder{}, false, err
	}
	res, err := r.ops.GetPurchasingOrder(r.ctx, gen.GetPurchasingOrderRequestObject{OrderId: id})
	o, err := expect[gen.GetPurchasingOrder200JSONResponse]("read order", res, err)
	return gen.PurchasingOrder(o), err == nil, err
}

// form is the order's receive screen: its open lines by SKU.
func (r *demoReceipts) form(o gen.PurchasingOrder) (map[string]gen.PurchasingReceiveLine, error) {
	res, err := r.ops.GetPurchasingReceiveForm(r.ctx, gen.GetPurchasingReceiveFormRequestObject{OrderId: o.Id})
	f, err := expect[gen.GetPurchasingReceiveForm200JSONResponse]("read the receive screen", res, err)
	if err != nil {
		return nil, err
	}
	out := map[string]gen.PurchasingReceiveLine{}
	for _, l := range f.Lines {
		if l.Sku != nil && l.DefaultLocationId != nil {
			out[*l.Sku] = l
		}
	}
	return out, nil
}

// receive posts one receipt of the lines.
func (r *demoReceipts) receive(o gen.PurchasingOrder, note string, lines ...gen.PurchasingReceiptLineInput) (gen.PurchasingReceiptResult, error) {
	res, err := r.ops.ReceivePurchasingOrder(r.ctx, gen.ReceivePurchasingOrderRequestObject{OrderId: o.Id, Body: &gen.PurchasingReceiptInput{
		ClientRequestId: uuid.New(), DeliveryNote: ptrOf(note), Lines: lines}})
	out, err := expect[gen.ReceivePurchasingOrder201JSONResponse]("receive on order "+fmt.Sprint(deref64Of(o.Number)), res, err)
	return gen.PurchasingReceiptResult(out), err
}

func deref64Of(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

// partTube receives 120 of the 180 m of steel tube: the rest is on order.
func (r *demoReceipts) partTube(o gen.PurchasingOrder) error {
	f, err := r.form(o)
	if err != nil {
		return err
	}
	tube, ok := f["VRK-ROR-40"]
	if !ok {
		return nil
	}
	_, err = r.receive(o, "NS-FS-30418", gen.PurchasingReceiptLineInput{OrderLineId: tube.OrderLineId, Quantity: "120", LocationId: *tube.DefaultLocationId})
	return err
}

// paintLot receives the whole paint order into one lot, with the supplier's
// lot and the expiry printed on the drums.
func (r *demoReceipts) paintLot(o gen.PurchasingOrder) error {
	f, err := r.form(o)
	if err != nil {
		return err
	}
	paint, ok := f["VRK-LAKK-7016"]
	if !ok {
		return nil
	}
	expiry := openapi_types.Date{Time: r.now.AddDate(1, 0, 0)}
	produced := openapi_types.Date{Time: r.now.AddDate(0, 0, -14)}
	_, err = r.receive(o, "JPC-DN-55102", gen.PurchasingReceiptLineInput{OrderLineId: paint.OrderLineId, Quantity: paint.Open,
		LocationId: *paint.DefaultLocationId, Lot: &gen.PurchasingReceiptLotInput{Number: ptrOf("LAKK-7016-2611"),
			SupplierLot: ptrOf("JPC-B4471"), ProductionDate: &produced, Expiry: &expiry}})
	return err
}

// plywoodUndone receives the plywood and takes it back: the sheets counted
// belonged to another delivery.
func (r *demoReceipts) plywoodUndone(o gen.PurchasingOrder) error {
	f, err := r.form(o)
	if err != nil {
		return err
	}
	ply, ok := f["VRK-FINER-18"]
	if !ok {
		return nil
	}
	got, err := r.receive(o, "NS-FS-30455", gen.PurchasingReceiptLineInput{OrderLineId: ply.OrderLineId, Quantity: ply.Open, LocationId: *ply.DefaultLocationId})
	if err != nil {
		return err
	}
	res, err := r.ops.UndoPurchasingReceipt(r.ctx, gen.UndoPurchasingReceiptRequestObject{ReceiptId: got.Receipt.Id,
		Body: &gen.PurchasingUndoInput{Note: ptrOf("Feiltelling: platene hørte til en annen levering.")}})
	_, err = expect[gen.UndoPurchasingReceipt200JSONResponse]("undo the plywood receipt", res, err)
	return err
}
