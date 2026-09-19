package projects

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/projects/gen"
	"github.com/vantigo-io/vantigo/server/internal/projects/store"
)

// This file is design §4.1's line rules, written the way values.go writes a
// project's: one function per rule, each answering the normalized value and
// the message to report, "" when the rule holds. They live beside the line
// operations rather than in values.go because values.go is already the whole
// of a project's own validation, and one file holding both would be read by
// nobody looking for either.

// The three pricing rules a line may carry (D9). A line is "variant + pricing
// rule": the variant's own list price, a negotiated amount in the project's
// currency, or a percentage off the list price. There is no hand-typed rate,
// and Projects never resolves any of these into money — Invoices does.
const (
	pricingList     = "list"
	pricingFixed    = "fixed"
	pricingDiscount = "discount"
)

// pricingModes is the enumeration in the order the contract and the design
// write it, so a message built from it reads the way the documentation does.
var pricingModes = []string{pricingList, pricingFixed, pricingDiscount}

// lineCodePattern is D2's line code: upper-case letters and digits, no
// hyphen, so the trackable code `<project>-<line>` always splits cleanly. It
// is shorter than a project's — a line code is read inside a project, where
// "PM" is already unambiguous.
var lineCodePattern = regexp.MustCompile(`^[A-Z0-9]{1,10}$`)

// validateLineCode is that rule: trimmed and upper-cased before it is
// validated or stored, which also makes uniqueness case-insensitive under
// ux_billing_lines_project_id_code.
func validateLineCode(raw string) (string, string) {
	code := strings.ToUpper(strings.TrimSpace(raw))
	if code == "" {
		return "", "A line code cannot be null or empty"
	}
	if !lineCodePattern.MatchString(code) {
		return "", fmt.Sprintf("A line code must be 1 to 10 upper-case letters and digits, but was '%s'", raw)
	}
	return code, ""
}

// lineCodeTaken is the message the `code` field carries when the unique index
// refuses the write. A line code is unique inside its project and nowhere
// else: two projects both billing 'PM' is the normal case, and it is the
// trackable code that is unique across them.
func lineCodeTaken(code string) string {
	return fmt.Sprintf("A line code must be unique within the project, and '%s' is already in use", code)
}

// validatePricingMode is the pricing rule's own name: required, one of the
// three, exactly as written — the strings are what Invoices will key on, so
// they are not matched case-insensitively.
func validatePricingMode(raw string) (string, string) {
	mode := strings.TrimSpace(raw)
	if mode == "" {
		return "", "A pricing mode cannot be null or empty"
	}
	for _, m := range pricingModes {
		if mode == m {
			return mode, ""
		}
	}
	return "", fmt.Sprintf("A pricing mode must be one of %s, but was '%s'", quotedList(pricingModes), raw)
}

// validateFixedAmount is the fixed rule both ways round: a 'fixed' line needs
// an amount greater than zero, and any other mode must not carry one — a
// stored amount on a line priced from the catalog is a number nobody reads.
func validateFixedAmount(mode string, amount *float64) string {
	if mode == pricingFixed {
		switch {
		case amount == nil:
			return fmt.Sprintf("A '%s' line must have a fixed amount", pricingFixed)
		case *amount <= 0:
			return "A fixed amount must be greater than zero"
		case *amount > maxAmount12:
			return fmt.Sprintf("A fixed amount cannot be greater than %.2f", maxAmount12)
		default:
			return ""
		}
	}
	if amount != nil {
		return fmt.Sprintf("A fixed amount is only allowed on a '%s' line", pricingFixed)
	}
	return ""
}

// validateDiscountPercent is the discount rule, the same way round: a
// percentage off the list price is more than nothing and at most everything.
func validateDiscountPercent(mode string, percent *float64) string {
	if mode == pricingDiscount {
		switch {
		case percent == nil:
			return fmt.Sprintf("A '%s' line must have a discount percent", pricingDiscount)
		case *percent <= 0 || *percent > 100:
			return "A discount percent must be greater than zero and at most 100"
		default:
			return ""
		}
	}
	if percent != nil {
		return fmt.Sprintf("A discount percent is only allowed on a '%s' line", pricingDiscount)
	}
	return ""
}

// validateFixedNeedsCurrency is D13 seen from the line: a negotiated amount
// is an amount in *some* currency, and the project's is the only one a line
// may be denominated in. It reports on `pricingMode` rather than on
// `fixedAmount`, because the fix is on the project, not in this body.
func validateFixedNeedsCurrency(mode string, currency *string) string {
	if mode != pricingFixed || currency != nil {
		return ""
	}
	return fmt.Sprintf("A '%s' line is an amount in the project's currency; set a project currency first", pricingFixed)
}

// validateLineBudgetAmountNeedsCurrency is a line's own D13: a budget amount
// is an amount in *some* currency, and the project's is the only one it may
// be denominated in. It reports on `budgetAmount` itself, unlike a 'fixed'
// line's rule (validateFixedNeedsCurrency, reported on `pricingMode`): a
// budget is not tied to the pricing rule, so the field the caller actually
// set is the field the fix belongs on.
func validateLineBudgetAmountNeedsCurrency(amount *float64, currency *string) string {
	if amount == nil || currency != nil {
		return ""
	}
	return "A budget amount is in the project's currency; set a project currency first"
}

// variantNotFound is the `variantId` message for a variant products does not
// know. Like a role assignment's unknown user, it is an ordinary field error
// rather than a 404: the project exists and the caller may manage it, so what
// is wrong is the body they sent, not the resource they addressed.
func variantNotFound(variantID int32) string {
	return fmt.Sprintf("Product variant %d does not exist", variantID)
}

// variantExists asks the catalog whether products still knows a variant. It
// is the one line rule that is not a property of the body alone, which is why
// it is not part of validateLine — and, for the same reason, it is never
// asked from inside a transaction that holds a lock: a create always asks it
// before opening one, and a change asks it just as unconditionally, before
// opening its own, even though the answer only ever matters when the request
// is actually moving the line to a different variant — which only the locked
// row can say (lines.go). Asking unconditionally, before any lock is taken,
// is the price of never letting a slow or blocked call into another module
// stall every other writer of the project behind that lock.
func (s *server) variantExists(ctx context.Context, variantID int32) (bool, error) {
	variant, err := s.deps.Products.Variant(ctx, variantID)
	if err != nil {
		return false, fmt.Errorf("projects: look up product variant: %w", err)
	}
	return variant != nil, nil
}

// parsedLine is one validated line body, in the shape the write wants: the
// code normalized, the amounts already pgtype.Numeric. Active is a pointer
// because a PUT that leaves it out leaves the line as it stands, and a create
// never carries one at all — a new line is active.
type parsedLine struct {
	Code            string
	VariantID       int32
	PricingMode     string
	FixedAmount     pgtype.Numeric
	DiscountPercent pgtype.Numeric
	BudgetHours     pgtype.Numeric
	BudgetAmount    pgtype.Numeric
	Active          *bool
}

// validateLine runs every line rule that is a property of the body alone and
// returns the write-ready line, the field errors (nil when there are none),
// and an error for an infrastructure failure — a number Postgres could not
// store — which is never the caller's fault and so is never a field error.
//
// The variant's existence is deliberately not among them (variantExists):
// whether it has to be checked at all depends on what the line already says,
// and on a change that is only settled under the row lock. Everything here is
// decidable from the body and the project, so it runs before any transaction
// and still reports every problem with the body in one round trip.
//
// The rules that depend on the pricing mode's *validated* value are skipped
// when that field failed: a second message derived from a value already
// rejected only adds noise. project is the line's own project, read by the
// handler: it is what says whether there is a currency to price a 'fixed'
// line in.
func validateLine(body gen.BillingLineRequest, project store.ProjectsProject) (parsedLine, map[string][]string, error) {
	errs := map[string][]string{}
	add := func(field, msg string) {
		if msg != "" {
			errs[field] = append(errs[field], msg)
		}
	}

	code, msg := validateLineCode(body.Code)
	add("code", msg)

	mode, msg := validatePricingMode(body.PricingMode)
	add("pricingMode", msg)
	if mode != "" {
		add("fixedAmount", validateFixedAmount(mode, body.FixedAmount))
		add("discountPercent", validateDiscountPercent(mode, body.DiscountPercent))
		add("pricingMode", validateFixedNeedsCurrency(mode, project.Currency))
	}

	add("budgetHours", validatePositiveAmount("Budget hours", body.BudgetHours, maxHours10))
	add("budgetAmount", validatePositiveAmount("A budget amount", body.BudgetAmount, maxAmount12))
	add("budgetAmount", validateLineBudgetAmountNeedsCurrency(body.BudgetAmount, project.Currency))

	if len(errs) > 0 {
		return parsedLine{}, errs, nil
	}

	fixedAmount, err := numericFromFloatPtr(body.FixedAmount)
	if err != nil {
		return parsedLine{}, nil, err
	}
	discountPercent, err := numericFromFloatPtr(body.DiscountPercent)
	if err != nil {
		return parsedLine{}, nil, err
	}
	budgetHours, err := numericFromFloatPtr(body.BudgetHours)
	if err != nil {
		return parsedLine{}, nil, err
	}
	budgetAmount, err := numericFromFloatPtr(body.BudgetAmount)
	if err != nil {
		return parsedLine{}, nil, err
	}

	return parsedLine{
		Code:            code,
		VariantID:       body.VariantId,
		PricingMode:     mode,
		FixedAmount:     fixedAmount,
		DiscountPercent: discountPercent,
		BudgetHours:     budgetHours,
		BudgetAmount:    budgetAmount,
		Active:          body.Active,
	}, nil, nil
}
