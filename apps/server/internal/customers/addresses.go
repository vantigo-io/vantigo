package customers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/customers/gen"
	"github.com/vantigo-io/vantigo/server/internal/customers/store"
	"github.com/vantigo-io/vantigo/server/internal/db"
)

// This file is GET/POST /customers/{id}/addresses and
// PUT/DELETE /customers/{id}/addresses/{addressId} (invoice-ready customer
// design D1, D3): a customer's typed addresses — postal, invoice, delivery,
// visiting — any number of each, exactly one primary per type while any
// address of that type exists. No .NET ancestor, since addresses are new to
// this port.
//
// Every write here (controller ruling) locks the customer row FOR NO KEY
// UPDATE first (queries/addresses.sql's LockCustomer), then does everything
// else inside that same transaction: address writes carry no revision of
// their own and never touch customers.customers' own columns (D3 — "an
// address is small, and last-writer-wins on one is acceptable"), so the
// customer lock, not a revision guard, is what serializes two concurrent
// writers. The timeline actor is always resolved before the transaction
// opens (customers foundation design D1) — including on a write that turns
// out to 404, since the 404 is only known once inside the transaction, one
// wasted directory call.
//
// maxCustomerAddresses is D3's own cap, checked under the customer lock so
// two concurrent creates can never both slip in as the 50th and 51st.
const maxCustomerAddresses = 50

// addressSnapshot is one typed address's normalized value, exactly the
// shape customers.customer_addresses' eight content columns store (D3): the
// type timeline_events.go's three address recorders use for the payload's
// before/after (customer.address_updated) or single snapshot
// (customer.address_added/_removed). json tags let it double as that
// payload's shape directly, the same convention contactInfo follows.
type addressSnapshot struct {
	Type       string  `json:"type"`
	Label      *string `json:"label"`
	Line1      string  `json:"line1"`
	Line2      *string `json:"line2"`
	PostalCode *string `json:"postalCode"`
	City       *string `json:"city"`
	Region     *string `json:"region"`
	Country    string  `json:"country"`
	IsPrimary  bool    `json:"isPrimary"`
}

func addressSnapshotFromRow(r store.CustomersCustomerAddress) addressSnapshot {
	return addressSnapshot{
		Type: r.Type, Label: r.Label, Line1: r.Line1, Line2: r.Line2,
		PostalCode: r.PostalCode, City: r.City, Region: r.Region, Country: r.Country, IsPrimary: r.IsPrimary,
	}
}

// addressDisplay is D3's own one-line rendering, e.g. "Storgata 1, 0155
// Oslo, NO" (the controller ruling's own example) — line1, an optional
// line2, "postalCode city" when either is set, and the country code
// upper-cased (validateCountryCode stores it lower-cased) — joined with
// ", ", blank parts dropped.
func addressDisplay(a addressSnapshot) string {
	parts := []string{a.Line1}
	if a.Line2 != nil {
		parts = append(parts, *a.Line2)
	}
	var cityLine string
	if a.PostalCode != nil {
		cityLine = *a.PostalCode
	}
	if a.City != nil {
		if cityLine != "" {
			cityLine += " "
		}
		cityLine += *a.City
	}
	if cityLine != "" {
		parts = append(parts, cityLine)
	}
	parts = append(parts, strings.ToUpper(a.Country))
	return strings.Join(parts, ", ")
}

// addressResponse is CustomerAddress.FromDomain: every address query
// selects exactly customer_addresses' own columns, so sqlc always hands
// this a store.CustomersCustomerAddress regardless of which query produced
// it, the same convention contactResponse follows for contacts.
func addressResponse(r store.CustomersCustomerAddress) gen.CustomerAddress {
	return gen.CustomerAddress{
		Id: r.ID, Type: r.Type, Label: r.Label, Line1: r.Line1, Line2: r.Line2,
		PostalCode: r.PostalCode, City: r.City, Region: r.Region, Country: r.Country,
		IsPrimary: r.IsPrimary, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

// errCustomerNotFound is the shared 404 sentinel every address write's
// transaction raises once LockCustomer (or, for PUT/DELETE, the address
// lookup that follows it) finds nothing — threaded out of the transaction
// fn as a sentinel error so db.WithTx's own error path stays a plain "did
// it fail" signal, the same technique contacts.go's
// errAssociationTargetNotFound uses.
var errCustomerNotFound = errors.New("customers: customer or address not found")

// errAddressCapReached is PostCustomersByIdAddresses' 400 when the customer
// is already at the 50-address cap (D3).
var errAddressCapReached = errors.New("customers: address cap reached")

// errPrimaryTransitionRefused is PutCustomersByIdAddressesByAddressId's 400
// when a same-type request tries to demote the address that is currently
// the only or primary one of its type without naming a replacement
// (controller ruling's exact wording).
var errPrimaryTransitionRefused = errors.New("customers: primary address transition refused")

// primaryTransitionProblem is errPrimaryTransitionRefused's body: the one
// address-write refusal that is not field-shaped the way validateAddress's
// are, so it is its own small helper rather than folded into validateAddress
// itself.
func primaryTransitionProblem() gen.PutCustomersByIdAddressesByAddressId400ApplicationProblemPlusJSONResponse {
	return gen.PutCustomersByIdAddressesByAddressId400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem("Invalid address", map[string][]string{
		"isPrimary": {"An address that is the only or primary one of its type stays primary; make another one primary instead"},
	}))
}

// demoteCurrentPrimary is the shared demote-before-promote step
// PostCustomersByIdAddresses and PutCustomersByIdAddressesByAddressId both
// need before setting a different address of addrType primary (controller
// ruling: "demote-before-promote order matters for the partial unique
// index"). A missing current primary (the type has no addresses yet) is not
// an error — there is simply nothing to demote.
func demoteCurrentPrimary(ctx context.Context, txq *store.Queries, customerID int32, addrType string, now time.Time) error {
	current, err := txq.PrimaryCustomerAddressOfType(ctx, store.PrimaryCustomerAddressOfTypeParams{CustomerID: customerID, Type: addrType})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return txq.SetCustomerAddressPrimary(ctx, store.SetCustomerAddressPrimaryParams{ID: current.ID, IsPrimary: false, UpdatedAt: now})
}

// GetCustomersByIdAddresses List a customer's addresses
// (GET /api/v1/customers/{id}/addresses)
//
// Read-only: no lock, and an archived customer's addresses list exactly the
// same as any other's (D1's controller ruling — archive blocks nothing in
// this module). Ordering is ListCustomerAddresses's own (queries/addresses.sql):
// type in the fixed order invoice/postal/delivery/visiting, primary first,
// then id.
func (s *server) GetCustomersByIdAddresses(ctx context.Context, req gen.GetCustomersByIdAddressesRequestObject) (gen.GetCustomersByIdAddressesResponseObject, error) {
	q := store.New(s.deps.Pool)
	if _, err := q.GetCustomer(ctx, req.Id); errors.Is(err, pgx.ErrNoRows) {
		return gen.GetCustomersByIdAddresses404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("customers: get customer: %w", err)
	}

	rows, err := q.ListCustomerAddresses(ctx, req.Id)
	if err != nil {
		return nil, fmt.Errorf("customers: list addresses: %w", err)
	}
	data := make([]gen.CustomerAddress, 0, len(rows))
	for _, r := range rows {
		data = append(data, addressResponse(r))
	}
	return gen.GetCustomersByIdAddresses200JSONResponse{Data: data}, nil
}

// PostCustomersByIdAddresses Add an address to a customer
// (POST /api/v1/customers/{id}/addresses)
//
// Ordering (controller ruling): (1) field validation, 400; (2) the actor,
// resolved before the transaction opens — a write always happens once
// validation passes, the 404/cap refusals included, since both are only
// known inside the transaction and the cost of one wasted directory call is
// accepted rather than resolving it twice; (3) inside the transaction: lock
// the customer row (404 if missing), the 50-address cap (400 if already at
// it), then the primary-flag bookkeeping — the first address of its type is
// primary whatever the request says; otherwise the request's own isPrimary
// (absent means false), demoting the type's current primary first if the
// new address is to replace it.
func (s *server) PostCustomersByIdAddresses(ctx context.Context, req gen.PostCustomersByIdAddressesRequestObject) (gen.PostCustomersByIdAddressesResponseObject, error) {
	body := gen.CustomerAddressRequest{}
	if req.Body != nil {
		body = *req.Body
	}

	parsed, errs := validateAddress(body.Type, body.Label, body.Line1, body.Line2, body.PostalCode, body.City, body.Region, body.Country)
	if errs != nil {
		return gen.PostCustomersByIdAddresses400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem("Invalid address", errs)), nil
	}
	requestedPrimary := body.IsPrimary != nil && *body.IsPrimary

	// Resolved before the transaction opens (customers foundation design D1,
	// actor.go): a successful write always follows past validation, so the
	// actor is always needed even though the 404/cap refusals waste the call.
	act, err := s.actorFor(ctx, generatedFallbackActor)
	if err != nil {
		return nil, fmt.Errorf("customers: resolve actor: %w", err)
	}

	now := s.deps.Clock()
	var created store.CustomersCustomerAddress
	var capProblem gen.PostCustomersByIdAddresses400ApplicationProblemPlusJSONResponse
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		if _, err := txq.LockCustomer(ctx, req.Id); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errCustomerNotFound
			}
			return err
		}

		total, err := txq.CountCustomerAddresses(ctx, req.Id)
		if err != nil {
			return err
		}
		if total >= maxCustomerAddresses {
			capProblem = gen.PostCustomersByIdAddresses400ApplicationProblemPlusJSONResponse(
				apicommon.ValidationProblem("Invalid address", map[string][]string{"addresses": {"A customer can have at most 50 addresses"}}))
			return errAddressCapReached
		}

		countOfType, err := txq.CountCustomerAddressesOfType(ctx, store.CountCustomerAddressesOfTypeParams{CustomerID: req.Id, Type: parsed.Type, ExcludeID: 0})
		if err != nil {
			return err
		}
		isPrimary := requestedPrimary
		if countOfType == 0 {
			isPrimary = true
		} else if requestedPrimary {
			if err := demoteCurrentPrimary(ctx, txq, req.Id, parsed.Type, now); err != nil {
				return err
			}
		}

		created, err = txq.InsertCustomerAddress(ctx, store.InsertCustomerAddressParams{
			CustomerID: req.Id, Type: parsed.Type, Label: parsed.Label, Line1: parsed.Line1, Line2: parsed.Line2,
			PostalCode: parsed.PostalCode, City: parsed.City, Region: parsed.Region, Country: parsed.Country,
			IsPrimary: isPrimary, Now: now,
		})
		if err != nil {
			return err
		}
		return recordCustomerAddressAdded(ctx, txq, now, req.Id, created.ID, addressSnapshotFromRow(created), act.Kind, act.Display, act.UserID)
	})
	switch {
	case errors.Is(err, errCustomerNotFound):
		return gen.PostCustomersByIdAddresses404Response{}, nil
	case errors.Is(err, errAddressCapReached):
		return capProblem, nil
	case err != nil:
		return nil, fmt.Errorf("customers: create address: %w", err)
	}

	location := fmt.Sprintf("%s/api/v1/customers/%d/addresses/%d", s.deps.Config.BasePath, req.Id, created.ID)
	return gen.PostCustomersByIdAddresses201JSONResponse{
		Body:    addressResponse(created),
		Headers: gen.PostCustomersByIdAddresses201ResponseHeaders{Location: &location},
	}, nil
}

// PutCustomersByIdAddressesByAddressId Replace a customer's address
// (PUT /api/v1/customers/{id}/addresses/{addressId})
//
// A full replace of every request field (D1, controller ruling); isPrimary
// absent means false. Same-type and type-changing writes need different
// primary-flag bookkeeping (controller ruling):
//
//   - same type: false on the address that is currently this type's primary
//     is refused (400) — there is always a primary while any address of the
//     type exists; true on an address that was not already primary demotes
//     the type's current primary first, in the order the partial unique
//     index needs.
//   - type changed: the address leaves its old type (promoting the oldest
//     remaining address there, if any, since this one no longer counts) and
//     joins the new type (primary if it is the first address there, or if
//     isPrimary is true — demoting the new type's current primary first).
//     The demote-before-promote order matters here too: the address's own
//     row is updated (leaving the old type, landing in the new one) before
//     the old type's oldest remaining address is promoted, so the two never
//     transiently collide on the old type's partial unique index.
//
// A "make primary" that demotes a different address records one event, for
// the address the request named, never two (controller ruling;
// timeline_events.go's recordCustomerAddressUpdated).
func (s *server) PutCustomersByIdAddressesByAddressId(ctx context.Context, req gen.PutCustomersByIdAddressesByAddressIdRequestObject) (gen.PutCustomersByIdAddressesByAddressIdResponseObject, error) {
	body := gen.CustomerAddressRequest{}
	if req.Body != nil {
		body = *req.Body
	}

	parsed, errs := validateAddress(body.Type, body.Label, body.Line1, body.Line2, body.PostalCode, body.City, body.Region, body.Country)
	if errs != nil {
		return gen.PutCustomersByIdAddressesByAddressId400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem("Invalid address", errs)), nil
	}
	requestedPrimary := body.IsPrimary != nil && *body.IsPrimary

	// Resolved before the transaction opens (customers foundation design D1):
	// a write always happens once the address is found, so the actor is
	// always needed past that point; the 404 case wastes one directory call,
	// the same trade-off contact_info.go's own PUT accepts.
	act, err := s.actorFor(ctx, generatedFallbackActor)
	if err != nil {
		return nil, fmt.Errorf("customers: resolve actor: %w", err)
	}

	now := s.deps.Clock()
	var before, after addressSnapshot
	var updated store.CustomersCustomerAddress
	var primaryProblem gen.PutCustomersByIdAddressesByAddressId400ApplicationProblemPlusJSONResponse
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		if _, err := txq.LockCustomer(ctx, req.Id); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errCustomerNotFound
			}
			return err
		}

		existing, err := txq.GetCustomerAddress(ctx, store.GetCustomerAddressParams{ID: req.AddressId, CustomerID: req.Id})
		if errors.Is(err, pgx.ErrNoRows) {
			return errCustomerNotFound
		}
		if err != nil {
			return err
		}
		before = addressSnapshotFromRow(existing)

		var isPrimary bool
		if parsed.Type == existing.Type {
			if existing.IsPrimary && !requestedPrimary {
				primaryProblem = primaryTransitionProblem()
				return errPrimaryTransitionRefused
			}
			isPrimary = requestedPrimary || existing.IsPrimary
			if isPrimary && !existing.IsPrimary {
				if err := demoteCurrentPrimary(ctx, txq, req.Id, parsed.Type, now); err != nil {
					return err
				}
			}
		} else {
			countOfNewType, err := txq.CountCustomerAddressesOfType(ctx, store.CountCustomerAddressesOfTypeParams{CustomerID: req.Id, Type: parsed.Type, ExcludeID: req.AddressId})
			if err != nil {
				return err
			}
			if countOfNewType == 0 {
				isPrimary = true
			} else {
				isPrimary = requestedPrimary
				if requestedPrimary {
					if err := demoteCurrentPrimary(ctx, txq, req.Id, parsed.Type, now); err != nil {
						return err
					}
				}
			}
		}

		updated, err = txq.UpdateCustomerAddress(ctx, store.UpdateCustomerAddressParams{
			ID: req.AddressId, CustomerID: req.Id,
			Type: parsed.Type, Label: parsed.Label, Line1: parsed.Line1, Line2: parsed.Line2,
			PostalCode: parsed.PostalCode, City: parsed.City, Region: parsed.Region, Country: parsed.Country,
			IsPrimary: isPrimary, UpdatedAt: now,
		})
		if err != nil {
			return err
		}
		after = addressSnapshotFromRow(updated)

		if parsed.Type != existing.Type && existing.IsPrimary {
			oldest, err := txq.OldestCustomerAddressOfType(ctx, store.OldestCustomerAddressOfTypeParams{CustomerID: req.Id, Type: existing.Type, ExcludeID: req.AddressId})
			if errors.Is(err, pgx.ErrNoRows) {
				// Nothing remains of the old type — nothing to promote.
			} else if err != nil {
				return err
			} else if err := txq.SetCustomerAddressPrimary(ctx, store.SetCustomerAddressPrimaryParams{ID: oldest.ID, IsPrimary: true, UpdatedAt: now}); err != nil {
				return err
			}
		}

		return recordCustomerAddressUpdated(ctx, txq, now, req.Id, req.AddressId, before, after, act.Kind, act.Display, act.UserID)
	})
	switch {
	case errors.Is(err, errCustomerNotFound):
		return gen.PutCustomersByIdAddressesByAddressId404Response{}, nil
	case errors.Is(err, errPrimaryTransitionRefused):
		return primaryProblem, nil
	case err != nil:
		return nil, fmt.Errorf("customers: update address: %w", err)
	}

	return gen.PutCustomersByIdAddressesByAddressId200JSONResponse(addressResponse(updated)), nil
}

// DeleteCustomersByIdAddressesByAddressId Remove an address from a customer
// (DELETE /api/v1/customers/{id}/addresses/{addressId})
//
// Unlike PUT, a delete of the only/primary address of its type is not
// refused: the invariant is "always a primary while any address of the type
// exists", which a delete of the last one simply makes vacuous. Deleting the
// primary promotes the oldest remaining address of the same type, if any
// (controller ruling); that promotion records no event of its own — only
// the removed address's own customer.address_removed does (the same "one
// event, not two" rule PUT's doc comment describes).
func (s *server) DeleteCustomersByIdAddressesByAddressId(ctx context.Context, req gen.DeleteCustomersByIdAddressesByAddressIdRequestObject) (gen.DeleteCustomersByIdAddressesByAddressIdResponseObject, error) {
	// Resolved before the transaction opens (customers foundation design D1):
	// this handler always records a customer.address_removed event once it
	// reaches here (the 404 case wastes one directory call).
	act, err := s.actorFor(ctx, generatedFallbackActor)
	if err != nil {
		return nil, fmt.Errorf("customers: resolve actor: %w", err)
	}

	now := s.deps.Clock()
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		if _, err := txq.LockCustomer(ctx, req.Id); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errCustomerNotFound
			}
			return err
		}

		existing, err := txq.GetCustomerAddress(ctx, store.GetCustomerAddressParams{ID: req.AddressId, CustomerID: req.Id})
		if errors.Is(err, pgx.ErrNoRows) {
			return errCustomerNotFound
		}
		if err != nil {
			return err
		}

		// The row itself is deleted before any promotion of a different
		// address of the same type: while the deleted row still exists with
		// is_primary=true, promoting another row of the same type would
		// transiently violate ux_customer_addresses_primary (two primaries
		// of one type at once) — deleting it first removes that row from the
		// index entirely, so the promotion below is always safe.
		if err := txq.DeleteCustomerAddress(ctx, store.DeleteCustomerAddressParams{ID: req.AddressId, CustomerID: req.Id}); err != nil {
			return err
		}

		if existing.IsPrimary {
			oldest, err := txq.OldestCustomerAddressOfType(ctx, store.OldestCustomerAddressOfTypeParams{CustomerID: req.Id, Type: existing.Type, ExcludeID: req.AddressId})
			if errors.Is(err, pgx.ErrNoRows) {
				// This was the last address of its type — nothing to promote.
			} else if err != nil {
				return err
			} else if err := txq.SetCustomerAddressPrimary(ctx, store.SetCustomerAddressPrimaryParams{ID: oldest.ID, IsPrimary: true, UpdatedAt: now}); err != nil {
				return err
			}
		}

		return recordCustomerAddressRemoved(ctx, txq, now, req.Id, req.AddressId, addressSnapshotFromRow(existing), act.Kind, act.Display, act.UserID)
	})
	switch {
	case errors.Is(err, errCustomerNotFound):
		return gen.DeleteCustomersByIdAddressesByAddressId404Response{}, nil
	case err != nil:
		return nil, fmt.Errorf("customers: delete address: %w", err)
	}
	return gen.DeleteCustomersByIdAddressesByAddressId204Response{}, nil
}
