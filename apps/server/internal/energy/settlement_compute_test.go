package energy

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/energy/store"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// settlementFixture is one test database with the helpers that write a
// metering point, its supply period, its intervals, the area's prices and
// tariffs as plain rows.
type settlementFixture struct {
	t    *testing.T
	ctx  context.Context
	pool *pgxpool.Pool
	q    *store.Queries
	oslo *time.Location
	gsrn int
}

func newSettlementFixture(t *testing.T) *settlementFixture {
	t.Helper()
	pool, _ := testdb.Migrated(t)
	oslo, err := time.LoadLocation("Europe/Oslo")
	if err != nil {
		t.Fatal(err)
	}
	f := &settlementFixture{t: t, ctx: context.Background(), pool: pool, q: store.New(pool), oslo: oslo}
	for m := 0; m < 4; m++ {
		f.exec(`SELECT energy.ensure_consumption_partition($1::date)`, time.Date(2026, time.Month(1+m), 1, 0, 0, 0, 0, time.UTC))
	}
	return f
}

func (f *settlementFixture) exec(sql string, args ...any) {
	f.t.Helper()
	if _, err := f.pool.Exec(f.ctx, sql, args...); err != nil {
		f.t.Fatalf("%s: %v", sql, err)
	}
}

// at is a wall-clock instant in Oslo.
func (f *settlementFixture) at(m time.Month, d, h, min int) time.Time {
	return time.Date(2026, m, d, h, min, 0, 0, f.oslo)
}

// month is the billing period of January 2026 (or m) in Oslo.
func (f *settlementFixture) month(m time.Month) billingPeriod {
	return billingPeriod{Start: f.at(m, 1, 0, 0), End: f.at(m+1, 1, 0, 0)}
}

// testComponent is a tariff component's row; nil fields are NULL.
type testComponent struct {
	kind, name                                 string
	rate, rateNight, rateReduced, rateBusiness *string
	dayFrom, dayTo                             *int16
	weekdaysOnly                               bool
	tiers                                      *string
}

type testVersion struct {
	from string
	to   *string
	comp []testComponent
}

func strp(v string) *string { return &v }
func hourp(v int16) *int16  { return &v }

func (f *settlementFixture) tariff(kind, name string, versions ...testVersion) int32 {
	f.t.Helper()
	var id int32
	if err := f.pool.QueryRow(f.ctx, `INSERT INTO energy.tariffs (kind, name, status, created_at, updated_at)
		VALUES ($1, $2, 'active', now(), now()) RETURNING id`, kind, name).Scan(&id); err != nil {
		f.t.Fatal(err)
	}
	for _, v := range versions {
		var vid int32
		if err := f.pool.QueryRow(f.ctx, `INSERT INTO energy.tariff_versions (tariff_id, valid_from, valid_to)
			VALUES ($1, $2::date, $3::date) RETURNING id`, id, v.from, v.to).Scan(&vid); err != nil {
			f.t.Fatal(err)
		}
		for i, c := range v.comp {
			f.exec(`INSERT INTO energy.tariff_components (version_id, position, kind, name, rate, rate_night, day_from, day_to,
				weekdays_only, tiers, rate_reduced, rate_business)
				VALUES ($1, $2, $3, $4, $5::numeric, $6::numeric, $7, $8, $9, $10::jsonb, $11::numeric, $12::numeric)`,
				vid, i, c.kind, c.name, c.rate, c.rateNight, c.dayFrom, c.dayTo, c.weekdaysOnly, c.tiers, c.rateReduced, c.rateBusiness)
		}
	}
	return id
}

// pointOpts is a metering point's billing facts.
type pointOpts struct {
	household  bool
	postal     string
	taxClass   string
	gridTariff *int32
}

// supply writes a metering point and one supply period from start on the
// power tariff, and answers the period's id.
func (f *settlementFixture) supply(o pointOpts, start time.Time, tariff *int32) (pointID, supplyID int32) {
	f.t.Helper()
	f.gsrn++
	if o.postal == "" {
		o.postal = "0150"
	}
	if o.taxClass == "" {
		o.taxClass = "ordinary"
	}
	if err := f.pool.QueryRow(f.ctx, `INSERT INTO energy.metering_points (gsrn, street_address, postal_code, city, country_code,
		price_area, connection_status, created_at, updated_at, household, electricity_tax_class, grid_tariff_id)
		VALUES ($1, 'Storgata 1', $2, 'Oslo', 'NO', 'NO1', 'Connected', now(), now(), $3, $4, $5) RETURNING id`,
		fmt.Sprintf("7070575000%08d", f.gsrn), o.postal, o.household, o.taxClass, o.gridTariff).Scan(&pointID); err != nil {
		f.t.Fatal(err)
	}
	if err := f.pool.QueryRow(f.ctx, `INSERT INTO energy.supply_periods (metering_point_id, customer_id, start, status, tariff_id, cadence)
		VALUES ($1, 501, $2, 'Active', $3, 'monthly') RETURNING id`, pointID, start, tariff).Scan(&supplyID); err != nil {
		f.t.Fatal(err)
	}
	return pointID, supplyID
}

// intervals writes the point's intervals of step over [from, to), each kwh.
func (f *settlementFixture) intervals(point int32, from, to time.Time, step time.Duration, kwh string) {
	f.t.Helper()
	f.exec(`INSERT INTO energy.consumption_intervals (metering_point_id, start, "end", quantity_kwh, quality, source, received_at)
		SELECT $1, g, g + $3::interval, $4::numeric, 'Measured', 'Import', now()
		FROM generate_series($2::timestamptz, $5::timestamptz - $3::interval, $3::interval) g`,
		point, from, fmt.Sprintf("%d seconds", int(step.Seconds())), kwh, to)
}

// setKWh sets the kWh of the point's intervals starting in [from, to).
func (f *settlementFixture) setKWh(point int32, from, to time.Time, kwh string) {
	f.t.Helper()
	f.exec(`UPDATE energy.consumption_intervals SET quantity_kwh = $4::numeric
		WHERE metering_point_id = $1 AND start >= $2 AND start < $3`, point, from, to, kwh)
}

// prices writes NO1's spot prices of step over [from, to); price answers the
// i-th price.
func (f *settlementFixture) prices(from, to time.Time, step time.Duration, price func(i int) string, state string) {
	f.t.Helper()
	for i, t := 0, from; t.Before(to); i, t = i+1, t.Add(step) {
		f.exec(`INSERT INTO energy.spot_prices (price_area, start, "end", nok_per_kwh, source, received_at, state)
			VALUES ('NO1', $1, $2, $3::numeric, 'Import', now(), $4)`, t, t.Add(step), price(i), state)
	}
}

func (f *settlementFixture) subject(supplyID int32) settlementSubject {
	f.t.Helper()
	subjects, err := loadSettlementSubjects(f.ctx, f.q, []int32{supplyID})
	if err != nil {
		f.t.Fatal(err)
	}
	return subjects[supplyID]
}

// compute settles period for supplyID a year after it.
func (f *settlementFixture) compute(supplyID int32, period billingPeriod) settlement {
	f.t.Helper()
	got, err := computeSettlement(f.ctx, f.q, f.subject(supplyID), period, period.End.AddDate(1, 0, 0))
	if err != nil {
		f.t.Fatal(err)
	}
	return got
}

// summary is a settlement's figures as text: kWh, amount, counts,
// estimated, blocker and detail.
func summary(st settlement) string {
	c := st.toContract()
	return fmt.Sprintf("kwh=%s amount=%s count=%d/%d estimated=%t blocked=%s %q",
		c.KWh, c.Amount, st.IntervalCount, st.ExpectedIntervalCount, c.Estimated, c.Blocked, c.BlockedDetail)
}

// lineTexts is the lines as text: kind|text|band|month|qty unit × price = amount|vat.
func lineTexts(st settlement) []string {
	var out []string
	for _, l := range st.toContract().Lines {
		month := ""
		if l.Month != nil {
			month = l.Month.Format(time.DateOnly)
		}
		out = append(out, fmt.Sprintf("%s|%s|%s|%s|%s %s × %s = %s|%s",
			l.Kind, l.Text, l.Band, month, l.Quantity, l.Unit, l.UnitPrice, l.Amount, l.VATHint))
	}
	return out
}

func assertSettlement(t *testing.T, got settlement, wantSummary string, wantLines []string) {
	t.Helper()
	if sum := summary(got); sum != wantSummary {
		t.Errorf("summary\n got %s\nwant %s", sum, wantSummary)
	}
	if g, w := strings.Join(lineTexts(got), "\n"), strings.Join(wantLines, "\n"); g != w {
		t.Errorf("lines\n got:\n%s\nwant:\n%s", g, w)
	}
}

// TestComputeSettlement pins the computation (energy settlements plan
// readings 3–6) on the test database: each pricing rule and each blocker,
// with the figures as the contract carries them.
func TestComputeSettlement(t *testing.T) {
	t.Parallel()
	f := newSettlementFixture(t)
	jan := f.month(time.January)

	fixed := f.tariff("power", "Fastpris", testVersion{from: "2025-01-01", comp: []testComponent{
		{kind: componentFixedEnergy, name: "Fastpris", rate: strp("1")},
		{kind: componentMonthlyFee, name: "Fastbeløp", rate: strp("39")},
	}})
	changed := f.tariff("power", "Endret", testVersion{from: "2025-01-01", to: strp("2026-01-16"), comp: []testComponent{
		{kind: componentFixedEnergy, name: "Fastpris", rate: strp("1")},
		{kind: componentMonthlyFee, name: "Fastbeløp", rate: strp("30")},
	}}, testVersion{from: "2026-01-16", comp: []testComponent{
		{kind: componentFixedEnergy, name: "Fastpris", rate: strp("2")},
		{kind: componentMonthlyFee, name: "Fastbeløp", rate: strp("30")},
	}})
	late := f.tariff("power", "Sen", testVersion{from: "2026-01-10", comp: []testComponent{
		{kind: componentFixedEnergy, name: "Fastpris", rate: strp("1")},
	}})
	grid := f.tariff("grid", "Nettleie", testVersion{from: "2025-01-01", comp: []testComponent{
		{kind: componentGridEnergy, name: "Energiledd", rate: strp("0.40"), rateNight: strp("0.20"), dayFrom: hourp(6), dayTo: hourp(22), weekdaysOnly: true},
		{kind: componentGridCapacity, name: "Kapasitetsledd",
			tiers: strp(`[{"fromKw":0,"perMonth":100},{"fromKw":2,"perMonth":200},{"fromKw":5,"perMonth":300},{"fromKw":6,"perMonth":400},{"fromKw":10,"perMonth":500}]`)},
		{kind: componentGridFixed, name: "Fastledd", rate: strp("100")},
		{kind: componentElectricityTax, name: "Elavgift", rate: strp("0.0713"), rateReduced: strp("0.0055")},
		{kind: componentEnovaLevy, name: "Enovaavgift", rate: strp("0.01"), rateBusiness: strp("800")},
	}})
	taxes := f.tariff("grid", "Avgifter", testVersion{from: "2025-01-01", comp: []testComponent{
		{kind: componentElectricityTax, name: "Elavgift", rate: strp("0.0713"), rateReduced: strp("0.0055")},
		{kind: componentEnovaLevy, name: "Enovaavgift", rate: strp("0.01"), rateBusiness: strp("800")},
	}})
	shortGrid := f.tariff("grid", "Kort nett", testVersion{from: "2026-01-01", to: strp("2026-01-31"), comp: []testComponent{
		{kind: componentGridFixed, name: "Fastledd", rate: strp("100")},
	}})

	// hourly is a point on tariff with 1 kWh every hour of January.
	hourly := func(o pointOpts, tariff int32) int32 {
		point, supply := f.supply(o, jan.Start, &tariff)
		f.intervals(point, jan.Start, jan.End, time.Hour, "1")
		return supply
	}

	t.Run("a spot month with hourly prices", func(t *testing.T) {
		// Prices for this area are written here only: the other spot cases
		// below run in their own fixtures.
		g := newSettlementFixture(t)
		tid := g.tariff("power", "Spotpris", testVersion{from: "2025-01-01", comp: []testComponent{
			{kind: componentSpotEnergy, name: "Strøm"},
			{kind: componentMarkup, name: "Påslag", rate: strp("0.02")},
			{kind: componentMonthlyFee, name: "Fastbeløp", rate: strp("39")},
		}})
		point, supply := g.supply(pointOpts{}, jan.Start, &tid)
		g.intervals(point, jan.Start, jan.End, time.Hour, "1")
		g.prices(jan.Start, jan.End, time.Hour, func(i int) string {
			if i%2 == 0 {
				return "0.5"
			}
			return "0.123457"
		}, "final")
		// Σ = 372 × 0.5 + 372 × 0.123457 = 231.926004; ÷ 744 = 0.3117285.
		assertSettlement(t, g.compute(supply, jan),
			`kwh=744.000 amount=285.81 count=744/744 estimated=false blocked= ""`, []string{
				"spot_energy|Strøm|||744.000 kWh × 0.311729 = 231.93|standard",
				"markup|Påslag|||744.000 kWh × 0.020000 = 14.88|standard",
				"monthly_fee|Fastbeløp|||1.000 month × 39.000000 = 39.00|standard",
			})
	})

	t.Run("quarter-hourly prices against hourly consumption are time-weighted", func(t *testing.T) {
		g := newSettlementFixture(t)
		tid := g.tariff("power", "Spotpris", testVersion{from: "2025-01-01", comp: []testComponent{
			{kind: componentSpotEnergy, name: "Strøm"},
		}})
		point, supply := g.supply(pointOpts{}, jan.Start, &tid)
		g.intervals(point, jan.Start, jan.End, time.Hour, "1")
		quarters := []string{"0.4", "0.5", "0.6", "0.7"}
		g.prices(jan.Start, jan.End, 15*time.Minute, func(i int) string { return quarters[i%4] }, "final")
		// Each hour takes its quarters' mean, 0.55, not its first quarter's 0.4.
		assertSettlement(t, g.compute(supply, jan),
			`kwh=744.000 amount=409.20 count=744/744 estimated=false blocked= ""`, []string{
				"spot_energy|Strøm|||744.000 kWh × 0.550000 = 409.20|standard",
			})
	})

	t.Run("a fixed month, an estimated interval", func(t *testing.T) {
		supply := hourly(pointOpts{}, fixed)
		f.exec(`UPDATE energy.consumption_intervals SET quality = 'Estimated', quantity_kwh = 2.5
			WHERE metering_point_id = (SELECT metering_point_id FROM energy.supply_periods WHERE id = $1) AND start = $2`,
			supply, f.at(time.January, 10, 3, 0))
		assertSettlement(t, f.compute(supply, jan),
			`kwh=745.500 amount=784.50 count=744/744 estimated=true blocked= ""`, []string{
				"fixed_energy|Fastpris|||745.500 kWh × 1.000000 = 745.50|standard",
				"monthly_fee|Fastbeløp|||1.000 month × 39.000000 = 39.00|standard",
			})
	})

	t.Run("a version change mid-month gives each component two lines", func(t *testing.T) {
		supply := hourly(pointOpts{}, changed)
		// 1–15 January (360 h, 15/31 = 0.484 month) at the first version,
		// 16–31 (384 h, 16/31 = 0.516) at the second.
		assertSettlement(t, f.compute(supply, jan),
			`kwh=744.000 amount=1158.00 count=744/744 estimated=false blocked= ""`, []string{
				"fixed_energy|Fastpris|||360.000 kWh × 1.000000 = 360.00|standard",
				"fixed_energy|Fastpris|||384.000 kWh × 2.000000 = 768.00|standard",
				"monthly_fee|Fastbeløp|||0.484 month × 30.000000 = 14.52|standard",
				"monthly_fee|Fastbeløp|||0.516 month × 30.000000 = 15.48|standard",
			})
	})

	t.Run("a partial first month prorates by counted days", func(t *testing.T) {
		start := f.at(time.January, 10, 14, 0)
		point, supply := f.supply(pointOpts{}, start, &fixed)
		f.intervals(point, start, jan.End, time.Hour, "1")
		// 10 hours on the 10th (not counted: its midnight is before the
		// start) and 21 whole days: 514 h, 21/31 = 0.677 month.
		assertSettlement(t, f.compute(supply, billingPeriod{Start: start, End: jan.End}),
			`kwh=514.000 amount=540.40 count=514/514 estimated=false blocked= ""`, []string{
				"fixed_energy|Fastpris|||514.000 kWh × 1.000000 = 514.00|standard",
				"monthly_fee|Fastbeløp|||0.677 month × 39.000000 = 26.40|standard",
			})
	})

	t.Run("a quarter across the spring DST change", func(t *testing.T) {
		q1 := billingPeriod{Start: f.at(time.January, 1, 0, 0), End: f.at(time.April, 1, 0, 0)}
		point, supply := f.supply(pointOpts{}, q1.Start, &fixed)
		f.intervals(point, q1.Start, q1.End, time.Hour, "0.5")
		// 2159 hours: 29 March has 23.
		assertSettlement(t, f.compute(supply, q1),
			`kwh=1079.500 amount=1196.50 count=2159/2159 estimated=false blocked= ""`, []string{
				"fixed_energy|Fastpris|||1079.500 kWh × 1.000000 = 1079.50|standard",
				"monthly_fee|Fastbeløp|||3.000 month × 39.000000 = 117.00|standard",
			})
	})

	t.Run("a grid tariff: day and night, the capacity tier from PT15M summed to the hour", func(t *testing.T) {
		point, supply := f.supply(pointOpts{gridTariff: &grid}, jan.Start, &fixed)
		f.intervals(point, jan.Start, jan.End, 15*time.Minute, "0.25")
		// 5 January (Monday) 10:00: 1+1+1.5+1.5 = 5 kWh, no quarter above 1.5.
		f.setKWh(point, f.at(time.January, 5, 10, 0), f.at(time.January, 5, 10, 30), "1")
		f.setKWh(point, f.at(time.January, 5, 10, 30), f.at(time.January, 5, 11, 0), "1.5")
		// 12 January (Monday) 18:00: 4 kWh.
		f.setKWh(point, f.at(time.January, 12, 18, 0), f.at(time.January, 12, 19, 0), "1")
		// 20 January (Tuesday) 08:00 6 kWh and 09:00 8 kWh: one day, one maximum.
		f.setKWh(point, f.at(time.January, 20, 8, 0), f.at(time.January, 20, 9, 0), "1.5")
		f.setKWh(point, f.at(time.January, 20, 9, 0), f.at(time.January, 20, 10, 0), "2")
		// 25 January (Sunday) 12:00: 2 kWh, at night by weekdays_only.
		f.setKWh(point, f.at(time.January, 25, 12, 0), f.at(time.January, 25, 13, 0), "0.5")
		// 764 kWh: day 22 weekdays × 16 h + 4 + 3 + 5 + 7 = 371, night 393.
		// Capacity: the days' maxima 8, 5, 4 → 5.667 kW → the 5 kW tier.
		// Enova for a business: 800 × 31/365 = 0.085 year.
		assertSettlement(t, f.compute(supply, jan),
			`kwh=764.000 amount=1552.47 count=2976/2976 estimated=false blocked= ""`, []string{
				"fixed_energy|Fastpris|||764.000 kWh × 1.000000 = 764.00|standard",
				"monthly_fee|Fastbeløp|||1.000 month × 39.000000 = 39.00|standard",
				"grid_energy|Energiledd|day||371.000 kWh × 0.400000 = 148.40|standard",
				"grid_energy|Energiledd|night||393.000 kWh × 0.200000 = 78.60|standard",
				"grid_capacity|Kapasitetsledd||2026-01-01|1.000 month × 300.000000 = 300.00|standard",
				"grid_fixed|Fastledd|||1.000 month × 100.000000 = 100.00|standard",
				"electricity_tax|Elavgift|||764.000 kWh × 0.071300 = 54.47|standard",
				"enova_levy|Enovaavgift|||0.085 year × 800.000000 = 68.00|standard",
			})
	})

	for _, tc := range []struct {
		name  string
		opts  pointOpts
		lines []string
		sum   string
	}{
		{"ordinary tax, a business's Enova levy by the year", pointOpts{taxClass: "ordinary"}, []string{
			"fixed_energy|Fastpris|||744.000 kWh × 1.000000 = 744.00|standard",
			"monthly_fee|Fastbeløp|||1.000 month × 39.000000 = 39.00|standard",
			"electricity_tax|Elavgift|||744.000 kWh × 0.071300 = 53.05|standard",
			"enova_levy|Enovaavgift|||0.085 year × 800.000000 = 68.00|standard",
		}, "904.05"},
		{"reduced tax, a household's Enova levy by the kWh", pointOpts{taxClass: "reduced", household: true}, []string{
			"fixed_energy|Fastpris|||744.000 kWh × 1.000000 = 744.00|standard",
			"monthly_fee|Fastbeløp|||1.000 month × 39.000000 = 39.00|standard",
			"electricity_tax|Elavgift|||744.000 kWh × 0.005500 = 4.09|standard",
			"enova_levy|Enovaavgift|||744.000 kWh × 0.010000 = 7.44|standard",
		}, "794.53"},
		{"exempt from tax, a household in the north exempt from VAT", pointOpts{taxClass: "exempt", household: true, postal: "9008"}, []string{
			"fixed_energy|Fastpris|||744.000 kWh × 1.000000 = 744.00|exempt_household_north",
			"monthly_fee|Fastbeløp|||1.000 month × 39.000000 = 39.00|exempt_household_north",
			"enova_levy|Enovaavgift|||744.000 kWh × 0.010000 = 7.44|exempt_household_north",
		}, "790.44"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.opts.gridTariff = &taxes
			supply := hourly(tc.opts, fixed)
			assertSettlement(t, f.compute(supply, jan),
				`kwh=744.000 amount=`+tc.sum+` count=744/744 estimated=false blocked= ""`, tc.lines)
		})
	}

	t.Run("blockers", func(t *testing.T) {
		blocked := func(t *testing.T, got settlement, want string) {
			t.Helper()
			assertSettlement(t, got, want, nil)
		}
		t.Run("period_open", func(t *testing.T) {
			supply := hourly(pointOpts{}, fixed)
			got, err := computeSettlement(f.ctx, f.q, f.subject(supply), jan, f.at(time.January, 31, 23, 0))
			if err != nil {
				t.Fatal(err)
			}
			blocked(t, got, `kwh=744.000 amount=0.00 count=744/744 estimated=false blocked=period_open "the period ends 2026-02-01T00:00:00+01:00"`)
		})
		t.Run("no_tariff: no power tariff", func(t *testing.T) {
			point, supply := f.supply(pointOpts{}, jan.Start, nil)
			f.intervals(point, jan.Start, jan.End, time.Hour, "1")
			blocked(t, f.compute(supply, jan), `kwh=744.000 amount=0.00 count=744/744 estimated=false blocked=no_tariff "no power tariff"`)
		})
		t.Run("no_tariff: a day no power version covers", func(t *testing.T) {
			blocked(t, f.compute(hourly(pointOpts{}, late), jan),
				`kwh=744.000 amount=0.00 count=744/744 estimated=false blocked=no_tariff "Sen has no version on 2026-01-01"`)
		})
		t.Run("no_tariff: a day no grid version covers", func(t *testing.T) {
			blocked(t, f.compute(hourly(pointOpts{gridTariff: &shortGrid}, fixed), jan),
				`kwh=744.000 amount=0.00 count=744/744 estimated=false blocked=no_tariff "Kort nett has no version on 2026-01-31"`)
		})
		t.Run("no_consumption", func(t *testing.T) {
			_, supply := f.supply(pointOpts{}, jan.Start, &fixed)
			blocked(t, f.compute(supply, jan), `kwh=0.000 amount=0.00 count=0/744 estimated=false blocked=no_consumption "no consumption in the period"`)
		})
		t.Run("incomplete: a missing interval", func(t *testing.T) {
			point, supply := f.supply(pointOpts{}, jan.Start, &fixed)
			f.intervals(point, jan.Start, f.at(time.January, 15, 10, 0), time.Hour, "1")
			f.intervals(point, f.at(time.January, 15, 11, 0), jan.End, time.Hour, "1")
			blocked(t, f.compute(supply, jan), `kwh=743.000 amount=0.00 count=743/744 estimated=false blocked=incomplete "1 of 744 intervals are missing"`)
		})
		t.Run("incomplete: an interval crossing the period's end", func(t *testing.T) {
			point, supply := f.supply(pointOpts{}, jan.Start, &fixed)
			f.intervals(point, jan.Start, f.at(time.January, 31, 23, 0), time.Hour, "1")
			f.intervals(point, f.at(time.January, 31, 23, 0), f.at(time.February, 1, 1, 0), 2*time.Hour, "2")
			blocked(t, f.compute(supply, jan),
				`kwh=743.000 amount=0.00 count=743/744 estimated=false blocked=incomplete "an interval crosses 2026-02-01T00:00:00+01:00"`)
		})
		t.Run("incomplete: two intervals overlapping each other", func(t *testing.T) {
			point, supply := f.supply(pointOpts{}, jan.Start, &fixed)
			f.intervals(point, jan.Start, jan.End, time.Hour, "1")
			f.intervals(point, f.at(time.January, 3, 4, 15), f.at(time.January, 3, 4, 30), 15*time.Minute, "0.25")
			blocked(t, f.compute(supply, jan),
				`kwh=744.250 amount=0.00 count=745/745 estimated=false blocked=incomplete "intervals overlap at 2026-01-03T04:15:00+01:00"`)
		})
	})

	t.Run("price blockers", func(t *testing.T) {
		t.Run("price_gap", func(t *testing.T) {
			g := newSettlementFixture(t)
			tid := g.tariff("power", "Spotpris", testVersion{from: "2025-01-01", comp: []testComponent{{kind: componentSpotEnergy, name: "Strøm"}}})
			point, supply := g.supply(pointOpts{}, jan.Start, &tid)
			g.intervals(point, jan.Start, jan.End, time.Hour, "1")
			g.prices(jan.Start, g.at(time.January, 15, 10, 0), time.Hour, func(int) string { return "0.5" }, "final")
			g.prices(g.at(time.January, 15, 11, 0), jan.End, time.Hour, func(int) string { return "0.5" }, "preliminary")
			// The gap wins over the preliminary prices after it.
			assertSettlement(t, g.compute(supply, jan),
				`kwh=744.000 amount=0.00 count=744/744 estimated=false blocked=price_gap "no price for NO1 at 2026-01-15T10:00:00+01:00"`, nil)
		})
		t.Run("price_preliminary", func(t *testing.T) {
			g := newSettlementFixture(t)
			tid := g.tariff("power", "Spotpris", testVersion{from: "2025-01-01", comp: []testComponent{{kind: componentSpotEnergy, name: "Strøm"}}})
			point, supply := g.supply(pointOpts{}, jan.Start, &tid)
			g.intervals(point, jan.Start, jan.End, time.Hour, "1")
			g.prices(jan.Start, g.at(time.January, 31, 0, 0), time.Hour, func(int) string { return "0.5" }, "final")
			g.prices(g.at(time.January, 31, 0, 0), jan.End, time.Hour, func(int) string { return "0.5" }, "preliminary")
			assertSettlement(t, g.compute(supply, jan),
				`kwh=744.000 amount=0.00 count=744/744 estimated=false blocked=price_preliminary "prices for 2026-01-31 are preliminary"`, nil)
		})
		t.Run("a fixed tariff needs no prices", func(t *testing.T) {
			g := newSettlementFixture(t)
			tid := g.tariff("power", "Fastpris", testVersion{from: "2025-01-01", comp: []testComponent{{kind: componentFixedEnergy, name: "Fastpris", rate: strp("1")}}})
			point, supply := g.supply(pointOpts{}, jan.Start, &tid)
			g.intervals(point, jan.Start, jan.End, time.Hour, "1")
			assertSettlement(t, g.compute(supply, jan),
				`kwh=744.000 amount=744.00 count=744/744 estimated=false blocked= ""`,
				[]string{"fixed_energy|Fastpris|||744.000 kWh × 1.000000 = 744.00|standard"})
		})
	})
}

// TestCorrectionOf pins a correction's differences (plan reading 8): the
// baseline (the invoiced regular and its invoiced corrections) summed per
// key, the differences in the recomputed order then the vanished keys, zero
// lines left out, and no correction when nothing differs or the
// recomputation is blocked.
func TestCorrectionOf(t *testing.T) {
	t.Parallel()
	r := func(v string) *big.Rat {
		x, ok := new(big.Rat).SetString(v)
		if !ok {
			t.Fatalf("%s is not a number", v)
		}
		return x
	}
	line := func(kind, text, qty, unit, price, amount string) settlementLine {
		return settlementLine{Kind: kind, Text: text, Quantity: r(qty), Unit: unit, UnitPrice: r(price), Amount: r(amount), VATHint: "standard"}
	}
	jan := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	month := jan
	capacity := func(price, amount string) settlementLine {
		l := line(componentGridCapacity, "Kapasitet", "1", "month", price, amount)
		l.Month = &month
		return l
	}
	regular := settlement{ID: 41, Kind: contracts.SettlementRegular, State: contracts.SettlementInvoiced, KWh: r("744"), Lines: []settlementLine{
		line(componentSpotEnergy, "Strøm", "744", "kWh", "0.55", "409.20"),
		line(componentMonthlyFee, "Fastbeløp", "1", "month", "39", "39.00"),
		line(componentMarkup, "Rabatt", "744", "kWh", "0.01", "7.44"),
		capacity("200", "200.00"),
	}}
	earlier := settlement{ID: 57, Kind: contracts.SettlementCorrection, State: contracts.SettlementInvoiced, KWh: r("1"), Lines: []settlementLine{
		line(componentSpotEnergy, "Strøm", "1", "kWh", "0.55", "0.55"),
	}}
	recomputed := settlement{
		Kind: contracts.SettlementRegular, State: contracts.SettlementDraft, SupplyPeriodID: 7, MeteringPointID: 8, CustomerID: 9,
		PeriodStart: jan, PeriodEnd: jan.AddDate(0, 1, 0), KWh: r("750"), IntervalCount: 744, ExpectedIntervalCount: 744,
		Currency: "NOK", ContractName: "Spotpris",
		Lines: []settlementLine{
			// A version change: two lines of one key, summed.
			line(componentSpotEnergy, "Strøm", "400", "kWh", "0.5", "200.00"),
			line(componentSpotEnergy, "Strøm", "350", "kWh", "0.6", "212.50"),
			line(componentMonthlyFee, "Fastbeløp", "1", "month", "39", "39.00"),
			line(componentMarkup, "Påslag", "750", "kWh", "0.02", "15.00"),
			capacity("300", "300.00"),
		},
	}
	recomputed.Amount = sumLineAmounts(recomputed.Lines)

	got, ok := correctionOf(recomputed, []settlement{regular, earlier})
	if !ok {
		t.Fatal("no correction for different figures")
	}
	want := []string{
		"spot_energy|Strøm|||5.000 kWh × 0.500000 = 2.75|standard",
		"markup|Påslag|||750.000 kWh × 0.020000 = 15.00|standard",
		"grid_capacity|Kapasitet||2026-01-01|0.000 month × 300.000000 = 100.00|standard",
		"markup|Rabatt|||-744.000 kWh × 0.010000 = -7.44|standard",
	}
	if g, w := strings.Join(lineTexts(got), "\n"), strings.Join(want, "\n"); g != w {
		t.Errorf("lines\n got:\n%s\nwant:\n%s", g, w)
	}
	c := got.toContract()
	if c.Kind != contracts.SettlementCorrection || c.CorrectsID == nil || *c.CorrectsID != 41 || c.KWh != "5.000" ||
		c.Amount != "110.31" || c.State != contracts.SettlementDraft || c.ID != 0 ||
		c.SupplyPeriodID != 7 || c.CustomerID != 9 || c.ContractName != "Spotpris" || c.Blocked != "" {
		t.Errorf("correction = %+v", c)
	}

	// Recomputed exactly as invoiced: nothing to correct.
	same := regular
	same.State, same.ID = contracts.SettlementDraft, 0
	if _, ok := correctionOf(same, []settlement{regular}); ok {
		t.Error("a correction for identical figures")
	}
	// A blocked recomputation writes no correction.
	stuck := recomputed
	stuck.Blocked, stuck.Lines = contracts.SettlementPriceGap, nil
	if _, ok := correctionOf(stuck, []settlement{regular}); ok {
		t.Error("a correction for a blocked recomputation")
	}
}
