package staff

import (
	"context"

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
