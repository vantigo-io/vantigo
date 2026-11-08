package customers

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/live"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// The live topic of the imports screens (live design K38). Events carry the
// import's id only: a screen that hears one refetches through its ordinary
// request, which checks the person's permission.
const (
	// liveImports is the imports list's and an import's page's topic, keyless:
	// import.changed with the import's id, or without one after a sweep.
	liveImports = "customers.imports"

	eventImportChanged = "import.changed"

	// importProgressEvery is the least time between two progress
	// notifications of one import (K37): a state change always notifies, a
	// progress write at most this often.
	importProgressEvery = time.Second
)

// liveTopics is the module's LiveTopics. The topic is authorised by the rule
// GET /customers/imports applies: customers:import and customers:view.
func liveTopics(module.Deps) []live.Topic {
	return []live.Topic{
		{Kind: liveImports, Authorize: func(ctx context.Context, key string) error {
			if key != "" || !live.HasPermission(ctx, "customers:import", "customers:view") {
				return live.ErrForbidden
			}
			return nil
		}},
	}
}

// notifyImport tells the imports screens that import id changed. It runs on q,
// the transaction that wrote (live design L6), so it is heard only if that
// commits.
func notifyImport(ctx context.Context, q live.Execer, id int32) error {
	return live.Notify(ctx, q, liveImports, eventImportChanged, strconv.FormatInt(int64(id), 10))
}

// notifyImportsSwept tells the imports screens that imports were removed in
// bulk: one event without an id, never one per import (L6).
func notifyImportsSwept(ctx context.Context, q live.Execer) error {
	return live.Notify(ctx, q, liveImports, eventImportChanged, "")
}

// notifyImportOnPool is notifyImport after a single write made on the pool,
// outside a transaction. A failure to send is logged, not answered: the write
// is made, and the screens' fallback poll repairs a lost event.
func (s *server) notifyImportOnPool(ctx context.Context, id int32) {
	if err := notifyImport(ctx, s.deps.Pool, id); err != nil {
		s.deps.Logger.WarnContext(ctx, "customers: notify an import", "error", err)
	}
}

// notifyImportFromWorker is the worker's: on the lease's connection q, with a
// failure logged rather than failing the run.
func (w *ImportWorker) notifyImportFromWorker(ctx context.Context, q live.Execer, id int32) {
	if err := notifyImport(ctx, q, id); err != nil {
		w.logger().Warn("customers: notify an import", "worker", importWorkerName, "importId", id, "error", err)
	}
}

// importProgress is a run's throttle (K37): a progress notification goes out at
// most once a second, and one suppressed inside the window is sent when the
// window ends, on the pool (the leased connection is the worker's alone), so
// the last progress is never left unheard.
type importProgress struct {
	mu    sync.Mutex
	last  time.Time
	timer *time.Timer
}

// notify sends import.changed on q unless one went out within
// importProgressEvery, in which case one trailing event is scheduled.
func (p *importProgress) notify(ctx context.Context, w *ImportWorker, q live.Execer, id int32) {
	p.mu.Lock()
	defer p.mu.Unlock()
	since := time.Since(p.last)
	if p.last.IsZero() || since >= importProgressEvery {
		p.last = time.Now()
		w.notifyImportFromWorker(ctx, q, id)
		return
	}
	if p.timer == nil {
		p.timer = time.AfterFunc(importProgressEvery-since, func() {
			p.mu.Lock()
			p.timer, p.last = nil, time.Now()
			p.mu.Unlock()
			w.notifyImportFromWorker(context.WithoutCancel(ctx), w.deps.Pool, id)
		})
	}
}

// stop drops a trailing event still waiting: the run ended and its final state
// was notified.
func (p *importProgress) stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.timer != nil {
		p.timer.Stop()
		p.timer = nil
	}
}
