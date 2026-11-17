package purchasing

import (
	"context"
	"fmt"

	"github.com/vantigo-io/vantigo/server/internal/worker"
)

// InOpenTx exposes inOpenTx to the external tests, whose contract-call hook
// tells a call made inside one of this module's transactions from one
// outside any.
var InOpenTx = inOpenTx

// The names the hook hears the two in-transaction calls by.
const (
	LockVariantsCall  = lockVariantsCall
	LockSuppliersCall = lockSuppliersCall
)

// SetContractCallHook installs the hook every call out of this module is
// reported to, once, from TestMain.
func SetContractCallHook(hook func(ctx context.Context, method string)) { contractCallHook = hook }

// PDFModel is the words of one PDF, as the tests read them.
type PDFModel struct {
	Watermark, Title, Subtitle string
	Seller, Supplier           []string
	Meta                       [][2]string
	Header                     []string
	Rows                       [][]string
	Totals                     [][2]string
	Notes                      []string
}

// SetPDFModelHook installs fn to hear every PDF model built, once, from
// TestMain.
func SetPDFModelHook(fn func(PDFModel)) {
	pdfModelBuilt = func(m pdfModel) {
		fn(PDFModel{Watermark: m.watermark, Title: m.title, Subtitle: m.subtitle, Seller: m.seller, Supplier: m.supplier,
			Meta: m.meta, Header: m.header, Rows: m.rows, Totals: m.totals, Notes: m.notes})
	}
}

// RunCycle runs one cycle of one of this module's workers, as module.Workers
// built it, and answers how many rows it handled.
func RunCycle(ctx context.Context, w worker.Worker) (int, error) {
	switch w := w.(type) {
	case *mailWorker:
		return w.cycle(ctx), nil
	case *deletionsWorker:
		return w.cycle(ctx), nil
	}
	return 0, fmt.Errorf("purchasing: %s is not one of this module's workers", w.Name())
}
