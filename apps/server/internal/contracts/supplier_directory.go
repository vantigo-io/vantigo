package contracts

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// MaxSuppliers is the most suppliers one SupplierDirectory read or lock names.
const MaxSuppliers = 1000

// SupplierDirectory is Accounting's register of suppliers as another module
// reads it (purchasing design P1): a single-provider slot Accounting fills,
// nil when Accounting is off. It answers before the books are set up (P2).
// The rules a provider keeps:
//
//   - It checks no user permission, as every directory: the caller's own
//     endpoints decide who sees a supplier's name.
//   - Supplier, Suppliers and SearchSuppliers read on the provider's pool,
//     before the caller opens a transaction (rule 21).
//   - An id that names no supplier is simply absent; Supplier answers nil.
//   - The bank account, the default account and the default tax code are
//     the books' and are not on the entry.
type SupplierDirectory interface {
	// Supplier is one supplier, nil when none has the id.
	Supplier(ctx context.Context, id int64) (*SupplierEntry, error)
	// Suppliers is the suppliers named, in the order asked, an id that names
	// none absent. More than MaxSuppliers ids is a failure.
	Suppliers(ctx context.Context, ids []int64) ([]SupplierEntry, error)
	// SearchSuppliers is at most limit suppliers whose name, number or
	// organisation number matches query, by number; inactive ones too.
	SearchSuppliers(ctx context.Context, query string, limit int) ([]SupplierEntry, error)
	// LockSuppliers takes FOR SHARE on the suppliers named, in id order, ON
	// the caller's tx, and answers their entries, an id that names none
	// absent (module-boundaries rule 28): what a caller holds while it
	// writes a row that names a supplier — an order created or sent — so
	// Accounting's delete and anonymisation, which take the supplier FOR
	// UPDATE before asking the holders, either see the caller's row or wait
	// for it. It never begins, commits or rolls back tx, reads no contract,
	// and is called after ProductCatalog.LockVariants and before the
	// caller's own rows.
	LockSuppliers(ctx context.Context, tx pgx.Tx, ids []int64) ([]SupplierEntry, error)
}

// SupplierEntry is a supplier as the directory answers it. Text that is not
// set is "".
type SupplierEntry struct {
	ID                 int64
	Number             int32
	Name               string
	OrganisationNumber string
	Country            string // ISO 3166 alpha-2
	AddressLine        string
	PostalCode         string
	City               string
	Email              string
	Currency           string // ISO 4217
	PaymentTermsDays   int
	Active             bool
	// IsPerson is a sole proprietor: personal data (SupplierPersonalData).
	IsPerson bool
	// Anonymised is a person supplier whose record was taken out after its
	// retention: the name is "Anonymised supplier N" and every contact field
	// is empty.
	Anonymised bool
}
