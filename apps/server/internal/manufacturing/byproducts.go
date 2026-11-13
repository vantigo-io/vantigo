package manufacturing

// By-products on a production (manufacturing phase 2 M120–M122). A BoM's
// by-products are snapshotted onto the order at confirm (order_outputs); a
// production names what it made of each (outputs), prefilled proportionally to
// the good quantity, and each becomes another produce line of the one
// Produce: the by-product's variant, its order_outputs id as the line, the
// order's output location, and for a lot-tracked one the lot named or the
// order's pattern rendered for its SKU, found or made by EnsureLots at step 7a
// as the main product's is. Produce lines are keyed by (variant, line id): an
// order_outputs id may equal a component line's or the order's.

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/gen"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/store"
)

// byproductValue is one by-product of a production request, judged.
type byproductValue struct {
	outputID int64
	quantity *big.Rat
	lot      *outputLotValues
}

// byproductMade is what a production made of one by-product: the produce
// line's figures and the lot it went into (0 for none).
type byproductMade struct {
	index    int // its index in the request's outputs
	output   store.ManufacturingOrderOutput
	quantity *big.Rat
	lotID    int64
	number   string
}

// judgeByproducts is the request's by-products' shape (M121): each once, a
// quantity 0 or more with at most three decimals.
func judgeByproducts(f *fieldErrors, prefix string, in *[]gen.ManufacturingProductionOutputInput) []byproductValue {
	if in == nil {
		return nil
	}
	if len(*in) > maxOutputs {
		f.add(prefix+"outputs", "At most 20 by-products")
		return nil
	}
	out := make([]byproductValue, 0, len(*in))
	seen := map[int64]bool{}
	for i, o := range *in {
		at := fmt.Sprintf("%soutputs[%d].", prefix, i)
		if seen[o.OutputId] {
			f.add(at+"outputId", "Two entries name this by-product")
		}
		seen[o.OutputId] = true
		q, msg := parseDecimal(o.Quantity, quantityPlaces, maxQuantity)
		f.addCoded(at+"quantity", codeQuantityInvalid, msg)
		v := byproductValue{outputID: o.OutputId, quantity: q}
		if o.Lot != nil {
			v.lot = judgeOutputLot(f, at, "lot", o.Lot)
		}
		out = append(out, v)
	}
	return out
}

// makesByproduct reports whether the request makes anything of a by-product.
func makesByproduct(v []byproductValue) bool {
	for _, b := range v {
		if b.quantity != nil && b.quantity.Sign() > 0 {
			return true
		}
	}
	return false
}

// prepareByproducts is step 7a for the by-products (M121, M86): each one the
// request makes, judged against the order's by-products and its variant as
// LockVariants answered it, and for a lot-tracked one its lot found or made by
// EnsureLots. A serial by-product is refused: its units are not numbered by an
// order in this version.
func (s *server) prepareByproducts(ctx context.Context, tx pgx.Tx, txq *store.Queries, ledger contracts.StockLedger, lo *lockedOrder,
	prep *preparedProduction, productionID int64) ([]byproductMade, error) {
	o := lo.order
	if len(prep.v.outputs) == 0 {
		return nil, nil
	}
	outputs, err := txq.OutputsOfOrders(ctx, []int64{o.ID})
	if err != nil {
		return nil, err
	}
	byID := map[int64]store.ManufacturingOrderOutput{}
	for _, x := range outputs {
		byID[x.ID] = x
	}
	var f fieldErrors
	for i, b := range prep.v.outputs {
		if _, ok := byID[b.outputID]; !ok {
			f.add(fmt.Sprintf("%soutputs[%d].outputId", prep.prefix, i), "Not a by-product of this order")
		}
	}
	if f.failed() {
		return nil, refuseInvalid(productionTitle, f)
	}
	mo := orderNumber(o.Number, o.Suffix)
	var made []byproductMade
	for i, b := range prep.v.outputs {
		if b.quantity.Sign() == 0 {
			continue
		}
		at := fmt.Sprintf("%soutputs[%d].", prep.prefix, i)
		x := byID[b.outputID]
		e, ok := lo.locked[x.VariantID]
		if !ok {
			return nil, errRelock
		}
		m := byproductMade{index: i, output: x, quantity: b.quantity}
		switch e.Tracking {
		case contracts.TrackingSerial:
			return nil, refuseField(productionTitle, at+"quantity", codeSerialQuantity,
				fmt.Sprintf("%s is tracked by serial number; a by-product is made into a lot, so track it by lot.", e.SKU))
		case contracts.TrackingLot:
			lot := contracts.NewLot{VariantID: x.VariantID, ProductionDate: prep.today}
			number := ""
			if l := b.lot; l != nil {
				lot.SupplierLot, lot.Expiry, lot.BestBefore, number = l.supplierLot, l.expiry, l.bestBefore, l.number
				if l.production != nil {
					lot.ProductionDate = *l.production
				}
			}
			id, number, err := s.ensureByproductLot(ctx, tx, txq, ledger, o.LotPattern, mo, e.SKU, at, productionSource(productionID), prep.today, lot, number)
			if err != nil {
				return nil, err
			}
			m.lotID, m.number = id, number
		default:
			if b.lot != nil {
				return nil, refuseField(productionTitle, at+"lot", codeLotNotAllowed, fmt.Sprintf("%s is not tracked in lots.", e.SKU))
			}
		}
		made = append(made, m)
	}
	return made, nil
}

// ensureByproductLot finds or makes a by-product's lot (M121, M80): the number
// sent, else the order's pattern rendered for the by-product's SKU. A counted
// number another document's lot has already is skipped under the counter's
// lock, each try in a savepoint, as the main product's is (ensureOutputLots).
func (s *server) ensureByproductLot(ctx context.Context, tx pgx.Tx, txq *store.Queries, ledger contracts.StockLedger, pattern, mo, sku, at string,
	src contracts.StockSource, today time.Time, lot contracts.NewLot, number string) (int64, string, error) {
	counted := false
	if number == "" {
		var err error
		if number, counted, err = renderLotNumber(ctx, txq, pattern, mo, lot.ProductionDate, sku, at); err != nil {
			return 0, "", err
		}
	}
	lot.Number, lot.MustCreate = number, counted
	for skips := 0; ; skips++ {
		var ids []int64
		var err error
		if counted {
			ids, err = ensureInSavepoint(ctx, tx, ledger, src, today, []contracts.NewLot{lot})
		} else {
			ids, err = ledger.EnsureLots(ctx, tx, src, today, []contracts.NewLot{lot})
		}
		var r *contracts.StockRefusal
		if errors.As(err, &r) && counted && skips < maxCounterSkips && onlyCode(r, contracts.StockLotExists) {
			if lot.Number, _, err = renderLotNumber(ctx, txq, pattern, mo, lot.ProductionDate, sku, at); err != nil {
				return 0, "", err
			}
			continue
		}
		if r != nil {
			switch r.Code {
			case contracts.StockLotDatesDiffer, contracts.StockLotExists, contracts.StockSerialQuantity:
				return 0, "", refuseField(productionTitle, at+"lot", r.Code, r.Detail)
			}
			if len(r.Lines) > 0 && onlyCode(r, r.Lines[0].Code) {
				switch r.Lines[0].Code {
				case contracts.StockLotDatesDiffer, contracts.StockLotExists, contracts.StockSerialQuantity:
					return 0, "", refuseField(productionTitle, at+"lot", r.Lines[0].Code, r.Lines[0].Detail)
				}
			}
			return 0, "", stockRefusal(r, nil)
		}
		if err != nil {
			return 0, "", fmt.Errorf("manufacturing: ensure a by-product's lot: %w", err)
		}
		return ids[0], lot.Number, nil
	}
}

// byproductLines is the by-products' produce lines (M121): at the order's
// output location, the line its order_outputs id.
func byproductLines(o store.ManufacturingOrder, made []byproductMade) []contracts.ProduceLine {
	out := make([]contracts.ProduceLine, 0, len(made))
	for _, m := range made {
		out = append(out, contracts.ProduceLine{VariantID: m.output.VariantID, LineID: m.output.ID, ToLocationID: o.OutputLocationID,
			Quantity: m.quantity.FloatString(quantityPlaces), LotID: m.lotID})
	}
	return out
}

// byproductHolds is the by-products' lots as the deviations' hold takes them.
func byproductHolds(made []byproductMade) []outputLot {
	var out []outputLot
	for _, m := range made {
		if m.lotID != 0 {
			out = append(out, outputLot{number: m.number, quantity: m.quantity, lotID: m.lotID})
		}
	}
	return out
}

// writeByproducts records what the production made of each by-product and
// adds it to the order's (M121).
func writeByproducts(ctx context.Context, txq *store.Queries, productionID int64, made []byproductMade) error {
	for _, m := range made {
		params := store.InsertProductionByproductParams{ProductionID: productionID, OutputID: m.output.ID, Quantity: numeric(m.quantity, quantityPlaces)}
		if m.lotID != 0 {
			id, number := m.lotID, m.number
			params.LotID, params.LotNumber = &id, &number
		}
		if err := txq.InsertProductionByproduct(ctx, params); err != nil {
			return err
		}
		if err := txq.AddOutputProduced(ctx, store.AddOutputProducedParams{ID: m.output.ID, Produced: numeric(m.quantity, quantityPlaces)}); err != nil {
			return err
		}
	}
	return nil
}

// byproductAnswer is a production's by-product row as answered.
func byproductAnswer(r store.ProductionByproductsRow, costs bool) gen.ManufacturingProductionByproduct {
	ref := componentRef(nil, store.ManufacturingOrderComponent{VariantID: r.VariantID, Sku: r.Sku, ProductName: r.ProductName, Unit: r.Unit}, costs)
	ref.ProductType = goodsType
	return gen.ManufacturingProductionByproduct{OutputId: r.OutputID, Variant: ref, Quantity: numericText(r.Quantity, quantityPlaces),
		LotId: r.LotID, LotNumber: r.LotNumber}
}

// draftByproducts is the production dialog's by-products (M121): each
// proposed as its expected × the quantity ÷ the order's quantity, and for a
// lot-tracked one the number its lot would have today.
func (s *server) draftByproducts(ctx context.Context, o store.ManufacturingOrder, outputs []store.ManufacturingOrderOutput, quantity *big.Rat,
	today time.Time, costs bool) ([]gen.ManufacturingProductionDraftByproduct, error) {
	out := make([]gen.ManufacturingProductionDraftByproduct, 0, len(outputs))
	if len(outputs) == 0 {
		return out, nil
	}
	ids := make([]int32, 0, len(outputs))
	for _, x := range outputs {
		ids = append(ids, x.VariantID)
	}
	entries, err := s.catalogVariants(ctx, dedupe(ids))
	if err != nil {
		return nil, err
	}
	q := store.New(s.deps.Pool)
	mo := orderNumber(o.Number, o.Suffix)
	for _, x := range outputs {
		e := entries[x.VariantID]
		d := gen.ManufacturingProductionDraftByproduct{OutputId: x.ID, Variant: outputAnswer(x, entries, costs).Variant,
			Expected: numericText(x.Expected, quantityPlaces), Produced: numericText(x.Produced, quantityPlaces),
			Proposed: text(proportional(ratOrZero(x.Expected), quantity, ratOrZero(o.Quantity)), quantityPlaces), Tracking: trackingOf(e)}
		if e.Tracking == contracts.TrackingLot {
			if d.LotNumber, err = previewLotNumber(ctx, q, o.LotPattern, mo, today, e.SKU); err != nil {
				return nil, err
			}
		}
		out = append(out, d)
	}
	return out, nil
}
