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

// The kinds of money line (decision X3, and supplier invoices design D1). The
// per diem day is the one that exists only inside a travel claim; the supplier
// invoice the one that never does and always sits on a project; the outlay
// and mileage stand alone or inside one.
const (
	kindOutlay          = "outlay"
	kindMileage         = "mileage"
	kindPerDiem         = "per_diem"
	kindSupplierInvoice = "supplier_invoice"
)

// entryKinds is every kind a request may carry, in the order design §3.1 names
// them and the supplier invoice after them. It is also what the list's kind
// filter accepts, so the two can never drift.
var entryKinds = []string{kindOutlay, kindMileage, kindPerDiem, kindSupplierInvoice}

// marksUp reports whether a billable line of this kind is priced as its net
// plus a markup: an outlay, and a supplier invoice, which borrows the outlay's
// money whole (supplier invoices design D3). Mileage bills per kilometre and a
// per diem day bills nothing.
func marksUp(kind string) bool { return kind == kindOutlay || kind == kindSupplierInvoice }

// takesReceipts reports whether a line of this kind carries documents: an
// outlay its receipts, a supplier invoice the supplier's invoice itself
// (design D2). Mileage and a per diem day carry none.
func takesReceipts(kind string) bool { return kind == kindOutlay || kind == kindSupplierInvoice }

// The supplier invoice's own limit and refusals (supplier invoices design D1).
// One project sentence serves both "no project named" and "no projects module
// at all", because the fact the caller can act on is the same: this kind lives
// on a project and nowhere else.
const (
	invoiceNumberMaxLength       = 100
	supplierInvoiceNeedsProject  = "A supplier invoice is booked on a project"
	supplierInvoiceNotInClaim    = "A supplier invoice is not a travel claim line"
	supplierInvoicePaidByCompany = "A supplier invoice is paid by the company"
)

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
	Kind             string
	EntryDate        openapi_types.Date
	Description      string
	PerDiemType      *string
	BreakfastCovered *bool
	LunchCovered     *bool
	DinnerCovered    *bool

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

	// InvoiceNumber and DueDate are the supplier invoice's own two fields
	// (supplier invoices design D1), refused on every other kind.
	InvoiceNumber *string
	DueDate       *openapi_types.Date
}

// bodyOfCreate is a create request as one entryBody.
func bodyOfCreate(b gen.ExpensesEntryRequest) entryBody {
	return entryBody{
		Kind: b.Kind, EntryDate: b.EntryDate, Description: derefString(b.Description),
		PerDiemType:      b.PerDiemType,
		BreakfastCovered: b.BreakfastCovered, LunchCovered: b.LunchCovered, DinnerCovered: b.DinnerCovered,
		CategoryID: b.CategoryId, Supplier: b.Supplier, PaidBy: b.PaidBy, Currency: b.Currency,
		GrossAmount: b.GrossAmount, VatAmount: b.VatAmount,
		DistanceKm: b.DistanceKm, FromPlace: b.FromPlace, ToPlace: b.ToPlace, Passengers: b.Passengers,
		ProjectID: b.ProjectId, BillingLineID: b.BillingLineId, Billable: b.Billable,
		MarkupPercent: b.MarkupPercent, BillRatePerKm: b.BillRatePerKm, ClaimID: b.ClaimId,
		InvoiceNumber: b.InvoiceNumber, DueDate: b.DueDate,
	}
}

// bodyOfUpdate is a replace request as one entryBody.
func bodyOfUpdate(b gen.ExpensesEntryUpdateRequest) entryBody {
	return entryBody{
		Kind: b.Kind, EntryDate: b.EntryDate, Description: derefString(b.Description),
		PerDiemType:      b.PerDiemType,
		BreakfastCovered: b.BreakfastCovered, LunchCovered: b.LunchCovered, DinnerCovered: b.DinnerCovered,
		CategoryID: b.CategoryId, Supplier: b.Supplier, PaidBy: b.PaidBy, Currency: b.Currency,
		GrossAmount: b.GrossAmount, VatAmount: b.VatAmount,
		DistanceKm: b.DistanceKm, FromPlace: b.FromPlace, ToPlace: b.ToPlace, Passengers: b.Passengers,
		ProjectID: b.ProjectId, BillingLineID: b.BillingLineId, Billable: b.Billable,
		MarkupPercent: b.MarkupPercent, BillRatePerKm: b.BillRatePerKm, ClaimID: b.ClaimId,
		InvoiceNumber: b.InvoiceNumber, DueDate: b.DueDate,
	}
}

// parsedEntry is one validated body, its amounts as exact decimals ready for
// money.go. What the server decides rather than the caller — a mileage line's
// rates and amount, an effective billable, a defaulted markup — is resolved
// afterwards, in entries.go, because it needs the rate table and the project.
type parsedEntry struct {
	Kind        string
	ClaimID     *int64
	Date        time.Time
	Description string

	// PerDiemType and Meals are the per diem day's own two answers: which kind
	// of day it was, and which meals somebody else paid for. Both are nil and
	// zero on every other kind.
	PerDiemType *string
	Meals       perDiemMeals

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

	// InvoiceNumber and DueDate are a supplier invoice's, nil on every other
	// kind. DueDate is a calendar date at UTC midnight, like Date.
	InvoiceNumber *string
	DueDate       *time.Time
}

// parseEntry runs design §3.1's rules over a body. defaultCurrency is the
// installation's, which a mileage line takes and may not depart from;
// projectsOn says whether this installation has the projects module at all
// (decision X2), and when it does not, every project-shaped field is refused
// on its own field. inClaim says the expense is a line of a travel claim, which
// only relaxes what may be left out here: the claim's project is the line's, so
// a billing line or a billable flag need not repeat it. Whether the caller may
// put a line in that claim at all is asked afterwards, outside any transaction
// (resolveClaimLine). keptSupplierInvoice says the row being replaced is
// already a supplier invoice, which is what keeps one editable in an
// installation that has since lost the projects module (decision X2).
func parseEntry(body entryBody, defaultCurrency string, projectsOn, inClaim, keptSupplierInvoice bool) (parsedEntry, map[string][]string) {
	var errs map[string][]string
	add := func(field, msg string) {
		if msg != "" {
			errs = withFieldError(errs, field, msg)
		}
	}

	p := parsedEntry{Kind: strings.TrimSpace(body.Kind), Currency: defaultCurrency, ClaimID: body.ClaimID}
	switch {
	case p.Kind == kindPerDiem && !inClaim:
		// The per diem day is the one kind that only ever exists inside a
		// travel claim: a trip is what gives a day its rate, its currency and
		// the window its date has to fall in, and there is no such thing as a
		// day of no trip.
		add("kind", perDiemNeedsClaim)
	case !slices.Contains(entryKinds, p.Kind):
		add("kind", fmt.Sprintf("'%s' is not an expense kind; must be one of %s", body.Kind, strings.Join(entryKinds, ", ")))
	}
	if p.Kind != kindPerDiem && body.PerDiemType != nil {
		add("perDiemType", notOnKind("perDiemType", p.Kind))
	}
	if p.Kind != kindPerDiem {
		for field, given := range map[string]bool{
			"breakfastCovered": body.BreakfastCovered != nil,
			"lunchCovered":     body.LunchCovered != nil,
			"dinnerCovered":    body.DinnerCovered != nil,
		} {
			if given {
				add(field, notOnKind(field, p.Kind))
			}
		}
	}

	if p.Kind != kindSupplierInvoice {
		if body.InvoiceNumber != nil {
			add("invoiceNumber", notOnKind("invoiceNumber", p.Kind))
		}
		if body.DueDate != nil {
			add("dueDate", notOnKind("dueDate", p.Kind))
		}
	}

	if body.EntryDate.IsZero() {
		add("entryDate", "A date is required")
	} else {
		p.Date = utcDay(body.EntryDate.Time)
	}
	p.Description = strings.TrimSpace(body.Description)
	switch {
	case p.Description == "" && p.Kind != kindPerDiem:
		// A per diem day may have none: what it is is its perDiemType, which
		// every reader already has, and a name the *server* invented would be
		// stored in one language for ever. The column takes the empty string
		// and the client names the day in its reader's own words.
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
	case kindSupplierInvoice:
		parseSupplierInvoice(&p, body, projectsOn, inClaim, keptSupplierInvoice, add)
	case kindOutlay:
		parseOutlay(&p, body, add)
	case kindMileage:
		parseMileage(&p, body, defaultCurrency, add)
	case kindPerDiem:
		parsePerDiem(&p, body, add)
	}
	parseProjectFields(&p, body, inClaim, add)

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
	parseAmounts(p, body, "An outlay", add)
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
	refuseMileageFields(body, kindOutlay, add)
}

// parseAmounts is the money an outlay and a supplier invoice share (supplier
// invoices design D1): a category, a currency in any ISO code, a gross above
// zero and an optional VAT from zero to that gross. what names the kind in the
// two sentences that say something is missing.
func parseAmounts(p *parsedEntry, body entryBody, what string, add func(field, msg string)) {
	if body.CategoryID == nil {
		add("categoryId", what+" needs a category")
	} else {
		p.CategoryID = body.CategoryID
	}

	currency, msg := validateCurrency(derefString(body.Currency))
	add("currency", msg)
	if msg == "" {
		p.Currency = currency
	}

	if body.GrossAmount == nil {
		add("grossAmount", what+" needs an amount")
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
}

// parseSupplierInvoice is the supplier invoice (supplier invoices design D1):
// the outlay's money, a supplier and the supplier's invoice number that are
// both required, an optional due date on or after the entry date — which on
// this kind *is* the invoice date, the one the period lock judges — and a
// payer that is always the company. It never sits in a travel claim and
// always sits on a project, and in an installation without the projects
// module there is no such kind at all. Whether the recorder may book it on
// the project is asked afterwards, of the directory, outside any transaction
// (checkSupplierInvoiceProject).
func parseSupplierInvoice(p *parsedEntry, body entryBody, projectsOn, inClaim, kept bool, add func(field, msg string)) {
	switch {
	case !projectsOn && kept:
		// Decision X2 for this kind: an installation that has lost the
		// projects module keeps what was booked. A supplier invoice already
		// recorded stays editable, its project columns carried through from
		// the row (prepared.CarryProject) — refusing the kind here would
		// leave its owner able to submit or delete it and nothing else.
	case !projectsOn:
		add("kind", supplierInvoiceNeedsProject)
	case body.ProjectID == nil:
		add("projectId", supplierInvoiceNeedsProject)
	}
	if inClaim {
		add("claimId", supplierInvoiceNotInClaim)
	}
	parseAmounts(p, body, "A supplier invoice", add)
	if msg := optionalText(body.Supplier, "A supplier", supplierMaxLength, &p.Supplier); msg != "" {
		add("supplier", msg)
	} else if p.Supplier == nil {
		add("supplier", "A supplier invoice names its supplier")
	}
	if msg := optionalText(body.InvoiceNumber, "An invoice number", invoiceNumberMaxLength, &p.InvoiceNumber); msg != "" {
		add("invoiceNumber", msg)
	} else if p.InvoiceNumber == nil {
		add("invoiceNumber", "A supplier invoice carries the supplier's invoice number")
	}
	if body.DueDate != nil {
		due := utcDay(body.DueDate.Time)
		if !p.Date.IsZero() && due.Before(p.Date) {
			add("dueDate", "The due date cannot be before the invoice date")
		} else {
			p.DueDate = &due
		}
	}
	// The company pays the supplier, so the payer is not the caller's to say:
	// left out or 'company' it is stored as company-paid, which is what the
	// owes-the-employee rule and every payroll surface read.
	switch paidBy := strings.TrimSpace(derefString(body.PaidBy)); paidBy {
	case "", paidByCompany:
		company := paidByCompany
		p.PaidBy = &company
	case paidByEmployee:
		add("paidBy", supplierInvoicePaidByCompany)
	default:
		add("paidBy", fmt.Sprintf("'%s' is not a payer; a supplier invoice is paid by the company", paidBy))
	}
	refuseMileageFields(body, kindSupplierInvoice, add)
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
func parseProjectFields(p *parsedEntry, body entryBody, inClaim bool, add func(field, msg string)) {
	p.ProjectID = body.ProjectID
	p.LineID = body.BillingLineID
	requested := body.Billable != nil && *body.Billable
	p.Billable = requested

	// A line inside a claim inherits the claim's project, so it need not name
	// one; claimLineRules reports the same two refusals once the claim has
	// answered whether it is on a project at all.
	if body.BillingLineID != nil && body.ProjectID == nil && !inClaim {
		add("billingLineId", "A billing line needs the project it belongs to")
	}
	if requested && body.ProjectID == nil && !inClaim {
		add("billable", "Only an expense on a project can be billed on to a customer")
	}

	if body.MarkupPercent != nil {
		if msg := validateDecimal("A markup", *body.MarkupPercent, 0, maxMarkupPercent); msg != "" {
			add("markupPercent", msg)
		} else if !marksUp(p.Kind) || !requested {
			add("markupPercent", "A markup belongs to a billable outlay or supplier invoice")
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
	article := "A"
	if kind == kindOutlay {
		article = "An"
	}
	return fmt.Sprintf("%s %s line carries no %s", article, kindLabel(kind), field)
}

// kindLabel names a kind the way a refusal says it out loud. The per diem
// day's and the supplier invoice's stored values are not already words.
func kindLabel(kind string) string {
	switch kind {
	case kindPerDiem:
		return "per diem"
	case kindSupplierInvoice:
		return "supplier invoice"
	}
	return kind
}

// withoutProjects is the message a project-shaped field carries in an
// installation with no projects module (decision X2).
func withoutProjects(what string) string {
	return "This installation has no projects module, so an expense cannot be " + what
}

// utcDay is a **calendar date** as the day it names: an entryDate, a lock date,
// a from/to filter — values that arrive on the wire as `2026-03-10` and mean
// that day wherever the reader is. It normalises to UTC midnight, which is what
// a `date` column holds and what every comparison in this module is made in.
//
// It is not for instants. A travel claim's departure and return are
// timestamptz, and the day one of those falls on depends on *where you are
// standing* — that is businessDay's question, and it has an answer only because
// the installation says which zone it keeps its calendar in.
func utcDay(d time.Time) time.Time {
	d = d.UTC()
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)
}

// businessDay is the calendar day an instant falls on **in the installation's
// own time zone** (`expenses.settings.time_zone`), rendered as the UTC midnight
// every date in this module is compared at.
//
// It is the module's one derivation of "which day is this trip's", and the
// reason there is a setting at all: a claim stores instants, Postgres keeps the
// instant and throws the typed offset away, and UTC is not what a Norwegian
// company's calendar means. A departure at 00:30 on 1 July in Oslo is a trip
// that departed on **1 July** — the day the period lock judges, the day
// GET /claims' from/to filter matches, the first day a per diem line may fall
// on and the first day the suggestion proposes. Under the UTC rule it was 30
// June: one day early for an hour or two a day, and wrong at exactly the month
// boundaries a period lock and a payroll month are about.
//
// The same expression runs in SQL — `(departure_at AT TIME ZONE @time_zone)::date`
// — from the same stored name, so Go and the database can never disagree about
// which day a claim is on.
func businessDay(t time.Time, loc *time.Location) time.Time {
	t = t.In(loc)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
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
