package calendar

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// askedSource counts the calls it gets and answers one item each time.
type askedSource struct{ calls int }

func (a *askedSource) CalendarKinds() []contracts.CalendarKind {
	return []contracts.CalendarKind{{Key: "test.item", Module: "test"}}
}

func (a *askedSource) CalendarItems(context.Context, contracts.CalendarQuery) ([]contracts.CalendarItem, error) {
	a.calls++
	return []contracts.CalendarItem{{Kind: "test.item", ID: "1"}}, nil
}

func TestSourceItems_RefusesAQueryAboutAnyoneButThePrincipal(t *testing.T) {
	t.Parallel()
	src := &askedSource{}
	s := newServer(module.Deps{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	me := uuid.New()
	ctx := contracts.WithPrincipal(context.Background(), contracts.Principal{UserID: me})
	day := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)

	items, err := s.sourceItems(ctx, src, contracts.CalendarQuery{UserID: uuid.New(), From: day, To: day, Zone: time.UTC})
	if err != nil || len(items) != 0 || src.calls != 0 {
		t.Errorf("someone else's query: items %v err %v calls %d, want nothing asked", items, err, src.calls)
	}
	items, err = s.sourceItems(ctx, src, contracts.CalendarQuery{UserID: me, From: day, To: day, Zone: time.UTC})
	if err != nil || len(items) != 1 || src.calls != 1 {
		t.Errorf("the principal's own query: items %v err %v calls %d", items, err, src.calls)
	}
}

// The busy read's accessors with no provider wired answer it unavailable.
// That they refuse a call inside a transaction is the harness's lock rule,
// which every worker test carries (lockedContractCalls): a locked call made
// here would be recorded against the parallel tests' harnesses.
func TestBusyReadAccessors_WithNoProviderAreUnavailable(t *testing.T) {
	t.Parallel()
	s := newServer(module.Deps{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if _, err := s.mailboxDelta(context.Background(), "oid", time.Time{}, time.Time{}, ""); !errors.Is(err, contracts.ErrMailboxUnavailable) {
		t.Errorf("Delta with no provider: %v", err)
	}
	if _, err := s.mailboxMarked(context.Background(), "oid", nil); !errors.Is(err, contracts.ErrMailboxUnavailable) {
		t.Errorf("Marked with no provider: %v", err)
	}
}

// The write-back's accessors with no provider wired answer it unavailable;
// their lock rule is the harness's, as the busy read's.
func TestWriteBackAccessors_WithNoProviderAreUnavailable(t *testing.T) {
	t.Parallel()
	s := newServer(module.Deps{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if _, err := s.mailboxVersions(context.Background(), "oid", "", time.Time{}, time.Time{}); !errors.Is(err, contracts.ErrMailboxUnavailable) {
		t.Errorf("Versions with no provider: %v", err)
	}
	if _, err := s.mailboxEvents(context.Background(), "oid", "", nil); !errors.Is(err, contracts.ErrMailboxUnavailable) {
		t.Errorf("Events with no provider: %v", err)
	}
}

// The change notifications' accessors with no provider wired answer it
// unavailable; their lock rule is the harness's, as the busy read's, and
// TestNotificationAccessors_RefuseACallInsideATransaction's.
func TestNotificationAccessors_WithNoProviderAreUnavailable(t *testing.T) {
	t.Parallel()
	s := newServer(module.Deps{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err := s.mailboxWatch(context.Background(), "oid"); !errors.Is(err, contracts.ErrMailboxUnavailable) {
		t.Errorf("Watch with no provider: %v", err)
	}
	if err := s.mailboxUnwatch(context.Background(), "oid"); !errors.Is(err, contracts.ErrMailboxUnavailable) {
		t.Errorf("Unwatch with no provider: %v", err)
	}
	if _, err := s.mailboxNotified(context.Background(), time.Time{}); !errors.Is(err, contracts.ErrMailboxUnavailable) {
		t.Errorf("Notified with no provider: %v", err)
	}
	if _, err := s.mailboxConnections(context.Background(), time.Time{}); !errors.Is(err, contracts.ErrMailboxUnavailable) {
		t.Errorf("Connections with no provider: %v", err)
	}
}

// The change notifications' accessors refuse a call made inside one of the
// module's transactions, and the provider is never asked. The test is not
// parallel on purpose: the locked calls it makes are recorded by the
// package's hook (TestMain), and no harness of a parallel test exists while
// the sequential tests run, so none counts them against itself.
func TestNotificationAccessors_RefuseACallInsideATransaction(t *testing.T) {
	box := modtest.NewFakeMailboxCalendar()
	s := newServer(module.Deps{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), MailboxCalendar: box})
	locked := context.WithValue(context.Background(), lockedTxKey{}, true)
	if err := s.mailboxWatch(locked, "oid"); !errors.Is(err, errCallInsideTransaction) {
		t.Errorf("Watch inside a transaction: %v", err)
	}
	if err := s.mailboxUnwatch(locked, "oid"); !errors.Is(err, errCallInsideTransaction) {
		t.Errorf("Unwatch inside a transaction: %v", err)
	}
	if _, err := s.mailboxNotified(locked, time.Time{}); !errors.Is(err, errCallInsideTransaction) {
		t.Errorf("Notified inside a transaction: %v", err)
	}
	if _, err := s.mailboxConnections(locked, time.Time{}); !errors.Is(err, errCallInsideTransaction) {
		t.Errorf("Connections inside a transaction: %v", err)
	}
	if got := box.Calls(); len(got) != 0 {
		t.Errorf("the provider was asked from inside a transaction: %+v", got)
	}
	// Outside one, each goes through.
	ctx := context.Background()
	if err := s.mailboxWatch(ctx, "oid"); err != nil || !box.Watched("oid") {
		t.Errorf("Watch: %v, watched %v", err, box.Watched("oid"))
	}
	if err := s.mailboxUnwatch(ctx, "oid"); err != nil || box.Watched("oid") {
		t.Errorf("Unwatch: %v, watched %v", err, box.Watched("oid"))
	}
	if _, err := s.mailboxNotified(ctx, time.Time{}); err != nil {
		t.Errorf("Notified: %v", err)
	}
	if _, err := s.mailboxConnections(ctx, time.Time{}); err != nil {
		t.Errorf("Connections: %v", err)
	}
}
