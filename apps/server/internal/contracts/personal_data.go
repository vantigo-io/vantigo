package contracts

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// CustomerPersonalData is what a module holds about one customer as a person
// (customers GDPR design D2): a read for the export, a write for the
// anonymisation. It is the second sanctioned cross-module direction, beside
// CustomerReferenceHolder, and deliberately not a method on it: a merge moves
// references and keeps everything, an anonymisation keeps the references and
// takes the person out of them, and a module may hold customer ids without
// holding anything about a person (docs/module-boundaries.md rule 9).
//
// ExportCustomerData answers the module's section of a private person's
// export: a JSON-serialisable value, nil when the module holds nothing for the
// id. It runs outside any transaction of the caller's — nobody holds a lock
// while it reads — on the module's own pool, in a read-only snapshot of its
// own if it needs several statements to agree.
//
// EraseCustomerData runs INSIDE the customers module's anonymisation
// transaction, which holds the customer row locked, and removes or blanks what
// the module holds about the person, on its own schema, in its own code. It
// keeps the merge holder's rules, none of which the signature shows:
//
//   - It never begins, commits or rolls back a transaction, and never touches
//     a pool: tx is the caller's, and so is the decision. An error rolls the
//     customer's whole anonymisation back, every module's part with it, and
//     the worker tries the customer again next cycle.
//   - It never reads a directory or any other contract: no in-process lookup
//     happens under a lock in this codebase (the customers module's actor.go).
//   - It reports what it removed or blanked, kind by kind — a kind it looked
//     at and had to leave alone included, at zero — so the customer.anonymised
//     event says every module was asked. A kind is "<module>.<what>" in the
//     API's camelCase, as a RepointedReferences kind is.
//   - It runs whether or not its module is enabled, for the holder's reason:
//     every schema is migrated whatever MODULES says, so a module switched off
//     still holds what it held. Its constructor must therefore need nothing a
//     disabled module's Deps lacks.
//   - Run again for an id it already erased, it finds nothing and reports
//     zeros.
type CustomerPersonalData interface {
	ExportCustomerData(ctx context.Context, customerID int32) (any, error)
	EraseCustomerData(ctx context.Context, tx pgx.Tx, customerID int32) ([]ErasedData, error)
}

// ErasedData is one kind of thing a module removed or blanked for a person,
// and how many rows of it.
type ErasedData struct {
	Kind  string
	Count int64
}

// CustomerPersonalDataHolder is one module's CustomerPersonalData under the
// module's name. The name is the export's key for the module's section
// (modules.communications, modules.energy, ...): the interface carries none,
// and Compose, which knows it, pairs the two.
type CustomerPersonalDataHolder struct {
	Module string
	Data   CustomerPersonalData
}
