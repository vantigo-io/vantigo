package main

// The demo seed's invoice match (purchasing design, delivery D), after the
// books are set up — a supplier invoice is Accounting's, and Accounting takes
// none before its setup:
//
//   - the paint order, received in full, gets its supplier invoice through
//     Register invoice — Accounting's draft with the order number as its
//     reference and the supplier's default account and tax code — which is
//     then posted with its PDF, fixing the match;
//   - the steel tube order, partly received, gets a supplier invoice a
//     bookkeeper typed in Accounting naming the order in its reference, at
//     a dearer metre than ordered, matched for the 120 m received: a draft,
//     its badge counting the price difference.
//
// Every write goes through purchasing.Operations and accounting.Operations
// as the owner. It reruns part by part: an order with a match is left as it
// is, and a registered invoice still a draft is posted by the next run.

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"math/big"
	"mime/multipart"
	"slices"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/accounting"
	acctgen "github.com/vantigo-io/vantigo/server/internal/accounting/gen"
	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/events"
	"github.com/vantigo-io/vantigo/server/internal/identity"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/purchasing"
	"github.com/vantigo-io/vantigo/server/internal/purchasing/gen"
)

// The demo's two supplier invoices of the match.
const (
	demoPaintInvoiceNumber = "JPC-F-88412"
	demoSteelInvoiceNumber = "NS-F-30502"
	demoSteelTubePrice     = "61.90" // a metre, against the order's 58.40
)

// seedDemoPurchasingMatches seeds the two matched supplier invoices.
func seedDemoPurchasingMatches(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger, cfg *config.Config, now time.Time) error {
	for _, m := range []string{"purchasing", "accounting", "inventory", "products"} {
		if !slices.Contains(cfg.Modules, m) {
			return nil
		}
	}
	var setUp bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM accounting.settings)`).Scan(&setUp); err != nil {
		return fmt.Errorf("check the books: %w", err)
	}
	if !setUp {
		logger.Info("purchasing match demo: the books are not set up; skipping the supplier invoices")
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
	m := &demoMatches{ctx: contracts.AsUser(contracts.WithPrincipal(ctx, contracts.Principal{UserID: owner}), owner), pool: pool,
		deps: deps, ops: ops, acct: accounting.Operations(deps), now: now, storage: cfg.StorageProvider != ""}
	made := 0
	for _, step := range []struct {
		name string
		run  func() (bool, error)
	}{
		{"register and post the paint invoice", m.paint},
		{"match the steel invoice with its price difference", m.steel},
	} {
		ok, err := step.run()
		if err != nil {
			return fmt.Errorf("purchasing match demo: %s: %w", step.name, err)
		}
		if ok {
			made++
		}
	}
	logger.Info("purchasing match demo seeded", "parts", made)
	return nil
}

// demoMatches is seedDemoPurchasingMatches' state.
type demoMatches struct {
	ctx     context.Context
	pool    *pgxpool.Pool
	deps    module.Deps
	ops     gen.StrictServerInterface
	acct    acctgen.StrictServerInterface
	now     time.Time
	storage bool
}

// orderWith is the received or partly received demo order with a line of
// the SKU, 0 when there is none.
func (m *demoMatches) orderWith(sku string) (int64, error) {
	var id int64
	err := m.pool.QueryRow(m.ctx, `
		SELECT coalesce((SELECT o.id FROM purchasing.orders o
			WHERE o.status IN ('partially_received', 'received')
			  AND EXISTS (SELECT 1 FROM purchasing.order_lines l WHERE l.order_id = o.id AND l.sku = $1 AND l.received > 0)
			ORDER BY o.id LIMIT 1), 0)`, sku).Scan(&id)
	return id, err
}

// chartIDs is the books' purchase account and the 25 % input VAT code.
func (m *demoMatches) chartIDs() (int32, int32, error) {
	var account, code int32
	err := m.pool.QueryRow(m.ctx, `
		SELECT (SELECT id FROM accounting.accounts WHERE number = ANY('{4300,4000,6300}') ORDER BY array_position('{4300,4000,6300}', number) LIMIT 1),
		       (SELECT id FROM accounting.tax_codes WHERE code = '1' LIMIT 1)`).Scan(&account, &code)
	return account, code, err
}

// paint registers the paint order's invoice from the order and posts it.
func (m *demoMatches) paint() (bool, error) {
	orderID, err := m.orderWith("VRK-LAKK-7016")
	if err != nil || orderID == 0 {
		return false, err
	}
	var invoiceID int64
	if err := m.pool.QueryRow(m.ctx, `SELECT coalesce((SELECT supplier_invoice_id FROM purchasing.invoice_registrations
		WHERE order_id = $1 ORDER BY created_at LIMIT 1), 0)`, orderID).Scan(&invoiceID); err != nil {
		return false, err
	}
	if invoiceID == 0 {
		var matched bool
		if err := m.pool.QueryRow(m.ctx, `SELECT EXISTS (SELECT 1 FROM purchasing.invoice_matches WHERE order_id = $1)`, orderID).Scan(&matched); err != nil || matched {
			return false, err
		}
	}
	res, err := m.ops.GetPurchasingOrder(m.ctx, gen.GetPurchasingOrderRequestObject{OrderId: orderID})
	order, err := expect[gen.GetPurchasingOrder200JSONResponse]("read the paint order", res, err)
	if err != nil {
		return false, err
	}
	if invoiceID == 0 {
		if err := m.supplierDefaults(order.SupplierId); err != nil {
			return false, err
		}
		// The supplier's total: the net with 25 % VAT.
		gross := new(big.Rat).Mul(mustDecimal(order.NetTotal), big.NewRat(5, 4))
		date := m.now.AddDate(0, 0, -2)
		res, err := m.ops.RegisterPurchasingInvoice(m.ctx, gen.RegisterPurchasingInvoiceRequestObject{OrderId: orderID,
			Body: &gen.PurchasingInvoiceRegisterInput{ClientRequestId: uuid.New(), InvoiceNumber: demoPaintInvoiceNumber,
				InvoiceDate: openapi_types.Date{Time: date}, Total: ptrOf(gross.FloatString(2))}})
		reg, err := expect[gen.RegisterPurchasingInvoice201JSONResponse]("register the paint invoice", res, err)
		if err != nil {
			return false, err
		}
		invoiceID = reg.SupplierInvoiceId
	}
	var state string
	if err := m.pool.QueryRow(m.ctx, `SELECT state FROM accounting.supplier_invoices WHERE id = $1`, invoiceID).Scan(&state); err != nil {
		return false, err
	}
	if state != "draft" || !m.storage {
		return true, nil
	}
	var files int
	if err := m.pool.QueryRow(m.ctx, `SELECT count(*) FROM accounting.supplier_invoice_files WHERE supplier_invoice_id = $1`, invoiceID).Scan(&files); err != nil {
		return false, err
	}
	if files == 0 {
		var name, address, postal, city string
		if err := m.pool.QueryRow(m.ctx, `SELECT name, coalesce(address_line, ''), coalesce(postal_code, ''), coalesce(city, '')
			FROM accounting.suppliers WHERE id = $1`, order.SupplierId).Scan(&name, &address, &postal, &city); err != nil {
			return false, err
		}
		var total string
		if err := m.pool.QueryRow(m.ctx, `SELECT total::text FROM accounting.supplier_invoices WHERE id = $1`, invoiceID).Scan(&total); err != nil {
			return false, err
		}
		cents, _ := strconv.ParseInt(new(big.Rat).Mul(mustDecimal(total), big.NewRat(100, 1)).FloatString(0), 10, 64)
		date := m.now.AddDate(0, 0, -2)
		body, boundary, err := demoInvoicePDF(demoSupplier{name: name, address: address, postal: postal, city: city},
			demoSupplierInvoice{number: demoPaintInvoiceNumber, text: fmt.Sprintf("Pulverlakk RAL 7016 etter deres ordre %d", deref64Of(order.Number)),
				amount: cents}, date, date.AddDate(0, 0, 30))
		if err != nil {
			return false, err
		}
		ures, err := m.acct.UploadAccountingSupplierInvoiceFile(m.ctx, acctgen.UploadAccountingSupplierInvoiceFileRequestObject{
			Id: invoiceID, Body: multipart.NewReader(bytes.NewReader(body), boundary)})
		if _, err := answer[acctgen.UploadAccountingSupplierInvoiceFile201JSONResponse]("attach the paint invoice's PDF", ures, err); err != nil {
			return false, err
		}
	}
	pres, err := m.acct.PostAccountingSupplierInvoice(m.ctx, acctgen.PostAccountingSupplierInvoiceRequestObject{Id: invoiceID})
	if _, err := answer[acctgen.PostAccountingSupplierInvoice200JSONResponse]("post the paint invoice", pres, err); err != nil {
		return false, err
	}
	// Purchasing's handlers take the posting now, as the events worker
	// would; the last price follows from the purchasing.invoice-posted
	// worker once the server runs.
	if err := events.NewDispatcher(m.pool, purchasing.Module().EventHandlers(m.deps), m.deps.Logger, m.deps.Clock).RunOnce(m.ctx); err != nil {
		return false, fmt.Errorf("deliver the posting: %w", err)
	}
	return true, nil
}

// supplierDefaults gives the supplier the books' purchase account and the
// 25 % input VAT as its defaults when it has no account, as a bookkeeper
// would before registering its first invoice.
func (m *demoMatches) supplierDefaults(supplierID int64) error {
	res, err := m.acct.GetAccountingSupplier(m.ctx, acctgen.GetAccountingSupplierRequestObject{Id: supplierID})
	sup, err := answer[acctgen.GetAccountingSupplier200JSONResponse]("read the supplier", res, err)
	if err != nil || sup.DefaultAccountId != nil {
		return err
	}
	account, code, err := m.chartIDs()
	if err != nil {
		return err
	}
	ures, err := m.acct.UpdateAccountingSupplier(m.ctx, acctgen.UpdateAccountingSupplierRequestObject{Id: supplierID,
		Body: &acctgen.AccountingSupplierInput{Name: sup.Name, Revision: ptrOf(sup.Revision), DefaultAccountId: &account, DefaultTaxCodeId: &code}})
	_, err = answer[acctgen.UpdateAccountingSupplier200JSONResponse]("give the supplier its defaults", ures, err)
	return err
}

// steel enters the steel supplier's invoice for the 120 m received at a
// dearer metre, naming the order, and matches it: left a draft.
func (m *demoMatches) steel() (bool, error) {
	orderID, err := m.orderWith("VRK-ROR-40")
	if err != nil || orderID == 0 {
		return false, err
	}
	var matched bool
	if err := m.pool.QueryRow(m.ctx, `SELECT EXISTS (SELECT 1 FROM purchasing.invoice_matches WHERE order_id = $1)`, orderID).Scan(&matched); err != nil || matched {
		return false, err
	}
	res, err := m.ops.GetPurchasingOrder(m.ctx, gen.GetPurchasingOrderRequestObject{OrderId: orderID})
	order, err := expect[gen.GetPurchasingOrder200JSONResponse]("read the steel order", res, err)
	if err != nil {
		return false, err
	}
	var tube *gen.PurchasingOrderLine
	for i, l := range order.Lines {
		if l.Sku != nil && *l.Sku == "VRK-ROR-40" {
			tube = &order.Lines[i]
		}
	}
	if tube == nil {
		return false, nil
	}
	account, code, err := m.chartIDs()
	if err != nil {
		return false, err
	}
	var invoiceID int64
	if err := m.pool.QueryRow(m.ctx, `SELECT coalesce((SELECT id FROM accounting.supplier_invoices WHERE supplier_id = $1
		AND supplier_number = $2 AND state = 'draft' LIMIT 1), 0)`, order.SupplierId, demoSteelInvoiceNumber).Scan(&invoiceID); err != nil {
		return false, err
	}
	if invoiceID == 0 {
		net := new(big.Rat).Mul(mustDecimal(tube.Received), mustDecimal(demoSteelTubePrice))
		gross := new(big.Rat).Mul(net, big.NewRat(5, 4))
		res, err := m.acct.CreateAccountingSupplierInvoice(m.ctx, acctgen.CreateAccountingSupplierInvoiceRequestObject{Body: &acctgen.AccountingSupplierInvoiceInput{
			SupplierId: order.SupplierId, Kind: acctgen.AccountingSupplierInvoiceKind("invoice"), InvoiceNumber: demoSteelInvoiceNumber,
			InvoiceDate: openapi_types.Date{Time: m.now.AddDate(0, 0, -1)}, Total: gross.FloatString(2),
			Reference: ptrOf(fmt.Sprintf("Deres ordre %d", deref64Of(order.Number))),
			Lines: []acctgen.AccountingSupplierInvoiceLineInput{{AccountId: account, TaxCodeId: &code, Amount: net.FloatString(2),
				Entry: ptrOf(acctgen.Net), Text: ptrOf(fmt.Sprintf("Stålrør 40x40, %s m", tube.Received))}}}})
		created, err := answer[acctgen.CreateAccountingSupplierInvoice201JSONResponse]("enter the steel invoice", res, err)
		if err != nil {
			return false, err
		}
		invoiceID = created.Id
	}
	mres, err := m.ops.MatchPurchasingInvoice(m.ctx, gen.MatchPurchasingInvoiceRequestObject{InvoiceId: invoiceID,
		Body: &gen.PurchasingInvoiceMatchInput{OrderId: orderID, Lines: []gen.PurchasingInvoiceMatchLineInput{
			{LinePosition: 1, OrderLineId: tube.Id, Quantity: tube.Received}}}})
	_, err = expect[gen.MatchPurchasingInvoice200JSONResponse]("match the steel invoice", mres, err)
	return err == nil, err
}

// mustDecimal reads an API decimal; the demo's are always valid.
func mustDecimal(s string) *big.Rat {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		panic("purchasing match demo: not a decimal: " + s)
	}
	return r
}
