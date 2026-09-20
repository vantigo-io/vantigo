package expenses

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"math/big"

	"github.com/vantigo-io/vantigo/server/internal/expenses/gen"
)

// This file is everything a travel claim's own body is judged on, before
// anything is asked of another module or of the database (design §3.6). A
// create and a full replace are held to exactly the same rules, which is why
// both reduce to one claimBody first, exactly as an expense's two writes do.
//
// Every failure is collected, so one round trip reports every problem with a
// body rather than the first.

// The lengths and bounds of design §3.6 and Global Constraints.
const (
	purposeMaxLength     = 200
	destinationMaxLength = 200

	// maxTripDays is the longest trip a claim may cover. A year and a day
	// covers a leap year's worth of secondment; past that, a "trip" is
	// somebody's whole posting and a per diem suggestion over it would be a
	// four-figure list of days.
	maxTripDays = 366

	// maxClaimLines bounds what one claim holds. It is decided under the
	// claim's own row lock, so two lines racing for the last slot cannot both
	// take it.
	maxClaimLines = 200
)

// claimBody is a create and a full replace reduced to one shape, so the rules
// below are written once and neither save can drift from the other. The update
// carries no userId: a replace never moves a claim to another person.
type claimBody struct {
	Purpose        string
	Destination    *string
	Abroad         *bool
	AbroadDayRate  *float64
	AbroadCurrency *string
	DepartureAt    time.Time
	ReturnAt       time.Time
	ProjectID      *int32
}

// claimBodyOfCreate is a create request as one claimBody.
func claimBodyOfCreate(b gen.ExpensesClaimRequest) claimBody {
	return claimBody{
		Purpose: b.Purpose, Destination: b.Destination,
		Abroad: b.Abroad, AbroadDayRate: b.AbroadDayRate, AbroadCurrency: b.AbroadCurrency,
		DepartureAt: b.DepartureAt, ReturnAt: b.ReturnAt, ProjectID: b.ProjectId,
	}
}

// claimBodyOfUpdate is a replace request as one claimBody.
func claimBodyOfUpdate(b gen.ExpensesClaimUpdateRequest) claimBody {
	return claimBody{
		Purpose: b.Purpose, Destination: b.Destination,
		Abroad: b.Abroad, AbroadDayRate: b.AbroadDayRate, AbroadCurrency: b.AbroadCurrency,
		DepartureAt: b.DepartureAt, ReturnAt: b.ReturnAt, ProjectID: b.ProjectId,
	}
}

// parsedClaim is one validated body, its day rate an exact decimal ready for
// the column. Whether the owner may book on the project is asked of the project
// directory afterwards, outside any transaction.
type parsedClaim struct {
	Purpose        string
	Destination    *string
	Abroad         bool
	AbroadDayRate  *big.Rat
	AbroadCurrency *string
	DepartureAt    time.Time
	ReturnAt       time.Time
	ProjectID      *int32
}

// parseClaim runs design §3.6's rules over a body. projectsOn says whether this
// installation has the projects module at all (decision X2); without it a
// project is refused on its own field, exactly as it is on an expense.
func parseClaim(body claimBody, projectsOn bool) (parsedClaim, map[string][]string) {
	var errs map[string][]string
	add := func(field, msg string) {
		if msg != "" {
			errs = withFieldError(errs, field, msg)
		}
	}

	p := parsedClaim{ProjectID: body.ProjectID}
	p.Purpose = strings.TrimSpace(body.Purpose)
	switch {
	case p.Purpose == "":
		add("purpose", "A purpose is required")
	case utf8.RuneCountInString(p.Purpose) > purposeMaxLength:
		add("purpose", fmt.Sprintf("A purpose can be at most %d characters", purposeMaxLength))
	}
	if msg := optionalText(body.Destination, "A destination", destinationMaxLength, &p.Destination); msg != "" {
		add("destination", msg)
	}

	// The two instants are stored and compared as they were entered (Global
	// Constraints): the per diem day boundaries are 24-hour periods from the
	// departure, so a claim that silently moved either would move every day of
	// the trip with it.
	switch {
	case body.DepartureAt.IsZero():
		add("departureAt", "A departure time is required")
	case body.ReturnAt.IsZero():
		add("returnAt", "A return time is required")
	case !body.ReturnAt.After(body.DepartureAt):
		add("returnAt", "The return must be after the departure")
	case body.ReturnAt.Sub(body.DepartureAt) > maxTripDays*24*time.Hour:
		add("returnAt", fmt.Sprintf("A trip can be at most %d days", maxTripDays))
	}
	p.DepartureAt, p.ReturnAt = body.DepartureAt, body.ReturnAt

	parseAbroad(&p, body, add)

	// Decision X2: without the projects module there is no project field
	// anywhere, and a stored one is carried through by the save rather than
	// judged here.
	if !projectsOn && body.ProjectID != nil {
		add("projectId", withoutProjects("booked on a project"))
	}

	if len(errs) > 0 {
		return parsedClaim{}, errs
	}
	return p, nil
}

// parseAbroad is design §4's rule for a trip out of the country: it is paid at
// the claim's own day rate, in the currency the traveller was paid in, rather
// than from the dated per diem table — so the two travel together. Naming
// either on a domestic trip is refused rather than stored and never used.
func parseAbroad(p *parsedClaim, body claimBody, add func(field, msg string)) {
	p.Abroad = body.Abroad != nil && *body.Abroad
	if !p.Abroad {
		if body.AbroadDayRate != nil {
			add("abroadDayRate", "A day rate of its own belongs to a claim abroad")
		}
		if body.AbroadCurrency != nil {
			add("abroadCurrency", "A currency of its own belongs to a claim abroad")
		}
		return
	}
	if body.AbroadDayRate == nil {
		add("abroadDayRate", "A claim abroad needs the day rate it is paid at")
	} else if msg := validateAboveZero("A day rate", *body.AbroadDayRate, maxRateValue); msg != "" {
		add("abroadDayRate", msg)
	} else {
		p.AbroadDayRate = ratFromFloat(*body.AbroadDayRate)
	}
	if body.AbroadCurrency == nil {
		add("abroadCurrency", "A claim abroad needs the currency its day rate is in")
	} else if currency, msg := validateCurrency(*body.AbroadCurrency); msg != "" {
		add("abroadCurrency", msg)
	} else {
		p.AbroadCurrency = &currency
	}
}
