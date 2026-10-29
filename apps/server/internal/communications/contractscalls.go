package communications

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/communications/store"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
)

// This file is the module's reach into the mail channels provider
// (deps.ChannelMailbox, Outlook connection design M11, module-boundaries rule
// 15): every call of the slot is made through one of the thin accessors below
// and through nowhere else, so "what does Communications ask of the provider,
// and when" has one place to read the answer and one place to check it from.
//
// What is checked is the lock rule: no call out of the module is made inside
// one of its transactions. Check, Forget and Send reach Microsoft Graph, and a
// transaction holding the default-channel slot's lock while it waits on the
// network would hold every channel writer with it. Every accessor reports its
// call to contractCallHook, which the tests install to record it with whether
// it was made inside one, and refuses a call made inside one with
// errCallInsideTransaction — the calendar's and Staff's rule: the mistake is a
// programming error, and failing the request loudly is better than holding a
// lock across a network call.
//
// The other neighbours this module reads (deps.Directory) predate the guard
// and are called inline, outside any transaction.

// providerSMTP and providerEntra are the channels.provider values: an SMTP
// channel with its credential row, and a Microsoft 365 one on the slot whose
// Key is "entra".
const (
	providerSMTP  = "smtp"
	providerEntra = "entra"
)

// errCallInsideTransaction is every accessor's answer to a call made inside
// one of this module's transactions. Nothing answers it to a client except as
// the 500 any other fault is.
var errCallInsideTransaction = errors.New("communications: a call out of the module was made inside one of its transactions")

// contractCallHook is handed the context and the name of every call out of the
// module. It is nil in production and installed once, before any test runs,
// by this package's own tests (SetContractCallHook in export_test.go).
var contractCallHook func(ctx context.Context, method string)

// lockedTxKey marks a context as inside one of this module's transactions.
// Nothing in the module branches on it except noteContractCall and the tests'
// hook.
type lockedTxKey struct{}

// inOpenTx reports whether ctx is inside one of this module's transactions.
func inOpenTx(ctx context.Context) bool {
	locked, _ := ctx.Value(lockedTxKey{}).(bool)
	return locked
}

// withTx runs fn in one READ COMMITTED transaction on the module's pool (the
// isolation lockDefaultChannelSlot requires), handing it a context marked as
// inside one, so a slot call made from fn is refused and recorded. The channel
// writers use it.
func (s *server) withTx(ctx context.Context, fn func(ctx context.Context, txq *store.Queries) error) error {
	locked := context.WithValue(ctx, lockedTxKey{}, true)
	return db.WithTx(locked, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		return fn(locked, store.New(tx))
	})
}

// noteContractCall reports one call out of the module, and answers whether it
// may go ahead: never from inside one of the module's transactions.
func noteContractCall(ctx context.Context, method string) error {
	if contractCallHook != nil {
		contractCallHook(ctx, method)
	}
	if inOpenTx(ctx) {
		return errCallInsideTransaction
	}
	return nil
}

// channelMailbox is the mail channels provider when one is filled under the
// key a Microsoft 365 channel names, else nil: a channel is never served by a
// provider under another key (M11). Key makes no network call.
func (s *server) channelMailbox(ctx context.Context) (contracts.ChannelMailbox, error) {
	if err := noteContractCall(ctx, "ChannelMailbox.Key"); err != nil {
		return nil, err
	}
	m := s.deps.ChannelMailbox
	if m == nil || m.Key() != providerEntra {
		return nil, nil
	}
	return m, nil
}

// mailboxDisplayName is the provider's name as an admin reads it.
func (s *server) mailboxDisplayName(ctx context.Context, m contracts.ChannelMailbox) (string, error) {
	if err := noteContractCall(ctx, "ChannelMailbox.DisplayName"); err != nil {
		return "", err
	}
	return m.DisplayName(), nil
}

// mailboxStatus is whether channels may use the provider now. It reads the
// provider's own state, never the network.
func (s *server) mailboxStatus(ctx context.Context, m contracts.ChannelMailbox) (contracts.MailboxStatus, error) {
	if err := noteContractCall(ctx, "ChannelMailbox.Status"); err != nil {
		return contracts.MailboxStatus{}, err
	}
	return m.Status(ctx)
}

// mailboxCheck resolves address to the mailbox the provider reaches, and
// records it on the provider's side so the outbox may send through it.
func (s *server) mailboxCheck(ctx context.Context, m contracts.ChannelMailbox, address string) (contracts.ChannelMailboxInfo, error) {
	if err := noteContractCall(ctx, "ChannelMailbox.Check"); err != nil {
		return contracts.ChannelMailboxInfo{}, err
	}
	return m.Check(ctx, address)
}

// mailboxForget drops a mailbox a channel no longer uses.
func (s *server) mailboxForget(ctx context.Context, m contracts.ChannelMailbox, mailbox string) error {
	if err := noteContractCall(ctx, "ChannelMailbox.Forget"); err != nil {
		return err
	}
	return m.Forget(ctx, mailbox)
}

// mailBudget is the largest message a channel on provider sends, in bytes of
// MIME (M13): nil for SMTP, which sets no limit of its own, and for a
// Microsoft 365 channel while no provider is filled (it cannot send then
// anyway); the provider's MaxMessage otherwise. No network call.
func (s *server) mailBudget(ctx context.Context, provider string) (*int64, error) {
	if provider != providerEntra {
		return nil, nil
	}
	m, err := s.channelMailbox(ctx)
	if err != nil || m == nil {
		return nil, err
	}
	if err := noteContractCall(ctx, "ChannelMailbox.MaxMessage"); err != nil {
		return nil, err
	}
	n := m.MaxMessage()
	return &n, nil
}
