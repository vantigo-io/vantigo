package live

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// pgDoc is a DocStore over a scratch table in the test database, to prove the
// fan-out between two hubs through Postgres (K28). The real store is
// Knowledge's.
type pgDoc struct {
	pool    *pgxpool.Pool
	fetches atomic.Int32
}

func newPGDoc(t *testing.T, url string) *pgDoc {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(context.Background(), `CREATE TABLE doc_rows (seq bigint PRIMARY KEY, data bytea NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	return &pgDoc{pool: pool}
}

func (d *pgDoc) CanWrite(context.Context, string) bool { return true }

func (d *pgDoc) Replay(ctx context.Context, _ string, after, _ int64, emit func(int64, []byte) error) (ReplayResult, error) {
	last, err := d.replay(ctx, after, emit)
	return ReplayResult{Last: last, Epoch: 1}, err
}

func (d *pgDoc) replay(ctx context.Context, after int64, emit func(int64, []byte) error) (int64, error) {
	rows, err := d.pool.Query(ctx, `SELECT seq, data FROM doc_rows WHERE seq > $1 ORDER BY seq`, after)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	type row struct {
		seq  int64
		data []byte
	}
	var all []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.seq, &r.data); err != nil {
			return 0, err
		}
		all = append(all, r)
	}
	var last int64
	if err := d.pool.QueryRow(ctx, `SELECT coalesce(max(seq), 0) FROM doc_rows`).Scan(&last); err != nil {
		return 0, err
	}
	for _, r := range all {
		if err := emit(r.seq, r.data); err != nil {
			return 0, err
		}
	}
	return last, nil
}

func (d *pgDoc) Append(ctx context.Context, key string, in DocWrite) (DocResult, error) {
	var res DocResult
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return res, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `LOCK TABLE doc_rows IN EXCLUSIVE MODE`); err != nil {
		return res, err
	}
	if err := tx.QueryRow(ctx, `INSERT INTO doc_rows SELECT coalesce(max(seq), 0) + 1, $1 FROM doc_rows RETURNING seq`, in.Data).Scan(&res.Seq); err != nil {
		return res, err
	}
	if err := NotifyDoc(ctx, tx, "alpha.doc:"+key, res.Seq, in.Hub); err != nil {
		return res, err
	}
	return res, tx.Commit(ctx)
}

func (d *pgDoc) Seed(context.Context, string, DocWrite) (bool, int64, error) { return false, 0, nil }

func (d *pgDoc) Compact(context.Context, string, int64, DocWrite) (bool, error) { return false, nil }

func (d *pgDoc) Fetch(ctx context.Context, _ string, seq int64) ([]byte, bool, error) {
	d.fetches.Add(1)
	var data []byte
	err := d.pool.QueryRow(ctx, `SELECT data FROM doc_rows WHERE seq = $1`, seq).Scan(&data)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	return data, err == nil, err
}

// startHub runs a hub over reg and access behind an httptest server.
func startServedHub(t *testing.T, url string, reg *Registry, access *fakeAccess) (*Hub, string) {
	t.Helper()
	h := New(Options{DatabaseURL: url, Registry: reg, Access: access, AppOrigin: appOrigin})
	ctx, cancel := context.WithCancel(context.Background())
	h.Start(ctx)
	srv := httptest.NewServer(h)
	t.Cleanup(func() {
		cancel()
		wctx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if err := h.Wait(wctx); err != nil {
			t.Errorf("the hub did not stop: %v", err)
		}
		srv.Close()
	})
	return h, "ws" + strings.TrimPrefix(srv.URL, "http") + Path + "?v=1"
}

func dialHub(t *testing.T, url string, user, session uuid.UUID) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: http.Header{
		"Origin": {appOrigin},
		"Cookie": {"session=" + user.String() + ":" + session.String()},
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.CloseNow() })
	return c
}

// next reads the next frame within timeout: a JSON frame as a map, or a
// binary one as kind, seq (updates only) and payload.
type wireFrame struct {
	json    map[string]any
	kind    byte
	seq     int64
	payload []byte
}

func nextFrame(t *testing.T, c *websocket.Conn, timeout time.Duration) (wireFrame, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	typ, b, err := c.Read(ctx)
	if err != nil {
		return wireFrame{}, false
	}
	if typ == websocket.MessageText {
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("frame %s: %v", b, err)
		}
		return wireFrame{json: m}, true
	}
	kind, _, payload, err := decodeBinary(b)
	if err != nil {
		t.Fatalf("binary frame: %v", err)
	}
	f := wireFrame{kind: kind, payload: payload}
	if kind == KindUpdate {
		f.seq, f.payload = int64(binary.BigEndian.Uint64(payload[:8])), payload[8:]
	}
	return f, true
}

// expectFrame reads until a frame matches, skipping the others, or fails.
func expectFrame(t *testing.T, c *websocket.Conn, what string, match func(wireFrame) bool) wireFrame {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		f, ok := nextFrame(t, c, time.Until(deadline))
		if !ok {
			break
		}
		if match(f) {
			return f
		}
	}
	t.Fatalf("no %s", what)
	return wireFrame{}
}

func isJSON(typ string) func(wireFrame) bool {
	return func(f wireFrame) bool { return f.json != nil && f.json["type"] == typ }
}

// Two hubs over one database (K28): the hub that stored a row skips its own
// notification and tells its own sockets at once; the other fetches it; a
// removed row gives doc_stale; awareness crosses in a batch; a reset reaches
// both.
func TestDoc_TwoHubsThroughPostgres(t *testing.T) {
	url := testdb.URL(t)
	store := newPGDoc(t, url)
	reg := NewRegistry()
	if err := reg.Register("alpha", Topic{Kind: "alpha.doc", Doc: store, Authorize: func(ctx context.Context, key string) error {
		if key != "1" || !HasPermission(ctx, "alpha:view") {
			return ErrForbidden
		}
		return nil
	}}); err != nil {
		t.Fatal(err)
	}
	access := newFakeAccess()
	_, urlA := startServedHub(t, url, reg, access)
	_, urlB := startServedHub(t, url, reg, access)
	user, session := access.person("alpha:view")

	join := func(url string) *websocket.Conn {
		c := dialHub(t, url, user, session)
		write(t, c, map[string]any{"type": "sub", "topic": docTopic})
		expectFrame(t, c, "ok", isJSON("ok"))
		write(t, c, map[string]any{"type": "doc_sync", "topic": docTopic, "after": 0})
		expectFrame(t, c, "synced", isJSON("synced"))
		return c
	}
	a1, a2, b := join(urlA), join(urlA), join(urlB)

	send := func(c *websocket.Conn, kind byte, payload []byte) {
		if err := c.Write(context.Background(), websocket.MessageBinary, appendBinary(kind, docTopic, payload)); err != nil {
			t.Fatal(err)
		}
	}
	send(a1, KindUpdate, []byte("hello"))
	if f := expectFrame(t, a1, "ack", isJSON("ack")); f.json["seq"] != float64(1) {
		t.Errorf("ack %v", f.json)
	}
	isUpdate := func(f wireFrame) bool { return f.json == nil && f.kind == KindUpdate }
	for name, c := range map[string]*websocket.Conn{"the same hub": a2, "the other hub": b} {
		if f := expectFrame(t, c, "update on "+name, isUpdate); f.seq != 1 || string(f.payload) != "hello" {
			t.Errorf("%s got %d %q", name, f.seq, f.payload)
		}
	}
	// Only the other hub read the row; the origin skipped its notification.
	if n := store.fetches.Load(); n != 1 {
		t.Errorf("%d fetches, want 1", n)
	}

	// Awareness: at once on the same hub, within a batch on the other.
	send(a1, KindAwareness, []byte("cursor"))
	isAwareness := func(f wireFrame) bool { return f.json == nil && f.kind == KindAwareness }
	expectFrame(t, a2, "awareness on the same hub", isAwareness)
	if f := expectFrame(t, b, "awareness on the other hub", isAwareness); string(f.payload) != "cursor" {
		t.Errorf("awareness %q", f.payload)
	}

	// A row that is gone by the time a hub reads it.
	pool := store.pool
	if err := NotifyDoc(context.Background(), pool, docTopic, 77, "elsewhere"); err != nil {
		t.Fatal(err)
	}
	expectFrame(t, b, "doc_stale", isJSON("doc_stale"))

	// A reset reaches every hub.
	if err := NotifyDocReset(context.Background(), pool, docTopic, 2); err != nil {
		t.Fatal(err)
	}
	// The sender's connection heard its ack, a reset and no update of its own
	// (a timed-out read ends a connection, so this is the last thing it does).
	for _, c := range []*websocket.Conn{a1, a2, b} {
		expectFrame(t, c, "doc_reset", isJSON("doc_reset"))
	}
	for {
		f, ok := nextFrame(t, a1, 300*time.Millisecond)
		if !ok {
			break
		}
		if f.json == nil {
			t.Errorf("the sender was sent a binary frame: %+v", f)
		}
	}
}

// Both hubs appending in a burst: every connection receives each other
// connection's rows exactly once, in order, and never its own (K28).
func TestDoc_TwoHubsBurstNoEchoNoDuplicates(t *testing.T) {
	url := testdb.URL(t)
	store := newPGDoc(t, url)
	reg := NewRegistry()
	if err := reg.Register("alpha", Topic{Kind: "alpha.doc", Doc: store, Authorize: func(ctx context.Context, key string) error {
		if key != "1" || !HasPermission(ctx, "alpha:view") {
			return ErrForbidden
		}
		return nil
	}}); err != nil {
		t.Fatal(err)
	}
	access := newFakeAccess()
	hubA, urlA := startServedHub(t, url, reg, access)
	hubB, urlB := startServedHub(t, url, reg, access)
	for _, h := range []*Hub{hubA, hubB} {
		h.limits.docRate, h.limits.docBurst = 1e6, 1<<20
	}
	user, session := access.person("alpha:view")
	join := func(url string) *websocket.Conn {
		c := dialHub(t, url, user, session)
		write(t, c, map[string]any{"type": "sub", "topic": docTopic})
		expectFrame(t, c, "ok", isJSON("ok"))
		write(t, c, map[string]any{"type": "doc_sync", "topic": docTopic, "after": 0})
		expectFrame(t, c, "synced", isJSON("synced"))
		return c
	}
	a, a2, b := join(urlA), join(urlA), join(urlB)
	const n = 12
	var wg sync.WaitGroup
	for _, w := range []struct {
		c      *websocket.Conn
		prefix string
	}{{a, "a"}, {b, "b"}} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range n {
				_ = w.c.Write(context.Background(), websocket.MessageBinary, appendBinary(KindUpdate, docTopic, []byte(w.prefix+strconv.Itoa(i))))
			}
		}()
	}
	wg.Wait()

	// Read until 2n rows were seen by a2 (all of them) and n by each sender.
	collect := func(c *websocket.Conn, want int) map[string]int {
		got := map[string]int{}
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			total := 0
			for _, v := range got {
				total += v
			}
			if total >= want {
				// Quiet period: nothing more may arrive.
				for {
					f, ok := nextFrame(t, c, 400*time.Millisecond)
					if !ok {
						return got
					}
					if f.json == nil && f.kind == KindUpdate {
						got[string(f.payload)]++
					}
				}
			}
			f, ok := nextFrame(t, c, time.Until(deadline))
			if !ok {
				break
			}
			if f.json == nil && f.kind == KindUpdate {
				got[string(f.payload)]++
			}
		}
		return got
	}
	check := func(name string, got map[string]int, wantPrefixes ...string) {
		t.Helper()
		want := 0
		for _, p := range wantPrefixes {
			for i := range n {
				want++
				if got[p+strconv.Itoa(i)] != 1 {
					t.Errorf("%s got %s%d %d times", name, p, i, got[p+strconv.Itoa(i)])
				}
			}
		}
		if len(got) != want {
			t.Errorf("%s got %d distinct rows, want %d: %v", name, len(got), want, got)
		}
	}
	check("the same-hub peer", collect(a2, 2*n), "a", "b")
	check("the sender on hub A", collect(a, n), "b")
	check("the sender on hub B", collect(b, n), "a")
}
