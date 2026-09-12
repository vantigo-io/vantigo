package energy_test

import "time"

// meteringPointJSON decodes MeteringPointResponse
// (Endpoints/MeteringPoints/Dtos/MeteringPointResponse.cs).
type meteringPointJSON struct {
	Id                           int32       `json:"id"`
	Gsrn                         string      `json:"gsrn"`
	MeterNumber                  *string     `json:"meterNumber"`
	Address                      addressJSON `json:"address"`
	PriceArea                    string      `json:"priceArea"`
	GridArea                     *string     `json:"gridArea"`
	ExpectedAnnualConsumptionKwh *float64    `json:"expectedAnnualConsumptionKwh"`
	Latitude                     *float64    `json:"latitude"`
	Longitude                    *float64    `json:"longitude"`
	ConnectionStatus             string      `json:"connectionStatus"`
	CreatedAt                    time.Time   `json:"createdAt"`
	UpdatedAt                    time.Time   `json:"updatedAt"`
}

type addressJSON struct {
	StreetAddress string `json:"streetAddress"`
	PostalCode    string `json:"postalCode"`
	City          string `json:"city"`
	CountryCode   string `json:"countryCode"`
}

type meteringPointListJSON struct {
	Data       []meteringPointJSON `json:"data"`
	Pagination struct {
		Page       int32 `json:"page"`
		PageSize   int32 `json:"pageSize"`
		TotalCount int32 `json:"totalCount"`
		TotalPages int32 `json:"totalPages"`
	} `json:"pagination"`
}

// meterJSON decodes MeterResponse.
type meterJSON struct {
	Id              int32      `json:"id"`
	MeteringPointId int32      `json:"meteringPointId"`
	MeterNumber     string     `json:"meterNumber"`
	InstalledAt     time.Time  `json:"installedAt"`
	RemovedAt       *time.Time `json:"removedAt"`
}

// supplyPeriodJSON decodes SupplyPeriodResponse.
type supplyPeriodJSON struct {
	Id              int32      `json:"id"`
	MeteringPointId int32      `json:"meteringPointId"`
	CustomerId      int32      `json:"customerId"`
	Start           time.Time  `json:"start"`
	End             *time.Time `json:"end"`
	Status          string     `json:"status"`
}

// switchSupplyPeriodJSON decodes SwitchSupplyPeriodResponse.
type switchSupplyPeriodJSON struct {
	EndedPeriod *supplyPeriodJSON `json:"endedPeriod"`
	NewPeriod   supplyPeriodJSON  `json:"newPeriod"`
}

type problemJSON struct {
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

type validationProblemJSON struct {
	Title  string              `json:"title"`
	Errors map[string][]string `json:"errors"`
}

// newMeteringPointBody is EnergyEndpointsTests.NewMeteringPoint, a valid
// create-metering-point body.
func newMeteringPointBody(gsrn, meterNumber string) map[string]any {
	return map[string]any{
		"gsrn":        gsrn,
		"meterNumber": meterNumber,
		"address": map[string]any{
			"streetAddress": "Testgata 1", "postalCode": "0001", "city": "Oslo", "countryCode": "NO",
		},
		"priceArea":        "NO1",
		"connectionStatus": "Connected",
	}
}
