package calendar

import (
	"context"
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
