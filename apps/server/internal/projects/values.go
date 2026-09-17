package projects

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/projects/gen"
)

// This file is design §4.1's validation, one function per rule: each takes
// the raw value and answers the normalized one plus the message to report,
// "" when the rule holds. validateProject runs them all and collects every
// failure into a map keyed by the camelCase JSON path, so a caller sees
// every problem with their body at once rather than one per round trip.

// The billing types a project may carry (design §2, D4). An internal project
// — one with no customer — must be non-billable: there is nobody to invoice.
const (
	billingTimeAndMaterials = "time-and-materials"
	billingFixedPrice       = "fixed-price"
	billingNonBillable      = "non-billable"
)

// projectCodePattern is D2's project code: upper-case letters and digits, no
// hyphen, so `<project>-<line>` always splits cleanly.
var projectCodePattern = regexp.MustCompile(`^[A-Z0-9]{2,20}$`)

// currencyPattern is an ISO-4217 alphabetic code's shape. The set of codes
// itself is not enforced: a deployment inventing a settlement currency is
// not this module's business (D13 only requires that there is exactly one
// per project).
var currencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)

// validateProjectCode is D2's code rule: trimmed and upper-cased before it
// is validated or stored, which also makes uniqueness case-insensitive under
// the plain unique index ux_projects_code.
func validateProjectCode(raw string) (string, string) {
	code := strings.ToUpper(strings.TrimSpace(raw))
	if code == "" {
		return "", "A project code cannot be null or empty"
	}
	if !projectCodePattern.MatchString(code) {
		return "", fmt.Sprintf("A project code must be 2 to 20 upper-case letters and digits, but was '%s'", raw)
	}
	return code, ""
}

// codeTaken is the message the `code` field carries when the unique index
// refuses the insert. It is a validation failure, not a conflict: the caller
// picked a name that is already spoken for, and the fix is to pick another.
func codeTaken(code string) string {
	return fmt.Sprintf("A project code must be unique, and '%s' is already in use", code)
}

// validateProjectName is the project name rule: non-blank, at most 200
// characters (the column's width), trimmed but case-preserved.
func validateProjectName(raw string) (string, string) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", "A project name cannot be null or empty"
	}
	if n := utf8.RuneCountInString(name); n > 200 {
		return "", fmt.Sprintf("A project name cannot be longer than 200 characters, the given value was %d characters", n)
	}
	return name, ""
}

// validateDescription is the description rule: optional, at most 4000
// characters. A blank description is stored as no description at all, so
// "  " and an absent field mean the same thing.
func validateDescription(raw *string) (*string, string) {
	if raw == nil {
		return nil, ""
	}
	trimmed := strings.TrimSpace(*raw)
	if trimmed == "" {
		return nil, ""
	}
	if n := utf8.RuneCountInString(trimmed); n > 4000 {
		return nil, fmt.Sprintf("A project description cannot be longer than 4000 characters, the given value was %d characters", n)
	}
	return &trimmed, ""
}

// validateBillingType is D4's billing type rule: one of the three, exactly
// as written — unlike the customer statuses this module's neighbours accept
// case-insensitively, these strings are also what other modules will key on.
func validateBillingType(raw string) (string, string) {
	switch strings.TrimSpace(raw) {
	case "":
		return "", "A billing type cannot be null or empty"
	case billingTimeAndMaterials, billingFixedPrice, billingNonBillable:
		return strings.TrimSpace(raw), ""
	default:
		return "", fmt.Sprintf("A billing type must be one of '%s', '%s' or '%s', but was '%s'",
			billingTimeAndMaterials, billingFixedPrice, billingNonBillable, raw)
	}
}

// validateInternalBillingType is D4's other half: a project with no customer
// has nobody to invoice, so it must be non-billable. A customer project may
// be any billing type, non-billable included.
func validateInternalBillingType(customerID *int32, billingType string) string {
	if customerID != nil || billingType == billingNonBillable {
		return ""
	}
	return fmt.Sprintf("A project with no customer is internal and must be '%s', but was '%s'", billingNonBillable, billingType)
}

// validateCurrency is D13's currency shape: upper-cased, three letters.
// Whether one is *required* is validateCurrencyRequired's rule, not this
// one.
func validateCurrency(raw *string) (*string, string) {
	if raw == nil {
		return nil, ""
	}
	currency := strings.ToUpper(strings.TrimSpace(*raw))
	if currency == "" {
		return nil, ""
	}
	if !currencyPattern.MatchString(currency) {
		return nil, fmt.Sprintf("A currency must be a three-letter ISO 4217 code, but was '%s'", *raw)
	}
	return &currency, ""
}

// validateCurrencyRequired is D13: one currency per project, required as
// soon as any amount is set, because an amount without one cannot be
// resolved by whoever bills it later.
func validateCurrencyRequired(currency *string, amounts ...*float64) string {
	if currency != nil {
		return ""
	}
	for _, a := range amounts {
		if a != nil {
			return "A currency is required as soon as an amount is set"
		}
	}
	return ""
}

// validateFixedPriceAmount is design §4.1's fixed-price rule, both ways
// round: a fixed-price project needs an amount greater than zero, and any
// other billing type must not carry one at all — a stored fixed price on a
// time-and-materials project would be a number nobody ever reads.
func validateFixedPriceAmount(billingType string, amount *float64) string {
	if billingType == billingFixedPrice {
		switch {
		case amount == nil:
			return fmt.Sprintf("A '%s' project must have a fixed price amount", billingFixedPrice)
		case *amount <= 0:
			return "A fixed price amount must be greater than zero"
		default:
			return ""
		}
	}
	if amount != nil {
		return fmt.Sprintf("A fixed price amount is only allowed on a '%s' project", billingFixedPrice)
	}
	return ""
}

// validatePositiveAmount is the shared rule for the two optional budgets:
// set or absent, never zero or negative. label names the quantity in the
// message ("Budget hours", "A budget amount").
func validatePositiveAmount(label string, v *float64) string {
	if v == nil || *v > 0 {
		return ""
	}
	return label + " must be greater than zero"
}

// validateDateOrder is design §4.1's date rule: a project cannot end before
// it starts. Either date alone is fine — a project with an end and no start
// is one whose beginning nobody recorded.
func validateDateOrder(start, end *openapi_types.Date) string {
	if start == nil || end == nil || !end.Before(start.Time) {
		return ""
	}
	return "An end date cannot be before the start date"
}

// parsedProject is one validated create body, in the shape the insert wants:
// every value normalized, every decimal already a pgtype.Numeric. CustomerName
// rides along because validation has just resolved it through the directory
// and the response needs it.
type parsedProject struct {
	Code             string
	Name             string
	Description      *string
	CustomerID       *int32
	CustomerName     *string
	StartDate        pgtype.Date
	EndDate          pgtype.Date
	BillingType      string
	Currency         *string
	FixedPriceAmount pgtype.Numeric
	BudgetHours      pgtype.Numeric
	BudgetAmount     pgtype.Numeric
}

// validateProject runs every §4.1 rule over a create body and returns the
// insert-ready project, the field errors (nil when there are none), and an
// error for an infrastructure failure — a directory lookup that failed, or a
// number Postgres could not store — which is never the caller's fault and so
// is never a field error.
//
// Every rule runs regardless of the others, so one round trip reports every
// problem. The rules that depend on another field's *validated* value
// (internal ⇒ non-billable, fixed price, currency) run against what came out
// of that field's own rule, and are skipped when that field failed: a second
// message derived from a value already rejected only adds noise.
func (s *server) validateProject(ctx context.Context, body gen.ProjectCreateRequest) (parsedProject, map[string][]string, error) {
	errs := map[string][]string{}
	add := func(field, msg string) {
		if msg != "" {
			errs[field] = append(errs[field], msg)
		}
	}

	code, msg := validateProjectCode(body.Code)
	add("code", msg)
	name, msg := validateProjectName(body.Name)
	add("name", msg)
	description, msg := validateDescription(body.Description)
	add("description", msg)

	// An archived customer resolves and is allowed: a project can outlive
	// the relationship that started it (§4.1).
	var customerName *string
	if body.CustomerId != nil {
		customer, err := s.deps.Directory.Customer(ctx, *body.CustomerId)
		if err != nil {
			return parsedProject{}, nil, fmt.Errorf("projects: look up customer: %w", err)
		}
		if customer == nil {
			add("customerId", fmt.Sprintf("Customer %d does not exist", *body.CustomerId))
		} else {
			customerName = &customer.Name
		}
	}

	billingType, msg := validateBillingType(body.BillingType)
	add("billingType", msg)
	if billingType != "" {
		add("billingType", validateInternalBillingType(body.CustomerId, billingType))
		add("fixedPriceAmount", validateFixedPriceAmount(billingType, body.FixedPriceAmount))
	}

	add("budgetHours", validatePositiveAmount("Budget hours", body.BudgetHours))
	add("budgetAmount", validatePositiveAmount("A budget amount", body.BudgetAmount))

	currency, msg := validateCurrency(body.Currency)
	add("currency", msg)
	if msg == "" {
		add("currency", validateCurrencyRequired(currency, body.FixedPriceAmount, body.BudgetAmount))
	}

	add("endDate", validateDateOrder(body.StartDate, body.EndDate))

	if len(errs) > 0 {
		return parsedProject{}, errs, nil
	}

	fixedPrice, err := numericFromFloatPtr(body.FixedPriceAmount)
	if err != nil {
		return parsedProject{}, nil, err
	}
	budgetHours, err := numericFromFloatPtr(body.BudgetHours)
	if err != nil {
		return parsedProject{}, nil, err
	}
	budgetAmount, err := numericFromFloatPtr(body.BudgetAmount)
	if err != nil {
		return parsedProject{}, nil, err
	}

	return parsedProject{
		Code:             code,
		Name:             name,
		Description:      description,
		CustomerID:       body.CustomerId,
		CustomerName:     customerName,
		StartDate:        dateToPgtype(body.StartDate),
		EndDate:          dateToPgtype(body.EndDate),
		BillingType:      billingType,
		Currency:         currency,
		FixedPriceAmount: fixedPrice,
		BudgetHours:      budgetHours,
		BudgetAmount:     budgetAmount,
	}, nil, nil
}

// dateToPgtype converts the contract's optional date into the nullable SQL
// date column, an invalid (SQL NULL) pgtype.Date for an absent one.
func dateToPgtype(d *openapi_types.Date) pgtype.Date {
	if d == nil {
		return pgtype.Date{}
	}
	return pgtype.Date{Time: d.Time, Valid: true}
}

// dateFromPgtype is dateToPgtype's inverse: a NULL date column becomes an
// absent contract date, never a zero one.
func dateFromPgtype(d pgtype.Date) *openapi_types.Date {
	if !d.Valid {
		return nil
	}
	return &openapi_types.Date{Time: d.Time}
}

// numericFromFloat converts a JSON number into a numeric column value
// through its shortest round-tripping decimal text, so the column's own
// scale does the rounding rather than Go — the same conversion products
// makes for its prices.
//
// Scan's error is returned rather than discarded: it can only fire for an
// infinity, which encoding/json refuses to decode, so nothing reachable from
// a request produces one today. Discarding it would store a silent SQL NULL
// for a number the caller actually sent.
func numericFromFloat(v float64) (pgtype.Numeric, error) {
	var n pgtype.Numeric
	if err := n.Scan(strconv.FormatFloat(v, 'f', -1, 64)); err != nil {
		return pgtype.Numeric{}, fmt.Errorf("projects: %v is not a storable decimal: %w", v, err)
	}
	return n, nil
}

// numericFromFloatPtr is numericFromFloat for an optional field: nil stays
// an invalid (SQL NULL) pgtype.Numeric, which is the column's own NULL and
// never an error.
func numericFromFloatPtr(v *float64) (pgtype.Numeric, error) {
	if v == nil {
		return pgtype.Numeric{}, nil
	}
	return numericFromFloat(*v)
}

// floatPtrFromNumeric reads an optional numeric column back onto the wire as
// a JSON number, nil for a SQL NULL — never 0, which is a budget somebody
// actually set to nothing.
func floatPtrFromNumeric(n pgtype.Numeric) *float64 {
	if !n.Valid {
		return nil
	}
	f, err := n.Float64Value()
	if err != nil || !f.Valid {
		return nil
	}
	return &f.Float64
}
