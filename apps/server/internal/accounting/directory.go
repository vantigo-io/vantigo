package accounting

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/accounting/store"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// The supplier register as other modules read it (purchasing design P1,
// A8): contracts.SupplierDirectory, read with no permission of its own and
// before the books are set up, and its one locking read, LockSuppliers, on
// the caller's transaction (module-boundaries rule 28).

// supplierDirectory is the module's contracts.SupplierDirectory, read on
// the module's pool.
type supplierDirectory struct{ pool store.DBTX }

var _ contracts.SupplierDirectory = supplierDirectory{}

// newSupplierDirectory is Module's SupplierDirectory.
func newSupplierDirectory(d module.Deps) contracts.SupplierDirectory {
	return supplierDirectory{pool: d.Pool}
}

// directoryEntry is a supplier's row as the directory answers it.
func directoryEntry(id int64, number int32, name string, org *string, country string, address, postal, city, email *string,
	cur string, terms int16, active, person bool, anonymised *time.Time, revision int32) contracts.SupplierEntry {
	return contracts.SupplierEntry{ID: id, Number: number, Name: name, OrganisationNumber: deref(org), Country: country,
		AddressLine: deref(address), PostalCode: deref(postal), City: deref(city), Email: deref(email), Currency: cur,
		PaymentTermsDays: int(terms), Active: active, IsPerson: person, Anonymised: anonymised != nil, Revision: revision}
}

// Supplier is one supplier, nil when none has the id.
func (d supplierDirectory) Supplier(ctx context.Context, id int64) (*contracts.SupplierEntry, error) {
	got, err := d.Suppliers(ctx, []int64{id})
	if err != nil || len(got) == 0 {
		return nil, err
	}
	return &got[0], nil
}

// Suppliers is the suppliers named, in the order asked.
func (d supplierDirectory) Suppliers(ctx context.Context, ids []int64) ([]contracts.SupplierEntry, error) {
	if len(ids) > contracts.MaxSuppliers {
		return nil, fmt.Errorf("accounting: a supplier read names %d suppliers, more than %d", len(ids), contracts.MaxSuppliers)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := store.New(d.pool).DirectorySuppliers(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("accounting: read the suppliers: %w", err)
	}
	byID := make(map[int64]contracts.SupplierEntry, len(rows))
	for _, r := range rows {
		byID[r.ID] = directoryEntry(r.ID, r.Number, r.Name, r.OrganisationNumber, r.Country, r.AddressLine, r.PostalCode, r.City,
			r.Email, r.Currency, r.PaymentTermsDays, r.Active, r.IsPerson, r.AnonymisedAt, r.Revision)
	}
	out := make([]contracts.SupplierEntry, 0, len(ids))
	for _, id := range ids {
		if e, ok := byID[id]; ok {
			out = append(out, e)
		}
	}
	return out, nil
}

// SearchSuppliers is at most limit suppliers matching query, by number.
func (d supplierDirectory) SearchSuppliers(ctx context.Context, query string, limit int) ([]contracts.SupplierEntry, error) {
	query = strings.TrimSpace(query)
	if query == "" || limit <= 0 {
		return nil, nil
	}
	rows, err := store.New(d.pool).SearchDirectorySuppliers(ctx, store.SearchDirectorySuppliersParams{Q: query,
		RowLimit: int32(min(limit, contracts.MaxSuppliers))})
	if err != nil {
		return nil, fmt.Errorf("accounting: search the suppliers: %w", err)
	}
	out := make([]contracts.SupplierEntry, 0, len(rows))
	for _, r := range rows {
		out = append(out, directoryEntry(r.ID, r.Number, r.Name, r.OrganisationNumber, r.Country, r.AddressLine, r.PostalCode,
			r.City, r.Email, r.Currency, r.PaymentTermsDays, r.Active, r.IsPerson, r.AnonymisedAt, r.Revision))
	}
	return out, nil
}

// LockSuppliers takes FOR SHARE on the suppliers, in id order, on tx.
func (d supplierDirectory) LockSuppliers(ctx context.Context, tx pgx.Tx, ids []int64) ([]contracts.SupplierEntry, error) {
	if len(ids) > contracts.MaxSuppliers {
		return nil, fmt.Errorf("accounting: a supplier lock names %d suppliers, more than %d", len(ids), contracts.MaxSuppliers)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	sorted := slices.Compact(slices.Sorted(slices.Values(ids)))
	rows, err := store.New(tx).LockDirectorySuppliers(ctx, sorted)
	if err != nil {
		return nil, fmt.Errorf("accounting: lock the suppliers: %w", err)
	}
	out := make([]contracts.SupplierEntry, 0, len(rows))
	for _, r := range rows {
		out = append(out, directoryEntry(r.ID, r.Number, r.Name, r.OrganisationNumber, r.Country, r.AddressLine, r.PostalCode,
			r.City, r.Email, r.Currency, r.PaymentTermsDays, r.Active, r.IsPerson, r.AnonymisedAt, r.Revision))
	}
	return out, nil
}
