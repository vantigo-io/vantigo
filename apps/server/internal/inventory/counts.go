package inventory

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
	"github.com/vantigo-io/vantigo/server/internal/inventory/store"
)

// Counts (design I16–I20). A count covers a site — its root's subtree — or
// one location and everything below it. Opening one takes the site row FOR
// UPDATE, so two opens in a site are serial and the overlap check
// (count_scope_overlaps) sees every count still counting; it then freezes one
// line per balance in scope, expecting what the balance holds. Movements are
// not blocked while counting. Lines are counted (count_lines.go) and the
// count posted as one count movement (count_post.go) or cancelled; a write to
// a count no longer counting is count_not_open. A count whose scope location
// has no balances opens with no lines, and its lines are all added: how stock
// is first placed (I20).
//
// A count line is moved while its balance differs from what it expected —
// only while the count is counting; a posted or cancelled count reports none.

// The statuses of a count.
const (
	countCounting  = "counting"
	countPosted    = "posted"
	countCancelled = "cancelled"
)

// countNotOpen is the 409 of a write to a count that is posted or cancelled.
func countNotOpen(c store.InventoryCount) error {
	return refuse(problem(http.StatusConflict, codeCountNotOpen, "Count not open",
		fmt.Sprintf("Count %d is %s; only a count still counting is changed.", c.ID, c.Status)))
}

// lockOpenCount takes the count row FOR UPDATE — the first lock of every
// write to a count once it is open (§4) — and refuses a count no longer
// counting. An unknown count is the 404.
func lockOpenCount(ctx context.Context, txq *store.Queries, id int64) (store.InventoryCount, error) {
	c, err := txq.LockCount(ctx, id)
	if noRows(err) {
		return store.InventoryCount{}, notFound()
	}
	if err != nil {
		return store.InventoryCount{}, fmt.Errorf("inventory: lock count %d: %w", id, err)
	}
	if c.Status != countCounting {
		return store.InventoryCount{}, countNotOpen(c)
	}
	return c, nil
}

// countHeader is a count as the contract answers it, without its lines.
func countHeader(c store.InventoryCount, site store.InventorySite, scope store.InventoryLocation, lineCount, counted, moved int32) gen.InventoryCount {
	out := gen.InventoryCount{
		Id: c.ID, Site: siteRefOf(site), Scope: locationRefOf(scope), Status: gen.InventoryCountStatus(c.Status), Note: c.Note,
		OpenedBy: posterOf(c.OpenedByUserID, c.OpenedByDisplay), OpenedAt: c.OpenedAt,
		LineCount: lineCount, CountedLines: counted, MovedLines: moved, MovementId: c.MovementID, Revision: c.Revision,
	}
	if c.PostedAt != nil {
		out.PostedAt, out.PostedBy = c.PostedAt, ptr(posterOf(c.PostedByUserID, c.PostedByDisplay))
	}
	if c.CancelledAt != nil {
		out.CancelledAt, out.CancelledBy = c.CancelledAt, ptr(posterOf(c.CancelledByUserID, c.CancelledByDisplay))
	}
	if c.Uncounted != nil {
		out.Uncounted = ptr(gen.InventoryUncounted(*c.Uncounted))
	}
	return out
}

// countOf reads one count with its lines as the contract answers it: the
// count, its lines with their balances now, its site and its locations in one
// snapshot, then the lines' variants from the catalog outside it. The lines
// are by full code, then SKU. An unknown id is the 404.
func (s *server) countOf(ctx context.Context, id int64) (gen.InventoryCount, error) {
	var (
		c         store.InventoryCount
		lines     []store.CountLinesRow
		site      store.InventorySite
		locations = map[int64]store.InventoryLocation{}
	)
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		var err error
		c, err = q.GetCount(ctx, id)
		if noRows(err) {
			return notFound()
		}
		if err != nil {
			return fmt.Errorf("inventory: read count %d: %w", id, err)
		}
		if lines, err = q.CountLines(ctx, id); err != nil {
			return fmt.Errorf("inventory: read the lines of count %d: %w", id, err)
		}
		sites, err := q.SitesByIDs(ctx, []int64{c.SiteID})
		if err != nil || len(sites) != 1 {
			return fmt.Errorf("inventory: read the site of count %d: %w", id, err)
		}
		site = sites[0]
		ids := []int64{c.ScopeLocationID}
		for _, l := range lines {
			ids = append(ids, l.LocationID)
		}
		rows, err := q.LocationsByIDs(ctx, distinct(ids))
		if err != nil {
			return fmt.Errorf("inventory: read the locations of count %d: %w", id, err)
		}
		for _, r := range rows {
			locations[r.ID] = r
		}
		return nil
	})
	if err != nil {
		return gen.InventoryCount{}, err
	}
	variantIDs := make([]int32, 0, len(lines))
	for _, l := range lines {
		variantIDs = append(variantIDs, l.VariantID)
	}
	refs, err := s.variantRefs(ctx, variantIDs)
	if err != nil {
		return gen.InventoryCount{}, err
	}
	out := make([]gen.InventoryCountLine, 0, len(lines))
	var counted, moved int32
	for _, l := range lines {
		expected, current := ratFromNumeric(l.Expected), ratFromNumeric(l.Current)
		line := gen.InventoryCountLine{
			Id: l.ID, Variant: refs[l.VariantID], Location: locationRefOf(locations[l.LocationID]),
			Expected: wireQuantity(expected), Current: wireQuantity(current), Added: l.Added,
			Moved:   c.Status == countCounting && expected.Cmp(current) != 0,
			Entered: snapshotOf(l.Entered), CountedAt: l.CountedAt,
		}
		if l.Counted.Valid {
			counted++
			line.Counted = ptr(wireQuantity(ratFromNumeric(l.Counted)))
			line.CountedBy = ptr(posterOf(l.CountedByUserID, l.CountedByDisplay))
		}
		if line.Moved {
			moved++
		}
		out = append(out, line)
	}
	slices.SortStableFunc(out, func(a, b gen.InventoryCountLine) int {
		return cmp.Or(strings.Compare(a.Location.FullCode, b.Location.FullCode), strings.Compare(a.Variant.Sku, b.Variant.Sku),
			cmp.Compare(a.Variant.Id, b.Variant.Id))
	})
	answer := countHeader(c, site, locations[c.ScopeLocationID], int32(len(lines)), counted, moved)
	answer.Lines = &out
	return answer, nil
}

// ListInventoryCounts List counts
// (GET /api/v1/inventory/counts)
//
// The counts newest first, at most 200, by status and site; without their
// lines.
func (s *server) ListInventoryCounts(ctx context.Context, req gen.ListInventoryCountsRequestObject) (gen.ListInventoryCountsResponseObject, error) {
	params := store.ListCountsParams{SiteID: req.Params.SiteId}
	if req.Params.Status != nil {
		params.Status = ptr(string(*req.Params.Status))
	}
	out := gen.InventoryCountList{Counts: []gen.InventoryCount{}}
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		rows, err := q.ListCounts(ctx, params)
		if err != nil {
			return fmt.Errorf("inventory: list counts: %w", err)
		}
		var siteIDs, locationIDs []int64
		for _, r := range rows {
			siteIDs, locationIDs = append(siteIDs, r.SiteID), append(locationIDs, r.ScopeLocationID)
		}
		sites, err := q.SitesByIDs(ctx, distinct(siteIDs))
		if err != nil {
			return fmt.Errorf("inventory: read the sites of counts: %w", err)
		}
		siteByID := make(map[int64]store.InventorySite, len(sites))
		for _, s := range sites {
			siteByID[s.ID] = s
		}
		scopes, err := q.LocationsByIDs(ctx, distinct(locationIDs))
		if err != nil {
			return fmt.Errorf("inventory: read the scopes of counts: %w", err)
		}
		scopeByID := make(map[int64]store.InventoryLocation, len(scopes))
		for _, l := range scopes {
			scopeByID[l.ID] = l
		}
		for _, r := range rows {
			c := store.InventoryCount{
				ID: r.ID, SiteID: r.SiteID, ScopeLocationID: r.ScopeLocationID, Status: r.Status, Note: r.Note,
				OpenedByUserID: r.OpenedByUserID, OpenedByDisplay: r.OpenedByDisplay, OpenedAt: r.OpenedAt,
				PostedByUserID: r.PostedByUserID, PostedByDisplay: r.PostedByDisplay, PostedAt: r.PostedAt,
				CancelledByUserID: r.CancelledByUserID, CancelledByDisplay: r.CancelledByDisplay, CancelledAt: r.CancelledAt,
				Uncounted: r.Uncounted, MovementID: r.MovementID, Revision: r.Revision,
			}
			out.Counts = append(out.Counts, countHeader(c, siteByID[r.SiteID], scopeByID[r.ScopeLocationID], r.LineCount, r.CountedLines, r.MovedLines))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return gen.ListInventoryCounts200JSONResponse(out), nil
}

// GetInventoryCount Get a count
// (GET /api/v1/inventory/counts/{id})
func (s *server) GetInventoryCount(ctx context.Context, req gen.GetInventoryCountRequestObject) (gen.GetInventoryCountResponseObject, error) {
	c, err := s.countOf(ctx, req.Id)
	if status, p, ok := answerOf(err); ok {
		switch status {
		case http.StatusNotFound:
			return gen.GetInventoryCount404Response{}, nil
		case http.StatusServiceUnavailable:
			return gen.GetInventoryCount503ApplicationProblemPlusJSONResponse(p), nil
		}
	}
	if err != nil {
		return nil, err
	}
	return gen.GetInventoryCount200JSONResponse(c), nil
}

// OpenInventoryCount Open a count
// (POST /api/v1/inventory/counts)
//
// Under the site row's lock (§4): the scope — the location given, or the
// site's root — must be an active location of the site; a count still
// counting whose scope is the same location, above it or below it refuses the
// open (count_scope_overlaps); then a line per balance in scope is frozen.
func (s *server) OpenInventoryCount(ctx context.Context, req gen.OpenInventoryCountRequestObject) (gen.OpenInventoryCountResponseObject, error) {
	if req.Body == nil {
		return gen.OpenInventoryCount400ApplicationProblemPlusJSONResponse(problem(http.StatusBadRequest, "", invalidCountTitle, "The request has no body.")), nil
	}
	in := *req.Body
	f := s.unknownProperties(ctx, countInputSchema)
	note := optionalText(&f, "note", in.Note, maxNoteLength)
	if f.failed() {
		return gen.OpenInventoryCount400ApplicationProblemPlusJSONResponse(invalid(invalidCountTitle, f)), nil
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	var id int64
	err = db.RetrySerializable(ctx, postAttempts, func() error {
		return s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
			var err error
			id, err = s.openCount(ctx, txq, who, in.SiteId, in.LocationId, note)
			return err
		})
	})
	if status, p, ok := answerOf(err); ok {
		switch status {
		case http.StatusBadRequest:
			return gen.OpenInventoryCount400ApplicationProblemPlusJSONResponse(p), nil
		case http.StatusConflict:
			return gen.OpenInventoryCount409ApplicationProblemPlusJSONResponse(p), nil
		case http.StatusServiceUnavailable:
			return gen.OpenInventoryCount503ApplicationProblemPlusJSONResponse(p), nil
		}
	}
	if err != nil {
		return nil, err
	}
	c, err := s.countOf(ctx, id)
	if status, p, ok := answerOf(err); ok && status == http.StatusServiceUnavailable {
		return gen.OpenInventoryCount503ApplicationProblemPlusJSONResponse(p), nil
	}
	if err != nil {
		return nil, err
	}
	return gen.OpenInventoryCount201JSONResponse(c), nil
}

// openCount is OpenInventoryCount's transaction.
func (s *server) openCount(ctx context.Context, txq *store.Queries, who actor, siteID int64, locationID *int64, note *string) (int64, error) {
	site, err := txq.LockSite(ctx, siteID)
	if noRows(err) {
		var f fieldErrors
		f.add("siteId", "No site has this id")
		return 0, refuseInvalid(invalidCountTitle, f)
	}
	if err != nil {
		return 0, fmt.Errorf("inventory: lock site %d: %w", siteID, err)
	}
	var scope store.InventoryLocation
	if locationID == nil {
		scope, err = txq.GetRootLocation(ctx, site.ID)
	} else {
		scope, err = txq.GetLocation(ctx, *locationID)
	}
	var f fieldErrors
	switch {
	case noRows(err) || (err == nil && scope.SiteID != site.ID):
		f.addCoded("locationId", codeLocationNotFound, "No location of this site has this id")
	case err != nil:
		return 0, fmt.Errorf("inventory: read the scope of a count: %w", err)
	case scope.Status != string(gen.Active):
		f.addCoded("locationId", codeLocationArchived, fmt.Sprintf("%s is archived and is not counted", scope.FullCode))
	}
	if f.failed() {
		return 0, refuseInvalid(invalidCountTitle, f)
	}
	other, err := txq.OverlappingCount(ctx, store.OverlappingCountParams{SiteID: site.ID, ScopeID: scope.ID, ScopeAncestors: scope.Ancestors})
	switch {
	case err == nil:
		return 0, refuse(problem(http.StatusConflict, codeCountScopeOverlaps, "Count scope overlaps",
			fmt.Sprintf("Count %d of %s is still counting and covers part of %s; post or cancel it first.", other.ID, other.FullCode, scope.FullCode)))
	case !noRows(err):
		return 0, fmt.Errorf("inventory: check the counts overlapping %s: %w", scope.FullCode, err)
	}
	id, err := txq.InsertCount(ctx, store.InsertCountParams{
		SiteID: site.ID, ScopeLocationID: scope.ID, Note: note, OpenedByUserID: who.UserID, OpenedByDisplay: &who.Display,
		OpenedAt: s.deps.Clock(),
	})
	if err != nil {
		return 0, fmt.Errorf("inventory: insert a count of %s: %w", scope.FullCode, err)
	}
	frozen, err := txq.FreezeCountLines(ctx, store.FreezeCountLinesParams{CountID: id, ScopeID: scope.ID})
	if err != nil {
		return 0, fmt.Errorf("inventory: freeze the lines of count %d: %w", id, err)
	}
	return id, s.recordEvent(ctx, txq, who, event{
		EntityKind: entityCount, EntityID: id, SiteID: &site.ID, Kind: eventCountOpened,
		Payload: map[string]any{"scopeLocationId": scope.ID, "scope": scope.FullCode, "lines": frozen},
	})
}

// CancelInventoryCount Cancel a count
// (POST /api/v1/inventory/counts/{id}/cancel)
//
// A count still counting is cancelled under its row's lock; nothing is
// posted, and its lines stay as the record of what was counted.
func (s *server) CancelInventoryCount(ctx context.Context, req gen.CancelInventoryCountRequestObject) (gen.CancelInventoryCountResponseObject, error) {
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		c, err := lockOpenCount(ctx, txq, req.Id)
		if err != nil {
			return err
		}
		if err := txq.CancelCount(ctx, store.CancelCountParams{
			ID: c.ID, CancelledByUserID: who.UserID, CancelledByDisplay: &who.Display, CancelledAt: s.deps.Clock(),
		}); err != nil {
			return fmt.Errorf("inventory: cancel count %d: %w", c.ID, err)
		}
		return s.recordEvent(ctx, txq, who, event{
			EntityKind: entityCount, EntityID: c.ID, SiteID: &c.SiteID, Kind: eventCountCancelled,
			Payload: map[string]any{"scopeLocationId": c.ScopeLocationID},
		})
	})
	if status, p, ok := answerOf(err); ok {
		switch status {
		case http.StatusNotFound:
			return gen.CancelInventoryCount404Response{}, nil
		case http.StatusConflict:
			return gen.CancelInventoryCount409ApplicationProblemPlusJSONResponse(p), nil
		}
	}
	if err != nil {
		return nil, err
	}
	c, err := s.countOf(ctx, req.Id)
	if status, p, ok := answerOf(err); ok && status == http.StatusServiceUnavailable {
		return gen.CancelInventoryCount503ApplicationProblemPlusJSONResponse(p), nil
	}
	if err != nil {
		return nil, err
	}
	return gen.CancelInventoryCount200JSONResponse(c), nil
}
