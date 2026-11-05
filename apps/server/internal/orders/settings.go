package orders

// The settings operations (orders design §9, O12, O25): the number series'
// starts, the default validity and payment terms, the channel to send from,
// whether a confirmation is sent on confirm, the invoicing default, the mail
// texts, the final period a new subscription takes and the default site a new
// order is fulfilled from (O46).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
	"github.com/vantigo-io/vantigo/server/internal/orders/store"
)

// nextNumber is the number a series hands out next: its counter's, never
// below the series' start.
func nextNumber(ctx context.Context, q *store.Queries, series string, start int64) (int64, bool, error) {
	next, err := q.CounterNextValue(ctx, series)
	if noRows(err) {
		return start, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("orders: read the %s series: %w", series, err)
	}
	return max(next, start), true, nil
}

// settingsView is the settings as the API answers them.
func (s *server) settingsView(ctx context.Context, q *store.Queries, st ordersSettings) (gen.OrdersSettings, error) {
	nextQuote, _, err := nextNumber(ctx, q, seriesQuote, st.quoteSeriesStart)
	if err != nil {
		return gen.OrdersSettings{}, err
	}
	nextOrder, _, err := nextNumber(ctx, q, seriesOrder, st.orderSeriesStart)
	if err != nil {
		return gen.OrdersSettings{}, err
	}
	out := gen.OrdersSettings{
		QuoteSeriesStart: st.quoteSeriesStart, OrderSeriesStart: st.orderSeriesStart, NextQuoteNumber: nextQuote,
		NextOrderNumber: nextOrder, QuoteValidityDays: st.quoteValidityDays, DefaultPaymentTermsDays: st.defaultPaymentTermsDays,
		ConfirmationOnConfirm: st.confirmationOnConfirm, CombineByDefault: st.combineByDefault,
		MailTexts: []gen.OrdersMailText{}, SendingAvailable: s.deps.OutboundMail != nil, Revision: st.revision,
		SendChannelId: st.sendChannel, SendChannels: []gen.OrdersSendChannel{}, SendChannelState: gen.Disabled,
		SubscriptionFinalPeriodDefault: gen.OrdersFinalPeriod(st.subscriptionFinalPeriodDefault),
		DefaultSiteId:                  st.defaultSiteID,
		FreightVariantId:               st.freightVariantID,
		FreightMarkupPercent:           wire(orZero(st.freightMarkup), 2),
		ShippingNoticeOnBook:           st.shippingNoticeOnBook,
	}
	for _, t := range st.mailTexts {
		out.MailTexts = append(out.MailTexts, gen.OrdersMailText{
			Kind: gen.OrdersMailTextKind(t.Kind), Language: gen.OrdersMailTextLanguage(t.Language), Subject: t.Subject, Body: t.Body,
		})
	}
	return out, nil
}

// GetOrdersSettings Get the Orders settings
// (GET /api/v1/orders/settings)
func (s *server) GetOrdersSettings(ctx context.Context, _ gen.GetOrdersSettingsRequestObject) (gen.GetOrdersSettingsResponseObject, error) {
	var out gen.OrdersSettings
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		st, err := readSettings(ctx, q)
		if err != nil {
			return err
		}
		out, err = s.settingsView(ctx, q, st)
		return err
	})
	if err != nil {
		return nil, err
	}
	if err := s.sendChannelsView(ctx, &out); err != nil {
		return nil, err
	}
	return gen.GetOrdersSettings200JSONResponse(out), nil
}

// PutOrdersSettings Save the Orders settings
// (PUT /api/v1/orders/settings)
//
// The send channel is judged through Communications before the transaction
// (send_channel_unknown); a series start may only rise above the highest
// number the series has used (series_start_too_low), judged under the
// settings' lock.
func (s *server) PutOrdersSettings(ctx context.Context, req gen.PutOrdersSettingsRequestObject) (gen.PutOrdersSettingsResponseObject, error) {
	resp := answers[gen.PutOrdersSettingsResponseObject]{
		badRequest: func(p gen.OrdersProblem) gen.PutOrdersSettingsResponseObject {
			return gen.PutOrdersSettings400ApplicationProblemPlusJSONResponse(p)
		},
		conflict: func(p gen.OrdersProblem) gen.PutOrdersSettingsResponseObject {
			return gen.PutOrdersSettings409ApplicationProblemPlusJSONResponse(p)
		},
	}
	f := s.unknownProperties(ctx, settingsInputSchema)
	b := *req.Body
	if b.QuoteSeriesStart < 1 {
		f.add("quoteSeriesStart", "A series starts at 1 or more")
	}
	if b.OrderSeriesStart < 1 {
		f.add("orderSeriesStart", "A series starts at 1 or more")
	}
	if b.QuoteValidityDays < 1 || b.QuoteValidityDays > 365 {
		f.add("quoteValidityDays", "A validity is 1 to 365 days")
	}
	if b.DefaultPaymentTermsDays < 0 || b.DefaultPaymentTermsDays > 365 {
		f.add("defaultPaymentTermsDays", "Payment terms are 0 to 365 days")
	}
	if b.SubscriptionFinalPeriodDefault != nil && !b.SubscriptionFinalPeriodDefault.Valid() {
		f.add("subscriptionFinalPeriodDefault", "The final period is full or prorated")
	}
	texts := make([]mailTextOverride, 0, len(b.MailTexts))
	seen := map[string]bool{}
	for i, t := range b.MailTexts {
		key := string(t.Kind) + "/" + string(t.Language)
		switch {
		case !t.Kind.Valid() || !t.Language.Valid():
			f.add(fmt.Sprintf("mailTexts[%d]", i), "A mail text is for a quote, a confirmation or a shipping notice, in nb or en")
		case seen[key]:
			f.add(fmt.Sprintf("mailTexts[%d]", i), "Each kind and language has one mail text")
		case utf8.RuneCountInString(t.Subject) > 300 || utf8.RuneCountInString(t.Body) > 4000:
			f.add(fmt.Sprintf("mailTexts[%d]", i), "A subject is at most 300 characters and a body 4000")
		}
		seen[key] = true
		if strings.TrimSpace(t.Subject) == "" && strings.TrimSpace(t.Body) == "" {
			continue
		}
		texts = append(texts, mailTextOverride{Kind: string(t.Kind), Language: string(t.Language), Subject: t.Subject, Body: t.Body})
	}
	var channel *uuid.UUID
	if b.SendChannelId != nil {
		id := *b.SendChannelId
		ch, err := s.outboundChannel(ctx, id)
		switch {
		case errors.Is(err, contracts.ErrChannelUnknown) || errors.Is(err, errDependencyUnavailable):
			f.addCoded("sendChannelId", codeSendChannelUnknown, "There is no such channel to send from")
		case err != nil:
			return nil, fmt.Errorf("orders: read channel %s: %w", id, err)
		default:
			channel = &ch.ID
		}
	}
	if b.DefaultSiteId != nil {
		if err := s.judgeSite(ctx, &f, "defaultSiteId", *b.DefaultSiteId); err != nil {
			return nil, err
		}
	}
	if m := b.FreightMarkupPercent; m != nil && (!finite(*m) || *m < 0 || *m > 500 || decimalPlaces(*m) > 2) {
		f.add("freightMarkupPercent", "A markup is 0 to 500 percent, with at most two decimals")
	}
	if b.FreightVariantId != nil {
		variants, err := s.catalogVariants(ctx, []int32{*b.FreightVariantId})
		if err != nil {
			return nil, err
		}
		if v, ok := variants[*b.FreightVariantId]; !ok || v.ProductType != productTypeService {
			f.addCoded("freightVariantId", codeFreightProductInvalid, "The freight product is a variant of a Service product")
		}
	}
	if f.failed() {
		return gen.PutOrdersSettings400ApplicationProblemPlusJSONResponse(invalid(invalidSettingsTitle, f)), nil
	}
	mailTexts, err := json.Marshal(texts)
	if err != nil {
		return nil, fmt.Errorf("orders: encode the mail texts: %w", err)
	}

	var out gen.OrdersSettings
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		cur := defaultSettings()
		row, err := txq.LockSettings(ctx)
		switch {
		case err == nil:
			if cur, err = settingsOf(row); err != nil {
				return err
			}
		case !noRows(err):
			return fmt.Errorf("orders: lock the settings: %w", err)
		}
		if cur.revision != b.Revision {
			return versionConflict("settings' revision", cur.revision, b.Revision)
		}
		var sf fieldErrors
		for _, series := range []struct {
			name, field string
			from, to    int64
		}{{seriesQuote, "quoteSeriesStart", cur.quoteSeriesStart, b.QuoteSeriesStart}, {seriesOrder, "orderSeriesStart", cur.orderSeriesStart, b.OrderSeriesStart}} {
			if series.to == series.from {
				continue
			}
			next, used, err := nextNumber(ctx, txq, series.name, series.from)
			if err != nil {
				return err
			}
			if used && series.to < next {
				sf.addCoded(series.field, codeSeriesStartTooLow,
					fmt.Sprintf("The %s series has used numbers up to %d; it can only start above that", series.name, next-1))
			}
		}
		if sf.failed() {
			return refuseInvalid(invalidSettingsTitle, sf)
		}
		finalPeriod := cur.subscriptionFinalPeriodDefault
		if b.SubscriptionFinalPeriodDefault != nil {
			finalPeriod = string(*b.SubscriptionFinalPeriodDefault)
		}
		// The shipping settings (O52, O53): absent keeps them; the freight
		// product is kept only when the body leaves the key out.
		freight, markup, notice := cur.freightVariantID, orZero(cur.freightMarkup), cur.shippingNoticeOnBook
		if hasProperty(ctx, "freightVariantId") {
			freight = b.FreightVariantId
		}
		if b.FreightMarkupPercent != nil {
			markup = ratFromFloat(*b.FreightMarkupPercent)
		}
		if b.ShippingNoticeOnBook != nil {
			notice = *b.ShippingNoticeOnBook
		}
		saved, err := txq.UpsertSettings(ctx, store.UpsertSettingsParams{
			QuoteSeriesStart: b.QuoteSeriesStart, OrderSeriesStart: b.OrderSeriesStart, QuoteValidityDays: b.QuoteValidityDays,
			DefaultPaymentTermsDays: b.DefaultPaymentTermsDays, SendChannel: channel,
			ConfirmationOnConfirm: b.ConfirmationOnConfirm, CombineByDefault: b.CombineByDefault, MailTexts: mailTexts,
			SubscriptionFinalPeriodDefault: finalPeriod, DefaultSiteID: b.DefaultSiteId, FreightVariantID: freight,
			FreightMarkupPercent: numeric(markup, 2), ShippingNoticeOnBook: notice, UpdatedAt: s.deps.Clock(),
		})
		if err != nil {
			return fmt.Errorf("orders: save the settings: %w", err)
		}
		st, err := settingsOf(saved)
		if err != nil {
			return err
		}
		out, err = s.settingsView(ctx, txq, st)
		return err
	})
	if err != nil {
		return answer(err, resp)
	}
	if err := s.sendChannelsView(ctx, &out); err != nil {
		return nil, err
	}
	return gen.PutOrdersSettings200JSONResponse(out), nil
}
