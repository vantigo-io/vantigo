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
