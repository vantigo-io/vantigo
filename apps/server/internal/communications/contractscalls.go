package communications

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/communications/store"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// This file is the module's reach into the mail channels provider
// (deps.ChannelMailboxes, Outlook connection design M11, module-boundaries rule
// 15): every call of the slot is made through one of the thin accessors below
// and through nowhere else, so "what does Communications ask of the provider,
// and when" has one place to read the answer and one place to check it from.
//
// What is checked is the lock rule: no call out of the module is made inside
// one of its transactions. Check, Forget, Send, Delta and Raw reach mailbox
// Graph, and a transaction holding the default-channel slot's lock while it
// waits on the network would hold every channel writer with it. Every accessor reports its
// call to contractCallHook, which the tests install to record it with whether
// it was made inside one, and refuses a call made inside one with
// errCallInsideTransaction — the calendar's and Staff's rule: the mistake is a
// programming error, and failing the request loudly is better than holding a
// lock across a network call.
//
// The other neighbour this module reads, deps.Directory, predates the guard
// and is called inline by the HTTP operations, outside any transaction; the
// mailbox sync's linker reads it through directoryContactsByEmail below, so
// the worker's one directory call is refused inside its import's transaction.

// providerSMTP is the channels.provider value of an SMTP channel, with its
// credential row. Every other value is a mail channels provider's Key — a
// mailbox channel, served only by the ChannelMailbox filled under that very
// key (Google Workspace design G4): the module names no provider.
const providerSMTP = "smtp"

// isMailboxProvider reports whether provider names a mailbox channel's
// provider: anything but SMTP. Whether one is filled under it is
// channelMailboxFor's answer.
func isMailboxProvider(provider string) bool { return provider != providerSMTP }

// channelMailboxFor is the ChannelMailbox filled under key, nil when none
// is (Google Workspace design G2): reading the slot calls nobody and makes
// no network call.
func channelMailboxFor(deps module.Deps, key string) contracts.ChannelMailbox {
	if key == providerSMTP {
		return nil
	}
	for _, m := range deps.ChannelMailboxes {
		if m != nil && m.Key() == key {
			return m
		}
	}
	return nil
}

// personalMailboxFor is the PersonalMailbox filled under key, nil when none
// is (G2). No network call.
func personalMailboxFor(deps module.Deps, key string) contracts.PersonalMailbox {
	for _, m := range deps.PersonalMailboxes {
		if m != nil && m.Key() == key {
			return m
		}
	}
	return nil
}

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

// channelMailbox is the mail channels provider filled under provider, a
// mailbox channel's provider, else nil: a channel is never served by a
// provider under another key (M11, Google Workspace design G3). Key makes no
// network call.
func (s *server) channelMailbox(ctx context.Context, provider string) (contracts.ChannelMailbox, error) {
	if err := noteContractCall(ctx, "ChannelMailbox.Key"); err != nil {
		return nil, err
	}
	return channelMailboxFor(s.deps, provider), nil
}

// offeredMailboxes is, for every filled mail channels provider by its Key,
// whether it is available now (its Status), the one fact about a provider
// that decides whether a mailbox channel on it can send. A status that does
// not answer is not offered, and logged. It makes a call out of the module
// per provider, so it is asked outside any transaction.
func (s *server) offeredMailboxes(ctx context.Context) (map[string]bool, error) {
	out := make(map[string]bool, len(s.deps.ChannelMailboxes))
	for _, m := range s.deps.ChannelMailboxes {
		if m == nil {
			continue
		}
		if err := noteContractCall(ctx, "ChannelMailbox.Key"); err != nil {
			return nil, err
		}
		status, err := s.mailboxStatus(ctx, m)
		if errors.Is(err, errCallInsideTransaction) {
			return nil, err
		}
		if err != nil {
			s.deps.Logger.WarnContext(ctx, "communications: the mail channels provider's status did not answer",
				"provider", m.Key(), "err", err)
			out[m.Key()] = false
			continue
		}
		out[m.Key()] = status.State == contracts.MailboxAvailable
	}
	return out, nil
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
// mailbox channel while no provider is filled under its key (it cannot send
// then anyway); that provider's MaxMessage otherwise. No network call.
func (s *server) mailBudget(ctx context.Context, provider string) (*int64, error) {
	if !isMailboxProvider(provider) {
		return nil, nil
	}
	m, err := s.channelMailbox(ctx, provider)
	if err != nil || m == nil {
		return nil, err
	}
	if err := noteContractCall(ctx, "ChannelMailbox.MaxMessage"); err != nil {
		return nil, err
	}
	n := m.MaxMessage()
	return &n, nil
}

// outboxMailbox is the mail channels provider the outbox sends a channel on
// provider through: the slot when it is filled under that very key, else nil
// — a channel is never sent through a provider under another key, nor
// through SMTP (M11). Key makes no network call.
func (w *OutboxWorker) outboxMailbox(ctx context.Context, provider string) (contracts.ChannelMailbox, error) {
	if err := noteContractCall(ctx, "ChannelMailbox.Key"); err != nil {
		return nil, err
	}
	return channelMailboxFor(w.deps, provider), nil
}

// mailboxSend hands the provider one message whole, from mailbox, with its
// blind copies beside it (M13). Never retried here: the outbox retries on its
// backoff.
func mailboxSend(ctx context.Context, m contracts.ChannelMailbox, mailbox string, mime []byte, bcc []string) (contracts.SentMessage, error) {
	if err := noteContractCall(ctx, "ChannelMailbox.Send"); err != nil {
		return contracts.SentMessage{}, err
	}
	return m.Send(ctx, mailbox, mime, bcc)
}

// syncMailbox is the mail channels provider the mailbox sync reads a channel
// on provider through: the one filled under that very key, else nil — a
// channel is never read through a provider under another key (M11, G3). Key
// makes no network call.
func (w *MailboxSyncWorker) syncMailbox(ctx context.Context, provider string) (contracts.ChannelMailbox, error) {
	if err := noteContractCall(ctx, "ChannelMailbox.Key"); err != nil {
		return nil, err
	}
	return channelMailboxFor(w.deps, provider), nil
}

// syncMailboxStatus is whether channels may use the provider now, for the
// mailbox sync. It reads the provider's own state, never the network.
func syncMailboxStatus(ctx context.Context, m contracts.ChannelMailbox) (contracts.MailboxStatus, error) {
	if err := noteContractCall(ctx, "ChannelMailbox.Status"); err != nil {
		return contracts.MailboxStatus{}, err
	}
	return m.Status(ctx)
}

// mailboxDelta reads one page of the changes to a channel's Inbox (M14).
func mailboxDelta(ctx context.Context, m contracts.ChannelMailbox, mailbox, folder, link string, since time.Time) (contracts.MailDelta, error) {
	if err := noteContractCall(ctx, "ChannelMailbox.Delta"); err != nil {
		return contracts.MailDelta{}, err
	}
	return m.Delta(ctx, mailbox, folder, link, since)
}

// mailboxRaw reads one received message whole, at most max bytes of it.
func mailboxRaw(ctx context.Context, m contracts.ChannelMailbox, mailbox, messageID string, max int64) ([]byte, error) {
	if err := noteContractCall(ctx, "ChannelMailbox.Raw"); err != nil {
		return nil, err
	}
	return m.Raw(ctx, mailbox, messageID, max)
}

// mailboxWatch keeps a channel's folder subscribed for change notifications (M17).
func mailboxWatch(ctx context.Context, m contracts.ChannelMailbox, mailbox, folder string) error {
	if err := noteContractCall(ctx, "ChannelMailbox.Watch"); err != nil {
		return err
	}
	return m.Watch(ctx, mailbox, folder)
}

// mailboxUnwatch drops a channel's folder subscription when the channel stops
// receiving (M17).
func (s *server) mailboxUnwatch(ctx context.Context, m contracts.ChannelMailbox, mailbox, folder string) error {
	if err := noteContractCall(ctx, "ChannelMailbox.Unwatch"); err != nil {
		return err
	}
	return m.Unwatch(ctx, mailbox, folder)
}

// mailboxNotified reads the channel mailboxes notified since since (M17).
func mailboxNotified(ctx context.Context, m contracts.ChannelMailbox, since time.Time) ([]contracts.MailboxNotice, error) {
	if err := noteContractCall(ctx, "ChannelMailbox.Notified"); err != nil {
		return nil, err
	}
	return m.Notified(ctx, since)
}

// mailboxMark applies a close-state mark to one message in a channel's mailbox (M17).
func mailboxMark(ctx context.Context, m contracts.ChannelMailbox, mailbox, messageID string, op contracts.MailMark) error {
	if err := noteContractCall(ctx, "ChannelMailbox.Mark"); err != nil {
		return err
	}
	return m.Mark(ctx, mailbox, messageID, op)
}

// mailboxEnsureFolder finds or creates a top-level folder in a channel's mailbox (M17).
func mailboxEnsureFolder(ctx context.Context, m contracts.ChannelMailbox, mailbox, name string) (string, error) {
	if err := noteContractCall(ctx, "ChannelMailbox.EnsureFolder"); err != nil {
		return "", err
	}
	return m.EnsureFolder(ctx, mailbox, name)
}

// directoryContactsByEmail is the linker's one read of the customer
// directory (M15): every contact with the sender's address and the customers
// each is linked to.
func directoryContactsByEmail(ctx context.Context, d contracts.CustomerDirectory, email string) ([]contracts.ContactMatch, error) {
	if err := noteContractCall(ctx, "Directory.ContactsByEmail"); err != nil {
		return nil, err
	}
	return d.ContactsByEmail(ctx, email)
}

// personalMailStatus is the caller's personal mail capability across every
// PersonalMailbox (M20, Google Workspace design G3): each is asked for its
// Status — each answers "no_account" for a caller whose directory account is
// not its own — and the answer is the first Available one; else the first
// whose Reason is not "no_account"; else "no_account". With no provider at
// all it is Available false with Reason "unavailable". The answer carries
// the Key and DisplayName of the provider that gave it, the one a personal
// message is recorded with and sent through.
func personalMailStatus(ctx context.Context, deps module.Deps) (contracts.PersonalMailStatus, error) {
	var first, notAccount *contracts.PersonalMailStatus
	for _, m := range deps.PersonalMailboxes {
		if m == nil {
			continue
		}
		status, err := personalMailStatusOf(ctx, m)
		if err != nil {
			return contracts.PersonalMailStatus{}, err
		}
		if status.Available {
			return status, nil
		}
		if first == nil {
			first = &status
		}
		if notAccount == nil && status.Reason != "no_account" {
			notAccount = &status
		}
	}
	switch {
	case notAccount != nil:
		return *notAccount, nil
	case first != nil:
		return *first, nil
	default:
		return contracts.PersonalMailStatus{Available: false, Reason: "unavailable"}, nil
	}
}

// personalMailStatusOf is one provider's Status for the caller, marked with
// the provider's Key and DisplayName; "unavailable" when m is nil — the
// provider a message was recorded with is no longer filled.
func personalMailStatusOf(ctx context.Context, m contracts.PersonalMailbox) (contracts.PersonalMailStatus, error) {
	if err := noteContractCall(ctx, "PersonalMailbox.Status"); err != nil {
		return contracts.PersonalMailStatus{}, err
	}
	if m == nil {
		return contracts.PersonalMailStatus{Available: false, Reason: "unavailable"}, nil
	}
	status, err := m.Status(ctx)
	if err != nil {
		return contracts.PersonalMailStatus{}, err
	}
	status.Provider, status.ProviderDisplayName = m.Key(), m.DisplayName()
	return status, nil
}

// personalMailboxSend sends mime through the author's delegated mailbox (M20).
// The context must carry the author's user ID via contracts.AsUser(ctx, authorUserID).
func personalMailboxSend(ctx context.Context, m contracts.PersonalMailbox, mime []byte, bcc []string) (contracts.SentMessage, error) {
	if err := noteContractCall(ctx, "PersonalMailbox.Send"); err != nil {
		return contracts.SentMessage{}, err
	}
	if m == nil {
		return contracts.SentMessage{}, contracts.ErrConnectionNeeded
	}
	return m.Send(ctx, mime, bcc)
}

// personalConnectionNeeded is connection_needed's sentence for status, the
// answer of personalMailStatus: the provider it came from named by its
// DisplayName ("Outlook"), and no name when no provider answered at all.
func personalConnectionNeeded(status contracts.PersonalMailStatus) string {
	if status.ProviderDisplayName == "" {
		return "Sending from your own address is not available on this installation."
	}
	return fmt.Sprintf("Your personal %[1]s connection is not available. Please connect or reconnect %[1]s before sending from your address.",
		status.ProviderDisplayName)
}
