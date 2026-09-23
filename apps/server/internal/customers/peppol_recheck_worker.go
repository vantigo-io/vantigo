package customers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/customers/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

// This file is the Peppol re-check worker (registry workers design D5, D6): it
// asks the Peppol network, on a schedule, the question a person's click asks
// today.
//
// What it deliberately does NOT do is decide anything. A lapsed registration
// surfaces through the billing profile's existing warnings
// (ehf_recipient_not_registered, ehf_available), which already derive from the
// stored lookup — so this worker needs no new code to be visible and adds no
// attention item of its own. It never switches a customer's invoiceDelivery
// either, in either direction: phase 2 delivery B's decision 2 stands, and only
// a person changes where a customer's invoices go.
//
// The two candidate sets are not symmetrical, and the asymmetry is the design:
//
//   - an aged stored answer, whose participant is still the one the customer
//     resolves to today. A lookup for a participant that changed is already
//     stale by identity — the billing profile drops it from every response and
//     every warning — so refreshing it would update a row nothing reads, and
//     re-stamping its checked_at would make it look current while naming the
//     wrong participant. Such a row is deleted rather than skipped: skipping it
//     leaves it aged forever, holding a place in every cycle's batch;
//   - a customer whose invoice_delivery is already 'ehf' and who has NO stored
//     answer at all. "Could this customer receive EHF" is a question a person
//     asks with a click; "is the customer we are already sending EHF to
//     actually registered" is one nobody should have to ask.

const (
	// peppolRecheckWorkerName is what the runner logs this worker as, in the
	// same <module>-<worker> spelling as the feed worker beside it.
	peppolRecheckWorkerName = "customers-peppol-recheck"

	// peppolRecheckLeaseKey is the ASCII string "CUSTPEP1" read as a big-endian
	// 64-bit value (design D5) — its own key, not the feed worker's: the two do
	// unrelated work and one holding the other's lease would silently halve
	// both.
	peppolRecheckLeaseKey int64 = 0x4355535450455031

	// peppolRecheckBatch bounds one cycle at a hundred customers (design D6).
	// Each one is a DNS query and possibly an SMP request against a public
	// network, one at a time, so the batch is what keeps a large installation's
	// nightly cycle from looking like a crawl of it.
	peppolRecheckBatch = 100

	// peppolRecheckEhfShare is how much of that batch the EHF set may take
	// (Task 4 review). The EHF set goes first because it matters most, but
	// "first" must not mean "all": an installation that switches two hundred
	// customers to EHF in an afternoon would otherwise spend every cycle on that
	// queue, and an aged answer behind it is re-checked no sooner than the queue
	// ends. Half each means the aged half always has fifty places, however long
	// the other queue is, and a cycle whose EHF set is small still spends the
	// whole batch (the aged set asks for whatever the EHF set left).
	peppolRecheckEhfShare = 50

	// The defaults a worker built from a Deps with no Config falls back on — the
	// same values config.go defaults CUSTOMERS_PEPPOL_RECHECK_POLL and
	// CUSTOMERS_PEPPOL_RECHECK_AGE to.
	defaultPeppolRecheckPoll = 24 * time.Hour
	defaultPeppolRecheckAge  = 720 * time.Hour
)

// PeppolRecheckWorker re-asks the Peppol network about customers whose stored
// answer has aged, and about customers already set to receive EHF who have no
// stored answer at all. It implements worker.Worker.
type PeppolRecheckWorker struct {
	deps module.Deps
	srv  *server
}

var _ worker.Worker = (*PeppolRecheckWorker)(nil)

// NewPeppolRecheckWorker builds the worker over d. s.peppolLookup is nil
// whenever PEPPOL_LOOKUP_ENABLED is off (newServer's own rule), and the module
// does not register this worker in that case — so a cycle never has to wonder
// whether it has a network to ask.
func NewPeppolRecheckWorker(d module.Deps) *PeppolRecheckWorker {
	return &PeppolRecheckWorker{deps: d, srv: newServer(d)}
}

// Name identifies this worker in the runner's logs.
func (w *PeppolRecheckWorker) Name() string { return peppolRecheckWorkerName }

// Interval is the poll cadence between cycles (CUSTOMERS_PEPPOL_RECHECK_POLL,
// default 24 hours).
func (w *PeppolRecheckWorker) Interval() time.Duration {
	if w.deps.Config != nil && w.deps.Config.CustomersPeppolRecheckPoll > 0 {
		return w.deps.Config.CustomersPeppolRecheckPoll
	}
	return defaultPeppolRecheckPoll
}

// Run is the worker loop, the same shape the feed worker's is.
func (w *PeppolRecheckWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.Interval())
	defer ticker.Stop()
	for {
		if _, err := w.RunCycle(ctx); err != nil && ctx.Err() == nil {
			w.logger().Error("peppol re-check cycle failed", "worker", peppolRecheckWorkerName, "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// RunCycle is one cycle under the advisory lease, reporting whether it ran:
// the never-checked EHF customers first, then the aged answers, up to
// peppolRecheckBatch between them.
//
// The EHF customers come first because they are the ones whose invoices are
// already being sent somewhere nobody has verified: on an installation with
// more aged answers than the batch, the customer that matters most must not be
// the one that never fits. They take at most peppolRecheckEhfShare of the batch
// for the mirror image of that reason — the aged set must not be the one that
// never fits either — and the aged set then asks for whatever is left, so a
// cycle with few EHF candidates still spends the whole batch.
func (w *PeppolRecheckWorker) RunCycle(ctx context.Context) (bool, error) {
	return w.underLease(ctx, func(ctx context.Context) error {
		if w.srv.peppolLookup == nil {
			// Unreachable through module.Workers, which does not register this
			// worker without the lookup — but a worker constructed directly
			// answers "nothing to do" rather than panicking on a nil seam.
			return nil
		}
		q := store.New(w.deps.Pool)

		missing, err := q.EhfCustomersWithoutPeppolLookup(ctx, peppolRecheckEhfShare)
		if err != nil {
			return fmt.Errorf("customers: select ehf customers without a peppol lookup: %w", err)
		}
		checked, changed, failed, dropped := 0, 0, 0, 0
		for _, row := range missing {
			if ctx.Err() != nil {
				return nil
			}
			profile := billingProfileFromRow(row.InvoiceEmail, row.ReminderEmail, row.PaymentTermsDays,
				row.Currency, row.Language, row.InvoiceDelivery, row.ReminderDelivery, row.PeppolID, row.Gln, row.BuyerReference)
			identity := identityFromRow(row.LegalCountry, row.LegalID, row.LegalName, row.LegalSource, row.LegalType)
			participant, _ := lookupParticipant(profile, identity, row.Type)
			if participant == "" {
				// invoiceDelivery says ehf but nothing says who to: the billing
				// profile's own ehf_without_recipient warning already reports that,
				// and there is nothing to ask the network about.
				continue
			}
			if ok, news := w.recheck(ctx, row.ID, participant); ok {
				checked++
				if news {
					changed++
				}
			} else {
				failed++
			}
		}

		remaining := peppolRecheckBatch - len(missing)
		if remaining > 0 {
			aged, err := q.AgedPeppolLookups(ctx, store.AgedPeppolLookupsParams{
				CheckedBefore: w.now().Add(-w.recheckAge()), RowLimit: int32(remaining),
			})
			if err != nil {
				return fmt.Errorf("customers: select aged peppol lookups: %w", err)
			}
			for _, row := range aged {
				if ctx.Err() != nil {
					return nil
				}
				profile := billingProfileFromRow(row.InvoiceEmail, row.ReminderEmail, row.PaymentTermsDays,
					row.Currency, row.Language, row.InvoiceDelivery, row.ReminderDelivery, row.PeppolID, row.Gln, row.BuyerReference)
				identity := identityFromRow(row.LegalCountry, row.LegalID, row.LegalName, row.LegalSource, row.LegalType)
				participant, _ := lookupParticipant(profile, identity, row.Type)
				// The participant rule (design D6, this file's header): only a
				// lookup that is still about the participant this customer
				// resolves to today is this worker's to refresh. The rest are not
				// left to sit either — a row nothing reads whose checked_at never
				// moves would hold a place in every cycle's batch for the rest of
				// the installation's life — so they go, and the customer's next
				// lookup is a first one, which is what it is for the participant it
				// resolves to now.
				if participant == "" || participant != row.ParticipantID {
					if w.drop(ctx, row.ID) {
						dropped++
					}
					continue
				}
				if ok, news := w.recheck(ctx, row.ID, participant); ok {
					checked++
					if news {
						changed++
					}
				} else {
					failed++
				}
			}
		}
		// changed is the number worth reading of the four: a cycle that checked a
		// hundred customers and moved none of their answers is the healthy case,
		// and one that moved many is the day something happened in the register.
		// dropped is there so a row disappearing is never a mystery.
		w.logger().Info("peppol re-check cycle finished", "worker", peppolRecheckWorkerName,
			"checked", checked, "changed", changed, "failed", failed, "dropped", dropped)
		return nil
	})
}

// underLease is design D5's lease, the same shape the feed worker's and
// communications/retention.go's are (see either for why the unlock runs on a
// context stripped of cancellation and why a failed unlock discards the
// connection), on this worker's own key.
func (w *PeppolRecheckWorker) underLease(ctx context.Context, action func(context.Context) error) (bool, error) {
	conn, err := w.deps.Pool.Acquire(ctx)
	if err != nil {
		return false, fmt.Errorf("customers: acquire a connection for the peppol re-check lease: %w", err)
	}
	defer conn.Release()

	var acquired bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, peppolRecheckLeaseKey).Scan(&acquired); err != nil {
		return false, fmt.Errorf("customers: take the peppol re-check lease: %w", err)
	}
	if !acquired {
		w.logger().Debug("the customers peppol re-check lease is held by another replica; skipping this cycle",
			"worker", peppolRecheckWorkerName)
		return false, nil
	}
	defer func() {
		release := context.WithoutCancel(ctx)
		if _, err := conn.Exec(release, `SELECT pg_advisory_unlock($1)`, peppolRecheckLeaseKey); err != nil {
			w.logger().Error("releasing the peppol re-check lease failed; discarding the connection",
				"worker", peppolRecheckWorkerName, "error", err)
			_ = conn.Conn().Close(release)
		}
	}()
	return true, action(ctx)
}

// recheck is one customer asked again through the click's own function, with
// the system actor, reporting whether it succeeded and whether the answer was
// news (the cycle's own log line, design D6 — nothing else acts on it: a lapsed
// registration is already visible through the billing profile's warnings, and
// the timeline event is written inside lookupAndStorePeppol). A network failure is
// already logged by kind inside lookupAndStorePeppol and leaves checked_at
// alone, so the customer is first in line next cycle (design D6); a database
// failure is logged here, because a cycle that stopped at the first one would
// leave the rest of the batch unchecked for a whole poll interval over a
// problem that may be one row's.
func (w *PeppolRecheckWorker) recheck(ctx context.Context, customerID int32, participant string) (ok, changed bool) {
	outcome, err := w.srv.lookupAndStorePeppol(ctx, customerID, participant, generatedFallbackActor)
	if err != nil {
		// Not while the process is shutting down: a cancellation landing inside
		// db.WithTx is this cycle being told to stop, not a database problem, and
		// Run's own loop applies the same guard to the same effect — an operator
		// reading Error lines must find only things that were actually wrong.
		if !errors.Is(err, errPeppolLookupUnavailable) && ctx.Err() == nil {
			w.logger().Error("customers: storing a peppol re-check failed",
				"worker", peppolRecheckWorkerName, "customerId", customerID, "error", err.Error())
		}
		return false, false
	}
	return true, outcome.Changed
}

// drop deletes a stored answer whose participant the customer no longer
// resolves to (design D6's participant rule), reporting whether it went. A
// database failure is logged and swallowed for recheck's own reason — a cycle
// that stopped at the first one would leave the rest of the batch unchecked for
// a whole poll interval over what may be one row's problem — and the row is
// simply dropped next cycle instead. A cancellation is not logged, as above.
func (w *PeppolRecheckWorker) drop(ctx context.Context, customerID int32) bool {
	if err := store.New(w.deps.Pool).DeleteCustomerPeppolLookup(ctx, customerID); err != nil {
		if ctx.Err() == nil {
			w.logger().Error("customers: dropping a peppol lookup whose participant changed failed",
				"worker", peppolRecheckWorkerName, "customerId", customerID, "error", err.Error())
		}
		return false
	}
	return true
}

// recheckAge is how old a stored answer must be before it is asked again
// (CUSTOMERS_PEPPOL_RECHECK_AGE, default 720 hours).
func (w *PeppolRecheckWorker) recheckAge() time.Duration {
	if w.deps.Config != nil && w.deps.Config.CustomersPeppolRecheckAge > 0 {
		return w.deps.Config.CustomersPeppolRecheckAge
	}
	return defaultPeppolRecheckAge
}

// now is the worker's clock, so tests control time exactly as they do for the
// handlers.
func (w *PeppolRecheckWorker) now() time.Time {
	if w.deps.Clock != nil {
		return w.deps.Clock().UTC()
	}
	return time.Now().UTC()
}

func (w *PeppolRecheckWorker) logger() *slog.Logger {
	if w.deps.Logger != nil {
		return w.deps.Logger
	}
	return slog.Default()
}
