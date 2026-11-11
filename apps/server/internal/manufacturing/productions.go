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

	"github.com/jackc/pgx/v5"

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
	ids := make([]int32, 0, len(r.components))
	for _, c := range r.components {
		ids = append(ids, c.VariantID)
	}
	entries, err := s.catalogVariants(ctx, slices.Compact(slices.Sorted(slices.Values(ids))))
	if err != nil {
		return answer(err, draftAnswers())
	}
	costs := s.has(ctx, permCostsView)
	out := gen.GetManufacturingProductionDraft200JSONResponse{Quantity: text(quantity, quantityPlaces), OutputScrap: text(outputScrap, quantityPlaces),
		ToLocationId: o.OutputLocationID, Lines: make([]gen.ManufacturingProductionDraftLine, 0, len(r.components))}
	for _, c := range r.components {
		out.Lines = append(out.Lines, gen.ManufacturingProductionDraftLine{ComponentId: c.ID, Variant: componentRef(entries, c, costs),
			Required: numericText(c.Required, quantityPlaces), Remaining: text(remainingOf(c), quantityPlaces),
			Reserved: numericText(c.Reserved, quantityPlaces), Proposed: text(proportional(ratOrZero(c.Required), base, ratOrZero(o.Quantity)), quantityPlaces)})
	}
	return out, nil
}

// productionLine is one judged line of a production request.
type productionLine struct {
	componentID int64
	consumed    *big.Rat // nil: not sent
	scrap       *big.Rat
	from        *int64
}

// productionValues is a production request, judged.
type productionValues struct {
	quantity    *big.Rat
	outputScrap *big.Rat
	to          *int64
	lines       []productionLine
	hash        string
}

// judgeProduction is the request's shape (M22): every quantity 0 or more
// with at most three decimals, each line once, and something to register.
func (s *server) judgeProduction(ctx context.Context, in gen.ManufacturingProductionInput) (productionValues, fieldErrors) {
	f := s.unknownProperties(ctx, productionInputSchema)
	v := productionValues{to: in.ToLocationId, outputScrap: new(big.Rat)}
	if n := len([]rune(in.ClientRequestId)); n < 1 || n > maxClientRequestIDRunes {
		f.add("clientRequestId", "1–100 characters")
	}
	q, msg := parseDecimal(in.Quantity, quantityPlaces, maxQuantity)
	f.addCoded("quantity", codeQuantityInvalid, msg)
	v.quantity = q
	if in.OutputScrap != nil {
		os, msg := parseDecimal(*in.OutputScrap, quantityPlaces, maxQuantity)
		f.addCoded("outputScrap", codeQuantityInvalid, msg)
		v.outputScrap = os
	}
	if len(in.Lines) > maxProductionLines {
		f.add("lines", "At most 400 lines")
	}
	seen := map[int64]bool{}
	something := false
	for i, l := range in.Lines {
		at := fmt.Sprintf("lines[%d].", i)
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
		v.lines = append(v.lines, pl)
	}
	if f.failed() {
		return v, f
	}
	if v.quantity.Sign() == 0 && v.outputScrap.Sign() == 0 && !something {
		f.addCoded("quantity", codeQuantityInvalid, "0 only with consumption, scrap or output scrap")
		return v, f
	}
	v.hash = productionHash(v)
	return v, f
}

// productionHash is the request's body as one value, quantities as their
// shortest text, so a replay with the same body matches whatever its
// spelling ("2" and "2.000") and acceptDeviation is not part of it.
func productionHash(v productionValues) string {
	type line struct {
		C    int64   `json:"c"`
		Used *string `json:"u"`
		S    string  `json:"s"`
		F    *int64  `json:"f"`
	}
	body := struct {
		Q     string `json:"q"`
		O     string `json:"o"`
		To    *int64 `json:"t"`
		Lines []line `json:"l"`
	}{Q: text(v.quantity, quantityPlaces), O: text(v.outputScrap, quantityPlaces), To: v.to}
	for _, l := range v.lines {
		x := line{C: l.componentID, S: text(l.scrap, quantityPlaces), F: l.from}
		if l.consumed != nil {
			t := text(l.consumed, quantityPlaces)
			x.Used = &t
		}
		body.Lines = append(body.Lines, x)
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

// replay is the answer to a clientRequestId already posted (M22a): the
// first production with 200 when the body is the same, else
// client_request_reused.
func (s *server) replay(ctx context.Context, orderID int64, p store.ManufacturingProduction, hash string) (gen.PostManufacturingProductionResponseObject, error) {
	if p.OrderID != orderID || p.RequestHash != hash {
		return answer(conflict(codeClientRequestReused, "The request id was used for another production",
			"A production with this clientRequestId was registered with another body. Send a new clientRequestId for a new production."), postAnswers())
	}
	out, err := s.productionAnswer(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	return gen.PostManufacturingProduction200JSONResponse(out), nil
}

// PostManufacturingProduction registers a production (M22, M22a): it needs
// manufacturing:shop-floor or manufacturing:orders-manage (M33).
func (s *server) PostManufacturingProduction(ctx context.Context, req gen.PostManufacturingProductionRequestObject) (gen.PostManufacturingProductionResponseObject, error) {
	if !s.has(ctx, permShopFloor) && !s.has(ctx, permOrdersManage) {
		return answer(refuse(problem(http.StatusForbidden, codeShopFloorRequired, "Registering production is not yours to do",
			"Registering a production needs manufacturing:shop-floor or manufacturing:orders-manage.")), postAnswers())
	}
	in := *req.Body
	v, f := s.judgeProduction(ctx, in)
	if f.failed() {
		return answer(refuseInvalid(productionTitle, f), postAnswers())
	}
	pool := store.New(s.deps.Pool)
	if p, err := pool.ProductionByClientRequest(ctx, in.ClientRequestId); err == nil {
		return s.replay(ctx, req.Id, p, v.hash)
	} else if !noRows(err) {
		return nil, err
	}
	pre, err := pool.GetOrder(ctx, req.Id)
	if noRows(err) {
		return gen.PostManufacturingProduction404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	if !isOpen(pre.Status) {
		return answer(orderNotOpen(pre.Status), postAnswers())
	}
	// Inventory is read before the transaction (M26a): the site's locations
	// to judge the request's, and where each component lies to split it.
	locations, err := s.siteLocations(ctx, pre.SiteID)
	if err != nil {
		return answer(err, postAnswers())
	}
	active := map[int64]bool{}
	for _, l := range locations {
		active[l.ID] = l.Active
	}
	var lf fieldErrors
	if v.to != nil && !active[*v.to] {
		lf.addCoded("toLocationId", codeLocationUnknown, "Not an active location of the order's site")
	}
	for i, l := range v.lines {
		if l.from != nil && !active[*l.from] {
			lf.addCoded(fmt.Sprintf("lines[%d].fromLocationId", i), codeLocationUnknown, "Not an active location of the order's site")
		}
	}
	if lf.failed() {
		return answer(refuseInvalid(productionTitle, lf), postAnswers())
	}
	components, err := pool.ListOrderComponents(ctx, pre.ID)
	if err != nil {
		return nil, err
	}
	variants := make([]int32, 0, len(components))
	for _, c := range components {
		variants = append(variants, c.VariantID)
	}
	who, err := s.actorOf(ctx)
	if err != nil {
		return nil, err
	}
	var productionID int64
	for attempt := 1; ; attempt++ {
		var held map[int32][]contracts.StockLocation
		if held, err = s.stockLocations(ctx, variants, pre.SiteID); err != nil {
			return answer(err, postAnswers())
		}
		err = s.withOrder(ctx, req.Id, nil, func(ctx context.Context, tx pgx.Tx, txq *store.Queries, lo *lockedOrder) error {
			id, err := s.postProduction(ctx, tx, txq, lo.order, in, v, held, who, attempt == maxSplitAttempts)
			productionID = id
			return err
		})
		if !errors.Is(err, errResplit) {
			break
		}
	}
	if errors.Is(err, errReplay) || db.IsUniqueViolation(err, productionUniqueIndex) {
		p, rerr := pool.ProductionByClientRequest(ctx, in.ClientRequestId)
		if rerr != nil {
			return nil, rerr
		}
		return s.replay(ctx, req.Id, p, v.hash)
	}
	if err != nil {
		return answer(err, postAnswers())
	}
	out, err := s.productionAnswer(ctx, productionID)
	if err != nil {
		return nil, err
	}
	return gen.PostManufacturingProduction201JSONResponse(out), nil
}

// postProduction is the post on the locked order: the state, the lines, the
// consumption warning, the production row, the split, Produce, and the
// totals. Unless last, Inventory's insufficient_stock on a split line is
// errResplit.
func (s *server) postProduction(ctx context.Context, tx pgx.Tx, txq *store.Queries, o store.ManufacturingOrder, in gen.ManufacturingProductionInput,
	v productionValues, held map[int32][]contracts.StockLocation, who actor, last bool) (int64, error) {
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
			f.add(fmt.Sprintf("lines[%d].componentId", i), "Not a component line of this order")
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
	consume, err := splitConsumption(o, byID, v.lines, held)
	if err != nil {
		return 0, err
	}
	production := contracts.StockProduction{Document: productionSource(p.ID), SiteID: o.SiteID, Consume: consume,
		PostedByUserID: who.id, PostedByDisplay: who.display}
	if v.quantity.Sign() > 0 {
		production.Produce = []contracts.ProduceLine{{VariantID: o.VariantID, LineID: o.ID, ToLocationID: to, Quantity: v.quantity.FloatString(quantityPlaces)}}
	}
	ledger, err := s.ledger()
	if err != nil {
		return 0, err
	}
	if len(production.Consume) > 0 || len(production.Produce) > 0 {
		movement, err := ledger.Produce(ctx, tx, orderSource(o.ID), production)
		if err != nil {
			// The engine may have claimed the movement: the whole
			// transaction rolls back, the production row with it (M2a).
			var r *contracts.StockRefusal
			if errors.As(err, &r) {
				if !last && r.Code == contracts.StockInsufficientStock && splitRefused(r, o, v.lines) {
					return 0, errResplit
				}
				return 0, stockRefusal(r, components)
			}
			return 0, fmt.Errorf("manufacturing: post production %d: %w", p.ID, err)
		}
		if err := txq.SetProductionMovement(ctx, store.SetProductionMovementParams{ID: p.ID, MovementID: &movement}); err != nil {
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
	}); err != nil {
		return 0, err
	}
	return p.ID, notifyOrder(ctx, tx, o.ID)
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

// splitConsumption is the production's consume lines (M22a): each line's
// consumed and scrapped are taken from its fromLocationId, else the order's
// component location; failing both they are split over the locations
// holding the component, its primary first, then by full code.
// insufficient_stock is answered only when the site's total falls short.
func splitConsumption(o store.ManufacturingOrder, byID map[int64]store.ManufacturingOrderComponent, lines []productionLine,
	held map[int32][]contracts.StockLocation) ([]contracts.ConsumeLine, error) {
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
	var short []contracts.StockLineRefusal
	for _, l := range lines {
		total := new(big.Rat).Set(l.scrap)
		if l.consumed != nil {
			total.Add(total, l.consumed)
		}
		if total.Sign() == 0 {
			continue
		}
		c := byID[l.componentID]
		from := l.from
		if from == nil {
			from = o.ComponentLocationID
		}
		if from != nil {
			out = append(out, contracts.ConsumeLine{VariantID: c.VariantID, LineID: c.ID, FromLocationID: *from, Quantity: total.FloatString(quantityPlaces)})
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
			pieces = append(pieces, contracts.ConsumeLine{VariantID: c.VariantID, LineID: c.ID, FromLocationID: loc.LocationID, Quantity: take.FloatString(quantityPlaces)})
		}
		if rest.Sign() > 0 {
			short = append(short, contracts.StockLineRefusal{Code: contracts.StockInsufficientStock, VariantID: c.VariantID, LineID: c.ID,
				Detail: fmt.Sprintf("%s of %s is not in stock at the site.", text(rest, quantityPlaces), c.Sku)})
			continue
		}
		out = append(out, pieces...)
	}
	if len(short) > 0 {
		components := make([]store.ManufacturingOrderComponent, 0, len(byID))
		for _, l := range lines {
			components = append(components, byID[l.componentID])
		}
		r := &contracts.StockRefusal{Code: contracts.StockInsufficientStock, VariantID: short[0].VariantID, LineID: short[0].LineID,
			Detail: "The site does not hold what the production consumes.", Lines: short}
		return nil, stockRefusal(r, components)
	}
	return out, nil
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
	costs := s.has(ctx, permCostsView)
	out := gen.ManufacturingProduction{Id: p.ID, OrderId: p.OrderID, OrderNumber: orderNumber(p.OrderNumber, p.OrderSuffix),
		ClientRequestId: p.ClientRequestID, Quantity: numericText(p.Quantity, quantityPlaces), OutputScrap: numericText(p.OutputScrap, quantityPlaces),
		ToLocationId: p.ToLocationID, MovementId: p.MovementID, PostedByUserId: p.PostedByUserID, PostedByDisplay: p.PostedByDisplay,
		PostedAt: p.PostedAt, Lines: make([]gen.ManufacturingProductionLine, 0, len(lines))}
	for _, l := range lines {
		c := store.ManufacturingOrderComponent{ID: l.ComponentID, VariantID: l.VariantID, Sku: l.Sku, ProductName: l.ProductName, Unit: l.Unit}
		ref := componentRef(nil, c, costs)
		ref.ProductType = goodsType
		out.Lines = append(out.Lines, gen.ManufacturingProductionLine{ComponentId: l.ComponentID, Variant: ref,
			Consumed: numericText(l.Consumed, quantityPlaces), Scrapped: numericText(l.Scrapped, quantityPlaces), FromLocationId: l.FromLocationID})
	}
	return out, nil
}
