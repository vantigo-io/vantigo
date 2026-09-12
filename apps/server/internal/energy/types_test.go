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

// consumptionJSON decodes ConsumptionResponse.
type consumptionJSON struct {
	Id              int64     `json:"id"`
	MeteringPointId int32     `json:"meteringPointId"`
	Start           time.Time `json:"start"`
	End             time.Time `json:"end"`
	QuantityKwh     float64   `json:"quantityKwh"`
	Quality         string    `json:"quality"`
	Source          string    `json:"source"`
	ReceivedAt      time.Time `json:"receivedAt"`
}

// consumptionAggregateJSON decodes ConsumptionAggregateResponse.
type consumptionAggregateJSON struct {
	BucketStart   time.Time `json:"bucketStart"`
	BucketEnd     time.Time `json:"bucketEnd"`
	QuantityKwh   float64   `json:"quantityKwh"`
	IntervalCount int64     `json:"intervalCount"`
	HasEstimated  bool      `json:"hasEstimated"`
}

// customerConsumptionAggregateJSON decodes CustomerConsumptionAggregateResponse.
type customerConsumptionAggregateJSON struct {
	MeteringPointId int32     `json:"meteringPointId"`
	BucketStart     time.Time `json:"bucketStart"`
	BucketEnd       time.Time `json:"bucketEnd"`
	QuantityKwh     float64   `json:"quantityKwh"`
	IntervalCount   int64     `json:"intervalCount"`
	HasEstimated    bool      `json:"hasEstimated"`
}

// customerMeteringPointJSON decodes CustomerMeteringPointResponse.
type customerMeteringPointJSON struct {
	MeteringPoint meteringPointJSON  `json:"meteringPoint"`
	SupplyPeriods []supplyPeriodJSON `json:"supplyPeriods"`
}

// energyStatsSummaryJSON decodes EnergyStatsSummaryResponse.
type energyStatsSummaryJSON struct {
	From                     time.Time `json:"from"`
	To                       time.Time `json:"to"`
	MeteringPointCount       int32     `json:"meteringPointCount"`
	MeteringPointCountDelta  int32     `json:"meteringPointCountDelta"`
	ActiveSupplyPeriods      int32     `json:"activeSupplyPeriods"`
	ActiveSupplyPeriodsDelta int32     `json:"activeSupplyPeriodsDelta"`
	ConsumptionKwh           float64   `json:"consumptionKwh"`
	ConsumptionKwhDelta      float64   `json:"consumptionKwhDelta"`
	PreviousConsumptionKwh   float64   `json:"previousConsumptionKwh"`
}

// energyStatsDailyBucketJSON decodes EnergyStatsDailyBucket.
type energyStatsDailyBucketJSON struct {
	Date  string  `json:"date"`
	Value float64 `json:"value"`
}

// energyStatsAttentionItemJSON decodes EnergyStatsAttentionItem.
type energyStatsAttentionItemJSON struct {
	Id         string    `json:"id"`
	Type       string    `json:"type"`
	Title      string    `json:"title"`
	OccurredAt time.Time `json:"occurredAt"`
	EntityId   string    `json:"entityId"`
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
