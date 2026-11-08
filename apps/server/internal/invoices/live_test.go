package invoices_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/live"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// liveEvent is one notification on live.Channel.
type liveEvent struct {
	T  string `json:"t"`
	K  string `json:"k"`
	ID string `json:"id"`
}

// liveListener LISTENs on the harness's database, as a hub does.
type liveListener struct{ events chan liveEvent }

func listenLive(t *testing.T, h *modtest.Harness) *liveListener {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	conn, err := pgx.ConnectConfig(ctx, h.Pool().Config().ConnConfig.Copy())
	if err != nil {
		t.Fatalf("connect the listener: %v", err)
	}
	if _, err := conn.Exec(ctx, "LISTEN "+live.Channel); err != nil {
		t.Fatalf("listen: %v", err)
	}
	l := &liveListener{events: make(chan liveEvent, 256)}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			n, err := conn.WaitForNotification(ctx)
			if err != nil {
				return
			}
			var e liveEvent
			if json.Unmarshal([]byte(n.Payload), &e) == nil && e.T != "" {
				l.events <- e
			}
		}
	}()
	t.Cleanup(func() {
		cancel()
		<-done
		_ = conn.Close(context.Background())
	})
	return l
}

// expect waits for every one of want, in any order, among what arrives.
func (l *liveListener) expect(t *testing.T, want ...liveEvent) {
	t.Helper()
	missing := map[liveEvent]bool{}
	for _, w := range want {
		missing[w] = true
	}
	deadline := time.After(10 * time.Second)
	var seen []liveEvent
	for len(missing) > 0 {
		select {
		case e := <-l.events:
			seen = append(seen, e)
			delete(missing, e)
		case <-deadline:
			t.Fatalf("live events missing %v; heard %v", missing, seen)
		}
	}
}

// settle drops what is still arriving, so the next expect sees only what the
// next step sends.
func (l *liveListener) settle() {
	for {
		select {
		case <-l.events:
		case <-time.After(200 * time.Millisecond):
			return
		}
	}
}

func runEvent(id int64) liveEvent {
	return liveEvent{T: "invoices.billing-runs", K: "run.changed", ID: strconv.FormatInt(id, 10)}
}

// collect drains what arrives until it goes quiet.
func (l *liveListener) collect() []liveEvent {
	var out []liveEvent
	for {
		select {
		case e := <-l.events:
			out = append(out, e)
		case <-time.After(300 * time.Millisecond):
			return out
		}
	}
}

// Creating and cancelling a run are heard with the run's id (K40).
func TestLive_CreateAndCancelAreHeard(t *testing.T) {
	t.Parallel()
	f := newFakeConsumption()
	h := newHarness(t, f.options()...)
	maker, _ := runMaker(t, h)
	l := listenLive(t, h.Harness)

	run := startRun(t, maker, map[string]any{"issue": false, "customerIds": []int32{customerAcme}})
	l.expect(t, runEvent(run.ID))
	l.settle()

	if res := maker.Do(http.MethodPost, billingRunPath(run.ID)+"/cancel", nil); res.Status != http.StatusOK {
		t.Fatalf("cancel = %d %s", res.Status, res.Body)
	}
	l.expect(t, runEvent(run.ID))
}

// The worker's start of a run and its finish are heard with the run's id; the
// items' outcomes between them are rate-limited, never one per row (K37, K40).
func TestLive_WorkerStartAndFinishAreHeard(t *testing.T) {
	t.Parallel()
	fx := newBillingFixture(t, true)
	fx.energy.put(august(9101, customerAcme, 1, gsrnTromso, contracts.VATHintStandard))
	fx.energy.put(august(9102, customerPerson, 3, gsrnBodo, contracts.VATHintStandard))
	run := fx.run(t, false, customerAcme, customerPerson)
	l := listenLive(t, fx.h.Harness)

	if n := carryAll(t, fx.h); n != 2 {
		t.Fatalf("carried %d items, want 2", n)
	}
	got := l.collect()
	if len(got) < 2 {
		t.Fatalf("heard %v, want at least the start and the finish", got)
	}
	for _, e := range got {
		if e != runEvent(run.ID) {
			t.Errorf("heard %v, want only the run's own events", e)
		}
	}
	if got := runStatus(t, fx.h, run.ID); got != "done" {
		t.Errorf("the run = %s, want done", got)
	}
}

// The worker's sweep of a run it finds with nothing left is heard once,
// without an id (K40).
func TestLive_SweepIsHeardWithoutAnId(t *testing.T) {
	t.Parallel()
	fx := newBillingFixture(t, true)
	fx.energy.put(august(9101, customerAcme, 1, gsrnTromso, contracts.VATHintStandard))
	stuck := fx.run(t, false, customerAcme)
	fx.h.Exec(t, `UPDATE invoices.billing_runs SET status = 'running', started_at = $2 WHERE id = $1`, stuck.ID, fx.h.Now())
	fx.h.Exec(t, `UPDATE invoices.billing_run_items SET status = 'drafted', finished_at = $2 WHERE run_id = $1`, stuck.ID, fx.h.Now())
	l := listenLive(t, fx.h.Harness)
	if err := billingWorker(t, fx.h).RunCycle(context.Background()); err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	l.expect(t, liveEvent{T: "invoices.billing-runs", K: "run.changed", ID: ""})
}

// The topic's Authorize (L9, K40): invoices:access, no key; every other key is
// forbidden.
func TestLive_TopicAuthorizesAsTheRunsGET(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, reader := h.SignInUser(t, "invoices:access")
	_, outsider := h.SignInUser(t, "invoices:create")
	topics := invoices.Module().LiveTopics(h.Deps())
	if len(topics) != 1 || topics[0].Kind != "invoices.billing-runs" {
		t.Fatalf("topics = %v, want invoices.billing-runs only", topics)
	}
	access := h.Deps().Access
	for name, tc := range map[string]struct {
		user uuid.UUID
		key  string
		ok   bool
	}{
		"a reader":      {reader, "", true},
		"no permission": {outsider, "", false},
		"with a key":    {reader, "1", false},
	} {
		err := live.Authorize(context.Background(), access, tc.user, topics[0], tc.key)
		if tc.ok && err != nil {
			t.Errorf("%s: %v, want allowed", name, err)
		}
		if !tc.ok && !errors.Is(err, live.ErrForbidden) {
			t.Errorf("%s: %v, want live.ErrForbidden", name, err)
		}
	}
}
