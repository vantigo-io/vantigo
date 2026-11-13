package orders

import (
	"cmp"
	"context"
	"fmt"
	"math/big"

	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
	"github.com/vantigo-io/vantigo/server/internal/orders/store"
)

// This file writes and reads a document's lines (O15): a save replaces them
// whole, a line the editor names by id written again under that id.

// The owners of lines; a subscription's is ownerSubscription
// (subscription_schedule.go).
const (
	ownerQuote    = "quote"
	ownerOrder    = "order"
	ownerTemplate = "template"
)

// writeLines replaces the lines of one document with lines, in order, on the
// caller's transaction, and answers them as stored. The caller holds the
// document's row and has locked the lines' variants first (MB rule 21).
func writeLines(ctx context.Context, txq *store.Queries, ownerKind string, ownerID int64, lines []pricedLine) ([]store.OrdersLine, error) {
	if err := txq.DeleteLinesOf(ctx, store.DeleteLinesOfParams{OwnerKind: ownerKind, OwnerID: ownerID}); err != nil {
		return nil, fmt.Errorf("orders: clear %s %d's lines: %w", ownerKind, ownerID, err)
	}
	out := make([]store.OrdersLine, 0, len(lines))
	for i, l := range lines {
		// A kit's child line names its kit, written before it (manufacturing
		// phase 2 M145).
		var parent *int64
		if l.kit != nil {
			parent = &out[*l.kit].ID
		}
		row, err := insertLine(ctx, txq, ownerKind, ownerID, int32(i+1), l, parent)
		if err != nil {
			return nil, fmt.Errorf("orders: write %s %d's line %d: %w", ownerKind, ownerID, i+1, err)
		}
		out = append(out, row)
	}
	return out, nil
}

// insertLine writes one line at position, under its own id when it has one;
// parent is a kit child line's kit line.
func insertLine(ctx context.Context, txq *store.Queries, ownerKind string, ownerID int64, position int32, l pricedLine, parent *int64) (store.OrdersLine, error) {
	p := store.InsertLineParams{
		OwnerKind: ownerKind, OwnerID: ownerID, Position: position, Kind: l.kind, VariantID: l.variantID,
		Sku: l.sku, Name: l.name, Unit: l.unit, Description: l.description,
		Quantity: numeric(l.quantity, quantityPlaces), UnitPrice: numeric(l.unitPrice, pricePlaces),
		DiscountPercent: numeric(orZero(l.discount), discountPlaces), PackagingID: l.packagingID,
		PackagingQuantity: numeric(l.packagingQuantity, quantityPlaces), PriceSource: l.priceSource,
		VatCodeID: l.vatCodeID, VatCode: l.vatCode, VatRatePercent: numeric(l.vatRate, ratePlaces), VatCategory: l.vatCategory,
		LineGross: numeric(orZero(l.amounts.Gross), amountPlaces), LineAllowance: numeric(orZero(l.amounts.Allowance), amountPlaces),
		LineNet: numeric(orZero(l.amounts.Net), amountPlaces), ExternalLineID: l.externalLineID,
		InvoicePolicy: cmp.Or(l.invoicePolicy, policyOnOrder), Optional: l.optional && (ownerKind == ownerQuote || ownerKind == ownerTemplate),
		DeliveryMethod: cmp.Or(l.deliveryMethod, methodManual),
		ProductID:      l.productID, PackagingName: l.packagingName, ValidFrom: l.validFrom, ValidTo: l.validTo, ChainID: l.chainID,
		ParentLineID: parent, PerKit: numeric(l.perKit, quantityPlaces),
	}
	if !pricedKind(l.kind) {
		p.PriceSource = nil
	}
	if l.id == nil {
		return txq.InsertLine(ctx, p)
	}
	return txq.InsertLineKeepingID(ctx, store.InsertLineKeepingIDParams{
		ID: *l.id, OwnerKind: p.OwnerKind, OwnerID: p.OwnerID, Position: p.Position, Kind: p.Kind, VariantID: p.VariantID,
		Sku: p.Sku, Name: p.Name, Unit: p.Unit, Description: p.Description, Quantity: p.Quantity, UnitPrice: p.UnitPrice,
		DiscountPercent: p.DiscountPercent, PackagingID: p.PackagingID, PackagingQuantity: p.PackagingQuantity,
		PriceSource: p.PriceSource, VatCodeID: p.VatCodeID, VatCode: p.VatCode, VatRatePercent: p.VatRatePercent,
		VatCategory: p.VatCategory, LineGross: p.LineGross, LineAllowance: p.LineAllowance, LineNet: p.LineNet,
		ExternalLineID: p.ExternalLineID, InvoicePolicy: p.InvoicePolicy, Optional: p.Optional, DeliveryMethod: p.DeliveryMethod,
		ProductID: p.ProductID, PackagingName: p.PackagingName, ValidFrom: p.ValidFrom, ValidTo: p.ValidTo, ChainID: p.ChainID,
		ParentLineID: p.ParentLineID, PerKit: p.PerKit,
	})
}

// orZero is v, 0 for nil.
func orZero(v *big.Rat) *big.Rat {
	if v == nil {
		return new(big.Rat)
	}
	return v
}

// linesOf reads one document's lines on q.
func linesOf(ctx context.Context, q *store.Queries, ownerKind string, ownerID int64) ([]store.OrdersLine, error) {
	rows, err := q.LinesOf(ctx, store.LinesOfParams{OwnerKind: ownerKind, OwnerID: ownerID})
	if err != nil {
		return nil, fmt.Errorf("orders: read %s %d's lines: %w", ownerKind, ownerID, err)
	}
	return rows, nil
}

// byID is lines keyed by id.
func byID(lines []store.OrdersLine) map[int64]store.OrdersLine {
	out := make(map[int64]store.OrdersLine, len(lines))
	for _, l := range lines {
		out[l.ID] = l
	}
	return out
}

// linesOut are stored lines as the API answers them, the unit price also as
// entered and shown — including VAT when the document's prices do (O19).
func linesOut(lines []store.OrdersLine, pricesIncludeVat bool) []gen.OrdersLine {
	out := make([]gen.OrdersLine, 0, len(lines))
	for _, l := range lines {
		out = append(out, lineOut(l, pricesIncludeVat))
	}
	return out
}

func lineOut(l store.OrdersLine, pricesIncludeVat bool) gen.OrdersLine {
	out := gen.OrdersLine{
		Id: l.ID, Position: l.Position, Kind: gen.OrdersLineKind(l.Kind), VariantId: l.VariantID, Sku: l.Sku, Name: l.Name,
		Unit: l.Unit, Description: l.Description, Quantity: wirePtr(ratOf(l.Quantity), quantityPlaces),
		UnitPrice: wirePtr(ratOf(l.UnitPrice), pricePlaces), DiscountPercent: wire(ratOrZero(l.DiscountPercent), discountPlaces),
		PackagingId: l.PackagingID, PackagingQuantity: wirePtr(ratOf(l.PackagingQuantity), quantityPlaces),
		PackagingName: l.PackagingName, ProductId: l.ProductID,
		VatCodeId: l.VatCodeID, VatCode: l.VatCode, VatRatePercent: wirePtr(ratOf(l.VatRatePercent), ratePlaces),
		VatCategory: l.VatCategory, LineGross: wire(ratOrZero(l.LineGross), amountPlaces),
		LineAllowance: wire(ratOrZero(l.LineAllowance), amountPlaces), LineNet: wire(ratOrZero(l.LineNet), amountPlaces),
		ExternalLineId: l.ExternalLineID, InvoicePolicy: gen.OrdersLineInvoicePolicy(l.InvoicePolicy), Optional: l.Optional,
		QtyCancelled: wire(ratOrZero(l.QtyCancelled), quantityPlaces), QtyDelivered: wire(ratOrZero(l.QtyDelivered), quantityPlaces),
		QtyInvoiced: wire(ratOrZero(l.QtyInvoiced), quantityPlaces), DeliveryMethod: gen.OrdersLineDeliveryMethod(l.DeliveryMethod),
		ParentLineId: l.ParentLineID, PerKit: wirePtr(ratOf(l.PerKit), quantityPlaces),
	}
	if l.PriceSource != nil {
		src := gen.OrdersLinePriceSource(*l.PriceSource)
		out.PriceSource = &src
	}
	if price := ratOf(l.UnitPrice); price != nil {
		shown := price
		if rate := ratOf(l.VatRatePercent); pricesIncludeVat && rate != nil {
			shown = withVAT(price, rate)
		}
		out.DisplayUnitPrice = wirePtr(shown, pricePlaces)
	}
	return out
}
