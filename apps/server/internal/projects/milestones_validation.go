package projects

import (
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/projects/gen"
	"github.com/vantigo-io/vantigo/server/internal/projects/store"
)

// This file is design §3.2's milestone rules, written the way values.go and
// lines_validation.go write a project's and a line's: one function per rule,
// each answering the normalized value and the message to report, "" when the
// rule holds. It also holds the two things a milestone is more than a form —
// the status flow, as one table, and the amount the plan actually counts.

// The four statuses a milestone may be in (§3.2, E5). Every move between them
// is in milestoneMoves; anything not in that table is refused.
const (
	milestoneStatusPlanned   = "planned"
	milestoneStatusReady     = "ready"
	milestoneStatusInvoiced  = "invoiced"
	milestoneStatusCancelled = "cancelled"
)

// milestoneStatuses is the enumeration in the order the design, the contract
// and the frontend write it, so a message built from it reads the way the
// documentation does — the convention projectStatuses and taskStatuses follow.
var milestoneStatuses = []string{
	milestoneStatusPlanned, milestoneStatusReady, milestoneStatusInvoiced, milestoneStatusCancelled,
}

// milestoneRight is who may make one move (§3.2's access column). Marking a
// milestone invoiced and undoing it are the only two writes a caller who is
// not the project's manager may make: whoever may see the money may say it
// was billed.
type milestoneRight int

const (
	rightManager milestoneRight = iota
	rightFinancials
)

// milestoneMove is everything one allowed move decides: who may make it, the
// timeline entry it writes, and which of the two sets of stamps it sets or
// clears. Keeping it in one table rather than in a chain of conditions is
// what makes "every other pair is refused" true by construction.
type milestoneMove struct {
	Right        milestoneRight
	Event        string
	SetReady     bool
	ClearReady   bool
	SetInvoice   bool
	ClearInvoice bool
}

// milestoneMoves is design §3.2's status table, exactly. A pair that is not
// in it is not a move — including a status a milestone already has, which is
// a button press rather than an event and must not write a timeline entry
// saying something happened.
//
// Reopening a cancelled milestone clears the ready stamps because a milestone
// cancelled while it was ready still carries them, and a planned one may not:
// ready → planned clears them for the same reason. It does not clear the
// invoice fields, because nothing invoiced can be cancelled in the first
// place (invoiced → cancelled is not in this table).
var milestoneMoves = map[[2]string]milestoneMove{
	{milestoneStatusPlanned, milestoneStatusReady}: {
		Right: rightManager, Event: eventMilestoneReady, SetReady: true,
	},
	{milestoneStatusReady, milestoneStatusPlanned}: {
		Right: rightManager, Event: eventMilestonePlanned, ClearReady: true,
	},
	{milestoneStatusReady, milestoneStatusInvoiced}: {
		Right: rightFinancials, Event: eventMilestoneInvoiced, SetInvoice: true,
	},
	{milestoneStatusInvoiced, milestoneStatusReady}: {
		Right: rightFinancials, Event: eventMilestoneInvoiceUndone, ClearInvoice: true,
	},
	{milestoneStatusPlanned, milestoneStatusCancelled}: {
		Right: rightManager, Event: eventMilestoneCancelled,
	},
	{milestoneStatusReady, milestoneStatusCancelled}: {
		Right: rightManager, Event: eventMilestoneCancelled,
	},
	{milestoneStatusCancelled, milestoneStatusPlanned}: {
		Right: rightManager, Event: eventMilestoneReopened, ClearReady: true,
	},
}

// milestoneMoveFor is the table's lookup: the move, and whether there is one.
func milestoneMoveFor(from, to string) (milestoneMove, bool) {
	move, ok := milestoneMoves[[2]string{from, to}]
	return move, ok
}

// validMilestoneStatus reports whether status is one of the four, exactly as
// written: like a project's and a task's, these strings are what the frontend
// and later modules key on, so they are not matched case-insensitively.
func validMilestoneStatus(status string) bool {
	for _, s := range milestoneStatuses {
		if status == s {
			return true
		}
	}
	return false
}

// validateMilestoneStatus is the status rule of the dedicated move operation:
// required, and one of the four. Whether the *move* is allowed is the table's
// question, not this one's.
func validateMilestoneStatus(raw string) (string, string) {
	if strings.TrimSpace(raw) == "" {
		return "", "A status cannot be null or empty"
	}
	if !validMilestoneStatus(raw) {
		return "", fmt.Sprintf("A status must be one of %s, but was '%s'", quotedList(milestoneStatuses), raw)
	}
	return raw, ""
}

// milestoneMoveNotAllowed is the refusal for a pair the table does not have.
// It names both statuses, because "you cannot do that" without saying what
// the milestone actually is leaves a caller with nowhere to go — and the pair
// is exactly what a stale plan in somebody's browser gets wrong.
func milestoneMoveNotAllowed(from, to string) string {
	return fmt.Sprintf("A milestone cannot move from '%s' to '%s'", from, to)
}

// milestoneMoveNeedsCurrency and milestoneMoveNeedsFixedPrice are the status
// flow's half of design §3.3. The project's guards deliberately exempt what
// cannot bill — a cancelled milestone lets the currency go, a cancelled or
// invoiced percent milestone lets the fixed price go — so the moves that turn
// such a milestone back into one that *can* bill are the paths that have to
// ask again, against the project as it now stands.
//
// Both report on `status` rather than on any field of the body, because no
// field of the body is what is wrong: the fix is on the project.
func milestoneMoveNeedsCurrency(to string) string {
	return fmt.Sprintf("A milestone cannot become '%s' while the project has no currency to denominate it in; set one first, or leave the milestone cancelled", to)
}

func milestoneMoveNeedsFixedPrice(to string) string {
	return fmt.Sprintf("A milestone priced as a percent cannot become '%s' while the project has no fixed price to be a share of; set one first, or leave the milestone cancelled", to)
}

// milestoneMoveWrongCurrency is the refusal for a flat amount entered in a
// currency the project has since moved off. Only a cancelled milestone can be
// in that position — the currency guard (design §3.3) exempts it and nothing
// else — and bringing it back would either report the old number as the new
// currency or silently convert it, both of which are lies. The honest answer
// is that this milestone belongs to the project's past.
func milestoneMoveWrongCurrency(amountCurrency, projectCurrency string) string {
	return fmt.Sprintf("This milestone's amount is in %s and the project is now in %s; a milestone cannot be brought back into a different currency, so add a new one instead",
		amountCurrency, projectCurrency)
}

// milestoneMoveRefusal asks design §3.3's two rules of one move, against the
// project row the transaction holds. It answers the message to refuse with
// ("" when the move may proceed) and whether the move must first convert the
// milestone from a percent to a flat amount.
//
// Moving *to* cancelled is never refused: cancelling is how a milestone the
// project can no longer support is got rid of, and refusing it would leave a
// caller with a milestone they can neither price nor remove.
//
// Undoing an invoicing is never refused either, and that is the one case the
// conversion exists for. Crediting an invoice is a real event; if the project
// has dropped the fixed price the milestone was a share of, the share means
// nothing any more but the amount that was actually billed still does, so the
// milestone becomes an amount milestone carrying the number that was frozen
// when it was invoiced.
func milestoneMoveRefusal(m store.ProjectsBillingMilestone, project store.ProjectsProject, to string, move milestoneMove) (msg string, convert bool) {
	if to == milestoneStatusCancelled {
		return "", false
	}
	if project.Currency == nil {
		return milestoneMoveNeedsCurrency(to), false
	}
	// A flat amount carries the currency it was entered in, and a cancelled
	// milestone can outlive a change to the project's (design §3.2). Reopening
	// one would put a number denominated in the old currency back into a plan
	// counted in the new one.
	if m.AmountCurrency != nil && *m.AmountCurrency != *project.Currency {
		return milestoneMoveWrongCurrency(*m.AmountCurrency, *project.Currency), false
	}
	if !m.Percent.Valid || project.FixedPriceAmount.Valid {
		return "", false
	}
	if move.ClearInvoice {
		return "", true
	}
	return milestoneMoveNeedsFixedPrice(to), false
}

// milestoneNotEditable is §3.2's read-only rule: the content of an invoiced
// or a cancelled milestone is a record, and the way back is the status rather
// than the form. It reports on `status` rather than on any field of the body,
// because no field of the body is what is wrong.
func milestoneNotEditable(status string) string {
	if status == milestoneStatusInvoiced {
		return "An invoiced milestone cannot be edited; undo the invoicing first"
	}
	return "A cancelled milestone cannot be edited; reopen it first"
}

// milestoneNotDeletable is §3.2's delete rule, seen from the refusal: only a
// milestone that is still planned and has never moved may be removed, because
// anything else is part of what the plan says happened.
func milestoneNotDeletable() string {
	return "Only a milestone that is still planned and has never changed status can be deleted; cancel this one instead"
}

// invoiceFieldNotAllowed is the refusal for a reference or a date sent on a
// move that is not the invoicing. Silently ignoring them would leave a caller
// believing they recorded something they did not.
func invoiceFieldNotAllowed(what string) string {
	return fmt.Sprintf("An invoice %s is only allowed when marking a milestone invoiced", what)
}

// validateMilestoneName is the name rule: non-blank, at most the column's 200
// characters, trimmed but case-preserved — a project's own name rule.
func validateMilestoneName(raw string) (string, string) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", "A milestone name cannot be null or empty"
	}
	if n := utf8.RuneCountInString(name); n > 200 {
		return "", fmt.Sprintf("A milestone name cannot be longer than 200 characters, the given value was %d characters", n)
	}
	return name, ""
}

// validateMilestoneDescription is the description rule: optional, at most the
// column's 2000 characters. A blank description is stored as none at all, so
// "  " and an absent field mean the same thing.
func validateMilestoneDescription(raw *string) (*string, string) {
	if raw == nil {
		return nil, ""
	}
	trimmed := strings.TrimSpace(*raw)
	if trimmed == "" {
		return nil, ""
	}
	if n := utf8.RuneCountInString(trimmed); n > 2000 {
		return nil, fmt.Sprintf("A milestone description cannot be longer than 2000 characters, the given value was %d characters", n)
	}
	return &trimmed, ""
}

// validateMilestoneAmount is the flat amount's own rule, checked only when
// one was sent: more than nothing, inside what numeric(12,2) can hold, and no
// more precise than that column keeps — a third decimal would be rounded away
// silently, and a milestone priced at something the caller did not type is
// worse than a refusal (the same argument validateMilestonePercent makes).
func validateMilestoneAmount(amount *float64) string {
	switch {
	case amount == nil:
		return ""
	case *amount <= 0:
		return "A milestone amount must be greater than zero"
	case *amount > maxAmount12:
		return fmt.Sprintf("A milestone amount cannot be greater than %.2f", maxAmount12)
	case decimalPlaces(*amount) > 2:
		return "A milestone amount cannot have more than two decimals"
	default:
		return ""
	}
}

// validateMilestonePercent is the percent's own rule: a share of the fixed
// price is more than nothing and at most all of it, with no more precision
// than the numeric(5,2) column keeps — a third decimal would be rounded away
// silently, and a milestone priced at something the caller did not type is
// worse than a refusal.
func validateMilestonePercent(percent *float64) string {
	switch {
	case percent == nil:
		return ""
	case *percent <= 0 || *percent > 100:
		return "A percent must be greater than zero and at most 100"
	case decimalPlaces(*percent) > 2:
		return "A percent cannot have more than two decimals"
	default:
		return ""
	}
}

// decimalPlaces counts the digits after the point in v's shortest text — the
// same text numericFromFloat stores the number through, so what it counts is
// what the column would be asked to keep.
func decimalPlaces(v float64) int {
	text := strconv.FormatFloat(v, 'f', -1, 64)
	point := strings.IndexByte(text, '.')
	if point < 0 {
		return 0
	}
	return len(text) - point - 1
}

// validateAmountOrPercent is §3.2's "exactly one": a milestone is either a
// flat amount or a share of the fixed price, never both and never neither.
// The message goes on both fields, because the form renders them as one
// choice and a caller who picked wrong has to see which two inputs are in it.
func validateAmountOrPercent(amount, percent *float64) string {
	if (amount == nil) == (percent == nil) {
		return "A milestone must carry exactly one of an amount and a percent"
	}
	return ""
}

// validatePercentNeedsFixedPrice is §3.2's other half of the percent rule: a
// share of a price the project does not have resolves to nothing, so it is
// refused until the project has one. It is reported on `percent` rather than
// on the project's field, because `percent` is what this body may drop.
func validatePercentNeedsFixedPrice(percent *float64, project store.ProjectsProject) string {
	if percent == nil || project.FixedPriceAmount.Valid {
		return ""
	}
	return fmt.Sprintf("A percent is a share of the project's fixed price, and this project has none; set a '%s' price first, or give the milestone an amount",
		billingFixedPrice)
}

// milestoneNeedsCurrency is D13 seen from a milestone: whichever way it is
// priced, it is an amount, and the project's currency is the only one it can
// be denominated in.
func milestoneNeedsCurrency() string {
	return "A milestone is an amount in the project's currency; set a project currency first"
}

// validateInvoiceReference is the reference's rule: optional, trimmed, at
// most the column's 100 characters. A blank one is stored as none, so a form
// submitted with an empty box records no reference rather than an empty one.
func validateInvoiceReference(raw *string) (*string, string) {
	if raw == nil {
		return nil, ""
	}
	trimmed := strings.TrimSpace(*raw)
	if trimmed == "" {
		return nil, ""
	}
	if n := utf8.RuneCountInString(trimmed); n > 100 {
		return nil, fmt.Sprintf("An invoice reference cannot be longer than 100 characters, the given value was %d characters", n)
	}
	return &trimmed, ""
}

// parsedMilestone is one validated body, in the shape the write wants: the
// name trimmed, the date already a pgtype.Date, the two amounts already
// pgtype.Numeric.
type parsedMilestone struct {
	Name        string
	Description *string
	PlannedDate pgtype.Date
	Amount      pgtype.Numeric
	Percent     pgtype.Numeric

	// AmountCurrency is the project's currency when the milestone carries a
	// flat amount, and nil when it carries a percent — set exactly when
	// Amount is, so a stored number always says what it is denominated in
	// (design §3.2). It is taken from the project the body was validated
	// against, which on every write is the row that write's own transaction
	// locked.
	AmountCurrency *string
}

// validateMilestone runs every §3.2 content rule over a create body and
// returns the write-ready milestone, the field errors (nil when there are
// none), and an error for an infrastructure failure — a number Postgres could
// not store — which is never the caller's fault and so is never a field error.
//
// Every rule runs regardless of the others, so one round trip reports every
// problem. project is the milestone's own project: it is what says whether
// there is a currency to denominate the amount in and a fixed price for a
// percent to be a share of. Both of those are re-asked against the row the
// write's own transaction locks (milestones.go), because both can be taken
// away by a concurrent update of the project — everything else here is a
// property of the body alone and cannot move.
func validateMilestone(body gen.BillingMilestoneRequest, project store.ProjectsProject) (parsedMilestone, map[string][]string, error) {
	errs := map[string][]string{}
	add := func(field, msg string) {
		if msg != "" {
			errs[field] = append(errs[field], msg)
		}
	}

	name, msg := validateMilestoneName(body.Name)
	add("name", msg)
	description, msg := validateMilestoneDescription(body.Description)
	add("description", msg)

	if msg := validateAmountOrPercent(body.Amount, body.Percent); msg != "" {
		add("amount", msg)
		add("percent", msg)
	} else {
		add("amount", validateMilestoneAmount(body.Amount))
		add("percent", validateMilestonePercent(body.Percent))
		add("percent", validatePercentNeedsFixedPrice(body.Percent, project))
		if project.Currency == nil {
			// The field the fix belongs on is the one the caller actually
			// set — the project's currency is not part of this body, so the
			// message has to hang off the amount they typed.
			if body.Amount != nil {
				add("amount", milestoneNeedsCurrency())
			} else {
				add("percent", milestoneNeedsCurrency())
			}
		}
	}

	if len(errs) > 0 {
		return parsedMilestone{}, errs, nil
	}

	amount, err := numericFromFloatPtr(body.Amount)
	if err != nil {
		return parsedMilestone{}, nil, err
	}
	percent, err := numericFromFloatPtr(body.Percent)
	if err != nil {
		return parsedMilestone{}, nil, err
	}
	var amountCurrency *string
	if body.Amount != nil {
		amountCurrency = project.Currency
	}
	return parsedMilestone{
		Name:           name,
		Description:    description,
		PlannedDate:    dateToPgtype(body.PlannedDate),
		Amount:         amount,
		Percent:        percent,
		AmountCurrency: amountCurrency,
	}, nil, nil
}

// milestoneFromUpdate is an update body seen as the create body §3.2's rules
// are written against. The two differ in exactly one thing — the revision,
// which is a concurrency token rather than a value any rule has an opinion
// about — so one validator serves both paths and a rule can never be enforced
// on a create but forgotten on an update. projectFromUpdate does the same for
// a project.
func milestoneFromUpdate(body gen.BillingMilestoneUpdateRequest) gen.BillingMilestoneRequest {
	return gen.BillingMilestoneRequest{
		Name:        body.Name,
		Description: body.Description,
		PlannedDate: body.PlannedDate,
		Amount:      body.Amount,
		Percent:     body.Percent,
	}
}

// milestoneEffectiveAmount is §3.2's "effective amount", the number the plan
// actually counts: the amount frozen when the milestone was invoiced, else
// the flat amount as entered, else the project's fixed price times the
// percent. It is computed on read rather than stored, which is what makes an
// open percent milestone follow a change to the fixed price — and what makes
// freezing on → invoiced the thing that stops an already-billed one moving.
//
// A percent milestone on a project with no fixed price has no effective
// amount at all, and says so with errMilestoneUnpriced rather than answering
// 0.00 — a milestone reading as nothing planned is the kind of silent money
// loss nobody notices. The status flow refuses every move that would create
// such a milestone (milestoneMoveRefusal), so the only one that can exist is
// a cancelled one on a project that has since dropped its price, and the
// response omits its amount rather than inventing one.
func milestoneEffectiveAmount(m store.ProjectsBillingMilestone, project store.ProjectsProject) (float64, error) {
	if m.InvoicedAmount.Valid {
		return floatFromNumeric(m.InvoicedAmount)
	}
	if m.Amount.Valid {
		return floatFromNumeric(m.Amount)
	}
	if !m.Percent.Valid {
		return 0, fmt.Errorf("projects: milestone %d: %w — it carries neither an amount nor a percent", m.ID, errMilestoneUnpriced)
	}
	percent, ok, err := numericText(m.Percent)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, fmt.Errorf("projects: milestone %d: %w — its percent could not be read", m.ID, errMilestoneUnpriced)
	}
	price, ok, err := numericText(project.FixedPriceAmount)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, fmt.Errorf("projects: milestone %d: %w — it is %s %% of a fixed price project %d no longer has",
			m.ID, errMilestoneUnpriced, percent, project.ID)
	}
	amount, ok := percentOfPrice(price, percent)
	if !ok {
		return 0, fmt.Errorf("projects: milestone %d: cannot resolve %s %% of %s", m.ID, percent, price)
	}
	return amount, nil
}

// errMilestoneUnpriced is what a milestone whose effective amount cannot be
// worked out answers with. It is a legitimate state for exactly one kind of
// milestone — a cancelled percent one on a project that has since left
// fixed-price billing — and an infrastructure failure for any other, which is
// why it is a sentinel the response layer matches on rather than a nil amount
// every caller has to remember to check.
var errMilestoneUnpriced = errors.New("a milestone with no resolvable amount")

// floatFromNumeric is floatPtrFromNumeric for a column the caller has already
// established is not NULL, answering 0 for one that somehow is — the callers
// here have all just checked Valid.
func floatFromNumeric(n pgtype.Numeric) (float64, error) {
	v, err := floatPtrFromNumeric(n)
	if err != nil || v == nil {
		return 0, err
	}
	return *v, nil
}

// milestoneOverdue is §3.2's overdue rule: a milestone nobody has billed yet,
// whose planned day has passed. Both sides are plain calendar dates in UTC —
// the column is a date, and the clock is the server's (Deps.Clock) — so a
// milestone planned for today is not overdue anywhere in the world until the
// server's own day turns over.
func milestoneOverdue(m store.ProjectsBillingMilestone, now time.Time) bool {
	if m.Status != milestoneStatusPlanned && m.Status != milestoneStatusReady {
		return false
	}
	if !m.PlannedDate.Valid {
		return false
	}
	today := now.UTC()
	return m.PlannedDate.Time.UTC().Before(time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC))
}

// milestoneCapabilities is §3.2's rules answered for one caller and one
// milestone, so the frontend renders buttons from the API rather than from a
// copy of the move table. Editing, deleting and every move but the invoicing
// are the project's manager's; marking invoiced and undoing it need financial
// rights, which a manager also has.
func milestoneCapabilities(m store.ProjectsBillingMilestone, project store.ProjectsProject, a access) gen.BillingMilestoneCapabilities {
	may := func(to string) bool {
		move, ok := milestoneMoveFor(m.Status, to)
		if !ok {
			return false
		}
		// The project's own rules, mirrored exactly: a capability must never
		// promise a move the status handler would then refuse, or the UI
		// offers a button that answers 400.
		if msg, _ := milestoneMoveRefusal(m, project, to, move); msg != "" {
			return false
		}
		if move.Right == rightFinancials {
			return a.canSeeMilestones()
		}
		return a.canManageMilestones()
	}
	open := m.Status == milestoneStatusPlanned || m.Status == milestoneStatusReady
	return gen.BillingMilestoneCapabilities{
		CanEdit:         a.canManageMilestones() && open,
		CanDelete:       a.canManageMilestones() && m.Status == milestoneStatusPlanned && !m.EverMoved,
		CanMarkReady:    m.Status == milestoneStatusPlanned && may(milestoneStatusReady),
		CanMarkPlanned:  m.Status == milestoneStatusReady && may(milestoneStatusPlanned),
		CanMarkInvoiced: may(milestoneStatusInvoiced),
		CanUndoInvoiced: m.Status == milestoneStatusInvoiced && may(milestoneStatusReady),
		CanCancel:       may(milestoneStatusCancelled),
		CanReopen:       m.Status == milestoneStatusCancelled && may(milestoneStatusPlanned),
	}
}

// milestoneTotals is what one project's plan adds up to (§3.2): one sum of
// effective amounts per status, and — against a fixed price — how much of it
// the plan does not cover or exceeds by. Cancelled milestones get their own
// sum and count against nothing: they bill nothing, so folding them into the
// comparison would make a dropped milestone look like planned work.
//
// Every sum is accumulated as a *big.Rat over the milestones' exact decimal
// amounts and only rounded once, on the way into the response: adding money
// in float64 reports 0.10 + 0.20 as 0.30000000000000004, and these four
// figures are the Economy tab's headline numbers.
//
// Cancelled milestones have no total of their own. They bill nothing, and —
// being exempt from the project's currency guard — a cancelled flat amount
// may be denominated in a currency the project has since moved off, so
// summing it would mix two currencies into one figure. Every milestone that
// *is* summed is in the project's current currency, because the guard will
// not let the currency move while one exists.
//
// A milestone with no resolvable amount contributes nothing, which is what it
// is worth.
func milestoneTotals(project store.ProjectsProject, amounts map[string]*big.Rat) (gen.BillingMilestonePlanTotals, error) {
	sum := func(status string) float64 {
		if r, ok := amounts[status]; ok {
			return roundHalfUpCents(r)
		}
		return 0
	}
	totals := gen.BillingMilestonePlanTotals{
		Currency: project.Currency,
		Planned:  sum(milestoneStatusPlanned),
		Ready:    sum(milestoneStatusReady),
		Invoiced: sum(milestoneStatusInvoiced),
	}
	fixedPrice, err := floatPtrFromNumeric(project.FixedPriceAmount)
	if err != nil {
		return gen.BillingMilestonePlanTotals{}, err
	}
	if fixedPrice == nil {
		return totals, nil
	}
	totals.FixedPrice = fixedPrice
	// The comparison runs over the accumulated rationals rather than over the
	// four rounded figures above, so nothing is rounded twice.
	planned := new(big.Rat)
	for _, status := range []string{milestoneStatusPlanned, milestoneStatusReady, milestoneStatusInvoiced} {
		if r, ok := amounts[status]; ok {
			planned.Add(planned, r)
		}
	}
	switch diff := new(big.Rat).Sub(exactCents(*fixedPrice), planned); diff.Sign() {
	case 1:
		left := roundHalfUpCents(diff)
		totals.Unplanned = &left
	case -1:
		over := roundHalfUpCents(diff.Neg(diff))
		totals.OverPlanned = &over
	}
	return totals, nil
}

// exactCents is v as the exact decimal its shortest text spells, never the
// binary fraction the float64 happens to hold — exactDecimal by another name,
// kept here because internal/time's copy is one this module may not import.
func exactCents(v float64) *big.Rat {
	r, _ := new(big.Rat).SetString(strconv.FormatFloat(v, 'f', -1, 64))
	if r == nil {
		return new(big.Rat)
	}
	return r
}

// percentOfPrice is design §3.2's effective amount for a percent milestone:
// the project's fixed price times the milestone's percent, divided by a
// hundred, rounded half up to the two places numeric(12,2) stores.
//
// Both operands arrive as the decimal *text* their numeric columns hold
// rather than as float64, and the arithmetic is math/big.Rat, because a half
// cent decided in binary floating point rounds the wrong way: 100 000.01 at
// 12.5 % is exactly 12 500.001 25 (down) while 999.99 at 50 % is exactly
// 499.995 (up), and only one of those two survives a float64 round trip.
// internal/time's `discounted` is the same rule for a discount line; it is
// reimplemented rather than shared because depguard forbids this module from
// importing internal/time.
//
// It reports false for a text neither big.Rat can read, which is never
// reachable from a stored numeric but must not silently become 0.00 — a
// milestone showing nothing planned is worse than an error.
func percentOfPrice(priceText, percentText string) (float64, bool) {
	price, ok := new(big.Rat).SetString(priceText)
	if !ok {
		return 0, false
	}
	percent, ok := new(big.Rat).SetString(percentText)
	if !ok {
		return 0, false
	}
	amount := new(big.Rat).Mul(price, percent)
	amount.Quo(amount, big.NewRat(100, 1))
	return roundHalfUpCents(amount), true
}

// roundHalfUpCents rounds r to two decimals, a half cent away from zero, and
// answers the nearest float64 — whose shortest text is then exactly those two
// decimals, which is what the column stores and what the contract's JSON
// number carries.
func roundHalfUpCents(r *big.Rat) float64 {
	cents := new(big.Rat).Mul(r, big.NewRat(100, 1))
	half := big.NewRat(1, 2)
	if cents.Sign() < 0 {
		half.Neg(half)
	}
	cents.Add(cents, half)
	whole := new(big.Int).Quo(cents.Num(), cents.Denom()) // truncates toward zero
	f, _ := new(big.Rat).SetFrac(whole, big.NewInt(100)).Float64()
	return f
}

// numericText is a stored decimal as the text its column holds — pgtype's own
// text encoding, which is the exact decimal Postgres returned and not a
// float64's approximation of it. An SQL NULL answers "", false.
func numericText(n pgtype.Numeric) (string, bool, error) {
	if !n.Valid {
		return "", false, nil
	}
	v, err := n.Value()
	if err != nil {
		return "", false, fmt.Errorf("projects: read a stored decimal as text: %w", err)
	}
	text, ok := v.(string)
	if !ok {
		return "", false, nil
	}
	return text, true, nil
}
