package projects_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/projects"
)

// This file pins design §3.3's currency guard against the write it is most
// at risk from: a billing line created with a budgetAmount at the exact
// moment another request clears the project's currency. Both writers now
// take the project's own row lock (LockProject, FOR UPDATE) as their first
// statement inside their own transaction, which serialises the two rather
// than letting one count against a row the other has not committed yet.
// Neither writer's own lock alone is enough — only one held by both, taken
// first, actually closes the race (task-1-report.md's Fix round 1). Run
// with -count=10 or more to exercise the timing; -race cannot catch what
// this pins, because a lost guard decision is database behaviour, not a Go
// data race.

// TestPostProjectsByIdBillingLines_RacingAClearOfTheCurrency_NeverLeavesABudgetedLineWithoutOne
// races, many times over, "create a billing line with a budgetAmount"
// against "clear the project's currency". Whichever wins, the loser must be
// refused (400, on 'budgetAmount' if the line lost the race for the lock, on
// 'currency' if the clear did) — never both committed, which is the state
// design §3.3 exists to prevent: an amount denominated in a currency the
// project no longer has.
func TestPostProjectsByIdBillingLines_RacingAClearOfTheCurrency_NeverLeavesABudgetedLineWithoutOne(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")

	const rounds = 20
	for i := range rounds {
		project := createProject(t, c, map[string]any{
			"code": fmt.Sprintf("RACECUR%04d", i), "currency": "NOK",
		})

		postBudgetedLine := func() *modtest.Response {
			return postLine(t, c, project.Id, map[string]any{"budgetAmount": 5000})
		}
		clearCurrency := func() *modtest.Response {
			return updateProject(t, c, project, map[string]any{"currency": nil})
		}

		oneSucceeded, oneRefused := false, false
		for _, r := range race(postBudgetedLine, clearCurrency) {
			switch r.Status {
			case http.StatusCreated, http.StatusOK:
				oneSucceeded = true
			case http.StatusBadRequest:
				oneRefused = true
			default:
				t.Fatalf("round %d: status %d body %s, want 200/201 or 400", i, r.Status, r.Body)
			}
		}
		if !oneSucceeded || !oneRefused {
			t.Fatalf("round %d: want exactly one side to win and the other refused, got succeeded=%v refused=%v",
				i, oneSucceeded, oneRefused)
		}

		// The invariant design §3.3 exists to protect, whichever side won:
		// no billing line's amount is left denominated in a currency the
		// project no longer has.
		if n := h.Count(t, `
			SELECT count(*) FROM projects.billing_lines bl
			JOIN projects.projects p ON p.id = bl.project_id
			WHERE bl.project_id = $1 AND p.currency IS NULL
			  AND (bl.budget_amount IS NOT NULL OR bl.fixed_amount IS NOT NULL)`, project.Id); n != 0 {
			t.Fatalf("round %d: %d billing line(s) with an amount on a project with no currency", i, n)
		}
	}
}

// lockProbeCatalog wraps fakeCatalog and, on every Variant lookup, checks
// whether the named project's row is locked right now by attempting
// `SELECT ... FOR UPDATE NOWAIT` over the harness's own pool — the identical
// *pgxpool.Pool the module's own transactions run on (modtest.Harness.Pool
// is what Compose wires into Deps.Pool). NOWAIT never waits: run outside any
// transaction of its own, it is a single autocommitted statement, so it
// either takes the row's lock and releases it immediately, or fails at once
// with 55P03 if some other session — this module's own LockProject — already
// holds it. A failure recorded here is exactly the bug fixed in this round:
// a cross-module call must never run while this module holds the project's
// row lock.
//
// The module now also has the harness-wide guarantee this probe was once a
// stand-in for: withProjectLock marks every locked transaction's context and
// the package's contract-call hook records anything asked of another module
// under it (contracts.go, harness_test.go's lockedContractCalls), which
// catches this class of bug on every write path rather than on the one a fix
// happened to touch. The probe is kept beside it because it proves something
// the mark cannot: that the row is *actually* locked in Postgres at the
// moment of the call, rather than that the code believes it is. The two fail
// independently, which is the point — a marking that drifted off a
// transaction would leave this test still failing.
type lockProbeCatalog struct {
	*fakeCatalog
	pool      *pgxpool.Pool
	projectID int32

	calledWhileLocked []string
}

func (c *lockProbeCatalog) Variant(ctx context.Context, id int32) (*contracts.VariantEntry, error) {
	c.probe("Variant")
	return c.fakeCatalog.Variant(ctx, id)
}

// Variants and ListPrice are probed for the same reason Variant is: they are
// the two the *response* is rendered from (responses.go), and a renderer that
// drifted inside a transaction would stall every other writer of the project
// just as surely as a validator that did. Every method of the contract this
// module calls is covered, so the probe cannot be outgrown by a new call site.
func (c *lockProbeCatalog) Variants(ctx context.Context, ids []int32) ([]contracts.VariantEntry, error) {
	c.probe("Variants")
	return c.fakeCatalog.Variants(ctx, ids)
}

func (c *lockProbeCatalog) ListPrice(ctx context.Context, variantID int32, currency string, at time.Time) (*contracts.Money, error) {
	c.probe("ListPrice")
	return c.fakeCatalog.ListPrice(ctx, variantID, currency, at)
}

// probe is not safe for concurrent use — this file's other test races
// requests, but the one below that uses lockProbeCatalog does not, so a
// plain slice is enough.
func (c *lockProbeCatalog) probe(method string) {
	if c.pool == nil || c.projectID == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := c.pool.Exec(ctx, `SELECT 1 FROM projects.projects WHERE id = $1 FOR UPDATE NOWAIT`, c.projectID); err != nil {
		c.calledWhileLocked = append(c.calledWhileLocked, method)
	}
}

// TestPutProjectsByIdBillingLines_VariantLookup_NeverRunsWhileTheProjectIsLocked
// pins this round's fix: the catalog must never be asked anything while this
// module holds the project's row lock. Moving a line to a different variant
// asks it three times over — once to validate the variant (D9's rule) and
// twice to render the answer (the variants' names, the new variant's list
// price) — and the probe covers all three. Before the fix,
// PutProjectsByIdBillingLinesByLineId asked about the variant from inside the
// transaction that holds LockProject (and LockBillingLine); the probe would
// have found the row locked and recorded the failure.
func TestPutProjectsByIdBillingLines_VariantLookup_NeverRunsWhileTheProjectIsLocked(t *testing.T) {
	t.Parallel()
	catalog := &lockProbeCatalog{fakeCatalog: newFakeCatalog()}
	// Deliberately modtest.New rather than newProjectsHarness: this test *is*
	// the probe, and it needs a catalog of its own wrapping the fake. It is
	// therefore the one harness in the package without the suite-wide
	// contract-call check — which is exactly the check this test duplicates
	// from the other side, against Postgres rather than against a context mark.
	h := modtest.New(t,
		modtest.WithRecorder(recorder),
		modtest.WithModule(projects.Module()),
		modtest.WithDirectory(fakeDirectory{}),
		modtest.WithProducts(catalog),
	)
	catalog.pool = h.Pool()

	c, _ := signIn(t, h, "projects:create")
	// A currency, so rendering the line actually asks for a list price: with
	// none, linePricing answers before the catalog is reached and ListPrice
	// would never be probed at all.
	project := createProject(t, c, map[string]any{"code": "LOCKPROBE1000", "currency": "NOK"})
	catalog.projectID = project.Id
	line := createLine(t, c, project.Id, nil)

	changeLine(t, c, project.Id, line.Id, lineBody(map[string]any{"variantId": variantDeveloperHour}))

	if calls := catalog.calledWhileLocked; len(calls) > 0 {
		t.Errorf("catalog called while the project's row lock was held: %v", calls)
	}
}
