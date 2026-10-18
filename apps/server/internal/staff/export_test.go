package staff

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/mail"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// InOpenTx exposes inOpenTx to the external tests, whose contract-call hook
// uses it to tell a call made from inside one of this module's transactions —
// a locked one or a read-only snapshot — from one made outside any.
var InOpenTx = inOpenTx

// SetContractCallHook installs the hook every call out of this module is
// reported to (contractscalls.go). The harness installs it once, from
// TestMain, before any test runs: a package-level hook written while other
// tests are making requests would be a data race of the tests' own making.
func SetContractCallHook(hook func(ctx context.Context, method string)) {
	contractCallHook = hook
}

// NoteContractCall is the report every call out of the module makes, for the
// test that drives the harness's recorder through it.
var NoteContractCall = noteContractCall

// ReadTxContext marks ctx as withReadTx does, and LockedContext as
// withLockedTx and withPersonLock do, for the recorder's own test.
func ReadTxContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, openTxKey{}, true)
}

// LockedContext marks ctx as a locked transaction's.
func LockedContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, lockedTxKey{}, true)
}

// PersonForUser is the audience resolver's link lookup, on d's pool.
func PersonForUser(ctx context.Context, d module.Deps, userID uuid.UUID) (*int64, error) {
	return personForUser(ctx, store.New(d.Pool), userID)
}

// SMTPSend is the mail accessor of a server built over d, so the harness's own
// test can prove the seam reaches its fake and is refused inside a
// transaction.
func SMTPSend(ctx context.Context, d module.Deps, out mail.Outbound) error {
	return (&server{deps: d}).smtpSend(ctx, d.Config.Mail, out)
}

// MailAvailable is mailAvailable for a server built over d.
func MailAvailable(d module.Deps) bool {
	return (&server{deps: d}).mailAvailable()
}

// DocumentUploadsPerHour is the upload's rate limit, for the test that runs
// into it.
const DocumentUploadsPerHour = documentUploadsPerHour

// ColumnClasses is the retention class of every column the erase reaches,
// keyed table.column, for the external tests that assert an erase column by
// column.
func ColumnClasses() map[string]string { return columnClasses }

// FieldsOfClass is the payload fields of one retention class, for the test
// that asserts an erase field by field.
func FieldsOfClass(class string) []string { return fieldsOfClass(class) }

// SetRetentionRunLedgerBudget bounds POST /retention/run's ledger pass at d
// for one test, which must not run in parallel, and restores the budget
// after it.
func SetRetentionRunLedgerBudget(t *testing.T, d time.Duration) {
	before := retentionRunLedgerBudget
	retentionRunLedgerBudget = d
	t.Cleanup(func() { retentionRunLedgerBudget = before })
}

// RetentionLeaseKey is the retention worker's advisory lease, for the test
// that holds it from outside to see the run refused as busy.
const RetentionLeaseKey = retentionLeaseKey

// OnDisableCommitted runs fn when Disable now for personID has committed the
// flag off, before it claims the link — the moment a test makes the worker
// take the link — for the rest of t.
func OnDisableCommitted(t *testing.T, personID int64, fn func()) {
	t.Helper()
	disableCommittedHooks.Store(personID, fn)
	t.Cleanup(func() { disableCommittedHooks.Delete(personID) })
}
