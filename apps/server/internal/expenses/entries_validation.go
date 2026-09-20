package expenses

import (
	"fmt"
	"math/big"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/expenses/gen"
)

// This file is everything an expense request is judged on before anything is
// asked of another module or of the database: the kinds and their fields, the
// lengths and the ranges of design §3.1, and the rule that a field the kind
// does not carry is refused on its own field rather than quietly ignored. A
// create and a full replace are held to exactly the same rules, which is why
// both reduce to one entryBody first.
//
// Every failure is collected, so one round trip reports every problem with a
// body rather than the first.

// The kinds of money line (decision X3). Travel claims and their per diem are
// a later delivery's: the kind is known here only so it can be refused with a
// message that says when it arrives, rather than as "not a kind".
const (
	kindOutlay  = "outlay"
	kindMileage = "mileage"
	kindPerDiem = "per_diem"
)

// entryKinds is what a request may carry today, in the order design §3.1 names
// them.
var entryKinds = []string{kindOutlay, kindMileage}

// Who paid an outlay (design §3.1). Mileage is always owed to the employee and
// carries neither value.
const (
	paidByEmployee = "employee"
	paidByCompany  = "company"
)

var paidByValues = []string{paidByEmployee, paidByCompany}

// The statuses an expense moves through (decision X4). Only draft is reachable
// in this delivery; the rest are read by the capabilities and by the list's
// status filter, so the vocabulary is written down once.
const (
	statusDraft     = "draft"
	statusSubmitted = "submitted"
	statusApproved  = "approved"
	statusRejected  = "rejected"
)

var entryStatuses = []string{statusDraft, statusSubmitted, statusApproved, statusRejected}

// The lengths and ranges of design §3.1 and Global Constraints. Every length
// is a count of characters, which is what the varchar(n) columns hold and what
// the refusals say.
const (
	descriptionMaxLength = 500
	supplierMaxLength    = 200
	placeMaxLength       = 200
	maxDistanceKm        = 9999.9
	maxPassengers        = 8
	distancePlaces       = 1
)

// entryBody is a create and a full replace reduced to one shape, so the rules
// below are written once and neither save can drift from the other. The update
// carries no userId: a replace never moves an expense to another person.
type entryBody struct {
	Kind          string
	EntryDate     openapi_types.Date
	Description   string
	CategoryID    *int32
	Supplier      *string
	PaidBy        *string
	Currency      *string
	GrossAmount   *float64
	VatAmount     *float64
	DistanceKm    *float64
	FromPlace     *string
	ToPlace       *string
	Passengers    *int32
	ProjectID     *int32
	BillingLineID *int32
	Billable      *bool
	MarkupPercent *float64
	BillRatePerKm *float64
	ClaimID       *int64
}

// bodyOfCreate is a create request as one entryBody.
func bodyOfCreate(b gen.ExpensesEntryRequest) entryBody {
	return entryBody{
		Kind: b.Kind, EntryDate: b.EntryDate, Description: b.Description,
		CategoryID: b.CategoryId, Supplier: b.Supplier, PaidBy: b.PaidBy, Currency: b.Currency,
		GrossAmount: b.GrossAmount, VatAmount: b.VatAmount,
		DistanceKm: b.DistanceKm, FromPlace: b.FromPlace, ToPlace: b.ToPlace, Passengers: b.Passengers,
		ProjectID: b.ProjectId, BillingLineID: b.BillingLineId, Billable: b.Billable,
		MarkupPercent: b.MarkupPercent, BillRatePerKm: b.BillRatePerKm, ClaimID: b.ClaimId,
	}
}

// bodyOfUpdate is a replace request as one entryBody.
func bodyOfUpdate(b gen.ExpensesEntryUpdateRequest) entryBody {
	return entryBody{
		Kind: b.Kind, EntryDate: b.EntryDate, Description: b.Description,
		CategoryID: b.CategoryId, Supplier: b.Supplier, PaidBy: b.PaidBy, Currency: b.Currency,
		GrossAmount: b.GrossAmount, VatAmount: b.VatAmount,
		DistanceKm: b.DistanceKm, FromPlace: b.FromPlace, ToPlace: b.ToPlace, Passengers: b.Passengers,
		ProjectID: b.ProjectId, BillingLineID: b.BillingLineId, Billable: b.Billable,
		MarkupPercent: b.MarkupPercent, BillRatePerKm: b.BillRatePerKm, ClaimID: b.ClaimId,
	}
}

// parsedEntry is one validated body, its amounts as exact decimals ready for
// money.go. What the server decides rather than the caller — a mileage line's
// rates and amount, an effective billable, a defaulted markup — is resolved
// afterwards, in entries.go, because it needs the rate table and the project.
type parsedEntry struct {
	Kind          string
	Date          time.Time
	Description   string
	CategoryID    *int32
	Supplier      *string
	PaidBy        *string
	Currency      string
	Gross         *big.Rat
	Vat           *big.Rat
	DistanceKm    *big.Rat
	FromPlace     *string
	ToPlace       *string
	Passengers    int16
	ProjectID     *int32
	LineID        *int32
	Billable      bool
	MarkupPercent *big.Rat
	BillRatePerKm *big.Rat
}

// parseEntry runs design §3.1's rules over a body. defaultCurrency is the
// installation's, which a mileage line takes and may not depart from;
// projectsOn says whether this installation has the projects module at all
// (decision X2), and when it does not, every project-shaped field is refused
// on its own field.
func parseEntry(body entryBody, defaultCurrency string, projectsOn bool) (parsedEntry, map[string][]string) {
	var errs map[string][]string
	add := func(field, msg string) {
		if msg != "" {
			errs = withFieldError(errs, field, msg)
		}
	}

	p := parsedEntry{Kind: strings.TrimSpace(body.Kind), Currency: defaultCurrency}
	switch {
	case p.Kind == kindPerDiem:
		add("kind", "Per diem belongs to a travel claim, which arrives in a later delivery")
	case !slices.Contains(entryKinds, p.Kind):
		add("kind", fmt.Sprintf("'%s' is not an expense kind; must be one of %s", body.Kind, strings.Join(entryKinds, ", ")))
	}
	if body.ClaimID != nil {
		add("claimId", "Travel claims arrive in a later delivery; an expense cannot belong to one yet")
	}

	if body.EntryDate.IsZero() {
		add("entryDate", "A date is required")
	} else {
		p.Date = utcDay(body.EntryDate.Time)
	}
	p.Description = strings.TrimSpace(body.Description)
	switch {
	case p.Description == "":
		add("description", "A description is required")
	case utf8.RuneCountInString(p.Description) > descriptionMaxLength:
		add("description", fmt.Sprintf("A description can be at most %d characters", descriptionMaxLength))
	}

	// Decision X2: without the projects module there is no project field,
	// billing line, billable flag, markup or customer rate anywhere.
	if !projectsOn {
		if body.ProjectID != nil {
			add("projectId", withoutProjects("booked on a project"))
		}
		if body.BillingLineID != nil {
			add("billingLineId", withoutProjects("booked on a billing line"))
		}
		if body.Billable != nil && *body.Billable {
			add("billable", withoutProjects("billed on to a customer"))
		}
		if body.MarkupPercent != nil {
			add("markupPercent", withoutProjects("given a markup"))
		}
		if body.BillRatePerKm != nil {
			add("billRatePerKm", withoutProjects("given a customer rate"))
		}
	}

	switch p.Kind {
	case kindOutlay:
		parseOutlay(&p, body, add)
	case kindMileage:
		parseMileage(&p, body, defaultCurrency, add)
	}
	parseProjectFields(&p, body, add)

	if len(errs) > 0 {
		return parsedEntry{}, errs
	}
	return p, nil
}

// parseOutlay is the outlay half of design §3.1: what somebody paid, to whom,
// in what currency, with the VAT they can read off the receipt. The mileage
// fields are refused rather than ignored, so a client that sent the wrong
// shape hears about it.
func parseOutlay(p *parsedEntry, body entryBody, add func(field, msg string)) {
	if body.CategoryID == nil {
		add("categoryId", "An outlay needs a category")
	} else {
		p.CategoryID = body.CategoryID
	}
	if msg := optionalText(body.Supplier, "A supplier", supplierMaxLength, &p.Supplier); msg != "" {
		add("supplier", msg)
	}
	switch paidBy := strings.TrimSpace(derefString(body.PaidBy)); {
	case paidBy == "":
		add("paidBy", "An outlay says who paid it")
	case !slices.Contains(paidByValues, paidBy):
		add("paidBy", fmt.Sprintf("'%s' is not a payer; must be one of %s", paidBy, strings.Join(paidByValues, ", ")))
	default:
		p.PaidBy = &paidBy
	}

	currency, msg := validateCurrency(derefString(body.Currency))
	add("currency", msg)
	if msg == "" {
		p.Currency = currency
	}

	if body.GrossAmount == nil {
		add("grossAmount", "An outlay needs an amount")
	} else if msg := validateAboveZero("An amount", *body.GrossAmount, maxMoney); msg != "" {
		add("grossAmount", msg)
	} else {
		p.Gross = ratFromFloat(*body.GrossAmount)
	}
	if body.VatAmount != nil {
		if msg := validateDecimal("VAT", *body.VatAmount, 0, maxMoney); msg != "" {
			add("vatAmount", msg)
		} else if body.GrossAmount != nil && *body.VatAmount > *body.GrossAmount {
			add("vatAmount", "VAT cannot be more than the amount it is part of")
		} else {
			p.Vat = ratFromFloat(*body.VatAmount)
		}
	}

	refuseMileageFields(body, kindOutlay, add)
}

// parseMileage is the mileage half of design §3.1: how far, from where to
// where and with how many passengers. What it is worth is not the caller's to
// say — the rate table prices it — so an amount, a VAT or a payer on it is
// refused.
func parseMileage(p *parsedEntry, body entryBody, defaultCurrency string, add func(field, msg string)) {
	switch {
	case body.DistanceKm == nil:
		add("distanceKm", "A mileage line needs a distance")
	case *body.DistanceKm <= 0:
		add("distanceKm", "A distance must be greater than zero")
	default:
		if msg := validateScaled("A distance", *body.DistanceKm, 0, maxDistanceKm, distancePlaces); msg != "" {
			add("distanceKm", msg)
		} else {
			p.DistanceKm = ratFromFloat(*body.DistanceKm)
		}
	}
	if msg := optionalText(body.FromPlace, "A place", placeMaxLength, &p.FromPlace); msg != "" {
		add("fromPlace", msg)
	}
	if msg := optionalText(body.ToPlace, "A place", placeMaxLength, &p.ToPlace); msg != "" {
		add("toPlace", msg)
	}
	if body.Passengers != nil {
		if *body.Passengers < 0 || *body.Passengers > maxPassengers {
			add("passengers", fmt.Sprintf("Passengers must be between 0 and %d", maxPassengers))
		} else {
			p.Passengers = int16(*body.Passengers)
		}
	}

	// Design §4: mileage is in the installation's own currency and nothing is
	// ever converted, so naming another one is a mistake rather than a wish.
	if body.Currency != nil && strings.TrimSpace(*body.Currency) != "" {
		currency, msg := validateCurrency(*body.Currency)
		switch {
		case msg != "":
			add("currency", msg)
		case currency != defaultCurrency:
			add("currency", fmt.Sprintf("A mileage line is in %s, this installation's currency, and nothing is converted", defaultCurrency))
		}
	}

	for field, given := range map[string]bool{
		"categoryId":  body.CategoryID != nil,
		"supplier":    body.Supplier != nil,
		"paidBy":      body.PaidBy != nil,
		"grossAmount": body.GrossAmount != nil,
		"vatAmount":   body.VatAmount != nil,
	} {
		if given {
			add(field, notOnKind(field, kindMileage))
		}
	}
}

// refuseMileageFields refuses the distance, the places and the passengers on a
// kind that does not drive anywhere.
func refuseMileageFields(body entryBody, kind string, add func(field, msg string)) {
	for field, given := range map[string]bool{
		"distanceKm": body.DistanceKm != nil,
		"fromPlace":  body.FromPlace != nil,
		"toPlace":    body.ToPlace != nil,
		"passengers": body.Passengers != nil,
	} {
		if given {
			add(field, notOnKind(field, kind))
		}
	}
}

// parseProjectFields is what may be said about the project an expense is
// booked on (decision X7): a billing line and a billable flag need a project,
// a markup belongs to a billable outlay and a customer rate per kilometre to
// billable mileage. Whether the person may actually book on the project is
// asked of the project directory afterwards, outside any transaction.
func parseProjectFields(p *parsedEntry, body entryBody, add func(field, msg string)) {
	p.ProjectID = body.ProjectID
	p.LineID = body.BillingLineID
	requested := body.Billable != nil && *body.Billable
	p.Billable = requested

	if body.BillingLineID != nil && body.ProjectID == nil {
		add("billingLineId", "A billing line needs the project it belongs to")
	}
	if requested && body.ProjectID == nil {
		add("billable", "Only an expense on a project can be billed on to a customer")
	}

	if body.MarkupPercent != nil {
		if msg := validateDecimal("A markup", *body.MarkupPercent, 0, maxMarkupPercent); msg != "" {
			add("markupPercent", msg)
		} else if p.Kind != kindOutlay || !requested {
			add("markupPercent", "A markup belongs to a billable outlay")
		} else {
			p.MarkupPercent = ratFromFloat(*body.MarkupPercent)
		}
	}
	if body.BillRatePerKm != nil {
		if msg := validateAboveZero("A customer rate", *body.BillRatePerKm, maxRateValue); msg != "" {
			add("billRatePerKm", msg)
		} else if p.Kind != kindMileage || !requested {
			add("billRatePerKm", "A rate per kilometre belongs to billable mileage")
		} else {
			p.BillRatePerKm = ratFromFloat(*body.BillRatePerKm)
		}
	}
}

// optionalText trims an optional string, refuses it when it is too long, and
// stores it as nil when it was left out or is blank — absent, never an empty
// string.
//
// The length is counted in characters, not bytes: that is what the message
// says, and what the varchar(n) column behind every caller of this holds. A
// byte count would refuse a Norwegian supplier name a third shorter than the
// limit with a message naming a number it is nowhere near.
func optionalText(raw *string, label string, maxLength int, into **string) string {
	if raw == nil {
		return ""
	}
	trimmed := strings.TrimSpace(*raw)
	if trimmed == "" {
		return ""
	}
	if utf8.RuneCountInString(trimmed) > maxLength {
		return fmt.Sprintf("%s can be at most %d characters", label, maxLength)
	}
	*into = &trimmed
	return ""
}

// derefString is a *string as a string, "" for nil.
func derefString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

// notOnKind is the message a field carries when the kind does not have it.
func notOnKind(field, kind string) string {
	return fmt.Sprintf("A %s line carries no %s", kind, field)
}

// withoutProjects is the message a project-shaped field carries in an
// installation with no projects module (decision X2).
func withoutProjects(what string) string {
	return "This installation has no projects module, so an expense cannot be " + what
}

// utcDay is a calendar date as the UTC day it names, whatever offset it
// arrived with — entry dates are calendar days, never instants.
func utcDay(d time.Time) time.Time {
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)
}

// lockedBeforeMessage is the entryDate message when a date falls before the
// period lock (design §4).
func lockedBeforeMessage(lock time.Time) string {
	return fmt.Sprintf("Expenses before %s are locked", lock.Format(time.DateOnly))
}

// cannotBookOnProject is the one projectId message, whatever the reason — the
// project does not exist, the person holds no role on it, or it is no longer
// open for work. Telling them apart would let a caller probe which project ids
// exist (design §8).
const cannotBookOnProject = "This project is not one the expense's owner can book on"
