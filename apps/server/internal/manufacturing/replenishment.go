package manufacturing

// Replenishment suggestions (manufacturing phase 2 M105–M111): the projected
// stock of each placed variant at a site, the suggestion its placement's rule
// makes of it, the manufacturing.replenishment lease worker that makes them
// nightly and on demand, and the screen's actions.
//
// The projection (M107), per variant with a reorder point at a site:
//
//	projected = usable onHand − reserved + incoming   (Stock.Placements)
//	          + Σ supply orders' remaining             (open, and reorder drafts; MTO left out)
//	          − Σ open orders' and reorder drafts' unreserved component demand
//	          − Σ unreserved open sales demand          (SalesOrderLines.Unreserved, less what MTO still makes for the line)
//
// The worker keeps the lease-worker rules: a run is claimed by one statement
// that moves its lease on, renewed before each site and finished only by the
// claim that ran it, every contract is read on the pool holding no
// connection, and only then one transaction per site replaces the site's
// open suggestions — so it runs on a pool of one. It is no event handler
// (rule 23).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/holidays"
	"github.com/vantigo-io/vantigo/server/internal/live"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/gen"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/store"
)

const (
	// originReorder is an order made from a suggestion (M111).
	originReorder = "reorder"

	// replenishmentWorkerName is what the runner logs the worker as.
	replenishmentWorkerName = "manufacturing.replenishment"
	replenishmentPoll       = 30 * time.Second
	// replenishmentLease is how long a claimed run is the claimer's; a run
	// whose lease ran out is claimed again (a worker that died).
	replenishmentLease = 10 * time.Minute
	// replenishmentMaxAttempts is how often a run is claimed before it fails.
	replenishmentMaxAttempts = 3
	// replenishmentKeep is how long finished runs are kept (M109).
	replenishmentKeep = 90 * 24 * time.Hour
	// nightlyHour is when the nightly run is due, in the business zone.
	nightlyHour = 2
	// runEvery is the on-demand rate (M109): one a minute.
	runEvery = time.Minute
	// runsListed is how many runs the list answers.
	runsListed = 20
	// maxSuggestionItems is the most suggestions one create names.
	maxSuggestionItems = 200

	// runRequestsKey ("MFGRUN01") serialises the on-demand requests;
	// suggestionSiteBase ("MFGSG" and the site id below it) one site's
	// replacement of its suggestions.
	runRequestsKey     int64 = 0x4D464752554E3031
	suggestionSiteBase int64 = 0x4D46475347000000

	// liveSuggestions is a site's suggestions, keyed by the site's id (§17).
	liveSuggestions       = "manufacturing.suggestions"
	eventSuggestionsReady = "suggestions.changed"

	codeSuggestionNotOpen = "suggestion_not_open" // 409
	codeReorderOpen       = "reorder_open"        // 409

	suggestionTitle = "No order was made from the suggestion"
)

// The suggestions' kinds and states (M110), and the runs' (M109).
const (
	kindMake = "make"
	kindBuy  = "buy"

	suggestionOpen = "open"

	runPending = "pending"
	runDone    = "done"
	runFailed  = "failed"
	runManual  = "manual"
)

// suggestionReason is the figures a suggestion was made from, as stored in
// its reason and answered: exact decimal text, "" for an unset maximum or
// multiple.
type suggestionReason struct {
	Projected          string `json:"projected"`
	ReorderPoint       string `json:"reorderPoint"`
	Maximum            string `json:"maximum,omitempty"`
	Multiple           string `json:"multiple,omitempty"`
	OnHand             string `json:"onHand"`
	Reserved           string `json:"reserved"`
	Incoming           string `json:"incoming"`
	InProduction       string `json:"inProduction"`
	NeededByProduction string `json:"neededByProduction"`
	NeededBySales      string `json:"neededBySales"`
}

// projection is M107's terms for one variant at one site.
type projection struct {
	onHand, reserved, incoming       *big.Rat
	inProduction, neededByProduction *big.Rat
	neededBySales                    *big.Rat
	reorderPoint, maximum, multiple  *big.Rat
}

// projected is M107's sum.
func (p projection) projected() *big.Rat {
	v := new(big.Rat).Sub(p.onHand, p.reserved)
	v.Add(v, p.incoming)
	v.Add(v, p.inProduction)
	v.Sub(v, p.neededByProduction)
	return v.Sub(v, p.neededBySales)
}

// suggestionPlan is what the rule (M108) makes of one projection.
type suggestionPlan struct {
	variantID int32
	kind      string
	quantity  *big.Rat
	needBy    time.Time
	bomID     *int64
	reason    suggestionReason
}

// judgeSuggestion is M108 over a projection: nil when the variant is not
// low. A make suggestion names the variant's default active BoM, whose lead
// time sets the need-by unless usable on hand less reserved is already at or
// below the reorder point.
func judgeSuggestion(variantID int32, p projection, bom *store.ManufacturingBom, today time.Time) *suggestionPlan {
	projected := p.projected()
	if projected.Cmp(p.reorderPoint) > 0 {
		return nil
	}
	target := new(big.Rat).Add(p.reorderPoint, big.NewRat(1, 1))
	if p.maximum != nil {
		target = p.maximum
	}
	needed := new(big.Rat).Sub(target, projected)
	// At least 1, as Inventory's rule (judgeLow) has it, whatever the unit:
	// a variant counted in decimals gets the true quantity above 1 only.
	if one := big.NewRat(1, 1); needed.Cmp(one) < 0 {
		needed = one
	}
	if p.multiple != nil && p.multiple.Sign() > 0 {
		needed = new(big.Rat).Mul(p.multiple, new(big.Rat).SetInt(ceilInt(new(big.Rat).Quo(needed, p.multiple))))
	}
	plan := &suggestionPlan{variantID: variantID, kind: kindBuy, quantity: ceilPlaces(needed, quantityPlaces), needBy: today}
	if bom != nil {
		plan.kind = kindMake
		id := bom.ID
		plan.bomID = &id
		if free := new(big.Rat).Sub(p.onHand, p.reserved); free.Cmp(p.reorderPoint) > 0 {
			plan.needBy = nextBusinessDays(today, int(bom.LeadTimeDays))
		}
	}
	plan.reason = suggestionReason{
		Projected: text(projected, quantityPlaces), ReorderPoint: text(p.reorderPoint, quantityPlaces),
		OnHand: text(p.onHand, quantityPlaces), Reserved: text(p.reserved, quantityPlaces), Incoming: text(p.incoming, quantityPlaces),
		InProduction: text(p.inProduction, quantityPlaces), NeededByProduction: text(p.neededByProduction, quantityPlaces),
		NeededBySales: text(p.neededBySales, quantityPlaces),
	}
	if p.maximum != nil {
		plan.reason.Maximum = text(p.maximum, quantityPlaces)
	}
	if p.multiple != nil {
		plan.reason.Multiple = text(p.multiple, quantityPlaces)
	}
	return plan
}

// nextBusinessDays is the civil day n business days after d: each step goes
// on to the nearest later day that is neither a weekend day nor a Norwegian
// public holiday.
func nextBusinessDays(d time.Time, n int) time.Time {
	for range n {
		d = d.AddDate(0, 0, 1)
		for !holidays.BusinessDay(d) {
			d = d.AddDate(0, 0, 1)
		}
	}
	return d
}

// textRat is a contract's decimal text, "" and unreadable as nil.
func textRat(s string) *big.Rat {
	if s == "" {
		return nil
	}
	v, ok := new(big.Rat).SetString(s)
	if !ok {
		return nil
	}
	return v
}

func orZeroRat(v *big.Rat) *big.Rat {
	if v == nil {
		return new(big.Rat)
	}
	return v
}

// sumBy adds q to m[k].
func sumBy[K comparable](m map[K]*big.Rat, k K, q *big.Rat) {
	if q == nil {
		return
	}
	if m[k] == nil {
		m[k] = new(big.Rat)
	}
	m[k].Add(m[k], q)
}

// replenishmentWorker makes the suggestions (M109).
type replenishmentWorker struct{ s *server }

func (*replenishmentWorker) Name() string            { return replenishmentWorkerName }
func (*replenishmentWorker) Interval() time.Duration { return replenishmentPoll }

func (w *replenishmentWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(replenishmentPoll)
	defer ticker.Stop()
	for {
		if _, err := w.RunCycle(ctx); err != nil && ctx.Err() == nil {
			w.s.logger().Error("manufacturing: the replenishment cycle failed", "worker", replenishmentWorkerName, "error", err.Error())
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// RunCycle queues the day's nightly run once 02:00 has passed in the
// business zone, prunes the old runs, and then claims and runs every due
// run, answering how many it finished.
func (w *replenishmentWorker) RunCycle(ctx context.Context) (int, error) {
	s := w.s
	pool := store.New(s.deps.Pool)
	now := s.deps.Clock()
	zone, err := s.businessZone(ctx)
	if err != nil {
		return 0, err
	}
	if local := now.In(zone); local.Hour() >= nightlyHour {
		day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
		if err := pool.InsertNightlyRun(ctx, store.InsertNightlyRunParams{NightlyFor: pgtype.Date{Time: day, Valid: true}, Now: now}); err != nil {
			return 0, fmt.Errorf("manufacturing: queue the nightly replenishment run: %w", err)
		}
	}
	if err := pool.PruneRuns(ctx, now.Add(-replenishmentKeep)); err != nil {
		return 0, fmt.Errorf("manufacturing: prune the replenishment runs: %w", err)
	}
	finished := 0
	for ctx.Err() == nil {
		at := s.deps.Clock()
		run, err := pool.ClaimRun(ctx, store.ClaimRunParams{Now: at, LeaseUntil: at.Add(replenishmentLease)})
		if noRows(err) {
			break
		}
		if err != nil {
			return finished, fmt.Errorf("manufacturing: claim a replenishment run: %w", err)
		}
		if run.Attempts > replenishmentMaxAttempts {
			msg := "The run was claimed too often without finishing."
			if err := w.finish(ctx, run, runFailed, runTally{}, &msg); err != nil {
				return finished, err
			}
			continue
		}
		tally, err := s.runReplenishment(ctx, run)
		if errors.Is(err, errRunLost) {
			s.logger().WarnContext(ctx, "manufacturing: a replenishment run was claimed again before it finished",
				"worker", replenishmentWorkerName, "run", run.ID)
			continue
		}
		if err != nil {
			if ctx.Err() != nil {
				return finished, ctx.Err()
			}
			s.logger().WarnContext(ctx, "manufacturing: a replenishment run failed", "worker", replenishmentWorkerName,
				"run", run.ID, "error", err.Error())
			msg := err.Error()
			if ferr := w.finish(ctx, run, runFailed, tally, &msg); ferr != nil {
				return finished, ferr
			}
			continue
		}
		if err := w.finish(ctx, run, runDone, tally, nil); err != nil {
			return finished, err
		}
		finished++
	}
	return finished, nil
}

// runTally is what a run did.
type runTally struct {
	suggestions, orders int32
	salesCounted        bool
}

// finish records the run's end by the claim that ran it; a run claimed
// again since is the new claim's to finish, and is left as it is.
func (w *replenishmentWorker) finish(ctx context.Context, run store.ManufacturingReplenishmentRun, status string, t runTally, msg *string) error {
	n, err := store.New(w.s.deps.Pool).FinishRun(ctx, store.FinishRunParams{
		ID: run.ID, Attempts: run.Attempts, Status: status, Now: w.s.deps.Clock(), Suggestions: t.suggestions,
		OrdersCreated: t.orders, SalesCounted: t.salesCounted, Error: msg,
	})
	if err != nil {
		return fmt.Errorf("manufacturing: finish replenishment run %d: %w", run.ID, err)
	}
	if n == 0 {
		w.s.logger().WarnContext(ctx, "manufacturing: a replenishment run was claimed again before it finished",
			"worker", replenishmentWorkerName, "run", run.ID)
	}
	return nil
}

// errRunLost is a run another worker claimed after its lease ran out.
var errRunLost = errors.New("manufacturing: the replenishment run was claimed again")

// runReplenishment is one run over its sites: each active site, or the one
// it was asked for. It first supersedes the open suggestions of every site
// no longer active, and renews the run's lease before each site. A site
// that fails is recorded and the others still run; the run then fails,
// naming each failed site, with what the others did tallied.
func (s *server) runReplenishment(ctx context.Context, run store.ManufacturingReplenishmentRun) (runTally, error) {
	t := runTally{salesCounted: s.deps.SalesOrderLines != nil}
	sites, err := s.activeSites(ctx)
	if err != nil {
		return t, err
	}
	today, err := s.stockDay(ctx)
	if err != nil {
		return t, err
	}
	pool := store.New(s.deps.Pool)
	active := make([]int64, 0, len(sites))
	for _, site := range sites {
		active = append(active, site.ID)
	}
	if err := pool.SupersedeSuggestionsNotAt(ctx, active); err != nil {
		return t, fmt.Errorf("manufacturing: supersede the suggestions of inactive sites: %w", err)
	}
	var failed []string
	for _, site := range sites {
		if run.SiteID != nil && *run.SiteID != site.ID {
			continue
		}
		at := s.deps.Clock()
		renewed, err := pool.RenewRunLease(ctx, store.RenewRunLeaseParams{ID: run.ID, Attempts: run.Attempts,
			LeaseUntil: at.Add(replenishmentLease)})
		if err != nil {
			return t, fmt.Errorf("manufacturing: renew the lease of replenishment run %d: %w", run.ID, err)
		}
		if renewed == 0 {
			return t, errRunLost
		}
		n, made, err := s.replenishSite(ctx, run.ID, site.ID, today)
		t.suggestions += n
		t.orders += made
		if err != nil {
			if ctx.Err() != nil {
				return t, ctx.Err()
			}
			s.logger().WarnContext(ctx, "manufacturing: replenishment of a site failed", "worker", replenishmentWorkerName,
				"run", run.ID, "site", site.Code, "error", err.Error())
			failed = append(failed, fmt.Sprintf("site %s: %s", site.Code, err.Error()))
		}
	}
	if len(failed) > 0 {
		return t, errors.New(strings.Join(failed, "; "))
	}
	return t, nil
}

// replenishSite reads the site's placements, Orders' unreserved demand and
// this module's orders on the pool, judges every placement, and replaces
// the site's open suggestions in one transaction; then automatic creation
// makes the orders its rules ask for, one transaction each.
func (s *server) replenishSite(ctx context.Context, runID, siteID int64, today time.Time) (int32, int32, error) {
	placements, err := s.stockPlacements(ctx, siteID)
	if err != nil || len(placements) == 0 {
		if err == nil {
			err = s.clearSuggestions(ctx, siteID)
		}
		return 0, 0, err
	}
	variantIDs := make([]int32, 0, len(placements))
	for _, p := range placements {
		variantIDs = append(variantIDs, p.VariantID)
	}
	pool := store.New(s.deps.Pool)
	mto, err := pool.MtoRemainingByLine(ctx)
	if err != nil {
		return 0, 0, err
	}
	// What make-to-order still makes for a line is left out of supply, so
	// it is taken off the line's demand; what it has produced is on hand
	// and stays the line's demand.
	mtoLeft := make(map[int64]*big.Rat, len(mto))
	for _, r := range mto {
		mtoLeft[r.LineID] = textRat(r.Remaining)
	}
	sales := map[int32]*big.Rat{}
	demand, err := s.unreservedDemand(ctx, variantIDs, siteID)
	if err != nil {
		return 0, 0, err
	}
	for _, d := range demand {
		q := textRat(d.Quantity)
		if left := mtoLeft[d.LineID]; left != nil && q != nil {
			if q = new(big.Rat).Sub(q, left); q.Sign() <= 0 {
				continue
			}
		}
		sumBy(sales, d.VariantID, q)
	}
	supply, components := map[int32]*big.Rat{}, map[int32]*big.Rat{}
	supplyRows, err := pool.SupplyAtSite(ctx, siteID)
	if err != nil {
		return 0, 0, err
	}
	for _, r := range supplyRows {
		sumBy(supply, r.VariantID, textRat(r.Remaining))
	}
	demandRows, err := pool.ComponentDemandAtSite(ctx, siteID)
	if err != nil {
		return 0, 0, err
	}
	for _, r := range demandRows {
		sumBy(components, r.VariantID, textRat(r.Unreserved))
	}
	boms, err := pool.DefaultBomsOfVariants(ctx, variantIDs)
	if err != nil {
		return 0, 0, err
	}
	bomOf := map[int32]store.ManufacturingBom{}
	for _, b := range boms {
		bomOf[b.VariantID] = b
	}
	reorderRows, err := pool.OpenReorderVariantsAt(ctx, siteID)
	if err != nil {
		return 0, 0, err
	}
	reorderOpen := make(map[int32]bool, len(reorderRows))
	for _, v := range reorderRows {
		reorderOpen[v] = true
	}
	var plans []suggestionPlan
	for _, p := range placements {
		proj := projection{
			onHand: orZeroRat(textRat(p.OnHand)), reserved: orZeroRat(textRat(p.Reserved)), incoming: orZeroRat(textRat(p.Incoming)),
			inProduction: orZeroRat(supply[p.VariantID]), neededByProduction: orZeroRat(components[p.VariantID]),
			neededBySales: orZeroRat(sales[p.VariantID]), reorderPoint: orZeroRat(textRat(p.ReorderPoint)),
			maximum: textRat(p.Maximum), multiple: textRat(p.Multiple),
		}
		var bom *store.ManufacturingBom
		if b, ok := bomOf[p.VariantID]; ok {
			bom = &b
		}
		if plan := judgeSuggestion(p.VariantID, proj, bom, today); plan != nil {
			plans = append(plans, *plan)
		}
	}
	ids := make([]int32, 0, len(plans))
	for _, p := range plans {
		ids = append(ids, p.variantID)
	}
	entries, err := s.catalogVariants(ctx, ids)
	if err != nil {
		return 0, 0, err
	}
	plans = slices.DeleteFunc(plans, func(p suggestionPlan) bool { _, ok := entries[p.variantID]; return !ok })
	inserted := map[int32]int64{}
	err = s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		if err := txq.LockSuggestionSite(ctx, suggestionSiteBase+siteID); err != nil {
			return err
		}
		if err := txq.SupersedeOpenSuggestions(ctx, siteID); err != nil {
			return err
		}
		at := s.deps.Clock()
		for _, p := range plans {
			e := entries[p.variantID]
			reason, err := json.Marshal(p.reason)
			if err != nil {
				return err
			}
			id, err := txq.InsertSuggestion(ctx, store.InsertSuggestionParams{
				RunID: runID, SiteID: siteID, VariantID: p.variantID, Sku: e.SKU, ProductName: e.ProductName, Kind: p.kind,
				Quantity: numeric(p.quantity, quantityPlaces), NeedBy: dateValue(p.needBy), Reason: reason, BomID: p.bomID,
				ReorderOpen: reorderOpen[p.variantID], Now: at,
			})
			if err != nil {
				return err
			}
			inserted[p.variantID] = id
		}
		return notifySuggestions(ctx, tx, siteID)
	})
	if err != nil {
		return 0, 0, err
	}
	made, err := s.autoCreate(ctx, siteID, plans, inserted, reorderOpen, today)
	return int32(len(plans)), made, err
}

// clearSuggestions supersedes a site's open suggestions when it has no
// placement left to judge.
func (s *server) clearSuggestions(ctx context.Context, siteID int64) error {
	return s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		if err := txq.LockSuggestionSite(ctx, suggestionSiteBase+siteID); err != nil {
			return err
		}
		if err := txq.SupersedeOpenSuggestions(ctx, siteID); err != nil {
			return err
		}
		return notifySuggestions(ctx, tx, siteID)
	})
}

// autoCreate makes, as the system, the orders the site's rules ask for
// (M111): a make suggestion of a variant whose rule creates automatically,
// never one with an order from an earlier suggestion still open or a draft
// — skipped here as read on the pool, and refused reorder_open by
// orderFromSuggestion when one was made since. A refusal is logged and
// leaves the suggestion open.
func (s *server) autoCreate(ctx context.Context, siteID int64, plans []suggestionPlan, inserted map[int32]int64,
	reorderOpen map[int32]bool, today time.Time) (int32, error) {
	rows, err := store.New(s.deps.Pool).RulesAt(ctx, siteID)
	if err != nil || len(rows) == 0 {
		return 0, err
	}
	rules := make(map[int32]store.ManufacturingReplenishmentRule, len(rows))
	for _, r := range rows {
		rules[r.VariantID] = r
	}
	var made int32
	for _, p := range plans {
		if p.kind != kindMake || reorderOpen[p.variantID] {
			continue
		}
		rule, ok := rules[p.variantID]
		if !ok {
			continue
		}
		_, failure, err := s.orderFromSuggestion(ctx, inserted[p.variantID], nil, rule.AutoConfirm, systemActor, today)
		if err != nil {
			return made, err
		}
		if failure != nil {
			s.logger().WarnContext(ctx, "manufacturing: automatic creation made no order", "worker", replenishmentWorkerName,
				"suggestion", inserted[p.variantID], "code", failure.Code, "detail", failure.Detail)
			continue
		}
		made++
	}
	return made, nil
}

// stockPlacements is Inventory's placements at the site, before any
// transaction (M106).
func (s *server) stockPlacements(ctx context.Context, siteID int64) ([]contracts.StockPlacement, error) {
	if err := noteContractCall(ctx, "Stock.Placements"); err != nil {
		return nil, err
	}
	if s.deps.Stock == nil {
		return nil, errDependencyUnavailable
	}
	out, err := s.deps.Stock.Placements(ctx, siteID)
	if err != nil {
		return nil, fmt.Errorf("manufacturing: read the placements at site %d: %w", siteID, err)
	}
	return out, nil
}

// unreservedDemand is Orders' unreserved sales demand at the site, before
// any transaction (M107a); none without Orders.
func (s *server) unreservedDemand(ctx context.Context, variantIDs []int32, siteID int64) ([]contracts.UnreservedDemand, error) {
	if s.deps.SalesOrderLines == nil || len(variantIDs) == 0 {
		return nil, nil
	}
	if err := noteContractCall(ctx, "SalesOrderLines.Unreserved"); err != nil {
		return nil, err
	}
	out, err := s.deps.SalesOrderLines.Unreserved(ctx, variantIDs, siteID)
	if err != nil {
		return nil, fmt.Errorf("manufacturing: read the unreserved sales demand at site %d: %w", siteID, err)
	}
	return out, nil
}

// notifySuggestions tells the site's suggestions screen it changed, on q.
func notifySuggestions(ctx context.Context, q live.Execer, siteID int64) error {
	key := strconv.FormatInt(siteID, 10)
	return live.Notify(ctx, q, liveSuggestions+":"+key, eventSuggestionsReady, key)
}

// errSuggestionTaken is a suggestion acted on between the read and the lock.
var errSuggestionTaken = errors.New("manufacturing: the suggestion is no longer open")

// orderFromSuggestion makes one order from a make suggestion (M111): origin
// reorder, at the suggestion's site, for quantity (the suggestion's when
// nil), due on its need-by date, from the variant's default active BoM —
// confirmed too when confirm. Judged on the pool first, then in one
// transaction: LockVariants, the suggestion FOR UPDATE, no order from a
// suggestion of the variant still open or a draft at the site (else the
// suggestion is marked reorder_open and the failure is reorder_open), the
// order's insert and its confirm, the suggestion done. Under LockVariants two
// creations of one variant run one after the other, so the second sees the
// first's order. A rule's refusal is the failure answered, nothing written.
func (s *server) orderFromSuggestion(ctx context.Context, id int64, quantity *big.Rat, confirm bool, who actor,
	today time.Time) (*gen.ManufacturingSuggestionMade, *gen.ManufacturingSuggestionFailure, error) {
	fail := func(code, detail string) (*gen.ManufacturingSuggestionMade, *gen.ManufacturingSuggestionFailure, error) {
		return nil, &gen.ManufacturingSuggestionFailure{SuggestionId: id, Code: code, Detail: detail}, nil
	}
	refused := func(err error) (*gen.ManufacturingSuggestionMade, *gen.ManufacturingSuggestionFailure, error) {
		if errors.Is(err, errSuggestionTaken) {
			return fail(codeSuggestionNotOpen, "The suggestion was acted on or replaced by a later run.")
		}
		if status, p, ok := answerOf(err); ok && status != http.StatusServiceUnavailable {
			return fail(deref(p.Code), cmpOr(deref(p.Detail), deref(p.Title)))
		}
		return nil, nil, err
	}
	sg, err := store.New(s.deps.Pool).GetSuggestion(ctx, id)
	if noRows(err) {
		return fail(codeSuggestionNotOpen, "No such suggestion.")
	}
	if err != nil {
		return nil, nil, err
	}
	switch {
	case sg.Status != suggestionOpen:
		return fail(codeSuggestionNotOpen, "The suggestion was acted on or replaced by a later run.")
	case sg.Kind != kindMake:
		return fail(codeBomRequired, "A buy suggestion is bought, not made: there is no bill of materials.")
	}
	b, err := s.orderBom(ctx, sg.VariantID, nil)
	if err != nil {
		return refused(err)
	}
	q := ratOrZero(sg.Quantity)
	if quantity != nil {
		q = quantity
	}
	site := sg.SiteID
	v := orderFields{quantity: q, siteID: &site, dueDate: sg.NeedBy, priority: priorityNormal}
	if _, err := s.judgePlace(ctx, b.ID, &v); err != nil {
		return refused(err)
	}
	var made gen.ManufacturingSuggestionMade
	var reorderOpen bool
	for attempt := 1; ; attempt++ {
		components, err := bomVariants(ctx, store.New(s.deps.Pool), b.ID)
		if err != nil {
			return nil, nil, err
		}
		ids := lockedVariants(sg.VariantID, components)
		err = s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
			reorderOpen = false
			locked, err := s.lockVariants(ctx, tx, ids)
			if err != nil {
				return err
			}
			if e, ok := locked[sg.VariantID]; !ok || e.ProductType != goodsType {
				return refuseField(suggestionTitle, "variantId", codeVariantNotGoods, "The variant is not goods")
			}
			cur, err := txq.LockSuggestion(ctx, id)
			if err != nil {
				return err
			}
			if cur.Status != suggestionOpen {
				return errSuggestionTaken
			}
			open, err := txq.HasOpenReorder(ctx, store.HasOpenReorderParams{VariantID: sg.VariantID, SiteID: sg.SiteID})
			if err != nil {
				return err
			}
			if open {
				reorderOpen = true
				if err := txq.MarkSuggestionReorderOpen(ctx, id); err != nil {
					return err
				}
				return notifySuggestions(ctx, tx, sg.SiteID)
			}
			bom, err := txq.GetBom(ctx, b.ID)
			if err != nil {
				return err
			}
			if bom.Status != statusActive {
				return notActive()
			}
			now, err := bomVariants(ctx, txq, b.ID)
			if err != nil {
				return err
			}
			for _, c := range now {
				if !slices.Contains(ids, c) {
					return errRelock
				}
			}
			o, err := s.insertOrder(ctx, txq, b.ID, sg.VariantID, v, originReorder, nil, nil, nil, nil, who)
			if err != nil {
				return err
			}
			notes := newNotices()
			notes.orders[o.ID] = true
			if confirm {
				if _, err := s.confirmOrder(ctx, tx, txq, o, locked, who, today, notes); err != nil {
					return err
				}
			}
			if err := txq.MarkSuggestionDone(ctx, store.MarkSuggestionDoneParams{
				ID: id, OrderID: &o.ID, ActedByUserID: who.id, ActedByDisplay: &who.display, Now: s.deps.Clock(),
			}); err != nil {
				return err
			}
			made = gen.ManufacturingSuggestionMade{SuggestionId: id, OrderId: o.ID, Number: orderNumber(o.Number, o.Suffix)}
			if err := notes.send(ctx, tx); err != nil {
				return err
			}
			return notifySuggestions(ctx, tx, sg.SiteID)
		})
		if errors.Is(err, errRelock) {
			if attempt < maxLockAttempts {
				continue
			}
			err = relockExhausted()
		}
		if err != nil {
			return refused(err)
		}
		if reorderOpen {
			return fail(codeReorderOpen, "An order from an earlier suggestion of the variant is still open or a draft at the site.")
		}
		return &made, nil, nil
	}
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// suggestionOf is a suggestion as the contract answers it.
func suggestionOf(sg store.ManufacturingSuggestion, entries map[int32]contracts.VariantEntry, costs bool, number *string) (gen.ManufacturingSuggestion, error) {
	var r suggestionReason
	if err := json.Unmarshal(sg.Reason, &r); err != nil {
		return gen.ManufacturingSuggestion{}, fmt.Errorf("manufacturing: suggestion %d's reason: %w", sg.ID, err)
	}
	optional := func(s string) *string {
		if s == "" {
			return nil
		}
		return &s
	}
	ref := refOf(entries, sg.VariantID, costs)
	if ref.Sku == "" {
		ref.Sku, ref.ProductName = sg.Sku, sg.ProductName
	}
	return gen.ManufacturingSuggestion{
		Id: sg.ID, SiteId: sg.SiteID, Variant: ref, Kind: gen.ManufacturingSuggestionKind(sg.Kind),
		Quantity: numericText(sg.Quantity, quantityPlaces), NeedBy: openapi_types.Date{Time: sg.NeedBy.Time},
		Reason: gen.ManufacturingSuggestionReason{
			Projected: r.Projected, ReorderPoint: r.ReorderPoint, Maximum: optional(r.Maximum), Multiple: optional(r.Multiple),
			OnHand: r.OnHand, Reserved: r.Reserved, Incoming: r.Incoming, InProduction: r.InProduction,
			NeededByProduction: r.NeededByProduction, NeededBySales: r.NeededBySales,
		},
		BomId: sg.BomID, Status: gen.ManufacturingSuggestionStatus(sg.Status), ReorderOpen: sg.ReorderOpen,
		OrderId: sg.OrderID, OrderNumber: number, CreatedAt: sg.CreatedAt,
	}, nil
}

// runOf is a run as the contract answers it.
func runOf(r store.ManufacturingReplenishmentRun) gen.ManufacturingReplenishmentRun {
	return gen.ManufacturingReplenishmentRun{
		Id: r.ID, SiteId: r.SiteID, Kind: gen.ManufacturingReplenishmentRunKind(r.Kind),
		Status: gen.ManufacturingReplenishmentRunStatus(r.Status), RequestedAt: r.RequestedAt, StartedAt: r.StartedAt,
		FinishedAt: r.FinishedAt, Suggestions: r.Suggestions, OrdersCreated: r.OrdersCreated, SalesCounted: r.SalesCounted,
		Error: r.Error,
	}
}

// ListManufacturingSuggestions is the open suggestions per site (M111).
func (s *server) ListManufacturingSuggestions(ctx context.Context, req gen.ListManufacturingSuggestionsRequestObject) (gen.ListManufacturingSuggestionsResponseObject, error) {
	sites, err := s.activeSites(ctx)
	if err != nil {
		return answer(err, answers[gen.ListManufacturingSuggestionsResponseObject]{
			unavailable: func(p gen.ManufacturingProblem) gen.ListManufacturingSuggestionsResponseObject {
				return gen.ListManufacturingSuggestions503ApplicationProblemPlusJSONResponse(p)
			},
		})
	}
	pool := store.New(s.deps.Pool)
	rows, err := pool.ListOpenSuggestions(ctx, req.Params.SiteId)
	if err != nil {
		return nil, err
	}
	ids := make([]int32, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.VariantID)
	}
	entries, err := s.catalogVariants(ctx, ids)
	if err != nil {
		return nil, err
	}
	costs := s.has(ctx, permCostsView)
	out := gen.ListManufacturingSuggestions200JSONResponse{Sites: []gen.ManufacturingSuggestionSite{}, SalesCounted: s.deps.SalesOrderLines != nil}
	for _, site := range sites {
		group := gen.ManufacturingSuggestionSite{Site: gen.ManufacturingSite{Id: site.ID, Code: site.Code, Name: site.Name,
			RootLocationId: site.RootLocationID}, Suggestions: []gen.ManufacturingSuggestion{}}
		for _, r := range rows {
			if r.SiteID != site.ID {
				continue
			}
			sg, err := suggestionOf(r, entries, costs, nil)
			if err != nil {
				return nil, err
			}
			group.Suggestions = append(group.Suggestions, sg)
		}
		if len(group.Suggestions) > 0 {
			out.Sites = append(out.Sites, group)
		}
	}
	runs, err := pool.ListRuns(ctx, 1)
	if err != nil {
		return nil, err
	}
	if len(runs) > 0 {
		r := runOf(runs[0])
		out.LastRun = &r
	}
	return out, nil
}

// CreateManufacturingOrdersFromSuggestions makes one order per suggestion
// named, each in its own transaction (M111).
func (s *server) CreateManufacturingOrdersFromSuggestions(ctx context.Context, req gen.CreateManufacturingOrdersFromSuggestionsRequestObject) (gen.CreateManufacturingOrdersFromSuggestionsResponseObject, error) {
	var f fieldErrors
	items := req.Body.Items
	switch {
	case len(items) == 0:
		f.add("items", "Name at least one suggestion")
	case len(items) > maxSuggestionItems:
		f.add("items", fmt.Sprintf("At most %d suggestions", maxSuggestionItems))
	}
	quantities := make([]*big.Rat, len(items))
	seen := map[int64]bool{}
	for i, it := range items {
		if seen[it.SuggestionId] {
			f.add("items", "A suggestion is named twice")
		}
		seen[it.SuggestionId] = true
		if it.Quantity != nil {
			q, msg := parsePositive(*it.Quantity, quantityPlaces, maxQuantity)
			f.addCoded(fmt.Sprintf("items[%d].quantity", i), codeQuantityInvalid, msg)
			quantities[i] = q
		}
	}
	if f.failed() {
		return gen.CreateManufacturingOrdersFromSuggestions400ApplicationProblemPlusJSONResponse(invalid(suggestionTitle, f)), nil
	}
	who, err := s.actorOf(ctx)
	if err != nil {
		return nil, err
	}
	today, err := s.stockDay(ctx)
	if err != nil {
		return nil, err
	}
	out := gen.CreateManufacturingOrdersFromSuggestions200JSONResponse{
		Made: []gen.ManufacturingSuggestionMade{}, Failed: []gen.ManufacturingSuggestionFailure{},
	}
	for i, it := range items {
		made, failure, err := s.orderFromSuggestion(ctx, it.SuggestionId, quantities[i], req.Body.Confirm, who, today)
		if errors.Is(err, errDependencyUnavailable) {
			return gen.CreateManufacturingOrdersFromSuggestions503ApplicationProblemPlusJSONResponse(dependencyUnavailable()), nil
		}
		if err != nil {
			return nil, err
		}
		if failure != nil {
			out.Failed = append(out.Failed, *failure)
			continue
		}
		out.Made = append(out.Made, *made)
	}
	return out, nil
}

// DismissManufacturingSuggestion dismisses an open suggestion (M111).
func (s *server) DismissManufacturingSuggestion(ctx context.Context, req gen.DismissManufacturingSuggestionRequestObject) (gen.DismissManufacturingSuggestionResponseObject, error) {
	who, err := s.actorOf(ctx)
	if err != nil {
		return nil, err
	}
	var out store.ManufacturingSuggestion
	err = s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		cur, err := txq.LockSuggestion(ctx, req.Id)
		if noRows(err) {
			return notFound()
		}
		if err != nil {
			return err
		}
		if cur.Status != suggestionOpen {
			return conflict(codeSuggestionNotOpen, "The suggestion is not open", "It was acted on, dismissed or replaced by a later run.")
		}
		out, err = txq.DismissSuggestion(ctx, store.DismissSuggestionParams{ID: req.Id, ActedByUserID: who.id,
			ActedByDisplay: &who.display, Now: s.deps.Clock()})
		if err != nil {
			return err
		}
		return notifySuggestions(ctx, tx, cur.SiteID)
	})
	if err != nil {
		return answer(err, answers[gen.DismissManufacturingSuggestionResponseObject]{
			notFound: func() gen.DismissManufacturingSuggestionResponseObject {
				return gen.DismissManufacturingSuggestion404Response{}
			},
			conflict: func(p gen.ManufacturingProblem) gen.DismissManufacturingSuggestionResponseObject {
				return gen.DismissManufacturingSuggestion409ApplicationProblemPlusJSONResponse(p)
			},
		})
	}
	entries, err := s.catalogVariants(ctx, []int32{out.VariantID})
	if err != nil {
		return nil, err
	}
	sg, err := suggestionOf(out, entries, s.has(ctx, permCostsView), nil)
	if err != nil {
		return nil, err
	}
	return gen.DismissManufacturingSuggestion200JSONResponse(sg), nil
}

// StartManufacturingReplenishmentRun asks for a run now (M109): at most one
// a minute per site, and one a minute of every site — a run of the same
// site, or of every site, asked for less than a minute ago is answered
// instead.
func (s *server) StartManufacturingReplenishmentRun(ctx context.Context, req gen.StartManufacturingReplenishmentRunRequestObject) (gen.StartManufacturingReplenishmentRunResponseObject, error) {
	var siteID *int64
	if req.Body != nil {
		siteID = req.Body.SiteId
	}
	if siteID != nil {
		sites, err := s.activeSites(ctx)
		if err != nil {
			return answer(err, answers[gen.StartManufacturingReplenishmentRunResponseObject]{
				unavailable: func(p gen.ManufacturingProblem) gen.StartManufacturingReplenishmentRunResponseObject {
					return gen.StartManufacturingReplenishmentRun503ApplicationProblemPlusJSONResponse(p)
				},
			})
		}
		if _, ok := siteOf(sites, *siteID); !ok {
			var f fieldErrors
			f.addCoded("siteId", codeSiteUnknown, "Not an active Inventory site")
			return gen.StartManufacturingReplenishmentRun400ApplicationProblemPlusJSONResponse(invalid("No run was started", f)), nil
		}
	}
	who, err := s.actorOf(ctx)
	if err != nil {
		return nil, err
	}
	var run store.ManufacturingReplenishmentRun
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		if err := txq.LockRunRequests(ctx, runRequestsKey); err != nil {
			return err
		}
		now := s.deps.Clock()
		recent, err := txq.RecentManualRun(ctx, store.RecentManualRunParams{Since: now.Add(-runEvery), SiteID: siteID})
		if err == nil {
			run = recent
			return nil
		}
		if !noRows(err) {
			return err
		}
		run, err = txq.InsertManualRun(ctx, store.InsertManualRunParams{SiteID: siteID, Now: now,
			RequestedByUserID: who.id, RequestedByDisplay: &who.display})
		return err
	})
	if err != nil {
		return nil, err
	}
	return gen.StartManufacturingReplenishmentRun202JSONResponse(runOf(run)), nil
}

// ListManufacturingReplenishmentRuns is the newest runs.
func (s *server) ListManufacturingReplenishmentRuns(ctx context.Context, _ gen.ListManufacturingReplenishmentRunsRequestObject) (gen.ListManufacturingReplenishmentRunsResponseObject, error) {
	rows, err := store.New(s.deps.Pool).ListRuns(ctx, runsListed)
	if err != nil {
		return nil, err
	}
	out := gen.ListManufacturingReplenishmentRuns200JSONResponse{Items: make([]gen.ManufacturingReplenishmentRun, 0, len(rows))}
	for _, r := range rows {
		out.Items = append(out.Items, runOf(r))
	}
	return out, nil
}

// ListManufacturingReplenishmentRules is a variant's rules at every active
// site.
func (s *server) ListManufacturingReplenishmentRules(ctx context.Context, req gen.ListManufacturingReplenishmentRulesRequestObject) (gen.ListManufacturingReplenishmentRulesResponseObject, error) {
	sites, err := s.activeSites(ctx)
	if err != nil {
		return answer(err, answers[gen.ListManufacturingReplenishmentRulesResponseObject]{
			unavailable: func(p gen.ManufacturingProblem) gen.ListManufacturingReplenishmentRulesResponseObject {
				return gen.ListManufacturingReplenishmentRules503ApplicationProblemPlusJSONResponse(p)
			},
		})
	}
	rules, err := store.New(s.deps.Pool).RulesOfVariant(ctx, req.Params.VariantId)
	if err != nil {
		return nil, err
	}
	out := gen.ListManufacturingReplenishmentRules200JSONResponse{Items: make([]gen.ManufacturingReplenishmentRule, 0, len(sites))}
	for _, site := range sites {
		r := gen.ManufacturingReplenishmentRule{VariantId: req.Params.VariantId, SiteId: site.ID}
		for _, x := range rules {
			if x.SiteID == site.ID {
				r.AutoCreate, r.AutoConfirm = x.AutoCreate, x.AutoConfirm
			}
		}
		out.Items = append(out.Items, r)
	}
	return out, nil
}

// PutManufacturingReplenishmentRule sets a variant's automatic creation at a
// site; both off removes the rule (M111).
func (s *server) PutManufacturingReplenishmentRule(ctx context.Context, req gen.PutManufacturingReplenishmentRuleRequestObject) (gen.PutManufacturingReplenishmentRuleResponseObject, error) {
	in := req.Body
	var f fieldErrors
	if in.AutoConfirm && !in.AutoCreate {
		f.add("autoConfirm", "Confirming automatically needs creating automatically")
	}
	if f.failed() {
		return gen.PutManufacturingReplenishmentRule400ApplicationProblemPlusJSONResponse(invalid("The rule was not saved", f)), nil
	}
	sites, err := s.activeSites(ctx)
	if err != nil {
		return answer(err, answers[gen.PutManufacturingReplenishmentRuleResponseObject]{
			unavailable: func(p gen.ManufacturingProblem) gen.PutManufacturingReplenishmentRuleResponseObject {
				return gen.PutManufacturingReplenishmentRule503ApplicationProblemPlusJSONResponse(p)
			},
		})
	}
	if _, ok := siteOf(sites, req.SiteId); !ok {
		f.addCoded("siteId", codeSiteUnknown, "Not an active Inventory site")
		return gen.PutManufacturingReplenishmentRule400ApplicationProblemPlusJSONResponse(invalid("The rule was not saved", f)), nil
	}
	who, err := s.actorOf(ctx)
	if err != nil {
		return nil, err
	}
	q := store.New(s.deps.Pool)
	if !in.AutoCreate {
		err = q.DeleteRule(ctx, store.DeleteRuleParams{VariantID: req.VariantId, SiteID: req.SiteId})
	} else {
		err = q.UpsertRule(ctx, store.UpsertRuleParams{VariantID: req.VariantId, SiteID: req.SiteId, AutoCreate: in.AutoCreate,
			AutoConfirm: in.AutoConfirm, UpdatedByUserID: who.id, UpdatedByDisplay: &who.display, Now: s.deps.Clock()})
	}
	if err != nil {
		return nil, err
	}
	return gen.PutManufacturingReplenishmentRule200JSONResponse{VariantId: req.VariantId, SiteId: req.SiteId,
		AutoCreate: in.AutoCreate, AutoConfirm: in.AutoConfirm}, nil
}
