package inventory

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
	"github.com/vantigo-io/vantigo/server/internal/inventory/store"
)

// Sites (design I1, I6). A site is a warehouse or a store and the root of a
// location tree: creating one creates its root location, of kind site and
// coded as the site, in the same transaction; renaming its code renames the
// root and every full code below it (I4). Archiving a site archives every
// location in it and is refused while anything in it holds stock;
// reactivating it reactivates it and its root alone. Archiving is refused,
// too, while a transfer from or to it is on its way (phase 2 design I48). A
// site is deleted only when no movement was ever posted in it and no
// transfer names it.

// siteFields is a site's request judged: the code normalised, the name
// trimmed, the kind and the address.
type siteFields struct {
	code, name, kind string
	address          address
}

// readSiteFields judges the fields a create and a replace share.
func readSiteFields(f *fieldErrors, code, name string, kind gen.InventorySiteKind, a *gen.InventoryAddress) siteFields {
	out := siteFields{code: normalizeCode(code), name: strings.TrimSpace(name), kind: string(kind)}
	if m := codeMessage(out.code); m != "" {
		f.addCoded("code", codeCodeInvalid, m)
	}
	switch n := utf8.RuneCountInString(out.name); {
	case n == 0:
		f.add("name", "A site needs a name")
	case n > maxNameLength:
		f.add("name", fmt.Sprintf("A name is at most %d characters", maxNameLength))
	}
	if !kind.Valid() {
		f.add("kind", "The kind must be warehouse or store")
	}
	out.address = readAddress(f, a)
	return out
}

// siteCodeTaken is the 409 of a site code another site has.
func siteCodeTaken(code string) error {
	return refuse(problem(http.StatusConflict, codeSiteCodeTaken, "Site code taken",
		fmt.Sprintf("Another site has the code %s.", code)))
}

// ListInventorySites List sites
// (GET /api/v1/inventory/sites)
func (s *server) ListInventorySites(ctx context.Context, req gen.ListInventorySitesRequestObject) (gen.ListInventorySitesResponseObject, error) {
	status := string(gen.Active)
	if req.Params.Status != nil {
		status = string(*req.Params.Status)
	}
	out := gen.InventorySiteList{Sites: []gen.InventorySite{}}
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		rows, err := q.ListSites(ctx, status)
		if err != nil {
			return fmt.Errorf("inventory: list sites: %w", err)
		}
		for _, r := range rows {
			out.Sites = append(out.Sites, siteRowOf(store.GetSiteRow(r)))
		}
		return s.withFigures(ctx, q, out.Sites)
	})
	if err != nil {
		return nil, err
	}
	return gen.ListInventorySites200JSONResponse(out), nil
}

// GetInventorySite Get a site
// (GET /api/v1/inventory/sites/{id})
func (s *server) GetInventorySite(ctx context.Context, req gen.GetInventorySiteRequestObject) (gen.GetInventorySiteResponseObject, error) {
	q := store.New(s.deps.Pool)
	row, err := q.GetSite(ctx, req.Id)
	if noRows(err) {
		return gen.GetInventorySite404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inventory: read site %d: %w", req.Id, err)
	}
	out := []gen.InventorySite{siteRowOf(row)}
	if err := s.withFigures(ctx, q, out); err != nil {
		return nil, err
	}
	return gen.GetInventorySite200JSONResponse(out[0]), nil
}

// CreateInventorySite Create a site
// (POST /api/v1/inventory/sites)
//
// The site and its root location are one transaction (I1). The code check is
// a read; a concurrent create of the same code that passed it too is refused
// by the unique index, and answered the same.
func (s *server) CreateInventorySite(ctx context.Context, req gen.CreateInventorySiteRequestObject) (gen.CreateInventorySiteResponseObject, error) {
	if req.Body == nil {
		return gen.CreateInventorySite400ApplicationProblemPlusJSONResponse(problem(http.StatusBadRequest, "", invalidSiteTitle, "The request has no body.")), nil
	}
	f := s.unknownProperties(ctx, siteInputSchema)
	in := readSiteFields(&f, req.Body.Code, req.Body.Name, req.Body.Kind, req.Body.Address)
	if f.failed() {
		return gen.CreateInventorySite400ApplicationProblemPlusJSONResponse(invalid(invalidSiteTitle, f)), nil
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	var out gen.InventorySite
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		taken, err := txq.SiteCodeTaken(ctx, store.SiteCodeTakenParams{Code: in.code})
		if err != nil {
			return fmt.Errorf("inventory: check site codes: %w", err)
		}
		if taken {
			return siteCodeTaken(in.code)
		}
		now := s.deps.Clock()
		// A warehouse fulfils orders unless told otherwise, a store does not
		// (orders design O45).
		fulfils := in.kind == string(gen.Warehouse)
		if req.Body.FulfilsOrders != nil {
			fulfils = *req.Body.FulfilsOrders
		}
		site, err := txq.InsertSite(ctx, store.InsertSiteParams{
			Code: in.code, Name: in.name, Kind: in.kind, AddressLine1: in.address.line1, AddressLine2: in.address.line2,
			PostalCode: in.address.postalCode, City: in.address.city, Country: in.address.country, FulfilsOrders: fulfils, Now: now,
		})
		if db.IsUniqueViolation(err, "ux_sites_code") {
			return siteCodeTaken(in.code)
		}
		if err != nil {
			return fmt.Errorf("inventory: insert site: %w", err)
		}
		root, err := txq.InsertLocation(ctx, store.InsertLocationParams{
			SiteID: site.ID, Kind: string(gen.InventoryLocationKindSite), Code: in.code, FullCode: in.code,
			Ancestors: []int64{}, Depth: 0, Now: now,
		})
		if db.IsUniqueViolation(err, "ux_locations_full_code") {
			return siteCodeTaken(in.code)
		}
		if err != nil {
			return fmt.Errorf("inventory: insert the root of site %d: %w", site.ID, err)
		}
		out = siteOf(site, root.ID)
		if s.has(ctx, permStockView) {
			out.StockLines = ptr(int32(0))
		}
		return s.recordEvent(ctx, txq, who, event{
			EntityKind: entitySite, EntityID: site.ID, SiteID: &site.ID, Kind: eventSiteCreated,
			Payload: map[string]any{"code": site.Code, "name": site.Name, "kind": site.Kind, "rootLocationId": root.ID, "fulfilsOrders": site.FulfilsOrders},
		})
	})
	if status, p, ok := answerOf(err); ok && status == http.StatusConflict {
		return gen.CreateInventorySite409ApplicationProblemPlusJSONResponse(p), nil
	}
	if err != nil {
		return nil, err
	}
	return gen.CreateInventorySite201JSONResponse(out), nil
}

// PutInventorySite Change a site
// (PUT /api/v1/inventory/sites/{id})
//
// Under the site row's lock (§4): a new code is checked against the other
// sites, then followed through every location of the site (I4); archiving
// is refused while anything in the site holds stock, and archives every
// location (I6); reactivating reactivates the site and its root alone.
// Every location of the site is locked FOR UPDATE in id order whenever the
// code or the status changes, since either touches them.
func (s *server) PutInventorySite(ctx context.Context, req gen.PutInventorySiteRequestObject) (gen.PutInventorySiteResponseObject, error) {
	revision, ok := parseIfMatch(req.Params.IfMatch)
	if !ok {
		return gen.PutInventorySite400ApplicationProblemPlusJSONResponse(invalid(invalidSiteTitle, badIfMatch())), nil
	}
	if req.Body == nil {
		return gen.PutInventorySite400ApplicationProblemPlusJSONResponse(problem(http.StatusBadRequest, "", invalidSiteTitle, "The request has no body.")), nil
	}
	f := s.unknownProperties(ctx, siteUpdateSchema)
	in := readSiteFields(&f, req.Body.Code, req.Body.Name, req.Body.Kind, req.Body.Address)
	status := string(req.Body.Status)
	if !req.Body.Status.Valid() {
		f.add("status", "The status must be active or archived")
	}
	if f.failed() {
		return gen.PutInventorySite400ApplicationProblemPlusJSONResponse(invalid(invalidSiteTitle, f)), nil
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	seesStock := s.has(ctx, permStockView)
	var out gen.InventorySite
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		cur, err := lockSite(ctx, txq, req.Id)
		if err != nil {
			return err
		}
		if cur.Revision != revision {
			return refuse(revisionConflict("site", cur.Revision, revision))
		}
		recode, statusChange := in.code != cur.Code, status != cur.Status
		var locations []store.InventoryLocation
		if recode || statusChange {
			if locations, err = txq.LockSiteLocations(ctx, cur.ID); err != nil {
				return fmt.Errorf("inventory: lock the locations of site %d: %w", cur.ID, err)
			}
		}
		var root store.InventoryLocation
		ids := make([]int64, 0, len(locations))
		for _, l := range locations {
			ids = append(ids, l.ID)
			if l.ParentID == nil {
				root = l
			}
		}
		now := s.deps.Clock()
		if recode {
			taken, err := txq.SiteCodeTaken(ctx, store.SiteCodeTakenParams{Code: in.code, ID: cur.ID})
			if err != nil {
				return fmt.Errorf("inventory: check site codes: %w", err)
			}
			if taken {
				return siteCodeTaken(in.code)
			}
		}
		if statusChange && status == string(gen.Archived) {
			open, err := txq.SiteHasOpenCount(ctx, cur.ID)
			if err != nil {
				return fmt.Errorf("inventory: read the open counts of site %d: %w", cur.ID, err)
			}
			if open {
				return openCountRefusal("The site " + cur.Code)
			}
			if err := stockRefusal(ctx, txq, "The site "+cur.Code, ids, seesStock); err != nil {
				return err
			}
			// A transfer on its way from or to the site (phase 2 design
			// I48); a draft does not hold it, its send is refused instead.
			// A send or a receive holds the site FOR SHARE, so this read,
			// under the site's FOR UPDATE, sees every one committed.
			inTransit, err := txq.SiteHasOpenTransfers(ctx, cur.ID)
			if err != nil {
				return fmt.Errorf("inventory: read the open transfers of site %d: %w", cur.ID, err)
			}
			if inTransit {
				return refuse(problem(http.StatusConflict, codeSiteHasOpenTransfers, "Site has open transfers",
					fmt.Sprintf("The site %s is the from or to site of a transfer still on its way. Receive or cancel it before archiving.", cur.Code)))
			}
		}
		fulfils := cur.FulfilsOrders
		if req.Body.FulfilsOrders != nil {
			fulfils = *req.Body.FulfilsOrders
		}
		site, err := txq.UpdateSite(ctx, store.UpdateSiteParams{
			ID: cur.ID, Code: in.code, Name: in.name, Kind: in.kind, AddressLine1: in.address.line1, AddressLine2: in.address.line2,
			PostalCode: in.address.postalCode, City: in.address.city, Country: in.address.country, Status: status,
			FulfilsOrders: fulfils, Now: now,
		})
		if db.IsUniqueViolation(err, "ux_sites_code") {
			return siteCodeTaken(in.code)
		}
		if err != nil {
			return fmt.Errorf("inventory: update site %d: %w", cur.ID, err)
		}
		if recode {
			err := txq.RecodeSite(ctx, store.RecodeSiteParams{SiteID: cur.ID, OldCode: cur.Code, NewCode: in.code, Now: now})
			if db.IsUniqueViolation(err, "ux_locations_full_code") {
				return refuse(problem(http.StatusConflict, codeFullCodeTaken, "Full code taken",
					fmt.Sprintf("The code %s would give a location a full code in use.", in.code)))
			}
			if err != nil {
				return fmt.Errorf("inventory: recode the locations of site %d: %w", cur.ID, err)
			}
		}
		if statusChange {
			touched := ids
			if status == string(gen.Active) {
				touched = []int64{root.ID}
			}
			if err := txq.SetSiteLocationsStatus(ctx, store.SetSiteLocationsStatusParams{Status: status, Ids: touched, Now: now}); err != nil {
				return fmt.Errorf("inventory: set the status of site %d's locations: %w", cur.ID, err)
			}
			// Every location archived with the site is no one's primary any
			// more (phase 2 I55); the reorder points and maximums stay.
			if status == string(gen.Archived) && root.ID != 0 {
				if err := s.clearPrimaries(ctx, txq, who, []int64{root.ID}, eventSiteArchived); err != nil {
					return err
				}
			}
		}
		if root.ID == 0 {
			if root, err = txq.GetRootLocation(ctx, cur.ID); err != nil {
				return fmt.Errorf("inventory: read the root of site %d: %w", cur.ID, err)
			}
		}
		out = siteOf(site, root.ID)
		sites := []gen.InventorySite{out}
		if err := s.withFigures(ctx, txq, sites); err != nil {
			return err
		}
		out = sites[0]
		return s.recordSiteChanges(ctx, txq, who, cur, site)
	})
	if status, p, ok := answerOf(err); ok {
		switch status {
		case http.StatusNotFound:
			return gen.PutInventorySite404Response{}, nil
		case http.StatusConflict:
			return gen.PutInventorySite409ApplicationProblemPlusJSONResponse(p), nil
		case http.StatusBadRequest:
			return gen.PutInventorySite400ApplicationProblemPlusJSONResponse(p), nil
		}
	}
	if err != nil {
		return nil, err
	}
	return gen.PutInventorySite200JSONResponse(out), nil
}

// recordSiteChanges records what a replace changed: the fields as
// site_changed, and the status on its own as site_archived or
// site_reactivated.
func (s *server) recordSiteChanges(ctx context.Context, txq *store.Queries, who actor, before, after store.InventorySite) error {
	c := changes{}
	note := func(field string, from, to any) {
		if fmt.Sprint(from) != fmt.Sprint(to) {
			c[field] = fieldChange{From: from, To: to}
		}
	}
	note("code", before.Code, after.Code)
	note("name", before.Name, after.Name)
	note("kind", before.Kind, after.Kind)
	note("fulfilsOrders", before.FulfilsOrders, after.FulfilsOrders)
	note("address", addressOf(before.AddressLine1, before.AddressLine2, before.PostalCode, before.City, before.Country),
		addressOf(after.AddressLine1, after.AddressLine2, after.PostalCode, after.City, after.Country))
	e := event{EntityKind: entitySite, EntityID: after.ID, SiteID: &after.ID, Kind: eventSiteChanged}
	if err := s.recordChanges(ctx, txq, who, e, c); err != nil {
		return err
	}
	if before.Status == after.Status {
		return nil
	}
	e.Kind = eventSiteReactivated
	if after.Status == string(gen.Archived) {
		e.Kind = eventSiteArchived
	}
	e.Payload = map[string]any{"code": after.Code}
	return s.recordEvent(ctx, txq, who, e)
}

// DeleteInventorySite Delete a site
// (DELETE /api/v1/inventory/sites/{id})
//
// A site with a movement in its history is archived, never deleted (I6). One
// without has no stock either — only a movement puts stock down — so its
// locations, then the site, go in one transaction under its lock.
func (s *server) DeleteInventorySite(ctx context.Context, req gen.DeleteInventorySiteRequestObject) (gen.DeleteInventorySiteResponseObject, error) {
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		site, err := lockSite(ctx, txq, req.Id)
		if err != nil {
			return err
		}
		used, err := txq.SiteHasMovements(ctx, site.ID)
		if err != nil {
			return fmt.Errorf("inventory: read the history of site %d: %w", site.ID, err)
		}
		if used {
			return refuse(problem(http.StatusConflict, codeLocationHasHistory, "Site has history",
				fmt.Sprintf("The site %s has movements and cannot be deleted; archive it instead.", site.Code)))
		}
		named, err := txq.SiteHasTransfers(ctx, site.ID)
		if err != nil {
			return fmt.Errorf("inventory: read the transfers of site %d: %w", site.ID, err)
		}
		if named {
			return refuse(problem(http.StatusConflict, codeSiteHasTransfers, "Site has transfers",
				fmt.Sprintf("A transfer names the site %s, so it cannot be deleted; delete the draft or archive the site instead.", site.Code)))
		}
		locations, err := txq.LockSiteLocations(ctx, site.ID)
		if err != nil {
			return fmt.Errorf("inventory: lock the locations of site %d: %w", site.ID, err)
		}
		ids := make([]int64, 0, len(locations))
		for _, l := range locations {
			ids = append(ids, l.ID)
		}
		if err := txq.DeleteLocations(ctx, ids); err != nil {
			return fmt.Errorf("inventory: delete the locations of site %d: %w", site.ID, err)
		}
		if err := txq.DeleteSite(ctx, site.ID); err != nil {
			return fmt.Errorf("inventory: delete site %d: %w", site.ID, err)
		}
		return s.recordEvent(ctx, txq, who, event{
			EntityKind: entitySite, EntityID: site.ID, SiteID: &site.ID, Kind: eventSiteDeleted,
			Payload: map[string]any{"code": site.Code, "name": site.Name, "locations": len(ids)},
		})
	})
	if status, p, ok := answerOf(err); ok {
		switch status {
		case http.StatusNotFound:
			return gen.DeleteInventorySite404Response{}, nil
		case http.StatusConflict:
			return gen.DeleteInventorySite409ApplicationProblemPlusJSONResponse(p), nil
		}
	}
	if err != nil {
		return nil, err
	}
	return gen.DeleteInventorySite204Response{}, nil
}

// withFigures fills each site's count of active locations and — for a
// caller holding inventory:stock-view — its stock lines and last receipt, in
// one query for all of them.
func (s *server) withFigures(ctx context.Context, q *store.Queries, sites []gen.InventorySite) error {
	if len(sites) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(sites))
	for _, site := range sites {
		ids = append(ids, site.Id)
	}
	rows, err := q.SiteFigures(ctx, ids)
	if err != nil {
		return fmt.Errorf("inventory: read the sites' figures: %w", err)
	}
	figures := make(map[int64]store.SiteFiguresRow, len(rows))
	for _, r := range rows {
		figures[r.ID] = r
	}
	seesStock := s.has(ctx, permStockView)
	open := map[int64]store.OpenTransferCountsRow{}
	if seesStock {
		rows, err := q.OpenTransferCounts(ctx, ids)
		if err != nil {
			return fmt.Errorf("inventory: read the sites' open transfers: %w", err)
		}
		for _, r := range rows {
			open[r.SiteID] = r
		}
	}
	for i := range sites {
		f := figures[sites[i].Id]
		sites[i].LocationCount = f.LocationCount
		if seesStock {
			sites[i].StockLines, sites[i].LastReceiptAt = ptr(f.StockLines), f.LastReceiptAt
			o := open[sites[i].Id]
			sites[i].IncomingTransfers, sites[i].OutgoingTransfers = ptr(o.Incoming), ptr(o.Outgoing)
		}
	}
	return nil
}

// maxSiteLocations is the most locations GET /sites/{id}/locations answers.
const maxSiteLocations = 10000

// ListInventorySiteLocations List a site's locations
// (GET /api/v1/inventory/sites/{id}/locations)
//
// Flat, by full code, filtered by status and by q — text the full code or the
// name contains, ignoring case — and cut at 10,000, saying so in truncated.
func (s *server) ListInventorySiteLocations(ctx context.Context, req gen.ListInventorySiteLocationsRequestObject) (gen.ListInventorySiteLocationsResponseObject, error) {
	var out gen.InventoryLocationList
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		if _, err := q.GetSite(ctx, req.Id); noRows(err) {
			return notFound()
		} else if err != nil {
			return fmt.Errorf("inventory: read site %d: %w", req.Id, err)
		}
		var status *string
		if req.Params.Status != nil {
			status = ptr(string(*req.Params.Status))
		}
		var text *string
		if req.Params.Q != nil && strings.TrimSpace(*req.Params.Q) != "" {
			text = ptr(strings.TrimSpace(*req.Params.Q))
		}
		rows, err := q.ListSiteLocations(ctx, store.ListSiteLocationsParams{SiteID: req.Id, Status: status, Q: text})
		if err != nil {
			return fmt.Errorf("inventory: list the locations of site %d: %w", req.Id, err)
		}
		if len(rows) > maxSiteLocations {
			rows, out.Truncated = rows[:maxSiteLocations], true
		}
		out.Locations = make([]gen.InventoryLocation, 0, len(rows))
		for _, r := range rows {
			out.Locations = append(out.Locations, locationOf(r))
		}
		return nil
	})
	if status, _, ok := answerOf(err); ok && status == http.StatusNotFound {
		return gen.ListInventorySiteLocations404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	return gen.ListInventorySiteLocations200JSONResponse(out), nil
}
