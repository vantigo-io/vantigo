package orders

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/orders/store"
)

// ordersSettings is the one settings row as the module reads it (§9), the
// defaults when it was never saved: series starting at 1, a quote valid for
// 30 days, 14 days to pay, no channel to send from, a subscription's final
// period billed in full.
type ordersSettings struct {
	quoteSeriesStart, orderSeriesStart int64
	quoteValidityDays                  int32
	defaultPaymentTermsDays            int32
	sendChannel                        *uuid.UUID
	confirmationOnConfirm              bool
	combineByDefault                   bool
	mailTexts                          []mailTextOverride
	// subscriptionFinalPeriodDefault is the final period a new subscription
	// takes (owner, 2026-11-06): full or prorated.
	subscriptionFinalPeriodDefault string
	// defaultSiteID is the Inventory site a new order is fulfilled from
	// (O46), nil for none.
	defaultSiteID *int64
	// freightVariantID is the freight product a shipping line takes (O53),
	// nil for none; freightMarkup the percent added to a carrier's estimate;
	// shippingNoticeOnBook whether booking queues the shipping notice (O52).
	freightVariantID     *int32
	freightMarkup        *big.Rat
	shippingNoticeOnBook bool
	revision             int32 // 0 while never saved
}

// mailTextOverride is one document kind's subject and body in one language,
// overriding the built-in mail text (O25, mailtext.go).
type mailTextOverride struct {
	Kind     string `json:"kind"`
	Language string `json:"language"`
	Subject  string `json:"subject"`
	Body     string `json:"body"`
}

// defaultSettings are the settings before the first save.
func defaultSettings() ordersSettings {
	return ordersSettings{quoteSeriesStart: 1, orderSeriesStart: 1, quoteValidityDays: 30, defaultPaymentTermsDays: 14,
		subscriptionFinalPeriodDefault: finalPeriodFull, freightMarkup: new(big.Rat), shippingNoticeOnBook: true}
}

// settingsOf is a stored row as the module reads it.
func settingsOf(row store.OrdersSetting) (ordersSettings, error) {
	out := ordersSettings{
		quoteSeriesStart: row.QuoteSeriesStart, orderSeriesStart: row.OrderSeriesStart,
		quoteValidityDays: row.QuoteValidityDays, defaultPaymentTermsDays: row.DefaultPaymentTermsDays,
		sendChannel: row.SendChannel, confirmationOnConfirm: row.ConfirmationOnConfirm,
		combineByDefault: row.CombineByDefault, subscriptionFinalPeriodDefault: row.SubscriptionFinalPeriodDefault,
		defaultSiteID: row.DefaultSiteID, freightVariantID: row.FreightVariantID, freightMarkup: ratOrZero(row.FreightMarkupPercent),
		shippingNoticeOnBook: row.ShippingNoticeOnBook, revision: row.Revision,
	}
	if len(row.MailTexts) > 0 {
		var texts []mailTextOverride
		if err := json.Unmarshal(row.MailTexts, &texts); err == nil {
			out.mailTexts = texts
		}
	}
	return out, nil
}

// readSettings reads the settings on q — the pool or a transaction's — the
// defaults when none were ever saved.
func readSettings(ctx context.Context, q *store.Queries) (ordersSettings, error) {
	row, err := q.GetSettings(ctx)
	if noRows(err) {
		return defaultSettings(), nil
	}
	if err != nil {
		return ordersSettings{}, fmt.Errorf("orders: read the settings: %w", err)
	}
	return settingsOf(row)
}

// mailText is the override for a kind and a language, nil for none.
func (s ordersSettings) mailText(kind, language string) *mailTextOverride {
	for i := range s.mailTexts {
		if s.mailTexts[i].Kind == kind && s.mailTexts[i].Language == language {
			return &s.mailTexts[i]
		}
	}
	return nil
}

// allocateNumber takes the next number of a series on the caller's
// transaction (O12): gapless while nothing rolls back, and never below the
// series' start in the settings.
func allocateNumber(ctx context.Context, txq *store.Queries, series string, start int64) (int64, error) {
	n, err := txq.AllocateNumber(ctx, store.AllocateNumberParams{CounterName: series, SeriesStart: start})
	if err != nil {
		return 0, fmt.Errorf("orders: allocate a %s number: %w", series, err)
	}
	return n, nil
}

// The number series (O12).
const (
	seriesQuote = "quote"
	seriesOrder = "order"
	// seriesDelivery numbers the deliveries (O47); it starts at 1.
	seriesDelivery = "delivery"
)
