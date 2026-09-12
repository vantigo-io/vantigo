package customers

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/customers/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// directory is this module's contracts.CustomerDirectory, the one sanctioned
// way another module reads customer data (.NET's ICustomerDirectory,
// SV/ApplicationServiceCollectionExtensions.cs:20-107). It is read-only and
// holds nothing but the queries: Compose builds it once, before any module
// mounts, and hands it to every module including this one.
type directory struct {
	q *store.Queries
}

var _ contracts.CustomerDirectory = (*directory)(nil)

// newDirectory is Module's Directory: the constructor Compose calls with the
// dependencies it was given.
func newDirectory(d module.Deps) contracts.CustomerDirectory {
	return &directory{q: store.New(d.Pool)}
}

// Customer looks up a customer by id, archived ones included: a supply period
// or a conversation can hold a reference to a customer that has since been
// archived, and must still be able to name it.
func (d *directory) Customer(ctx context.Context, id int32) (*contracts.CustomerEntry, error) {
	row, err := d.q.DirectoryCustomer(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("customers: directory customer: %w", err)
	}
	return &contracts.CustomerEntry{ID: row.ID, Name: row.Name, Archived: row.Archived}, nil
}

// Contact looks up a contact by id.
func (d *directory) Contact(ctx context.Context, id int32) (*contracts.ContactEntry, error) {
	row, err := d.q.DirectoryContact(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("customers: directory contact: %w", err)
	}
	return &contracts.ContactEntry{ID: row.ID, FirstName: row.FirstName, LastName: row.LastName, Email: row.Email}, nil
}

// ContactsByEmail finds every contact reachable at email, each with the
// customers that match makes candidates. The address is trimmed and
// lower-cased first, as .NET normalised it before comparing. More than one
// match is ambiguous, and resolving it is the caller's business, not the
// directory's.
func (d *directory) ContactsByEmail(ctx context.Context, email string) ([]contracts.ContactMatch, error) {
	rows, err := d.q.DirectoryContactsByEmail(ctx, normalizeEmail(email))
	if err != nil {
		return nil, fmt.Errorf("customers: directory contacts by email: %w", err)
	}
	matches := make([]contracts.ContactMatch, 0, len(rows))
	for _, row := range rows {
		matches = append(matches, contracts.ContactMatch{
			ContactID:            row.ContactID,
			CandidateCustomerIDs: row.CandidateCustomerIds,
		})
	}
	return matches, nil
}

// normalizeEmail is the comparison form of an address: trimmed and
// lower-cased, .NET's Trim().ToLowerInvariant().
func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}
