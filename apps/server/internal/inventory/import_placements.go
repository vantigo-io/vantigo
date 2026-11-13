package inventory

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/fileimport"
	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
	"github.com/vantigo-io/vantigo/server/internal/inventory/store"
)

// The placements import (phase 2 design I61): a CSV of sku, site (code),
// primary_location (full code), reorder_point and maximum, and optionally
// multiple (manufacturing phase 2 M105), read by
// srv/fileimport as the opening-balance import is, at most 5000 rows. Every
// row is resolved before any transaction — the SKUs in one LookupSKUs, the
// sites and locations in one read each — and judged as a PUT would judge
// it, an empty cell clearing its field. A dry run, the default, answers
// each row's outcome. A commit with any failing row writes nothing; one with
// every row ok writes them all, last wins for a variant and site named
// twice, each bumping the placement's revision, a row clearing the primary
// location, the reorder point and the maximum removing the placement, each change a placement_set or
// placement_cleared event. The commit records one placements_imported event
// keyed by the file's SHA-256, and a file already committed answers 200 with
// what its first commit wrote, writing nothing: the import is idempotent by
// the file.

// placementImportColumns are the header's five names, as
// fileimport.HeaderKey reads them; placementImportMultiple is the sixth a
// header may add (M105). A file without it leaves every placement's multiple
// as it is, and a placement it creates has none.
var placementImportColumns = []string{"sku", "site", "primarylocation", "reorderpoint", "maximum"}

const placementImportMultiple = "multiple"

// placementsResolved is a test's hook, run after a commit's rows are
// resolved and before its transaction opens: what a concurrent write may do
// in between.
var placementsResolved atomic.Pointer[func()]

// placementWrite is one ok row as the commit writes it.
type placementWrite struct {
	variantID                       int32
	siteID                          int64
	primaryLocationID               *int64
	reorderPoint, maximum, multiple *big.Rat
	sku, productName                string
}

// clears reports whether the row clears all three fields.
func (w placementWrite) clears() bool {
	return w.primaryLocationID == nil && w.reorderPoint == nil && w.maximum == nil
}

// placementImportProblem is the 400 of a file or a request the import cannot
// read.
func placementImportProblem(field, message string) gen.ImportInventoryPlacements400ApplicationProblemPlusJSONResponse {
	var f fieldErrors
	f.add(field, message)
	return gen.ImportInventoryPlacements400ApplicationProblemPlusJSONResponse(invalid(invalidImportTitle, f))
}

// ImportInventoryPlacements Import placements
// (POST /api/v1/inventory/imports/placements)
func (s *server) ImportInventoryPlacements(ctx context.Context, req gen.ImportInventoryPlacementsRequestObject) (gen.ImportInventoryPlacementsResponseObject, error) {
	if req.Body == nil {
		return placementImportProblem("file", fileimport.EmptyMessage), nil
	}
	data, err := io.ReadAll(io.LimitReader(req.Body, maxImportBytes+1))
	if err != nil {
		return placementImportProblem("file", fmt.Sprintf(fileimport.UnreadableMessage, err)), nil
	}
	if len(data) > maxImportBytes {
		return placementImportProblem("file", fmt.Sprintf("The file is larger than %d MB, which is more than one import takes", maxImportBytes>>20)), nil
	}
	file, refusal := fileimport.Read(data, importLimits)
	if refusal != "" {
		return placementImportProblem("file", refusal), nil
	}
	column := map[string]int{}
	for i, h := range file.Header {
		column[fileimport.HeaderKey(h)] = i
	}
	_, withMultiple := column[placementImportMultiple]
	want := len(placementImportColumns)
	if withMultiple {
		want++
	}
	for _, name := range placementImportColumns {
		if _, ok := column[name]; !ok || len(file.Header) != want {
			return placementImportProblem("file", "The header must name exactly the columns sku, site, primary_location, reorder_point and maximum, and optionally multiple"), nil
		}
	}
	rows, writes, err := s.resolvePlacementImport(ctx, file, column)
	if status, p, ok := answerOf(err); ok && status == http.StatusServiceUnavailable {
		return gen.ImportInventoryPlacements503ApplicationProblemPlusJSONResponse(p), nil
	}
	if err != nil {
		return nil, err
	}
	result := gen.InventoryPlacementImportResult{Ok: !slices.ContainsFunc(rows, func(r gen.InventoryPlacementImportRow) bool {
		return r.Outcome != gen.InventoryPlacementImportOutcomeOk
	}), Rows: rows}
	if (req.Params.DryRun == nil || *req.Params.DryRun) || !result.Ok {
		return gen.ImportInventoryPlacements200JSONResponse(result), nil
	}

	if hook := placementsResolved.Load(); hook != nil {
		(*hook)()
	}
	sum := sha256.Sum256(data)
	hashKey, digest := int64(binary.BigEndian.Uint64(sum[:8])), hex.EncodeToString(sum[:])
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	var done importOutcome
	replay := false
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		var err error
		done, replay, err = s.commitPlacements(ctx, txq, who, writes, !withMultiple, len(file.Rows), hashKey, digest)
		return err
	})
	if status, p, ok := answerOf(err); ok && status == http.StatusBadRequest {
		return gen.ImportInventoryPlacements400ApplicationProblemPlusJSONResponse(p), nil
	}
	if err != nil {
		return nil, err
	}
	result.Set, result.Cleared = &done.Set, &done.Cleared
	if replay {
		return gen.ImportInventoryPlacements200JSONResponse(result), nil
	}
	return gen.ImportInventoryPlacements201JSONResponse(result), nil
}

// placementImportLockKey is the advisory lock every placements commit takes
// first (LockPlacementImports): "INVPLACE" as eight bytes.
const placementImportLockKey int64 = 0x494e56504c414345

// importOutcome is what a commit wrote, or, on a replay, what the commit of
// the same file wrote before.
type importOutcome struct {
	Set     int32 `json:"set"`
	Cleared int32 `json:"cleared"`
}

// commitPlacements is the commit's transaction, in one lock order every
// writer of placements keeps — sites before locations before placements:
//
//  1. the imports' advisory lock, so commits run one at a time and the
//     file's hash is judged against every commit before it — a replay
//     writes nothing and answers what the first commit wrote;
//  2. every site the file names, cleared rows' too, FOR SHARE in id order,
//     each still there and active, as an archive (FOR UPDATE, then
//     clearPrimaries) is ordered against it;
//  3. the primary locations FOR SHARE in id order, each still active;
//  4. the placements, the writes sorted by (variant, site): the removals,
//     then the upsert, each with its placement_set or placement_cleared
//     event, and the import's one placements_imported event.
func (s *server) commitPlacements(ctx context.Context, txq *store.Queries, who actor, writes []placementWrite, keepMultiple bool, fileRows int, hashKey int64, digest string) (importOutcome, bool, error) {
	var out importOutcome
	if err := txq.LockPlacementImports(ctx, placementImportLockKey); err != nil {
		return out, false, fmt.Errorf("inventory: lock the placement imports: %w", err)
	}
	payload, err := txq.PlacementImportSeen(ctx, store.PlacementImportSeenParams{HashKey: hashKey, Sha256: digest})
	switch {
	case err == nil:
		if err := json.Unmarshal(payload, &out); err != nil {
			return out, false, fmt.Errorf("inventory: read the earlier import of this file: %w", err)
		}
		return out, true, nil
	case !noRows(err):
		return out, false, fmt.Errorf("inventory: read the placement imports: %w", err)
	}

	writes = slices.Clone(writes)
	slices.SortFunc(writes, func(a, b placementWrite) int {
		return cmp.Or(cmp.Compare(a.variantID, b.variantID), cmp.Compare(a.siteID, b.siteID))
	})
	var siteIDs, locationIDs, variantIDs []int64
	for _, w := range writes {
		siteIDs = append(siteIDs, w.siteID)
		variantIDs = append(variantIDs, int64(w.variantID))
		if w.primaryLocationID != nil {
			locationIDs = append(locationIDs, *w.primaryLocationID)
		}
	}
	slices.Sort(siteIDs)
	slices.Sort(locationIDs)
	siteIDs, locationIDs = slices.Compact(siteIDs), slices.Compact(locationIDs)
	sites, err := txq.ShareTransferSites(ctx, siteIDs)
	if err != nil {
		return out, false, fmt.Errorf("inventory: share the import's sites: %w", err)
	}
	if len(sites) != len(siteIDs) {
		return out, false, refuse(problem(http.StatusBadRequest, codeSiteArchived, "Site is gone",
			"A site the file names was deleted after the file was read. Nothing was written; import the file again."))
	}
	for _, site := range sites {
		if site.Status != string(gen.Active) {
			return out, false, refuse(problem(http.StatusBadRequest, codeSiteArchived, "Site is archived",
				fmt.Sprintf("The site %s was archived after the file was read. Nothing was written; import the file again.", site.Code)))
		}
	}
	locations, err := txq.ShareLocations(ctx, locationIDs)
	if err != nil {
		return out, false, fmt.Errorf("inventory: share the import's locations: %w", err)
	}
	if len(locations) != len(locationIDs) {
		return out, false, refuse(problem(http.StatusBadRequest, codePlacementLocationInvalid, "Location is gone",
			"A primary location was deleted after the file was read. Nothing was written; import the file again."))
	}
	for _, l := range locations {
		if l.Status != string(gen.Active) {
			return out, false, refuse(problem(http.StatusBadRequest, codePlacementLocationInvalid, "Location is archived",
				fmt.Sprintf("The location %s was archived after the file was read. Nothing was written; import the file again.", l.FullCode)))
		}
	}

	// What is there now, for each write's event.
	ids := make([]int32, 0, len(variantIDs))
	for _, id := range slices.Compact(variantIDs) {
		ids = append(ids, int32(id))
	}
	current, err := txq.PlacementsOfVariants(ctx, ids)
	if err != nil {
		return out, false, fmt.Errorf("inventory: read the import's placements: %w", err)
	}
	before := make(map[siteKey]store.InventoryPlacement, len(current))
	for _, p := range current {
		before[siteKey{p.VariantID, p.SiteID}] = p
	}

	var clears, sets []placementWrite
	for _, w := range writes {
		if w.clears() {
			clears = append(clears, w)
		} else {
			sets = append(sets, w)
		}
	}
	if len(clears) > 0 {
		params := store.DeletePlacementsOfParams{}
		for _, w := range clears {
			params.VariantIds, params.SiteIds = append(params.VariantIds, w.variantID), append(params.SiteIds, w.siteID)
		}
		removed, err := txq.DeletePlacementsOf(ctx, params)
		if err != nil {
			return out, false, fmt.Errorf("inventory: remove the import's cleared placements: %w", err)
		}
		slices.SortFunc(removed, func(a, b store.DeletePlacementsOfRow) int {
			return cmp.Or(cmp.Compare(a.VariantID, b.VariantID), cmp.Compare(a.SiteID, b.SiteID))
		})
		for _, r := range removed {
			was := before[siteKey{r.VariantID, r.SiteID}]
			if err := s.recordEvent(ctx, txq, who, event{
				EntityKind: entityPlacement, EntityID: int64(r.VariantID), SiteID: &r.SiteID, Kind: eventPlacementCleared,
				Payload: map[string]any{"variantId": r.VariantID, "sku": was.Sku, "removed": true, "reason": eventPlacementsImported,
					"fields": placementChanges(&was, placementFields{})},
			}); err != nil {
				return out, false, err
			}
		}
		out.Cleared = int32(len(removed))
	}
	if len(sets) > 0 {
		params := store.UpsertPlacementsParams{UpdatedByUserID: who.UserID, UpdatedByDisplay: &who.Display, Now: s.deps.Clock(),
			KeepMultiple: keepMultiple}
		for i, w := range sets {
			if keepMultiple {
				// The event's fields compare what the placement keeps.
				if was, ok := before[siteKey{w.variantID, w.siteID}]; ok {
					sets[i].multiple = optionalRat(was.Multiple)
				}
			}
			reorderPoint, err := optionalNumeric(w.reorderPoint)
			if err != nil {
				return out, false, err
			}
			maximum, err := optionalNumeric(w.maximum)
			if err != nil {
				return out, false, err
			}
			multiple, err := optionalNumeric(w.multiple)
			if err != nil {
				return out, false, err
			}
			var location int64
			if w.primaryLocationID != nil {
				location = *w.primaryLocationID
			}
			params.VariantIds = append(params.VariantIds, w.variantID)
			params.SiteIds = append(params.SiteIds, w.siteID)
			params.PrimaryLocationIds = append(params.PrimaryLocationIds, location)
			params.ReorderPoints = append(params.ReorderPoints, reorderPoint)
			params.Maximums = append(params.Maximums, maximum)
			params.Multiples = append(params.Multiples, multiple)
			params.Skus = append(params.Skus, w.sku)
			params.ProductNames = append(params.ProductNames, w.productName)
		}
		if err := txq.UpsertPlacements(ctx, params); err != nil {
			return out, false, fmt.Errorf("inventory: write the import's placements: %w", err)
		}
		for _, w := range sets {
			var was *store.InventoryPlacement
			if p, ok := before[siteKey{w.variantID, w.siteID}]; ok {
				was = &p
			}
			c := placementChanges(was, placementFields{primaryLocationID: w.primaryLocationID, reorderPoint: w.reorderPoint, maximum: w.maximum, multiple: w.multiple})
			if len(c) == 0 {
				continue
			}
			siteID := w.siteID
			if err := s.recordEvent(ctx, txq, who, event{
				EntityKind: entityPlacement, EntityID: int64(w.variantID), SiteID: &siteID, Kind: eventPlacementSet,
				Payload: map[string]any{"variantId": w.variantID, "sku": w.sku, "reason": eventPlacementsImported, "fields": c},
			}); err != nil {
				return out, false, err
			}
		}
		out.Set = int32(len(sets))
	}
	return out, false, s.recordEvent(ctx, txq, who, event{
		EntityKind: entityPlacement, EntityID: hashKey, Kind: eventPlacementsImported,
		Payload: map[string]any{"sha256": digest, "set": out.Set, "cleared": out.Cleared, "rows": fileRows, "writes": len(writes)},
	})
}

// resolvePlacementImport answers every row's outcome and, when every row is
// ok, the writes — one per variant and site, the last row naming it. It
// reads the catalog once for every distinct SKU, and the sites and
// locations once each, before any transaction.
func (s *server) resolvePlacementImport(ctx context.Context, file fileimport.File, column map[string]int) ([]gen.InventoryPlacementImportRow, []placementWrite, error) {
	cell := func(r fileimport.Record, name string) string {
		if i, ok := column[name]; ok && i < len(r.Cells) {
			return strings.TrimSpace(r.Cells[i])
		}
		return ""
	}
	var siteCodes, fullCodes, skus []string
	for _, r := range file.Rows {
		siteCodes = append(siteCodes, normalizeCode(cell(r, "site")))
		if code := strings.ToUpper(cell(r, "primarylocation")); code != "" {
			fullCodes = append(fullCodes, code)
		}
		if sku := strings.ToLower(cell(r, "sku")); sku != "" {
			skus = append(skus, sku)
		}
	}
	q := store.New(s.deps.Pool)
	sites, err := q.SitesByCodes(ctx, distinct(siteCodes))
	if err != nil {
		return nil, nil, fmt.Errorf("inventory: read the import's sites: %w", err)
	}
	siteByCode := make(map[string]store.InventorySite, len(sites))
	for _, site := range sites {
		siteByCode[site.Code] = site
	}
	locations, err := q.LocationsByFullCodes(ctx, distinct(fullCodes))
	if err != nil {
		return nil, nil, fmt.Errorf("inventory: read the import's locations: %w", err)
	}
	locationByCode := make(map[string]store.InventoryLocation, len(locations))
	for _, l := range locations {
		locationByCode[l.FullCode] = l
	}
	found, err := s.catalogLookupSKUs(ctx, distinct(skus))
	if err != nil {
		return nil, nil, err
	}

	rows := make([]gen.InventoryPlacementImportRow, 0, len(file.Rows))
	writes := make([]placementWrite, 0, len(file.Rows))
	last := map[siteKey]int{}
	for _, r := range file.Rows {
		row := gen.InventoryPlacementImportRow{
			Row: int32(r.Row + 1), Sku: cell(r, "sku"), Site: cell(r, "site"), PrimaryLocation: cell(r, "primarylocation"),
			ReorderPoint: cell(r, "reorderpoint"), Maximum: cell(r, "maximum"), Outcome: gen.InventoryPlacementImportOutcomeOk,
		}
		if _, ok := column[placementImportMultiple]; ok {
			row.Multiple = ptr(cell(r, placementImportMultiple))
		}
		w, outcome, detail := judgePlacementRow(row, extraCells(r, len(file.Header)), siteByCode, locationByCode, found)
		row.Outcome = outcome
		if detail != "" {
			row.Detail = &detail
		}
		rows = append(rows, row)
		if outcome != gen.InventoryPlacementImportOutcomeOk {
			continue
		}
		k := siteKey{w.variantID, w.siteID}
		if i, ok := last[k]; ok {
			writes[i] = w // last wins
			continue
		}
		last[k] = len(writes)
		writes = append(writes, w)
	}
	return rows, writes, nil
}

// judgePlacementRow is one row's outcome and, when ok, its write.
func judgePlacementRow(row gen.InventoryPlacementImportRow, extra bool, sites map[string]store.InventorySite,
	locations map[string]store.InventoryLocation, variants map[string][]contracts.VariantEntry) (placementWrite, gen.InventoryPlacementImportOutcome, string) {
	var w placementWrite
	if extra {
		return w, gen.InventoryPlacementImportOutcomeQuantityInvalid,
			"The row has more cells than the header. Put a number with a decimal comma in quotes, such as \"2,5\", or separate the columns with ;"
	}
	site, ok := sites[normalizeCode(row.Site)]
	switch {
	case !ok:
		return w, gen.InventoryPlacementImportOutcomeSiteNotFound, "No site has this code"
	case site.Status != string(gen.Active):
		return w, gen.InventoryPlacementImportOutcomeSiteArchived, "The site " + site.Code + " is archived"
	}
	entries := variants[strings.ToLower(row.Sku)]
	switch {
	case len(entries) == 0:
		return w, gen.InventoryPlacementImportOutcomeVariantNotFound, "No variant has this SKU"
	case entries[0].ProductType != productTypeGoods:
		return w, gen.InventoryPlacementImportOutcomeVariantNotStockable, row.Sku + " is a service; only goods are kept in stock"
	}
	// The first by id, as the opening-balance import takes it.
	v := entries[0]
	w = placementWrite{variantID: v.ID, siteID: site.ID, sku: v.SKU, productName: v.ProductName}
	if row.PrimaryLocation != "" {
		l, ok := locations[strings.ToUpper(row.PrimaryLocation)]
		switch {
		case !ok || l.SiteID != site.ID:
			return w, gen.InventoryPlacementImportOutcomePlacementLocationInvalid, "No location of the site " + site.Code + " has this full code"
		case l.Status != string(gen.Active):
			return w, gen.InventoryPlacementImportOutcomePlacementLocationInvalid, l.FullCode + " is archived"
		}
		w.primaryLocationID = &l.ID
	}
	if row.ReorderPoint != "" {
		q, msg := parseImportDecimal(row.ReorderPoint, "reorder point")
		if msg != "" {
			return w, gen.InventoryPlacementImportOutcomeQuantityInvalid, msg
		}
		w.reorderPoint = q
	}
	if row.Maximum != "" {
		q, msg := parseImportDecimal(row.Maximum, "maximum")
		if msg == "" && q.Sign() <= 0 {
			msg = "The maximum must be more than 0"
		}
		if msg != "" {
			return w, gen.InventoryPlacementImportOutcomeQuantityInvalid, msg
		}
		w.maximum = q
	}
	if row.Multiple != nil && *row.Multiple != "" {
		q, msg := parseImportDecimal(*row.Multiple, "multiple")
		if msg == "" && q.Sign() <= 0 {
			msg = "The multiple must be more than 0"
		}
		if msg != "" {
			return w, gen.InventoryPlacementImportOutcomeQuantityInvalid, msg
		}
		w.multiple = q
	}
	if w.reorderPoint != nil && w.maximum != nil && w.maximum.Cmp(w.reorderPoint) < 0 {
		return w, gen.InventoryPlacementImportOutcomeMaximumBelowReorderPoint,
			fmt.Sprintf("The maximum %s is below the reorder point %s", decimalText(w.maximum), decimalText(w.reorderPoint))
	}
	return w, gen.InventoryPlacementImportOutcomeOk, ""
}
