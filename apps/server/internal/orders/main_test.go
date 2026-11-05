package orders_test

import (
	"os"
	"sync"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/openapi/contracttest"
	"github.com/vantigo-io/vantigo/server/internal/orders"
)

// recorder validates every exchange of every orders test against
// orders.yaml and records which operations answered successfully; TestMain
// turns it into the coverage gate.
var recorder = contracttest.NewForModule("orders")

// pending is the operations not built yet, which the gate lets go uncovered:
// none.
var pending []string

// pdfModels is every PDF model built, for the tests that read what a PDF
// says (the bytes are embedded-subset and cannot be read back).
var pdfModels = &models{}

type models struct {
	mu  sync.Mutex
	all []orders.PDFModel
}

func (m *models) add(p orders.PDFModel) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.all = append(m.all, p)
}

// find is the last model whose title is title, whose subtitle is subtitle and
// which every want accepts. Tests run in parallel, each on its own database, so
// two tests print "Tilbud 1" alike: a test that reads what its PDF says passes
// a want that tells its own model apart, or it may read another test's.
func (m *models) find(title, subtitle string, want ...func(orders.PDFModel) bool) (orders.PDFModel, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
next:
	for i := len(m.all) - 1; i >= 0; i-- {
		if m.all[i].Title != title || m.all[i].Subtitle != subtitle {
			continue
		}
		for _, ok := range want {
			if !ok(m.all[i]) {
				continue next
			}
		}
		return m.all[i], true
	}
	return orders.PDFModel{}, false
}

func TestMain(m *testing.M) {
	// The whole suite's lock rule, installed once before any test runs: every
	// call this module makes out of its own database is reported here and,
	// when made from inside one of the module's transactions, recorded for
	// the harness to fail on — all but LockVariants, which runs on the
	// transaction by design (MB rule 21).
	orders.SetContractCallHook(lockedContractCalls.note)
	orders.SetPDFModelHook(pdfModels.add)
	os.Exit(contracttest.RequireCoverage(m, recorder, pending...))
}
