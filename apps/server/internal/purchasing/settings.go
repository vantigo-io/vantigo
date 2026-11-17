package purchasing

// The settings (P9): the order series' start, the default receiving site, the
// mail channel orders are sent from (judged through Communications before
// the transaction, as Orders' is), the text printed under an order, the
// delivery address per site when it differs from the company's, and the price
// tolerance of P38. One row, created by the first save; until then the
// defaults below answer, at revision 0.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/purchasing/gen"
	"github.com/vantigo-io/vantigo/server/internal/purchasing/store"
)

// seriesOrder is the order series' counter (P20).
const seriesOrder = "order"

// baseCurrency is the books' currency.
const baseCurrency = "NOK"

// purchasingSettings is the settings row as the module reads it.
type purchasingSettings struct {
	orderSeriesStart  int64
	defaultSiteID     *int64
	sendChannel       *uuid.UUID
	orderFooter       string
	deliveryAddresses []siteAddress
	priceTolerance    *big.Rat
	revision          int32
	updatedAt         *time.Time
}

// siteAddress is a site's delivery address as the settings keep it, a JSON
// element of delivery_addresses.
type siteAddress struct {
	SiteID     int64  `json:"siteId"`
	Name       string `json:"name,omitempty"`
	Line1      string `json:"line1,omitempty"`
	Line2      string `json:"line2,omitempty"`
	PostalCode string `json:"postalCode,omitempty"`
	City       string `json:"city,omitempty"`
	Country    string `json:"country,omitempty"`
}

// defaultSettings are the settings before the first save.
func defaultSettings() purchasingSettings {
	return purchasingSettings{orderSeriesStart: 1, priceTolerance: big.NewRat(2, 1)}
}

// settingsOf reads a settings row.
func settingsOf(row store.PurchasingSetting) (purchasingSettings, error) {
	out := purchasingSettings{
		orderSeriesStart: row.OrderSeriesStart, defaultSiteID: row.DefaultSiteID, sendChannel: row.SendChannel,
		orderFooter: row.OrderFooter, priceTolerance: ratOrZero(row.PriceTolerancePercent), revision: row.Revision,
		updatedAt: &row.UpdatedAt,
	}
	if len(row.DeliveryAddresses) > 0 {
		if err := json.Unmarshal(row.DeliveryAddresses, &out.deliveryAddresses); err != nil {
			return out, fmt.Errorf("purchasing: read the delivery addresses: %w", err)
		}
	}
	return out, nil
}

// readSettings is the settings, the defaults when none is saved.
func readSettings(ctx context.Context, q *store.Queries) (purchasingSettings, error) {
	row, err := q.GetSettings(ctx)
	if noRows(err) {
		return defaultSettings(), nil
	}
	if err != nil {
		return purchasingSettings{}, fmt.Errorf("purchasing: read the settings: %w", err)
	}
	return settingsOf(row)
}

// addressFor is the settings' delivery address of a site, nil when it has
// none.
func (st purchasingSettings) addressFor(siteID int64) *siteAddress {
	for _, a := range st.deliveryAddresses {
		if a.SiteID == siteID {
			return &a
		}
	}
	return nil
}

// nextNumber is the number the series hands out next: its counter's, never
// below the series' start; used says whether it has handed one out.
func nextNumber(ctx context.Context, q *store.Queries, start int64) (int64, bool, error) {
	next, err := q.CounterNextValue(ctx, seriesOrder)
	if noRows(err) {
		return start, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("purchasing: read the order series: %w", err)
	}
	return max(next, start), true, nil
}

// addressIn is a settings address as the API takes it.
func addressIn(siteID int64, a gen.PurchasingAddress) siteAddress {
	return siteAddress{SiteID: siteID, Name: strings.TrimSpace(deref(a.Name)), Line1: strings.TrimSpace(deref(a.Line1)),
		Line2: strings.TrimSpace(deref(a.Line2)), PostalCode: strings.TrimSpace(deref(a.PostalCode)),
		City: strings.TrimSpace(deref(a.City)), Country: strings.ToUpper(strings.TrimSpace(deref(a.Country)))}
}

// addressOut is a settings address as the API answers it.
func addressOut(a siteAddress) gen.PurchasingAddress {
	return gen.PurchasingAddress{Name: textPtr(a.Name), Line1: textPtr(a.Line1), Line2: textPtr(a.Line2),
		PostalCode: textPtr(a.PostalCode), City: textPtr(a.City), Country: textPtr(a.Country)}
}

// settingsView is the settings as the API answers them, without the channels.
func settingsView(ctx context.Context, q *store.Queries, st purchasingSettings) (gen.PurchasingSettings, error) {
	next, _, err := nextNumber(ctx, q, st.orderSeriesStart)
	if err != nil {
		return gen.PurchasingSettings{}, err
	}
	out := gen.PurchasingSettings{
		OrderSeriesStart: st.orderSeriesStart, NextOrderNumber: next, DefaultSiteId: st.defaultSiteID,
		SendChannelId: st.sendChannel, OrderFooter: st.orderFooter, PriceTolerancePercent: trimmed(st.priceTolerance, 2),
		Revision: st.revision, UpdatedAt: st.updatedAt, Channels: []gen.PurchasingSendChannel{},
		DeliveryAddresses: []gen.PurchasingSiteAddress{},
	}
	for _, a := range st.deliveryAddresses {
		out.DeliveryAddresses = append(out.DeliveryAddresses, gen.PurchasingSiteAddress{SiteId: a.SiteID, Address: addressOut(a)})
	}
	return out, nil
}

// channelsView adds Communications' channels and whether the chosen one can
// send now; it asks Communications, so it runs outside any transaction.
func (s *server) channelsView(ctx context.Context, out *gen.PurchasingSettings) error {
	channels, err := s.outboundChannels(ctx)
	if err != nil {
		return fmt.Errorf("purchasing: read the channels: %w", err)
	}
	for _, c := range channels {
		out.Channels = append(out.Channels, gen.PurchasingSendChannel{Id: c.ID, Address: c.Address, DisplayName: c.DisplayName, Available: c.Available})
		if out.SendChannelId != nil && c.ID == *out.SendChannelId && c.Available {
			out.SendingAvailable = true
		}
	}
	return nil
}

// sendingChannel is the channel the settings choose to send from, when
// Communications is on and the channel can send now (P21, MB rule 17); ok is
// false otherwise, which a send answers as sending_disabled.
func (s *server) sendingChannel(ctx context.Context) (uuid.UUID, bool, error) {
	if s.deps.OutboundMail == nil {
		return uuid.Nil, false, nil
	}
	settings, err := readSettings(ctx, store.New(s.deps.Pool))
	if err != nil {
		return uuid.Nil, false, err
	}
	if settings.sendChannel == nil {
		return uuid.Nil, false, nil
	}
	ch, err := s.outboundChannel(ctx, *settings.sendChannel)
	if errors.Is(err, contracts.ErrChannelUnknown) || errors.Is(err, errDependencyUnavailable) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("purchasing: read the send channel: %w", err)
	}
	return ch.ID, ch.Available, nil
}

// GetPurchasingSettings Get the Purchasing settings
// (GET /api/v1/purchasing/settings)
func (s *server) GetPurchasingSettings(ctx context.Context, _ gen.GetPurchasingSettingsRequestObject) (gen.GetPurchasingSettingsResponseObject, error) {
	var out gen.PurchasingSettings
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		st, err := readSettings(ctx, q)
		if err != nil {
			return err
		}
		out, err = settingsView(ctx, q, st)
		return err
	})
	if err != nil {
		return nil, err
	}
	if err := s.channelsView(ctx, &out); err != nil {
		return nil, err
	}
	return gen.GetPurchasingSettings200JSONResponse(out), nil
}

// judgeSite records on f what is wrong with a receiving site: unknown
// (site_not_found) or not active (site_archived).
func judgeSite(f *fieldErrors, field string, sites map[int64]contracts.StockSite, id int64) {
	site, ok := sites[id]
	switch {
	case !ok:
		f.addCoded(field, codeSiteNotFound, "No site of Inventory's has this id")
	case !site.Active:
		f.addCoded(field, codeSiteArchived, "The site is archived")
	}
}

// PutPurchasingSettings Save the Purchasing settings
// (PUT /api/v1/purchasing/settings)
//
// The send channel is judged through Communications and the sites through
// Inventory before the transaction; the series start may only rise above the
// highest number used (series_start_too_low), judged under the settings'
// lock.
func (s *server) PutPurchasingSettings(ctx context.Context, req gen.PutPurchasingSettingsRequestObject) (gen.PutPurchasingSettingsResponseObject, error) {
	resp := answers[gen.PutPurchasingSettingsResponseObject]{
		badRequest: func(p gen.PurchasingProblem) gen.PutPurchasingSettingsResponseObject {
			return gen.PutPurchasingSettings400ApplicationProblemPlusJSONResponse(p)
		},
		conflict: func(p gen.PurchasingProblem) gen.PutPurchasingSettingsResponseObject {
			return gen.PutPurchasingSettings409ApplicationProblemPlusJSONResponse(p)
		},
	}
	b := *req.Body
	var f fieldErrors
	if b.OrderSeriesStart < 1 {
		f.add("orderSeriesStart", "A series starts at 1 or more")
	}
	if utf8.RuneCountInString(b.OrderFooter) > 4000 {
		f.add("orderFooter", "The text under an order is at most 4000 characters")
	}
	tolerance, ok := parseDecimal(b.PriceTolerancePercent, 2, hundred)
	if !ok {
		f.add("priceTolerancePercent", "A tolerance is 0 to 100 percent, with at most two decimals")
	}
	sites, _, err := s.sites(ctx)
	if err != nil {
		return answer(err, resp)
	}
	if b.DefaultSiteId != nil {
		judgeSite(&f, "defaultSiteId", sites, *b.DefaultSiteId)
	}
	if len(b.DeliveryAddresses) > 200 {
		f.add("deliveryAddresses", "At most 200 delivery addresses")
	}
	addresses := make([]siteAddress, 0, len(b.DeliveryAddresses))
	seen := map[int64]bool{}
	for i, a := range b.DeliveryAddresses {
		field := fmt.Sprintf("deliveryAddresses[%d].siteId", i)
		if _, ok := sites[a.SiteId]; !ok {
			f.addCoded(field, codeSiteNotFound, "No site of Inventory's has this id")
			continue
		}
		if seen[a.SiteId] {
			f.add(field, "Each site has one delivery address")
			continue
		}
		seen[a.SiteId] = true
		addresses = append(addresses, addressIn(a.SiteId, a.Address))
	}
	var channel *uuid.UUID
	if b.SendChannelId != nil {
		id := *b.SendChannelId
		ch, err := s.outboundChannel(ctx, id)
		switch {
		case errors.Is(err, contracts.ErrChannelUnknown) || errors.Is(err, errDependencyUnavailable):
			f.addCoded("sendChannelId", codeSendChannelUnknown, "There is no such channel to send from")
		case err != nil:
			return nil, fmt.Errorf("purchasing: read channel %s: %w", id, err)
		default:
			channel = &ch.ID
		}
	}
	if f.failed() {
		return gen.PutPurchasingSettings400ApplicationProblemPlusJSONResponse(invalid(invalidSettingsTitle, f)), nil
	}
	encoded, err := json.Marshal(addresses)
	if err != nil {
		return nil, fmt.Errorf("purchasing: encode the delivery addresses: %w", err)
	}

	var out gen.PurchasingSettings
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		cur := defaultSettings()
		row, err := txq.LockSettings(ctx)
		switch {
		case err == nil:
			if cur, err = settingsOf(row); err != nil {
				return err
			}
		case !noRows(err):
			return fmt.Errorf("purchasing: lock the settings: %w", err)
		}
		if cur.revision != b.Revision {
			return versionConflict("settings' revision", cur.revision, b.Revision)
		}
		if b.OrderSeriesStart != cur.orderSeriesStart {
			next, used, err := nextNumber(ctx, txq, cur.orderSeriesStart)
			if err != nil {
				return err
			}
			if used && b.OrderSeriesStart < next {
				return refuseField(invalidSettingsTitle, "orderSeriesStart", codeSeriesStartTooLow,
					fmt.Sprintf("The order series has used numbers up to %d; it can only start above that", next-1))
			}
		}
		saved, err := txq.UpsertSettings(ctx, store.UpsertSettingsParams{
			OrderSeriesStart: b.OrderSeriesStart, DefaultSiteID: b.DefaultSiteId, SendChannel: channel,
			OrderFooter: b.OrderFooter, DeliveryAddresses: encoded, PriceTolerancePercent: numeric(tolerance, 2),
			UpdatedAt: s.deps.Clock(),
		})
		if err != nil {
			return fmt.Errorf("purchasing: save the settings: %w", err)
		}
		st, err := settingsOf(saved)
		if err != nil {
			return err
		}
		out, err = settingsView(ctx, txq, st)
		return err
	})
	if err != nil {
		return answer(err, resp)
	}
	if err := s.channelsView(ctx, &out); err != nil {
		return nil, err
	}
	return gen.PutPurchasingSettings200JSONResponse(out), nil
}
