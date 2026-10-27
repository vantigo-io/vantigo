package entra

import (
	"context"
	"errors"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// This file is where the directory picker and the exact matches (directory.go)
// reach identity: contracts.SSOConnections (deps.SSOConnections), the users
// not yet connected and who already holds a directory account's keys. Every
// call to it is made through one of the thin accessors below and through
// nowhere else, so "what does the directory ask of identity, and when" has
// one place to read the answer and one place to check it from — Staff's,
// Calendar's and Planning's pattern.
//
// What is checked is the lock rule (module-boundaries rule 10): no call out
// of the module is made inside one of its transactions. Identity reads
// through the same pool, so a transaction that waits for a second connection
// can starve it. A transaction's body that hands a context on marks it with
// lockedTxKey; every accessor reports its call to contractCallHook, which a
// test may install to record it, and refuses a call made inside one with
// errCallInsideTransaction: the mistake is a programming error, and failing
// the request loudly is better than holding a connection across identity's
// read. The directory's reads open no transaction today, so nothing marks a
// context yet; the check keeps a future one from calling out of it.

// errCallInsideTransaction is every accessor's answer to a call made inside
// one of this module's transactions. Nothing answers it to a client except as
// the 500 any other fault is.
var errCallInsideTransaction = errors.New("entra: a call out of the module was made inside one of its transactions")

// contractCallHook is handed the context and the name of every call out of
// the module through the accessors below. It is nil in production.
var contractCallHook func(ctx context.Context, method string)

// lockedTxKey marks a context as inside one of this module's transactions —
// Staff's mechanism, copied rather than imported because no module imports
// another.
type lockedTxKey struct{}

// inOpenTx reports whether ctx is inside one of this module's transactions.
func inOpenTx(ctx context.Context) bool {
	locked, _ := ctx.Value(lockedTxKey{}).(bool)
	return locked
}

// noteContractCall reports one call out of the module, and answers whether
// it may go ahead: never from inside one of the module's transactions.
func noteContractCall(ctx context.Context, method string) error {
	if contractCallHook != nil {
		contractCallHook(ctx, method)
	}
	if inOpenTx(ctx) {
		return errCallInsideTransaction
	}
	return nil
}

// unconnected is the users not yet connected under the configured issuer,
// at most limit, as identity answers them to the caller
// (contracts.SSOConnections.Unconnected): nothing to anyone but an Owner,
// contracts.ErrSSONotConfigured while single sign-on is off — also when
// identity is not composed, as there is then no issuer to connect under.
func (s *server) unconnected(ctx context.Context, limit int) ([]contracts.ConnectCandidate, error) {
	if err := noteContractCall(ctx, "SSOConnections.Unconnected"); err != nil {
		return nil, err
	}
	if s.deps.SSOConnections == nil {
		return nil, contracts.ErrSSONotConfigured
	}
	return s.deps.SSOConnections.Unconnected(ctx, limit)
}

// keyHolders is who holds each of keys under the configured issuer
// (contracts.SSOConnections.KeyHolders). It answers nothing for an empty
// batch without asking, and nothing when identity is not composed: nobody
// then holds anything.
func (s *server) keyHolders(ctx context.Context, keys []contracts.DirectoryKey) ([]contracts.KeyHolder, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	if err := noteContractCall(ctx, "SSOConnections.KeyHolders"); err != nil {
		return nil, err
	}
	if s.deps.SSOConnections == nil {
		return nil, nil
	}
	return s.deps.SSOConnections.KeyHolders(ctx, keys)
}
