package knowledge

// The document store of live co-editing (knowledge design K24-K32): the
// module's live.DocStore over knowledge.article_updates. The server relays and
// stores opaque Yjs updates and never decodes one. Every write takes the
// article's row lock (the one a content save takes) for its short
// transaction and never the tree's advisory lock (K5), so it serialises with a
// content save of the same article and never with a move.

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/knowledge/store"
	"github.com/vantigo-io/vantigo/server/internal/live"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// The log's bounds (K27): compaction is demanded above the first pair, and
// updates are refused beyond the second.
const (
	logDemandBytes = 4 << 20
	logDemandRows  = 2000
	logFullBytes   = 2 * logDemandBytes
	logFullRows    = 2 * logDemandRows
)

// docStore is the knowledge.doc topic's live.DocStore.
type docStore struct {
	deps module.Deps
	// The bounds, fields so that a test can lower them.
	demandBytes, demandRows, fullBytes, fullRows int64
}

func newDocStore(d module.Deps) *docStore {
	return &docStore{deps: d, demandBytes: logDemandBytes, demandRows: logDemandRows, fullBytes: logFullBytes, fullRows: logFullRows}
}

func (s *docStore) tx(ctx context.Context, fn func(tx pgx.Tx, q *store.Queries) error) error {
	return db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		return fn(tx, store.New(tx))
	})
}

// docOf parses a topic key into the article's id.
func docOf(key string) (uuid.UUID, error) {
	id, err := uuid.Parse(key)
	if err != nil {
		return uuid.Nil, fmt.Errorf("knowledge: %q is not a document key: %w", key, live.ErrDocReadOnly)
	}
	return id, nil
}

// CanWrite: write access, the page not locked and not trashed (K26).
func (s *docStore) CanWrite(ctx context.Context, key string) bool {
	id, err := uuid.Parse(key)
	user, ok := contracts.AsUserFrom(ctx)
	if err != nil || !ok {
		return false
	}
	m, err := store.New(s.deps.Pool).ArticleMetaFor(ctx, store.ArticleMetaForParams{UserID: user, ID: id})
	if err != nil {
		return false
	}
	return m.TrashedAt == nil && !m.Locked && m.Permission == levelWrite
}

// compactionCooldown spaces the requests to compact one document, across
// replicas: the time of the last is on the article row (K31).
const compactionCooldown = time.Minute

// collabActiveWindow is how recently a non-empty log must have taken a row
// for a single-editor save or a restore to be refused (collab_active): past it
// the log is stale, and the write resets it as it always did.
const collabActiveWindow = 2 * time.Minute

// refuseActiveLog is the 409 collab_active when the article's log is non-empty
// and took a row within collabActiveWindow. The caller holds the row lock.
func refuseActiveLog(ctx context.Context, q *store.Queries, id uuid.UUID, now time.Time) error {
	st, err := q.DocState(ctx, id)
	if err != nil {
		return fmt.Errorf("knowledge: read the log's state of %s: %w", id, err)
	}
	if st.UpdateRows > 0 && st.LastAppendAt != nil && now.Sub(*st.LastAppendAt) < collabActiveWindow {
		return collabActive()
	}
	return nil
}

// guard runs under the article's row lock, in the transaction of a write: the
// person must still be able to write (write access, not locked, not trashed;
// ErrDocReadOnly), and the write's epoch must be the log's (a *live.StaleError
// otherwise). It answers the log's state.
func guard(ctx context.Context, q *store.Queries, id uuid.UUID, in live.DocWrite) (store.DocStateRow, error) {
	var zero store.DocStateRow
	if err := q.LockArticle(ctx, id); err != nil {
		return zero, fmt.Errorf("knowledge: lock article %s: %w", id, err)
	}
	m, err := q.ArticleMetaFor(ctx, store.ArticleMetaForParams{UserID: in.User, ID: id})
	if noRows(err) {
		return zero, live.ErrDocReadOnly
	}
	if err != nil {
		return zero, fmt.Errorf("knowledge: read article %s: %w", id, err)
	}
	if m.TrashedAt != nil || m.Locked || m.Permission != levelWrite {
		return zero, live.ErrDocReadOnly
	}
	st, err := q.DocState(ctx, id)
	if err != nil {
		return zero, fmt.Errorf("knowledge: read the log's state of %s: %w", id, err)
	}
	if in.Epoch != st.Epoch {
		return zero, &live.StaleError{Epoch: st.Epoch}
	}
	return st, nil
}

// Replay streams the rows after `after` and answers the newest seq and the
// epoch (K28, K32). A client holding another epoch than the current one is
// sent no row.
func (s *docStore) Replay(ctx context.Context, key string, after, epoch int64, emit func(seq int64, data []byte) error) (live.ReplayResult, error) {
	id, err := docOf(key)
	if err != nil {
		return live.ReplayResult{}, err
	}
	rows, err := store.New(s.deps.Pool).ListUpdatesAfter(ctx, store.ListUpdatesAfterParams{ArticleID: id, After: after})
	if err != nil {
		return live.ReplayResult{}, fmt.Errorf("knowledge: read the log of %s: %w", id, err)
	}
	var res live.ReplayResult
	if len(rows) > 0 {
		res.Last, res.Epoch = rows[0].Newest, rows[0].Epoch
	}
	if epoch != 0 && epoch != res.Epoch {
		res.Stale = true
		return res, nil
	}
	for _, r := range rows {
		if r.Seq == nil {
			continue
		}
		if err := emit(*r.Seq, r.Data); err != nil {
			return live.ReplayResult{}, err
		}
	}
	return res, nil
}

// Append stores one update at max(seq) + 1 under the row lock and notifies on
// the same transaction (K27). It refuses an empty log (no seed yet), and a
// full one, answering the log's newest seq and whether to ask the writer to
// compact.
func (s *docStore) Append(ctx context.Context, key string, in live.DocWrite) (live.DocResult, error) {
	id, err := docOf(key)
	if err != nil {
		return live.DocResult{}, err
	}
	var (
		res  live.DocResult
		full bool
	)
	err = s.tx(ctx, func(tx pgx.Tx, q *store.Queries) error {
		st, err := guard(ctx, q, id, in)
		if err != nil {
			return err
		}
		if st.UpdateRows == 0 {
			return live.ErrDocNotSeeded
		}
		rows, bytes := st.UpdateRows+1, st.UpdateBytes+int64(len(in.Data))
		full = rows > s.fullRows || bytes > s.fullBytes
		now := s.deps.Clock()
		if full || rows > s.demandRows || bytes > s.demandBytes {
			if st.LastCompactionAskAt == nil || now.Sub(*st.LastCompactionAskAt) >= compactionCooldown {
				if err := q.MarkCompactionAsked(ctx, store.MarkCompactionAskedParams{ID: id, Now: now}); err != nil {
					return fmt.Errorf("knowledge: note a compaction ask of %s: %w", id, err)
				}
				res.CompactionDemanded = true
			}
		}
		if full {
			rng, err := q.UpdateSeqRange(ctx, id)
			if err != nil {
				return fmt.Errorf("knowledge: measure the log of %s: %w", id, err)
			}
			res.Seq = rng.LastSeq
			return nil // commit the ask; the refusal is answered below
		}
		seq, err := q.AppendUpdate(ctx, store.AppendUpdateParams{ArticleID: id, Data: in.Data, CreatedBy: in.User, Now: now})
		if err != nil {
			return fmt.Errorf("knowledge: append to the log of %s: %w", id, err)
		}
		if err := q.AddDocCounters(ctx, store.AddDocCountersParams{ID: id, Rows: 1, Bytes: int64(len(in.Data)), Now: now}); err != nil {
			return fmt.Errorf("knowledge: count the log of %s: %w", id, err)
		}
		res.Seq = seq
		return live.NotifyDoc(ctx, tx, liveDoc+":"+id.String(), seq, in.Hub)
	})
	if err == nil && full {
		err = live.ErrDocLogFull
	}
	return res, err
}

// Seed stores the first row, only when the log is empty and the article is
// still at the version the seed was built from (K29): the insert is
// conditional and runs under the row lock, so of many concurrent seeds exactly
// one is accepted.
func (s *docStore) Seed(ctx context.Context, key string, in live.DocWrite) (accepted bool, seq int64, err error) {
	id, err := docOf(key)
	if err != nil {
		return false, 0, err
	}
	err = s.tx(ctx, func(tx pgx.Tx, q *store.Queries) error {
		st, err := guard(ctx, q, id, in)
		if err != nil {
			return err
		}
		if st.UpdateRows > 0 || int64(st.Version) != in.Version {
			return nil
		}
		n, err := q.SeedIfEmpty(ctx, store.SeedIfEmptyParams{ArticleID: id, Data: in.Data, CreatedBy: in.User, Now: s.deps.Clock()})
		if err != nil {
			return fmt.Errorf("knowledge: seed the log of %s: %w", id, err)
		}
		if n == 0 {
			return nil
		}
		if err := q.SetDocCounters(ctx, store.SetDocCountersParams{ID: id, Rows: 1, Bytes: int64(len(in.Data)), Now: s.deps.Clock()}); err != nil {
			return fmt.Errorf("knowledge: count the log of %s: %w", id, err)
		}
		accepted, seq = true, 1
		return live.NotifyDoc(ctx, tx, liveDoc+":"+id.String(), seq, in.Hub)
	})
	return accepted, seq, err
}

// Compact replaces the rows up to upTo with one row at upTo, in one
// transaction under the row lock (K31). It refuses, changing nothing, when
// upTo is not above the log's first seq (a late compaction: the log is
// contiguous from its first seq, and a compaction leaves its row at upTo), is
// above its newest, or the data is over 2 MiB. It is not notified: the state
// is the same as the rows it replaces.
func (s *docStore) Compact(ctx context.Context, key string, upTo int64, in live.DocWrite) (bool, error) {
	id, err := docOf(key)
	if err != nil {
		return false, err
	}
	if len(in.Data) == 0 || len(in.Data) > live.MaxDocState {
		return false, nil
	}
	ok := false
	err = s.tx(ctx, func(_ pgx.Tx, q *store.Queries) error {
		st, err := guard(ctx, q, id, in)
		if err != nil {
			return err
		}
		rng, err := q.UpdateSeqRange(ctx, id)
		if err != nil {
			return fmt.Errorf("knowledge: measure the log of %s: %w", id, err)
		}
		if st.UpdateRows < 2 || upTo <= rng.FirstSeq || upTo > rng.LastSeq {
			return nil
		}
		gone, err := q.CompactUpdates(ctx, store.CompactUpdatesParams{ArticleID: id, UpTo: upTo})
		if err != nil {
			return fmt.Errorf("knowledge: compact the log of %s: %w", id, err)
		}
		if err := q.InsertUpdateAt(ctx, store.InsertUpdateAtParams{ArticleID: id, Seq: upTo, Data: in.Data, CreatedBy: in.User, Now: s.deps.Clock()}); err != nil {
			return fmt.Errorf("knowledge: store the compacted row of %s: %w", id, err)
		}
		err = q.SetDocCounters(ctx, store.SetDocCountersParams{
			ID: id, Rows: int32(st.UpdateRows - gone.Rows + 1), Bytes: st.UpdateBytes - gone.Bytes + int64(len(in.Data)), Now: s.deps.Clock(),
		})
		if err != nil {
			return fmt.Errorf("knowledge: count the log of %s: %w", id, err)
		}
		ok = true
		return nil
	})
	return ok, err
}

// Fetch reads the row of a notification from another hub; found is false when
// compaction removed it.
func (s *docStore) Fetch(ctx context.Context, key string, seq int64) ([]byte, bool, error) {
	id, err := docOf(key)
	if err != nil {
		return nil, false, err
	}
	data, err := store.New(s.deps.Pool).GetUpdate(ctx, store.GetUpdateParams{ArticleID: id, Seq: seq})
	if noRows(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("knowledge: read row %d of %s: %w", seq, id, err)
	}
	return data, true, nil
}

// resetLog deletes an article's log in the caller's transaction, starts a new
// epoch and tells the document's subscribers (K32): the JSON is the truth
// again, and the next open seeds from it. The caller holds the article's row
// lock.
func resetLog(ctx context.Context, tx pgx.Tx, q *store.Queries, id uuid.UUID) error {
	epoch, err := q.ResetDocLog(ctx, id)
	if err != nil {
		return fmt.Errorf("knowledge: reset the log of %s: %w", id, err)
	}
	if _, err := q.DeleteUpdates(ctx, id); err != nil {
		return fmt.Errorf("knowledge: reset the log of %s: %w", id, err)
	}
	if err := live.NotifyDocReset(ctx, tx, liveDoc+":"+id.String(), epoch); err != nil {
		return fmt.Errorf("knowledge: notify the reset of %s: %w", id, err)
	}
	return nil
}
