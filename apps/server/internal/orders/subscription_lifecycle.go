package orders

// A subscription's life (orders design O42, O44): activate a draft, pause
// from a period, resume from a later one — the periods between skipped —
// end it, and cancel a draft or one with nothing billed. Each step reads what
// it needs before its transaction, locks the subscription's row, judges it
// again under the lock at the version the client read, bumps the version,
// logs one change and publishes its event while it holds the row (O3).

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/events"
	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
	"github.com/vantigo-io/vantigo/server/internal/orders/store"
)

// lifecycleAnswers are the typed responses of a lifecycle step.
func lifecycleAnswers[R any](bad func(gen.OrdersProblem) R, nf func() R, conf func(gen.OrdersProblem) R) answers[R] {
	return answers[R]{badRequest: bad, notFound: nf, conflict: conf}
}

// stepState is a subscription's lifecycle columns as SetSubscriptionState
// writes them, from the row as it is.
func stepState(sub store.OrdersSubscription, now time.Time) store.SetSubscriptionStateParams {
	return store.SetSubscriptionStateParams{
		ID: sub.ID, Status: sub.Status, PausedFrom: sub.PausedFrom, EndDate: sub.EndDate, FinalPeriod: sub.FinalPeriod, ActivatedAt: sub.ActivatedAt,
		ActivatedByUserID: sub.ActivatedByUserID, ActivatedByDisplay: sub.ActivatedByDisplay, EndedAt: sub.EndedAt,
		CancelledAt: sub.CancelledAt, Version: sub.Version + 1, Now: now,
	}
}

// step runs one lifecycle step: under the subscription's lock, at the
// version the client read, write judges and writes it on the transaction and
// answers its change log's actions; the step's version is logged with them
// and the subscription is answered as it then is.
func (s *server) step(ctx context.Context, id int64, version int32, expected time.Time,
	write func(ctx context.Context, tx pgx.Tx, txq *store.Queries, sch schedule, by actor, now time.Time) (store.OrdersSubscription, []changeAction, error),
) (gen.OrdersSubscription, error) {
	today, err := s.todayOf(ctx)
	if err != nil {
		return gen.OrdersSubscription{}, err
	}
	by, err := s.actorFor(ctx)
	if err != nil {
		return gen.OrdersSubscription{}, err
	}
	var out gen.OrdersSubscription
	err = s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		sub, err := lockSubscription(ctx, txq, id)
		if err != nil {
			return err
		}
		if err := judgeSubscriptionVersion(sub, version, expected); err != nil {
			return err
		}
		sch, err := readSchedule(ctx, txq, sub)
		if err != nil {
			return err
		}
		now := s.deps.Clock()
		row, actions, err := write(ctx, tx, txq, sch, by, now)
		if err != nil {
			return err
		}
		if err := logSubscriptionChange(ctx, txq, row.ID, row.Version, by, now, actions); err != nil {
			return err
		}
		out, err = viewOn(ctx, txq, row, today)
		return err
	})
	return out, err
}

// ActivateSubscription Activate a subscription
// (POST /api/v1/orders/subscriptions/{id}/activate)
//
// A draft is activated (O42) when its customer may take it
// (customer_disabled), it has a product line (subscription_empty), every
// product line is priced (line_unpriced) with a VAT code
// (vat_code_unmapped), and every catalog line is still a service
// (subscription_goods_not_allowed). The customer and the catalog are read
// before the transaction.
func (s *server) ActivateSubscription(ctx context.Context, req gen.ActivateSubscriptionRequestObject) (gen.ActivateSubscriptionResponseObject, error) {
	resp := lifecycleAnswers(
		func(p gen.OrdersProblem) gen.ActivateSubscriptionResponseObject {
			return gen.ActivateSubscription400ApplicationProblemPlusJSONResponse(p)
		},
		func() gen.ActivateSubscriptionResponseObject { return gen.ActivateSubscription404Response{} },
		func(p gen.OrdersProblem) gen.ActivateSubscriptionResponseObject {
			return gen.ActivateSubscription409ApplicationProblemPlusJSONResponse(p)
		})
	if f := s.unknownProperties(ctx, versionInputSchema); f.failed() {
		return gen.ActivateSubscription400ApplicationProblemPlusJSONResponse(invalid(invalidSubscriptionTitle, f)), nil
	}
	q := store.New(s.deps.Pool)
	cur, err := q.SubscriptionByID(ctx, req.Id)
	if noRows(err) {
		return gen.ActivateSubscription404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("orders: read subscription %d: %w", req.Id, err)
	}
	customer, err := s.billingProfile(ctx, cur.CustomerID)
	if err != nil {
		return answer(err, resp)
	}
	lines, err := linesOf(ctx, q, ownerSubscription, cur.ID)
	if err != nil {
		return nil, err
	}
	variants, err := s.catalogVariants(ctx, variantIDsOf(pricedLinesOf(lines)))
	if err != nil {
		return answer(err, resp)
	}
	out, err := s.step(ctx, req.Id, req.Body.Version, req.Body.ExpectedUpdatedAt,
		func(ctx context.Context, tx pgx.Tx, txq *store.Queries, sch schedule, by actor, now time.Time) (store.OrdersSubscription, []changeAction, error) {
			sub := sch.sub
			if sub.Status != subscriptionDraft {
				return sub, nil, conflict(codeSubscriptionNotDraft, "The subscription is not a draft",
					fmt.Sprintf("Subscription %d is %s; only a draft is activated.", sub.Number, sub.Status))
			}
			if err := customerConfirmable(customer); err != nil {
				return sub, nil, err
			}
			products := 0
			for _, l := range sch.lines {
				if l.Kind == kindProduct {
					products++
				}
			}
			if products == 0 {
				return sub, nil, conflict(codeSubscriptionEmpty, "The subscription has no lines",
					fmt.Sprintf("Subscription %d has no product line to bill.", sub.Number))
			}
			failures := sendable(sch.lines)
			failures = append(failures, judgeServices(pricedLinesOf(sch.lines), variants)...)
			if len(failures) > 0 {
				return sub, nil, refuse(linesProblem(invalidSubscriptionTitle, failures))
			}
			st := stepState(sub, now)
			st.Status, st.ActivatedAt, st.ActivatedByUserID, st.ActivatedByDisplay = subscriptionActive, &now, by.id, by.display
			row, err := txq.SetSubscriptionState(ctx, st)
			if err != nil {
				return sub, nil, fmt.Errorf("orders: activate subscription %d: %w", sub.ID, err)
			}
			if err := events.Publish(ctx, tx, moduleName, contracts.SubscriptionStarted{
				SubscriptionID: row.ID, Number: row.Number, Version: row.Version, CustomerID: row.CustomerID, Currency: row.Currency,
				Interval: row.BillingInterval, StartDate: row.StartDate.Time.Format(time.DateOnly), StartedAt: now,
			}, now); err != nil {
				return sub, nil, err
			}
			return row, []changeAction{{Kind: actionActivated}}, nil
		})
	if err != nil {
		return answer(err, resp)
	}
	return gen.ActivateSubscription200JSONResponse(out), nil
}

// PauseSubscription Pause a subscription
// (POST /api/v1/orders/subscriptions/{id}/pause)
//
// Billing stops from a period start (O44): the one asked, else the first
// that starts after today and has no live allocation in it or after it. The
// periods before it still bill; a period from it on with a live allocation
// refuses (change_in_billed_period).
func (s *server) PauseSubscription(ctx context.Context, req gen.PauseSubscriptionRequestObject) (gen.PauseSubscriptionResponseObject, error) {
	resp := lifecycleAnswers(
		func(p gen.OrdersProblem) gen.PauseSubscriptionResponseObject {
			return gen.PauseSubscription400ApplicationProblemPlusJSONResponse(p)
		},
		func() gen.PauseSubscriptionResponseObject { return gen.PauseSubscription404Response{} },
		func(p gen.OrdersProblem) gen.PauseSubscriptionResponseObject {
			return gen.PauseSubscription409ApplicationProblemPlusJSONResponse(p)
		})
	if f := s.unknownProperties(ctx, pauseInputSchema); f.failed() {
		return gen.PauseSubscription400ApplicationProblemPlusJSONResponse(invalid(invalidSubscriptionTitle, f)), nil
	}
	today, err := s.todayOf(ctx)
	if err != nil {
		return answer(err, resp)
	}
	body := *req.Body
	out, err := s.step(ctx, req.Id, body.Version, body.ExpectedUpdatedAt,
		func(ctx context.Context, tx pgx.Tx, txq *store.Queries, sch schedule, by actor, now time.Time) (store.OrdersSubscription, []changeAction, error) {
			sub := sch.sub
			if sub.Status != subscriptionActive {
				return sub, nil, conflict(codeSubscriptionNotActive, "The subscription is not active",
					fmt.Sprintf("Subscription %d is %s; only an active subscription is paused.", sub.Number, sub.Status))
			}
			from := defaultPause(sch, today)
			if body.From != nil {
				from = civil(body.From.Time)
			}
			if err := judgeEffectiveFrom(sch, from); err != nil {
				return sub, nil, pauseField(err)
			}
			st := stepState(sub, now)
			st.Status, st.PausedFrom = subscriptionPaused, pgDate(&from)
			row, err := txq.SetSubscriptionState(ctx, st)
			if err != nil {
				return sub, nil, fmt.Errorf("orders: pause subscription %d: %w", sub.ID, err)
			}
			if err := publishSubscriptionChanged(ctx, tx, row, now); err != nil {
				return sub, nil, err
			}
			return row, []changeAction{{Kind: actionPaused, To: ptr(from.Format(time.DateOnly))}}, nil
		})
	if err != nil {
		return answer(err, resp)
	}
	return gen.PauseSubscription200JSONResponse(out), nil
}

// pauseField renames judgeEffectiveFrom's field refusal to the pause's and
// resume's own field, from.
func pauseField(err error) error {
	if status, p, ok := answerOf(err); ok && status == http.StatusBadRequest && p.Errors != nil {
		if msgs, ok := (*p.Errors)["effectiveFrom"]; ok {
			return refuseField(invalidSubscriptionTitle, "from", codeNotAPeriodStart, msgs[0])
		}
	}
	return err
}

// defaultPause is the period start a pause takes when none is asked: the
// first period starting after today, or the first without a live allocation
// when that is later.
func defaultPause(sch schedule, today time.Time) time.Time {
	from := sch.cal.start
	if p, ok := sch.cal.periodOn(today); ok {
		from = sch.cal.period(p.index + 1).start
	}
	if open := sch.firstOpenStart(); open.After(from) {
		from = open
	}
	return from
}

// publishSubscriptionChanged publishes orders.subscription.changed.v1 for row at now.
func publishSubscriptionChanged(ctx context.Context, tx pgx.Tx, row store.OrdersSubscription, now time.Time) error {
	return events.Publish(ctx, tx, moduleName, contracts.SubscriptionChanged{
		SubscriptionID: row.ID, Number: row.Number, Version: row.Version, CustomerID: row.CustomerID, Status: row.Status, ChangedAt: now,
	}, now)
}

// skippedStarts is the period starts of sch from from up to, not including,
// until — the periods a pause skips.
func skippedStarts(sch schedule, from, until time.Time) []time.Time {
	var out []time.Time
	for _, p := range sch.cal.periodsThrough(until) {
		if !p.start.Before(from) && p.start.Before(until) {
			out = append(out, p.start)
		}
	}
	return out
}

// ResumeSubscription Resume a subscription
// (POST /api/v1/orders/subscriptions/{id}/resume)
//
// Billing restarts from a period start on or after the pause (O44); every
// period from the pause to it is recorded as skipped and never billed.
func (s *server) ResumeSubscription(ctx context.Context, req gen.ResumeSubscriptionRequestObject) (gen.ResumeSubscriptionResponseObject, error) {
	resp := lifecycleAnswers(
		func(p gen.OrdersProblem) gen.ResumeSubscriptionResponseObject {
			return gen.ResumeSubscription400ApplicationProblemPlusJSONResponse(p)
		},
		func() gen.ResumeSubscriptionResponseObject { return gen.ResumeSubscription404Response{} },
		func(p gen.OrdersProblem) gen.ResumeSubscriptionResponseObject {
			return gen.ResumeSubscription409ApplicationProblemPlusJSONResponse(p)
		})
	if f := s.unknownProperties(ctx, resumeInputSchema); f.failed() {
		return gen.ResumeSubscription400ApplicationProblemPlusJSONResponse(invalid(invalidSubscriptionTitle, f)), nil
	}
	body := *req.Body
	from := civil(body.From.Time)
	out, err := s.step(ctx, req.Id, body.Version, body.ExpectedUpdatedAt,
		func(ctx context.Context, tx pgx.Tx, txq *store.Queries, sch schedule, by actor, now time.Time) (store.OrdersSubscription, []changeAction, error) {
			sub := sch.sub
			if sub.Status != subscriptionPaused || !sub.PausedFrom.Valid {
				return sub, nil, conflict(codeSubscriptionNotPaused, "The subscription is not paused",
					fmt.Sprintf("Subscription %d is %s; only a paused subscription is resumed.", sub.Number, sub.Status))
			}
			paused := civil(sub.PausedFrom.Time)
			switch {
			case !sch.cal.isPeriodStart(from):
				return sub, nil, refuseField(invalidSubscriptionTitle, "from", codeNotAPeriodStart,
					fmt.Sprintf("%s is not the first day of one of the subscription's periods", from.Format(time.DateOnly)))
			case from.Before(paused):
				return sub, nil, refuseField(invalidSubscriptionTitle, "from", "",
					fmt.Sprintf("Billing resumes on or after the pause, %s", paused.Format(time.DateOnly)))
			}
			if skipped := skippedStarts(sch, paused, from); len(skipped) > 0 {
				dates := make([]pgtype.Date, 0, len(skipped))
				for _, d := range skipped {
					dates = append(dates, pgDate(&d))
				}
				if err := txq.InsertSubscriptionSkips(ctx, store.InsertSubscriptionSkipsParams{SubscriptionID: sub.ID, PeriodStarts: dates}); err != nil {
					return sub, nil, fmt.Errorf("orders: skip subscription %d's paused periods: %w", sub.ID, err)
				}
			}
			st := stepState(sub, now)
			st.Status, st.PausedFrom = subscriptionActive, pgDate(nil)
			row, err := txq.SetSubscriptionState(ctx, st)
			if err != nil {
				return sub, nil, fmt.Errorf("orders: resume subscription %d: %w", sub.ID, err)
			}
			if err := refreshBilledThrough(ctx, txq, row, now); err != nil {
				return sub, nil, err
			}
			if row, err = txq.SubscriptionByID(ctx, row.ID); err != nil {
				return sub, nil, fmt.Errorf("orders: read subscription %d: %w", sub.ID, err)
			}
			if err := publishSubscriptionChanged(ctx, tx, row, now); err != nil {
				return sub, nil, err
			}
			return row, []changeAction{{Kind: actionResumed, From: ptr(paused.Format(time.DateOnly)), To: ptr(from.Format(time.DateOnly))}}, nil
		})
	if err != nil {
		return answer(err, resp)
	}
	return gen.ResumeSubscription200JSONResponse(out), nil
}

// EndSubscription End a subscription
// (POST /api/v1/orders/subscriptions/{id}/end)
//
// The end date is set (O44): the period it falls in is the last one billed,
// in full or pro-rated to the end as the final period asked — the
// subscription's own when none is — says (owner, 2026-11-06), and the choice
// is stored and logged. No period with a live allocation may start after the
// end (end_before_billed), and a final period with a live allocation is not
// pro-rated (final_period_billed): the user credits first. A paused
// subscription ends with its paused periods up to the end skipped.
func (s *server) EndSubscription(ctx context.Context, req gen.EndSubscriptionRequestObject) (gen.EndSubscriptionResponseObject, error) {
	resp := lifecycleAnswers(
		func(p gen.OrdersProblem) gen.EndSubscriptionResponseObject {
			return gen.EndSubscription400ApplicationProblemPlusJSONResponse(p)
		},
		func() gen.EndSubscriptionResponseObject { return gen.EndSubscription404Response{} },
		func(p gen.OrdersProblem) gen.EndSubscriptionResponseObject {
			return gen.EndSubscription409ApplicationProblemPlusJSONResponse(p)
		})
	if f := s.unknownProperties(ctx, endInputSchema); f.failed() {
		return gen.EndSubscription400ApplicationProblemPlusJSONResponse(invalid(invalidSubscriptionTitle, f)), nil
	}
	body := *req.Body
	end := civil(body.EndDate.Time)
	out, err := s.step(ctx, req.Id, body.Version, body.ExpectedUpdatedAt,
		func(ctx context.Context, tx pgx.Tx, txq *store.Queries, sch schedule, by actor, now time.Time) (store.OrdersSubscription, []changeAction, error) {
			sub := sch.sub
			switch sub.Status {
			case subscriptionActive, subscriptionPaused, subscriptionEnded:
			default:
				return sub, nil, conflict(codeSubscriptionNotActive, "The subscription is not active",
					fmt.Sprintf("Subscription %d is %s; only an active, paused or ended subscription is ended.", sub.Number, sub.Status))
			}
			if end.Before(civil(sub.StartDate.Time)) {
				return sub, nil, refuseField(invalidSubscriptionTitle, "endDate", "", "The end is on or after the start")
			}
			final := sub.FinalPeriod
			if body.FinalPeriod != nil {
				if !body.FinalPeriod.Valid() {
					return sub, nil, refuseField(invalidSubscriptionTitle, "finalPeriod", "", "The final period is full or prorated")
				}
				final = string(*body.FinalPeriod)
			}
			if sch.hasLive && sch.maxLive.After(end) {
				return sub, nil, conflict(codeEndBeforeBilled, "The subscription is billed past the end",
					fmt.Sprintf("The period from %s is on an invoice or a draft invoice; end on or after its first day, or credit it first.", sch.maxLive.Format(time.DateOnly)))
			}
			if err := judgeFinalPeriod(sch, end, final); err != nil {
				return sub, nil, err
			}
			if sub.Status == subscriptionPaused && sub.PausedFrom.Valid {
				until := end.AddDate(0, 0, 1)
				if p, ok := sch.cal.periodOn(end); ok {
					until = p.end.AddDate(0, 0, 1)
				}
				if skipped := skippedStarts(sch, civil(sub.PausedFrom.Time), until); len(skipped) > 0 {
					dates := make([]pgtype.Date, 0, len(skipped))
					for _, d := range skipped {
						dates = append(dates, pgDate(&d))
					}
					if err := txq.InsertSubscriptionSkips(ctx, store.InsertSubscriptionSkipsParams{SubscriptionID: sub.ID, PeriodStarts: dates}); err != nil {
						return sub, nil, fmt.Errorf("orders: skip subscription %d's paused periods: %w", sub.ID, err)
					}
				}
			}
			st := stepState(sub, now)
			st.Status, st.PausedFrom, st.EndDate, st.EndedAt, st.FinalPeriod = subscriptionEnded, pgDate(nil), pgDate(&end), &now, final
			row, err := txq.SetSubscriptionState(ctx, st)
			if err != nil {
				return sub, nil, fmt.Errorf("orders: end subscription %d: %w", sub.ID, err)
			}
			if sub.Status == subscriptionPaused {
				// The skipped periods count as covered: billed through may run on.
				if err := refreshBilledThrough(ctx, txq, row, now); err != nil {
					return sub, nil, err
				}
				if row, err = txq.SubscriptionByID(ctx, row.ID); err != nil {
					return sub, nil, fmt.Errorf("orders: read subscription %d: %w", sub.ID, err)
				}
			}
			if err := events.Publish(ctx, tx, moduleName, contracts.SubscriptionEnded{
				SubscriptionID: row.ID, Number: row.Number, Version: row.Version, CustomerID: row.CustomerID, Status: row.Status,
				EndDate: end.Format(time.DateOnly), EndedAt: now,
			}, now); err != nil {
				return sub, nil, err
			}
			return row, []changeAction{
				{Kind: actionEnded, From: dateText(sub.EndDate), To: ptr(end.Format(time.DateOnly))},
				{Kind: actionFinalPeriod, From: ptr(sub.FinalPeriod), To: ptr(final)},
			}, nil
		})
	if err != nil {
		return answer(err, resp)
	}
	return gen.EndSubscription200JSONResponse(out), nil
}

// judgeFinalPeriod refuses to pro-rate a final period that is billed
// (final_period_billed, owner 2026-11-06): with the subscription ending on
// end, the period end falls in, when end is not its last day, may not have a
// live allocation while final is prorated — the user credits it first. A
// full final period is always allowed.
func judgeFinalPeriod(sch schedule, end time.Time, final string) error {
	if final != finalPeriodProrated {
		return nil
	}
	cal := sch.cal
	cal.end, cal.prorateEnd = &end, true
	p, ok := cal.periodOn(end)
	if !ok || p.end.Equal(p.fullEnd) || !sch.livePeriod(p.start) {
		return nil
	}
	return conflict(codeFinalPeriodBilled, "The final period is billed",
		fmt.Sprintf("The period from %s is on an invoice or a draft invoice and cannot be pro-rated to %s; bill it in full, or credit it first.",
			p.start.Format(time.DateOnly), end.Format(time.DateOnly)))
}

// dateText is a date column as a change log value, "" for NULL.
func dateText(d pgtype.Date) *string {
	if !d.Valid {
		return ptr("")
	}
	return ptr(d.Time.Format(time.DateOnly))
}

// CancelSubscription Cancel a subscription
// (POST /api/v1/orders/subscriptions/{id}/cancel)
//
// A draft is cancelled, and an active or paused subscription with nothing
// billed — no live allocation (subscription_billed); an activated one's
// cancel publishes orders.subscription.ended.v1.
func (s *server) CancelSubscription(ctx context.Context, req gen.CancelSubscriptionRequestObject) (gen.CancelSubscriptionResponseObject, error) {
	resp := lifecycleAnswers(
		nil,
		func() gen.CancelSubscriptionResponseObject { return gen.CancelSubscription404Response{} },
		func(p gen.OrdersProblem) gen.CancelSubscriptionResponseObject {
			return gen.CancelSubscription409ApplicationProblemPlusJSONResponse(p)
		})
	out, err := s.step(ctx, req.Id, req.Body.Version, req.Body.ExpectedUpdatedAt,
		func(ctx context.Context, tx pgx.Tx, txq *store.Queries, sch schedule, by actor, now time.Time) (store.OrdersSubscription, []changeAction, error) {
			sub := sch.sub
			switch sub.Status {
			case subscriptionDraft:
			case subscriptionActive, subscriptionPaused:
				if sch.hasLive {
					return sub, nil, conflict(codeSubscriptionBilled, "The subscription is billed",
						fmt.Sprintf("Subscription %d has periods on invoices or draft invoices; end it instead.", sub.Number))
				}
			default:
				return sub, nil, conflict(codeSubscriptionNotCancellable, "The subscription cannot be cancelled",
					fmt.Sprintf("Subscription %d is %s.", sub.Number, sub.Status))
			}
			st := stepState(sub, now)
			st.Status, st.PausedFrom, st.CancelledAt = subscriptionCancelled, pgDate(nil), &now
			row, err := txq.SetSubscriptionState(ctx, st)
			if err != nil {
				return sub, nil, fmt.Errorf("orders: cancel subscription %d: %w", sub.ID, err)
			}
			if sub.Status != subscriptionDraft {
				if err := events.Publish(ctx, tx, moduleName, contracts.SubscriptionEnded{
					SubscriptionID: row.ID, Number: row.Number, Version: row.Version, CustomerID: row.CustomerID, Status: row.Status, EndedAt: now,
				}, now); err != nil {
					return sub, nil, err
				}
			}
			return row, []changeAction{{Kind: actionCancelled}}, nil
		})
	if err != nil {
		return answer(err, resp)
	}
	return gen.CancelSubscription200JSONResponse(out), nil
}
