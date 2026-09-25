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
// with the address they were supplied at and the metering point's consumption
// while they were, and kept when the person is anonymised.
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
	// Consumption is the point's metered kWh inside the period, one element a
	// month, oldest first; empty when nothing was metered, and always for a
	// cancelled period, which supplied nobody.
	Consumption []exportedMonthlyConsumption `json:"consumption"`
}

type exportedMonthlyConsumption struct {
	Month string  `json:"month"`
	Kwh   float64 `json:"kwh"`
}

type exportedMeteringPoint struct {
	Gsrn          string `json:"gsrn"`
	StreetAddress string `json:"streetAddress"`
	PostalCode    string `json:"postalCode"`
	City          string `json:"city"`
	CountryCode   string `json:"countryCode"`
}

// ExportCustomerData answers nil for a customer never supplied. Each period's
// consumption is its own query — an export is rare and deliberate, and a
// person has few periods.
func (p customerPersonalData) ExportCustomerData(ctx context.Context, customerID int32) (any, error) {
	q := store.New(p.pool)
	rows, err := q.CustomerSupplyPeriodsForExport(ctx, customerID)
	if err != nil {
		return nil, fmt.Errorf("energy: read customer %d's supply periods: %w", customerID, err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	section := supplyPeriodsSection{SupplyPeriods: make([]exportedSupplyPeriod, 0, len(rows))}
	for _, r := range rows {
		months, err := q.SupplyPeriodConsumptionByMonth(ctx, store.SupplyPeriodConsumptionByMonthParams{
			TimeZone: marketTimeZone(&r.PriceArea), SupplyPeriodID: r.ID,
		})
		if err != nil {
			return nil, fmt.Errorf("energy: read supply period %d's consumption: %w", r.ID, err)
		}
		consumption := make([]exportedMonthlyConsumption, 0, len(months))
		for _, m := range months {
			consumption = append(consumption, exportedMonthlyConsumption{Month: m.Month, Kwh: floatFromNumeric(m.QuantityKwh)})
		}
		section.SupplyPeriods = append(section.SupplyPeriods, exportedSupplyPeriod{
			ID: r.ID, Start: r.Start, End: r.End, Status: r.Status,
			MeteringPoint: exportedMeteringPoint{
				Gsrn: r.Gsrn, StreetAddress: r.StreetAddress, PostalCode: r.PostalCode, City: r.City, CountryCode: r.CountryCode,
			},
			Consumption: consumption,
		})
	}
	return section, nil
}

// EraseCustomerData keeps everything and says so (design D2): a supply period
// is the metering point's history, the address is the point's rather than the
// person's, the consumption is the point's series and needed for settlement,
// and the row keeps pointing at the anonymised customer, whose name the
// directory answers as "Anonymised person" from then on. Nothing here names
// the person, so there is nothing to blank — the link stays, which makes this
// pseudonymisation of it rather than its removal (docs/customers.md).
func (customerPersonalData) EraseCustomerData(context.Context, pgx.Tx, int32) ([]contracts.ErasedData, error) {
	return []contracts.ErasedData{{Kind: customerReferenceKindSupplyPeriods, Count: 0}}, nil
}
