package energy

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/energy/gen"
	"github.com/vantigo-io/vantigo/server/internal/energy/store"
)

// This file is a tariff body's validation (energy tariffs plan, reading 3):
// pure, every check run regardless of the others, every refusal keyed by its
// place in the body — versions[1].components[2].rate — with 0-based body
// positions, so the 400 names the line the person is looking at. What it
// answers when nothing is refused is the body as the tables store it: names
// trimmed, rates rounded as numeric(12,6) rounds them, the tiers as the jsonb
// holds them and each component's position its place in its version. The
// handlers write it as it is.

// A tariff component's kind (design D3, ck_tariff_components_kind).
const (
	componentSpotEnergy     = "spot_energy"
	componentFixedEnergy    = "fixed_energy"
	componentMarkup         = "markup"
	componentMonthlyFee     = "monthly_fee"
	componentGridEnergy     = "grid_energy"
	componentGridCapacity   = "grid_capacity"
	componentGridFixed      = "grid_fixed"
	componentElectricityTax = "electricity_tax"
	componentEnovaLevy      = "enova_levy"
)

// componentKinds is every kind, in the order a refusal lists them.
var componentKinds = []string{
	componentSpotEnergy, componentFixedEnergy, componentMarkup, componentMonthlyFee,
	componentGridEnergy, componentGridCapacity, componentGridFixed, componentElectricityTax, componentEnovaLevy,
}

// componentKindsOf is which kinds a power and a grid tariff may hold.
var componentKindsOf = map[string]map[string]bool{
	tariffKindPower: {componentSpotEnergy: true, componentFixedEnergy: true, componentMarkup: true, componentMonthlyFee: true},
	tariffKindGrid: {
		componentGridEnergy: true, componentGridCapacity: true, componentGridFixed: true,
		componentElectricityTax: true, componentEnovaLevy: true, componentMonthlyFee: true,
	},
}

// singleComponentKinds may appear at most once in a version: each prices one
// thing once. markup, monthly_fee and grid_fixed may repeat (a supplier may
// list two markups); spot_energy and fixed_energy are the power version's
// exactly-one rule instead.
var singleComponentKinds = map[string]bool{
	componentGridEnergy: true, componentGridCapacity: true, componentElectricityTax: true, componentEnovaLevy: true,
}

// The columns' limits.
const (
	tariffNameMaxLength        = 120
	tariffDescriptionMaxLength = 500
	componentNameMaxLength     = 80
	// A rate is numeric(12,6): six decimals and below a million.
	tariffRateLimit = 1_000_000
	// A tier's fromKw is three decimals below 100 000 kW, its perMonth two
	// decimals below a million.
	tierFromKwLimit   = 100_000
	tierPerMonthLimit = 1_000_000
	tiersMax          = 50
)

// tariffInput is a validated tariff body, ready to write.
type tariffInput struct {
	Kind        string
	Name        string
	Description *string
	Versions    []tariffVersionInput
}

// tariffVersionInput is one version, its components with every field but
// VersionID, which the write fills in once the version row exists.
type tariffVersionInput struct {
	ValidFrom  pgtype.Date
	ValidTo    pgtype.Date
	Components []store.InsertTariffComponentParams
}

// validateTariff judges body (reading 3). It answers the body ready to write
// and the refusals keyed by place; the input is meaningful only when there
// are none.
func validateTariff(body gen.TariffRequest) (tariffInput, map[string][]string) {
	errs := map[string][]string{}
	add := func(key, msg string) { errs[key] = append(errs[key], msg) }
	var in tariffInput

	kindKnown := false
	if body.Kind != nil {
		in.Kind = strings.TrimSpace(*body.Kind)
		_, kindKnown = componentKindsOf[in.Kind]
	}
	if !kindKnown {
		add("kind", "Kind must be power or grid.")
	}

	if body.Name != nil {
		in.Name = strings.TrimSpace(*body.Name)
	}
	switch {
	case in.Name == "":
		add("name", "Name is required.")
	case utf16Length(in.Name) > tariffNameMaxLength:
		add("name", fmt.Sprintf("Name cannot be longer than %d characters.", tariffNameMaxLength))
	}
	if body.Description != nil {
		if d := strings.TrimSpace(*body.Description); d != "" {
			in.Description = &d
			if utf16Length(d) > tariffDescriptionMaxLength {
				add("description", fmt.Sprintf("Description cannot be longer than %d characters.", tariffDescriptionMaxLength))
			}
		}
	}

	if body.Versions == nil || len(*body.Versions) == 0 {
		add("versions", "A tariff needs at least one version.")
		return in, errs
	}
	versions := *body.Versions
	in.Versions = make([]tariffVersionInput, len(versions))
	for i, v := range versions {
		in.Versions[i] = validateTariffVersion(add, fmt.Sprintf("versions[%d]", i), v, in.Kind, kindKnown)
	}
	checkVersionOverlaps(add, in.Versions)
	return in, errs
}

// validateTariffVersion judges one version at key: its dates, then each of
// its components, then the rules across them, which hold only when the
// tariff's kind is known.
func validateTariffVersion(add func(key, msg string), key string, v gen.TariffVersionRequest, kind string, kindKnown bool) tariffVersionInput {
	var out tariffVersionInput
	if v.ValidFrom == nil {
		add(key+".validFrom", "A version needs a valid-from date.")
	} else {
		out.ValidFrom = pgtype.Date{Time: v.ValidFrom.Time, Valid: true}
	}
	if v.ValidTo != nil {
		out.ValidTo = pgtype.Date{Time: v.ValidTo.Time, Valid: true}
		if out.ValidFrom.Valid && !out.ValidTo.Time.After(out.ValidFrom.Time) {
			add(key+".validTo", "A version's valid-to date must be after its valid-from date.")
		}
	}

	if v.Components == nil || len(*v.Components) == 0 {
		add(key+".components", "A version needs at least one component.")
		return out
	}
	components := *v.Components
	out.Components = make([]store.InsertTariffComponentParams, len(components))
	energyKinds := 0
	seen := map[string]bool{}
	for j, c := range components {
		ckey := fmt.Sprintf("%s.components[%d]", key, j)
		out.Components[j] = validateTariffComponent(add, ckey, c, kind, kindKnown)
		out.Components[j].Position = int16(j)
		k := out.Components[j].Kind
		if k == componentSpotEnergy || k == componentFixedEnergy {
			energyKinds++
		}
		if singleComponentKinds[k] && kindKnown && componentKindsOf[kind][k] {
			if seen[k] {
				add(ckey+".kind", fmt.Sprintf("A version can hold only one %s component.", k))
			}
			seen[k] = true
		}
	}
	if kindKnown && kind == tariffKindPower && energyKinds != 1 {
		add(key+".components", "A power tariff's version needs exactly one of spot_energy and fixed_energy.")
	}
	return out
}

// componentFields is which of a component's optional fields each kind
// prices with; required is what it cannot do without. grid_energy's night
// rate (rateNight, dayFrom, dayTo, weekdaysOnly) is optional as a group and
// judged on its own (checkNightRate).
var componentFields = map[string]struct{ required, allowed []string }{
	componentSpotEnergy:     {},
	componentFixedEnergy:    {required: []string{"rate"}},
	componentMarkup:         {required: []string{"rate"}},
	componentMonthlyFee:     {required: []string{"rate"}},
	componentGridFixed:      {required: []string{"rate"}},
	componentGridEnergy:     {required: []string{"rate"}, allowed: []string{"rateNight", "dayFrom", "dayTo", "weekdaysOnly"}},
	componentGridCapacity:   {required: []string{"tiers"}},
	componentElectricityTax: {required: []string{"rate", "rateReduced"}},
	componentEnovaLevy:      {required: []string{"rate", "rateBusiness"}},
}

// componentFieldLabels is each field as a refusal names it.
var componentFieldLabels = map[string]string{
	"rate": "rate", "rateNight": "night rate", "dayFrom": "day start", "dayTo": "day end",
	"weekdaysOnly": "weekdays-only rule", "tiers": "tiers", "rateReduced": "reduced rate", "rateBusiness": "business rate",
}

// validateTariffComponent judges one component at key: its kind against the
// tariff's, its name, which fields it carries, and each figure's range. A
// field the kind does not price with must be absent, so a body says only
// what will price; weekdaysOnly false counts as absent, being what the
// column holds when nothing is said.
func validateTariffComponent(add func(key, msg string), key string, c gen.TariffComponentRequest, tariffKind string, tariffKindKnown bool) store.InsertTariffComponentParams {
	var out store.InsertTariffComponentParams
	if c.Kind != nil {
		out.Kind = strings.TrimSpace(*c.Kind)
	}
	fields, kindKnown := componentFields[out.Kind]
	switch {
	case !kindKnown:
		add(key+".kind", "Component kind must be one of "+strings.Join(componentKinds, ", ")+".")
	case tariffKindKnown && !componentKindsOf[tariffKind][out.Kind]:
		add(key+".kind", fmt.Sprintf("A %s tariff cannot hold %s.", tariffKind, out.Kind))
	}

	if c.Name != nil {
		out.Name = strings.TrimSpace(*c.Name)
	}
	switch {
	case out.Name == "":
		add(key+".name", "A component needs a name.")
	case utf16Length(out.Name) > componentNameMaxLength:
		add(key+".name", fmt.Sprintf("A component's name cannot be longer than %d characters.", componentNameMaxLength))
	}

	present := map[string]bool{
		"rate": c.Rate != nil, "rateNight": c.RateNight != nil, "dayFrom": c.DayFrom != nil, "dayTo": c.DayTo != nil,
		"weekdaysOnly": c.WeekdaysOnly != nil && *c.WeekdaysOnly, "tiers": c.Tiers != nil,
		"rateReduced": c.RateReduced != nil, "rateBusiness": c.RateBusiness != nil,
	}
	if kindKnown {
		uses := map[string]bool{}
		for _, f := range fields.required {
			uses[f] = true
			if !present[f] {
				add(key+"."+f, fmt.Sprintf("A %s component needs its %s.", out.Kind, componentFieldLabels[f]))
			}
		}
		for _, f := range fields.allowed {
			uses[f] = true
		}
		for _, f := range []string{"rate", "rateNight", "dayFrom", "dayTo", "weekdaysOnly", "tiers", "rateReduced", "rateBusiness"} {
			if present[f] && !uses[f] {
				add(key+"."+f, fmt.Sprintf("A %s component takes no %s.", out.Kind, componentFieldLabels[f]))
			}
		}
		if out.Kind == componentGridEnergy {
			checkNightRate(add, key, present)
		}
	}

	out.Rate = tariffRate(add, key+".rate", "Rate", c.Rate)
	out.RateNight = tariffRate(add, key+".rateNight", "Night rate", c.RateNight)
	out.RateReduced = tariffRate(add, key+".rateReduced", "Reduced rate", c.RateReduced)
	out.RateBusiness = tariffRate(add, key+".rateBusiness", "Business rate", c.RateBusiness)
	out.DayFrom, out.DayTo = dayHours(add, key, c.DayFrom, c.DayTo)
	out.WeekdaysOnly = c.WeekdaysOnly != nil && *c.WeekdaysOnly
	if c.Tiers != nil {
		out.Tiers = tariffTiers(add, key+".tiers", *c.Tiers)
	}
	return out
}

// checkNightRate is grid_energy's night rate: rateNight, dayFrom and dayTo
// all or none, and weekdaysOnly only with them. Each missing part is its own
// field's refusal.
func checkNightRate(add func(key, msg string), key string, present map[string]bool) {
	group := []string{"rateNight", "dayFrom", "dayTo"}
	some := present["weekdaysOnly"]
	for _, f := range group {
		some = some || present[f]
	}
	if !some {
		return
	}
	for _, f := range group {
		if !present[f] {
			add(key+"."+f, "A night rate needs rateNight, dayFrom and dayTo together.")
		}
	}
}

// tariffRate is a rate as numeric(12,6) stores it: rounded to six decimals,
// at least 0 and below a million. Absent is SQL NULL.
func tariffRate(add func(key, msg string), key, label string, v *float64) pgtype.Numeric {
	if v == nil {
		return pgtype.Numeric{}
	}
	r := roundPrice(*v)
	switch {
	case r < 0:
		add(key, label+" cannot be negative.")
		return pgtype.Numeric{}
	case r >= tariffRateLimit:
		add(key, fmt.Sprintf("%s must be less than %d.", label, tariffRateLimit))
		return pgtype.Numeric{}
	}
	n, err := numericFromFloatPtr(&r)
	if err != nil {
		add(key, label+" is not a number.")
	}
	return n
}

// dayHours is the night rate's day: whole hours, 0 ≤ dayFrom < dayTo ≤ 24.
func dayHours(add func(key, msg string), key string, from, to *int32) (*int16, *int16) {
	var f, t *int16
	if from != nil {
		if *from < 0 || *from > 23 {
			add(key+".dayFrom", "The day must start at a whole hour from 0 to 23.")
		} else {
			v := int16(*from)
			f = &v
		}
	}
	if to != nil {
		if *to < 1 || *to > 24 {
			add(key+".dayTo", "The day must end at a whole hour from 1 to 24.")
		} else {
			v := int16(*to)
			t = &v
		}
	}
	if f != nil && t != nil && *t <= *f {
		add(key+".dayTo", "The day must end after it starts.")
	}
	return f, t
}

// tariffTiers judges grid_capacity's tiers at key and answers them as the
// jsonb holds them: 1–50, the first from 0 kW, each from strictly above the
// one before, fromKw to three decimals below 100 000 and perMonth to two
// below a million.
func tariffTiers(add func(key, msg string), key string, tiers []gen.TariffTier) []byte {
	if len(tiers) == 0 || len(tiers) > tiersMax {
		add(key, fmt.Sprintf("A capacity component needs between 1 and %d tiers.", tiersMax))
		return nil
	}
	out := make([]gen.TariffTier, len(tiers))
	for k, tier := range tiers {
		tkey := fmt.Sprintf("%s[%d]", key, k)
		from, per := roundDecimals(tier.FromKw, 3), roundDecimals(tier.PerMonth, 2)
		switch {
		case from < 0:
			add(tkey+".fromKw", "A tier cannot start below 0 kW.")
		case from >= tierFromKwLimit:
			add(tkey+".fromKw", fmt.Sprintf("A tier must start below %d kW.", tierFromKwLimit))
		case k == 0 && from != 0:
			add(tkey+".fromKw", "The first tier must start at 0 kW.")
		case k > 0 && from <= out[k-1].FromKw:
			add(tkey+".fromKw", "Each tier must start above the one before it.")
		}
		switch {
		case per < 0:
			add(tkey+".perMonth", "A tier's monthly price cannot be negative.")
		case per >= tierPerMonthLimit:
			add(tkey+".perMonth", fmt.Sprintf("A tier's monthly price must be less than %d.", tierPerMonthLimit))
		}
		out[k] = gen.TariffTier{FromKw: from, PerMonth: per}
	}
	b, err := json.Marshal(out)
	if err != nil {
		add(key, "The tiers are not numbers.")
		return nil
	}
	return b
}

// checkVersionOverlaps refuses versions whose [validFrom, validTo) ranges
// share a day: in validFrom order, each version is checked against every
// earlier one, and the later is refused naming the other by its place in
// the body. Versions without a valid range are already refused and left
// out. The exclusion constraint tariff_versions_no_overlap backs this.
func checkVersionOverlaps(add func(key, msg string), versions []tariffVersionInput) {
	order := make([]int, 0, len(versions))
	for i, v := range versions {
		if v.ValidFrom.Valid && (!v.ValidTo.Valid || v.ValidTo.Time.After(v.ValidFrom.Time)) {
			order = append(order, i)
		}
	}
	sort.SliceStable(order, func(a, b int) bool {
		return versions[order[a]].ValidFrom.Time.Before(versions[order[b]].ValidFrom.Time)
	})
	for n, i := range order {
		for _, j := range order[:n] {
			earlier := versions[j]
			if !earlier.ValidTo.Valid || earlier.ValidTo.Time.After(versions[i].ValidFrom.Time) {
				add(fmt.Sprintf("versions[%d].validFrom", i), fmt.Sprintf(
					"The version overlaps versions[%d], valid from %s.", j, earlier.ValidFrom.Time.Format(time.DateOnly)))
				break
			}
		}
	}
}
