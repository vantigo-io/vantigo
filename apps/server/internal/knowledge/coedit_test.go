package knowledge_test

// Live co-editing, the server's half (knowledge design §5, K24-K32): the
// module's DocStore over article_updates, the collaborative save and the
// log's reset. The frame protocol itself is proved in internal/live.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/knowledge"
	"github.com/vantigo-io/vantigo/server/internal/live"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// docStoreOf is the knowledge.doc topic's DocStore, as the hub gets it.
func docStoreOf(t *testing.T, h *modtest.Harness) live.DocStore {
	t.Helper()
	for _, topic := range knowledge.Module().LiveTopics(h.Deps()) {
		if topic.Kind == "knowledge.doc" {
			if topic.Doc == nil {
				t.Fatal("knowledge.doc has no Doc")
			}
			return topic.Doc
		}
	}
	t.Fatal("no knowledge.doc topic")
	return nil
}

func asUser(p person) context.Context { return contracts.AsUser(context.Background(), p.id) }

// replayed is the log as a joiner is sent it.
func replayed(t *testing.T, s live.DocStore, id string, after int64) (rows map[int64]string, order []int64, last int64) {
	t.Helper()
	rows = map[int64]string{}
	res, err := s.Replay(context.Background(), id, after, 0, func(seq int64, data []byte) error {
		rows[seq] = string(data)
		order = append(order, seq)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return rows, order, res.Last
}

// logState reads the log's state off the article row.
func logState(t *testing.T, h *modtest.Harness, id string) (epoch, version, rows, bytes int64) {
	t.Helper()
	err := h.Pool().QueryRow(context.Background(),
		`SELECT doc_epoch, version, update_rows, update_bytes FROM knowledge.articles WHERE id = $1`, id).Scan(&epoch, &version, &rows, &bytes)
	if err != nil {
		t.Fatal(err)
	}
	return epoch, version, rows, bytes
}

// writeAs is a write by user at the log's current epoch.
func writeAs(t *testing.T, h *modtest.Harness, id string, user person, data string) live.DocWrite {
	t.Helper()
	epoch, version, _, _ := logState(t, h, id)
	return live.DocWrite{User: user.id, Data: []byte(data), Hub: "hub-1", Epoch: epoch, Version: version}
}

// seedLog seeds the article's log as the first writer of a joined client does.
func seedLog(t *testing.T, h *modtest.Harness, s live.DocStore, id string, user person) {
	t.Helper()
	ok, seq, err := s.Seed(context.Background(), id, writeAs(t, h, id, user, "seed"))
	if err != nil || !ok || seq != 1 {
		t.Fatalf("seed = %v %d %v", ok, seq, err)
	}
}

func appendRow(t *testing.T, h *modtest.Harness, s live.DocStore, id string, user person, data string) live.DocResult {
	t.Helper()
	res, err := s.Append(context.Background(), id, writeAs(t, h, id, user, data))
	if err != nil {
		t.Fatalf("append %q: %v", data, err)
	}
	return res
}

// docEvent is a notification on the document topic, with its origin hub.
type docEvent struct {
	T  string `json:"t"`
	K  string `json:"k"`
	ID string `json:"id"`
	H  string `json:"h"`
}

func listenDocs(t *testing.T, h *modtest.Harness) chan docEvent {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	conn, err := pgx.ConnectConfig(ctx, h.Pool().Config().ConnConfig.Copy())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, "LISTEN "+live.Channel); err != nil {
		t.Fatal(err)
	}
	events := make(chan docEvent, 256)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			n, err := conn.WaitForNotification(ctx)
			if err != nil {
				return
			}
			var e docEvent
			if json.Unmarshal([]byte(n.Payload), &e) == nil && len(e.T) > 14 && e.T[:14] == "knowledge.doc:" {
				events <- e
			}
		}
	}()
	t.Cleanup(func() {
		cancel()
		<-done
		_ = conn.Close(context.Background())
	})
	return events
}

func expectDocEvent(t *testing.T, events chan docEvent, want docEvent) {
	t.Helper()
	select {
	case e := <-events:
		if e != want {
			t.Fatalf("heard %+v, want %+v", e, want)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("no notification, want %+v", want)
	}
}

func noDocEvent(t *testing.T, events chan docEvent) {
	t.Helper()
	select {
	case e := <-events:
		t.Fatalf("unexpected notification %+v", e)
	case <-time.After(200 * time.Millisecond):
	}
}

// Rows get seq 1, 2, 3 per article, in order, after the seed; the notification
// carries the seq and the origin hub; Append refuses an empty log (K27).
func TestDocStore_AppendOrderNotificationAndSeqPerArticle(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	s := docStoreOf(t, h)
	a := signIn(t, h)
	one, two := create(t, a, "", "workspace", "One"), create(t, a, "", "workspace", "Two")
	events := listenDocs(t, h)

	if _, err := s.Append(context.Background(), one.ID, writeAs(t, h, one.ID, a, "early")); !errors.Is(err, live.ErrDocNotSeeded) {
		t.Fatalf("append to an empty log: %v, want not seeded", err)
	}
	seedLog(t, h, s, one.ID, a)
	expectDocEvent(t, events, docEvent{T: "knowledge.doc:" + one.ID, K: "update", ID: "1", H: "hub-1"})
	for i, data := range []string{"a", "b", "c"} {
		if res := appendRow(t, h, s, one.ID, a, data); res.Seq != int64(i+2) || res.CompactionDemanded {
			t.Fatalf("append %q = %+v", data, res)
		}
		expectDocEvent(t, events, docEvent{T: "knowledge.doc:" + one.ID, K: "update", ID: string(rune('2' + i)), H: "hub-1"})
	}
	seedLog(t, h, s, two.ID, a) // another article starts at 1 as well
	rows, order, last := replayed(t, s, one.ID, 0)
	if last != 4 || len(order) != 4 || order[0] != 1 || order[3] != 4 || rows[3] != "b" {
		t.Errorf("replay = %v %v last %d", rows, order, last)
	}
	if rows, order, last := replayed(t, s, one.ID, 3); last != 4 || len(order) != 1 || rows[4] != "c" {
		t.Errorf("replay after 3 = %v %v last %d", rows, order, last)
	}
	if _, order, last := replayed(t, s, one.ID, 4); last != 4 || len(order) != 0 {
		t.Errorf("replay after the end = %v last %d", order, last)
	}
	if _, order, last := replayed(t, s, uuid.NewString(), 0); last != 0 || len(order) != 0 {
		t.Errorf("an empty log = %v last %d", order, last)
	}
	if data, found, err := s.Fetch(context.Background(), one.ID, 3); err != nil || !found || string(data) != "b" {
		t.Errorf("fetch 3 = %q %v %v", data, found, err)
	}
	if _, found, err := s.Fetch(context.Background(), one.ID, 9); err != nil || found {
		t.Errorf("fetch 9 = %v %v", found, err)
	}
	// The counters on the article row follow the log.
	if _, _, rows, bytes := logState(t, h, one.ID); rows != 4 || bytes != int64(len("seed")+3) {
		t.Errorf("counters = %d rows %d bytes", rows, bytes)
	}
	// An article that is gone refuses a write.
	if _, err := s.Append(context.Background(), uuid.NewString(), live.DocWrite{User: a.id, Data: []byte("x"), Epoch: 0}); !errors.Is(err, live.ErrDocReadOnly) {
		t.Errorf("append to nothing: %v", err)
	}
}

// A compaction is asked for above 2000 rows or 4 MiB, and updates are refused
// beyond twice that (K27); the counters on the article row decide, and asks
// are spaced a minute apart across replicas (K31).
func TestDocStore_LogBoundsAndCompactionAsks(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	s := docStoreOf(t, h)
	a := signIn(t, h)
	fill := func(id string, rows, bytesEach, upTo int64) {
		h.Exec(t, `INSERT INTO knowledge.article_updates (article_id, seq, data, created_at) SELECT $1, g, decode(repeat('00', $3::int), 'hex'), now() FROM generate_series(1, $2::int) g`, id, rows, bytesEach)
		h.Exec(t, `UPDATE knowledge.articles SET update_rows = $2::int, update_bytes = $2::bigint * $3::bigint WHERE id = $1`, id, rows, bytesEach)
		_ = upTo
	}

	byRows := create(t, a, "", "workspace", "Rows")
	fill(byRows.ID, 1999, 1, 0)
	if res := appendRow(t, h, s, byRows.ID, a, "r2000"); res.CompactionDemanded {
		t.Errorf("2000 rows already ask for a compaction: %+v", res)
	}
	if res := appendRow(t, h, s, byRows.ID, a, "r2001"); !res.CompactionDemanded {
		t.Errorf("2001 rows do not ask for a compaction: %+v", res)
	}
	// Asked once: not again within a minute, whichever replica asks.
	if res := appendRow(t, h, s, byRows.ID, a, "r2002"); res.CompactionDemanded {
		t.Errorf("a second ask within a minute: %+v", res)
	}
	h.Advance(61 * time.Second)
	if res := appendRow(t, h, s, byRows.ID, a, "r2003"); !res.CompactionDemanded {
		t.Errorf("no ask after a minute: %+v", res)
	}
	h.Exec(t, `INSERT INTO knowledge.article_updates (article_id, seq, data, created_at) SELECT $1, g, '\x00', now() FROM generate_series(2004, 3999) g`, byRows.ID)
	h.Exec(t, `UPDATE knowledge.articles SET update_rows = 3999 WHERE id = $1`, byRows.ID)
	if _, err := s.Append(context.Background(), byRows.ID, writeAs(t, h, byRows.ID, a, "r4000")); err != nil {
		t.Errorf("the 4000th row: %v", err)
	}
	h.Advance(61 * time.Second)
	res, err := s.Append(context.Background(), byRows.ID, writeAs(t, h, byRows.ID, a, "r4001"))
	if !errors.Is(err, live.ErrDocLogFull) || !res.CompactionDemanded || res.Seq != 4000 {
		t.Errorf("the 4001st row: %+v %v, want log_full asking for a compaction at 4000", res, err)
	}

	byBytes := create(t, a, "", "workspace", "Bytes")
	fill(byBytes.ID, 16, 262144, 0) // 4 MiB exactly
	if res := appendRow(t, h, s, byBytes.ID, a, "x"); !res.CompactionDemanded {
		t.Errorf("over 4 MiB does not ask for a compaction: %+v", res)
	}
	h.Exec(t, `UPDATE knowledge.articles SET update_bytes = 8136 * 1024 WHERE id = $1`, byBytes.ID)
	h.Advance(61 * time.Second)
	if _, err := s.Append(context.Background(), byBytes.ID, writeAs(t, h, byBytes.ID, a, string(bytes.Repeat([]byte{1}, 50<<10)))); err != nil {
		t.Errorf("a row that fits: %v", err)
	}
	if _, err := s.Append(context.Background(), byBytes.ID, writeAs(t, h, byBytes.ID, a, string(bytes.Repeat([]byte{1}, 256<<10)))); !errors.Is(err, live.ErrDocLogFull) {
		t.Errorf("a row past 8 MiB: %v, want log_full", err)
	}
}

// Concurrent seeds: exactly one is accepted and the log holds one row (K29);
// a seed built from another version of the article is rejected.
func TestDocStore_SeedRaceAndVersion(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	s := docStoreOf(t, h)
	a := signIn(t, h)
	art := create(t, a, "", "workspace", "Seeded")
	events := listenDocs(t, h)

	const n = 8
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		accepted int
		failure  error
	)
	for i := range n {
		w := writeAs(t, h, art.ID, a, string(rune('a'+i)))
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, seq, err := s.Seed(context.Background(), art.ID, w)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failure = err
			}
			if ok {
				accepted++
				if seq != 1 {
					failure = errors.New("an accepted seed is not row 1")
				}
			}
		}()
	}
	wg.Wait()
	if failure != nil {
		t.Fatal(failure)
	}
	if accepted != 1 {
		t.Errorf("%d seeds accepted, want 1", accepted)
	}
	if got := h.Count(t, `SELECT count(*) FROM knowledge.article_updates WHERE article_id = $1`, art.ID); got != 1 {
		t.Errorf("%d rows, want 1", got)
	}
	expectDocEvent(t, events, docEvent{T: "knowledge.doc:" + art.ID, K: "update", ID: "1", H: "hub-1"})
	noDocEvent(t, events)
	if ok, _, err := s.Seed(context.Background(), art.ID, writeAs(t, h, art.ID, a, "late")); err != nil || ok {
		t.Errorf("a late seed = %v %v", ok, err)
	}

	// A seed built before someone saved the article is stale (K29).
	other := create(t, a, "", "workspace", "Moved on")
	stale := writeAs(t, h, other.ID, a, "from version 1")
	expectStatus(t, "retitle", a.c.Do(http.MethodPatch, "/api/v1/knowledge/articles/"+other.ID, map[string]any{"title": "Moved again"}), http.StatusOK, "")
	if ok, _, err := s.Seed(context.Background(), other.ID, stale); err != nil || ok {
		t.Errorf("a seed from an old version = %v %v, want rejected", ok, err)
	}
	if got := h.Count(t, `SELECT count(*) FROM knowledge.article_updates WHERE article_id = $1`, other.ID); got != 0 {
		t.Errorf("a rejected seed left %d rows", got)
	}
}

// A write whose epoch is not the log's, because the log was reset after the
// client synced, is refused and changes nothing; so is a replay (K32).
func TestDocStore_EpochRefusesWritesAcrossAReset(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	s := docStoreOf(t, h)
	a := signIn(t, h)
	art := create(t, a, "", "workspace", "Epochs")
	seedLog(t, h, s, art.ID, a)
	stale := writeAs(t, h, art.ID, a, "before the reset")
	expectStatus(t, "reset", a.c.Do(http.MethodPost, "/api/v1/knowledge/articles/"+art.ID+"/collab/reset", nil), http.StatusNoContent, "")

	var se *live.StaleError
	if _, err := s.Append(context.Background(), art.ID, stale); !errors.As(err, &se) || se.Epoch != stale.Epoch+1 {
		t.Errorf("append across a reset: %v", err)
	}
	if _, _, err := s.Seed(context.Background(), art.ID, stale); !errors.As(err, &se) {
		t.Errorf("seed across a reset: %v", err)
	}
	if _, err := s.Compact(context.Background(), art.ID, 2, stale); !errors.As(err, &se) {
		t.Errorf("compact across a reset: %v", err)
	}
	if n := h.Count(t, `SELECT count(*) FROM knowledge.article_updates WHERE article_id = $1`, art.ID); n != 0 {
		t.Errorf("a stale write left %d rows", n)
	}
	// A replay for the old epoch is stale and sends no row.
	res, err := s.Replay(context.Background(), art.ID, 0, stale.Epoch, func(int64, []byte) error { t.Error("a row was emitted"); return nil })
	if err != nil || !res.Stale || res.Epoch != stale.Epoch+1 {
		t.Errorf("replay = %+v %v", res, err)
	}
	// The same seq in the new epoch is not mistaken for the old one.
	fresh := writeAs(t, h, art.ID, a, "after")
	if ok, seq, err := s.Seed(context.Background(), art.ID, fresh); err != nil || !ok || seq != 1 {
		t.Errorf("seed at the new epoch = %v %d %v", ok, seq, err)
	}
	if res, err := s.Replay(context.Background(), art.ID, 0, fresh.Epoch, func(int64, []byte) error { return nil }); err != nil || res.Stale {
		t.Errorf("replay at the new epoch = %+v %v", res, err)
	}
}

// Compaction replaces the rows up to upTo with one row at upTo; a late one,
// an out-of-range one and an oversized one are refused and change nothing;
// it is not notified (K31).
func TestDocStore_Compaction(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	s := docStoreOf(t, h)
	a := signIn(t, h)
	art := create(t, a, "", "workspace", "Compacted")
	seedLog(t, h, s, art.ID, a)
	for _, d := range []string{"2", "3", "4", "5"} {
		appendRow(t, h, s, art.ID, a, d)
	}
	events := listenDocs(t, h)
	compact := func(upTo int64, data []byte) bool {
		w := writeAs(t, h, art.ID, a, "")
		w.Data = data
		ok, err := s.Compact(context.Background(), art.ID, upTo, w)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}

	for name, upTo := range map[string]int64{"the first row": 1, "zero": 0, "past the newest": 6} {
		if compact(upTo, []byte("state")) {
			t.Errorf("a compaction up to %s was accepted", name)
		}
	}
	if compact(4, bytes.Repeat([]byte{1}, live.MaxDocState+1)) {
		t.Error("an oversized compaction was accepted")
	}
	if compact(4, nil) {
		t.Error("an empty compaction was accepted")
	}
	if got := h.Count(t, `SELECT count(*) FROM knowledge.article_updates WHERE article_id = $1`, art.ID); got != 5 {
		t.Fatalf("refusals changed the log: %d rows", got)
	}

	if !compact(4, []byte("state-4")) {
		t.Fatal("a valid compaction was refused")
	}
	rows, order, last := replayed(t, s, art.ID, 0)
	if last != 5 || len(order) != 2 || order[0] != 4 || order[1] != 5 || rows[4] != "state-4" || rows[5] != "5" {
		t.Errorf("after compaction: %v %v last %d", rows, order, last)
	}
	if _, _, n, b := logState(t, h, art.ID); n != 2 || b != int64(len("state-4")+1) {
		t.Errorf("counters after compaction = %d rows %d bytes", n, b)
	}
	for _, upTo := range []int64{4, 3, 2} {
		if compact(upTo, []byte("late")) {
			t.Errorf("a late compaction up to %d was accepted", upTo)
		}
	}
	if _, found, _ := s.Fetch(context.Background(), art.ID, 2); found {
		t.Error("row 2 survived the compaction")
	}
	if data, found, _ := s.Fetch(context.Background(), art.ID, 4); !found || string(data) != "state-4" {
		t.Errorf("the compacted row = %q %v", data, found)
	}
	if res := appendRow(t, h, s, art.ID, a, "6"); res.Seq != 6 {
		t.Errorf("append after compaction = %d, want 6", res.Seq)
	}
	if !compact(6, []byte("state-6")) {
		t.Error("a second compaction was refused")
	}
	// Only the appends and the seed were announced, never the compactions.
	for range 6 {
		select {
		case e := <-events:
			if e.K != "update" {
				t.Errorf("heard %+v", e)
			}
		case <-time.After(150 * time.Millisecond):
		}
	}
}

// CanWrite asks for write access, not locked, not trashed (K26); and every
// write asks again inside its transaction, so a demotion, a lock or the trash
// a moment ago stops a write at once (K26, K27).
func TestDocStore_WriteAccessIsCheckedInsideTheWrite(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	s := docStoreOf(t, h)
	a, reader, stranger := signIn(t, h), signIn(t, h), signIn(t, h)
	art := create(t, a, "", "workspace", "Guarded")
	expectStatus(t, "share", setAccess(a, art.ID, "read", map[uuid.UUID]string{a.id: "write", reader.id: "write"}, true), http.StatusOK, "")
	priv := create(t, a, "", "private", "Mine")
	seedLog(t, h, s, art.ID, a)

	can := func(p person, id string) bool { return s.CanWrite(asUser(p), id) }
	if !can(a, art.ID) || !can(reader, art.ID) {
		t.Error("a writer cannot write")
	}
	if can(stranger, priv.ID) || can(a, "not-a-uuid") || s.CanWrite(context.Background(), art.ID) {
		t.Error("a stranger, a bad key or no user can write")
	}
	write := func(p person) error {
		_, err := s.Append(context.Background(), art.ID, writeAs(t, h, art.ID, p, "x"))
		return err
	}
	if err := write(reader); err != nil {
		t.Fatalf("a writer's append: %v", err)
	}
	// Demoted a moment ago: the hub's answer is up to a minute old.
	expectStatus(t, "demote", setAccess(a, art.ID, "read", map[uuid.UUID]string{a.id: "write", reader.id: "read"}, false), http.StatusOK, "")
	if can(reader, art.ID) {
		t.Error("a demoted person can write")
	}
	if err := write(reader); !errors.Is(err, live.ErrDocReadOnly) {
		t.Errorf("append by a demoted person: %v", err)
	}
	if _, _, err := s.Seed(context.Background(), art.ID, writeAs(t, h, art.ID, reader, "s")); !errors.Is(err, live.ErrDocReadOnly) {
		t.Errorf("seed by a demoted person: %v", err)
	}
	if _, err := s.Compact(context.Background(), art.ID, 1, writeAs(t, h, art.ID, reader, "s")); !errors.Is(err, live.ErrDocReadOnly) {
		t.Errorf("compact by a demoted person: %v", err)
	}
	expectStatus(t, "lock", a.c.Do(http.MethodPatch, "/api/v1/knowledge/articles/"+art.ID, map[string]any{"locked": true}), http.StatusOK, "")
	if can(a, art.ID) {
		t.Error("a locked page can be written")
	}
	if err := write(a); !errors.Is(err, live.ErrDocReadOnly) {
		t.Errorf("append to a locked page: %v", err)
	}
	expectStatus(t, "unlock", a.c.Do(http.MethodPatch, "/api/v1/knowledge/articles/"+art.ID, map[string]any{"locked": false}), http.StatusOK, "")
	if err := write(a); err != nil {
		t.Errorf("append to an unlocked page: %v", err)
	}
	expectStatus(t, "trash", a.c.Do(http.MethodPost, "/api/v1/knowledge/articles/"+art.ID+"/trash", nil), http.StatusNoContent, "")
	if can(a, art.ID) {
		t.Error("a trashed page can be written")
	}
	if err := write(a); !errors.Is(err, live.ErrDocReadOnly) {
		t.Errorf("append to a trashed page: %v", err)
	}
}

func collabSave(p person, id, title, text string, extra map[string]any) *modtest.Response {
	body := map[string]any{"title": title, "content": paragraph(text), "mode": "collab"}
	for k, v := range extra {
		body[k] = v
	}
	return p.c.Do(http.MethodPut, "/api/v1/knowledge/articles/"+id+"/content", body)
}

// A single-editor save, a restore and the manual reset each delete the log in
// their transaction, start a new epoch and tell the document's subscribers
// (K32); a collaborative save does not.
func TestDocLog_ResetBySingleSaveRestoreAndEndpoint(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	s := docStoreOf(t, h)
	a, reader, stranger := signIn(t, h), signIn(t, h), signIn(t, h)
	art := create(t, a, "", "workspace", "Reset")
	expectStatus(t, "share", setAccess(a, art.ID, "read", map[uuid.UUID]string{a.id: "write"}, true), http.StatusOK, "")
	topic := "knowledge.doc:" + art.ID
	events := listenDocs(t, h)
	logRows := func() int {
		return h.Count(t, `SELECT count(*) FROM knowledge.article_updates WHERE article_id = $1`, art.ID)
	}
	seed := func() {
		seedLog(t, h, s, art.ID, a)
		expectDocEvent(t, events, docEvent{T: topic, K: "update", ID: "1", H: "hub-1"})
	}

	// A collaborative save leaves the log alone and says nothing about it.
	seed()
	expectStatus(t, "collab save", collabSave(a, art.ID, "Reset", "live text", nil), http.StatusOK, "")
	if logRows() != 1 {
		t.Fatalf("a collaborative save changed the log: %d rows", logRows())
	}
	noDocEvent(t, events)

	// A single-editor save deletes it and starts epoch 2.
	expectStatus(t, "single save", save(a, art.ID, "Reset", "fallback text", 2), http.StatusOK, "")
	expectDocEvent(t, events, docEvent{T: topic, K: "reset", ID: "2"})
	if epoch, _, rows, bytes := logState(t, h, art.ID); logRows() != 0 || epoch != 2 || rows != 0 || bytes != 0 {
		t.Errorf("after a single save: %d rows, epoch %d, counters %d/%d", logRows(), epoch, rows, bytes)
	}

	// A restore deletes it and starts epoch 2.
	seed()
	expectStatus(t, "restore", a.c.Do(http.MethodPost, "/api/v1/knowledge/articles/"+art.ID+"/versions/3/restore", map[string]any{"expectedVersion": 3}), http.StatusOK, "")
	expectDocEvent(t, events, docEvent{T: topic, K: "reset", ID: "3"})
	if logRows() != 0 {
		t.Errorf("a restore left %d rows", logRows())
	}

	// The endpoint: a writer may, a reader may not, a stranger sees nothing.
	seed()
	path := "/api/v1/knowledge/articles/" + art.ID + "/collab/reset"
	expectStatus(t, "reader resets", reader.c.Do(http.MethodPost, path, nil), http.StatusForbidden, "article_read_only")
	priv := create(t, a, "", "private", "Private")
	expectStatus(t, "stranger resets", stranger.c.Do(http.MethodPost, "/api/v1/knowledge/articles/"+priv.ID+"/collab/reset", nil), http.StatusNotFound, "")
	noDocEvent(t, events)
	expectStatus(t, "writer resets", a.c.Do(http.MethodPost, path, nil), http.StatusNoContent, "")
	expectDocEvent(t, events, docEvent{T: topic, K: "reset", ID: "4"})
	if logRows() != 0 {
		t.Errorf("the reset left %d rows", logRows())
	}
	expectStatus(t, "lock", a.c.Do(http.MethodPatch, "/api/v1/knowledge/articles/"+art.ID, map[string]any{"locked": true}), http.StatusOK, "")
	expectStatus(t, "reset a locked page", a.c.Do(http.MethodPost, path, nil), http.StatusNoContent, "")
	expectDocEvent(t, events, docEvent{T: topic, K: "reset", ID: "5"})
}

// The collaborative save (K32): no version comparison, the lock and the
// document checks as ever, versions coalesced as ever, and it needs a live
// log; a single save without a version is a 400.
func TestCollabSave(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	s := docStoreOf(t, h)
	a, b := signIn(t, h), signIn(t, h)
	art := create(t, a, "", "workspace", "Together")
	expectStatus(t, "share", setAccess(a, art.ID, "read", map[uuid.UUID]string{a.id: "write", b.id: "write"}, true), http.StatusOK, "")
	versions := func() int {
		r := a.c.Do(http.MethodGet, "/api/v1/knowledge/articles/"+art.ID+"/versions", nil)
		expectStatus(t, "versions", r, http.StatusOK, "")
		var out struct{ Items []struct{ Version int32 } }
		r.JSON(&out)
		return len(out.Items)
	}

	// Nobody has opened the page live: a collaborative save would race a
	// single-editor one.
	expectStatus(t, "collab save on an empty log", collabSave(a, art.ID, "Together", "one", nil), http.StatusConflict, "collab_inactive")
	if _, got := get(t, a, art.ID); got.Version != 1 {
		t.Errorf("a refused save changed the version to %d", got.Version)
	}
	seedLog(t, h, s, art.ID, a)

	// No expectedVersion, and a stale one is ignored.
	expectStatus(t, "collab save", collabSave(a, art.ID, "Together", "one", nil), http.StatusOK, "")
	expectStatus(t, "collab save with a stale version", collabSave(a, art.ID, "Together", "two", map[string]any{"expectedVersion": 1}), http.StatusOK, "")
	if r, got := get(t, a, art.ID); r.Status != http.StatusOK || got.Version != 3 {
		t.Errorf("version = %d, want 3", got.Version)
	}
	if n := versions(); n != 1 {
		t.Errorf("%d history entries after two saves by one person, want 1", n)
	}
	expectStatus(t, "B collab save", collabSave(b, art.ID, "Together", "three", nil), http.StatusOK, "")
	if n := versions(); n != 2 {
		t.Errorf("%d history entries after B's save, want 2", n)
	}
	if _, got := get(t, a, art.ID); got.UpdatedBy == nil || got.UpdatedBy.UserID != b.id.String() {
		t.Errorf("updatedBy = %+v, want B", got.UpdatedBy)
	}

	expectStatus(t, "single without a version", a.c.Do(http.MethodPut, "/api/v1/knowledge/articles/"+art.ID+"/content", map[string]any{
		"title": "Together", "content": paragraph("x"),
	}), http.StatusBadRequest, "")
	expectStatus(t, "an explicit single without a version", a.c.Do(http.MethodPut, "/api/v1/knowledge/articles/"+art.ID+"/content", map[string]any{
		"title": "Together", "content": paragraph("x"), "mode": "single",
	}), http.StatusBadRequest, "")
	bad := a.c.Do(http.MethodPut, "/api/v1/knowledge/articles/"+art.ID+"/content", map[string]any{
		"title": "Together", "mode": "collab",
		"content": map[string]any{"type": "doc", "content": []any{map[string]any{"type": "script"}}},
	})
	expectStatus(t, "an invalid document", bad, http.StatusBadRequest, "invalid_document")
	reader := signIn(t, h)
	expectStatus(t, "a reader's collab save", collabSave(reader, art.ID, "Together", "x", nil), http.StatusForbidden, "article_read_only")
	expectStatus(t, "lock", a.c.Do(http.MethodPatch, "/api/v1/knowledge/articles/"+art.ID, map[string]any{"locked": true}), http.StatusOK, "")
	expectStatus(t, "collab save of a locked page", collabSave(a, art.ID, "Together", "x", nil), http.StatusConflict, "article_locked")
}

// An erased user's authorship of updates is forgotten, and the rows stay
// (K17).
func TestDocLog_ErasureNullsAuthors(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	a := signIn(t, h)
	gone := uuid.New()
	art := create(t, a, "", "workspace", "Erase")
	h.Exec(t, `INSERT INTO knowledge.article_updates (article_id, seq, data, created_by, created_at) VALUES ($1, 1, '\x01', $2, now()), ($1, 2, '\x02', $3, now())`, art.ID, gone, a.id)

	res, err := retentionCycle(t, h)
	if err != nil || res.Erased != 1 {
		t.Fatalf("retention = %+v, %v, want one erased user", res, err)
	}
	if n := h.Count(t, `SELECT count(*) FROM knowledge.article_updates WHERE article_id = $1`, art.ID); n != 2 {
		t.Errorf("%d rows kept, want 2", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM knowledge.article_updates WHERE created_by = $1`, gone); n != 0 {
		t.Errorf("%d rows still name the erased user", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM knowledge.article_updates WHERE created_by = $1`, a.id); n != 1 {
		t.Errorf("%d rows name the other author, want 1", n)
	}
}
