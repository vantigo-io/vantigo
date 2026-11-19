package customers

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/customers/gen"
	"github.com/vantigo-io/vantigo/server/internal/customers/store"
	"github.com/vantigo-io/vantigo/server/internal/db"
)

// This file is the default brand of a customer and of a customer group (brands
// design B28, delivery E1): PUT /customers/{id}/brand, GET /customers/brands,
// the brandId a group carries, and the brand block on every customer response.
//
// The order a document's brand is picked in is the person's choice, then the
// customer's own brand, then its group's, then the workspace default brand.
// This module owns the two middle steps and resolves them on every read, never
// stored: a brand archived since is skipped (the customer's default falls back
// to the next in the order, and the response says so in unavailable) rather than
// blocking the brand's archive or leaving a customer without one. No foreign
// key stands under the columns — a brand is identity's row, reached only through
// contracts.Brands — so a write checks the id there, and an id that no longer
// resolves is tolerated at read.
//
// A write is refused for an unknown or archived brand only when it CHANGES the
// value: a form that saves a customer's other fields again, or a group renamed,
// may carry a brand archived since without being refused for state it is already
// in (owner.go's disabled-owner rule, the same reason).
//
// A brand changes a document's look and name, and never the seller's legal
// identity (brands design B29a): the legal entity, the VAT registration and the
// bank account are Invoices' single settings, and the pages say so.

// brandNotFound is the field error for a brandId no published brand holds,
// worded as groupNotFound (group_membership.go) words its own.
func brandNotFound(id uuid.UUID) string {
	return fmt.Sprintf("Brand %s is not a published brand", id)
}

// brandChoiceOf is a chosen brand as the response shows it: published and
// archived brands by name, an id nobody resolves as unknown.
func brandChoiceOf(id uuid.UUID, entry *contracts.BrandEntry) gen.CustomerBrandChoice {
	if entry == nil {
		return gen.CustomerBrandChoice{Id: id, State: gen.CustomerBrandChoiceStateUnknown}
	}
	name := entry.Name
	state := gen.CustomerBrandChoiceStatePublished
	if entry.State == contracts.BrandStateArchived {
		state = gen.CustomerBrandChoiceStateArchived
	}
	return gen.CustomerBrandChoice{Id: id, Name: &name, State: state}
}

// brandLookup is what one response needs of contracts.Brands: the published
// brands (the picker's list, whose default is the workspace's) and the entries
// of the ids a customer or a group points at.
type brandLookup struct {
	published map[uuid.UUID]*contracts.BrandEntry
	other     map[uuid.UUID]*contracts.BrandEntry
	def       *contracts.BrandEntry
}

// entry is the brand with id, nil when no brand resolves it.
func (l brandLookup) entry(id uuid.UUID) *contracts.BrandEntry {
	if e, ok := l.published[id]; ok {
		return e
	}
	return l.other[id]
}

// loadBrands reads the published brands once and the entries of ids that are
// not among them (archived, or unknown) one by one. It is nil-safe: with no
// contracts.Brands (a harness without identity) nothing resolves.
func (s *server) loadBrands(ctx context.Context, ids []uuid.UUID) (brandLookup, error) {
	l := brandLookup{published: map[uuid.UUID]*contracts.BrandEntry{}, other: map[uuid.UUID]*contracts.BrandEntry{}}
	if s.deps.Brands == nil {
		return l, nil
	}
	list, err := s.deps.Brands.List(ctx)
	if err != nil {
		return brandLookup{}, fmt.Errorf("customers: list the brands: %w", err)
	}
	for i := range list {
		l.published[list[i].ID] = &list[i]
		if list[i].Default {
			l.def = &list[i]
		}
	}
	for _, id := range ids {
		if _, ok := l.published[id]; ok {
			continue
		}
		if _, ok := l.other[id]; ok {
			continue
		}
		e, err := s.deps.Brands.Get(ctx, id)
		if err != nil {
			return brandLookup{}, fmt.Errorf("customers: read a brand: %w", err)
		}
		l.other[id] = e // nil for an id nobody resolves
	}
	return l, nil
}

// effectiveBrand applies the order of B28 to a customer's own brand and its
// group's: the first one that is published, else the workspace default. A
// choice that is archived or unknown is skipped and named in unavailable.
func effectiveBrand(own, group *uuid.UUID, l brandLookup) gen.CustomerBrandInfo {
	info := gen.CustomerBrandInfo{Unavailable: []gen.CustomerBrandInfoUnavailable{}}
	usable := func(id *uuid.UUID, who gen.CustomerBrandInfoUnavailable) (*gen.CustomerBrandChoice, bool) {
		if id == nil {
			return nil, false
		}
		e := l.entry(*id)
		choice := brandChoiceOf(*id, e)
		if e == nil || e.State != contracts.BrandStatePublished {
			info.Unavailable = append(info.Unavailable, who)
			return &choice, false
		}
		return &choice, true
	}
	ownChoice, ownOK := usable(own, gen.CustomerBrandInfoUnavailableCustomer)
	groupChoice, groupOK := usable(group, gen.CustomerBrandInfoUnavailableGroup)
	info.Own, info.Group = ownChoice, groupChoice
	switch {
	case ownOK:
		info.Effective = gen.CustomerEffectiveBrand{Id: ownChoice.Id, Name: ownChoice.Name, Source: gen.CustomerEffectiveBrandSourceCustomer}
	case groupOK:
		info.Effective = gen.CustomerEffectiveBrand{Id: groupChoice.Id, Name: groupChoice.Name, Source: gen.CustomerEffectiveBrandSourceGroup}
	default:
		info.Effective = gen.CustomerEffectiveBrand{Source: gen.CustomerEffectiveBrandSourceDefault}
		if l.def != nil {
			name := l.def.Name
			info.Effective.Id, info.Effective.Name = l.def.ID, &name
		}
	}
	return info
}

// decorateBrands fills dec.brands for customerIDs: one batched query and the
// contracts.Brands reads. Like the rest of decorate it runs after the write has
// committed, never under a lock.
func (s *server) decorateBrands(ctx context.Context, q *store.Queries, customerIDs []int32, dec *customerDecoration) error {
	if s.deps.Brands == nil {
		return nil
	}
	rows, err := q.CustomerBrandsForCustomers(ctx, customerIDs)
	if err != nil {
		return fmt.Errorf("customers: load customer brands: %w", err)
	}
	var ids []uuid.UUID
	for _, r := range rows {
		if r.BrandID != nil {
			ids = append(ids, *r.BrandID)
		}
		if r.GroupBrandID != nil {
			ids = append(ids, *r.GroupBrandID)
		}
	}
	lookup, err := s.loadBrands(ctx, ids)
	if err != nil {
		return err
	}
	for _, r := range rows {
		dec.brands[r.CustomerID] = effectiveBrand(r.BrandID, r.GroupBrandID, lookup)
	}
	return nil
}

// brand is one customer's brand block, nil when the harness has no brands.
func (d customerDecoration) brand(customerID int32) *gen.CustomerBrandInfo {
	if b, ok := d.brands[customerID]; ok {
		return &b
	}
	return nil
}

// validateBrandChange checks a brand id a write would CHANGE to: it must be a
// published brand. An unchanged id passes untouched, whatever its state.
// field is the request's field name. The error is the message for that field;
// "" means valid.
func (s *server) validateBrandChange(ctx context.Context, current, wanted *uuid.UUID) (string, error) {
	if wanted == nil || (current != nil && *current == *wanted) {
		return "", nil
	}
	if s.deps.Brands == nil {
		return brandNotFound(*wanted), nil
	}
	e, err := s.deps.Brands.Get(ctx, *wanted)
	if err != nil {
		return "", fmt.Errorf("customers: read a brand: %w", err)
	}
	if e == nil || e.State != contracts.BrandStatePublished {
		return brandNotFound(*wanted), nil
	}
	return "", nil
}

// uuidPtrs is the ids among ptrs that are set.
func uuidPtrs(ptrs ...*uuid.UUID) []uuid.UUID {
	var out []uuid.UUID
	for _, p := range ptrs {
		if p != nil {
			out = append(out, *p)
		}
	}
	return out
}

// brandChoicePtr is a group's brand as its summary shows it, nil for none.
func brandChoicePtr(id *uuid.UUID, l brandLookup) *gen.CustomerBrandChoice {
	if id == nil {
		return nil
	}
	c := brandChoiceOf(*id, l.entry(*id))
	return &c
}

// GetCustomersBrands List the published brands a customer or a group can be
// given as its default (GET /api/v1/customers/brands)
//
// The picker's options, fed by contracts.Brands, the workspace default first.
// It needs customers:view and nothing from identity, so a person who edits a
// customer does not need the permission that manages brands. A harness without
// identity offers none.
func (s *server) GetCustomersBrands(ctx context.Context, _ gen.GetCustomersBrandsRequestObject) (gen.GetCustomersBrandsResponseObject, error) {
	out := []gen.CustomerBrandOption{}
	if s.deps.Brands != nil {
		list, err := s.deps.Brands.List(ctx)
		if err != nil {
			return nil, fmt.Errorf("customers: list the brands: %w", err)
		}
		for _, b := range list {
			out = append(out, gen.CustomerBrandOption{Id: b.ID, Name: b.Name, IsDefault: b.Default})
		}
	}
	return gen.GetCustomersBrands200JSONResponse(out), nil
}

// PutCustomersByIdBrand Set or clear a customer's default brand
// (PUT /api/v1/customers/{id}/brand)
//
// Ordering is PutCustomersByIdType's: (1) the customer lookup, 404; (2) a
// supplied revision that disagrees with the row just read, 409 — ahead of the
// no-op check; (3) the no-op check, which writes nothing (customers foundation
// design D5); (4) the brand's own validation through contracts.Brands, 400
// keyed brandId, for a CHANGE only; (5) the guarded write, which is a plain
// column write and records no timeline entry: a brand is how the customer is
// invoiced, not an event in the relationship.
func (s *server) PutCustomersByIdBrand(ctx context.Context, req gen.PutCustomersByIdBrandRequestObject) (gen.PutCustomersByIdBrandResponseObject, error) {
	body := gen.PutCustomerBrandRequest{}
	if req.Body != nil {
		body = *req.Body
	}
	q := store.New(s.deps.Pool)
	existing, err := q.GetCustomer(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PutCustomersByIdBrand404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("customers: get customer: %w", err)
	}
	if body.Revision != nil && *body.Revision != existing.Revision {
		return gen.PutCustomersByIdBrand409ApplicationProblemPlusJSONResponse(customerRevisionConflict(*body.Revision, existing.Revision)), nil
	}
	summary, err := q.CustomerTimelineSummary(ctx, req.Id)
	if err != nil {
		return nil, fmt.Errorf("customers: timeline summary: %w", err)
	}
	respond := func(row customerRow) (gen.PutCustomersByIdBrandResponseObject, error) {
		dec, err := s.decorate(ctx, q, row)
		if err != nil {
			return nil, err
		}
		return gen.PutCustomersByIdBrand200JSONResponse(safeCustomerResponse(row, s.hasPermission(ctx, legalIdentityView), dec)), nil
	}

	current, err := q.CustomerBrand(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PutCustomersByIdBrand404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("customers: read customer brand: %w", err)
	}
	if uuidPtrEqual(current, body.BrandId) {
		return respond(fromCustomerRow(existing, summary))
	}
	msg, err := s.validateBrandChange(ctx, current, body.BrandId)
	if err != nil {
		return nil, err
	}
	if msg != "" {
		return gen.PutCustomersByIdBrand400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem(
			"Invalid customer brand", map[string][]string{"brandId": {msg}})), nil
	}

	var updated store.SetCustomerBrandRow
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		if _, err := lockWritableCustomer(ctx, txq, req.Id); err != nil {
			return err
		}
		updated, err = txq.SetCustomerBrand(ctx, store.SetCustomerBrandParams{
			ID: req.Id, BrandID: body.BrandId, UpdatedAt: s.deps.Clock(), ExpectedRevision: body.Revision,
		})
		return err
	})
	switch {
	case isReadOnlyCustomer(err):
		return gen.PutCustomersByIdBrand409ApplicationProblemPlusJSONResponse(readOnlyProblem(err)), nil
	case errors.Is(err, pgx.ErrNoRows):
		fresh, ferr := q.GetCustomer(ctx, req.Id)
		if errors.Is(ferr, pgx.ErrNoRows) {
			return gen.PutCustomersByIdBrand404Response{}, nil
		}
		if ferr != nil {
			return nil, fmt.Errorf("customers: re-read customer after conflict: %w", ferr)
		}
		return gen.PutCustomersByIdBrand409ApplicationProblemPlusJSONResponse(customerRevisionConflict(existing.Revision, fresh.Revision)), nil
	case err != nil:
		return nil, fmt.Errorf("customers: set customer brand: %w", err)
	}
	return respond(fromSetCustomerBrandRow(updated, summary))
}
