package purchasing

// The badge on Accounting's supplier invoices (purchasing design P6, §11):
// contracts.SupplierInvoiceMatches, the orders each invoice is matched to
// and how many differences the matches have. It reads on the pool and is
// provider-gated: a caller without purchasing:access is answered no
// matches, never a refusal.

import (
	"context"
	"fmt"
	"strconv"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/purchasing/gen"
	"github.com/vantigo-io/vantigo/server/internal/purchasing/store"
)

// invoiceMatches is the module's contracts.SupplierInvoiceMatches.
type invoiceMatches struct{ deps module.Deps }

var _ contracts.SupplierInvoiceMatches = invoiceMatches{}

func newSupplierInvoiceMatches(d module.Deps) contracts.SupplierInvoiceMatches {
	return invoiceMatches{deps: d}
}

// Summary is, per invoice asked that is matched to anything, its orders and
// the count of its matches' warnings.
func (p invoiceMatches) Summary(ctx context.Context, invoiceIDs []int64) ([]contracts.SupplierInvoiceMatch, error) {
	if len(invoiceIDs) > contracts.MaxSuppliers {
		return nil, fmt.Errorf("purchasing: %d supplier invoices asked at once, at most %d", len(invoiceIDs), contracts.MaxSuppliers)
	}
	if len(invoiceIDs) == 0 || p.deps.Pool == nil || !contracts.HasPermission(ctx, p.deps.Access, permAccess) {
		return nil, nil
	}
	q := store.New(p.deps.Pool)
	matches, err := q.InvoiceMatchesOf(ctx, invoiceIDs)
	if err != nil {
		return nil, fmt.Errorf("purchasing: read the invoices' matches: %w", err)
	}
	if len(matches) == 0 {
		return nil, nil
	}
	mc, err := readMatchContext(ctx, q, matches)
	if err != nil {
		return nil, err
	}
	var out []contracts.SupplierInvoiceMatch
	at := map[int64]int{}
	warned := map[int64][]gen.PurchasingInvoiceMatch{}
	for _, m := range matches {
		i, ok := at[m.SupplierInvoiceID]
		if !ok {
			i = len(out)
			at[m.SupplierInvoiceID] = i
			out = append(out, contracts.SupplierInvoiceMatch{InvoiceID: m.SupplierInvoiceID})
		}
		o := mc.orders[m.OrderID]
		seen := false
		for _, mo := range out[i].Orders {
			seen = seen || mo.ID == o.ID
		}
		if !seen {
			out[i].Orders = append(out[i].Orders, contracts.MatchedOrder{ID: o.ID, Number: strconv.FormatInt(deref64(o.Number), 10)})
		}
		warned[m.SupplierInvoiceID] = append(warned[m.SupplierInvoiceID], gen.PurchasingInvoiceMatch{OrderLineId: m.OrderLineID,
			Warnings: matchWarnings(m, o, mc.lines[m.OrderLineID], mc.tolerance)})
	}
	for i := range out {
		out[i].Differences = int(differences(warned[out[i].InvoiceID]))
	}
	return out, nil
}
