package purchasing

// The proposals (P31–P35, A4, A11): what should be bought, from the purchase
// demand's one source — Manufacturing's buy suggestions where Manufacturing
// is enabled, Inventory's low stock where it is not (module.Deps
// .PurchaseDemand) — per variant and site, less what Purchasing's own drafts
// hold of it and what was sent to the site after the source computed it (a
// stored suggestion does not count an order sent since), and rounded once,
// to each active supplier's item of the variant: up to whole purchase
// packagings, then to the item's minimum, then to its multiple. The day to
// order by is the day needed, or today, less the item's lead time.
//
// Create orders makes one draft per supplier and site from the ticked rows,
// or adds their lines to the oldest draft of that supplier and site; a line
// of a variant the draft already has in the same packaging takes the added
// quantity. Its lines are planned as the editor plans them (planLines): the
// item's number, packaging, price — only in the item's own currency and
// packaging (itemPriceFor, A32) — and expected day. Nothing is ordered by
// itself (P34): a person creates the drafts and a person sends them.
//
// Every contract is read before the transaction; inside it the variants are
// locked, then the suppliers, then — this module's own rows — the advisory
// lock that serialises Create orders, the drafts. A row is judged again
// there: one no longer proposed (on a draft or sent since, or no longer in
// the demand) refuses the request with proposal_gone, so a second click
// orders nothing twice. The request is idempotent by its clientRequestId
// (A11), kept on the change-log row it writes on each draft — no table of its
// own: a replay with the same body answers the first result, another body
// is client_request_reused.

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/purchasing/gen"
	"github.com/vantigo-io/vantigo/server/internal/purchasing/store"
)

const (
	// codeProposalGone is Create orders' 409 for a row no longer proposed.
	codeProposalGone = "proposal_gone"
	// codeSupplierItemNotFound is a row's refusal of an item that is not the
	// row variant's.
	codeSupplierItemNotFound = "supplier_item_not_found"
	// actionFromProposals is the change-log action Create orders writes.
	actionFromProposals = "from_proposals"
	// proposalOrdersKey ("PURCPROP") serialises Create orders.
	proposalOrdersKey int64 = 0x5055524350524F50

	invalidProposalsTitle = "The orders were not created"
)

// pairKey is a (variant, site).
type pairKey struct {
	variant int32
	site    int64
}

// demandOf is the purchase demand at the site, or every site, merged to one
// need per (variant, site) — the larger when a source answers two — and its
// source; nil and "" when no module provides one. Before any transaction.
func (s *server) demandOf(ctx context.Context, siteID *int64) (map[pairKey]contracts.PurchaseNeed, string, error) {
	if s.deps.PurchaseDemand == nil {
		return nil, "", nil
	}
	if err := noteContractCall(ctx, "PurchaseDemand.Demand"); err != nil {
		return nil, "", err
	}
	needs, err := s.deps.PurchaseDemand.Demand(ctx, siteID)
	if err != nil {
		return nil, "", fmt.Errorf("purchasing: read the purchase demand: %w", err)
	}
	out := map[pairKey]contracts.PurchaseNeed{}
	source := ""
	for _, n := range needs {
		q, ok := parseDecimal(n.Quantity, quantityPlaces, maxQuantity)
		if !ok || q.Sign() <= 0 {
			s.logWarn(ctx, "purchasing: a need's quantity is not a decimal; it is left out",
				"variant", n.VariantID, "site", n.SiteID, "quantity", n.Quantity)
			continue
		}
		if source == "" {
			source = n.Source
		}
		k := pairKey{n.VariantID, n.SiteID}
		if prev, ok := out[k]; ok && mustRat(prev.Quantity).Cmp(q) >= 0 {
			continue
		}
		n.Quantity = trimmed(q, quantityPlaces)
		out[k] = n
	}
	return out, source, nil
}

// logWarn logs a warning when the module has a logger.
func (s *server) logWarn(ctx context.Context, msg string, args ...any) {
	if s.deps.Logger != nil {
		s.deps.Logger.WarnContext(ctx, msg, args...)
	}
}

// netted is what is subtracted from one need.
type netted struct{ drafts, sent *big.Rat }

// nettingOf reads, on q, what the drafts hold and what was sent after each
// need's computed time.
func nettingOf(ctx context.Context, q *store.Queries, needs map[pairKey]contracts.PurchaseNeed) (map[pairKey]netted, error) {
	out := map[pairKey]netted{}
	if len(needs) == 0 {
		return out, nil
	}
	var variants []int32
	var params store.SentSinceParams
	for k, n := range needs {
		variants = append(variants, k.variant)
		params.VariantIds, params.SiteIds, params.Since = append(params.VariantIds, k.variant), append(params.SiteIds, k.site), append(params.Since, n.ComputedAt)
	}
	get := func(k pairKey) netted {
		v, ok := out[k]
		if !ok {
			v = netted{drafts: new(big.Rat), sent: new(big.Rat)}
		}
		return v
	}
	drafts, err := q.DraftQuantities(ctx, distinctSorted(variants))
	if err != nil {
		return nil, fmt.Errorf("purchasing: read the drafts' quantities: %w", err)
	}
	for _, d := range drafts {
		k := pairKey{d.VariantID, d.SiteID}
		v := get(k)
		v.drafts = mustRat(d.Quantity)
		out[k] = v
	}
	sent, err := q.SentSince(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("purchasing: read what was sent since the demand: %w", err)
	}
	for _, r := range sent {
		k := pairKey{r.VariantID, r.SiteID}
		v := get(k)
		v.sent = mustRat(r.Quantity)
		out[k] = v
	}
	return out, nil
}

// remainingOf is a need less its netting, nil when nothing remains.
func remainingOf(n contracts.PurchaseNeed, net netted) (need, drafts, sent, remaining *big.Rat) {
	need, drafts, sent = mustRat(n.Quantity), new(big.Rat), new(big.Rat)
	if net.drafts != nil {
		drafts = net.drafts
	}
	if net.sent != nil {
		sent = net.sent
	}
	remaining = new(big.Rat).Sub(need, drafts)
	remaining.Sub(remaining, sent)
	if remaining.Sign() <= 0 {
		return need, drafts, sent, nil
	}
	return need, drafts, sent, remaining
}

// itemRounding is a remaining need rounded to one supplier item.
type itemRounding struct {
	packagingID   *int32
	packagingName *string
	factor        *big.Rat
	orderQuantity *big.Rat // in purchase packagings
	quantity      *big.Rat // in the variant's unit
	price         *big.Rat // per the purchase packaging, nil when none
}

// roundToItem rounds remaining (in the variant's unit) once to item (A4): to
// whole purchase packagings when the item names one the variant still has —
// else the unit, to three places — then up to the minimum, then up to a whole
// number of the multiple; the price is the item's in that packaging.
func roundToItem(v contracts.VariantEntry, item store.PurchasingSupplierItem, remaining *big.Rat) itemRounding {
	r := itemRounding{factor: big.NewRat(1, 1)}
	if item.PackagingID != nil {
		if pk := packagingOf(v, *item.PackagingID); pk != nil {
			if f, ok := packagingFactor(v, item.PackagingID); ok {
				r.packagingID, r.packagingName, r.factor = &pk.ID, ptr(pk.Name), f
			}
		}
	}
	count := new(big.Rat).Quo(remaining, r.factor)
	if r.packagingID != nil {
		count = ceilWhole(count)
	} else {
		count = ceilPlaces(count, quantityPlaces)
	}
	if m := ratOf(item.MinimumQuantity); m != nil && count.Cmp(m) < 0 {
		count = m
	}
	if k := ratOf(item.OrderMultiple); k != nil && k.Sign() > 0 {
		count = new(big.Rat).Mul(k, ceilWhole(new(big.Rat).Quo(count, k)))
	}
	r.orderQuantity = roundTo(count, quantityPlaces)
	r.quantity = roundTo(new(big.Rat).Mul(r.orderQuantity, r.factor), quantityPlaces)
	r.price = itemPriceFor(v, ratOf(item.Price), item.PackagingID, r.packagingID)
	return r
}

// ceilWhole is the least whole number at or above v.
func ceilWhole(v *big.Rat) *big.Rat {
	n, rem := new(big.Int).QuoRem(v.Num(), v.Denom(), new(big.Int))
	if rem.Sign() > 0 {
		n.Add(n, big.NewInt(1))
	}
	return new(big.Rat).SetInt(n)
}

// ceilPlaces is v rounded up to places decimals.
func ceilPlaces(v *big.Rat, places int) *big.Rat {
	scale := new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(places)), nil))
	return new(big.Rat).Quo(ceilWhole(new(big.Rat).Mul(v, scale)), scale)
}

// ListPurchasingProposals List the purchase proposals
// (GET /api/v1/purchasing/proposals)
func (s *server) ListPurchasingProposals(ctx context.Context, req gen.ListPurchasingProposalsRequestObject) (gen.ListPurchasingProposalsResponseObject, error) {
	resp := answers[gen.ListPurchasingProposalsResponseObject]{
		unavailable: func(p gen.PurchasingProblem) gen.ListPurchasingProposalsResponseObject {
			return gen.ListPurchasingProposals503ApplicationProblemPlusJSONResponse(p)
		},
	}
	out := gen.PurchasingProposalList{Rows: []gen.PurchasingProposal{}}
	needs, source, err := s.demandOf(ctx, req.Params.SiteId)
	if err != nil {
		return answer(err, resp)
	}
	if source != "" {
		out.Source = &source
	}
	if req.Params.VariantId != nil {
		for k := range needs {
			if k.variant != *req.Params.VariantId {
				delete(needs, k)
			}
		}
	}
	if len(needs) == 0 {
		return gen.ListPurchasingProposals200JSONResponse(out), nil
	}
	var variantIDs []int32
	for k := range needs {
		variantIDs = append(variantIDs, k.variant)
	}
	variants, err := s.catalogVariants(ctx, variantIDs)
	if err != nil {
		return answer(err, resp)
	}
	sites, _, err := s.sites(ctx)
	if err != nil {
		return answer(err, resp)
	}
	d, err := s.daysOf(ctx)
	if err != nil {
		return nil, err
	}
	q := s.store()
	items, err := q.SupplierItemsOfVariants(ctx, distinctSorted(variantIDs))
	if err != nil {
		return nil, fmt.Errorf("purchasing: read the variants' supplier items: %w", err)
	}
	var supplierIDs []int64
	for _, it := range items {
		supplierIDs = append(supplierIDs, it.SupplierID)
	}
	suppliers, err := s.suppliers(ctx, supplierIDs)
	if err != nil {
		return answer(err, resp)
	}
	byVariant := map[int32][]store.PurchasingSupplierItem{}
	for _, it := range items {
		if e, ok := suppliers[it.SupplierID]; ok && e.Active {
			byVariant[it.VariantID] = append(byVariant[it.VariantID], it)
		}
	}
	net, err := nettingOf(ctx, q, needs)
	if err != nil {
		return nil, err
	}
	for k, n := range needs {
		v, ok := variants[k.variant]
		site, siteOK := sites[k.site]
		if !ok || !siteOK || !site.Active {
			continue
		}
		need, drafts, sent, remaining := remainingOf(n, net[k])
		if remaining == nil {
			continue
		}
		row := gen.PurchasingProposal{
			VariantId: v.ID, Sku: v.SKU, ProductName: v.ProductName, Unit: v.Unit, ProductStatus: v.ProductStatus,
			SiteId: site.ID, SiteName: site.Name, Source: n.Source, ComputedAt: n.ComputedAt,
			Need: trimmed(need, quantityPlaces), OnDrafts: trimmed(drafts, quantityPlaces), SentSince: trimmed(sent, quantityPlaces),
			Remaining: trimmed(remaining, quantityPlaces), Options: []gen.PurchasingProposalOption{},
		}
		neededOn := d.today
		if n.NeededOn != nil {
			row.NeededOn = &openapi_types.Date{Time: *n.NeededOn}
			neededOn = *n.NeededOn
		}
		for _, it := range byVariant[v.ID] {
			e := suppliers[it.SupplierID]
			r := roundToItem(v, it, remaining)
			opt := gen.PurchasingProposalOption{
				SupplierItemId: it.ID, SupplierId: it.SupplierID, SupplierName: e.Name, Preferred: it.Preferred,
				SupplierItemNumber: it.SupplierItemNumber, Currency: it.Currency, CurrencyDiffers: it.Currency != supplierCurrency(e),
				LeadTimeDays: it.LeadTimeDays, MinimumQuantity: numericText(it.MinimumQuantity, quantityPlaces),
				Multiple: numericText(it.OrderMultiple, quantityPlaces), PackagingId: r.packagingID, PackagingName: r.packagingName,
				PackagingQuantity: trimmed(r.factor, quantityPlaces), OrderQuantity: trimmed(r.orderQuantity, quantityPlaces),
				Quantity: trimmed(r.quantity, quantityPlaces), OrderBy: openapi_types.Date{Time: neededOn},
			}
			if r.price != nil {
				opt.Price = ptr(priceText(r.price))
			}
			if it.LeadTimeDays != nil {
				opt.OrderBy = openapi_types.Date{Time: neededOn.AddDate(0, 0, -int(*it.LeadTimeDays))}
			}
			row.Options = append(row.Options, opt)
		}
		out.Rows = append(out.Rows, row)
	}
	slices.SortFunc(out.Rows, func(a, b gen.PurchasingProposal) int {
		return cmp.Or(cmp.Compare(sites[a.SiteId].Code, sites[b.SiteId].Code), cmp.Compare(a.Sku, b.Sku), cmp.Compare(a.VariantId, b.VariantId))
	})
	return gen.ListPurchasingProposals200JSONResponse(out), nil
}

// proposalRequestHash is the hash a Create orders request is replayed by:
// its rows, in order, as sent.
func proposalRequestHash(rows []gen.PurchasingProposalOrdersRow) string {
	body, _ := json.Marshal(rows)
	sum := sha256.Sum256(append([]byte("purchasing.proposals.orders\n"), body...))
	return hex.EncodeToString(sum[:])
}

// proposalReplay answers an earlier Create orders by its clientRequestId:
// found false when there is none, client_request_reused when it had another
// body.
func proposalReplay(ctx context.Context, q *store.Queries, requestID, hash string) (gen.PurchasingProposalOrdersResult, bool, error) {
	out := gen.PurchasingProposalOrdersResult{Orders: []gen.PurchasingProposalOrder{}}
	rows, err := q.ProposalRequestChanges(ctx, requestID)
	if err != nil {
		return out, false, fmt.Errorf("purchasing: read the request %s: %w", requestID, err)
	}
	for _, r := range rows {
		var actions []change
		if err := json.Unmarshal(r.Actions, &actions); err != nil {
			return out, false, fmt.Errorf("purchasing: read a change of order %d: %w", r.OrderID, err)
		}
		for _, a := range actions {
			if a.Action != actionFromProposals || deref(a.RequestID) != requestID {
				continue
			}
			if deref(a.RequestHash) != hash {
				return out, false, clientRequestReused()
			}
			var added int32
			_, _ = fmt.Sscan(deref(a.To), &added)
			out.Orders = append(out.Orders, gen.PurchasingProposalOrder{OrderId: r.OrderID, SupplierId: r.SupplierID,
				SupplierName: r.SupplierName, SiteId: r.SiteID, Created: a.Created != nil && *a.Created, LinesAdded: added})
		}
	}
	return out, len(out.Orders) > 0, nil
}

// proposalGroup is the rows of one supplier and site, by their index in the
// request.
type proposalGroup struct {
	supplierID int64
	siteID     int64
	rows       []int
}

// CreatePurchasingProposalOrders Create purchase orders from proposals
// (POST /api/v1/purchasing/proposals/orders)
func (s *server) CreatePurchasingProposalOrders(ctx context.Context, req gen.CreatePurchasingProposalOrdersRequestObject) (gen.CreatePurchasingProposalOrdersResponseObject, error) {
	resp := answers[gen.CreatePurchasingProposalOrdersResponseObject]{
		badRequest: func(p gen.PurchasingProblem) gen.CreatePurchasingProposalOrdersResponseObject {
			return gen.CreatePurchasingProposalOrders400ApplicationProblemPlusJSONResponse(p)
		},
		conflict: func(p gen.PurchasingProblem) gen.CreatePurchasingProposalOrdersResponseObject {
			return gen.CreatePurchasingProposalOrders409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.PurchasingProblem) gen.CreatePurchasingProposalOrdersResponseObject {
			return gen.CreatePurchasingProposalOrders503ApplicationProblemPlusJSONResponse(p)
		},
	}
	b := *req.Body
	requestID, hash := b.ClientRequestId.String(), proposalRequestHash(b.Rows)
	q := s.store()
	if prior, found, err := proposalReplay(ctx, q, requestID, hash); err != nil || found {
		if err != nil {
			return answer(err, resp)
		}
		return gen.CreatePurchasingProposalOrders200JSONResponse(prior), nil
	}
	if len(b.Rows) == 0 || len(b.Rows) > maxLines {
		var f fieldErrors
		f.add("rows", fmt.Sprintf("Name 1 to %d rows", maxLines))
		return gen.CreatePurchasingProposalOrders400ApplicationProblemPlusJSONResponse(invalid(invalidProposalsTitle, f)), nil
	}

	// The rows as written, judged against the items, the sites and the
	// catalog — all read before the transaction.
	var itemIDs []int64
	var siteIDs []int64
	for _, r := range b.Rows {
		itemIDs, siteIDs = append(itemIDs, r.SupplierItemId), append(siteIDs, r.SiteId)
	}
	itemRows, err := q.SupplierItemsByIDs(ctx, distinctSorted(itemIDs))
	if err != nil {
		return nil, fmt.Errorf("purchasing: read the rows' supplier items: %w", err)
	}
	items := map[int64]store.PurchasingSupplierItem{}
	for _, it := range itemRows {
		items[it.ID] = it
	}
	sites, _, err := s.sites(ctx)
	if err != nil {
		return answer(err, resp)
	}
	var failures []lineFailure
	seen := map[pairKey]bool{}
	var groups []*proposalGroup
	groupOf := map[[2]int64]*proposalGroup{}
	var variantIDs []int32
	var supplierIDs []int64
	for i, r := range b.Rows {
		fail := func(code, detail string) {
			failures = append(failures, lineFailure{index: i, code: code, detail: detail})
		}
		it, ok := items[r.SupplierItemId]
		switch site, siteOK := sites[r.SiteId]; {
		case !ok || it.VariantID != r.VariantId:
			fail(codeSupplierItemNotFound, fmt.Sprintf("Variant %d has no supplier item %d", r.VariantId, r.SupplierItemId))
			continue
		case !siteOK:
			fail(codeSiteNotFound, fmt.Sprintf("No site of Inventory's has the id %d", r.SiteId))
			continue
		case !site.Active:
			fail(codeSiteArchived, fmt.Sprintf("%s is archived", site.Name))
			continue
		case seen[pairKey{r.VariantId, r.SiteId}]:
			fail(codeLineInvalid, "A variant is ordered once per site")
			continue
		}
		if _, ok := readQuantity(r.OrderQuantity); !ok {
			fail(codeQuantityInvalid, "A quantity is more than 0, with at most three decimals")
			continue
		}
		seen[pairKey{r.VariantId, r.SiteId}] = true
		key := [2]int64{it.SupplierID, r.SiteId}
		g, ok := groupOf[key]
		if !ok {
			g = &proposalGroup{supplierID: it.SupplierID, siteID: r.SiteId}
			groupOf[key] = g
			groups = append(groups, g)
		}
		g.rows = append(g.rows, i)
		variantIDs, supplierIDs = append(variantIDs, r.VariantId), append(supplierIDs, it.SupplierID)
	}
	if len(failures) > 0 {
		return answer(refuseLines(invalidProposalsTitle, failures), resp)
	}
	var demandSite *int64
	if len(distinctSorted(siteIDs)) == 1 {
		demandSite = &b.Rows[0].SiteId
	}
	needs, _, err := s.demandOf(ctx, demandSite)
	if err != nil {
		return answer(err, resp)
	}
	if s.deps.PurchaseDemand == nil {
		return gen.CreatePurchasingProposalOrders503ApplicationProblemPlusJSONResponse(dependencyUnavailable()), nil
	}
	d, err := s.daysOf(ctx)
	if err != nil {
		return nil, err
	}
	by, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}

	out := gen.PurchasingProposalOrdersResult{Orders: []gen.PurchasingProposalOrder{}}
	err = s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		out.Orders = out.Orders[:0]
		variants, err := s.lockVariants(ctx, tx, variantIDs)
		if err != nil {
			return err
		}
		held, err := s.lockSuppliers(ctx, tx, distinctSorted(supplierIDs)...)
		if err != nil {
			return err
		}
		if err := txq.LockProposalOrders(ctx, proposalOrdersKey); err != nil {
			return fmt.Errorf("purchasing: serialise Create orders: %w", err)
		}
		if prior, found, err := proposalReplay(ctx, txq, requestID, hash); err != nil || found {
			if err == nil {
				out = prior
			}
			return err
		}
		// Each row judged again, on the drafts as they stand now.
		asked := map[pairKey]contracts.PurchaseNeed{}
		for _, r := range b.Rows {
			if n, ok := needs[pairKey{r.VariantId, r.SiteId}]; ok {
				asked[pairKey{r.VariantId, r.SiteId}] = n
			}
		}
		net, err := nettingOf(ctx, txq, asked)
		if err != nil {
			return err
		}
		var gone []lineFailure
		for i, r := range b.Rows {
			k := pairKey{r.VariantId, r.SiteId}
			n, ok := asked[k]
			if !ok {
				gone = append(gone, lineFailure{index: i, code: codeProposalGone, detail: "The row is no longer proposed"})
				continue
			}
			if _, _, _, remaining := remainingOf(n, net[k]); remaining == nil {
				gone = append(gone, lineFailure{index: i, code: codeProposalGone, detail: "The row is on a draft or ordered since"})
			}
		}
		if len(gone) > 0 {
			p := linesProblem(invalidProposalsTitle, gone)
			p.Status, p.Code = ptr(int32(http.StatusConflict)), ptr(codeProposalGone)
			p.Detail = ptr("Some rows are no longer proposed: they are on a draft or ordered since. Load the proposals again.")
			return refuse(p)
		}
		now := s.deps.Clock()
		for _, g := range groups {
			entry, ok := held[g.supplierID]
			var e *contracts.SupplierEntry
			if ok {
				e = &entry
			}
			if err := judgeSupplier(e, g.supplierID); err != nil {
				return err
			}
			o, created, err := s.draftFor(ctx, txq, entry, g.siteID, d.today, by, now)
			if err != nil {
				return err
			}
			added, err := s.addProposalLines(ctx, txq, o, g, b.Rows, variants, now)
			if err != nil {
				return err
			}
			actions := []change{}
			if created {
				actions = append(actions, change{Action: "created"})
			}
			actions = append(actions, change{Action: actionFromProposals, To: ptr(fmt.Sprint(added)), RequestID: &requestID,
				RequestHash: &hash, Created: &created})
			if err := logChange(ctx, txq, o.ID, by, now, actions...); err != nil {
				return err
			}
			out.Orders = append(out.Orders, gen.PurchasingProposalOrder{OrderId: o.ID, SupplierId: g.supplierID,
				SupplierName: o.SupplierName, SiteId: g.siteID, Created: created, LinesAdded: int32(added)})
		}
		return nil
	})
	if err != nil {
		return answer(err, resp)
	}
	return gen.CreatePurchasingProposalOrders200JSONResponse(out), nil
}

// draftFor is the oldest draft of the supplier for the site, locked, or a
// new one with the supplier's currency and language, today's date and the
// caller as buyer (P15); created reports which.
func (s *server) draftFor(ctx context.Context, txq *store.Queries, e contracts.SupplierEntry, siteID int64, today time.Time,
	by actor, now time.Time) (store.PurchasingOrder, bool, error) {
	o, err := txq.LockOldestDraft(ctx, store.LockOldestDraftParams{SupplierID: e.ID, SiteID: siteID})
	if err == nil {
		return o, false, nil
	}
	if !noRows(err) {
		return o, false, fmt.Errorf("purchasing: lock the draft of supplier %d for site %d: %w", e.ID, siteID, err)
	}
	snap := snapshotOf(e)
	o, err = txq.InsertOrder(ctx, store.InsertOrderParams{
		SupplierID: e.ID, SupplierNumber: snap.number, SupplierName: snap.name, SupplierOrganisationNumber: snap.organisationNumber,
		SupplierAddressLine: snap.addressLine, SupplierPostalCode: snap.postalCode, SupplierCity: snap.city,
		SupplierCountry: snap.country, SupplierEmail: snap.email, SiteID: siteID, Currency: supplierCurrency(e),
		Language: languageFor(e), OrderDate: dayCol(today), BuyerID: by.id, BuyerDisplay: by.display,
		NetTotal: numeric(new(big.Rat), amountPlaces), CreatedByUserID: by.id, CreatedByDisplay: by.display, Now: now,
	})
	if err != nil {
		return o, false, fmt.Errorf("purchasing: create the draft of supplier %d for site %d: %w", e.ID, siteID, err)
	}
	return o, true, nil
}

// addProposalLines writes a group's rows onto the draft o — a new line each,
// or the quantity added to a line of the variant the draft has in the item's
// packaging — and sums its total; it answers how many lines it touched. A
// failing row is refused by its index in the request.
func (s *server) addProposalLines(ctx context.Context, txq *store.Queries, o store.PurchasingOrder, g *proposalGroup,
	rows []gen.PurchasingProposalOrdersRow, variants map[int32]contracts.VariantEntry, now time.Time) (int, error) {
	saved, err := txq.OrderLines(ctx, o.ID)
	if err != nil {
		return 0, fmt.Errorf("purchasing: read order %d's lines: %w", o.ID, err)
	}
	var ids []int32
	for _, i := range g.rows {
		ids = append(ids, rows[i].VariantId)
	}
	items, err := itemsOf(ctx, txq, g.supplierID, ids)
	if err != nil {
		return 0, err
	}
	existing := map[int64]store.PurchasingOrderLine{}
	var in []lineInput
	for _, i := range g.rows {
		r := rows[i]
		in = append(in, lineInput{PurchasingLineInput: gen.PurchasingLineInput{Kind: gen.PurchasingLineInputKindVariant,
			VariantId: &r.VariantId, OrderQuantity: r.OrderQuantity}, keys: map[string]bool{}})
		v, ok := variants[r.VariantId]
		item, hasItem := items[r.VariantId]
		if !ok || !hasItem {
			continue
		}
		rounded := roundToItem(v, item, big.NewRat(1, 1))
		for _, l := range saved {
			if l.Kind == kindVariant && l.VariantID != nil && *l.VariantID == r.VariantId && samePackaging(l.PackagingID, rounded.packagingID) {
				sum := new(big.Rat).Add(ratOrZero(l.OrderQuantity), mustRat(r.OrderQuantity))
				existing[l.ID] = l
				in[len(in)-1] = lineInput{PurchasingLineInput: gen.PurchasingLineInput{Id: &l.ID, Kind: gen.PurchasingLineInputKind(l.Kind),
					OrderQuantity: trimmed(sum, quantityPlaces)}, keys: map[string]bool{}}
				break
			}
		}
	}
	planned, failures, err := planLines(lineContext{currency: o.Currency, orderDate: o.OrderDate.Time, variants: variants,
		items: items, existing: existing}, in)
	if err != nil {
		return 0, err
	}
	for j := range failures {
		failures[j].index = g.rows[failures[j].index]
	}
	if len(failures) > 0 {
		return 0, refuseLines(invalidProposalsTitle, failures)
	}
	added := 0
	for _, p := range planned {
		if p.existing == nil {
			added++
		}
	}
	if len(saved)+added > maxLines {
		return 0, refuseLines(invalidProposalsTitle, []lineFailure{{index: g.rows[0], code: codeTooManyLines,
			detail: fmt.Sprintf("The draft would have more than %d lines", maxLines)}})
	}
	position := int32(0)
	for _, l := range saved {
		position = max(position, l.Position)
	}
	for _, p := range planned {
		if p.existing != nil {
			if _, err := txq.UpdateLine(ctx, p.updateParams(p.existing.Position)); err != nil {
				return 0, fmt.Errorf("purchasing: add to line %d: %w", p.existing.ID, err)
			}
			continue
		}
		position++
		if _, err := txq.InsertLine(ctx, p.insertParams(o.ID, position)); err != nil {
			return 0, fmt.Errorf("purchasing: add a line to order %d: %w", o.ID, err)
		}
	}
	if err := txq.RefreshOrderTotal(ctx, store.RefreshOrderTotalParams{ID: o.ID, Now: now}); err != nil {
		return 0, fmt.Errorf("purchasing: sum order %d: %w", o.ID, err)
	}
	return len(planned), nil
}
