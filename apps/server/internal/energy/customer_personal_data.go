package energy

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/energy/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// customerPersonalData is this module's contracts.CustomerPersonalData
// (customers GDPR design D2): a private person's supply periods, handed over
// with the address they were supplied at, and kept when the person is
// anonymised.
type customerPersonalData struct {
	pool *pgxpool.Pool
}

var _ contracts.CustomerPersonalData = customerPersonalData{}

// newCustomerPersonalData is Module's CustomerPersonalData.
func newCustomerPersonalData(d module.Deps) contracts.CustomerPersonalData {
	return customerPersonalData{pool: d.Pool}
}

type supplyPeriodsSection struct {
	SupplyPeriods []exportedSupplyPeriod `json:"supplyPeriods"`
}

type exportedSupplyPeriod struct {
	ID            int32                 `json:"id"`
	Start         time.Time             `json:"start"`
	End           *time.Time            `json:"end,omitempty"`
	Status        string                `json:"status"`
	MeteringPoint exportedMeteringPoint `json:"meteringPoint"`
}

type exportedMeteringPoint struct {
	Gsrn          string `json:"gsrn"`
	StreetAddress string `json:"streetAddress"`
	PostalCode    string `json:"postalCode"`
	City          string `json:"city"`
	CountryCode   string `json:"countryCode"`
}

// ExportCustomerData answers nil for a customer never supplied.
func (p customerPersonalData) ExportCustomerData(ctx context.Context, customerID int32) (any, error) {
	rows, err := store.New(p.pool).CustomerSupplyPeriodsForExport(ctx, customerID)
	if err != nil {
		return nil, fmt.Errorf("energy: read customer %d's supply periods: %w", customerID, err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	section := supplyPeriodsSection{SupplyPeriods: make([]exportedSupplyPeriod, 0, len(rows))}
	for _, r := range rows {
		section.SupplyPeriods = append(section.SupplyPeriods, exportedSupplyPeriod{
			ID: r.ID, Start: r.Start, End: r.End, Status: r.Status,
			MeteringPoint: exportedMeteringPoint{
				Gsrn: r.Gsrn, StreetAddress: r.StreetAddress, PostalCode: r.PostalCode, City: r.City, CountryCode: r.CountryCode,
			},
		})
	}
	return section, nil
}

// EraseCustomerData keeps everything and says so (design D2): a supply period
// is the metering point's history, the address is the point's rather than the
// person's, and the row keeps pointing at the anonymised customer, whose name
// the directory answers as "Anonymised person" from then on. Nothing here
// names the person, so there is nothing to blank.
func (customerPersonalData) EraseCustomerData(context.Context, pgx.Tx, int32) ([]contracts.ErasedData, error) {
	return []contracts.ErasedData{{Kind: customerReferenceKindSupplyPeriods, Count: 0}}, nil
}
