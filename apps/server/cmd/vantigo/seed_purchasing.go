package main

// The demo seed's purchasing (purchasing design, delivery A): the workshop's
// three suppliers in Accounting's register — open before the books are set
// up (P2) —, their supplier items of the workshop's raw materials and goods,
// and purchase orders in every state delivery A makes: a draft, a sent order
// with the steel tube the workshop is low on, a confirmed one, one with a
// line closed, a closed one and a cancelled one. Every write goes through the
// modules' own operations (purchasing.Operations, accounting.Operations) as
// the owner, so the seed writes nothing their rules would refuse. It runs
// after the workshop, whose site and goods it buys for, and before the books;
// once — an installation with a purchase order skips it. The orders sent
// store a PDF, so they need an object store (STORAGE_PROVIDER); without one
// only the suppliers, the items and the draft are made.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/accounting"
	acctgen "github.com/vantigo-io/vantigo/server/internal/accounting/gen"
	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/identity"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/purchasing"
	"github.com/vantigo-io/vantigo/server/internal/purchasing/gen"
)

// demoPurchaseSupplier is one of the workshop's suppliers.
type demoPurchaseSupplier struct {
	key, name, address, postal, city, email string
}

var demoPurchaseSuppliers = []demoPurchaseSupplier{
	{key: "steel", name: "Norsk Stål AS", address: "Verkstedveien 8", postal: "2010", city: "Strømmen", email: "ordre@norskstal.example"},
	{key: "paint", name: "Jotun Powder Coatings AS", address: "Hystadveien 167", postal: "3209", city: "Sandefjord", email: "orders@jotun-powder.example"},
	{key: "tools", name: "Verktøygrossisten AS", address: "Grossistveien 3", postal: "1405", city: "Langhus", email: "salg@verktoygrossisten.example"},
}

// demoSupplierItem is one supplier item: the workshop product, the price,
// the supplier's number and the lead time.
type demoSupplierItem struct {
	supplier, product, number, price string
	leadDays                         int32
	minimum                          string
}

var demoSupplierItems = []demoSupplierItem{
	{supplier: "steel", product: "tube", number: "NS-RHS40-2", price: "58.40", leadDays: 5, minimum: "60"},
	{supplier: "steel", product: "screw", number: "NS-M8X20", price: "0.95", leadDays: 3, minimum: "500"},
	{supplier: "steel", product: "plywood", number: "NS-BJF18", price: "612.00", leadDays: 7},
	{supplier: "paint", product: "paint", number: "JPC-7016-20", price: "172.50", leadDays: 10, minimum: "20"},
	{supplier: "tools", product: "pegboard", number: "VG-TAVLE-120", price: "289.00", leadDays: 4},
	{supplier: "tools", product: "vise", number: "VG-SKS-125", price: "498.00", leadDays: 4},
}

// seedDemoPurchasing seeds Purchasing's demo.
func seedDemoPurchasing(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger, cfg *config.Config, now time.Time) error {
	for _, m := range []string{"purchasing", "accounting", "inventory", "products"} {
		if !slices.Contains(cfg.Modules, m) {
			logger.Info("purchasing demo not seeded: MODULES leaves out " + m)
			return nil
		}
	}
	var siteID int64
	var seeded bool
	err := pool.QueryRow(ctx, `SELECT coalesce((SELECT id FROM inventory.sites WHERE code = $1), 0),
		EXISTS (SELECT 1 FROM purchasing.orders)`, demoWorkshopSiteCode).Scan(&siteID, &seeded)
	if err != nil {
		return fmt.Errorf("check the purchasing demo: %w", err)
	}
	if siteID == 0 || seeded {
		logger.Info("purchasing demo: no workshop site, or purchase orders already exist; skipping")
		return nil
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
	p := &demoPurchases{ctx: contracts.AsUser(contracts.WithPrincipal(ctx, contracts.Principal{UserID: owner}), owner),
		ops: ops, acct: accounting.Operations(deps), siteID: siteID, now: now, suppliers: map[string]int64{}, variants: map[string]int32{}}
	for _, pr := range demoWorkshopProducts {
		var id int32
		if err := pool.QueryRow(ctx, `SELECT id FROM products.product_variants WHERE sku = $1`, pr.sku).Scan(&id); err != nil {
			return fmt.Errorf("read the variant %s: %w", pr.sku, err)
		}
		p.variants[pr.key] = id
	}
	storage := cfg.StorageProvider != ""
	steps := []struct {
		name string
		run  func() error
	}{
		{"add the suppliers", p.addSuppliers},
		{"set the default site", p.settings},
		{"add the supplier items", p.addItems},
		{"make the purchase orders", func() error { return p.orders(storage) }},
	}
	for _, step := range steps {
		if err := step.run(); err != nil {
			return fmt.Errorf("purchasing demo: %s: %w", step.name, err)
		}
	}
	if !storage {
		logger.Info("purchasing demo: no object store (STORAGE_PROVIDER) configured; only the draft is made, as a sent order stores its PDF")
	}
	logger.Info("purchasing demo seeded", "suppliers", len(p.suppliers), "supplier_items", len(demoSupplierItems), "orders", p.orderCount)
	return nil
}

// demoPurchases is seedDemoPurchasing's state.
type demoPurchases struct {
	ctx        context.Context
	ops        gen.StrictServerInterface
	acct       acctgen.StrictServerInterface
	siteID     int64
	now        time.Time
	suppliers  map[string]int64
	variants   map[string]int32
	orderCount int
}

// expect is an operation's response as the type it answers on success, or
// an error naming what was refused and why.
func expect[T any](what string, res any, err error) (T, error) {
	var zero T
	if err != nil {
		if p, ok := purchasing.RefusalProblem(err); ok {
			body, _ := json.Marshal(p)
			return zero, fmt.Errorf("%s: refused %s", what, body)
		}
		if p, ok := accounting.RefusalProblem(err); ok {
			body, _ := json.Marshal(p)
			return zero, fmt.Errorf("%s: refused %s", what, body)
		}
		return zero, fmt.Errorf("%s: %w", what, err)
	}
	out, ok := res.(T)
	if !ok {
		body, _ := json.Marshal(res)
		return zero, fmt.Errorf("%s: answered %T %s", what, res, body)
	}
	return out, nil
}

func (p *demoPurchases) addSuppliers() error {
	terms := int32(30)
	for i, s := range demoPurchaseSuppliers {
		res, err := p.acct.CreateAccountingSupplier(p.ctx, acctgen.CreateAccountingSupplierRequestObject{Body: &acctgen.AccountingSupplierInput{
			Name: s.name, AddressLine: ptrOf(s.address), PostalCode: ptrOf(s.postal), City: ptrOf(s.city), Email: ptrOf(s.email),
			OrganisationNumber: ptrOf(norwegianOrgNumber(2000 + i)), PaymentTermsDays: &terms,
		}})
		created, err := expect[acctgen.CreateAccountingSupplier201JSONResponse]("add the supplier "+s.name, res, err)
		if err != nil {
			return err
		}
		p.suppliers[s.key] = created.Id
	}
	return nil
}

func (p *demoPurchases) settings() error {
	res, err := p.ops.GetPurchasingSettings(p.ctx, gen.GetPurchasingSettingsRequestObject{})
	cur, err := expect[gen.GetPurchasingSettings200JSONResponse]("read the settings", res, err)
	if err != nil {
		return err
	}
	res2, err := p.ops.PutPurchasingSettings(p.ctx, gen.PutPurchasingSettingsRequestObject{Body: &gen.PurchasingSettingsInput{
		Revision: cur.Revision, OrderSeriesStart: 4501, DefaultSiteId: &p.siteID, PriceTolerancePercent: "2",
		OrderFooter: "Leveres til varemottaket, hverdager 07–15. Merk pakkseddelen med vårt ordrenummer.", DeliveryAddresses: []gen.PurchasingSiteAddress{},
	}})
	_, err = expect[gen.PutPurchasingSettings200JSONResponse]("save the settings", res2, err)
	return err
}

func (p *demoPurchases) addItems() error {
	for _, it := range demoSupplierItems {
		in := gen.PurchasingSupplierItemInput{SupplierId: p.suppliers[it.supplier], VariantId: p.variants[it.product],
			SupplierItemNumber: ptrOf(it.number), Price: ptrOf(it.price), LeadTimeDays: ptrOf(it.leadDays)}
		if it.minimum != "" {
			in.MinimumQuantity = ptrOf(it.minimum)
		}
		res, err := p.ops.CreatePurchasingSupplierItem(p.ctx, gen.CreatePurchasingSupplierItemRequestObject{Body: &in})
		if _, err := expect[gen.CreatePurchasingSupplierItem201JSONResponse]("add the item "+it.number, res, err); err != nil {
			return err
		}
	}
	return nil
}

// draft makes a draft for supplier with lines of product → quantity.
func (p *demoPurchases) draft(supplier, note string, lines ...[2]string) (gen.PurchasingOrder, error) {
	in := make([]gen.PurchasingLineInput, 0, len(lines))
	for _, l := range lines {
		v := p.variants[l[0]]
		in = append(in, gen.PurchasingLineInput{Kind: gen.PurchasingLineInputKindVariant, VariantId: &v, OrderQuantity: l[1]})
	}
	res, err := p.ops.CreatePurchasingOrder(p.ctx, gen.CreatePurchasingOrderRequestObject{Body: &gen.PurchasingOrderInput{
		SupplierId: p.suppliers[supplier], SiteId: &p.siteID, Note: &note, Lines: &in}})
	o, err := expect[gen.CreatePurchasingOrder201JSONResponse]("make a draft for "+supplier, res, err)
	if err == nil {
		p.orderCount++
	}
	return gen.PurchasingOrder(o), err
}

// sent makes a draft and marks it as sent: the supplier was rung, or the
// order placed in its web shop.
func (p *demoPurchases) sent(supplier, note string, lines ...[2]string) (gen.PurchasingOrder, error) {
	o, err := p.draft(supplier, note, lines...)
	if err != nil {
		return o, err
	}
	res, err := p.ops.MarkPurchasingOrderSent(p.ctx, gen.MarkPurchasingOrderSentRequestObject{OrderId: o.Id,
		Body: &gen.PurchasingMarkSentInput{ClientRequestId: uuid.New(), ExpectedUpdatedAt: o.UpdatedAt}})
	r, err := expect[gen.MarkPurchasingOrderSent200JSONResponse]("mark the order to "+supplier+" as sent", res, err)
	return r.Order, err
}

func (p *demoPurchases) orders(storage bool) error {
	if _, err := p.draft("tools", "Påfyll av verktøytavler til butikken.", [2]string{"pegboard", "6"}); err != nil {
		return err
	}
	if !storage {
		return nil
	}
	// The steel the workshop is low on, on order.
	if _, err := p.sent("steel", "Stålrør til neste serie arbeidsbenker.", [2]string{"tube", "180"}, [2]string{"screw", "1000"}); err != nil {
		return err
	}
	// The paint, confirmed by the supplier.
	paint, err := p.sent("paint", "", [2]string{"paint", "40"})
	if err != nil {
		return err
	}
	day := openapi_types.Date{Time: p.now.AddDate(0, 0, 10)}
	if paint.Lines[0].ExpectedOn != nil {
		day = *paint.Lines[0].ExpectedOn
	}
	res, err := p.ops.ConfirmPurchasingOrder(p.ctx, gen.ConfirmPurchasingOrderRequestObject{OrderId: paint.Id, Body: &gen.PurchasingConfirmationInput{
		ExpectedUpdatedAt: paint.UpdatedAt, SupplierReference: ptrOf("JPC-SO-77812"), ConfirmedOn: &day}})
	if _, err := expect[gen.ConfirmPurchasingOrder200JSONResponse]("confirm the paint", res, err); err != nil {
		return err
	}
	// Plywood and screws, the screws' line closed: the supplier is out.
	ply, err := p.sent("steel", "", [2]string{"plywood", "10"}, [2]string{"screw", "500"})
	if err != nil {
		return err
	}
	res2, err := p.ops.ClosePurchasingOrderLine(p.ctx, gen.ClosePurchasingOrderLineRequestObject{OrderId: ply.Id, LineId: ply.Lines[1].Id,
		Body: &gen.PurchasingVersionInput{ExpectedUpdatedAt: ply.UpdatedAt}})
	if _, err := expect[gen.ClosePurchasingOrderLine200JSONResponse]("close the screws' line", res2, err); err != nil {
		return err
	}
	// Vises, closed with the order's remainder.
	vise, err := p.sent("tools", "", [2]string{"vise", "4"})
	if err != nil {
		return err
	}
	res3, err := p.ops.ClosePurchasingOrder(p.ctx, gen.ClosePurchasingOrderRequestObject{OrderId: vise.Id, Body: &gen.PurchasingVersionInput{ExpectedUpdatedAt: vise.UpdatedAt}})
	if _, err := expect[gen.ClosePurchasingOrder200JSONResponse]("close the vises' order", res3, err); err != nil {
		return err
	}
	// Paint ordered twice by mistake: cancelled.
	twice, err := p.sent("paint", "Feilbestilling.", [2]string{"paint", "20"})
	if err != nil {
		return err
	}
	res4, err := p.ops.CancelPurchasingOrder(p.ctx, gen.CancelPurchasingOrderRequestObject{OrderId: twice.Id, Body: &gen.PurchasingCancelInput{ExpectedUpdatedAt: twice.UpdatedAt}})
	_, err = expect[gen.CancelPurchasingOrder200JSONResponse]("cancel the second paint order", res4, err)
	return err
}
