package live

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// startHub starts a hub on databaseURL with short periods, and stops it when
// the test ends.
func startHub(t *testing.T, databaseURL string, access contracts.Access, reg *Registry, tune ...func(*Hub)) *Hub {
	t.Helper()
	h := New(Options{DatabaseURL: databaseURL, Registry: reg, Access: access, AppOrigin: "https://vantigo.example.com"})
	h.limits.backoffMin, h.limits.backoffMax = 20*time.Millisecond, 200*time.Millisecond
	h.limits.batch, h.limits.heartbeat, h.limits.pushThrottle = 50*time.Millisecond, 200*time.Millisecond, 0
	for _, f := range tune {
		f(h)
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.Start(ctx)
	t.Cleanup(func() {
		cancel()
		wctx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if err := h.Wait(wctx); err != nil {
			t.Errorf("the hub did not stop: %v", err)
		}
	})
	return h
}

func connect(t *testing.T, databaseURL string) *pgx.Conn {
	t.Helper()
	conn, err := pgx.Connect(context.Background(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}

// nextEvent waits for the next event frame on c, skipping presence.
func nextEvent(t *testing.T, c *client) map[string]any {
	t.Helper()
	for {
		f := frame(t, c)
		if f["type"] != "presence" {
			return f
		}
	}
}

// Two hubs on one database, as two replicas: a notification on a committed
// transaction reaches both, one on a rolled-back transaction neither, and a
// subscription that was denied hears nothing (L6, L7, L9).
func TestHubs_CommittedReachesBothRolledBackNeither(t *testing.T) {
	url := testdb.URL(t)
	access := newFakeAccess()
	reg := testRegistry(t, "1")
	a := startHub(t, url, access, reg)
	b := startHub(t, url, access, reg)
	user, session := access.person("alpha:view")
	outsider, os := access.person()
	ca, cb := attach(t, a, user, session), attach(t, b, user, session)
	denied := attach(t, b, outsider, os)
	for c, h := range map[*client]*Hub{ca: a, cb: b} {
		send(h, c, map[string]any{"type": "sub", "topic": "alpha.list"})
		if f := frame(t, c); f["type"] != "ok" {
			t.Fatalf("sub: %v", f)
		}
	}
	send(b, denied, map[string]any{"type": "sub", "topic": "alpha.list"})
	frame(t, denied) // denied

	conn := connect(t, url)
	ctx := context.Background()
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := Notify(ctx, tx, "alpha.list", "row.changed", "rolled-back"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	tx, err = conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := Notify(ctx, tx, "alpha.list", "row.changed", "committed"); err != nil {
		t.Fatal(err)
	}
	for _, c := range []*client{ca, cb} {
		noFrame(t, c) // not before the commit
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]*client{"a": ca, "b": cb} {
		if f := nextEvent(t, c); f["type"] != "event" || f["id"] != "committed" {
			t.Errorf("hub %s: %v, want the committed event (and never the rolled-back one)", name, f)
		}
	}
	noFrame(t, denied)
}

// A listener whose connection is killed degrades every socket, reconnects
// with backoff, and resyncs them; events flow again after (L7).
func TestHub_AKilledListenerDegradesThenResyncs(t *testing.T) {
	url := testdb.URL(t)
	access := newFakeAccess()
	h := startHub(t, url, access, testRegistry(t))
	user, session := access.person("alpha:view")
	c := attach(t, h, user, session)
	send(h, c, map[string]any{"type": "sub", "topic": "alpha.list"})
	frame(t, c)

	h.mu.Lock()
	pid := h.listenPID
	h.mu.Unlock()
	admin := connect(t, url)
	if _, err := admin.Exec(context.Background(), "SELECT pg_terminate_backend($1)", int32(pid)); err != nil {
		t.Fatal(err)
	}
	if f := frame(t, c); f["type"] != "degraded" {
		t.Fatalf("%v, want degraded", f)
	}
	if f := frame(t, c); f["type"] != "resync" || f["reason"] != "reconnect" {
		t.Fatalf("%v, want resync once the listener is back", f)
	}
	if err := Notify(context.Background(), admin, "alpha.list", "row.changed", "after"); err != nil {
		t.Fatal(err)
	}
	if f := nextEvent(t, c); f["id"] != "after" {
		t.Errorf("%v, want the event after the reconnect", f)
	}
}

// A DATABASE_URL carrying the pool's own parameters, which the installation
// guide tells operators to set, still brings the listener up: the connection
// is built from pgxpool.ParseConfig, never a plain pgx.Connect (L7).
func TestHub_ADatabaseURLWithPoolParametersListens(t *testing.T) {
	url := testdb.URL(t)
	sep := "?"
	if strings.Contains(url, "?") {
		sep = "&"
	}
	access := newFakeAccess()
	h := startHub(t, url+sep+"pool_max_conns=3&pool_min_conns=1", access, testRegistry(t))
	if h.Health(context.Background()).Listener != "up" {
		t.Fatal("the listener is not up")
	}
	user, session := access.person("alpha:view")
	c := attach(t, h, user, session)
	send(h, c, map[string]any{"type": "sub", "topic": "alpha.list"})
	frame(t, c)
	if err := Notify(context.Background(), connect(t, url), "alpha.list", "row.changed", "1"); err != nil {
		t.Fatal(err)
	}
	if f := nextEvent(t, c); f["id"] != "1" {
		t.Errorf("%v, want the event", f)
	}
	if usage := h.Health(context.Background()).QueueUsage; usage < 0 || usage > 1 {
		t.Errorf("queue usage = %v, want 0 to 1", usage)
	}
}

// Presence crosses hubs in batches on the publisher connection (L13a), and a
// hub that shuts down publishes its members' leaving.
func TestHubs_PresenceCrossesHubs(t *testing.T) {
	url := testdb.URL(t)
	access := newFakeAccess()
	reg := testRegistry(t, "1")
	a := New(Options{DatabaseURL: url, Registry: reg, Access: access})
	a.limits.batch, a.limits.heartbeat, a.limits.pushThrottle = 50*time.Millisecond, 200*time.Millisecond, 0
	actx, stopA := context.WithCancel(context.Background())
	a.Start(actx)
	b := startHub(t, url, access, reg)

	kari, ks := access.person("alpha:view")
	ola, os := access.person("alpha:view")
	ck := attach(t, a, kari, ks)
	co := attach(t, b, ola, os)
	for _, x := range []struct {
		h *Hub
		c *client
	}{{a, ck}, {b, co}} {
		send(x.h, x.c, map[string]any{"type": "sub", "topic": "alpha.item:1"})
		frame(t, x.c)
		frame(t, x.c)
	}
	send(a, ck, map[string]any{"type": "presence", "topic": "alpha.item:1", "state": "replying"})
	if m := waitPresence(t, co, 1); m[0]["userId"] != kari.String() || m[0]["state"] != "replying" {
		t.Errorf("hub b sees %v, want Kari replying", m)
	}

	stopA()
	wctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	<-ck.done    // closed with 1001 once the leaving is taken
	a.detach(ck) // the socket's own goroutine would; there is none here
	if err := a.Wait(wctx); err != nil {
		t.Fatal(err)
	}
	if m := waitPresence(t, co, 0); len(m) != 0 {
		t.Errorf("after hub a shut down: %v, want nobody", m)
	}
}

// waitPresence reads presence frames off c until one has n members.
func waitPresence(t *testing.T, c *client, n int) []map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		f := frame(t, c)
		if f["type"] != "presence" {
			continue
		}
		var members []map[string]any
		for _, m := range f["members"].([]any) {
			members = append(members, m.(map[string]any))
		}
		if len(members) == n {
			return members
		}
	}
	t.Fatalf("no presence frame with %d members", n)
	return nil
}
