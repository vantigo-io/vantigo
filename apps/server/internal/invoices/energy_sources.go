package invoices

import (
	"context"
	"fmt"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// The two reads of this module's own rows an energy line's building needs
// besides the settlements (energy consumption billing design D7, D8): the
// codes the settings give each hint, and the invoice each correction's
// original was invoiced by. The wizard and a save's refresh share them.

// energyDefaults is the VAT code each hint takes by default (D8): the
// settings' two energy codes, or id 9 (category O) for both while the seller
// is not VAT registered, as the work codes are.
func energyDefaults(settings store.InvoicesSetting) energyVatCodes {
	if !settings.VatRegistered {
		return energyVatCodes{standard: vatCodeNotRegistered, exempt: vatCodeNotRegistered}
	}
	return energyVatCodes{standard: settings.EnergyVatCode, exempt: settings.EnergyVatCodeExempt}
}

// correctedNumbers is, for every correction among settlements, the number of
// the issued invoice its original settlement is invoiced on, read from this
// module's own rows on q; an original no invoiced row names is absent.
func correctedNumbers(ctx context.Context, q *store.Queries, settlements []contracts.Settlement) (map[int64]int64, error) {
	var p store.LiveSourcesForParams
	for _, st := range settlements {
		if st.CorrectsID != nil {
			p.Kinds, p.Ids = append(p.Kinds, string(contracts.WorkSourceSettlement)), append(p.Ids, *st.CorrectsID)
		}
	}
	out := map[int64]int64{}
	if len(p.Ids) == 0 {
		return out, nil
	}
	rows, err := q.LiveSourcesFor(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the invoices the corrected settlements are on: %w", err)
	}
	for _, r := range rows {
		if r.State == sourceInvoiced && r.Number != nil {
			out[r.SourceID] = *r.Number
		}
	}
	return out, nil
}
