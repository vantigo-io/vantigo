package inventory

import (
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

// Locations (design I2–I6, I21). A location is a node of its site's tree,
// typed by a fixed list of kinds — site is the root's alone — and coded
// uniquely among its siblings. A change may rename, recode, re-kind, re-sort,
// re-parent inside the site (the subtree and its stock move with it: it is
// the location that moved, not the stock), archive the subtree, or
// reactivate the location alone; a delete takes the subtree only when no
// movement line ever named any of it. A site's root takes only a name and a
// sort order: its code is the site's, and it is archived and deleted with
// its site (root_location).

// locationKindMessage is why kind cannot be a written location's kind, "" when
// it can.
func locationKindMessage(kind gen.InventoryLocationKind) string {
	if !kind.Valid() || kind == gen.InventoryLocationKindSite {
		return "The kind must be one of zone, area, sales_floor, back_room, aisle, rack, shelf, bin, display, dock or other"
	}
	return ""
}

// GetInventoryLocation Get a location
// (GET /api/v1/inventory/locations/{id})
//
// The location with its site, its path from the root down to its parent, and
// its children by sort order then code — archived ones included (I29) — read
// from one snapshot.
func (s *server) GetInventoryLocation(ctx context.Context, req gen.GetInventoryLocationRequestObject) (gen.GetInventoryLocationResponseObject, error) {
	var out gen.InventoryLocationDetail
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		loc, err := q.GetLocation(ctx, req.Id)
		if noRows(err) {
			return notFound()
		}
		if err != nil {
			return fmt.Errorf("inventory: read location %d: %w", req.Id, err)
		}
		sites, err := q.SitesByIDs(ctx, []int64{loc.SiteID})
		if err != nil || len(sites) != 1 {
			return fmt.Errorf("inventory: read the site of location %d: %w", req.Id, err)
		}
		path, err := q.LocationsByIDs(ctx, loc.Ancestors)
		if err != nil {
			return fmt.Errorf("inventory: read the path of location %d: %w", req.Id, err)
		}
		slices.SortFunc(path, func(a, b store.InventoryLocation) int { return int(a.Depth - b.Depth) })
		children, err := q.ListChildren(ctx, &loc.ID)
		if err != nil {
			return fmt.Errorf("inventory: read the children of location %d: %w", req.Id, err)
		}
		out = gen.InventoryLocationDetail{
			Location: locationOf(loc), Site: siteRefOf(sites[0]),
			Path:     make([]gen.InventoryLocationRef, 0, len(path)),
			Children: make([]gen.InventoryLocation, 0, len(children)),
		}
		for _, p := range path {
			out.Path = append(out.Path, locationRefOf(p))
		}
		for _, c := range children {
			out.Children = append(out.Children, locationOf(c))
		}
		return nil
	})
	if status, _, ok := answerOf(err); ok && status == http.StatusNotFound {
		return gen.GetInventoryLocation404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	return gen.GetInventoryLocation200JSONResponse(out), nil
}

// CreateInventoryLocation Create a location
// (POST /api/v1/inventory/locations)
//
// The lock order of a create (§4): the parent's site row FOR UPDATE, then the
// parent FOR SHARE, which must be active (parent_invalid). The new location is
// one level below its parent, at most maxDepth below the root
// (depth_exceeded), and its code is unique among its siblings
// (location_code_taken) and makes a full code no other location holds
// (full_code_taken).
func (s *server) CreateInventoryLocation(ctx context.Context, req gen.CreateInventoryLocationRequestObject) (gen.CreateInventoryLocationResponseObject, error) {
	if req.Body == nil {
		return gen.CreateInventoryLocation400ApplicationProblemPlusJSONResponse(problem(http.StatusBadRequest, "", invalidLocationTitle, "The request has no body.")), nil
	}
	in := *req.Body
	f := s.unknownProperties(ctx, locationInputSchema)
	code := normalizeCode(in.Code)
	if m := codeMessage(code); m != "" {
		f.addCoded("code", codeCodeInvalid, m)
	}
	f.add("kind", locationKindMessage(in.Kind))
	name := optionalText(&f, "name", in.Name, maxNameLength)
	sortOrder := int32(0)
	if in.SortOrder != nil {
		sortOrder = *in.SortOrder
	}
	if f.failed() {
		return gen.CreateInventoryLocation400ApplicationProblemPlusJSONResponse(invalid(invalidLocationTitle, f)), nil
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	var out store.InventoryLocation
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		seen, err := txq.GetLocation(ctx, in.ParentId)
		if noRows(err) {
			return parentInvalid("parentId", "No location has this id")
		}
		if err != nil {
			return fmt.Errorf("inventory: read location %d: %w", in.ParentId, err)
		}
		if _, err := lockSite(ctx, txq, seen.SiteID); err != nil {
			return err
		}
		parent, err := txq.ShareLocation(ctx, in.ParentId)
		if noRows(err) {
			return parentInvalid("parentId", "No location has this id")
		}
		if err != nil {
			return fmt.Errorf("inventory: share location %d: %w", in.ParentId, err)
		}
		if parent.Status != string(gen.Active) {
			return parentInvalid("parentId", fmt.Sprintf("The parent %s is archived", parent.FullCode))
		}
		depth := int(parent.Depth) + 1
		if depth > maxDepth {
			return depthExceeded("parentId", depth)
		}
		fullCode := joinFullCode(parent.FullCode, code)
		if err := siblingCodeTaken(ctx, txq, parent.ID, code, 0, fullCode); err != nil {
			return err
		}
		if err := takenFullCodes(ctx, txq, []string{fullCode}, nil); err != nil {
			return err
		}
		out, err = txq.InsertLocation(ctx, store.InsertLocationParams{
			SiteID: parent.SiteID, ParentID: &parent.ID, Kind: string(in.Kind), Code: code, Name: name, SortOrder: sortOrder,
			FullCode: fullCode, Ancestors: childAncestors(parent), Depth: int16(depth), Now: s.deps.Clock(),
		})
		if db.IsUniqueViolation(err, "ux_locations_parent_code", "ux_locations_full_code") {
			return refuse(problem(http.StatusConflict, codeLocationCodeTaken, "Location code taken",
				fmt.Sprintf("The full code %s is in use.", fullCode)))
		}
		if err != nil {
			return fmt.Errorf("inventory: insert location: %w", err)
		}
		return s.recordEvent(ctx, txq, who, event{
			EntityKind: entityLocation, EntityID: out.ID, SiteID: &out.SiteID, Kind: eventLocationCreated,
			Payload: map[string]any{"parentId": parent.ID, "kind": out.Kind, "code": out.Code, "fullCode": out.FullCode},
		})
	})
	if status, p, ok := answerOf(err); ok {
		switch status {
		case http.StatusBadRequest:
			return gen.CreateInventoryLocation400ApplicationProblemPlusJSONResponse(p), nil
		case http.StatusConflict:
			return gen.CreateInventoryLocation409ApplicationProblemPlusJSONResponse(p), nil
		case http.StatusNotFound:
			return gen.CreateInventoryLocation400ApplicationProblemPlusJSONResponse(invalid(invalidLocationTitle, parentGone())), nil
		}
	}
	if err != nil {
		return nil, err
	}
	return gen.CreateInventoryLocation201JSONResponse(locationOf(out)), nil
}

// parentGone is the field error of a parent whose site went between the read
// and the lock: as if the parent did not exist.
func parentGone() fieldErrors {
	var f fieldErrors
	f.addCoded("parentId", codeParentInvalid, "No location has this id")
	return f
}

// locationChange is a location's replace judged.
type locationChange struct {
	kind      string
	code      string
	name      *string
	sortOrder int32
	status    string
	parentID  *int64
	// seesStock is whether the caller holds inventory:stock-view, read
	// before the transaction: an archive's location_has_stock says the
	// quantity only to them (stockRefusal).
	seesStock bool
}

// PutInventoryLocation Change a location
// (PUT /api/v1/inventory/locations/{id})
//
// The lock order of a change (§4): the site row FOR UPDATE, then the location,
// its subtree and a new parent FOR UPDATE in id order. Then, against the
// locked rows: the revision (revision_conflict); the root's rule (a root
// takes only name and sortOrder: root_location); the new parent — in the
// site, not the location or below it, active (parent_invalid); the depth of
// the deepest location of the subtree under it (depth_exceeded); the code
// among the new siblings (location_code_taken) and every full code the
// subtree would take (full_code_taken); an archive's stock check over the
// subtree (location_has_stock); and a reactivation's active parent. Only
// then is anything written: the location, the subtree's full codes,
// ancestors and depths in one statement (I4, I21), and an archive's subtree.
// The balances are not touched: a re-parent moves the location with its
// stock.
func (s *server) PutInventoryLocation(ctx context.Context, req gen.PutInventoryLocationRequestObject) (gen.PutInventoryLocationResponseObject, error) {
	revision, ok := parseIfMatch(req.Params.IfMatch)
	if !ok {
		return gen.PutInventoryLocation400ApplicationProblemPlusJSONResponse(invalid(invalidLocationTitle, badIfMatch())), nil
	}
	if req.Body == nil {
		return gen.PutInventoryLocation400ApplicationProblemPlusJSONResponse(problem(http.StatusBadRequest, "", invalidLocationTitle, "The request has no body.")), nil
	}
	f := s.unknownProperties(ctx, locationUpdateSchema)
	in := locationChange{
		kind: string(req.Body.Kind), code: normalizeCode(req.Body.Code), sortOrder: req.Body.SortOrder,
		status: string(req.Body.Status), parentID: req.Body.ParentId, seesStock: s.has(ctx, permStockView),
	}
	in.name = optionalText(&f, "name", req.Body.Name, maxNameLength)
	if !req.Body.Kind.Valid() {
		f.add("kind", locationKindMessage(req.Body.Kind))
	}
	if !req.Body.Status.Valid() {
		f.add("status", "The status must be active or archived")
	}
	if f.failed() {
		return gen.PutInventoryLocation400ApplicationProblemPlusJSONResponse(invalid(invalidLocationTitle, f)), nil
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	var out store.InventoryLocation
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		var err error
		out, err = s.changeLocation(ctx, txq, who, req.Id, revision, in)
		return err
	})
	if status, p, ok := answerOf(err); ok {
		switch status {
		case http.StatusBadRequest:
			return gen.PutInventoryLocation400ApplicationProblemPlusJSONResponse(p), nil
		case http.StatusNotFound:
			return gen.PutInventoryLocation404Response{}, nil
		case http.StatusConflict:
			return gen.PutInventoryLocation409ApplicationProblemPlusJSONResponse(p), nil
		}
	}
	if err != nil {
		return nil, err
	}
	return gen.PutInventoryLocation200JSONResponse(locationOf(out)), nil
}

// changeLocation is PutInventoryLocation's transaction.
func (s *server) changeLocation(ctx context.Context, txq *store.Queries, who actor, id int64, revision int32, in locationChange) (store.InventoryLocation, error) {
	seen, err := txq.GetLocation(ctx, id)
	if noRows(err) {
		return store.InventoryLocation{}, notFound()
	}
	if err != nil {
		return store.InventoryLocation{}, fmt.Errorf("inventory: read location %d: %w", id, err)
	}
	if _, err := lockSite(ctx, txq, seen.SiteID); err != nil {
		return store.InventoryLocation{}, err
	}
	var also []int64
	if in.parentID != nil {
		also = append(also, *in.parentID)
	}
	sub, err := lockSubtree(ctx, txq, id, also...)
	if err != nil {
		return store.InventoryLocation{}, err
	}
	cur := sub.node
	if cur.Revision != revision {
		return store.InventoryLocation{}, refuse(revisionConflict("location", cur.Revision, revision))
	}
	if cur.ParentID == nil {
		return s.changeRoot(ctx, txq, who, cur, in)
	}

	var f fieldErrors
	f.add("kind", locationKindMessage(gen.InventoryLocationKind(in.kind)))
	if in.parentID == nil {
		f.add("parentId", "A location other than the site's root needs a parent")
	}
	if f.failed() {
		return store.InventoryLocation{}, refuseInvalid(invalidLocationTitle, f)
	}
	parent, err := s.newParent(ctx, txq, sub, *in.parentID, *cur.ParentID)
	if err != nil {
		return store.InventoryLocation{}, err
	}
	reparent := parent.ID != *cur.ParentID
	if reparent {
		// A move must not carry stock in or out of a count still counting: a
		// count's scope in the moved subtree, above where it was or above where
		// it goes would leave two counts over the same balances, or a count's
		// lines outside its scope.
		ids := append(slices.Clone(sub.ids), cur.Ancestors...)
		ids = append(append(ids, parent.Ancestors...), parent.ID)
		open, err := txq.SubtreeHasOpenCount(ctx, ids)
		if err != nil {
			return store.InventoryLocation{}, fmt.Errorf("inventory: read the open counts over the move of location %d: %w", cur.ID, err)
		}
		if open {
			return store.InventoryLocation{}, refuse(problem(http.StatusConflict, codeLocationHasOpenCount, "Location has an open count",
				"The location "+cur.FullCode+" is being counted, or something below it, above it or above where it would go is. Post or cancel the count before moving it."))
		}
	}
	depth := int(parent.Depth) + 1
	if deepest := depth + sub.height(); deepest > maxDepth {
		return store.InventoryLocation{}, depthExceeded("parentId", deepest)
	}
	if m := codeMessage(in.code); m != "" {
		return store.InventoryLocation{}, codeInvalid(invalidLocationTitle, "code", m)
	}
	fullCode := joinFullCode(parent.FullCode, in.code)
	if reparent || in.code != cur.Code {
		if err := siblingCodeTaken(ctx, txq, parent.ID, in.code, cur.ID, fullCode); err != nil {
			return store.InventoryLocation{}, err
		}
		if err := takenFullCodes(ctx, txq, sub.fullCodesUnder(fullCode), sub.ids); err != nil {
			return store.InventoryLocation{}, err
		}
	}
	archive := in.status == string(gen.Archived) && cur.Status == string(gen.Active)
	reactivate := in.status == string(gen.Active) && cur.Status == string(gen.Archived)
	if archive {
		open, err := txq.SubtreeHasOpenCount(ctx, append(slices.Clone(sub.ids), cur.Ancestors...))
		if err != nil {
			return store.InventoryLocation{}, fmt.Errorf("inventory: read the open counts over location %d: %w", cur.ID, err)
		}
		if open {
			return store.InventoryLocation{}, openCountRefusal("The location " + cur.FullCode)
		}
		if err := stockRefusal(ctx, txq, "The location "+cur.FullCode, sub.ids, in.seesStock); err != nil {
			return store.InventoryLocation{}, err
		}
	}
	if reactivate && parent.Status != string(gen.Active) {
		return store.InventoryLocation{}, parentInvalid("status",
			fmt.Sprintf("The parent %s is archived; reactivate it first", parent.FullCode))
	}

	now := s.deps.Clock()
	ancestors := childAncestors(parent)
	out, err := txq.UpdateLocation(ctx, store.UpdateLocationParams{
		ID: cur.ID, ParentID: &parent.ID, Kind: in.kind, Code: in.code, Name: in.name, SortOrder: in.sortOrder,
		Status: in.status, FullCode: fullCode, Ancestors: ancestors, Depth: int16(depth), Now: now,
	})
	if db.IsUniqueViolation(err, "ux_locations_parent_code", "ux_locations_full_code") {
		return store.InventoryLocation{}, refuse(problem(http.StatusConflict, codeFullCodeTaken, "Full code taken",
			fmt.Sprintf("The full code %s is in use.", fullCode)))
	}
	if err != nil {
		return store.InventoryLocation{}, fmt.Errorf("inventory: update location %d: %w", cur.ID, err)
	}
	if fullCode != cur.FullCode || reparent {
		if err := txq.RewriteDescendants(ctx, store.RewriteDescendantsParams{
			ID: cur.ID, OldFullCode: cur.FullCode, NewFullCode: fullCode, NewAncestors: ancestors,
			OldAncestorCount: int32(len(cur.Ancestors)), DepthDelta: int32(depth - int(cur.Depth)), Now: now,
		}); err != nil {
			return store.InventoryLocation{}, fmt.Errorf("inventory: rewrite the subtree of location %d: %w", cur.ID, err)
		}
	}
	if archive {
		if err := txq.ArchiveSubtree(ctx, store.ArchiveSubtreeParams{ID: cur.ID, Now: now}); err != nil {
			return store.InventoryLocation{}, fmt.Errorf("inventory: archive the subtree of location %d: %w", cur.ID, err)
		}
	}
	return out, s.recordLocationChanges(ctx, txq, who, cur, out, len(sub.ids))
}

// newParent is the location a change puts the location under, judged against
// the locked rows: the current parent when it stays; otherwise a location of
// the same site, not the location or below it, and active (parent_invalid).
func (s *server) newParent(ctx context.Context, txq *store.Queries, sub subtree, id, current int64) (store.InventoryLocation, error) {
	if sub.contains(id) {
		return store.InventoryLocation{}, parentInvalid("parentId", "A location cannot be put under itself or a location below it")
	}
	if id == current {
		parent, err := txq.GetLocation(ctx, id)
		if err != nil {
			return store.InventoryLocation{}, fmt.Errorf("inventory: read location %d: %w", id, err)
		}
		return parent, nil
	}
	parent, ok := sub.others[id]
	switch {
	case !ok:
		return store.InventoryLocation{}, parentInvalid("parentId", "No location has this id")
	case parent.SiteID != sub.node.SiteID:
		return store.InventoryLocation{}, parentInvalid("parentId", "A location stays in its site; the parent is in another")
	case parent.Status != string(gen.Active):
		return store.InventoryLocation{}, parentInvalid("parentId", fmt.Sprintf("The parent %s is archived", parent.FullCode))
	}
	return parent, nil
}

// changeRoot is a change of a site's root, which takes only its name and its
// sort order: its kind, code, place and status are the site's
// (root_location).
func (s *server) changeRoot(ctx context.Context, txq *store.Queries, who actor, cur store.InventoryLocation, in locationChange) (store.InventoryLocation, error) {
	if in.parentID != nil || in.kind != cur.Kind || in.code != cur.Code || in.status != cur.Status {
		return store.InventoryLocation{}, rootRefusal(
			"A site's root takes only a name and a sort order; its code is the site's, and it is archived and deleted with its site.")
	}
	out, err := txq.UpdateLocation(ctx, store.UpdateLocationParams{
		ID: cur.ID, Kind: cur.Kind, Code: cur.Code, Name: in.name, SortOrder: in.sortOrder, Status: cur.Status,
		FullCode: cur.FullCode, Ancestors: cur.Ancestors, Depth: cur.Depth, Now: s.deps.Clock(),
	})
	if err != nil {
		return store.InventoryLocation{}, fmt.Errorf("inventory: update location %d: %w", cur.ID, err)
	}
	return out, s.recordLocationChanges(ctx, txq, who, cur, out, 1)
}

// recordLocationChanges records what a change did: the fields as
// location_changed, a new parent as location_reparented (with the full codes,
// so the page can offer a label reprint), and the status on its own as
// location_archived — with how many locations the archive reached — or
// location_reactivated.
func (s *server) recordLocationChanges(ctx context.Context, txq *store.Queries, who actor, before, after store.InventoryLocation, subtreeSize int) error {
	c := changes{}
	note := func(field string, from, to any) {
		if fmt.Sprint(from) != fmt.Sprint(to) {
			c[field] = fieldChange{From: from, To: to}
		}
	}
	note("kind", before.Kind, after.Kind)
	note("code", before.Code, after.Code)
	note("name", deref(before.Name), deref(after.Name))
	note("sortOrder", before.SortOrder, after.SortOrder)
	e := event{EntityKind: entityLocation, EntityID: after.ID, SiteID: &after.SiteID, Kind: eventLocationChanged}
	if err := s.recordChanges(ctx, txq, who, e, c); err != nil {
		return err
	}
	if before.ParentID != nil && after.ParentID != nil && *before.ParentID != *after.ParentID {
		e.Kind = eventLocationReparented
		e.Payload = map[string]any{
			"parentId": fieldChange{From: *before.ParentID, To: *after.ParentID},
			"fullCode": fieldChange{From: before.FullCode, To: after.FullCode},
		}
		if err := s.recordEvent(ctx, txq, who, e); err != nil {
			return err
		}
	}
	if before.Status == after.Status {
		return nil
	}
	e.Kind, e.Payload = eventLocationReactivated, map[string]any{"fullCode": after.FullCode}
	if after.Status == string(gen.Archived) {
		e.Kind, e.Payload = eventLocationArchived, map[string]any{"fullCode": after.FullCode, "locations": subtreeSize}
	}
	return s.recordEvent(ctx, txq, who, e)
}

// deref is *p, "" for nil.
func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// DeleteInventoryLocation Delete a location
// (DELETE /api/v1/inventory/locations/{id})
//
// Under the site row's lock and the subtree's FOR UPDATE (§4): refused for a
// site's root (root_location), and for a subtree a movement line ever named
// (location_has_history, by an EXISTS on the lines' from and to over the
// locked ids); otherwise the whole subtree goes in one statement. A subtree
// with no history has no stock either, since only a movement puts it down.
func (s *server) DeleteInventoryLocation(ctx context.Context, req gen.DeleteInventoryLocationRequestObject) (gen.DeleteInventoryLocationResponseObject, error) {
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		seen, err := txq.GetLocation(ctx, req.Id)
		if noRows(err) {
			return notFound()
		}
		if err != nil {
			return fmt.Errorf("inventory: read location %d: %w", req.Id, err)
		}
		if seen.ParentID == nil {
			return rootRefusal("A site's root is deleted with its site, never alone.")
		}
		if _, err := lockSite(ctx, txq, seen.SiteID); err != nil {
			return err
		}
		sub, err := lockSubtree(ctx, txq, req.Id)
		if err != nil {
			return err
		}
		if err := historyRefusal(ctx, txq, "The location "+sub.node.FullCode, sub.ids); err != nil {
			return err
		}
		if err := txq.DeleteLocations(ctx, sub.ids); err != nil {
			return fmt.Errorf("inventory: delete the subtree of location %d: %w", req.Id, err)
		}
		return s.recordEvent(ctx, txq, who, event{
			EntityKind: entityLocation, EntityID: sub.node.ID, SiteID: &sub.node.SiteID, Kind: eventLocationDeleted,
			Payload: map[string]any{"fullCode": sub.node.FullCode, "locations": len(sub.ids)},
		})
	})
	if status, p, ok := answerOf(err); ok {
		switch status {
		case http.StatusNotFound:
			return gen.DeleteInventoryLocation404Response{}, nil
		case http.StatusConflict:
			return gen.DeleteInventoryLocation409ApplicationProblemPlusJSONResponse(p), nil
		}
	}
	if err != nil {
		return nil, err
	}
	return gen.DeleteInventoryLocation204Response{}, nil
}

// BulkCreateInventoryLocations Create locations in bulk
// (POST /api/v1/inventory/locations/bulk)
//
// The levels are judged and expanded before the transaction (bulk.go). Then,
// in the lock order of a create (§4): the site row FOR UPDATE, the parent FOR
// SHARE and active, the deepest level within maxDepth, and every new full
// code checked against the tree at once — one collision refuses the whole
// grid, listing every colliding full code (location_code_taken for the first
// level's codes among the parent's children, full_code_taken otherwise).
// The grid is inserted a level at a statement.
func (s *server) BulkCreateInventoryLocations(ctx context.Context, req gen.BulkCreateInventoryLocationsRequestObject) (gen.BulkCreateInventoryLocationsResponseObject, error) {
	if req.Body == nil {
		return gen.BulkCreateInventoryLocations400ApplicationProblemPlusJSONResponse(problem(http.StatusBadRequest, "", invalidBulkTitle, "The request has no body.")), nil
	}
	f := s.unknownProperties(ctx, bulkInputSchema)
	levels, lf := expandLevels(req.Body.Levels)
	f.merge(lf)
	if f.failed() {
		return gen.BulkCreateInventoryLocations400ApplicationProblemPlusJSONResponse(invalid(invalidBulkTitle, f)), nil
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	var created []store.InventoryLocation
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		var err error
		created, err = s.bulkCreate(ctx, txq, who, req.Body.ParentId, levels)
		return err
	})
	if status, p, ok := answerOf(err); ok {
		switch status {
		case http.StatusBadRequest, http.StatusNotFound:
			if status == http.StatusNotFound {
				p = invalid(invalidBulkTitle, parentGone())
			}
			return gen.BulkCreateInventoryLocations400ApplicationProblemPlusJSONResponse(p), nil
		case http.StatusConflict:
			return gen.BulkCreateInventoryLocations409ApplicationProblemPlusJSONResponse(p), nil
		}
	}
	if err != nil {
		return nil, err
	}
	out := gen.InventoryBulkCreated{Created: make([]gen.InventoryLocation, 0, len(created))}
	for _, l := range created {
		out.Created = append(out.Created, locationOf(l))
	}
	return gen.BulkCreateInventoryLocations201JSONResponse(out), nil
}

// bulkCreate is BulkCreateInventoryLocations' transaction. It answers the
// locations made, by full code.
func (s *server) bulkCreate(ctx context.Context, txq *store.Queries, who actor, parentID int64, levels []bulkLevel) ([]store.InventoryLocation, error) {
	seen, err := txq.GetLocation(ctx, parentID)
	if noRows(err) {
		return nil, parentInvalid("parentId", "No location has this id")
	}
	if err != nil {
		return nil, fmt.Errorf("inventory: read location %d: %w", parentID, err)
	}
	if _, err := lockSite(ctx, txq, seen.SiteID); err != nil {
		return nil, err
	}
	parent, err := txq.ShareLocation(ctx, parentID)
	if noRows(err) {
		return nil, parentInvalid("parentId", "No location has this id")
	}
	if err != nil {
		return nil, fmt.Errorf("inventory: share location %d: %w", parentID, err)
	}
	if parent.Status != string(gen.Active) {
		return nil, parentInvalid("parentId", fmt.Sprintf("The parent %s is archived", parent.FullCode))
	}
	if deepest := int(parent.Depth) + len(levels); deepest > maxDepth {
		return nil, depthExceeded("levels", deepest)
	}
	plan := planBulk(parent.FullCode, levels)
	var all []string
	for _, level := range plan {
		for _, p := range level {
			all = append(all, p.fullCode)
		}
	}
	taken, err := txq.TakenFullCodes(ctx, store.TakenFullCodesParams{FullCodes: all, Excluded: []int64{}})
	if err != nil {
		return nil, fmt.Errorf("inventory: check full codes: %w", err)
	}
	if len(taken) > 0 {
		code, title := codeFullCodeTaken, "Full codes taken"
		if slices.ContainsFunc(plan[0], func(p plannedLocation) bool { return slices.Contains(taken, p.fullCode) }) {
			code, title = codeLocationCodeTaken, "Location codes taken"
		}
		return nil, refuse(problem(http.StatusConflict, code, title,
			fmt.Sprintf("Nothing was created; these locations exist already: %s.", joinCodes(taken))))
	}

	now := s.deps.Clock()
	ids := map[string]int64{parent.FullCode: parent.ID}
	var out []store.InventoryLocation
	for _, level := range plan {
		params := store.InsertChildrenParams{Now: now}
		for _, p := range level {
			params.ParentIds = append(params.ParentIds, ids[p.parent])
			params.Kinds = append(params.Kinds, p.kind)
			params.Codes = append(params.Codes, p.code)
			params.FullCodes = append(params.FullCodes, p.fullCode)
		}
		rows, err := txq.InsertChildren(ctx, params)
		if db.IsUniqueViolation(err, "ux_locations_parent_code", "ux_locations_full_code") {
			return nil, refuse(problem(http.StatusConflict, codeFullCodeTaken, "Full codes taken",
				"Nothing was created; a location of the grid exists already."))
		}
		if err != nil {
			return nil, fmt.Errorf("inventory: insert a bulk level under location %d: %w", parent.ID, err)
		}
		for _, r := range rows {
			ids[r.FullCode] = r.ID
		}
		out = append(out, rows...)
	}
	slices.SortFunc(out, func(a, b store.InventoryLocation) int { return strings.Compare(a.FullCode, b.FullCode) })
	created := make([]string, 0, len(out))
	for _, l := range out {
		created = append(created, l.FullCode)
	}
	return out, s.recordEvent(ctx, txq, who, event{
		EntityKind: entityLocation, EntityID: parent.ID, SiteID: &parent.SiteID, Kind: eventLocationsBulk,
		Payload: map[string]any{"count": len(out), "fullCodes": created},
	})
}

// joinCodes lists full codes for a person, the first 50 and a count of the
// rest.
func joinCodes(codes []string) string {
	const shown = 50
	if len(codes) <= shown {
		return strings.Join(codes, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(codes[:shown], ", "), len(codes)-shown)
}
