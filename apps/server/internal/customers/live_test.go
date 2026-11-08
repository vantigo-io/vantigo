package customers_test

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

	"github.com/vantigo-io/vantigo/server/internal/customers"
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
	return liveEvent{T: "customers.imports", K: "import.changed", ID: strconv.Itoa(int(id))}
}

// Every request that changes an import is heard with the import's id, and the
// worker's start and finish are heard too (K38).
func TestLive_ImportsAreHeard(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := uploadClient(t, h)
	l := listenLive(t, h)

	imp := uploadImport(t, c, manyRows(2))
	l.expect(t, importEvent(imp.ID))
	l.settle()

	importOf(t, putMapping(t, c, imp.ID, mappingOf("create", "", "name", "email")), http.StatusOK)
	l.expect(t, importEvent(imp.ID))
	l.settle()

	importOf(t, postCheck(t, c, imp.ID), http.StatusOK)
	l.expect(t, importEvent(imp.ID))
	l.settle()

	// Stopped before the worker took it up: back to draft.
	importOf(t, c.Do(http.MethodPost, importPath(imp.ID, "/stop"), nil), http.StatusOK)
	l.expect(t, importEvent(imp.ID))
	l.settle()

	importOf(t, postCheck(t, c, imp.ID), http.StatusOK)
	l.settle()
	runImportWorker(t, h)
	l.expect(t, importEvent(imp.ID))
	if got := getImport(t, c, imp.ID); got.State != "checked" {
		t.Fatalf("after the worker the import is %q, want checked", got.State)
	}
	l.settle()

	importOf(t, postRun(t, c, imp.ID), http.StatusOK)
	l.expect(t, importEvent(imp.ID))
	l.settle()
	runImportWorker(t, h)
	l.expect(t, importEvent(imp.ID))
	l.settle()

	if r := c.Do(http.MethodDelete, importPath(imp.ID, ""), nil); r.Status != http.StatusNoContent {
		t.Fatalf("delete: status %d body %s", r.Status, r.Body)
	}
	l.expect(t, importEvent(imp.ID))
}

// The worker's sweep of old imports is heard once, without an id (K38).
func TestLive_SweepIsHeardWithoutAnId(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithEnv("CUSTOMERS_IMPORT_RETENTION", "24h"))
	c, _ := uploadClient(t, h)
	old := importNow(t, c, h, manyRows(1), mappingOf("create", "", "name", "email"))
	h.Exec(t, `UPDATE customers.imports SET updated_at = updated_at - interval '2 days' WHERE id = $1`, old.ID)
	l := listenLive(t, h)
	runImportWorker(t, h)
	l.expect(t, liveEvent{T: "customers.imports", K: "import.changed", ID: ""})
}

// The topic's Authorize (L9, K38): customers:import and customers:view, no
// key; every other key is forbidden.
func TestLive_TopicAuthorizesAsTheImportsGET(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, both := h.SignInUser(t, "customers:import", "customers:view")
	_, viewOnly := h.SignInUser(t, "customers:view")
	topics := customers.Module().LiveTopics(h.Deps())
	if len(topics) != 1 || topics[0].Kind != "customers.imports" {
		t.Fatalf("topics = %v, want customers.imports only", topics)
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
