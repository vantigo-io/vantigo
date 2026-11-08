package energy_test

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

	"github.com/vantigo-io/vantigo/server/internal/energy"
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

func importEvent(id int32) liveEvent {
	return liveEvent{T: "energy.imports", K: "import.changed", ID: strconv.Itoa(int(id))}
}

// Every request that changes an import is heard with the import's id, and the
// worker's start and finish are heard too (K39).
func TestLive_ImportsAreHeard(t *testing.T) {
	t.Parallel()
	h, c, _ := importHarness(t)
	l := listenLive(t, h)

	imp := uploadEnergyImport(t, c, manyPoints(2))
	l.expect(t, importEvent(imp.ID))
	l.settle()

	energyImportOf(t, putEnergyMapping(c, imp.ID, manyPointsMapping), http.StatusOK)
	l.expect(t, importEvent(imp.ID))
	l.settle()

	energyImportOf(t, postEnergyCheck(c, imp.ID), http.StatusOK)
	l.expect(t, importEvent(imp.ID))
	l.settle()

	// Stopped before the worker took it up: back to draft.
	energyImportOf(t, c.Do(http.MethodPost, energyImportPath(imp.ID, "/stop"), nil), http.StatusOK)
	l.expect(t, importEvent(imp.ID))
	l.settle()

	energyImportOf(t, postEnergyCheck(c, imp.ID), http.StatusOK)
	l.settle()
	runEnergyImportWorker(t, h)
	l.expect(t, importEvent(imp.ID))
	checked := getEnergyImport(t, c, imp.ID)
	if checked.State != "checked" {
		t.Fatalf("after the worker the import is %q, want checked", checked.State)
	}
	l.settle()

	switches := checked.Switched
	energyImportOf(t, postEnergyRun(c, imp.ID, &switches), http.StatusOK)
	l.expect(t, importEvent(imp.ID))
	l.settle()
	runEnergyImportWorker(t, h)
	l.expect(t, importEvent(imp.ID))
	l.settle()

	if r := c.Do(http.MethodDelete, energyImportPath(imp.ID, ""), nil); r.Status != http.StatusNoContent {
		t.Fatalf("delete: status %d body %s", r.Status, r.Body)
	}
	l.expect(t, importEvent(imp.ID))
}

// The worker's sweep of old imports is heard once, without an id (K39).
func TestLive_SweepIsHeardWithoutAnId(t *testing.T) {
	t.Parallel()
	h, c, _ := importHarness(t, modtest.WithEnv("ENERGY_IMPORT_RETENTION", "24h"))
	old := importEnergyNow(t, h, c, manyPoints(1), manyPointsMapping)
	h.Exec(t, `UPDATE energy.imports SET updated_at = updated_at - interval '2 days' WHERE id = $1`, old.ID)
	l := listenLive(t, h)
	runEnergyImportWorker(t, h)
	l.expect(t, liveEvent{T: "energy.imports", K: "import.changed", ID: ""})
}

// The topic's Authorize (L9, K39): energy:import and
// energy:metering-points-view, no key; every other key is forbidden.
func TestLive_TopicAuthorizesAsTheImportsGET(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, both := h.SignInUser(t, "energy:import", "energy:metering-points-view")
	_, viewOnly := h.SignInUser(t, "energy:metering-points-view")
	topics := energy.Module().LiveTopics(h.Deps())
	if len(topics) != 1 || topics[0].Kind != "energy.imports" {
		t.Fatalf("topics = %v, want energy.imports only", topics)
	}
	access := h.Deps().Access
	for name, tc := range map[string]struct {
		user uuid.UUID
		key  string
		ok   bool
	}{
		"both permissions": {both, "", true},
		"view only":        {viewOnly, "", false},
		"with a key":       {both, "1", false},
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
