package orders_test

import (
	"crypto/sha256"
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

// pdfModels is every PDF model rendered, by the bytes it rendered to, for the
// tests that read what a PDF says (the bytes are embedded-subset and cannot be
// read back).
var pdfModels = &models{byPDF: map[[sha256.Size]byte]orders.PDFModel{}}

type models struct {
	mu    sync.Mutex
	byPDF map[[sha256.Size]byte]orders.PDFModel
}

func (m *models) add(p orders.PDFModel, pdf []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.byPDF[sha256.Sum256(pdf)] = p
}

// find is the model rendered to pdf — the body a test was served — when its
// title is title, its subtitle is subtitle and every want accepts it. Tests
// run in parallel, each on its own database, so two tests print "Leveranse 1 –
// Ordre 1" alike: keyed by the bytes, a test reads its own PDF's model and
// never another test's. Two PDFs alike to the byte are the same words.
func (m *models) find(pdf []byte, title, subtitle string, want ...func(orders.PDFModel) bool) (orders.PDFModel, bool) {
	m.mu.Lock()
	p, ok := m.byPDF[sha256.Sum256(pdf)]
	m.mu.Unlock()
	if !ok || p.Title != title || p.Subtitle != subtitle {
		return orders.PDFModel{}, false
	}
	for _, accept := range want {
		if !accept(p) {
			return orders.PDFModel{}, false
		}
	}
	return p, true
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
