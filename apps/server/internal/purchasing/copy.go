package purchasing

// Copy (P22, decision 16): a new draft from any order, for the same supplier
// and site, in the supplier's currency now, with today's prices from the
// supplier items. A goods line whose variant is gone or Discontinued is left
// out; a line keeps its packaging while that is still the variant's; text
// lines are copied as they are. A recurring purchase is a copy.

import (
	"context"
	"math/big"
	"strings"

	"github.com/vantigo-io/vantigo/server/internal/purchasing/gen"
)

// CopyPurchasingOrder Copy a purchase order
// (POST /api/v1/purchasing/orders/{orderId}/copy)
func (s *server) CopyPurchasingOrder(ctx context.Context, req gen.CopyPurchasingOrderRequestObject) (gen.CopyPurchasingOrderResponseObject, error) {
	resp := answers[gen.CopyPurchasingOrderResponseObject]{
		badRequest: func(p gen.PurchasingProblem) gen.CopyPurchasingOrderResponseObject {
			return gen.CopyPurchasingOrder400ApplicationProblemPlusJSONResponse(p)
		},
		notFound: func(p gen.PurchasingProblem) gen.CopyPurchasingOrderResponseObject {
			return gen.CopyPurchasingOrder404ApplicationProblemPlusJSONResponse(p)
		},
		conflict: func(p gen.PurchasingProblem) gen.CopyPurchasingOrderResponseObject {
			return gen.CopyPurchasingOrder409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.PurchasingProblem) gen.CopyPurchasingOrderResponseObject {
			return gen.CopyPurchasingOrder503ApplicationProblemPlusJSONResponse(p)
		},
	}
	src, err := readOrder(ctx, s.store(), req.OrderId)
	if err != nil {
		return answer(err, resp)
	}
	o := src.order
	e, err := s.supplier(ctx, o.SupplierID)
	if err != nil {
		return answer(err, resp)
	}
	if err := judgeSupplier(e, o.SupplierID); err != nil {
		return answer(err, resp)
	}
	d, err := s.daysOf(ctx)
	if err != nil {
		return nil, err
	}
	by, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	currency := strings.ToUpper(strings.TrimSpace(e.Currency))
	if currency == "" {
		currency = baseCurrency
	}
	var ids []int32
	for _, l := range src.lines {
		if l.VariantID != nil {
			ids = append(ids, *l.VariantID)
		}
	}
	variants, err := s.catalogVariants(ctx, ids)
	if err != nil {
		return answer(err, resp)
	}
	items, err := itemsOf(ctx, s.store(), o.SupplierID, ids)
	if err != nil {
		return nil, err
	}
	var lines []lineInput
	for _, l := range src.lines {
		in := lineInput{keys: map[string]bool{}}
		in.OrderQuantity = trimmed(ratOrZero(l.OrderQuantity), quantityPlaces)
		in.Description = ptr(l.Description)
		in.ExpectedOn = nil
		if !samePrice(ratOrZero(l.DiscountPercent), new(big.Rat)) {
			in.DiscountPercent = ptr(trimmed(ratOrZero(l.DiscountPercent), discountPlaces))
		}
		if l.Kind == kindText {
			in.Kind = gen.PurchasingLineInputKindText
			in.Unit = ptr(l.Unit)
			in.UnitPrice = ptr(priceText(ratOrZero(l.UnitPrice)))
			lines = append(lines, in)
			continue
		}
		v, ok := variants[*l.VariantID]
		if !ok || (v.ProductType == typeGoods && v.ProductStatus == productDiscontinued) {
			continue
		}
		in.Kind, in.VariantId = gen.PurchasingLineInputKindVariant, l.VariantID
		// The packaging the line was in, while the variant still has it; the
		// supplier item's price when it has one in the currency that converts
		// to that packaging (itemPriceFor), else the line's own when the
		// currency is the same.
		item, hasItem := items[v.ID]
		packaging := l.PackagingID
		if l.PackagingID == nil || packagingOf(v, *l.PackagingID) != nil {
			in.PackagingId, in.keys["packagingId"] = l.PackagingID, true
		} else if hasItem && item.PackagingID != nil && packagingOf(v, *item.PackagingID) != nil {
			packaging = item.PackagingID
		} else {
			packaging = nil
		}
		if !hasItem || item.Currency != currency || itemPriceFor(v, ratOf(item.Price), item.PackagingID, packaging) == nil {
			price := new(big.Rat)
			if o.Currency == currency {
				price = ratOrZero(l.UnitPrice)
			}
			in.UnitPrice = ptr(priceText(price))
		}
		lines = append(lines, in)
	}
	id, err := s.createDraft(ctx, newDraft{
		header: header{supplierID: o.SupplierID, siteID: o.SiteID, currency: currency, language: o.Language, orderDate: d.today,
			buyer: by, supplierContact: o.SupplierContact, note: o.Note, internalNote: o.InternalNote},
		lines: lines, copiedFrom: &o.ID, createdBy: by,
	})
	if err != nil {
		return answer(err, resp)
	}
	view, err := s.getOrderView(ctx, id)
	if err != nil {
		return nil, err
	}
	return gen.CopyPurchasingOrder201JSONResponse(view), nil
}

// samePrice reports whether two decimals are equal.
func samePrice(a, b *big.Rat) bool { return a.Cmp(b) == 0 }
