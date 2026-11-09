package live

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

type docRow struct {
	seq  int64
	data []byte
}

// fakeDoc is a DocStore over a slice.
type fakeDoc struct {
	mu       sync.Mutex
	rows     []docRow
	next     int64
	epoch    int64
	version  int64
	canWrite atomic.Bool
	full     bool
	demand   bool
	needSeed bool          // Append refuses an empty log
	fail     error         // Append, Seed and Compact answer it
	readOnly bool          // Append answers ErrDocReadOnly: write access lost inside the window
	gate     chan struct{} // Replay waits for it to close, when set
	appends  atomic.Int32
}

func newFakeDoc() *fakeDoc {
	d := &fakeDoc{epoch: 1, version: 1}
	d.canWrite.Store(true)
	return d
}

func (d *fakeDoc) CanWrite(context.Context, string) bool { return d.canWrite.Load() }

// reset starts a new epoch with an empty log.
func (d *fakeDoc) reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.epoch++
	d.rows, d.next = nil, 0
}

func (d *fakeDoc) Replay(_ context.Context, _ string, after, epoch int64, emit func(int64, []byte) error) (ReplayResult, error) {
	d.mu.Lock()
	rows := append([]docRow(nil), d.rows...)
	gate, cur := d.gate, d.epoch
	d.mu.Unlock()
	if gate != nil {
		<-gate
	}
	res := ReplayResult{Epoch: cur}
	if len(rows) > 0 {
		res.Last = rows[len(rows)-1].seq
	}
	if epoch != 0 && epoch != cur {
		res.Stale = true
		return res, nil
	}
	for _, r := range rows {
		if r.seq > after {
			if err := emit(r.seq, r.data); err != nil {
				return ReplayResult{}, err
			}
		}
	}
	return res, nil
}

func (d *fakeDoc) add(data []byte) int64 {
	d.next++
	d.rows = append(d.rows, docRow{d.next, data})
	return d.next
}

// check is what every write checks under the lock.
func (d *fakeDoc) check(in DocWrite) error {
	switch {
	case d.fail != nil:
		return d.fail
	case d.readOnly:
		return ErrDocReadOnly
	case in.Epoch != d.epoch:
		return &StaleError{Epoch: d.epoch}
	}
	return nil
}

func (d *fakeDoc) Append(_ context.Context, _ string, in DocWrite) (DocResult, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.check(in); err != nil {
		return DocResult{}, err
	}
	if d.needSeed && len(d.rows) == 0 {
		return DocResult{}, ErrDocNotSeeded
	}
	if d.full {
		return DocResult{Seq: d.next, CompactionDemanded: true}, ErrDocLogFull
	}
	d.appends.Add(1)
	return DocResult{Seq: d.add(in.Data), CompactionDemanded: d.demand}, nil
}

func (d *fakeDoc) Seed(_ context.Context, _ string, in DocWrite) (bool, int64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.check(in); err != nil {
		return false, 0, err
	}
	if len(d.rows) > 0 || in.Version != d.version {
		return false, 0, nil
	}
	return true, d.add(in.Data), nil
}

func (d *fakeDoc) Compact(_ context.Context, _ string, upTo int64, in DocWrite) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.check(in); err != nil {
		return false, err
	}
	if len(d.rows) < 2 || upTo <= d.rows[0].seq || upTo > d.rows[len(d.rows)-1].seq {
		return false, nil
	}
	kept := []docRow{{upTo, in.Data}}
	for _, r := range d.rows {
		if r.seq > upTo {
			kept = append(kept, r)
		}
	}
	d.rows = kept
	return true, nil
}

func (d *fakeDoc) Fetch(_ context.Context, _ string, seq int64) ([]byte, bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, r := range d.rows {
		if r.seq == seq {
			return r.data, true, nil
		}
	}
	return nil, false, nil
}

// docEnv is a hub serving alpha.doc:1 over a fakeDoc.
type docEnv struct {
	h      *Hub
	access *fakeAccess
	store  *fakeDoc
	reader *sdkmetric.ManualReader
}

func newDocEnv(t *testing.T) *docEnv {
	t.Helper()
	store := newFakeDoc()
	reg := NewRegistry()
	err := reg.Register("alpha", Topic{Kind: "alpha.doc", Doc: store, Authorize: func(ctx context.Context, key string) error {
		if key != "1" || !HasPermission(ctx, "alpha:view") {
			return ErrForbidden
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	access := newFakeAccess()
	reader := sdkmetric.NewManualReader()
	h := New(Options{Registry: reg, Access: access, MeterProvider: sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))})
	h.listenerUp = true
	return &docEnv{h: h, access: access, store: store, reader: reader}
}

const docTopic = "alpha.doc:1"

// join attaches a person and subscribes them to the document, without syncing.
func (e *docEnv) join(t *testing.T) *client {
	t.Helper()
	user, session := e.access.person("alpha:view")
	c := attach(t, e.h, user, session)
	send(e.h, c, map[string]any{"type": "sub", "topic": docTopic})
	if f := frame(t, c); f["type"] != "ok" {
		t.Fatalf("sub answered %v", f)
	}
	return c
}

// joinSynced subscribes and syncs from 0, leaving the queue empty and the
// replay finished, so that live rows go straight to the queue.
func (e *docEnv) joinSynced(t *testing.T) *client {
	t.Helper()
	c := e.join(t)
	send(e.h, c, map[string]any{"type": "doc_sync", "topic": docTopic, "after": 0})
	for {
		if f := rawFrame(t, c); !isBinaryFrame(f) {
			var m map[string]any
			_ = json.Unmarshal(f, &m)
			if m["type"] == "synced" {
				waitReplayed(t, c.doc(docTopic))
				return c
			}
		}
	}
}

// waitReplayed waits for s's replay to end. The synced frame is queued
// before the replay re-takes the lock to flush the rows it held back and
// clear syncing; a row delivered in between is held back, not queued.
func waitReplayed(t *testing.T, s *docSub) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		s.mu.Lock()
		syncing := s.syncing
		s.mu.Unlock()
		if !syncing {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the replay did not end")
		}
		runtime.Gosched()
	}
}

// seedPayload is a seed frame's payload: the article version, then the state.
func seedPayload(version int64, state string) []byte {
	b := make([]byte, 8, 8+len(state))
	binary.BigEndian.PutUint64(b, uint64(version))
	return append(b, state...)
}

func (e *docEnv) binary(c *client, kind byte, payload []byte) {
	e.h.handleBinary(context.Background(), c, appendBinary(kind, docTopic, payload))
}

// count is a counter's value for attr, 0 when never recorded.
func (e *docEnv) count(t *testing.T, name string, attr attribute.KeyValue) int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := e.reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				continue
			}
			for _, dp := range sum.DataPoints {
				if v, ok := dp.Attributes.Value(attr.Key); ok && v == attr.Value {
					return dp.Value
				}
			}
		}
	}
	return 0
}

func rawFrame(t testing.TB, c *client) []byte {
	t.Helper()
	select {
	case b := <-c.send:
		return b
	case <-time.After(2 * time.Second):
		t.Fatal("no frame")
		return nil
	}
}

// updateFrame is a binary update frame as a client reads it: seq and data.
func updateFrame(t testing.TB, c *client) (int64, []byte) {
	t.Helper()
	b := rawFrame(t, c)
	if !isBinaryFrame(b) {
		t.Fatalf("expected a binary frame, got %s", b)
	}
	kind, topic, payload, err := decodeBinary(b)
	if err != nil || kind != KindUpdate || topic != docTopic || len(payload) < 8 {
		t.Fatalf("update frame: kind %d topic %q err %v", kind, topic, err)
	}
	return int64(binary.BigEndian.Uint64(payload[:8])), payload[8:]
}

func TestBinary_Codec(t *testing.T) {
	b := appendBinary(KindSeed, docTopic, []byte("abc"), []byte("def"))
	kind, topic, payload, err := decodeBinary(b)
	if err != nil || kind != KindSeed || topic != docTopic || string(payload) != "abcdef" {
		t.Fatalf("round trip: %d %q %q %v", kind, topic, payload, err)
	}
	if !isBinaryFrame(b) || isBinaryFrame([]byte(`{"type":"ok"}`)) {
		t.Error("isBinaryFrame misreads a frame")
	}
	for name, bad := range map[string][]byte{
		"empty":          nil,
		"short header":   {1, 0},
		"zero topic":     {1, 0, 0},
		"topic too long": append([]byte{1, 0, 81}, bytes.Repeat([]byte("a"), 81)...),
		"truncated":      {1, 0, 20, 'a'},
		"bad grammar":    append([]byte{1, 0, 3}, "A.b"...),
		"unknown kind":   append([]byte{9, 0, byte(len(docTopic))}, docTopic...),
	} {
		if _, _, _, err := decodeBinary(bad); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// A joiner is sent the log in order and then synced; a log longer than the
// queue needs the blocking enqueue (K28).
func TestDoc_JoinReplaysInOrderThenSynced(t *testing.T) {
	e := newDocEnv(t)
	e.h.limits.queue = 4
	for i := 1; i <= 50; i++ {
		e.store.add([]byte("row" + strconv.Itoa(i)))
	}
	c := e.join(t)
	send(e.h, c, map[string]any{"type": "doc_sync", "topic": docTopic, "after": 0})
	for i := 1; i <= 50; i++ {
		seq, data := updateFrame(t, c)
		if seq != int64(i) || string(data) != "row"+strconv.Itoa(i) {
			t.Fatalf("row %d arrived as %d %q", i, seq, data)
		}
	}
	if f := frame(t, c); f["type"] != "synced" || f["seq"] != float64(50) {
		t.Errorf("after the rows: %v, want synced 50", f)
	}
	// Later, only the rows after `after`.
	send(e.h, c, map[string]any{"type": "doc_sync", "topic": docTopic, "after": 48})
	if seq, _ := updateFrame(t, c); seq != 49 {
		t.Errorf("resync after 48 began at %d", seq)
	}
	updateFrame(t, c)
	if f := frame(t, c); f["type"] != "synced" {
		t.Errorf("resync ended with %v", f)
	}
}

// A socket that never reads its replay is closed with 1008 (K28).
func TestDoc_ReplayToASocketThatDoesNotReadCloses(t *testing.T) {
	e := newDocEnv(t)
	e.h.limits.queue = 2
	e.h.limits.docBlock = 50 * time.Millisecond
	for i := 0; i < 10; i++ {
		e.store.add([]byte("x"))
	}
	c := e.join(t)
	send(e.h, c, map[string]any{"type": "doc_sync", "topic": docTopic, "after": 0})
	select {
	case <-c.done:
	case <-time.After(2 * time.Second):
		t.Fatal("the socket was not closed")
	}
	if c.code != closePolicy {
		t.Errorf("closed with %d, want 1008", c.code)
	}
}

// An update is stored, acknowledged to its sender, fanned out to the others
// at once, and never echoed (K28).
func TestDoc_UpdateIsAckedFannedOutAndNeverEchoed(t *testing.T) {
	e := newDocEnv(t)
	a, b := e.joinSynced(t), e.joinSynced(t)
	e.binary(a, KindUpdate, []byte("one"))
	e.binary(a, KindUpdate, []byte("two"))
	if f := frame(t, a); f["type"] != "ack" || f["seq"] != float64(1) {
		t.Errorf("first ack: %v", f)
	}
	if f := frame(t, a); f["type"] != "ack" || f["seq"] != float64(2) {
		t.Errorf("second ack: %v", f)
	}
	noFrame(t, a)
	for i, want := range []string{"one", "two"} {
		seq, data := updateFrame(t, b)
		if seq != int64(i+1) || string(data) != want {
			t.Errorf("b got %d %q, want %d %q", seq, data, i+1, want)
		}
	}
	if got := e.count(t, "vantigo.live.doc_updates", attribute.String("result", "stored")); got != 2 {
		t.Errorf("doc_updates stored = %d, want 2", got)
	}
}

// Acks and rejections come back in the order the frames were sent.
func TestDoc_AcksAndRejectionsKeepTheirOrder(t *testing.T) {
	e := newDocEnv(t)
	c := e.joinSynced(t)
	e.binary(c, KindUpdate, []byte("a"))
	e.binary(c, KindUpdate, bytes.Repeat([]byte("x"), MaxDocUpdate+1))
	e.binary(c, KindUpdate, []byte("b"))
	e.store.full = true
	e.binary(c, KindUpdate, []byte("c"))
	want := []struct{ typ, extra string }{{"ack", "1"}, {"update_rejected", "too_large"}, {"ack", "2"}, {"update_rejected", "log_full"}}
	for i, w := range want {
		f := frame(t, c)
		got := ""
		if f["type"] == "ack" {
			got = strconv.Itoa(int(f["seq"].(float64)))
		} else {
			got = f["reason"].(string)
		}
		if f["type"] != w.typ || got != w.extra {
			t.Errorf("frame %d = %v, want %s %s", i, f, w.typ, w.extra)
		}
	}
	if n := e.store.appends.Load(); n != 2 {
		t.Errorf("%d appends, want 2", n)
	}
}

// A reader's frames are dropped and counted; the first update is answered
// read_only once (K26).
func TestDoc_ReaderFramesAreDroppedAndCounted(t *testing.T) {
	e := newDocEnv(t)
	e.store.canWrite.Store(false)
	reader := e.joinSynced(t)
	e.store.canWrite.Store(true)
	writer := e.joinSynced(t)

	e.binary(reader, KindUpdate, []byte("u1"))
	e.binary(reader, KindUpdate, []byte("u2"))
	e.binary(reader, KindAwareness, []byte("cursor"))
	e.binary(reader, KindSeed, seedPayload(1, "seed"))
	if f := frame(t, reader); f["type"] != "update_rejected" || f["reason"] != "read_only" {
		t.Errorf("first refusal: %v", f)
	}
	noFrame(t, reader)
	noFrame(t, writer)
	if n := e.store.appends.Load(); n != 0 {
		t.Errorf("%d appends from a reader", n)
	}
	if got := e.count(t, "vantigo.live.doc_dropped", attribute.String("reason", "read_only")); got != 4 {
		t.Errorf("doc_dropped read_only = %d, want 4", got)
	}
	// A reader still hears the others.
	e.binary(writer, KindUpdate, []byte("w"))
	if _, data := updateFrame(t, reader); string(data) != "w" {
		t.Errorf("reader got %q", data)
	}
}

// The re-check turns write off within a minute (K26).
func TestDoc_RecheckRefreshesCanWrite(t *testing.T) {
	e := newDocEnv(t)
	c := e.joinSynced(t)
	e.binary(c, KindUpdate, []byte("a"))
	frame(t, c) // ack
	e.store.canWrite.Store(false)
	e.h.recheck(context.Background(), c)
	e.binary(c, KindUpdate, []byte("b"))
	if f := frame(t, c); f["type"] != "update_rejected" || f["reason"] != "read_only" {
		t.Errorf("after the re-check: %v", f)
	}
	if n := e.store.appends.Load(); n != 1 {
		t.Errorf("%d appends, want 1", n)
	}
	e.store.canWrite.Store(true)
	e.h.recheck(context.Background(), c)
	e.binary(c, KindUpdate, []byte("c"))
	if f := frame(t, c); f["type"] != "ack" {
		t.Errorf("after write returned: %v", f)
	}
}

// 50 updates a second with a burst of 200 (K25), per connection.
func TestDoc_UpdateRateLimit(t *testing.T) {
	e := newDocEnv(t)
	now := time.Unix(1_000_000, 0)
	e.h.now = func() time.Time { return now }
	e.h.limits.docBurst = 3
	e.h.limits.docRate = 10
	c := e.joinSynced(t)
	for range 3 {
		e.binary(c, KindUpdate, []byte("u"))
		if f := frame(t, c); f["type"] != "ack" {
			t.Fatalf("within the burst: %v", f)
		}
	}
	e.binary(c, KindUpdate, []byte("u"))
	if f := frame(t, c); f["type"] != "update_rejected" || f["reason"] != "rate" {
		t.Errorf("past the burst: %v", f)
	}
	now = now.Add(200 * time.Millisecond) // two tokens
	for range 2 {
		e.binary(c, KindUpdate, []byte("u"))
		if f := frame(t, c); f["type"] != "ack" {
			t.Fatalf("after refill: %v", f)
		}
	}
	e.binary(c, KindUpdate, []byte("u"))
	if f := frame(t, c); f["reason"] != "rate" {
		t.Errorf("after the refill is spent: %v", f)
	}
	other := e.joinSynced(t) // another connection has its own bucket
	e.binary(other, KindUpdate, []byte("u"))
	if f := frame(t, other); f["type"] != "ack" {
		t.Errorf("another connection: %v", f)
	}
}

// Compaction is requested of the writer that has just stored when the store
// says so, also with log_full (K31); a compact frame is acknowledged or
// refused.
func TestDoc_CompactionRequestAndAnswers(t *testing.T) {
	e := newDocEnv(t)
	c := e.joinSynced(t)
	e.store.demand = true
	e.binary(c, KindUpdate, []byte("a"))
	frame(t, c) // ack
	if f := frame(t, c); f["type"] != "compact_requested" || f["upTo"] != float64(1) {
		t.Errorf("request: %v", f)
	}
	e.store.demand = false
	e.binary(c, KindUpdate, []byte("b"))
	e.binary(c, KindUpdate, []byte("c"))
	frame(t, c)
	frame(t, c)
	noFrame(t, c)

	// A full log refuses and still asks for a compaction.
	e.store.full = true
	e.binary(c, KindUpdate, []byte("d"))
	if f := frame(t, c); f["type"] != "update_rejected" || f["reason"] != "log_full" {
		t.Errorf("full: %v", f)
	}
	if f := frame(t, c); f["type"] != "compact_requested" || f["upTo"] != float64(3) {
		t.Errorf("request on a full log: %v", f)
	}
	e.store.full = false

	upTo := make([]byte, 8)
	binary.BigEndian.PutUint64(upTo, 3)
	e.binary(c, KindCompact, append(upTo, "state"...))
	if f := frame(t, c); f["type"] != "ack" || f["seq"] != float64(3) {
		t.Errorf("compaction: %v", f)
	}
	e.binary(c, KindCompact, append(upTo, "late"...)) // log is now only row 3
	if f := frame(t, c); f["type"] != "compact_rejected" {
		t.Errorf("a late compaction: %v", f)
	}
	e.binary(c, KindCompact, append(make([]byte, 8), "zero"...))
	if f := frame(t, c); f["type"] != "compact_rejected" {
		t.Errorf("upTo 0: %v", f)
	}
	e.binary(c, KindCompact, append(upTo, bytes.Repeat([]byte("x"), MaxDocState+1)...))
	if f := frame(t, c); f["type"] != "compact_rejected" {
		t.Errorf("an oversized compaction: %v", f)
	}
}

// The first seed is accepted and fanned out as an update; a second is
// rejected and changes nothing (K29).
func TestDoc_Seed(t *testing.T) {
	e := newDocEnv(t)
	a, b := e.joinSynced(t), e.joinSynced(t)
	e.binary(a, KindSeed, seedPayload(1, "seed-a"))
	if f := frame(t, a); f["type"] != "seed_accepted" {
		t.Errorf("seed: %v", f)
	}
	if f := frame(t, a); f["type"] != "ack" || f["seq"] != float64(1) {
		t.Errorf("seed ack: %v", f)
	}
	if seq, data := updateFrame(t, b); seq != 1 || string(data) != "seed-a" {
		t.Errorf("fan-out: %d %q", seq, data)
	}
	e.binary(b, KindSeed, seedPayload(1, "seed-b"))
	if f := frame(t, b); f["type"] != "seed_rejected" {
		t.Errorf("second seed: %v", f)
	}
	noFrame(t, a)
	if len(e.store.rows) != 1 {
		t.Errorf("%d rows, want 1", len(e.store.rows))
	}
}

// A row appended while a joiner replays is held back, then delivered after
// synced unless the replay carried it (K28).
func TestDoc_LiveRowsDuringAReplay(t *testing.T) {
	e := newDocEnv(t)
	writer := e.joinSynced(t)
	e.binary(writer, KindUpdate, []byte("one"))
	frame(t, writer)

	gate := make(chan struct{})
	e.store.mu.Lock()
	e.store.gate = gate
	e.store.mu.Unlock()
	joiner := e.join(t)
	send(e.h, joiner, map[string]any{"type": "doc_sync", "topic": docTopic, "after": 0})
	time.Sleep(50 * time.Millisecond)           // the replay is waiting at the gate
	e.binary(writer, KindUpdate, []byte("two")) // appended after the replay's snapshot
	frame(t, writer)
	e.store.mu.Lock()
	e.store.gate = nil
	e.store.mu.Unlock()
	close(gate)

	if seq, _ := updateFrame(t, joiner); seq != 1 {
		t.Errorf("replay began at %d", seq)
	}
	if f := frame(t, joiner); f["type"] != "synced" || f["seq"] != float64(1) {
		t.Errorf("synced: %v", f)
	}
	if seq, data := updateFrame(t, joiner); seq != 2 || string(data) != "two" {
		t.Errorf("held-back row: %d %q", seq, data)
	}
	noFrame(t, joiner)

	// A row at or below synced, arriving as an event, is a duplicate.
	e.h.fanOutDoc(docTopic, nil, 1, []byte("one"))
	noFrame(t, joiner)
	e.h.fanOutDoc(docTopic, nil, 3, []byte("three"))
	if seq, _ := updateFrame(t, joiner); seq != 3 {
		t.Errorf("a newer row: %d", seq)
	}
}

// A subscription that has not asked for the log is sent no live rows.
func TestDoc_NoLiveRowsBeforeDocSync(t *testing.T) {
	e := newDocEnv(t)
	writer := e.joinSynced(t)
	idle := e.join(t)
	e.binary(writer, KindUpdate, []byte("one"))
	frame(t, writer)
	noFrame(t, idle)
}

// A binary frame for a topic without a document subscription is dropped and
// counted; a malformed one closes the socket with 1007.
func TestDoc_StrayAndMalformedFrames(t *testing.T) {
	e := newDocEnv(t)
	user, session := e.access.person("alpha:view")
	c := attach(t, e.h, user, session)
	e.binary(c, KindUpdate, []byte("x"))
	noFrame(t, c)
	if c.isClosed() {
		t.Fatal("a stray frame closed the socket")
	}
	if got := e.count(t, "vantigo.live.doc_dropped", attribute.String("reason", "unsubscribed")); got != 1 {
		t.Errorf("doc_dropped unsubscribed = %d, want 1", got)
	}
	e.h.handleBinary(context.Background(), c, []byte{1, 2, 3})
	if !c.isClosed() || c.code != closeInvalid {
		t.Errorf("closed %v with %d, want 1007", c.isClosed(), c.code)
	}
}

// A reset notification tells every subscriber, and a client whose seq is past
// the log's end is told the same (K32).
func TestDoc_Reset(t *testing.T) {
	e := newDocEnv(t)
	a, b := e.joinSynced(t), e.joinSynced(t)
	e.h.dispatch(`{"t":"` + docTopic + `","k":"reset","id":"2"}`)
	for _, c := range []*client{a, b} {
		if f := frame(t, c); f["type"] != "doc_reset" || f["topic"] != docTopic || f["epoch"] != float64(2) {
			t.Errorf("reset: %v", f)
		}
	}
	// After a reset a fresh seed at seq 1 is delivered, not taken for a duplicate.
	e.store.add([]byte("old"))
	e.h.fanOutDoc(docTopic, nil, 1, []byte("seed"))
	if seq, _ := updateFrame(t, a); seq != 1 {
		t.Errorf("row after a reset: %d", seq)
	}
	send(e.h, a, map[string]any{"type": "doc_sync", "topic": docTopic, "after": 40})
	if f := frame(t, a); f["type"] != "doc_reset" || f["epoch"] != float64(2) {
		t.Errorf("sync past the end: %v", f)
	}
}

// A slow consumer of a document topic gets the ordinary resync, and the
// fan-out never blocks (L10).
func TestDoc_SlowConsumer(t *testing.T) {
	e := newDocEnv(t)
	e.h.limits.queue = 4
	e.h.limits.docRate = 1e9
	e.h.limits.docBurst = 1 << 30
	writer := e.joinSynced(t)
	slow := e.joinSynced(t)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 6 {
			e.binary(writer, KindUpdate, []byte("u"))
			<-writer.send
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the fan-out blocked on a slow consumer")
	}
	var sawResync, sawStale bool
	for len(slow.send) > 0 {
		b := <-slow.send
		if !isBinaryFrame(b) && bytes.Contains(b, []byte("slow_consumer")) {
			sawResync = true
		}
		if !isBinaryFrame(b) && bytes.Contains(b, []byte("doc_stale")) {
			sawStale = true
		}
	}
	if !sawResync || !sawStale {
		t.Errorf("the slow consumer was told resync %v, doc_stale %v", sawResync, sawStale)
	}
}

// Awareness goes to the other subscriptions at once, never to the sender;
// the hub keeps the newest frame of each connection for the other hubs (K30).
func TestDoc_AwarenessRelayAndCoalescing(t *testing.T) {
	e := newDocEnv(t)
	a, b := e.joinSynced(t), e.joinSynced(t)
	e.binary(a, KindAwareness, []byte("cursor-1"))
	e.binary(a, KindAwareness, []byte("cursor-2"))
	for _, want := range []string{"cursor-1", "cursor-2"} {
		kind, _, payload, err := decodeBinary(rawFrame(t, b))
		if err != nil || kind != KindAwareness || string(payload) != want {
			t.Errorf("relay: %d %q %v, want %q", kind, payload, err, want)
		}
	}
	noFrame(t, a)
	e.awMu()
	if got := e.h.awareness[docTopic][a]; string(got) != "cursor-2" {
		t.Errorf("pending = %q, want the newest", got)
	}
	e.h.awMu.Unlock()

	e.binary(a, KindAwareness, bytes.Repeat([]byte("x"), MaxDocAwareness+1))
	noFrame(t, b)
	if got := e.count(t, "vantigo.live.doc_dropped", attribute.String("reason", "awareness_too_large")); got != 1 {
		t.Errorf("oversized awareness dropped %d times, want 1", got)
	}
}

func (e *docEnv) awMu() { e.h.awMu.Lock() }

// Awareness from another hub is relayed to every subscription; our own is not.
func TestDoc_AwarenessFromAnotherHub(t *testing.T) {
	e := newDocEnv(t)
	a := e.joinSynced(t)
	enc := base64.StdEncoding.EncodeToString([]byte("remote"))
	body, _ := json.Marshal(payload{T: docTopic, K: docAwarenessKind, H: "another-hub", A: []string{enc, "!!notbase64"}})
	e.h.dispatch(string(body))
	if _, _, p, _ := decodeBinary(rawFrame(t, a)); string(p) != "remote" {
		t.Errorf("relayed %q", p)
	}
	noFrame(t, a)
	own, _ := json.Marshal(payload{T: docTopic, K: docAwarenessKind, H: e.h.id, A: []string{enc}})
	e.h.dispatch(string(own))
	noFrame(t, a)
}

// The largest accepted awareness frame fits a notification once encoded, and
// a batch is split under the encoded limit (K30).
func TestAwarenessBodiesStayUnderTheLimit(t *testing.T) {
	frames := map[*client][]byte{}
	for range 7 {
		frames[&client{}] = bytes.Repeat([]byte{0xff}, MaxDocAwareness)
	}
	bodies := awarenessBodies(docTopic, uuid.NewString(), frames)
	if len(bodies) < 7 {
		t.Errorf("%d notifications for 7 maximal frames, want one each", len(bodies))
	}
	total := 0
	for _, b := range bodies {
		if len(b) > maxPayload {
			t.Errorf("a notification of %d bytes is over %d", len(b), maxPayload)
		}
		var p payload
		if err := json.Unmarshal([]byte(b), &p); err != nil {
			t.Fatal(err)
		}
		total += len(p.A)
	}
	if total != 7 {
		t.Errorf("%d frames in the notifications, want 7", total)
	}
}

// A notification from another hub is fetched; one from this hub is skipped;
// a burst is read with one replay; a removed row gives doc_stale (K28).
func TestDoc_NotificationsFromOtherHubs(t *testing.T) {
	e := newDocEnv(t)
	a := e.joinSynced(t)
	e.store.add([]byte("one"))
	e.h.dispatch(`{"t":"` + docTopic + `","k":"update","id":"1","h":"` + e.h.id + `"}`)
	noFrame(t, a)
	e.h.dispatch(`{"t":"` + docTopic + `","k":"update","id":"1","h":"other"}`)
	if seq, data := updateFrame(t, a); seq != 1 || string(data) != "one" {
		t.Errorf("fetched %d %q", seq, data)
	}
	e.store.add([]byte("two"))
	e.store.add([]byte("three"))
	e.store.add([]byte("four"))
	// A burst of notifications 2 and 4 is read with one replay and only those
	// rows are pushed: row 3 was never announced by another hub.
	e.h.queueFetch(docTopic, "1", e.store, 2)
	e.h.queueFetch(docTopic, "1", e.store, 4)
	got := map[int64]int{}
	for len(got) < 2 {
		seq, _ := updateFrame(t, a)
		got[seq]++
	}
	time.Sleep(100 * time.Millisecond)
	for len(a.send) > 0 {
		if seq, _ := updateFrame(t, a); seq != 0 {
			got[seq]++
		}
	}
	if got[2] != 1 || got[4] != 1 || got[3] != 0 {
		t.Errorf("pushed %v, want rows 2 and 4 once and not 3", got)
	}
	for len(a.send) > 0 {
		<-a.send
	}
	e.h.dispatch(`{"t":"` + docTopic + `","k":"update","id":"99","h":"other"}`)
	if f := frame(t, a); f["type"] != "doc_stale" {
		t.Errorf("a removed row: %v", f)
	}
}

// A doc_error is counted.
func TestDoc_ErrorFrameIsCounted(t *testing.T) {
	e := newDocEnv(t)
	c := e.joinSynced(t)
	send(e.h, c, map[string]any{"type": "doc_error", "topic": docTopic, "message": "bad update"})
	var rm metricdata.ResourceMetrics
	if err := e.reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	var n int64
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if sum, ok := m.Data.(metricdata.Sum[int64]); ok && m.Name == "vantigo.live.doc_apply_errors" {
				for _, dp := range sum.DataPoints {
					n += dp.Value
				}
			}
		}
	}
	if n != 1 {
		t.Errorf("doc_apply_errors = %d, want 1", n)
	}
}

// A reset racing an append: the write of a connection that synced before the
// reset is refused as stale, the connection is told the new epoch and holds
// none until it syncs again (K32).
func TestDoc_ResetRacingAnAppendAndASeed(t *testing.T) {
	e := newDocEnv(t)
	c := e.joinSynced(t)
	e.binary(c, KindUpdate, []byte("a"))
	frame(t, c) // ack
	e.store.reset()
	e.binary(c, KindUpdate, []byte("b"))
	if f := frame(t, c); f["type"] != "update_rejected" || f["reason"] != "stale" {
		t.Errorf("append after a reset: %v", f)
	}
	if f := frame(t, c); f["type"] != "doc_reset" || f["epoch"] != float64(2) {
		t.Errorf("reset frame: %v", f)
	}
	e.binary(c, KindUpdate, []byte("c")) // holds no epoch now
	if f := frame(t, c); f["reason"] != "stale" {
		t.Errorf("append without an epoch: %v", f)
	}
	e.binary(c, KindSeed, seedPayload(1, "s"))
	if f := frame(t, c); f["type"] != "seed_rejected" {
		t.Errorf("seed without an epoch: %v", f)
	}
	// Syncing again binds the new epoch.
	send(e.h, c, map[string]any{"type": "doc_sync", "topic": docTopic, "after": 0, "epoch": 0})
	if f := frame(t, c); f["type"] != "synced" || f["epoch"] != float64(2) || f["seq"] != float64(0) {
		t.Errorf("sync after a reset: %v", f)
	}
	e.binary(c, KindSeed, seedPayload(1, "s"))
	if f := frame(t, c); f["type"] != "seed_accepted" {
		t.Errorf("seed at the new epoch: %v", f)
	}
	frame(t, c) // ack
	// A seed racing a reset is stale too.
	e.store.reset()
	e.binary(c, KindSeed, seedPayload(1, "s"))
	if f := frame(t, c); f["type"] != "seed_rejected" {
		t.Errorf("seed across a reset: %v", f)
	}
	if f := frame(t, c); f["type"] != "doc_reset" || f["epoch"] != float64(3) {
		t.Errorf("reset after the seed: %v", f)
	}
	// A compaction across a reset is refused the same way.
	send(e.h, c, map[string]any{"type": "doc_sync", "topic": docTopic, "after": 0})
	frame(t, c)
	e.store.reset()
	e.binary(c, KindCompact, append(make([]byte, 7), append([]byte{2}, "x"...)...))
	if f := frame(t, c); f["type"] != "compact_rejected" {
		t.Errorf("compact across a reset: %v", f)
	}
	if f := frame(t, c); f["type"] != "doc_reset" {
		t.Errorf("reset after the compaction: %v", f)
	}
}

// A client reconnecting with a seq and an epoch from before a reset is told to
// start over, and is sent no row: seqs are reused after a reset (K32).
func TestDoc_SyncWithAStaleEpoch(t *testing.T) {
	e := newDocEnv(t)
	e.store.add([]byte("old"))
	e.store.reset()
	e.store.add([]byte("new1"))
	e.store.add([]byte("new2"))
	c := e.join(t)
	send(e.h, c, map[string]any{"type": "doc_sync", "topic": docTopic, "after": 1, "epoch": 1})
	if f := frame(t, c); f["type"] != "doc_reset" || f["epoch"] != float64(2) {
		t.Fatalf("stale epoch: %v", f)
	}
	noFrame(t, c)
	// With the current epoch the sync proceeds from its watermark.
	send(e.h, c, map[string]any{"type": "doc_sync", "topic": docTopic, "after": 1, "epoch": 2})
	if seq, data := updateFrame(t, c); seq != 2 || string(data) != "new2" {
		t.Errorf("row %d %q", seq, data)
	}
	if f := frame(t, c); f["type"] != "synced" || f["epoch"] != float64(2) {
		t.Errorf("synced: %v", f)
	}
}

// Frames discarded during a replay: the client is told doc_stale and gets no
// synced, since the replay it would vouch for lost rows.
func TestDoc_DrainDuringReplayIsStale(t *testing.T) {
	e := newDocEnv(t)
	e.h.limits.queue = 4
	e.store.add([]byte("a"))
	e.store.add([]byte("b"))
	gate := make(chan struct{})
	e.store.gate = gate
	c := e.join(t)
	send(e.h, c, map[string]any{"type": "doc_sync", "topic": docTopic, "after": 0})
	time.Sleep(50 * time.Millisecond) // the replay waits at the gate
	for range 5 {                     // the fifth overflows the queue
		e.h.enqueue(c, framePong)
	}
	close(gate)
	var types []string
	deadline := time.After(500 * time.Millisecond)
loop:
	for {
		select {
		case b := <-c.send:
			if isBinaryFrame(b) {
				types = append(types, "update")
			} else {
				var m map[string]any
				_ = json.Unmarshal(b, &m)
				types = append(types, m["type"].(string))
			}
		case <-deadline:
			break loop
		}
	}
	stale, synced := false, false
	for _, ty := range types {
		stale = stale || ty == "doc_stale"
		synced = synced || ty == "synced"
	}
	if !stale || synced {
		t.Errorf("frames %v: want doc_stale and no synced", types)
	}
	// The client answers doc_sync and the replay completes.
	send(e.h, c, map[string]any{"type": "doc_sync", "topic": docTopic, "after": 0})
	for {
		b := rawFrame(t, c)
		if !isBinaryFrame(b) && bytes.Contains(b, []byte(`"synced"`)) {
			break
		}
	}
}

// Write access lost inside the window the 60-second re-check leaves: the store
// answers read-only and the subscription stops writing (K26).
func TestDoc_StoreRefusesAWriterWhoLostAccess(t *testing.T) {
	e := newDocEnv(t)
	c := e.joinSynced(t)
	e.store.readOnly = true
	e.binary(c, KindUpdate, []byte("a"))
	if f := frame(t, c); f["type"] != "update_rejected" || f["reason"] != "read_only" {
		t.Errorf("append: %v", f)
	}
	e.binary(c, KindUpdate, []byte("b"))
	if f := frame(t, c); f["reason"] != "read_only" { // the first refusal at the hub is answered too
		t.Errorf("second append: %v", f)
	}
	e.binary(c, KindUpdate, []byte("b")) // now dropped silently
	noFrame(t, c)
	if n := e.store.appends.Load(); n != 0 {
		t.Errorf("%d appends", n)
	}
}

// A seed of another version than the article's is rejected; a seed frame too
// short to carry a version is rejected; an update before any seed is refused.
func TestDoc_SeedVersionAndNotSeeded(t *testing.T) {
	e := newDocEnv(t)
	e.store.needSeed = true
	c := e.joinSynced(t)
	e.binary(c, KindUpdate, []byte("early"))
	if f := frame(t, c); f["type"] != "update_rejected" || f["reason"] != "not_seeded" {
		t.Errorf("update before a seed: %v", f)
	}
	e.binary(c, KindSeed, seedPayload(7, "built from version 7"))
	if f := frame(t, c); f["type"] != "seed_rejected" {
		t.Errorf("a seed from another version: %v", f)
	}
	e.binary(c, KindSeed, []byte{1, 2, 3})
	if f := frame(t, c); f["type"] != "seed_rejected" {
		t.Errorf("a short seed: %v", f)
	}
	e.binary(c, KindSeed, seedPayload(1, "ok"))
	if f := frame(t, c); f["type"] != "seed_accepted" {
		t.Errorf("the seed: %v", f)
	}
	frame(t, c)
	e.binary(c, KindUpdate, []byte("after"))
	if f := frame(t, c); f["type"] != "ack" {
		t.Errorf("update after the seed: %v", f)
	}
}

// One bucket for update, seed and compact frames; awareness has its own.
func TestDoc_RateBucketsAreSharedAndSeparate(t *testing.T) {
	e := newDocEnv(t)
	now := time.Unix(1_000_000, 0)
	e.h.now = func() time.Time { return now }
	e.h.limits.docBurst, e.h.limits.docRate = 2, 1
	e.h.limits.awareBurst, e.h.limits.awareRate = 2, 1
	a, b := e.joinSynced(t), e.joinSynced(t)
	e.binary(a, KindUpdate, []byte("1"))
	frame(t, a)
	e.binary(a, KindSeed, seedPayload(1, "s")) // log not empty: rejected, but it took a token
	if f := frame(t, a); f["type"] != "seed_rejected" {
		t.Fatalf("seed: %v", f)
	}
	e.binary(a, KindUpdate, []byte("2"))
	if f := frame(t, a); f["type"] != "update_rejected" || f["reason"] != "rate" {
		t.Errorf("an update after an update and a seed: %v", f)
	}
	upTo := make([]byte, 8)
	binary.BigEndian.PutUint64(upTo, 1)
	e.binary(a, KindCompact, append(upTo, "x"...))
	if f := frame(t, a); f["type"] != "compact_rejected" {
		t.Errorf("a compact frame over the shared limit: %v", f)
	}
	// Awareness: its own two tokens, untouched by the writes above.
	for range 3 {
		e.binary(a, KindAwareness, []byte("c"))
	}
	n := 0
	for len(b.send) > 0 {
		if f := <-b.send; isBinaryFrame(f) {
			if k, _, _, _ := decodeBinary(f); k == KindAwareness {
				n++
			}
		}
	}
	if n != 2 {
		t.Errorf("%d awareness frames relayed, want 2", n)
	}
	if got := e.count(t, "vantigo.live.doc_dropped", attribute.String("reason", "awareness_rate")); got != 1 {
		t.Errorf("awareness_rate drops = %d, want 1", got)
	}
}

// Ten binary frames for topics without a subscription in a minute close the
// socket with 1008.
func TestDoc_StrayBinaryFramesCloseTheSocket(t *testing.T) {
	e := newDocEnv(t)
	user, session := e.access.person("alpha:view")
	c := attach(t, e.h, user, session)
	for range maxStrays - 1 {
		e.binary(c, KindUpdate, []byte("x"))
	}
	if c.isClosed() {
		t.Fatal("closed too early")
	}
	e.binary(c, KindUpdate, []byte("x"))
	if !c.isClosed() || c.code != closePolicy {
		t.Errorf("closed %v with %d, want 1008", c.isClosed(), c.code)
	}
	// Spread over more than a minute they are tolerated.
	e2 := newDocEnv(t)
	now := time.Unix(1_000_000, 0)
	e2.h.now = func() time.Time { return now }
	user, session = e2.access.person("alpha:view")
	c2 := attach(t, e2.h, user, session)
	for range 30 {
		e2.binary(c2, KindUpdate, []byte("x"))
		now = now.Add(10 * time.Second)
	}
	if c2.isClosed() {
		t.Error("a slow trickle closed the socket")
	}
}

// A store failure that is not an answer (a timeout, a lost connection) is
// update_rejected unavailable, and the socket stays open.
func TestDoc_TransientStoreErrorIsUnavailable(t *testing.T) {
	e := newDocEnv(t)
	c := e.joinSynced(t)
	e.store.fail = context.DeadlineExceeded
	e.binary(c, KindUpdate, []byte("a"))
	if f := frame(t, c); f["type"] != "update_rejected" || f["reason"] != "unavailable" {
		t.Errorf("append: %v", f)
	}
	e.binary(c, KindSeed, seedPayload(1, "s"))
	if f := frame(t, c); f["type"] != "seed_rejected" {
		t.Errorf("seed: %v", f)
	}
	if c.isClosed() {
		t.Error("the socket was closed")
	}
	e.store.fail = nil
	e.binary(c, KindUpdate, []byte("a"))
	if f := frame(t, c); f["type"] != "ack" {
		t.Errorf("after the store returned: %v", f)
	}
}

// A replay stops between rows once its subscription is gone, and does not
// close the socket.
func TestDoc_ReplayStopsWhenTheSubscriptionIsGone(t *testing.T) {
	e := newDocEnv(t)
	for range 5 {
		e.store.add([]byte("r"))
	}
	gate := make(chan struct{})
	e.store.gate = gate
	c := e.join(t)
	send(e.h, c, map[string]any{"type": "doc_sync", "topic": docTopic, "after": 0})
	time.Sleep(50 * time.Millisecond)
	send(e.h, c, map[string]any{"type": "unsub", "topic": docTopic})
	close(gate)
	time.Sleep(100 * time.Millisecond)
	noFrame(t, c)
	if c.isClosed() {
		t.Error("the socket was closed")
	}
}
