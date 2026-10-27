package energy

import (
	"encoding/json"
	"maps"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/energy/gen"
)

// tariffBody decodes a tariff body as the router would.
func tariffBody(t *testing.T, raw string) gen.TariffRequest {
	t.Helper()
	var b gen.TariffRequest
	if err := json.Unmarshal([]byte(raw), &b); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return b
}

// oneVersion is a body of kind with one open version from 2026-01-01
// holding components (JSON objects, comma-separated).
func oneVersion(kind, components string) string {
	return `{"kind":"` + kind + `","name":"T","versions":[{"validFrom":"2026-01-01","components":[` + components + `]}]}`
}

const (
	spotC   = `{"kind":"spot_energy","name":"Spot"}`
	fixedC  = `{"kind":"fixed_energy","name":"Fixed","rate":1.25}`
	markupC = `{"kind":"markup","name":"Markup","rate":0.049}`
	feeC    = `{"kind":"monthly_fee","name":"Fee","rate":39}`
	gridEC  = `{"kind":"grid_energy","name":"Energy","rate":0.3}`
	capC    = `{"kind":"grid_capacity","name":"Capacity","tiers":[{"fromKw":0,"perMonth":135},{"fromKw":2,"perMonth":200}]}`
	fixedGC = `{"kind":"grid_fixed","name":"Fixed","rate":50}`
	taxC    = `{"kind":"electricity_tax","name":"Tax","rate":0.0979,"rateReduced":0.0056}`
	enovaC  = `{"kind":"enova_levy","name":"Enova","rate":0.01,"rateBusiness":800}`
)

// TestValidateTariff_Keys pins reading 3: each rule's refusal, under the key
// of its place in the body, and nothing else refused.
func TestValidateTariff_Keys(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		body string
		want map[string]string // key -> a substring of its message
	}{
		{"a valid power tariff", oneVersion("power", spotC+","+markupC+","+markupC+","+feeC), nil},
		{"a valid fixed power tariff", oneVersion("power", fixedC), nil},
		{"a valid grid tariff", oneVersion("grid", gridEC+","+capC+","+fixedGC+","+fixedGC+","+taxC+","+enovaC+","+feeC), nil},
		{"a grid energy with its night rate", oneVersion("grid",
			`{"kind":"grid_energy","name":"E","rate":0.4,"rateNight":0.3,"dayFrom":6,"dayTo":22,"weekdaysOnly":true}`), nil},
		{"weekdaysOnly false is no field", oneVersion("power", `{"kind":"spot_energy","name":"S","weekdaysOnly":false}`), nil},

		{"no kind", `{"name":"T","versions":[{"validFrom":"2026-01-01","components":[` + spotC + `]}]}`,
			map[string]string{"kind": "Kind must be power or grid."}},
		{"an unknown kind", oneVersion("water", spotC), map[string]string{"kind": "power or grid"}},
		{"a blank name", `{"kind":"power","name":"  ","versions":[{"validFrom":"2026-01-01","components":[` + spotC + `]}]}`,
			map[string]string{"name": "Name is required."}},
		{"a name too long", `{"kind":"power","name":"` + strings.Repeat("n", 121) + `","versions":[{"validFrom":"2026-01-01","components":[` + spotC + `]}]}`,
			map[string]string{"name": "120"}},
		{"a description too long", `{"kind":"power","name":"T","description":"` + strings.Repeat("d", 501) + `","versions":[{"validFrom":"2026-01-01","components":[` + spotC + `]}]}`,
			map[string]string{"description": "500"}},
		{"no versions", `{"kind":"power","name":"T","versions":[]}`, map[string]string{"versions": "at least one version"}},
		{"no validFrom", `{"kind":"power","name":"T","versions":[{"components":[` + spotC + `]}]}`,
			map[string]string{"versions[0].validFrom": "valid-from"}},
		{"validTo on validFrom", `{"kind":"power","name":"T","versions":[{"validFrom":"2026-01-01","validTo":"2026-01-01","components":[` + spotC + `]}]}`,
			map[string]string{"versions[0].validTo": "after its valid-from"}},
		{"no components", `{"kind":"power","name":"T","versions":[{"validFrom":"2026-01-01","components":[]}]}`,
			map[string]string{"versions[0].components": "at least one component"}},

		{"a power tariff without an energy kind", oneVersion("power", markupC),
			map[string]string{"versions[0].components": "A power tariff's version needs exactly one of spot_energy and fixed_energy."}},
		{"a power tariff with both energy kinds", oneVersion("power", spotC+","+fixedC),
			map[string]string{"versions[0].components": "exactly one"}},
		{"two spot energies", oneVersion("power", spotC+","+spotC), map[string]string{"versions[0].components": "exactly one"}},
		{"a grid tariff cannot hold spot_energy", oneVersion("grid", spotC),
			map[string]string{"versions[0].components[0].kind": "A grid tariff cannot hold spot_energy."}},
		{"a grid tariff cannot hold markup", oneVersion("grid", gridEC+","+markupC),
			map[string]string{"versions[0].components[1].kind": "A grid tariff cannot hold markup."}},
		{"a power tariff cannot hold grid_energy", oneVersion("power", spotC+","+gridEC),
			map[string]string{"versions[0].components[1].kind": "A power tariff cannot hold grid_energy."}},
		{"a power tariff cannot hold grid_capacity", oneVersion("power", spotC+","+capC),
			map[string]string{"versions[0].components[1].kind": "cannot hold grid_capacity"}},
		{"a power tariff cannot hold grid_fixed", oneVersion("power", spotC+","+fixedGC),
			map[string]string{"versions[0].components[1].kind": "cannot hold grid_fixed"}},
		{"a power tariff cannot hold electricity_tax", oneVersion("power", spotC+","+taxC),
			map[string]string{"versions[0].components[1].kind": "cannot hold electricity_tax"}},
		{"a power tariff cannot hold enova_levy", oneVersion("power", spotC+","+enovaC),
			map[string]string{"versions[0].components[1].kind": "cannot hold enova_levy"}},
		{"a grid tariff cannot hold fixed_energy", oneVersion("grid", fixedC),
			map[string]string{"versions[0].components[0].kind": "cannot hold fixed_energy"}},
		{"an unknown component kind", oneVersion("grid", `{"kind":"water","name":"W"}`),
			map[string]string{"versions[0].components[0].kind": "Component kind must be one of"}},
		{"two grid energies", oneVersion("grid", gridEC+","+gridEC),
			map[string]string{"versions[0].components[1].kind": "only one grid_energy"}},
		{"two capacities", oneVersion("grid", capC+","+capC), map[string]string{"versions[0].components[1].kind": "only one grid_capacity"}},
		{"two taxes", oneVersion("grid", taxC+","+taxC), map[string]string{"versions[0].components[1].kind": "only one electricity_tax"}},
		{"two levies", oneVersion("grid", enovaC+","+enovaC), map[string]string{"versions[0].components[1].kind": "only one enova_levy"}},
		{"a component without a name", oneVersion("power", `{"kind":"spot_energy"}`),
			map[string]string{"versions[0].components[0].name": "needs a name"}},
		{"a component name too long", oneVersion("power", `{"kind":"spot_energy","name":"`+strings.Repeat("c", 81)+`"}`),
			map[string]string{"versions[0].components[0].name": "80"}},

		{"spot_energy takes no rate", oneVersion("power", `{"kind":"spot_energy","name":"S","rate":1}`),
			map[string]string{"versions[0].components[0].rate": "takes no rate"}},
		{"fixed_energy needs its rate", oneVersion("power", `{"kind":"fixed_energy","name":"F"}`),
			map[string]string{"versions[0].components[0].rate": "needs its rate"}},
		{"markup needs its rate", oneVersion("power", spotC+`,{"kind":"markup","name":"M"}`),
			map[string]string{"versions[0].components[1].rate": "needs its rate"}},
		{"markup takes no night rate", oneVersion("power", spotC+`,{"kind":"markup","name":"M","rate":1,"rateNight":1}`),
			map[string]string{"versions[0].components[1].rateNight": "takes no night rate"}},
		{"monthly_fee needs its rate", oneVersion("power", spotC+`,{"kind":"monthly_fee","name":"M"}`),
			map[string]string{"versions[0].components[1].rate": "needs its rate"}},
		{"grid_fixed needs its rate", oneVersion("grid", `{"kind":"grid_fixed","name":"G"}`),
			map[string]string{"versions[0].components[0].rate": "needs its rate"}},
		{"grid_fixed takes no tiers", oneVersion("grid", `{"kind":"grid_fixed","name":"G","rate":1,"tiers":[{"fromKw":0,"perMonth":1}]}`),
			map[string]string{"versions[0].components[0].tiers": "takes no tiers"}},
		{"grid_energy needs its rate", oneVersion("grid", `{"kind":"grid_energy","name":"E"}`),
			map[string]string{"versions[0].components[0].rate": "needs its rate"}},
		{"grid_energy's night rate without its hours", oneVersion("grid", `{"kind":"grid_energy","name":"E","rate":1,"rateNight":0.5}`),
			map[string]string{"versions[0].components[0].dayFrom": "together", "versions[0].components[0].dayTo": "together"}},
		{"grid_energy's hours without the night rate", oneVersion("grid", `{"kind":"grid_energy","name":"E","rate":1,"dayFrom":6,"dayTo":22}`),
			map[string]string{"versions[0].components[0].rateNight": "together"}},
		{"weekdaysOnly without a night rate", oneVersion("grid", `{"kind":"grid_energy","name":"E","rate":1,"weekdaysOnly":true}`),
			map[string]string{"versions[0].components[0].rateNight": "together", "versions[0].components[0].dayFrom": "together", "versions[0].components[0].dayTo": "together"}},
		{"a day ending before it starts", oneVersion("grid", `{"kind":"grid_energy","name":"E","rate":1,"rateNight":0.5,"dayFrom":22,"dayTo":6}`),
			map[string]string{"versions[0].components[0].dayTo": "end after it starts"}},
		{"a day from 24", oneVersion("grid", `{"kind":"grid_energy","name":"E","rate":1,"rateNight":0.5,"dayFrom":24,"dayTo":24}`),
			map[string]string{"versions[0].components[0].dayFrom": "0 to 23"}},
		{"a day to 25", oneVersion("grid", `{"kind":"grid_energy","name":"E","rate":1,"rateNight":0.5,"dayFrom":0,"dayTo":25}`),
			map[string]string{"versions[0].components[0].dayTo": "1 to 24"}},
		{"a day from -1", oneVersion("grid", `{"kind":"grid_energy","name":"E","rate":1,"rateNight":0.5,"dayFrom":-1,"dayTo":24}`),
			map[string]string{"versions[0].components[0].dayFrom": "0 to 23"}},
		{"weekdaysOnly on a markup", oneVersion("power", spotC+`,{"kind":"markup","name":"M","rate":1,"weekdaysOnly":true}`),
			map[string]string{"versions[0].components[1].weekdaysOnly": "takes no weekdays-only rule"}},
		{"grid_capacity needs its tiers", oneVersion("grid", `{"kind":"grid_capacity","name":"C"}`),
			map[string]string{"versions[0].components[0].tiers": "needs its tiers"}},
		{"grid_capacity takes no rate", oneVersion("grid", `{"kind":"grid_capacity","name":"C","rate":1,"tiers":[{"fromKw":0,"perMonth":1}]}`),
			map[string]string{"versions[0].components[0].rate": "takes no rate"}},
		{"electricity_tax needs its reduced rate", oneVersion("grid", `{"kind":"electricity_tax","name":"T","rate":0.1}`),
			map[string]string{"versions[0].components[0].rateReduced": "needs its reduced rate"}},
		{"electricity_tax needs its rate", oneVersion("grid", `{"kind":"electricity_tax","name":"T","rateReduced":0.1}`),
			map[string]string{"versions[0].components[0].rate": "needs its rate"}},
		{"enova_levy needs its business rate", oneVersion("grid", `{"kind":"enova_levy","name":"L","rate":0.01}`),
			map[string]string{"versions[0].components[0].rateBusiness": "needs its business rate"}},
		{"enova_levy takes no reduced rate", oneVersion("grid", `{"kind":"enova_levy","name":"L","rate":0.01,"rateBusiness":800,"rateReduced":1}`),
			map[string]string{"versions[0].components[0].rateReduced": "takes no reduced rate"}},

		{"a negative rate", oneVersion("power", `{"kind":"fixed_energy","name":"F","rate":-0.01}`),
			map[string]string{"versions[0].components[0].rate": "Rate cannot be negative."}},
		{"a rate of a million", oneVersion("power", `{"kind":"fixed_energy","name":"F","rate":1000000}`),
			map[string]string{"versions[0].components[0].rate": "less than 1000000"}},
		{"a rate rounding onto a million", oneVersion("power", `{"kind":"fixed_energy","name":"F","rate":999999.9999995}`),
			map[string]string{"versions[0].components[0].rate": "less than 1000000"}},
		{"a negative night rate", oneVersion("grid", `{"kind":"grid_energy","name":"E","rate":1,"rateNight":-1,"dayFrom":6,"dayTo":22}`),
			map[string]string{"versions[0].components[0].rateNight": "Night rate cannot be negative."}},
		{"a negative business rate", oneVersion("grid", `{"kind":"enova_levy","name":"L","rate":0.01,"rateBusiness":-1}`),
			map[string]string{"versions[0].components[0].rateBusiness": "Business rate cannot be negative."}},

		{"no tiers", oneVersion("grid", `{"kind":"grid_capacity","name":"C","tiers":[]}`),
			map[string]string{"versions[0].components[0].tiers": "between 1 and 50"}},
		{"51 tiers", oneVersion("grid", `{"kind":"grid_capacity","name":"C","tiers":[`+manyTiers(51)+`]}`),
			map[string]string{"versions[0].components[0].tiers": "between 1 and 50"}},
		{"50 tiers", oneVersion("grid", `{"kind":"grid_capacity","name":"C","tiers":[`+manyTiers(50)+`]}`), nil},
		{"the first tier above 0", oneVersion("grid", `{"kind":"grid_capacity","name":"C","tiers":[{"fromKw":1,"perMonth":1}]}`),
			map[string]string{"versions[0].components[0].tiers[0].fromKw": "first tier must start at 0"}},
		{"tiers not ascending", oneVersion("grid", `{"kind":"grid_capacity","name":"C","tiers":[{"fromKw":0,"perMonth":1},{"fromKw":5,"perMonth":2},{"fromKw":5,"perMonth":3}]}`),
			map[string]string{"versions[0].components[0].tiers[2].fromKw": "above the one before"}},
		{"tiers equal once rounded", oneVersion("grid", `{"kind":"grid_capacity","name":"C","tiers":[{"fromKw":0,"perMonth":1},{"fromKw":0.0004,"perMonth":2}]}`),
			map[string]string{"versions[0].components[0].tiers[1].fromKw": "above the one before"}},
		{"a tier from 100000 kW", oneVersion("grid", `{"kind":"grid_capacity","name":"C","tiers":[{"fromKw":0,"perMonth":1},{"fromKw":100000,"perMonth":2}]}`),
			map[string]string{"versions[0].components[0].tiers[1].fromKw": "below 100000"}},
		{"a negative tier", oneVersion("grid", `{"kind":"grid_capacity","name":"C","tiers":[{"fromKw":-1,"perMonth":1}]}`),
			map[string]string{"versions[0].components[0].tiers[0].fromKw": "below 0"}},
		{"a negative monthly price", oneVersion("grid", `{"kind":"grid_capacity","name":"C","tiers":[{"fromKw":0,"perMonth":-1}]}`),
			map[string]string{"versions[0].components[0].tiers[0].perMonth": "cannot be negative"}},
		{"a monthly price of a million", oneVersion("grid", `{"kind":"grid_capacity","name":"C","tiers":[{"fromKw":0,"perMonth":1000000}]}`),
			map[string]string{"versions[0].components[0].tiers[0].perMonth": "less than 1000000"}},

		{"versions meeting at a day", `{"kind":"power","name":"T","versions":[` +
			`{"validFrom":"2026-01-01","validTo":"2026-07-01","components":[` + spotC + `]},` +
			`{"validFrom":"2026-07-01","components":[` + spotC + `]}]}`, nil},
		{"versions with a gap", `{"kind":"power","name":"T","versions":[` +
			`{"validFrom":"2026-01-01","validTo":"2026-03-01","components":[` + spotC + `]},` +
			`{"validFrom":"2026-07-01","components":[` + spotC + `]}]}`, nil},
		{"an open version overlapping a later one", `{"kind":"power","name":"T","versions":[` +
			`{"validFrom":"2027-01-01","components":[` + spotC + `]},` +
			`{"validFrom":"2026-01-01","components":[` + spotC + `]}]}`,
			map[string]string{"versions[0].validFrom": "The version overlaps versions[1], valid from 2026-01-01."}},
		{"a later version overlapping only the first", `{"kind":"power","name":"T","versions":[` +
			`{"validFrom":"2026-01-01","components":[` + spotC + `]},` +
			`{"validFrom":"2026-03-01","validTo":"2026-04-01","components":[` + spotC + `]},` +
			`{"validFrom":"2026-05-01","validTo":"2026-06-01","components":[` + spotC + `]}]}`,
			map[string]string{"versions[1].validFrom": "overlaps versions[0]", "versions[2].validFrom": "overlaps versions[0]"}},
		{"two versions from one day", `{"kind":"power","name":"T","versions":[` +
			`{"validFrom":"2026-01-01","validTo":"2026-02-01","components":[` + spotC + `]},` +
			`{"validFrom":"2026-01-01","validTo":"2026-03-01","components":[` + spotC + `]}]}`,
			map[string]string{"versions[1].validFrom": "overlaps versions[0]"}},
		{"the errors of a second version's third component", `{"kind":"power","name":"T","versions":[` +
			`{"validFrom":"2026-01-01","validTo":"2026-02-01","components":[` + spotC + `]},` +
			`{"validFrom":"2026-02-01","components":[` + spotC + `,` + markupC + `,{"kind":"markup","name":"M","rate":-1}]}]}`,
			map[string]string{"versions[1].components[2].rate": "cannot be negative"}},
		{"every error at once", `{"kind":"power","name":"","versions":[{"components":[{"kind":"markup","name":"M"}]}]}`,
			map[string]string{
				"name": "required", "versions[0].validFrom": "valid-from", "versions[0].components": "exactly one",
				"versions[0].components[0].rate": "needs its rate",
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, errs := validateTariff(tariffBody(t, tc.body))
			gotKeys := slices.Sorted(maps.Keys(errs))
			wantKeys := slices.Sorted(maps.Keys(tc.want))
			if !slices.Equal(gotKeys, wantKeys) {
				t.Fatalf("keys = %v, want %v (errors %v)", gotKeys, wantKeys, errs)
			}
			for k, sub := range tc.want {
				if len(errs[k]) != 1 || !strings.Contains(errs[k][0], sub) {
					t.Errorf("errors[%s] = %v, want one message containing %q", k, errs[k], sub)
				}
			}
		})
	}
}

func manyTiers(n int) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = `{"fromKw":` + strconv.Itoa(i) + `,"perMonth":1}`
	}
	return strings.Join(parts, ",")
}

// TestValidateTariff_Normalises pins what the handlers write: names and the
// description trimmed (a blank description none), rates to six decimals,
// tiers to three and two, positions the body's order, the dates as given.
func TestValidateTariff_Normalises(t *testing.T) {
	t.Parallel()
	in, errs := validateTariff(tariffBody(t, `{"kind":" grid ","name":"  Nettleie  ","description":"   ","versions":[
		{"validFrom":"2026-01-01","validTo":"2026-07-01","components":[
			{"kind":"grid_energy","name":" Energiledd ","rate":0.12345649,"rateNight":0.1234565,"dayFrom":6,"dayTo":22,"weekdaysOnly":true},
			{"kind":"grid_capacity","name":"Kapasitet","tiers":[{"fromKw":0,"perMonth":135.005},{"fromKw":2.0005,"perMonth":200}]}
		]}]}`))
	if len(errs) > 0 {
		t.Fatalf("errors = %v, want none", errs)
	}
	if in.Kind != "grid" || in.Name != "Nettleie" || in.Description != nil {
		t.Errorf("header = %q %q %v, want grid, Nettleie and no description", in.Kind, in.Name, in.Description)
	}
	v := in.Versions[0]
	if got := v.ValidFrom.Time.Format("2006-01-02"); got != "2026-01-01" || !v.ValidTo.Valid || v.ValidTo.Time.Format("2006-01-02") != "2026-07-01" {
		t.Errorf("dates = %v %v", v.ValidFrom, v.ValidTo)
	}
	e, c := v.Components[0], v.Components[1]
	if e.Position != 0 || c.Position != 1 || e.Name != "Energiledd" {
		t.Errorf("positions %d %d name %q, want 0, 1 and Energiledd", e.Position, c.Position, e.Name)
	}
	if r := floatPtrFromNumeric(e.Rate); r == nil || *r != 0.123456 {
		t.Errorf("rate = %v, want 0.123456", r)
	}
	if r := floatPtrFromNumeric(e.RateNight); r == nil || *r != 0.123457 {
		t.Errorf("rateNight = %v, want 0.123457", r)
	}
	if e.DayFrom == nil || *e.DayFrom != 6 || e.DayTo == nil || *e.DayTo != 22 || !e.WeekdaysOnly {
		t.Errorf("day = %v %v %v", e.DayFrom, e.DayTo, e.WeekdaysOnly)
	}
	if e.RateReduced.Valid || e.RateBusiness.Valid || e.Tiers != nil {
		t.Errorf("unused fields set: %+v", e)
	}
	if got := string(c.Tiers); got != `[{"fromKw":0,"perMonth":135.01},{"fromKw":2.001,"perMonth":200}]` {
		t.Errorf("tiers = %s", got)
	}
}
