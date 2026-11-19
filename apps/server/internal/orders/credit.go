package orders

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/creditlimit"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
	"github.com/vantigo-io/vantigo/server/internal/orders/store"
)

// This file is the customer's credit limit as Orders judges it (invoices
// phase 6 design I38; plan
// docs/superpowers/plans/2026-11-18-invoices-phase6-e-plan.md, step E13):
// contracts.OrderBacklog, what a customer's confirmed orders hold uninvoiced,
// and the confirm's computed warning credit_limit_exceeded. The warning is a
// sales warning of scope credit: never stored, a block at confirm that the
// warnings' own override overrides, and info on a draft. The exposure is
// creditlimit's one definition — Invoices' open NOK through
// contracts.CustomerReceivables, read before the transaction, plus this
// module's own UninvoicedGrossSums, read on it, plus the order itself.

// codeCreditLimitExceeded is the confirm's refusal when the credit warning is
// its only block.
const codeCreditLimitExceeded = "credit_limit_exceeded"

// newOrderBacklog is the module's contracts.OrderBacklog.
func newOrderBacklog(d module.Deps) contracts.OrderBacklog {
	return orderBacklog{pool: d.Pool}
}

// orderBacklog serves from this module's own tables, on the pool.
type orderBacklog struct {
	pool *pgxpool.Pool
}

// UninvoicedForCustomer is UninvoicedGrossSums for every confirmed order.
func (b orderBacklog) UninvoicedForCustomer(ctx context.Context, customerID int32) ([]contracts.CurrencyAmount, error) {
	return uninvoicedGross(ctx, store.New(b.pool), customerID, nil)
}

// uninvoicedGross is UninvoicedGrossSums as the contract's amounts, leaving
// out exclude when it is set.
func uninvoicedGross(ctx context.Context, q *store.Queries, customerID int32, exclude *int64) ([]contracts.CurrencyAmount, error) {
	rows, err := q.UninvoicedGrossSums(ctx, store.UninvoicedGrossSumsParams{CustomerID: customerID, ExcludeOrderID: exclude})
	if err != nil {
		return nil, fmt.Errorf("orders: read customer %d's uninvoiced orders: %w", customerID, err)
	}
	out := make([]contracts.CurrencyAmount, 0, len(rows))
	for _, r := range rows {
		out = append(out, contracts.CurrencyAmount{Currency: r.Currency, Amount: ratOrZero(r.Amount).FloatString(amountPlaces)})
	}
	return out, nil
}

// creditRead is what a credit judgement reads before its transaction: the
// customer's limit, and Invoices' receivables — nil with invoices off
// (invoicesOff) or when they could not be read (failed).
type creditRead struct {
	limit       string
	name        string
	receivables *contracts.CustomerReceivableTotals
	invoicesOff bool
	failed      bool
	// rates are today's rates of the currencies the customer's orders are in
	// (contracts.ExchangeRates, read before the transaction); a currency with
	// none counts for nothing and is listed as uncounted.
	rates creditlimit.Rates
	// showFigures is whether the caller holds invoices:access, and so may see
	// the limit and the exposure.
	showFigures bool
}

// judgeCreditRaise judges a confirmed order's save that raised its gross
// against the customer's credit limit, on the transaction that wrote its
// lines (so the backlog counts them as they now stand): nil within the limit;
// the warning an allowed override overrides, to log; otherwise the refusal
// credit_limit_exceeded. A receivables read that failed never refuses.
func judgeCreditRaise(ctx context.Context, q *store.Queries, c *creditRead, order store.OrdersOrder, o warningOverride, now time.Time) ([]gen.OrdersWarning, error) {
	w, err := creditWarningOf(ctx, q, c, order, nil, nil, gen.OrdersWarningLevelBlock, now)
	if err != nil || w == nil || w.Level != gen.OrdersWarningLevelBlock {
		return nil, err
	}
	if o.requested && o.allowed {
		return []gen.OrdersWarning{*w}, nil
	}
	detail := warningLabel(*w)
	if o.requested {
		detail += " Only a holder of Manage Orders settings may override a block."
	}
	return nil, refuse(problem(http.StatusConflict, codeCreditLimitExceeded, "The customer's credit limit blocks this", detail))
}

// readCredit is the read before the transaction, nil when the customer has no
// limit. A receivables read that fails is logged and judged "could not be
// checked", never refused: an outage of Invoices must not stop sales (plan
// reading E7).
func (s *server) readCredit(ctx context.Context, profile *contracts.CustomerBillingProfile, currencies ...string) (*creditRead, error) {
	if profile == nil || profile.CreditLimit == nil {
		return nil, nil
	}
	c := &creditRead{limit: *profile.CreditLimit, name: profile.Name, showFigures: s.has(ctx, "invoices:access")}
	// The rates of the orders' currencies — this module's own backlog read on
	// the pool for which they are, and the document's own — today, before any
	// transaction. A rate that cannot be read is logged and its currency left
	// uncounted.
	backlog, err := uninvoicedGross(ctx, store.New(s.deps.Pool), profile.ID, nil)
	if err != nil {
		return nil, err
	}
	if s.deps.ExchangeRates != nil {
		if err := noteContractCall(ctx, "ExchangeRates.Rate"); err != nil {
			return nil, err
		}
	}
	c.rates, err = creditlimit.RatesFor(ctx, s.deps.ExchangeRates, s.deps.Clock(), append(creditlimit.Currencies(backlog), currencies...)...)
	if err != nil {
		s.deps.Logger.WarnContext(ctx, "orders: the exchange rates could not be read for the credit limit", "customer_id", profile.ID, "error", err.Error())
		c.rates = creditlimit.Rates{}
	}
	if s.deps.Receivables == nil {
		c.invoicesOff = true
		return c, nil
	}
	if err := noteContractCall(ctx, "Receivables.ReceivablesForCustomer"); err != nil {
		return nil, err
	}
	totals, err := s.deps.Receivables.ReceivablesForCustomer(ctx, profile.ID)
	if err != nil {
		s.deps.Logger.WarnContext(ctx, "orders: the customer's receivables could not be read for its credit limit",
			"customer_id", profile.ID, "error", err.Error())
		c.failed = true
		return c, nil
	}
	c.receivables = &totals
	return c, nil
}

// creditWarning is the computed credit warning for a customer's order on q:
// the exposure with the order (order's id left out of the backlog, its gross
// added) at level when it passes the limit; credit_not_checked at info when
// the receivables could not be read; nil when within the limit or c is nil.
func creditWarning(ctx context.Context, q *store.Queries, c *creditRead, order store.OrdersOrder, level gen.OrdersWarningLevel, now time.Time) (*gen.OrdersWarning, error) {
	return creditWarningOf(ctx, q, c, order, &order.ID,
		[]contracts.CurrencyAmount{{Currency: order.Currency, Amount: ratOrZero(order.GrossTotal).FloatString(amountPlaces)}}, level, now)
}

// creditWarningOf is creditWarning with the backlog's exclusion and the extra
// amounts named: a confirm leaves its own order out and adds its gross; a
// confirmed order's save counts the order in the backlog as its lines now
// stand, and adds nothing.
//
// The figures are money, so only a caller who may see the customer's
// invoices (invoices:access, as Customers' overview gates them) gets them:
// anyone else is told the order is over the limit, without the numbers.
func creditWarningOf(ctx context.Context, q *store.Queries, c *creditRead, order store.OrdersOrder, exclude *int64,
	extra []contracts.CurrencyAmount, level gen.OrdersWarningLevel, now time.Time,
) (*gen.OrdersWarning, error) {
	if c == nil {
		return nil, nil
	}
	w := gen.OrdersWarning{Scope: gen.OrdersWarningScopeCredit, Id: order.CustomerID, Name: c.name, UpdatedAt: now}
	if c.failed {
		w.Level, w.Code = gen.OrdersWarningLevelInfo, ptr(gen.CreditNotChecked)
		w.Text = "The customer's credit could not be checked: its open invoices could not be read."
		return &w, nil
	}
	backlog, err := uninvoicedGross(ctx, q, order.CustomerID, exclude)
	if err != nil {
		return nil, err
	}
	exposure, err := creditlimit.Exposure(c.receivables, backlog, c.rates, extra...)
	if err != nil {
		return nil, err
	}
	exceeds, err := creditlimit.Exceeds(&c.limit, exposure)
	if err != nil || !exceeds {
		return nil, err
	}
	if !c.showFigures {
		w.Level, w.Code, w.Text = level, ptr(gen.CreditLimitExceeded), "The customer is over its credit limit with this order."
		return &w, nil
	}
	figures := gen.OrdersCreditFigures{Limit: c.limit, ExposureNok: exposure.NOK, InvoicesCounted: c.receivables != nil,
		Uncounted: make([]gen.OrdersCreditAmount, 0, len(exposure.Uncounted))}
	text := fmt.Sprintf("Credit limit NOK %s, exposure NOK %s.", c.limit, exposure.NOK)
	var uncounted []string
	for _, u := range exposure.Uncounted {
		figures.Uncounted = append(figures.Uncounted, gen.OrdersCreditAmount{Currency: u.Currency, Amount: u.Amount})
		uncounted = append(uncounted, u.Currency+" "+u.Amount)
	}
	if len(uncounted) > 0 {
		text += " Not counted: " + strings.Join(uncounted, ", ") + "."
	}
	if c.invoicesOff {
		text += " Open invoices are not counted: invoicing is off."
	}
	w.Level, w.Code, w.Credit, w.Text = level, ptr(gen.CreditLimitExceeded), &figures, text
	return &w, nil
}

// draftCreditWarnings is a draft order's credit warning as GET shows it,
// info, before confirm: read through the directory and Invoices on the pool,
// so it is never called inside a transaction. A read that fails is logged and
// shows nothing rather than failing the read.
func (s *server) draftCreditWarnings(ctx context.Context, q *store.Queries, order store.OrdersOrder) []gen.OrdersWarning {
	if order.Status != orderDraft {
		return nil
	}
	profile, err := s.billingProfile(ctx, order.CustomerID)
	if err != nil {
		s.deps.Logger.WarnContext(ctx, "orders: the customer's credit limit could not be read", "customer_id", order.CustomerID, "error", err.Error())
		return nil
	}
	c, err := s.readCredit(ctx, profile, order.Currency)
	if err != nil {
		s.deps.Logger.WarnContext(ctx, "orders: the customer's credit could not be read", "customer_id", order.CustomerID, "error", err.Error())
		return nil
	}
	w, err := creditWarning(ctx, q, c, order, gen.OrdersWarningLevelInfo, s.deps.Clock())
	if err != nil {
		s.deps.Logger.WarnContext(ctx, "orders: the customer's credit could not be judged", "customer_id", order.CustomerID, "error", err.Error())
		return nil
	}
	if w == nil {
		return nil
	}
	return []gen.OrdersWarning{*w}
}
