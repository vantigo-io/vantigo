package modtest

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// The fake busy provider answers, of the ids asked, the ones set; nothing
// under SetDeny; SetError's error; and records every call.
func TestFakeCalendarBusy(t *testing.T) {
	ctx := context.Background()
	f := NewFakeCalendarBusy()
	ann, bob := uuid.New(), uuid.New()
	busy := contracts.BusyTime{Spans: []contracts.TimeSpan{{StartsAt: Start, EndsAt: Start.Add(time.Hour)}}}
	f.SetBusy(ann, busy)
	f.SetBusy(bob, contracts.BusyTime{})
	got, err := f.Busy(ctx, []uuid.UUID{ann}, Start, Start.AddDate(0, 0, 6))
	if err != nil || len(got) != 1 || len(got[ann].Spans) != 1 {
		t.Errorf("Busy = %v, %v; want Ann's alone", got, err)
	}
	f.SetDeny(true)
	if got, err := f.Busy(ctx, []uuid.UUID{ann, bob}, Start, Start); err != nil || got == nil || len(got) != 0 {
		t.Errorf("Busy under SetDeny = %v, %v; want an empty map", got, err)
	}
	boom := errors.New("boom")
	f.SetError(boom)
	if _, err := f.Busy(ctx, []uuid.UUID{ann}, Start, Start); !errors.Is(err, boom) {
		t.Errorf("Busy under SetError = %v", err)
	}
	calls := f.Calls()
	if len(calls) != 3 || !slices.Equal(calls[1].UserIDs, []uuid.UUID{ann, bob}) || !calls[0].To.Equal(Start.AddDate(0, 0, 6)) {
		t.Errorf("Calls = %+v", calls)
	}
}
