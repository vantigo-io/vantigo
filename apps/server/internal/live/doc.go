package live

import (
	"context"
	"encoding/binary"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// A document topic (knowledge design K25-K31) is a topic whose Topic.Doc is
// set: the hub relays and stores opaque update bytes through the module's
// DocStore and knows no table. The module owns the rows; this file owns the
// protocol: replay on doc_sync, acks in the order frames were sent, the
// fan-out to the other subscriptions on this hub at once, the fetch of other
// hubs' rows when their notification arrives, and the limits.

// ErrDocLogFull is what DocStore.Append answers when the log is past its hard
// limit (K27); the hub tells the sender update_rejected log_full, and asks it
// to compact when DocResult says so.
var ErrDocLogFull = errors.New("live: the document's log is full")

// ErrDocReadOnly is what a DocStore answers when the document can no longer
// be written by this person: it is gone, locked or trashed, or they lost
// write access.
var ErrDocReadOnly = errors.New("live: the document cannot be written")

// ErrDocNotSeeded is what Append answers while the log is empty: a client
// seeds first (K29).
var ErrDocNotSeeded = errors.New("live: the document's log has no seed")

// StaleError is what a DocStore answers when the epoch of a write (the log as
// the client synced it) is not the log's current one: the log was reset since
// (K32). Epoch is the current one.
type StaleError struct{ Epoch int64 }

func (e *StaleError) Error() string { return "live: the document's log was reset" }

// DocWrite is one write to a DocStore: who wrote it, the bytes, the id of the
// hub that took it (which the store hands to NotifyDoc so that hub can skip
// its own notification), the epoch the writer's subscription synced at, and,
// for a seed, the article version its document was built from.
type DocWrite struct {
	User    uuid.UUID
	Data    []byte
	Hub     string
	Epoch   int64
	Version int64
}

// DocResult is what Append answers: the row's seq (the log's newest, with
// ErrDocLogFull), and whether the writer should now be asked to compact (K31):
// the store decides, so that asks are spaced across replicas.
type DocResult struct {
	Seq                int64
	CompactionDemanded bool
}

// ReplayResult is what Replay answers: the log's newest seq and current epoch.
// Stale says the epoch the client holds is not the current one: no row was
// emitted, and the client starts over.
type ReplayResult struct {
	Last, Epoch int64
	Stale       bool
}

// DocStore is a module's storage for a document topic (K26). key is the
// topic's key. Append, Seed and Compact run under the document's lock, check
// the write's epoch against the log's, check that the person may still write,
// and notify through NotifyDoc on the transaction that stores (not Compact,
// which is not fanned out, K31).
type DocStore interface {
	// CanWrite: may this person's frames be stored? ctx carries
	// contracts.AsUser. Asked on subscribe and at every re-check; false on error.
	CanWrite(ctx context.Context, key string) bool
	// Replay streams the rows after `after` in seq order and answers the
	// newest seq and the epoch of the log, even when no row follows. epoch is
	// the one the client holds (0 when it holds none). emit's error stops it.
	Replay(ctx context.Context, key string, after, epoch int64, emit func(seq int64, data []byte) error) (ReplayResult, error)
	// Append stores one update. ErrDocLogFull, ErrDocReadOnly, ErrDocNotSeeded
	// and *StaleError are answers; any other error is the store's failure.
	Append(ctx context.Context, key string, in DocWrite) (DocResult, error)
	// Seed stores a whole document, only when the log is empty and the
	// article is still at in.Version (K29).
	Seed(ctx context.Context, key string, in DocWrite) (accepted bool, seq int64, err error)
	// Compact replaces the rows up to upTo with in.Data at upTo, or refuses
	// (K31) with ok false and nothing changed.
	Compact(ctx context.Context, key string, upTo int64, in DocWrite) (ok bool, err error)
	// Fetch reads one row for a notification from another hub; found is
	// false when compaction removed it.
	Fetch(ctx context.Context, key string, seq int64) (data []byte, found bool, err error)
}

var errClientGone = errors.New("live: the client is gone")

// pendingRow is a live row held back while a subscription replays.
type pendingRow struct {
	seq   int64
	frame []byte
}

const (
	// maxPendingRows and maxPendingBytes bound what a replaying subscription
	// holds back; past them it replays again instead.
	maxPendingRows  = 256
	maxPendingBytes = 1 << 20
)

// docSub is one connection's subscription to a document topic.
type docSub struct {
	h     *Hub
	c     *client
	topic string
	key   string
	store DocStore

	// stale is set when frames for this connection were discarded: the client
	// has been told doc_stale and syncs again from its contiguous watermark.
	stale atomic.Bool

	mu         sync.Mutex
	epoch      int64 // the log's epoch this subscription synced at; 0 when it holds none
	floor      int64 // the newest epoch heard of; a sync older than it is stale
	canWrite   bool
	rejected   bool  // update_rejected read_only has been sent
	started    bool  // a doc_sync has begun: live rows are wanted
	syncing    bool  // a replay is running
	again      bool  // another doc_sync arrived meanwhile
	againAt    int64 //   and wants the rows after this
	againEpoch int64 //   for a client holding this epoch
	synced     int64 // the newest seq a replay answered; rows at or below are duplicates
	pending    []pendingRow
	pendingN   int
	overflow   bool
}

func (s *docSub) setCanWrite(v bool) {
	s.mu.Lock()
	s.canWrite = v
	if v {
		s.rejected = false
	}
	s.mu.Unlock()
}

// writable reports whether the subscription may store, and whether this is
// the first refusal (only that one is answered).
func (s *docSub) writable() (ok, first bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.canWrite {
		return true, false
	}
	first = !s.rejected
	s.rejected = true
	return false, first
}

// deliver gives the subscription a live row: held back while it replays,
// dropped when its replay already carried it, else queued at once.
func (s *docSub) deliver(seq int64, frame []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case !s.started:
		return
	case s.syncing:
		if s.overflow {
			return
		}
		if len(s.pending) >= maxPendingRows || s.pendingN+len(frame) > maxPendingBytes {
			s.overflow, s.pending, s.pendingN = true, nil, 0
			return
		}
		s.pending = append(s.pending, pendingRow{seq, frame})
		s.pendingN += len(frame)
	case seq <= s.synced:
	default:
		s.h.enqueue(s.c, frame)
	}
}

// reset forgets what the subscription was given: the log was deleted and the
// epoch is now `epoch` (K32). It holds no epoch until it syncs again.
func (s *docSub) reset(epoch int64) {
	s.mu.Lock()
	s.epoch, s.floor = 0, max(s.floor, epoch)
	s.synced = 0
	s.pending, s.pendingN, s.overflow = nil, 0, false
	s.mu.Unlock()
}

func (s *docSub) boundEpoch() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.epoch
}

// start begins a replay of the rows after `after`, for a client holding
// `epoch`, or asks the one running to go again from there.
func (s *docSub) start(after, epoch int64) {
	s.mu.Lock()
	s.started = true
	if s.syncing {
		if !s.again || after < s.againAt {
			s.againAt, s.againEpoch = after, epoch
		}
		s.again = true
		s.mu.Unlock()
		return
	}
	s.syncing = true
	s.mu.Unlock()
	h := s.h
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		s.run(after, epoch)
	}()
}

// current says whether the subscription is still the connection's for its
// topic: a replay stops between rows once it is not (an unsub, a revoke).
func (s *docSub) current() bool { return !s.c.isClosed() && s.c.doc(s.topic) == s }

// run replays until nothing more is asked: the rows after `after`, then
// synced, then the live rows held back meanwhile (K28). A replay during which
// frames of the connection were discarded is abandoned: the client has been
// told doc_stale and syncs again.
func (s *docSub) run(after, epoch int64) {
	h := s.h
	for {
		s.stale.Store(false)
		ctx, cancel := context.WithTimeout(h.base, h.limits.docReplay)
		rows := 0
		res, err := s.store.Replay(ctx, s.key, after, epoch, func(seq int64, data []byte) error {
			if !s.current() {
				return errClientGone
			}
			rows++
			return h.enqueueBlocking(s.c, frameDocUpdate(s.topic, seq, data))
		})
		cancel()
		if err != nil {
			s.mu.Lock()
			s.syncing = false
			s.mu.Unlock()
			if !s.c.isClosed() && !errors.Is(err, errClientGone) {
				h.logger.Warn("live: could not replay a document", "topic", s.topic, "error", err)
				s.c.close(closeInternal, "storage error")
			}
			return
		}
		h.metrics.docReplayRows.Record(context.Background(), int64(rows))

		s.mu.Lock()
		switch {
		case s.stale.Load() || !s.current():
			s.syncing, s.again = false, false
			s.pending, s.pendingN, s.overflow = nil, 0, false
			s.mu.Unlock()
			return
		case res.Stale || res.Epoch < s.floor || (epoch == 0 && after > res.Last):
			// The log was reset since the client's copy (K32), or is shorter
			// than its seq: it starts over.
			s.epoch, s.floor = 0, max(s.floor, res.Epoch)
			s.synced = 0
			s.pending, s.pendingN, s.overflow = nil, 0, false
			floor := s.floor
			s.mu.Unlock()
			// Like the rows, the closing frame waits for room instead of
			// draining the queue, and does so outside the lock (a leaf).
			err := h.enqueueBlocking(s.c, frameDocReset(s.topic, floor))
			s.mu.Lock()
			s.syncing, s.again = false, false
			s.pending, s.pendingN, s.overflow = nil, 0, false
			s.mu.Unlock()
			_ = err // a client that is gone or too slow has been closed
			return
		}
		s.epoch, s.floor = res.Epoch, max(s.floor, res.Epoch)
		s.synced = max(s.synced, res.Last)
		s.mu.Unlock()
		// The last row of a long replay may have filled the queue: synced, which
		// closes the replay, waits for room like the rows before it instead of
		// overflowing the queue into a resync. Live rows arriving meanwhile are
		// still held back (syncing stays set), so they follow it.
		err = h.enqueueBlocking(s.c, frameSynced(s.topic, res.Last, res.Epoch))
		s.mu.Lock()
		if err != nil || s.stale.Load() || !s.current() {
			s.syncing, s.again = false, false
			s.pending, s.pendingN, s.overflow = nil, 0, false
			s.mu.Unlock()
			return
		}
		if !s.overflow {
			for _, p := range s.pending {
				if p.seq > s.synced {
					h.enqueue(s.c, p.frame)
				}
			}
		}
		again, from, fromEpoch := s.again, s.againAt, s.againEpoch
		if s.overflow {
			// Rows were dropped for want of room: replay once more.
			from, fromEpoch = s.synced, res.Epoch
			if again {
				from = min(from, s.againAt)
			}
			again = true
		}
		s.pending, s.pendingN, s.overflow = nil, 0, false
		s.again = false
		if !again {
			s.syncing = false
			s.mu.Unlock()
			return
		}
		s.mu.Unlock()
		after, epoch = from, fromEpoch
	}
}

// enqueueBlocking queues a replay frame, waiting for room up to docBlock: a
// 2000-row replay would overflow the queue by itself (K28). A socket that
// takes none in that time is closed with 1008.
func (h *Hub) enqueueBlocking(c *client, frame []byte) error {
	select {
	case c.send <- frame:
		return nil
	case <-c.done:
		return errClientGone
	default:
	}
	t := time.NewTimer(h.limits.docBlock)
	defer t.Stop()
	select {
	case c.send <- frame:
		return nil
	case <-c.done:
		return errClientGone
	case <-t.C:
		c.close(closePolicy, "too slow")
		return errClientGone
	}
}

// enqueueDroppable queues a frame that is not worth a resync: it is dropped
// when the queue is full (awareness).
func (h *Hub) enqueueDroppable(c *client, frame []byte) {
	select {
	case c.send <- frame:
	default:
		h.metrics.docDrop("queue_full")
	}
}

// docSubs is the document subscriptions to topic, except except's.
func (h *Hub) docSubs(topic string, except *client) []*docSub {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]*docSub, 0, len(h.subs[topic]))
	for c := range h.subs[topic] {
		if c == except {
			continue
		}
		if s := c.doc(topic); s != nil {
			out = append(out, s)
		}
	}
	return out
}

// fanOutDoc gives every document subscription to topic on this hub, except
// from's connection, the row at seq.
func (h *Hub) fanOutDoc(topic string, from *client, seq int64, data []byte) {
	subs := h.docSubs(topic, from)
	if len(subs) == 0 {
		return
	}
	frame := frameDocUpdate(topic, seq, data)
	for _, s := range subs {
		s.deliver(seq, frame)
	}
}

// docSignal sends every document subscription to topic a control frame.
func (h *Hub) docSignal(topic, typ string) {
	frame := frameDocSignal(typ, topic)
	for _, s := range h.docSubs(topic, nil) {
		h.enqueue(s.c, frame)
	}
}

// docSync acts on a doc_sync frame (K28).
func (h *Hub) docSync(c *client, topic string, after, epoch int64) {
	s := c.doc(topic)
	if s == nil {
		h.metrics.docDrop("unsubscribed")
		return
	}
	s.stale.Store(false)
	s.start(max(after, 0), max(epoch, 0))
}

// maxStrays is how many binary frames for topics a connection holds no
// document subscription to are tolerated in a minute before it is closed.
const maxStrays = 10

// stray records a frame for a topic without a document subscription and
// reports whether the connection has sent too many.
func (c *client) stray(now time.Time) bool {
	kept := c.strays[:0]
	for _, at := range c.strays {
		if now.Sub(at) < time.Minute {
			kept = append(kept, at)
		}
	}
	c.strays = append(kept, now)
	return len(c.strays) >= maxStrays
}

// handleBinary acts on one binary frame from c (K25). A frame that is not a
// frame closes the socket with 1007; one for a topic the connection holds no
// document subscription to is dropped and counted, and the tenth in a minute
// closes the socket with 1008.
func (h *Hub) handleBinary(ctx context.Context, c *client, data []byte) {
	kind, topic, payload, err := decodeBinary(data)
	if err != nil {
		c.close(closeInvalid, "malformed binary frame")
		return
	}
	s := c.doc(topic)
	if s == nil {
		h.metrics.docDrop("unsubscribed")
		if c.stray(h.now()) {
			c.close(closePolicy, "binary frames for topics not subscribed")
		}
		return
	}
	ctx, cancel := context.WithTimeout(ctx, h.limits.docStore)
	defer cancel()
	switch kind {
	case KindUpdate:
		h.docUpdate(ctx, s, payload)
	case KindSeed:
		h.docSeed(ctx, s, payload)
	case KindAwareness:
		h.docAwareness(s, payload)
	case KindCompact:
		h.docCompact(ctx, s, payload)
	}
}

// staleSub is a write refused because the log was reset since the connection
// synced: it holds no epoch until it syncs again, and is told the new one.
func (h *Hub) staleSub(s *docSub, epoch int64) {
	s.reset(epoch)
	h.enqueue(s.c, frameDocReset(s.topic, epoch))
}

// asStale reports whether err is a *StaleError.
func asStale(err error) (*StaleError, bool) {
	var e *StaleError
	return e, errors.As(err, &e)
}

func (h *Hub) docUpdate(ctx context.Context, s *docSub, data []byte) {
	c := s.c
	if ok, first := s.writable(); !ok {
		h.metrics.docUpdate("read_only")
		h.metrics.docDrop("read_only")
		if first {
			h.enqueue(c, frameUpdateRejected(s.topic, "read_only"))
		}
		return
	}
	if len(data) == 0 {
		h.metrics.docDrop("empty")
		return
	}
	if len(data) > MaxDocUpdate {
		h.metrics.docUpdate("too_large")
		h.enqueue(c, frameUpdateRejected(s.topic, "too_large"))
		return
	}
	if !c.writes.allow(h.now(), h.limits.docRate, h.limits.docBurst) {
		h.metrics.docUpdate("rate")
		h.enqueue(c, frameUpdateRejected(s.topic, "rate"))
		return
	}
	epoch := s.boundEpoch()
	if epoch == 0 {
		h.metrics.docUpdate("stale")
		h.enqueue(c, frameUpdateRejected(s.topic, "stale"))
		return
	}
	res, err := s.store.Append(ctx, s.key, DocWrite{User: c.user, Data: data, Hub: h.id, Epoch: epoch})
	if stale, ok := asStale(err); ok {
		h.metrics.docUpdate("stale")
		h.enqueue(c, frameUpdateRejected(s.topic, "stale"))
		h.staleSub(s, stale.Epoch)
		return
	}
	switch {
	case errors.Is(err, ErrDocLogFull):
		h.metrics.docUpdate("log_full")
		h.enqueue(c, frameUpdateRejected(s.topic, "log_full"))
		if res.CompactionDemanded {
			h.enqueue(c, frameCompactRequested(s.topic, res.Seq))
		}
		return
	case errors.Is(err, ErrDocReadOnly):
		s.setCanWrite(false)
		h.metrics.docUpdate("read_only")
		h.enqueue(c, frameUpdateRejected(s.topic, "read_only"))
		return
	case errors.Is(err, ErrDocNotSeeded):
		h.metrics.docUpdate("not_seeded")
		h.enqueue(c, frameUpdateRejected(s.topic, "not_seeded"))
		return
	case err != nil:
		// A timeout, a serialization failure, a lost connection: the client
		// keeps the update and tries again with backoff.
		h.logger.Warn("live: a document store failed", "topic", s.topic, "op", "append", "error", err)
		h.metrics.docUpdate("unavailable")
		h.enqueue(c, frameUpdateRejected(s.topic, "unavailable"))
		return
	}
	h.metrics.docUpdate("stored")
	h.enqueue(c, frameAck(s.topic, res.Seq))
	h.fanOutDoc(s.topic, c, res.Seq, data)
	if res.CompactionDemanded {
		h.enqueue(c, frameCompactRequested(s.topic, res.Seq))
	}
}

// docSeed handles a seed frame: the article version it was built from (8
// bytes, big endian), then the whole document (K29).
func (h *Hub) docSeed(ctx context.Context, s *docSub, payload []byte) {
	c := s.c
	if ok, _ := s.writable(); !ok {
		h.metrics.docDrop("read_only")
		return
	}
	reject := func(result string) {
		h.metrics.docSeed(result)
		h.enqueue(c, frameDocSignal("seed_rejected", s.topic))
	}
	if len(payload) <= 8 || len(payload)-8 > MaxDocState {
		reject("too_large")
		return
	}
	if !c.writes.allow(h.now(), h.limits.docRate, h.limits.docBurst) {
		reject("rate")
		return
	}
	epoch := s.boundEpoch()
	if epoch == 0 {
		reject("stale")
		return
	}
	version := int64(binary.BigEndian.Uint64(payload[:8]))
	data := payload[8:]
	accepted, seq, err := s.store.Seed(ctx, s.key, DocWrite{User: c.user, Data: data, Hub: h.id, Epoch: epoch, Version: version})
	if stale, ok := asStale(err); ok {
		reject("stale")
		h.staleSub(s, stale.Epoch)
		return
	}
	switch {
	case errors.Is(err, ErrDocReadOnly):
		s.setCanWrite(false)
		reject("read_only")
	case err != nil:
		h.logger.Warn("live: a document store failed", "topic", s.topic, "op", "seed", "error", err)
		reject("unavailable")
	case !accepted:
		reject("rejected")
	default:
		h.metrics.docSeed("accepted")
		h.enqueue(c, frameDocSignal("seed_accepted", s.topic))
		h.enqueue(c, frameAck(s.topic, seq))
		h.fanOutDoc(s.topic, c, seq, data)
	}
}

func (h *Hub) docCompact(ctx context.Context, s *docSub, payload []byte) {
	c := s.c
	if ok, _ := s.writable(); !ok {
		h.metrics.docDrop("read_only")
		return
	}
	if len(payload) < 8 {
		c.close(closeInvalid, "malformed binary frame")
		return
	}
	reject := func(result string) {
		h.metrics.docCompaction(result)
		h.enqueue(c, frameDocSignal("compact_rejected", s.topic))
	}
	upTo := binary.BigEndian.Uint64(payload[:8])
	data := payload[8:]
	if upTo == 0 || upTo > 1<<62 || len(data) == 0 || len(data) > MaxDocState {
		reject("refused")
		return
	}
	if !c.writes.allow(h.now(), h.limits.docRate, h.limits.docBurst) {
		reject("rate")
		return
	}
	epoch := s.boundEpoch()
	if epoch == 0 {
		reject("stale")
		return
	}
	ok, err := s.store.Compact(ctx, s.key, int64(upTo), DocWrite{User: c.user, Data: data, Hub: h.id, Epoch: epoch})
	if stale, isStale := asStale(err); isStale {
		reject("stale")
		h.staleSub(s, stale.Epoch)
		return
	}
	switch {
	case errors.Is(err, ErrDocReadOnly):
		s.setCanWrite(false)
		reject("read_only")
	case err != nil:
		h.logger.Warn("live: a document store failed", "topic", s.topic, "op", "compact", "error", err)
		reject("unavailable")
	case !ok:
		reject("refused")
	default:
		h.metrics.docCompaction("ok")
		h.enqueue(c, frameAck(s.topic, int64(upTo)))
	}
}

// docWorker is the fetch worker of one document topic on this hub (K28): it
// reads the rows other hubs appended, off the listener's loop. It holds the
// seqs of the notifications received from other hubs and nothing else, so a
// row this hub appended is never read back (no echo) and a row is pushed once
// (no duplicates): one Fetch for a single notification, one Replay for a burst
// whose rows are filtered to those seqs.
type docWorker struct {
	running bool
	seqs    map[int64]struct{}
	store   DocStore
	key     string
	topic   string
}

// queueFetch records that another hub appended seq to topic.
func (h *Hub) queueFetch(topic, key string, store DocStore, seq int64) {
	h.mu.Lock()
	wanted := len(h.subs[topic]) > 0 && !h.closed
	h.mu.Unlock()
	if !wanted {
		return
	}
	h.docMu.Lock()
	defer h.docMu.Unlock()
	w := h.docWorkers[topic]
	if w == nil {
		w = &docWorker{store: store, key: key, topic: topic, seqs: map[int64]struct{}{}}
		h.docWorkers[topic] = w
	}
	w.seqs[seq] = struct{}{}
	if !w.running {
		w.running = true
		h.wg.Add(1)
		go func() {
			defer h.wg.Done()
			h.fetchLoop(w)
		}()
	}
}

func (h *Hub) fetchLoop(w *docWorker) {
	for {
		h.docMu.Lock()
		if len(w.seqs) == 0 {
			w.running = false
			delete(h.docWorkers, w.topic)
			h.docMu.Unlock()
			return
		}
		want := w.seqs
		w.seqs = map[int64]struct{}{}
		h.docMu.Unlock()

		ctx, cancel := context.WithTimeout(h.base, h.limits.docStore)
		missing := 0
		var err error
		if len(want) == 1 {
			for seq := range want {
				var (
					data  []byte
					found bool
				)
				data, found, err = w.store.Fetch(ctx, w.key, seq)
				if err == nil && !found {
					missing++
				} else if err == nil {
					h.fanOutDoc(w.topic, nil, seq, data)
				}
			}
		} else {
			lo := int64(1 << 62)
			for seq := range want {
				lo = min(lo, seq)
			}
			_, err = w.store.Replay(ctx, w.key, lo-1, 0, func(seq int64, data []byte) error {
				if _, ok := want[seq]; ok {
					delete(want, seq)
					h.fanOutDoc(w.topic, nil, seq, data)
				}
				return nil
			})
			missing = len(want)
		}
		cancel()
		switch {
		case err != nil:
			h.logger.Warn("live: could not read a document's new rows", "topic", w.topic, "error", err)
			h.docSignal(w.topic, "doc_stale")
		case missing > 0:
			// Compaction removed a row first: the subscribers ask again.
			h.docSignal(w.topic, "doc_stale")
		}
	}
}

// dispatchDoc acts on a notification for a document topic (K27, K30, K32).
func (h *Hub) dispatchDoc(t Topic, p payload) {
	_, key, _ := SplitTopic(p.T)
	switch p.K {
	case docUpdateKind:
		seq, err := strconv.ParseInt(p.ID, 10, 64)
		if err != nil || seq < 1 || p.H == h.id {
			return
		}
		h.queueFetch(p.T, key, t.Doc, seq)
	case docResetKind:
		epoch, _ := strconv.ParseInt(p.ID, 10, 64)
		for _, s := range h.docSubs(p.T, nil) {
			s.reset(epoch)
		}
		frame := frameDocReset(p.T, epoch)
		for _, s := range h.docSubs(p.T, nil) {
			h.enqueue(s.c, frame)
		}
	case docAwarenessKind:
		if p.H == h.id {
			return
		}
		h.relayAwareness(p.T, p.A)
	}
}

// refreshCanWrite re-asks the store whether c may write each document it is
// subscribed to (K26), within a re-check.
func (h *Hub) refreshCanWrite(ctx context.Context, access *cycleAccess, c *client, topic string, t Topic, key string) {
	s := c.doc(topic)
	if s == nil || t.Doc == nil {
		return
	}
	s.setCanWrite(h.canWrite(ctx, access, c, t, key))
}

// canWrite asks t's store, as c's user, whether they may write.
func (h *Hub) canWrite(ctx context.Context, access *cycleAccess, c *client, t Topic, key string) (ok bool) {
	ctx, cancel := context.WithTimeout(ctx, h.limits.authorize)
	defer cancel()
	defer func() {
		if r := recover(); r != nil {
			h.logger.Error("live: a document store's CanWrite panicked", "kind", t.Kind, "panic", r)
			ok = false
		}
	}()
	if c.station != uuid.Nil {
		return false // no document topic admits a station; and it writes none
	}
	return t.Doc.CanWrite(contracts.AsUser(withAccess(ctx, access), c.user), key)
}

// updateBucket is a token bucket (K25), used from a connection's read loop
// only.
type updateBucket struct {
	tokens float64
	at     time.Time
}

// allow takes a token, at rate a second with a burst.
func (b *updateBucket) allow(now time.Time, rate float64, burst int) bool {
	if b.at.IsZero() {
		b.tokens = float64(burst)
	} else {
		b.tokens = min(float64(burst), b.tokens+now.Sub(b.at).Seconds()*rate)
	}
	b.at = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// doc, setDoc, delDoc and allDocs guard a connection's document
// subscriptions with their own lock, a leaf, so that the overflow path of
// enqueue may read them.
func (c *client) doc(topic string) *docSub {
	c.docMu.Lock()
	defer c.docMu.Unlock()
	return c.docs[topic]
}

func (c *client) setDoc(topic string, s *docSub) {
	c.docMu.Lock()
	c.docs[topic] = s
	c.docMu.Unlock()
}

func (c *client) delDoc(topic string) {
	c.docMu.Lock()
	delete(c.docs, topic)
	c.docMu.Unlock()
}

func (c *client) allDocs() []*docSub {
	c.docMu.Lock()
	defer c.docMu.Unlock()
	out := make([]*docSub, 0, len(c.docs))
	for _, s := range c.docs {
		out = append(out, s)
	}
	return out
}
