package contracts

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// SupplierReferenceHolder is a module that keeps documents naming one of
// Accounting's suppliers in its own schema, which the supplier's delete and
// its anonymisation must not outrun (purchasing design P10, A8;
// module-boundaries rule 28). Accounting asks every holder INSIDE its own
// transaction, after it has taken the supplier FOR UPDATE and before it
// writes; the first hold refuses the write with 409. The rules are rule 8's:
//
//   - It never begins, commits or rolls back a transaction, and never touches
//     a pool: tx is the caller's. Its SQL is its own, on its own schema.
//   - It never reads a directory or any other contract, and never calls back
//     into Accounting.
//   - It locks nothing of its own beyond what its read takes: a writer of
//     its rows holds the supplier FOR SHARE (SupplierDirectory.LockSuppliers)
//     and so has committed, or waits, before the answer is read.
//   - It runs whether or not its module is enabled: every schema is migrated
//     whatever MODULES says, and a document in the schema still names the
//     supplier.
type SupplierReferenceHolder interface {
	// SupplierHeld answers what the holder keeps of the supplier that should
	// stop the write purpose names, nil when it keeps nothing that should.
	SupplierHeld(ctx context.Context, tx pgx.Tx, supplierID int64, purpose SupplierHoldPurpose) (*SupplierHold, error)
}

// SupplierHoldPurpose is the write Accounting asks a SupplierReferenceHolder
// about.
type SupplierHoldPurpose string

const (
	// SupplierHoldDelete is the supplier's delete: any document naming it
	// holds it.
	SupplierHoldDelete SupplierHoldPurpose = "delete"
	// SupplierHoldAnonymise is a person supplier's anonymisation: an open
	// document naming it holds it, as one still being worked on needs the
	// name.
	SupplierHoldAnonymise SupplierHoldPurpose = "anonymise"
)

// The kinds of SupplierHold.
const (
	// SupplierHoldKindDocuments: documents name the supplier.
	SupplierHoldKindDocuments = "documents"
	// SupplierHoldKindOpenDocuments: open documents name the supplier.
	SupplierHoldKindOpenDocuments = "open_documents"
)

// SupplierHold is what a holder keeps of a supplier: Kind, and the number of
// documents in Documents.
type SupplierHold struct {
	Kind      string
	Documents int
}

// SupplierPersonalData is what a module holds about one of Accounting's
// suppliers as a person (purchasing design P7, A8; module-boundaries rule 27):
// a read for the access request Accounting answers, a write for its
// anonymisation. It is a many-provider slot, collected from every module
// given, enabled or not, and the supplier's counterpart of
// CustomerPersonalData, with its rules:
//
//   - ExportSupplierData answers the module's section of a person supplier's
//     export, a JSON-serialisable value, nil when it holds nothing for the
//     id. It runs outside any transaction of the caller's, on the module's
//     own pool.
//   - EraseSupplierData runs INSIDE Accounting's anonymisation transaction,
//     which holds the supplier row FOR UPDATE, and blanks or removes what the
//     module holds about the person on its own schema. It never begins or
//     ends a transaction, never reads a directory or any other contract, and
//     never leaves the process: an object in a store is written to a
//     deletion ledger of the module's own that a worker of its own finishes,
//     never deleted inside tx. It reports what it did, kind by kind, a kind
//     it looked at included at zero, and run again for an id it already
//     erased reports zeros.
type SupplierPersonalData interface {
	ExportSupplierData(ctx context.Context, supplierID int64) (any, error)
	EraseSupplierData(ctx context.Context, tx pgx.Tx, supplierID int64) ([]ErasedData, error)
}

// SupplierPersonalDataHolder is one module's SupplierPersonalData under the
// module's name, the export's key for its section, as
// CustomerPersonalDataHolder is.
type SupplierPersonalDataHolder struct {
	Module string
	Data   SupplierPersonalData
}
