package manufacturing

// Unbuilds (manufacturing phase 2 M123): finished goods taken apart, posted as
// one Produce with Unbuild true — the finished item a consume line, the
// components produce lines — which Inventory records as a movement of kind
// unbuild, a reverse edge the trace never expands (M88).
//
// From a done order, for a tracked product the person chooses the finished
// lot, and the unbuild restores only what the productions that put output in
// that lot consumed, proportionally within them: the lots they took, never
// those of a production that made another lot. An untracked product's unbuild
// restores from all the order's productions. A free-standing unbuild restores
// a BoM's lines without scrap, a tracked component into a lot named.
//
// The lock order: LockVariants on the product and every component, the order
// FOR UPDATE (an unbuild from one), the counter, the unbuild's rows,
// EnsureLots for a free-standing unbuild's new lots, then Produce. The cost of
// the order is not recomputed.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/gen"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/store"
)

const (
	unbuildTitle       = "The unbuild was not posted"
	sourceUnbuild      = "unbuild"
	unbuildUniqueIndex = "ux_unbuilds_client_request"
	maxUnbuildNote     = 2000
)

func unbuildSource(id int64) contracts.StockSource {
	return contracts.StockSource{Module: moduleName, Kind: sourceUnbuild, ID: id}
}

// unbuildNumber is an unbuild's display number, UB-<n>.
func unbuildNumber(n int64) string { return "UB-" + strconv.FormatInt(n, 10) }

// unbuildComponent is a free-standing unbuild's lot for a tracked component:
// an existing lot, or a new one by its number.
type unbuildComponent struct {
	lotID int64
	lot   *outputLotValues
}

// unbuildValues is an unbuild request, judged.
type unbuildValues struct {
	clientRequestID string
	orderID, bomID  *int64
	quantity        *big.Rat
	lotID           *int64
	siteID          *int64
	from, to        *int64
	note            string
	components      map[int32]unbuildComponent
	hash            string
}

// judgeUnbuild is the request's shape (M123).
func (s *server) judgeUnbuild(ctx context.Context, in gen.ManufacturingUnbuildInput) (unbuildValues, error) {
	f := s.unknownProperties(ctx, unbuildInputSchema)
	v := unbuildValues{clientRequestID: in.ClientRequestId, orderID: in.OrderId, bomID: in.BomId, lotID: in.LotId, siteID: in.SiteId,
		from: in.FromLocationId, to: in.ToLocationId, components: map[int32]unbuildComponent{}}
	if n := utf8.RuneCountInString(in.ClientRequestId); n < 1 || n > maxClientRequestIDRunes {
		f.add("clientRequestId", "1–100 characters")
	}
	q, msg := parsePositive(in.Quantity, quantityPlaces, maxQuantity)
	f.addCoded("quantity", codeQuantityInvalid, msg)
	v.quantity = q
	if (v.orderID == nil) == (v.bomID == nil) {
		f.add("orderId", "Name the done order to take apart, or a bill of materials")
	}
	if v.orderID != nil && v.siteID != nil {
		f.add("siteId", "An unbuild from an order is at the order's site")
	}
	if in.Note != nil {
		v.note = strings.TrimSpace(*in.Note)
		if utf8.RuneCountInString(v.note) > maxUnbuildNote {
			f.add("note", "At most 2000 characters")
		}
	}
	if in.Components != nil {
		if v.orderID != nil && len(*in.Components) > 0 {
			f.add("components", "An unbuild from an order restores the lots its productions took")
		}
		for i, c := range *in.Components {
			at := fmt.Sprintf("components[%d].", i)
			if _, ok := v.components[c.VariantId]; ok {
				f.add(at+"variantId", "Two entries name this component")
			}
			uc := unbuildComponent{}
			switch {
			case c.LotId != nil && c.Lot != nil:
				f.add(at+"lotId", "Name an existing lot or a new one, not both")
			case c.LotId != nil:
				uc.lotID = *c.LotId
			case c.Lot != nil:
				uc.lot = judgeOutputLot(&f, at, c.Lot)
				if uc.lot != nil && uc.lot.number == "" {
					f.add(at+"lot.number", "A new lot needs its number")
				}
			default:
				f.addCoded(at+"lotId", contracts.StockLotRequired, "Name the lot the component goes back into")
			}
			v.components[c.VariantId] = uc
		}
	}
	if f.failed() {
		return v, refuseInvalid(unbuildTitle, f)
	}
	body, _ := json.Marshal(struct {
		Order, Bom, Lot, Site, From, To *int64
		Q, Note                         string
		Components                      *[]gen.ManufacturingUnbuildComponentInput
	}{v.orderID, v.bomID, v.lotID, v.siteID, v.from, v.to, text(v.quantity, quantityPlaces), v.note, in.Components})
	sum := sha256.Sum256(body)
	v.hash = hex.EncodeToString(sum[:])
	return v, nil
}

// restoreLine is one component an unbuild restores: its variant, the lot it
// goes back into (0 for none) and how much.
type restoreLine struct {
	variantID int32
	sku       string
	name      string
	unit      string
	lotID     int64
	lotNumber string
	quantity  *big.Rat
}

// restoredPart is one exact part of a component's restore: a lot it goes
// back into and how much, before rounding.
type restoredPart struct {
	lotID     int64
	lotNumber string
	amount    *big.Rat
}

// roundRestore rounds a component's parts (§16): the component's figure
// half-up to three decimals, each lot's half-up but the last, which takes the
// remainder, so the lots sum to the figure. A part of 0 is left out.
func roundRestore(parts []restoredPart) []restoredPart {
	total := new(big.Rat)
	for _, p := range parts {
		total.Add(total, p.amount)
	}
	figure := roundPlaces(total, quantityPlaces)
	out := make([]restoredPart, 0, len(parts))
	left := new(big.Rat).Set(figure)
	for i, p := range parts {
		q := roundPlaces(p.amount, quantityPlaces)
		if i == len(parts)-1 || q.Cmp(left) > 0 {
			q = new(big.Rat).Set(left)
		}
		left.Sub(left, q)
		if q.Sign() > 0 {
			out = append(out, restoredPart{lotID: p.lotID, lotNumber: p.lotNumber, amount: q})
		}
		if left.Sign() <= 0 {
			break
		}
	}
	return out
}

// planFromOrder is what an unbuild of quantity from the order restores
// (M123): from the productions that put output in the lot (every production
// with good output for lot 0), each production's consumption × quantity ×
// what it put there ÷ (what they put there together × its own good output),
// per component and the lot it took.
func planFromOrder(ctx context.Context, q *store.Queries, orderID, lotID int64, quantity *big.Rat) ([]restoreLine, error) {
	type prod struct{ quantity, lot *big.Rat }
	prods := map[int64]prod{}
	var ids []int64
	total := new(big.Rat)
	if lotID != 0 {
		rows, err := q.OrderProductionsOfLot(ctx, store.OrderProductionsOfLotParams{OrderID: orderID, LotID: lotID})
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			prods[r.ID] = prod{ratOrZero(r.ProductionQuantity), ratOrZero(r.LotQuantity)}
			ids = append(ids, r.ID)
			total.Add(total, ratOrZero(r.LotQuantity))
		}
	} else {
		rows, err := q.OrderProductionsGood(ctx, orderID)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			prods[r.ID] = prod{ratOrZero(r.ProductionQuantity), ratOrZero(r.LotQuantity)}
			ids = append(ids, r.ID)
			total.Add(total, ratOrZero(r.LotQuantity))
		}
	}
	if len(ids) == 0 || total.Sign() == 0 {
		return nil, nil
	}
	rows, err := q.ProductionConsumption(ctx, ids)
	if err != nil {
		return nil, err
	}
	type key struct {
		variant int32
		lot     int64
	}
	var variants []int32
	info := map[int32]store.ProductionConsumptionRow{}
	parts := map[int32][]restoredPart{}
	at := map[key]int{}
	for _, r := range rows {
		p := prods[r.ProductionID]
		if p.quantity.Sign() == 0 || ratOrZero(r.Consumed).Sign() == 0 {
			continue
		}
		amount := new(big.Rat).Mul(ratOrZero(r.Consumed), quantity)
		amount.Mul(amount, p.lot)
		amount.Quo(amount, new(big.Rat).Mul(total, p.quantity))
		if _, ok := info[r.VariantID]; !ok {
			info[r.VariantID] = r
			variants = append(variants, r.VariantID)
		}
		k := key{r.VariantID, r.LotID}
		if i, ok := at[k]; ok {
			parts[r.VariantID][i].amount.Add(parts[r.VariantID][i].amount, amount)
			continue
		}
		at[k] = len(parts[r.VariantID])
		parts[r.VariantID] = append(parts[r.VariantID], restoredPart{lotID: r.LotID, lotNumber: r.LotNumber, amount: amount})
	}
	var out []restoreLine
	for _, v := range variants {
		r := info[v]
		for _, p := range roundRestore(parts[v]) {
			out = append(out, restoreLine{variantID: v, sku: r.Sku, name: r.ProductName, unit: r.Unit, lotID: p.lotID, lotNumber: p.lotNumber, quantity: p.amount})
		}
	}
	return out, nil
}

// planFromBom is what a free-standing unbuild of quantity restores (M123):
// each BoM line's quantity × quantity ÷ the BoM's output quantity, without
// scrap, half-up to three decimals, one line per variant.
func planFromBom(bom store.ManufacturingBom, lines []store.ManufacturingBomLine, quantity *big.Rat) []restoreLine {
	var variants []int32
	sums := map[int32]*big.Rat{}
	for _, l := range lines {
		if sums[l.VariantID] == nil {
			sums[l.VariantID] = new(big.Rat)
			variants = append(variants, l.VariantID)
		}
		v := new(big.Rat).Mul(ratOrZero(l.Quantity), quantity)
		sums[l.VariantID].Add(sums[l.VariantID], v.Quo(v, ratOrZero(bom.OutputQuantity)))
	}
	out := make([]restoreLine, 0, len(variants))
	for _, v := range variants {
		if q := roundPlaces(sums[v], quantityPlaces); q.Sign() > 0 {
			out = append(out, restoreLine{variantID: v, quantity: q})
		}
	}
	return out
}

// preparedUnbuild is an unbuild judged before its transaction.
type preparedUnbuild struct {
	v          unbuildValues
	variantID  int32
	bom        store.ManufacturingBom
	order      *store.ManufacturingOrder
	siteID     int64
	from, to   int64
	plan       []restoreLine
	components []int32
	lotRefs    map[int64]contracts.LotRef
	today      time.Time
	who        actor
}

// prepareUnbuild reads what an unbuild needs before its transaction (M123,
// rule 24): the order or the BoM, the site and its locations, the restore
// plan, the lots named and today.
func (s *server) prepareUnbuild(ctx context.Context, v unbuildValues) (*preparedUnbuild, error) {
	pool := store.New(s.deps.Pool)
	prep := &preparedUnbuild{v: v}
	var root int64
	if v.orderID != nil {
		o, err := pool.GetOrder(ctx, *v.orderID)
		if noRows(err) {
			return nil, notFound()
		}
		if err != nil {
			return nil, err
		}
		if o.Status != orderDone {
			return nil, conflict(codeOrderNotOpen, "The manufacturing order is not done",
				fmt.Sprintf("The order is %s; an unbuild takes apart what a done order made.", o.Status))
		}
		prep.order, prep.variantID, prep.siteID = &o, o.VariantID, o.SiteID
		if prep.bom, err = pool.GetBom(ctx, o.BomID); err != nil {
			return nil, err
		}
	} else {
		b, err := pool.GetBom(ctx, *v.bomID)
		if noRows(err) {
			return nil, notFound()
		}
		if err != nil {
			return nil, err
		}
		prep.bom, prep.variantID = b, b.VariantID
		site := v.siteID
		sites, err := s.activeSites(ctx)
		if err != nil {
			return nil, err
		}
		operations, err := bomSite(ctx, pool, b.ID)
		if err != nil {
			return nil, err
		}
		switch {
		case site == nil && operations != nil:
			site = operations
		case site == nil && len(sites) > 0:
			site = &sites[0].ID
		}
		if site == nil {
			return nil, refuseField(unbuildTitle, "siteId", codeSiteUnknown, "Not an active Inventory site")
		}
		if _, ok := siteOf(sites, *site); !ok {
			return nil, refuseField(unbuildTitle, "siteId", codeSiteUnknown, "Not an active Inventory site")
		}
		prep.siteID = *site
	}
	locations, err := s.siteLocations(ctx, prep.siteID)
	if err != nil {
		return nil, err
	}
	active := map[int64]bool{}
	for _, l := range locations {
		active[l.ID] = l.Active
		if l.Root {
			root = l.ID
		}
	}
	prep.from, prep.to = root, root
	if o := prep.order; o != nil {
		prep.from = o.OutputLocationID
		if o.ComponentLocationID != nil {
			prep.to = *o.ComponentLocationID
		}
	}
	if v.from != nil {
		prep.from = *v.from
	}
	if v.to != nil {
		prep.to = *v.to
	}
	var lf fieldErrors
	if !active[prep.from] {
		lf.addCoded("fromLocationId", codeLocationUnknown, "Not an active location of the site")
	}
	if !active[prep.to] {
		lf.addCoded("toLocationId", codeLocationUnknown, "Not an active location of the site")
	}
	if lf.failed() {
		return nil, refuseInvalid(unbuildTitle, lf)
	}
	// The plan, from this module's own rows: a done order's productions do
	// not change, so it is read before the transaction; a BoM's lines are
	// read again under the BoM's share lock.
	if o := prep.order; o != nil {
		lot := int64(0)
		if v.lotID != nil {
			lot = *v.lotID
		}
		if prep.plan, err = planFromOrder(ctx, pool, o.ID, lot, v.quantity); err != nil {
			return nil, err
		}
	} else {
		lines, err := pool.LinesOfBoms(ctx, []int64{prep.bom.ID})
		if err != nil {
			return nil, err
		}
		prep.plan = planFromBom(prep.bom, lines, v.quantity)
	}
	for _, l := range prep.plan {
		prep.components = append(prep.components, l.variantID)
	}
	// The lots named by id, for their variant and number.
	var lotIDs []int64
	if v.lotID != nil {
		lotIDs = append(lotIDs, *v.lotID)
	}
	for _, c := range v.components {
		if c.lotID != 0 {
			lotIDs = append(lotIDs, c.lotID)
		}
	}
	prep.lotRefs = map[int64]contracts.LotRef{}
	if len(lotIDs) > 0 {
		refs, err := s.lotRefs(ctx, lotIDs)
		if err != nil {
			return nil, err
		}
		for _, r := range refs {
			prep.lotRefs[r.ID] = r
		}
	}
	if prep.today, err = s.stockDay(ctx); err != nil {
		return nil, err
	}
	if prep.who, err = s.actorOf(ctx); err != nil {
		return nil, err
	}
	return prep, nil
}

// unbuildAnswers are the create's typed refusals.
func unbuildAnswers() answers[gen.CreateManufacturingUnbuildResponseObject] {
	return answers[gen.CreateManufacturingUnbuildResponseObject]{
		badRequest: func(p gen.ManufacturingProblem) gen.CreateManufacturingUnbuildResponseObject {
			return gen.CreateManufacturingUnbuild400ApplicationProblemPlusJSONResponse(p)
		},
		notFound: func() gen.CreateManufacturingUnbuildResponseObject {
			return gen.CreateManufacturingUnbuild404Response{}
		},
		conflict: func(p gen.ManufacturingProblem) gen.CreateManufacturingUnbuildResponseObject {
			return gen.CreateManufacturingUnbuild409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.ManufacturingProblem) gen.CreateManufacturingUnbuildResponseObject {
			return gen.CreateManufacturingUnbuild503ApplicationProblemPlusJSONResponse(p)
		},
	}
}

// unbuildReplay is the unbuild a clientRequestId already posted answers: u
// when the body is the same, else client_request_reused.
func unbuildReplay(u store.ManufacturingUnbuild, hash string) (int64, error) {
	if u.RequestHash != hash {
		return 0, conflict(codeClientRequestReused, "The request id was used for another unbuild",
			"An unbuild with this clientRequestId was posted with another body. Send a new clientRequestId for a new unbuild.")
	}
	return u.ID, nil
}

// errUnbuildReplay is the post's answer when another request with the same
// clientRequestId committed meanwhile.
var errUnbuildReplay = errors.New("manufacturing: the unbuild was posted meanwhile")

// CreateManufacturingUnbuild takes finished goods apart (M123).
func (s *server) CreateManufacturingUnbuild(ctx context.Context, req gen.CreateManufacturingUnbuildRequestObject) (gen.CreateManufacturingUnbuildResponseObject, error) {
	v, err := s.judgeUnbuild(ctx, *req.Body)
	if err != nil {
		return answer(err, unbuildAnswers())
	}
	pool := store.New(s.deps.Pool)
	if u, err := pool.UnbuildByClientRequest(ctx, v.clientRequestID); err == nil {
		id, err := unbuildReplay(u, v.hash)
		if err != nil {
			return answer(err, unbuildAnswers())
		}
		return s.unbuildResponse(ctx, id, true)
	} else if !noRows(err) {
		return nil, err
	}
	prep, err := s.prepareUnbuild(ctx, v)
	if err != nil {
		return answer(err, unbuildAnswers())
	}
	var id int64
	err = s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		var err error
		id, err = s.postUnbuild(ctx, tx, txq, prep)
		return err
	})
	if errors.Is(err, errUnbuildReplay) || db.IsUniqueViolation(err, unbuildUniqueIndex) {
		u, rerr := pool.UnbuildByClientRequest(ctx, v.clientRequestID)
		if rerr != nil {
			return nil, rerr
		}
		id, rerr := unbuildReplay(u, v.hash)
		if rerr != nil {
			return answer(rerr, unbuildAnswers())
		}
		return s.unbuildResponse(ctx, id, true)
	}
	if err != nil {
		return answer(err, unbuildAnswers())
	}
	return s.unbuildResponse(ctx, id, false)
}

// postUnbuild is the unbuild on its transaction, in the lock order the file's
// comment names.
func (s *server) postUnbuild(ctx context.Context, tx pgx.Tx, txq *store.Queries, prep *preparedUnbuild) (int64, error) {
	v := prep.v
	locked, err := s.lockVariants(ctx, tx, lockedVariants(prep.variantID, prep.components))
	if err != nil {
		return 0, err
	}
	plan := prep.plan
	if o := prep.order; o != nil {
		row, err := txq.GetOrderForUpdate(ctx, o.ID)
		if err != nil {
			return 0, err
		}
		if row.Status != orderDone {
			return 0, conflict(codeOrderNotOpen, "The manufacturing order is not done",
				fmt.Sprintf("The order is %s; an unbuild takes apart what a done order made.", row.Status))
		}
	} else {
		// The BoM's lines as they are now, held until the commit.
		if _, err := txq.GetBomForShare(ctx, prep.bom.ID); err != nil {
			return 0, err
		}
		lines, err := txq.LinesOfBoms(ctx, []int64{prep.bom.ID})
		if err != nil {
			return 0, err
		}
		plan = planFromBom(prep.bom, lines, v.quantity)
		for _, l := range plan {
			if _, ok := locked[l.variantID]; !ok {
				return 0, conflict(codeRevisionConflict, "Revision conflict",
					"The bill of materials changed while the unbuild was being posted. Try again.")
			}
		}
	}
	if _, err := txq.UnbuildByClientRequest(ctx, v.clientRequestID); err == nil {
		return 0, errUnbuildReplay
	} else if !noRows(err) {
		return 0, err
	}
	product, ok := locked[prep.variantID]
	if !ok {
		return 0, refuseField(unbuildTitle, "orderId", codeVariantNotFound, "The product is not in the catalog")
	}
	lotNumber, err := s.judgeFinishedLot(ctx, txq, prep, product)
	if err != nil {
		return 0, err
	}
	if len(plan) == 0 {
		return 0, refuseField(unbuildTitle, "quantity", codeQuantityInvalid, "Nothing would be restored: no component was consumed for it")
	}
	// The components' lots: an order's from its productions; a free-standing
	// unbuild's the lots named.
	var newLots []contracts.NewLot
	var newAt []int
	for i := range plan {
		l := &plan[i]
		e, ok := locked[l.variantID]
		if !ok {
			return 0, conflict(codeRevisionConflict, "Revision conflict", "A component changed while the unbuild was being posted. Try again.")
		}
		l.sku, l.name, l.unit = e.SKU, e.ProductName, e.Unit
		if prep.order != nil {
			continue
		}
		c, named := v.components[l.variantID]
		switch {
		case !isTracked(e) && named:
			return 0, refuseField(unbuildTitle, "components", codeLotNotAllowed, fmt.Sprintf("%s is not tracked in lots.", e.SKU))
		case !isTracked(e):
		case !named:
			return 0, refuseField(unbuildTitle, "components", contracts.StockLotRequired, fmt.Sprintf("Name the lot %s goes back into.", e.SKU))
		case e.Tracking == contracts.TrackingSerial:
			return 0, refuseField(unbuildTitle, "components", codeSerialQuantity, fmt.Sprintf("%s is tracked by serial number; restore it by a receipt or an adjustment.", e.SKU))
		case c.lotID != 0:
			ref, ok := prep.lotRefs[c.lotID]
			if !ok || ref.VariantID != l.variantID {
				return 0, refuseField(unbuildTitle, "components", contracts.StockLotUnknown, fmt.Sprintf("Not a lot of %s.", e.SKU))
			}
			l.lotID, l.lotNumber = c.lotID, ref.Number
		default:
			lot := contracts.NewLot{VariantID: l.variantID, Number: c.lot.number, SupplierLot: c.lot.supplierLot, ProductionDate: prep.today,
				Expiry: c.lot.expiry, BestBefore: c.lot.bestBefore}
			if c.lot.production != nil {
				lot.ProductionDate = *c.lot.production
			}
			l.lotNumber = c.lot.number
			newLots = append(newLots, lot)
			newAt = append(newAt, i)
		}
	}
	number, err := txq.NextUnbuildNumber(ctx)
	if err != nil {
		return 0, err
	}
	at := s.deps.Clock()
	params := store.InsertUnbuildParams{Number: number, OrderID: v.orderID, BomID: prep.bom.ID, VariantID: prep.variantID,
		Quantity: numeric(v.quantity, quantityPlaces), SiteID: prep.siteID, FromLocationID: prep.from, ToLocationID: prep.to, Note: v.note,
		PostedByUserID: prep.who.id, PostedByDisplay: prep.who.display, Now: at, ClientRequestID: v.clientRequestID, RequestHash: v.hash}
	if v.lotID != nil {
		params.LotID, params.LotNumber = v.lotID, &lotNumber
	}
	u, err := txq.InsertUnbuild(ctx, params)
	if err != nil {
		return 0, err
	}
	ledger, err := s.ledger()
	if err != nil {
		return 0, err
	}
	src := unbuildSource(u.ID)
	if len(newLots) > 0 {
		ids, err := ledger.EnsureLots(ctx, tx, src, prep.today, newLots)
		var r *contracts.StockRefusal
		if errors.As(err, &r) {
			return 0, refuseField(unbuildTitle, "components", r.Code, r.Detail)
		}
		if err != nil {
			return 0, fmt.Errorf("manufacturing: ensure unbuild %d's lots: %w", u.ID, err)
		}
		for j, i := range newAt {
			plan[i].lotID = ids[j]
		}
	}
	production := contracts.StockProduction{Document: src, SiteID: prep.siteID, PostedByUserID: prep.who.id, PostedByDisplay: prep.who.display,
		Today: prep.today, Unbuild: true,
		Consume: []contracts.ConsumeLine{{VariantID: prep.variantID, LineID: u.ID, FromLocationID: prep.from,
			Quantity: v.quantity.FloatString(quantityPlaces), LotID: lotIDOf(v.lotID)}}}
	for _, l := range plan {
		production.Produce = append(production.Produce, contracts.ProduceLine{VariantID: l.variantID, LineID: u.ID, ToLocationID: prep.to,
			Quantity: l.quantity.FloatString(quantityPlaces), LotID: l.lotID})
	}
	posted, err := ledger.Produce(ctx, tx, src, production)
	if err != nil {
		var r *contracts.StockRefusal
		if errors.As(err, &r) {
			return 0, unbuildRefusal(r)
		}
		return 0, fmt.Errorf("manufacturing: post unbuild %d: %w", u.ID, err)
	}
	if err := txq.SetUnbuildMovement(ctx, store.SetUnbuildMovementParams{ID: u.ID, MovementID: &posted.MovementID}); err != nil {
		return 0, err
	}
	for i, l := range plan {
		p := store.InsertUnbuildLineParams{UnbuildID: u.ID, LineNo: int32(i + 1), VariantID: l.variantID, Sku: l.sku, ProductName: l.name,
			Unit: l.unit, Quantity: numeric(l.quantity, quantityPlaces)}
		if l.lotID != 0 {
			id, n := l.lotID, l.lotNumber
			p.LotID, p.LotNumber = &id, &n
		}
		if err := txq.InsertUnbuildLine(ctx, p); err != nil {
			return 0, err
		}
	}
	if o := prep.order; o != nil {
		payload := map[string]any{"unbuildId": u.ID, "number": unbuildNumber(u.Number), "quantity": text(v.quantity, quantityPlaces)}
		if v.lotID != nil {
			payload["lotId"], payload["lotNumber"] = *v.lotID, lotNumber
		}
		if err := event(ctx, txq, o.ID, gen.ManufacturingOrderEventKindUnbuilt, prep.who, at, payload); err != nil {
			return 0, err
		}
		if err := notifyOrder(ctx, tx, o.ID); err != nil {
			return 0, err
		}
	}
	return u.ID, nil
}

// lotIDOf is a lot id or 0.
func lotIDOf(id *int64) int64 {
	if id == nil {
		return 0
	}
	return *id
}

// judgeFinishedLot judges the finished lot taken apart and the quantity
// against what is left (M123): a tracked product names its lot (lot_required),
// an untracked one none (lot_not_allowed). From an order, the lot is one it
// made (lot_not_of_order) and the quantity at most what the lot received from
// the order's productions less what was unbuilt from it — the order's good
// output for an untracked product — else unbuild_too_much. It answers the
// lot's number.
func (s *server) judgeFinishedLot(ctx context.Context, txq *store.Queries, prep *preparedUnbuild, product contracts.VariantEntry) (string, error) {
	v := prep.v
	switch {
	case isTracked(product) && v.lotID == nil:
		return "", refuseField(unbuildTitle, "lotId", contracts.StockLotRequired, fmt.Sprintf("%s is tracked: choose the lot to take apart.", product.SKU))
	case !isTracked(product) && v.lotID != nil:
		return "", refuseField(unbuildTitle, "lotId", codeLotNotAllowed, fmt.Sprintf("%s is not tracked in lots.", product.SKU))
	}
	o := prep.order
	if o == nil {
		if v.lotID == nil {
			return "", nil
		}
		ref, ok := prep.lotRefs[*v.lotID]
		if !ok || ref.VariantID != prep.variantID {
			return "", refuseField(unbuildTitle, "lotId", contracts.StockLotUnknown, fmt.Sprintf("Not a lot of %s.", product.SKU))
		}
		return ref.Number, nil
	}
	unbuilt, err := txq.UnbuiltOfOrders(ctx, []int64{o.ID})
	if err != nil {
		return "", err
	}
	lot := lotIDOf(v.lotID)
	done := new(big.Rat)
	for _, u := range unbuilt {
		if u.LotID == lot {
			done = ratOrZero(u.Quantity)
		}
	}
	received, number := new(big.Rat), ""
	if lot == 0 {
		fresh, err := txq.GetOrder(ctx, o.ID)
		if err != nil {
			return "", err
		}
		received = ratOrZero(fresh.Produced)
	} else {
		ids, err := txq.ProductionIDsOfOrder(ctx, o.ID)
		if err != nil {
			return "", err
		}
		outputs, err := txq.ProductionOutputs(ctx, ids)
		if err != nil {
			return "", err
		}
		found := false
		for _, out := range outputs {
			if out.LotID == lot {
				found = true
				number = out.LotNumber
				received.Add(received, ratOrZero(out.Quantity))
			}
		}
		if !found {
			return "", refuseField(unbuildTitle, "lotId", codeLotNotOfOrder, "Not a lot this order's productions made")
		}
	}
	left := positive(new(big.Rat).Sub(received, done))
	if v.quantity.Cmp(left) > 0 {
		return "", refuseField(unbuildTitle, "quantity", codeUnbuildTooMuch,
			fmt.Sprintf("At most %s is left to take apart: %s made, %s unbuilt already.", text(left, quantityPlaces),
				text(received, quantityPlaces), text(done, quantityPlaces)))
	}
	return number, nil
}

// unbuildRefusal is Inventory's refusal of an unbuild as this module's 409,
// passed through, each line named by its index: 0 the finished item, then the
// components in the order restored.
func unbuildRefusal(r *contracts.StockRefusal) error {
	p := problem(http.StatusConflict, r.Code, "Inventory refused", r.Detail+" Nothing was changed.")
	if len(r.Lines) > 0 {
		lines := make([]gen.ManufacturingProblemLine, 0, len(r.Lines))
		for _, l := range r.Lines {
			line := gen.ManufacturingProblemLine{Index: int32(max(l.Index, 0)), Code: l.Code, Detail: l.Detail}
			if l.LotID != 0 {
				lot := l.LotID
				line.LotId = &lot
			}
			lines = append(lines, line)
		}
		p.Lines = &lines
	}
	return refuse(p)
}

// unbuildResponse is the create's answer: 200 for a replay, 201 for a post.
func (s *server) unbuildResponse(ctx context.Context, id int64, replayed bool) (gen.CreateManufacturingUnbuildResponseObject, error) {
	out, err := s.unbuildAnswer(ctx, id)
	if err != nil {
		return answer(err, unbuildAnswers())
	}
	if replayed {
		return gen.CreateManufacturingUnbuild200JSONResponse(out), nil
	}
	return gen.CreateManufacturingUnbuild201JSONResponse(out), nil
}

// unbuildRow is an unbuild as its reads answer it, with its order's number.
type unbuildRow struct {
	store.ManufacturingUnbuild
	orderNumber *string
}

// unbuildsAnswer is unbuilds as answered, with their lines, the catalog read
// after the rows.
func (s *server) unbuildsAnswer(ctx context.Context, q *store.Queries, rows []unbuildRow) ([]gen.ManufacturingUnbuild, error) {
	ids := make([]int64, 0, len(rows))
	variants := []int32{}
	for _, r := range rows {
		ids = append(ids, r.ID)
		variants = append(variants, r.VariantID)
	}
	lines, err := q.UnbuildLines(ctx, ids)
	if err != nil {
		return nil, err
	}
	entries, err := s.catalogVariants(ctx, dedupe(variants))
	if err != nil {
		return nil, err
	}
	costs := s.has(ctx, permCostsView)
	byUnbuild := map[int64][]gen.ManufacturingUnbuildLine{}
	for _, l := range lines {
		ref := componentRef(nil, store.ManufacturingOrderComponent{VariantID: l.VariantID, Sku: l.Sku, ProductName: l.ProductName, Unit: l.Unit}, costs)
		ref.ProductType = goodsType
		byUnbuild[l.UnbuildID] = append(byUnbuild[l.UnbuildID], gen.ManufacturingUnbuildLine{Variant: ref, Quantity: numericText(l.Quantity, quantityPlaces),
			LotId: l.LotID, LotNumber: l.LotNumber})
	}
	out := make([]gen.ManufacturingUnbuild, 0, len(rows))
	for _, r := range rows {
		u := gen.ManufacturingUnbuild{Id: r.ID, Number: unbuildNumber(r.Number), OrderId: r.OrderID, OrderNumber: r.orderNumber, BomId: r.BomID,
			Variant: refOf(entries, r.VariantID, costs), Quantity: numericText(r.Quantity, quantityPlaces), LotId: r.LotID, LotNumber: r.LotNumber,
			SiteId: r.SiteID, FromLocationId: r.FromLocationID, ToLocationId: r.ToLocationID, MovementId: r.MovementID, Note: r.Note,
			ClientRequestId: r.ClientRequestID, PostedByUserId: r.PostedByUserID, PostedByDisplay: r.PostedByDisplay, PostedAt: r.PostedAt,
			Lines: byUnbuild[r.ID]}
		if u.Lines == nil {
			u.Lines = []gen.ManufacturingUnbuildLine{}
		}
		out = append(out, u)
	}
	return out, nil
}

// orderNumberOf is an unbuild's order's display number, nil for none.
func orderNumberOf(number *int64, suffix *int32) *string {
	if number == nil {
		return nil
	}
	s := int32(0)
	if suffix != nil {
		s = *suffix
	}
	n := orderNumber(*number, s)
	return &n
}

// unbuildAnswer is one unbuild as answered.
func (s *server) unbuildAnswer(ctx context.Context, id int64) (gen.ManufacturingUnbuild, error) {
	q := store.New(s.deps.Pool)
	r, err := q.GetUnbuild(ctx, id)
	if noRows(err) {
		return gen.ManufacturingUnbuild{}, notFound()
	}
	if err != nil {
		return gen.ManufacturingUnbuild{}, err
	}
	row := unbuildRow{ManufacturingUnbuild: store.ManufacturingUnbuild{ID: r.ID, Number: r.Number, OrderID: r.OrderID, BomID: r.BomID,
		VariantID: r.VariantID, Quantity: r.Quantity, LotID: r.LotID, LotNumber: r.LotNumber, SiteID: r.SiteID, FromLocationID: r.FromLocationID,
		ToLocationID: r.ToLocationID, MovementID: r.MovementID, Note: r.Note, PostedByUserID: r.PostedByUserID, PostedByDisplay: r.PostedByDisplay,
		PostedAt: r.PostedAt, ClientRequestID: r.ClientRequestID, RequestHash: r.RequestHash}, orderNumber: orderNumberOf(r.OrderNumber, r.OrderSuffix)}
	out, err := s.unbuildsAnswer(ctx, q, []unbuildRow{row})
	if err != nil {
		return gen.ManufacturingUnbuild{}, err
	}
	return out[0], nil
}

// GetManufacturingUnbuild is one unbuild with what it restored.
func (s *server) GetManufacturingUnbuild(ctx context.Context, req gen.GetManufacturingUnbuildRequestObject) (gen.GetManufacturingUnbuildResponseObject, error) {
	out, err := s.unbuildAnswer(ctx, req.Id)
	if err != nil {
		return answer(err, answers[gen.GetManufacturingUnbuildResponseObject]{
			notFound: func() gen.GetManufacturingUnbuildResponseObject { return gen.GetManufacturingUnbuild404Response{} },
			unavailable: func(p gen.ManufacturingProblem) gen.GetManufacturingUnbuildResponseObject {
				return gen.GetManufacturingUnbuild503ApplicationProblemPlusJSONResponse(p)
			},
		})
	}
	return gen.GetManufacturingUnbuild200JSONResponse(out), nil
}

// ListManufacturingUnbuilds is a page of unbuilds, newest first.
func (s *server) ListManufacturingUnbuilds(ctx context.Context, req gen.ListManufacturingUnbuildsRequestObject) (gen.ListManufacturingUnbuildsResponseObject, error) {
	reply := answers[gen.ListManufacturingUnbuildsResponseObject]{
		badRequest: func(p gen.ManufacturingProblem) gen.ListManufacturingUnbuildsResponseObject {
			return gen.ListManufacturingUnbuilds400ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.ManufacturingProblem) gen.ListManufacturingUnbuildsResponseObject {
			return gen.ListManufacturingUnbuilds503ApplicationProblemPlusJSONResponse(p)
		},
	}
	page, size := int32(1), int32(defaultPageSize)
	if req.Params.Page != nil {
		page = *req.Params.Page
	}
	if req.Params.PageSize != nil {
		size = *req.Params.PageSize
	}
	if page < 1 || page > maxPage || size < 1 || size > maxPageSize {
		return answer(refuseField("The list was not read", "page", "", "page 1–100000, pageSize 1–200"), reply)
	}
	q := store.New(s.deps.Pool)
	rows, err := q.ListUnbuilds(ctx, store.ListUnbuildsParams{OrderID: req.Params.OrderId, PageSize: size, PageOffset: (page - 1) * size})
	if err != nil {
		return nil, err
	}
	out := gen.ListManufacturingUnbuilds200JSONResponse{Page: page, PageSize: size}
	list := make([]unbuildRow, 0, len(rows))
	for _, r := range rows {
		out.Total = r.Total
		list = append(list, unbuildRow{ManufacturingUnbuild: store.ManufacturingUnbuild{ID: r.ID, Number: r.Number, OrderID: r.OrderID, BomID: r.BomID,
			VariantID: r.VariantID, Quantity: r.Quantity, LotID: r.LotID, LotNumber: r.LotNumber, SiteID: r.SiteID, FromLocationID: r.FromLocationID,
			ToLocationID: r.ToLocationID, MovementID: r.MovementID, Note: r.Note, PostedByUserID: r.PostedByUserID, PostedByDisplay: r.PostedByDisplay,
			PostedAt: r.PostedAt, ClientRequestID: r.ClientRequestID, RequestHash: r.RequestHash}, orderNumber: orderNumberOf(r.OrderNumber, r.OrderSuffix)})
	}
	items, err := s.unbuildsAnswer(ctx, q, list)
	if err != nil {
		return answer(err, reply)
	}
	out.Items = items
	return out, nil
}
