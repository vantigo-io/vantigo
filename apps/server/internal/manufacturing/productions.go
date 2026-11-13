package manufacturing

// Productions (manufacturing design M22, M22a, M2a): the proportional
// prefill, the consumption warning, the location split, and the post through
// StockLedger.Produce — src the order, the document the production record —
// which consumes the components and produces the item in one movement. A
// refusal from Produce rolls the whole transaction back, the production row
// with it: nothing is committed after one.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/gen"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/store"
)

const (
	productionTitle         = "The production was not registered"
	productionUniqueIndex   = "ux_productions_client_request"
	deviationPercent        = 10
	maxProductionLines      = 400
	maxClientRequestIDRunes = 100
)

// proportional is a line's prefill (M22a): required × base / order
// quantity, rounded half-up to three decimals.
func proportional(required, base, orderQuantity *big.Rat) *big.Rat {
	v := new(big.Rat).Mul(required, base)
	return roundPlaces(v.Quo(v, orderQuantity), quantityPlaces)
}

// draftAnswers are the prefill's typed refusals.
func draftAnswers() answers[gen.GetManufacturingProductionDraftResponseObject] {
	return answers[gen.GetManufacturingProductionDraftResponseObject]{
		badRequest: func(p gen.ManufacturingProblem) gen.GetManufacturingProductionDraftResponseObject {
			return gen.GetManufacturingProductionDraft400ApplicationProblemPlusJSONResponse(p)
		},
		notFound: func() gen.GetManufacturingProductionDraftResponseObject {
			return gen.GetManufacturingProductionDraft404Response{}
		},
		conflict: func(p gen.ManufacturingProblem) gen.GetManufacturingProductionDraftResponseObject {
			return gen.GetManufacturingProductionDraft409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.ManufacturingProblem) gen.GetManufacturingProductionDraftResponseObject {
			return gen.GetManufacturingProductionDraft503ApplicationProblemPlusJSONResponse(p)
		},
	}
}

// GetManufacturingProductionDraft is the production dialog's prefill
// (M22a): the quantity defaults to what remains to produce.
func (s *server) GetManufacturingProductionDraft(ctx context.Context, req gen.GetManufacturingProductionDraftRequestObject) (gen.GetManufacturingProductionDraftResponseObject, error) {
	r, err := s.readOrder(ctx, req.Id)
	if err != nil {
		return answer(err, draftAnswers())
	}
	o := r.order
	if !isOpen(o.Status) {
		return answer(orderNotOpen(o.Status), draftAnswers())
	}
	var f fieldErrors
	quantity := positive(new(big.Rat).Sub(ratOrZero(o.Quantity), ratOrZero(o.Produced)))
	if req.Params.Quantity != nil {
		q, msg := parseDecimal(*req.Params.Quantity, quantityPlaces, maxQuantity)
		f.addCoded("quantity", codeQuantityInvalid, msg)
		quantity = q
	}
	outputScrap := new(big.Rat)
	if req.Params.OutputScrap != nil {
		q, msg := parseDecimal(*req.Params.OutputScrap, quantityPlaces, maxQuantity)
		f.addCoded("outputScrap", codeQuantityInvalid, msg)
		outputScrap = q
	}
	if f.failed() {
		return answer(refuseInvalid("The prefill was not computed", f), draftAnswers())
	}
	base := new(big.Rat).Add(quantity, outputScrap)
	ids := make([]int32, 0, len(r.components)+1)
	ids = append(ids, o.VariantID)
	for _, c := range r.components {
		ids = append(ids, c.VariantID)
	}
	entries, err := s.catalogVariants(ctx, slices.Compact(slices.Sorted(slices.Values(ids))))
	if err != nil {
		return answer(err, draftAnswers())
	}
	today, err := s.stockDay(ctx)
	if err != nil {
		return answer(err, draftAnswers())
	}
	// The tracked components' lots at the site (manufacturing phase 2 M82):
	// the proposal, FEFO, and every lot for Change lot, so neither the order
	// page nor the shop floor reads Inventory for the picker.
	var tracked []int32
	for _, c := range r.components {
		if isTracked(entries[c.VariantID]) {
			tracked = append(tracked, c.VariantID)
		}
	}
	lots, err := s.stockLots(ctx, tracked, o.SiteID)
	if err != nil {
		return answer(err, draftAnswers())
	}
	pool := newLotPool(lots)
	costs := s.has(ctx, permCostsView)
	output, err := s.draftOutput(ctx, o, entries[o.VariantID], today)
	if err != nil {
		return answer(err, draftAnswers())
	}
	byProducts, err := s.draftByproducts(ctx, o, r.outputs, quantity, today, costs)
	if err != nil {
		return answer(err, draftAnswers())
	}
	out := gen.GetManufacturingProductionDraft200JSONResponse{Quantity: text(quantity, quantityPlaces), OutputScrap: text(outputScrap, quantityPlaces),
		ToLocationId: o.OutputLocationID, Lines: make([]gen.ManufacturingProductionDraftLine, 0, len(r.components)), Output: output, ByProducts: byProducts}
	for _, c := range r.components {
		proposed := proportional(ratOrZero(c.Required), base, ratOrZero(o.Quantity))
		line := gen.ManufacturingProductionDraftLine{ComponentId: c.ID, Variant: componentRef(entries, c, costs),
			Required: numericText(c.Required, quantityPlaces), Remaining: text(remainingOf(c), quantityPlaces),
			Reserved: numericText(c.Reserved, quantityPlaces), Proposed: text(proposed, quantityPlaces),
			Tracking: trackingOf(entries[c.VariantID]), Lots: []gen.ManufacturingDraftLot{}, LotChoices: []gen.ManufacturingLotChoice{}, Short: "0"}
		if isTracked(entries[c.VariantID]) {
			known := map[[2]int64]contracts.StockLot{}
			for _, l := range lots[c.VariantID] {
				known[[2]int64{l.LotID, l.LocationID}] = l
				line.LotChoices = append(line.LotChoices, gen.ManufacturingLotChoice{LotId: l.LotID, LotNumber: l.Number, Expiry: dateOut(l.Expiry),
					BestBefore: dateOut(l.BestBefore), LocationId: l.LocationID, FullCode: l.FullCode, Quantity: text(mustRat(l.Quantity), quantityPlaces),
					Status: gen.ManufacturingLotChoiceStatus(l.Status), Expired: contracts.LotExpired(l.Expiry, today), Serial: l.Serial})
			}
			picks, short, err := pool.propose(c.VariantID, proposed, o.ComponentLocationID, today)
			if err != nil {
				return answer(err, draftAnswers())
			}
			for _, pk := range picks {
				l := known[[2]int64{pk.LotID, pk.LocationID}]
				line.Lots = append(line.Lots, gen.ManufacturingDraftLot{LotId: pk.LotID, LotNumber: l.Number, Expiry: dateOut(l.Expiry),
					LocationId: pk.LocationID, FullCode: l.FullCode, Quantity: text(mustRat(pk.Quantity), quantityPlaces)})
			}
			line.Short = text(short, quantityPlaces)
		}
		out.Lines = append(out.Lines, line)
	}
	return out, nil
}

// draftOutput is what a production of the draft makes (M80, M81): the
// product's tracking, and for a tracked one the number its lot would have
// today, its default dates, or the first default serial.
func (s *server) draftOutput(ctx context.Context, o store.ManufacturingOrder, e contracts.VariantEntry, today time.Time) (gen.ManufacturingProductionDraftOutput, error) {
	out := gen.ManufacturingProductionDraftOutput{Tracking: trackingOf(e), LotPattern: o.LotPattern, ProductionDate: openapi_types.Date{Time: today}}
	if !isTracked(e) {
		return out, nil
	}
	q := store.New(s.deps.Pool)
	mo := orderNumber(o.Number, o.Suffix)
	if e.ExpiryDays != nil {
		out.Expiry = &openapi_types.Date{Time: today.AddDate(0, 0, int(*e.ExpiryDays))}
	}
	if e.BestBeforeDays != nil {
		out.BestBefore = &openapi_types.Date{Time: today.AddDate(0, 0, int(*e.BestBeforeDays))}
	}
	if e.Tracking == contracts.TrackingSerial {
		before, err := q.SerialOutputNumbersOfOrder(ctx, o.ID)
		if err != nil {
			return out, err
		}
		first := defaultSerialFirst(mo, before)
		out.SerialFirst = &first
		return out, nil
	}
	number, err := previewLotNumber(ctx, q, o.LotPattern, mo, today, e.SKU)
	if err != nil {
		return out, err
	}
	out.LotNumber = number
	return out, nil
}

// productionLine is one judged line of a production request.
type productionLine struct {
	componentID int64
	consumed    *big.Rat // nil: not sent
	scrap       *big.Rat
	from        *int64
	// lots are a tracked component's lot rows (manufacturing phase 2 M82),
	// nil when none were sent; proposed says they are the draft's proposal
	// as it was, which the post may propose again once (M65a).
	lots     []lotRow
	proposed bool
}

// productionValues is a production request, judged.
type productionValues struct {
	quantity    *big.Rat
	outputScrap *big.Rat
	to          *int64
	lines       []productionLine
	// outputLot and serials are the output's lot and serial numbers as sent
	// (M80, M81), nil when not sent.
	outputLot *outputLotValues
	serials   *serialValues
	// outputs is what it makes of the order's by-products (manufacturing
	// phase 2 M121), nil when none were sent.
	outputs []byproductValue
	hash    string
}

// judgeProduction is the request's shape (M22): every quantity 0 or more
// with at most three decimals, each line once, and something to register.
// f carries what the body's closed schemas refused already; prefix is where
// the production sits in the body ("" for a production's own post,
// "production." inside a finish).
func judgeProduction(in gen.ManufacturingProductionInput, f fieldErrors, prefix string) (productionValues, fieldErrors) {
	v := productionValues{to: in.ToLocationId, outputScrap: new(big.Rat)}
	if n := len([]rune(in.ClientRequestId)); n < 1 || n > maxClientRequestIDRunes {
		f.add(prefix+"clientRequestId", "1–100 characters")
	}
	q, msg := parseDecimal(in.Quantity, quantityPlaces, maxQuantity)
	f.addCoded(prefix+"quantity", codeQuantityInvalid, msg)
	v.quantity = q
	if in.OutputScrap != nil {
		os, msg := parseDecimal(*in.OutputScrap, quantityPlaces, maxQuantity)
		f.addCoded(prefix+"outputScrap", codeQuantityInvalid, msg)
		v.outputScrap = os
	}
	if len(in.Lines) > maxProductionLines {
		f.add(prefix+"lines", "At most 400 lines")
	}
	seen := map[int64]bool{}
	something := false
	for i, l := range in.Lines {
		at := fmt.Sprintf("%slines[%d].", prefix, i)
		pl := productionLine{componentID: l.ComponentId, scrap: new(big.Rat), from: l.FromLocationId}
		if seen[l.ComponentId] {
			f.add(at+"componentId", "Two lines name this component line")
		}
		seen[l.ComponentId] = true
		if l.Consumed != nil {
			c, msg := parseDecimal(*l.Consumed, quantityPlaces, maxQuantity)
			f.addCoded(at+"consumed", codeQuantityInvalid, msg)
			pl.consumed = c
			if c != nil && c.Sign() > 0 {
				something = true
			}
		}
		if l.Scrap != nil {
			c, msg := parseDecimal(*l.Scrap, quantityPlaces, maxQuantity)
			f.addCoded(at+"scrap", codeQuantityInvalid, msg)
			pl.scrap = c
			if c != nil && c.Sign() > 0 {
				something = true
			}
		}
		pl.lots, pl.proposed = judgeLotRows(&f, at, l, pl)
		v.lines = append(v.lines, pl)
	}
	v.outputLot = judgeOutputLot(&f, prefix, "outputLot", in.OutputLot)
	v.serials = judgeSerials(&f, prefix, in.OutputSerials)
	v.outputs = judgeByproducts(&f, prefix, in.Outputs)
	if f.failed() {
		return v, f
	}
	if v.quantity.Sign() == 0 && v.outputScrap.Sign() == 0 && !something && !makesByproduct(v.outputs) {
		f.addCoded(prefix+"quantity", codeQuantityInvalid, "0 only with consumption, scrap or output scrap")
		return v, f
	}
	v.hash = productionHash(v)
	return v, f
}

// productionHash is the request's body as one value, quantities as their
// shortest text, so a replay with the same body matches whatever its
// spelling ("2" and "2.000") and acceptDeviation is not part of it.
func productionHash(v productionValues) string {
	type lot struct {
		L  int64  `json:"l"`
		At int64  `json:"a"`
		C  string `json:"c"`
		S  string `json:"s"`
		E  bool   `json:"e,omitempty"`
	}
	type line struct {
		C    int64   `json:"c"`
		Used *string `json:"u"`
		S    string  `json:"s"`
		F    *int64  `json:"f"`
		Lots []lot   `json:"lots,omitempty"`
		P    bool    `json:"p,omitempty"`
	}
	type output struct {
		N, Supplier           string
		Made, Exp, BestBefore string
		First                 string
		Numbers               []string
	}
	type byproduct struct {
		ID     int64   `json:"i"`
		Q      string  `json:"q"`
		Output *output `json:"lot,omitempty"`
	}
	body := struct {
		Q          string      `json:"q"`
		O          string      `json:"o"`
		To         *int64      `json:"t"`
		Lines      []line      `json:"l"`
		Output     *output     `json:"out,omitempty"`
		Byproducts []byproduct `json:"by,omitempty"`
	}{Q: text(v.quantity, quantityPlaces), O: text(v.outputScrap, quantityPlaces), To: v.to}
	for _, b := range v.outputs {
		x := byproduct{ID: b.outputID, Q: text(b.quantity, quantityPlaces)}
		if l := b.lot; l != nil {
			x.Output = &output{N: l.number, Supplier: l.supplierLot, Made: dayText(l.production), Exp: dayText(l.expiry), BestBefore: dayText(l.bestBefore)}
		}
		body.Byproducts = append(body.Byproducts, x)
	}
	for _, l := range v.lines {
		x := line{C: l.componentID, S: text(l.scrap, quantityPlaces), F: l.from, P: l.proposed}
		if l.consumed != nil {
			t := text(l.consumed, quantityPlaces)
			x.Used = &t
		}
		for _, r := range l.lots {
			x.Lots = append(x.Lots, lot{L: r.lotID, At: r.locationID, C: text(r.consumed, quantityPlaces), S: text(r.scrapped, quantityPlaces), E: r.allowExpired})
		}
		body.Lines = append(body.Lines, x)
	}
	if v.outputLot != nil || v.serials != nil {
		body.Output = &output{}
		if o := v.outputLot; o != nil {
			body.Output.N, body.Output.Supplier = o.number, o.supplierLot
			body.Output.Made, body.Output.Exp, body.Output.BestBefore = dayText(o.production), dayText(o.expiry), dayText(o.bestBefore)
		}
		if sv := v.serials; sv != nil {
			body.Output.First, body.Output.Numbers = sv.first, sv.numbers
		}
	}
	b, _ := json.Marshal(body)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// postAnswers are the post's typed refusals.
func postAnswers() answers[gen.PostManufacturingProductionResponseObject] {
	return answers[gen.PostManufacturingProductionResponseObject]{
		badRequest: func(p gen.ManufacturingProblem) gen.PostManufacturingProductionResponseObject {
			return gen.PostManufacturingProduction400ApplicationProblemPlusJSONResponse(p)
		},
		forbidden: func(p gen.ManufacturingProblem) gen.PostManufacturingProductionResponseObject {
			return gen.PostManufacturingProduction403ApplicationProblemPlusJSONResponse(p)
		},
		notFound: func() gen.PostManufacturingProductionResponseObject {
			return gen.PostManufacturingProduction404Response{}
		},
		conflict: func(p gen.ManufacturingProblem) gen.PostManufacturingProductionResponseObject {
			return gen.PostManufacturingProduction409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.ManufacturingProblem) gen.PostManufacturingProductionResponseObject {
			return gen.PostManufacturingProduction503ApplicationProblemPlusJSONResponse(p)
		},
	}
}

// errReplay is the post's answer when another request with the same
// clientRequestId was committed meanwhile: the transaction rolls back and the
// first production answers.
var errReplay = errors.New("manufacturing: the production was posted meanwhile")

// errResplit is the post's answer when Inventory refused insufficient_stock
// on a line split over the locations read before the transaction (M22a):
// another production took from them meanwhile, so the transaction rolls back
// and the post starts again from a fresh Stock.Locations, at most
// maxSplitAttempts times.
var errResplit = errors.New("manufacturing: the locations changed under the split; read them again")

// maxSplitAttempts is how many times a post reads the locations and splits.
const maxSplitAttempts = 3

// replayOf is the production a clientRequestId already posted answers
// (M22a): p when the body is the same and of the same order, else
// client_request_reused.
func replayOf(orderID int64, p store.ManufacturingProduction, hash string) (int64, error) {
	if p.OrderID != orderID || p.RequestHash != hash {
		return 0, conflict(codeClientRequestReused, "The request id was used for another production",
			"A production with this clientRequestId was registered with another body. Send a new clientRequestId for a new production.")
	}
	return p.ID, nil
}

// requireShopFloor is the 403 shop_floor_required of a production or a
// shop-floor action by a caller holding neither manufacturing:shop-floor nor
// manufacturing:orders-manage (M33): the operation's rule asks only for
// manufacturing:access, and the handler checks the either-or.
func (s *server) requireShopFloor(ctx context.Context, what string) error {
	if s.has(ctx, permShopFloor) || s.has(ctx, permOrdersManage) {
		return nil
	}
	return refuse(problem(http.StatusForbidden, codeShopFloorRequired, what+" is not yours to do",
		what+" needs manufacturing:shop-floor or manufacturing:orders-manage."))
}

// preparedProduction is a production request judged before its
// transaction: its values, the order's site and component variants, and —
// when its clientRequestId was posted before — the production it replays.
type preparedProduction struct {
	in       gen.ManufacturingProductionInput
	v        productionValues
	orderID  int64
	siteID   int64
	variants []int32
	// tracked is the component variants the catalog said were tracked
	// before the transaction: whose lots are read, for their numbers, before
	// each attempt (manufacturing phase 2 M82). LockVariants' answer decides.
	tracked []int32
	replay  *store.ManufacturingProduction
	// today and expiredOverride are what the ledger judges a lot's expiry
	// by, read on the pool before the transaction (manufacturing phase 2
	// M62, rule 24): the business day, and whether the poster holds
	// inventory:expired-override.
	today           time.Time
	expiredOverride bool
	// reproposed is set once the proposed lots were proposed again (M65a).
	reproposed bool
	// prefix is where the production sits in the body ("" for a
	// production's own post, "production." inside a finish), for the field
	// errors raised in the transaction.
	prefix string
}

// prepareProduction judges a production request for order orderID before
// any transaction (M22, M22a, M26a): its shape, under prefix with f the
// closed schemas' refusals; a replay of its clientRequestId; the order open;
// its locations against the site's, read from Inventory now.
func (s *server) prepareProduction(ctx context.Context, orderID int64, in gen.ManufacturingProductionInput, f fieldErrors, prefix string) (*preparedProduction, error) {
	v, f := judgeProduction(in, f, prefix)
	if f.failed() {
		return nil, refuseInvalid(productionTitle, f)
	}
	pool := store.New(s.deps.Pool)
	if p, err := pool.ProductionByClientRequest(ctx, in.ClientRequestId); err == nil {
		return &preparedProduction{in: in, v: v, orderID: orderID, replay: &p, prefix: prefix}, nil
	} else if !noRows(err) {
		return nil, err
	}
	pre, err := pool.GetOrder(ctx, orderID)
	if noRows(err) {
		return nil, notFound()
	}
	if err != nil {
		return nil, err
	}
	if !isOpen(pre.Status) {
		return nil, orderNotOpen(pre.Status)
	}
	// Inventory is read before the transaction (M26a): the site's locations
	// to judge the request's, and where each component lies to split it.
	locations, err := s.siteLocations(ctx, pre.SiteID)
	if err != nil {
		return nil, err
	}
	active := map[int64]bool{}
	for _, l := range locations {
		active[l.ID] = l.Active
	}
	var lf fieldErrors
	if v.to != nil && !active[*v.to] {
		lf.addCoded(prefix+"toLocationId", codeLocationUnknown, "Not an active location of the order's site")
	}
	for i, l := range v.lines {
		if l.from != nil && !active[*l.from] {
			lf.addCoded(fmt.Sprintf("%slines[%d].fromLocationId", prefix, i), codeLocationUnknown, "Not an active location of the order's site")
		}
		for j, r := range l.lots {
			if !active[r.locationID] {
				lf.addCoded(fmt.Sprintf("%slines[%d].lots[%d].locationId", prefix, i, j), codeLocationUnknown, "Not an active location of the order's site")
			}
		}
	}
	if lf.failed() {
		return nil, refuseInvalid(productionTitle, lf)
	}
	components, err := pool.ListOrderComponents(ctx, pre.ID)
	if err != nil {
		return nil, err
	}
	today, err := s.stockDay(ctx)
	if err != nil {
		return nil, err
	}
	prep := &preparedProduction{in: in, v: v, orderID: orderID, siteID: pre.SiteID, variants: make([]int32, 0, len(components)),
		today: today, expiredOverride: s.has(ctx, permInventoryExpiredOverride), prefix: prefix}
	for _, c := range components {
		prep.variants = append(prep.variants, c.VariantID)
	}
	entries, err := s.catalogVariants(ctx, slices.Compact(slices.Sorted(slices.Values(prep.variants))))
	if err != nil {
		return nil, err
	}
	prep.tracked = trackedVariants(entries)
	return prep, nil
}

// runProduction posts prep on order orderID under the lock order (M26a,
// M86): before, when set, does the write's own work-order and time-log
// writes on the locked orders first (steps 4–6), then the production is
// posted — its lots ensured (7a), Produce last (7c). The locations and the
// tracked components' lots are read before each attempt; the attempt runs
// again when another production took from a split's locations meanwhile
// (errResplit) or a lot taken was not read (errLotsMoved), with the proposed
// lots proposed again once when they were refused (M65a), and once when
// PostgreSQL chose it as a deadlock's victim (M86) — a second deadlock is 409
// revision_conflict. A nil prep runs before alone. A clientRequestId
// committed by another request meanwhile answers that production as the
// replay. A lot taken that was still not read on the last attempt is 409
// revision_conflict: a lot row is never kept without its number. idsOf names the orders the write locks, by default the
// production's alone; the production's order is one of them.
func (s *server) runProduction(ctx context.Context, orderID int64, idsOf func(context.Context, *store.Queries) ([]int64, error), prep *preparedProduction, who actor,
	before func(ctx context.Context, tx pgx.Tx, txq *store.Queries, orders map[int64]*lockedOrder) error) (int64, error) {
	if idsOf == nil {
		idsOf = func(context.Context, *store.Queries) ([]int64, error) { return []int64{orderID}, nil }
	}
	var productionID int64
	var err error
	resplits, deadlocked := 0, false
	for {
		var held map[int32][]contracts.StockLocation
		var lots map[int32][]contracts.StockLot
		if prep != nil {
			if held, err = s.stockLocations(ctx, prep.variants, prep.siteID); err != nil {
				return 0, err
			}
			if lots, err = s.stockLots(ctx, prep.tracked, prep.siteID); err != nil {
				return 0, err
			}
			held = usableLocations(held, lots, prep.today)
		}
		var componentLocation *int64
		err = s.withOrders(ctx, idsOf, nil, func(ctx context.Context, tx pgx.Tx, txq *store.Queries, orders map[int64]*lockedOrder) error {
			if before != nil {
				if err := before(ctx, tx, txq, orders); err != nil {
					return err
				}
			}
			if prep == nil {
				return nil
			}
			componentLocation = orders[orderID].order.ComponentLocationID
			id, err := s.postProduction(ctx, tx, txq, orders[orderID], prep, held, lotInfos(lots), who, resplits == maxSplitAttempts-1)
			productionID = id
			return err
		})
		var again *errRepropose
		switch {
		case (errors.Is(err, errResplit) || errors.Is(err, errLotsMoved)) && resplits < maxSplitAttempts-1:
			resplits++
			continue
		case errors.As(err, &again) && !prep.reproposed:
			prep.reproposed = true
			if err = s.repropose(ctx, prep, componentLocation, again); err != nil {
				return 0, err
			}
			continue
		case isDeadlock(err) && !deadlocked:
			deadlocked = true
			continue
		case isDeadlock(err):
			err = deadlockConflict()
		case errors.Is(err, errLotsMoved):
			err = lotsMovedConflict()
		}
		break
	}
	if prep != nil && (errors.Is(err, errReplay) || db.IsUniqueViolation(err, productionUniqueIndex)) {
		p, rerr := store.New(s.deps.Pool).ProductionByClientRequest(ctx, prep.in.ClientRequestId)
		if rerr != nil {
			return 0, rerr
		}
		id, rerr := replayOf(orderID, p, prep.v.hash)
		if rerr != nil {
			return 0, rerr
		}
		return id, errReplayed
	}
	return productionID, err
}

// errReplayed is runProduction's answer, with the first production's id,
// when the post was a replay: the handler answers 200 rather than 201.
var errReplayed = errors.New("manufacturing: the production was a replay")

// PostManufacturingProduction registers a production (M22, M22a): it needs
// manufacturing:shop-floor or manufacturing:orders-manage (M33).
func (s *server) PostManufacturingProduction(ctx context.Context, req gen.PostManufacturingProductionRequestObject) (gen.PostManufacturingProductionResponseObject, error) {
	if err := s.requireShopFloor(ctx, "Registering a production"); err != nil {
		return answer(err, postAnswers())
	}
	prep, err := s.prepareProduction(ctx, req.Id, *req.Body, s.unknownProperties(ctx, productionInputSchema), "")
	if err != nil {
		return answer(err, postAnswers())
	}
	if prep.replay != nil {
		id, err := replayOf(req.Id, *prep.replay, prep.v.hash)
		if err != nil {
			return answer(err, postAnswers())
		}
		return s.productionResponse(ctx, id, true)
	}
	who, err := s.actorOf(ctx)
	if err != nil {
		return nil, err
	}
	id, err := s.runProduction(ctx, req.Id, nil, prep, who, func(ctx context.Context, tx pgx.Tx, _ *store.Queries, orders map[int64]*lockedOrder) error {
		return notifyOrder(ctx, tx, req.Id)
	})
	if errors.Is(err, errReplayed) {
		return s.productionResponse(ctx, id, true)
	}
	if err != nil {
		return answer(err, postAnswers())
	}
	return s.productionResponse(ctx, id, false)
}

// productionResponse is the post's answer: 200 for a replay, 201 for a
// first post.
func (s *server) productionResponse(ctx context.Context, id int64, replayed bool) (gen.PostManufacturingProductionResponseObject, error) {
	out, err := s.productionAnswer(ctx, id)
	if err != nil {
		return nil, err
	}
	if replayed {
		return gen.PostManufacturingProduction200JSONResponse(out), nil
	}
	return gen.PostManufacturingProduction201JSONResponse(out), nil
}

// postProduction is the post on the locked order: the state, the lines, the
// consumption warning, the production row, the output's lots (M80, M81,
// step 7a), the split, Produce (7c), the lots taken (M82), and the totals.
// Unless last, Inventory's insufficient_stock on a split line is errResplit;
// a lot taken that known has no number of is errLotsMoved; a refusal of
// proposed lots only is errRepropose until they were proposed again (M65a).
func (s *server) postProduction(ctx context.Context, tx pgx.Tx, txq *store.Queries, lo *lockedOrder, prep *preparedProduction,
	held map[int32][]contracts.StockLocation, known map[int64]lotInfo, who actor, last bool) (int64, error) {
	o := lo.order
	in, v := prep.in, prep.v
	if !isOpen(o.Status) {
		return 0, orderNotOpen(o.Status)
	}
	if _, err := txq.ProductionByClientRequest(ctx, in.ClientRequestId); err == nil {
		return 0, errReplay
	} else if !noRows(err) {
		return 0, err
	}
	components, err := txq.ListOrderComponents(ctx, o.ID)
	if err != nil {
		return 0, err
	}
	byID := map[int64]store.ManufacturingOrderComponent{}
	for _, c := range components {
		byID[c.ID] = c
	}
	var f fieldErrors
	for i, l := range v.lines {
		if _, ok := byID[l.componentID]; !ok {
			f.add(fmt.Sprintf("%slines[%d].componentId", prep.prefix, i), "Not a component line of this order")
		}
	}
	if f.failed() {
		return 0, refuseInvalid(productionTitle, f)
	}
	if in.AcceptDeviation == nil || !*in.AcceptDeviation {
		if err := judgeDeviation(o, byID, v); err != nil {
			return 0, err
		}
	}
	// The checks a production with good output waits for (M98).
	if v.quantity.Sign() > 0 {
		if err := productionGate(ctx, txq, o.ID); err != nil {
			return 0, err
		}
	}
	to := o.OutputLocationID
	if v.to != nil {
		to = *v.to
	}
	at := s.deps.Clock()
	p, err := txq.InsertProduction(ctx, store.InsertProductionParams{
		OrderID: o.ID, ClientRequestID: in.ClientRequestId, RequestHash: v.hash, Quantity: numeric(v.quantity, quantityPlaces),
		OutputScrap: numeric(v.outputScrap, quantityPlaces), ToLocationID: to, PostedByUserID: who.id, PostedByDisplay: who.display, Now: at,
	})
	if err != nil {
		return 0, err
	}
	ledger, err := s.ledger()
	if err != nil {
		return 0, err
	}
	// 7a. The output's lots, found or made after the order's lock and
	// before Produce (M80, M86): the lots come before the locations
	// Produce shares, for every writer (M58).
	outs, newLots, err := s.outputLots(ctx, txq, o, lo.locked[o.VariantID], v, prep.today, prep.prefix)
	if err != nil {
		return 0, err
	}
	if len(newLots) > 0 {
		if err := s.ensureOutputLots(ctx, tx, txq, ledger, o, lo.locked[o.VariantID], prep, p.ID, outs, newLots); err != nil {
			return 0, err
		}
	}
	// 7a for the by-products (manufacturing phase 2 M121): every output's lot
	// is found or made before any is held.
	made, err := s.prepareByproducts(ctx, tx, txq, ledger, lo, prep, p.ID)
	if err != nil {
		return 0, err
	}
	// 7b. The open quarantining deviations of the order hold every lot it
	// makes or adds to (M80, M86, M99), the by-products' with the main
	// product's.
	var holds []outputLot
	if len(newLots) > 0 {
		holds = append(holds, outs...)
	}
	holds = append(holds, byproductHolds(made)...)
	if err := s.holdNewOutput(ctx, tx, txq, o.ID, holds, who, at); err != nil {
		return 0, err
	}
	tracked := map[int32]bool{}
	for id, e := range lo.locked {
		tracked[id] = isTracked(e)
	}
	consume, refs, err := splitConsumption(o, byID, v.lines, held, tracked)
	if err != nil {
		return 0, err
	}
	// Today and the override are the pool's, read before the transaction
	// (manufacturing phase 2 M62).
	production := contracts.StockProduction{Document: productionSource(p.ID), SiteID: o.SiteID, Consume: consume,
		PostedByUserID: who.id, PostedByDisplay: who.display, Today: prep.today, ExpiredOverride: prep.expiredOverride}
	if v.quantity.Sign() > 0 {
		if len(outs) == 0 {
			production.Produce = []contracts.ProduceLine{{VariantID: o.VariantID, LineID: o.ID, ToLocationID: to, Quantity: v.quantity.FloatString(quantityPlaces)}}
		}
		for _, out := range outs {
			production.Produce = append(production.Produce, contracts.ProduceLine{VariantID: o.VariantID, LineID: o.ID, ToLocationID: to,
				Quantity: out.quantity.FloatString(quantityPlaces), LotID: out.lotID})
		}
	}
	production.Produce = append(production.Produce, byproductLines(o, made)...)
	var posted contracts.StockPosted
	if len(production.Consume) > 0 || len(production.Produce) > 0 {
		// 7c. Produce.
		posted, err = ledger.Produce(ctx, tx, orderSource(o.ID), production)
		if err != nil {
			// The engine may have claimed the movement: the whole
			// transaction rolls back, the production row with it (M2a).
			var r *contracts.StockRefusal
			if errors.As(err, &r) {
				if !last && r.Code == contracts.StockInsufficientStock && splitRefused(r, o, v.lines) {
					return 0, errResplit
				}
				if again := reproposable(r, refs); again != nil && !prep.reproposed {
					return 0, &errRepropose{components: again, refusal: productionRefusal(r, components, refs, made, len(production.Produce)-len(made))}
				}
				return 0, productionRefusal(r, components, refs, made, len(production.Produce)-len(made))
			}
			return 0, fmt.Errorf("manufacturing: post production %d: %w", p.ID, err)
		}
		if err := txq.SetProductionMovement(ctx, store.SetProductionMovementParams{ID: p.ID, MovementID: &posted.MovementID}); err != nil {
			return 0, err
		}
	}
	for _, l := range v.lines {
		consumed := new(big.Rat)
		if l.consumed != nil {
			consumed = l.consumed
		}
		if err := txq.InsertProductionLine(ctx, store.InsertProductionLineParams{ProductionID: p.ID, ComponentID: l.componentID,
			Consumed: numeric(consumed, quantityPlaces), Scrapped: numeric(l.scrap, quantityPlaces), FromLocationID: l.from}); err != nil {
			return 0, err
		}
		if consumed.Sign() > 0 || l.scrap.Sign() > 0 {
			if err := txq.AddComponentUse(ctx, store.AddComponentUseParams{ID: l.componentID, Consumed: numeric(consumed, quantityPlaces),
				Scrapped: numeric(l.scrap, quantityPlaces)}); err != nil {
				return 0, err
			}
		}
	}
	lotRows, err := writeLineLots(ctx, txq, p.ID, v.lines, refs, posted, known)
	if err != nil {
		return 0, err
	}
	outputIDs := make([]int64, 0, len(outs))
	for _, out := range outs {
		if err := txq.InsertProductionOutput(ctx, store.InsertProductionOutputParams{ProductionID: p.ID, LotID: out.lotID, LotNumber: out.number,
			Serial: out.serial, Quantity: numeric(out.quantity, quantityPlaces)}); err != nil {
			return 0, err
		}
		outputIDs = append(outputIDs, out.lotID)
	}
	if err := writeByproducts(ctx, txq, p.ID, made); err != nil {
		return 0, err
	}
	if err := syncReserved(ctx, ledger, tx, txq, o.ID); err != nil {
		return 0, err
	}
	if _, err := txq.AddOrderProduction(ctx, store.AddOrderProductionParams{ID: o.ID, Produced: numeric(v.quantity, quantityPlaces),
		OutputScrap: numeric(v.outputScrap, quantityPlaces), Now: at}); err != nil {
		return 0, err
	}
	if err := event(ctx, txq, o.ID, gen.ManufacturingOrderEventKindProductionPosted, who, at, map[string]any{
		"productionId": p.ID, "quantity": text(v.quantity, quantityPlaces), "outputScrap": text(v.outputScrap, quantityPlaces),
		"lines": len(v.lines), "deviationAccepted": in.AcceptDeviation != nil && *in.AcceptDeviation,
		"outputLots": outputIDs, "lotRows": lotRows, "byProducts": len(made),
	}); err != nil {
		return 0, err
	}
	return p.ID, nil
}

// productionRefusal is Inventory's refusal of a production as this module's
// 409 (stockRefusal), each failing line named by its component line and, for
// a lot, the lot (M82): a consume line's index names its component. A produce
// line's index past the consume lines and the main product's lines (mains)
// names a by-product (manufacturing phase 2 M121): its order output, at its
// index in the request's outputs.
func productionRefusal(r *contracts.StockRefusal, components []store.ManufacturingOrderComponent, refs []consumeRef, made []byproductMade,
	mains int) error {
	err := stockRefusal(r, components)
	var ref *refusal
	if !errors.As(err, &ref) || ref.problem.Lines == nil {
		return err
	}
	lines := *ref.problem.Lines
	for i, l := range r.Lines {
		if i >= len(lines) {
			break
		}
		if l.LotID != 0 {
			lot := l.LotID
			lines[i].LotId = &lot
		}
		if l.Index >= len(refs) {
			// A produce line names no component, whatever its line id.
			lines[i].ComponentId = nil
			if k := l.Index - len(refs) - mains; k >= 0 && k < len(made) {
				id := made[k].output.ID
				lines[i].OutputId, lines[i].Index = &id, int32(made[k].index)
			}
			continue
		}
		if lines[i].ComponentId == nil && l.Index >= 0 {
			id := refs[l.Index].componentID
			lines[i].ComponentId = &id
		}
	}
	return err
}

// judgeDeviation is the consumption warning (M22, M22a): a sent consumed
// quantity more than 10 % off its proportional figure, judged only where the
// base is more than 0, is consumption_differs naming every such line.
func judgeDeviation(o store.ManufacturingOrder, byID map[int64]store.ManufacturingOrderComponent, v productionValues) error {
	base := new(big.Rat).Add(v.quantity, v.outputScrap)
	if base.Sign() == 0 {
		return nil
	}
	var deviations []gen.ManufacturingDeviation
	for _, l := range v.lines {
		if l.consumed == nil {
			continue
		}
		c := byID[l.componentID]
		expected := proportional(ratOrZero(c.Required), base, ratOrZero(o.Quantity))
		diff := new(big.Rat).Sub(l.consumed, expected)
		diff.Abs(diff)
		limit := new(big.Rat).Mul(expected, big.NewRat(deviationPercent, 100))
		if diff.Cmp(limit) > 0 {
			deviations = append(deviations, gen.ManufacturingDeviation{ComponentId: c.ID, Sku: c.Sku,
				Expected: text(expected, quantityPlaces), Consumed: text(l.consumed, quantityPlaces)})
		}
	}
	if len(deviations) == 0 {
		return nil
	}
	p := problem(http.StatusConflict, codeConsumptionDiffers, "The consumption differs from the order",
		fmt.Sprintf("%d line(s) differ from the proportional consumption by more than 10 %%. Send the same request with acceptDeviation true to post it.", len(deviations)))
	p.Deviations = &deviations
	return refuse(p)
}

// splitConsumption is the production's consume lines (M22a, M82), with what
// each stands for: a line with lots, one consume line per lot row; else its
// consumed and scrapped taken from its fromLocationId, else the order's
// component location; failing both they are split over the locations
// holding the component, its primary first, then by full code. A tracked
// component's line without lots is posted with AutoLot, Inventory taking the
// lots FEFO at each location under its locks. insufficient_stock is answered
// only when the site's total falls short.
func splitConsumption(o store.ManufacturingOrder, byID map[int64]store.ManufacturingOrderComponent, lines []productionLine,
	held map[int32][]contracts.StockLocation, tracked map[int32]bool) ([]contracts.ConsumeLine, []consumeRef, error) {
	left := map[int32][]*big.Rat{}
	for variant, locations := range held {
		for _, l := range locations {
			q, ok := new(big.Rat).SetString(l.Quantity)
			if !ok {
				q = new(big.Rat)
			}
			left[variant] = append(left[variant], q)
		}
	}
	var out []contracts.ConsumeLine
	var refs []consumeRef
	var short []contracts.StockLineRefusal
	for _, l := range lines {
		c := byID[l.componentID]
		if l.lots != nil {
			// The rows add up to the line (M82): judged on the request, and
			// again here for rows a re-proposal wrote.
			want, got := new(big.Rat).Set(l.scrap), new(big.Rat)
			if l.consumed != nil {
				want.Add(want, l.consumed)
			}
			for _, r := range l.lots {
				got.Add(got, r.total())
			}
			if got.Cmp(want) != 0 {
				short = append(short, contracts.StockLineRefusal{Code: contracts.StockInsufficientStock, VariantID: c.VariantID, LineID: c.ID,
					Detail: fmt.Sprintf("The lots cover %s of the %s of %s the line takes.", text(got, quantityPlaces), text(want, quantityPlaces), c.Sku)})
				continue
			}
			for j, r := range l.lots {
				if r.total().Sign() == 0 {
					continue
				}
				out = append(out, contracts.ConsumeLine{VariantID: c.VariantID, LineID: c.ID, FromLocationID: r.locationID,
					Quantity: r.total().FloatString(quantityPlaces), LotID: r.lotID, AllowExpired: r.allowExpired})
				refs = append(refs, consumeRef{componentID: c.ID, row: j, proposed: l.proposed})
			}
			continue
		}
		total := new(big.Rat).Set(l.scrap)
		if l.consumed != nil {
			total.Add(total, l.consumed)
		}
		if total.Sign() == 0 {
			continue
		}
		auto := tracked[c.VariantID]
		ref := consumeRef{componentID: c.ID, row: -1, auto: auto}
		from := l.from
		if from == nil {
			from = o.ComponentLocationID
		}
		if from != nil {
			out = append(out, contracts.ConsumeLine{VariantID: c.VariantID, LineID: c.ID, FromLocationID: *from, Quantity: total.FloatString(quantityPlaces), AutoLot: auto})
			refs = append(refs, ref)
			continue
		}
		rest := new(big.Rat).Set(total)
		var pieces []contracts.ConsumeLine
		for i, loc := range held[c.VariantID] {
			if rest.Sign() == 0 {
				break
			}
			take := new(big.Rat).Set(rest)
			if take.Cmp(left[c.VariantID][i]) > 0 {
				take.Set(left[c.VariantID][i])
			}
			if take.Sign() <= 0 {
				continue
			}
			left[c.VariantID][i].Sub(left[c.VariantID][i], take)
			rest.Sub(rest, take)
			pieces = append(pieces, contracts.ConsumeLine{VariantID: c.VariantID, LineID: c.ID, FromLocationID: loc.LocationID, Quantity: take.FloatString(quantityPlaces), AutoLot: auto})
		}
		if rest.Sign() > 0 {
			short = append(short, contracts.StockLineRefusal{Code: contracts.StockInsufficientStock, VariantID: c.VariantID, LineID: c.ID,
				Detail: fmt.Sprintf("%s of %s is not in stock at the site.", text(rest, quantityPlaces), c.Sku)})
			continue
		}
		out = append(out, pieces...)
		for range pieces {
			refs = append(refs, ref)
		}
	}
	if len(short) > 0 {
		components := make([]store.ManufacturingOrderComponent, 0, len(byID))
		for _, l := range lines {
			components = append(components, byID[l.componentID])
		}
		r := &contracts.StockRefusal{Code: contracts.StockInsufficientStock, VariantID: short[0].VariantID, LineID: short[0].LineID,
			Detail: "The site does not hold what the production consumes.", Lines: short}
		return nil, nil, stockRefusal(r, components)
	}
	return out, refs, nil
}

// splitRefused reports whether a refusal names — in its lines, or as its
// own line when it lists none — a line the post split over the locations
// holding it: one with neither its own fromLocationId nor the order's
// component location.
func splitRefused(r *contracts.StockRefusal, o store.ManufacturingOrder, lines []productionLine) bool {
	if o.ComponentLocationID != nil {
		return false
	}
	split := map[int64]bool{}
	for _, l := range lines {
		if l.from == nil {
			split[l.componentID] = true
		}
	}
	if len(r.Lines) == 0 {
		return split[r.LineID]
	}
	for _, l := range r.Lines {
		if split[l.LineID] {
			return true
		}
	}
	return false
}

// GetManufacturingProduction is one production with its order's id.
func (s *server) GetManufacturingProduction(ctx context.Context, req gen.GetManufacturingProductionRequestObject) (gen.GetManufacturingProductionResponseObject, error) {
	out, err := s.productionAnswer(ctx, req.Id)
	if noRows(err) {
		return gen.GetManufacturingProduction404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	return gen.GetManufacturingProduction200JSONResponse(out), nil
}

// productionAnswer is a production as an answer.
func (s *server) productionAnswer(ctx context.Context, id int64) (gen.ManufacturingProduction, error) {
	q := store.New(s.deps.Pool)
	p, err := q.GetProduction(ctx, id)
	if err != nil {
		return gen.ManufacturingProduction{}, err
	}
	lines, err := q.ListProductionLines(ctx, id)
	if err != nil {
		return gen.ManufacturingProduction{}, err
	}
	made, taken, err := productionLotsOf(ctx, q, []int64{id})
	if err != nil {
		return gen.ManufacturingProduction{}, err
	}
	byRows, err := q.ProductionByproducts(ctx, []int64{id})
	if err != nil {
		return gen.ManufacturingProduction{}, err
	}
	costs := s.has(ctx, permCostsView)
	out := gen.ManufacturingProduction{Id: p.ID, OutputLots: made[id], OrderId: p.OrderID, OrderNumber: orderNumber(p.OrderNumber, p.OrderSuffix),
		ClientRequestId: p.ClientRequestID, Quantity: numericText(p.Quantity, quantityPlaces), OutputScrap: numericText(p.OutputScrap, quantityPlaces),
		ToLocationId: p.ToLocationID, MovementId: p.MovementID, PostedByUserId: p.PostedByUserID, PostedByDisplay: p.PostedByDisplay,
		PostedAt: p.PostedAt, Lines: make([]gen.ManufacturingProductionLine, 0, len(lines)),
		ByProducts: make([]gen.ManufacturingProductionByproduct, 0, len(byRows))}
	for _, r := range byRows {
		out.ByProducts = append(out.ByProducts, byproductAnswer(r, costs))
	}
	for _, l := range lines {
		c := store.ManufacturingOrderComponent{ID: l.ComponentID, VariantID: l.VariantID, Sku: l.Sku, ProductName: l.ProductName, Unit: l.Unit}
		ref := componentRef(nil, c, costs)
		ref.ProductType = goodsType
		out.Lines = append(out.Lines, gen.ManufacturingProductionLine{ComponentId: l.ComponentID, Variant: ref,
			Consumed: numericText(l.Consumed, quantityPlaces), Scrapped: numericText(l.Scrapped, quantityPlaces), FromLocationId: l.FromLocationID,
			Lots: taken[[2]int64{id, l.ComponentID}]})
		if out.Lines[len(out.Lines)-1].Lots == nil {
			out.Lines[len(out.Lines)-1].Lots = []gen.ManufacturingProductionLineLot{}
		}
	}
	if out.OutputLots == nil {
		out.OutputLots = []gen.ManufacturingProductionOutput{}
	}
	return out, nil
}
