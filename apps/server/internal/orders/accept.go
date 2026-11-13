package orders

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/events"
	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
	"github.com/vantigo-io/vantigo/server/internal/orders/store"
)

// AcceptQuote Accept a quote
// (POST /api/v1/orders/quotes/{id}/accept)
//
// A sent or expired quote is accepted once (O20), with the optional lines the
// customer took (O21), into a draft order — or, with orders:sales-confirm, a
// confirmed one. The order is made by createOrder (O28) from the quote's
// header and the chosen lines with their frozen prices, its origin channel
// quote and its source the quote at its revision (O27); the quote records
// what it became and orders.quote.accepted.v1 is published, on one
// transaction that locks the variants first, then the quote (MB rule 21).
func (s *server) AcceptQuote(ctx context.Context, req gen.AcceptQuoteRequestObject) (gen.AcceptQuoteResponseObject, error) {
	resp := answers[gen.AcceptQuoteResponseObject]{
		badRequest: func(p gen.OrdersProblem) gen.AcceptQuoteResponseObject {
			return gen.AcceptQuote400ApplicationProblemPlusJSONResponse(p)
		},
		notFound: func() gen.AcceptQuoteResponseObject { return gen.AcceptQuote404Response{} },
		conflict: func(p gen.OrdersProblem) gen.AcceptQuoteResponseObject {
			return gen.AcceptQuote409ApplicationProblemPlusJSONResponse(p)
		},
	}
	f := s.unknownProperties(ctx, acceptInputSchema)
	body := *req.Body
	if !body.Into.Valid() {
		f.add("into", "into is draft_order, confirmed_order or subscription")
	}
	var pl plan
	if body.Into == gen.OrdersAcceptInputIntoSubscription {
		if body.Plan == nil {
			f.add("plan", "A subscription needs its plan: interval, billing and start date")
		} else {
			pl = readPlan(&f, string(body.Plan.Interval), string(body.Plan.Billing), body.Plan.StartDate.Time, body.Plan.EndDate,
				body.Plan.NoticeMonths, body.Plan.InvoiceDay, body.Plan.FinalPeriod)
		}
	} else if body.Plan != nil {
		f.add("plan", "A plan is for an accept into a subscription")
	}
	override := s.readOverride(ctx, &f, body.OverrideWarnings, body.OverrideReason)
	if f.failed() {
		return gen.AcceptQuote400ApplicationProblemPlusJSONResponse(invalid(invalidAcceptTitle, f)), nil
	}
	confirm := body.Into == gen.OrdersAcceptInputIntoConfirmedOrder
	if confirm {
		if !s.has(ctx, permSalesConfirm) {
			return gen.AcceptQuote403JSONResponse(apicommon.ForbiddenBody()), nil
		}
	}
	if body.Into == gen.OrdersAcceptInputIntoSubscription {
		if !s.has(ctx, permSubscriptionsManage) {
			return gen.AcceptQuote403JSONResponse(apicommon.ForbiddenBody()), nil
		}
		return s.acceptIntoSubscription(ctx, req.Id, body, pl, resp)
	}

	q := store.New(s.deps.Pool)
	quote, err := q.GetQuote(ctx, req.Id)
	if noRows(err) {
		return gen.AcceptQuote404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("orders: read quote %d: %w", req.Id, err)
	}
	if err := judgeAnswerable(quote, body.Revision, body.ExpectedUpdatedAt); err != nil {
		return answer(err, resp)
	}
	quoteLines, err := linesOf(ctx, q, ownerQuote, quote.ID)
	if err != nil {
		return nil, err
	}
	var chosen []int64
	if body.OptionalLineIds != nil {
		chosen = *body.OptionalLineIds
	}
	lines, err := acceptedLines(quoteLines, chosen)
	if err != nil {
		return answer(err, resp)
	}
	// The order's kit lines are added with their children (manufacturing
	// phase 2 M145).
	if lines, err = s.withKits(ctx, lines, nil, invalidAcceptTitle); err != nil {
		return answer(err, resp)
	}
	by, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	// Accept and confirm judges the customer as a confirm does, from the
	// billing profile read before the transaction.
	var customer *contracts.CustomerBillingProfile
	var p printer
	var today time.Time
	if confirm {
		if customer, err = s.billingProfile(ctx, quote.CustomerID); err != nil {
			return answer(err, resp)
		}
		if p, err = s.readPrinter(ctx); err != nil {
			return nil, err
		}
		// The day the ledger judges a lot's expiry on, read on the pool
		// (rule 24).
		if today, err = s.todayOf(ctx); err != nil {
			return nil, err
		}
	}

	var out gen.OrdersAccepted
	var confirmed *store.OrdersOrder
	var printed []byte
	err = s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		if err := s.lockLines(ctx, tx, invalidAcceptTitle, lines); err != nil {
			return err
		}
		locked, err := txq.LockQuote(ctx, quote.ID)
		if err != nil {
			return fmt.Errorf("orders: lock quote %d: %w", quote.ID, err)
		}
		if err := judgeAnswerable(locked, body.Revision, body.ExpectedUpdatedAt); err != nil {
			return err
		}
		order, orderLines, err := s.createOrder(ctx, tx, txq, CreateOrder{
			Origin: OrderOrigin{
				Channel: channelQuote, SourceModule: ptr(moduleName), SourceKind: ptr(ownerQuote),
				SourceID: ptr(quoteSource(locked.ID, locked.Revision)),
			},
			Header: headerOfQuote(locked), Lines: lines, CreatedBy: by,
		})
		if err != nil {
			return err
		}
		if confirm {
			if order, err = confirmOrderTx(ctx, s, tx, txq, order, orderLines, override, by, customer, true, today); err != nil {
				return err
			}
			if printed, err = p.confirmation(order, orderLines, s.deps.Clock()); err != nil {
				return err
			}
			confirmed = &order
		}
		now := s.deps.Clock()
		accepted, err := txq.AcceptQuoteRow(ctx, store.AcceptQuoteRowParams{ID: locked.ID, Now: &now, IntoKind: ownerOrder, IntoID: order.ID})
		if err != nil {
			return fmt.Errorf("orders: accept quote %d: %w", locked.ID, err)
		}
		if err := events.Publish(ctx, tx, moduleName, contracts.QuoteAccepted{
			QuoteID: accepted.ID, Number: accepted.Number, Revision: accepted.Revision, CustomerID: accepted.CustomerID,
			AcceptedIntoKind: ownerOrder, AcceptedIntoID: order.ID, AcceptedAt: now,
		}, now); err != nil {
			return err
		}
		warnings, err := documentWarnings(ctx, txq, accepted.CustomerID, quoteLines)
		if err != nil {
			return err
		}
		orderWarnings, err := documentWarnings(ctx, txq, order.CustomerID, orderLines)
		if err != nil {
			return err
		}
		view := orderView(order, orderLines, orderWarnings, nil)
		out = gen.OrdersAccepted{Quote: quoteView(accepted, quoteLines, &order.Number, warnings), Order: &view}
		return nil
	})
	if err != nil {
		return answer(err, resp)
	}
	if confirmed != nil && afterOrderConfirmed != nil {
		afterOrderConfirmed(ctx, s, *confirmed, printed)
	}
	return gen.AcceptQuote200JSONResponse(out), nil
}

// acceptedLines are the lines an accept carries to the order (O21): every
// line that is not optional, and the optional lines the customer took, as
// ordinary lines with their frozen prices; the rest are dropped. A chosen id
// that is not one of the quote's optional lines is refused.
func acceptedLines(lines []store.OrdersLine, chosen []int64) ([]pricedLine, error) {
	optional := map[int64]bool{}
	for _, l := range lines {
		if l.Optional {
			optional[l.ID] = true
		}
	}
	var f fieldErrors
	for _, id := range chosen {
		if !optional[id] {
			f.add("optionalLineIds", fmt.Sprintf("Line %d is not one of the quote's optional lines", id))
		}
	}
	if f.failed() {
		return nil, refuseInvalid(invalidAcceptTitle, f)
	}
	out := make([]pricedLine, 0, len(lines))
	for _, l := range lines {
		if l.Optional && !slices.Contains(chosen, l.ID) {
			continue
		}
		p := pricedLineOf(l)
		p.optional = false
		out = append(out, p)
	}
	return out, nil
}

// acceptIntoSubscription is an accept into a subscription (O20, O42): the
// quote's header and chosen lines, with their frozen prices, become a draft
// subscription on plan, its origin channel quote and its source the quote at
// its revision. Every catalog line must be a service
// (subscription_goods_not_allowed); the catalog is read before the
// transaction, which locks the variants first, then the quote.
func (s *server) acceptIntoSubscription(ctx context.Context, id int64, body gen.OrdersAcceptInput, pl plan,
	resp answers[gen.AcceptQuoteResponseObject]) (gen.AcceptQuoteResponseObject, error) {
	q := store.New(s.deps.Pool)
	quote, err := q.GetQuote(ctx, id)
	if noRows(err) {
		return gen.AcceptQuote404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("orders: read quote %d: %w", id, err)
	}
	if err := judgeAnswerable(quote, body.Revision, body.ExpectedUpdatedAt); err != nil {
		return answer(err, resp)
	}
	quoteLines, err := linesOf(ctx, q, ownerQuote, quote.ID)
	if err != nil {
		return nil, err
	}
	var chosen []int64
	if body.OptionalLineIds != nil {
		chosen = *body.OptionalLineIds
	}
	lines, err := acceptedLines(quoteLines, chosen)
	if err != nil {
		return answer(err, resp)
	}
	variants, err := s.catalogVariants(ctx, variantIDsOf(lines))
	if err != nil {
		return answer(err, resp)
	}
	if failures := judgeServices(lines, variants); len(failures) > 0 {
		return answer(refuseLines(invalidAcceptTitle, failures), resp)
	}
	by, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	today, err := s.todayOf(ctx)
	if err != nil {
		return answer(err, resp)
	}
	var out gen.OrdersAccepted
	err = s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		if err := s.lockLines(ctx, tx, invalidAcceptTitle, lines); err != nil {
			return err
		}
		locked, err := txq.LockQuote(ctx, quote.ID)
		if err != nil {
			return fmt.Errorf("orders: lock quote %d: %w", quote.ID, err)
		}
		if err := judgeAnswerable(locked, body.Revision, body.ExpectedUpdatedAt); err != nil {
			return err
		}
		sub, _, err := s.createSubscriptionRow(ctx, txq, subscriptionOrigin{
			Channel: channelQuote, SourceModule: ptr(moduleName), SourceKind: ptr(ownerQuote),
			SourceID: ptr(quoteSource(locked.ID, locked.Revision)),
		}, headerOfQuote(locked), pl, lines, by)
		if err != nil {
			return err
		}
		now := s.deps.Clock()
		accepted, err := txq.AcceptQuoteIntoSubscription(ctx, store.AcceptQuoteIntoSubscriptionParams{ID: locked.ID, Now: &now, IntoID: &sub.ID})
		if err != nil {
			return fmt.Errorf("orders: accept quote %d: %w", locked.ID, err)
		}
		if err := events.Publish(ctx, tx, moduleName, contracts.QuoteAccepted{
			QuoteID: accepted.ID, Number: accepted.Number, Revision: accepted.Revision, CustomerID: accepted.CustomerID,
			AcceptedIntoKind: ownerSubscription, AcceptedIntoID: sub.ID, AcceptedAt: now,
		}, now); err != nil {
			return err
		}
		warnings, err := documentWarnings(ctx, txq, accepted.CustomerID, quoteLines)
		if err != nil {
			return err
		}
		view, err := viewOn(ctx, txq, sub, today)
		if err != nil {
			return err
		}
		out = gen.OrdersAccepted{Quote: quoteView(accepted, quoteLines, &sub.Number, warnings), Subscription: &view}
		return nil
	})
	if err != nil {
		return answer(err, resp)
	}
	return gen.AcceptQuote200JSONResponse(out), nil
}
