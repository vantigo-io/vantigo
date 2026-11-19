package customers

import (
	"context"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/creditlimit"
	"github.com/vantigo-io/vantigo/server/internal/customers/gen"
	"github.com/vantigo-io/vantigo/server/internal/customers/store"
	"github.com/vantigo-io/vantigo/server/internal/events"
)

// This file is the billing profile's credit half (invoices phase 6 design
// I37, I38; plan docs/superpowers/plans/2026-11-18-invoices-phase6-e-plan.md,
// step E11): the credit limit in NOK, and whether the customer is
// bookkeeping-obligated — derived from the registry, with a person's override
// and its reason. They ride on GET/PUT .../billing-profile, under the same
// permissions, but are their own columns and their own write
// (UpdateCustomerCreditSettings), so the CSV importer and the export, which
// speak the profile's eleven columns, never touch them.

// customersModule is the module's name as the outbox records its publisher.
const customersModule = "customers"

// valuePtr is v's address.
func valuePtr[T any](v T) *T { return &v }

// recordCustomerCreditSettingsUpdated is the credit half's timeline entry: a
// billing-profile update, its before and after the credit settings, its
// changes only the fields that moved.
func recordCustomerCreditSettingsUpdated(ctx context.Context, q *store.Queries, now time.Time, customerID int32, before, after creditSettings, actorKind, actorDisplay string, actorUserID *uuid.UUID) error {
	changes := map[string]any{}
	if !stringPtrEqual(before.CreditLimit, after.CreditLimit) {
		changes["creditLimit"] = map[string]any{"before": before.CreditLimit, "after": after.CreditLimit}
	}
	if !boolPtrEqual(before.BookkeepingObligatedOverride, after.BookkeepingObligatedOverride) || before.OverrideReason != after.OverrideReason {
		changes["bookkeepingObligatedOverride"] = map[string]any{
			"before": before.BookkeepingObligatedOverride, "after": after.BookkeepingObligatedOverride,
		}
	}
	payload := map[string]any{"customerId": customerID, "before": before, "after": after, "changes": changes}
	return recordGeneratedEvent(ctx, q, customerID, now, "customer.billing_profile_updated", "Customer billing profile updated", payload, 1, actorKind, actorDisplay, actorUserID)
}

// The sources of the bookkeeping obligation, as the wire names them.
const (
	bookkeepingSourceOverride = "override"
	bookkeepingSourceRegistry = "registry"
	bookkeepingSourceUnknown  = "unknown"
)

// maxOverrideReason is the override reason's column width.
const maxOverrideReason = 500

// creditSettings is the credit half of a billing profile as it is stored:
// CreditLimit decimal text with two decimals, nil for none; the override nil
// for "as the registry says", with Reason "" whenever it is nil. json tags
// make it the timeline payload's shape.
type creditSettings struct {
	CreditLimit                  *string `json:"creditLimit"`
	BookkeepingObligatedOverride *bool   `json:"bookkeepingObligatedOverride"`
	OverrideReason               string  `json:"bookkeepingObligatedOverrideReason"`
}

// creditSettingsEqual is whether a and b store the same.
func creditSettingsEqual(a, b creditSettings) bool {
	return stringPtrEqual(a.CreditLimit, b.CreditLimit) && boolPtrEqual(a.BookkeepingObligatedOverride, b.BookkeepingObligatedOverride) &&
		a.OverrideReason == b.OverrideReason
}

// boolPtrEqual is stringPtrEqual's *bool twin.
func boolPtrEqual(a, b *bool) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// creditFacts is what CustomerCreditSettings read: the stored settings and
// the registry's two flags.
type creditFacts struct {
	settings                          creditSettings
	hasRecord                         bool
	vatRegistered, businessRegistered *bool
}

// readCreditFacts is CustomerCreditSettings for one customer, pgx.ErrNoRows
// wrapped when it does not exist.
func readCreditFacts(ctx context.Context, q *store.Queries, customerID int32) (creditFacts, error) {
	row, err := q.CustomerCreditSettings(ctx, customerID)
	if err != nil {
		return creditFacts{}, fmt.Errorf("customers: read customer %d's credit settings: %w", customerID, err)
	}
	limit, err := creditLimitText(row.CreditLimit)
	if err != nil {
		return creditFacts{}, err
	}
	return creditFacts{
		settings: creditSettings{
			CreditLimit: limit, BookkeepingObligatedOverride: row.BookkeepingObligatedOverride,
			OverrideReason: row.BookkeepingObligatedOverrideReason,
		},
		hasRecord: row.HasRegistryRecord, vatRegistered: row.RegistryVatRegistered,
		businessRegistered: row.RegistryBusinessRegisterRegistered,
	}, nil
}

// bookkeepingObligated is the derived fact (I37): the override when set;
// else, with a registry record, registered in the VAT register or in the
// Register of Business Enterprises. The latter is unknown (nil) on a record
// not refreshed since it was added: a VAT registration still says yes, but
// without one the answer is unknown, never a "no" the register did not give.
// A customer with no record — a person, a foreign business — is unknown, never
// obligated.
func bookkeepingObligated(f creditFacts) (*bool, string) {
	if f.settings.BookkeepingObligatedOverride != nil {
		v := *f.settings.BookkeepingObligatedOverride
		return &v, bookkeepingSourceOverride
	}
	if !f.hasRecord {
		return nil, bookkeepingSourceUnknown
	}
	vat := f.vatRegistered != nil && *f.vatRegistered
	if !vat && f.businessRegistered == nil {
		return nil, bookkeepingSourceUnknown
	}
	v := vat || *f.businessRegistered
	return &v, bookkeepingSourceRegistry
}

// withCreditFacts fills the billing profile response's credit half.
func withCreditFacts(resp *gen.CustomerBillingProfile, f creditFacts) {
	obligated, source := bookkeepingObligated(f)
	resp.BookkeepingObligated = obligated
	resp.BookkeepingObligatedSource = gen.CustomerBillingProfileBookkeepingObligatedSource(source)
	resp.CreditLimit = f.settings.CreditLimit
	resp.BookkeepingObligatedOverride = f.settings.BookkeepingObligatedOverride
	if f.settings.BookkeepingObligatedOverride != nil {
		resp.BookkeepingObligatedOverrideReason = valuePtr(f.settings.OverrideReason)
	}
}

// creditLimitPattern is a NOK amount: up to twelve whole digits, at most two
// decimals — numeric(14,2)'s width.
var creditLimitPattern = regexp.MustCompile(`^[0-9]{1,12}(\.[0-9]{1,2})?$`)

// validateCreditSettings judges the PUT's credit fields into errs, keyed by
// their JSON names, on top of before, what is stored. Unlike the rest of the
// profile they are not a full replace: an absent field keeps its stored
// value, since a client written before them must not wipe a limit it never
// saw. creditLimit sets the limit (a non-negative NOK amount with at most two
// decimals) and clearCreditLimit removes it; bookkeepingObligatedOverride
// sets the override, which needs its reason, and clearBookkeepingOverride
// removes it with its reason. A value and its clear together are refused.
func validateCreditSettings(req gen.PutCustomerBillingProfileRequest, before creditSettings, errs map[string][]string) creditSettings {
	out := before
	clearLimit := req.ClearCreditLimit != nil && *req.ClearCreditLimit
	clearOverride := req.ClearBookkeepingOverride != nil && *req.ClearBookkeepingOverride
	switch {
	case clearLimit && req.CreditLimit != nil:
		errs["creditLimit"] = []string{"A credit limit cannot be set and cleared in one request"}
	case clearLimit:
		out.CreditLimit = nil
	case req.CreditLimit != nil:
		raw := strings.TrimSpace(*req.CreditLimit)
		if !creditLimitPattern.MatchString(raw) {
			errs["creditLimit"] = []string{fmt.Sprintf("A credit limit must be an amount in NOK of zero or more, with at most two decimals and no thousands separators, but was '%s'", *req.CreditLimit)}
			break
		}
		r, _ := new(big.Rat).SetString(raw)
		out.CreditLimit = valuePtr(r.FloatString(2))
	}
	switch {
	case clearOverride && req.BookkeepingObligatedOverride != nil:
		errs["bookkeepingObligatedOverride"] = []string{"An override cannot be set and cleared in one request"}
	case clearOverride:
		out.BookkeepingObligatedOverride, out.OverrideReason = nil, ""
	case req.BookkeepingObligatedOverride != nil:
		v := *req.BookkeepingObligatedOverride
		reason := ""
		if req.BookkeepingObligatedOverrideReason != nil {
			reason = strings.TrimSpace(*req.BookkeepingObligatedOverrideReason)
		}
		switch {
		case reason == "":
			errs["bookkeepingObligatedOverrideReason"] = []string{"An override of the bookkeeping obligation needs a reason"}
		case utf8.RuneCountInString(reason) > maxOverrideReason:
			errs["bookkeepingObligatedOverrideReason"] = []string{fmt.Sprintf("A reason is at most %d characters", maxOverrideReason)}
		default:
			out.BookkeepingObligatedOverride, out.OverrideReason = &v, reason
		}
	}
	return out
}

// creditLimitText is the limit column as decimal text with two decimals, nil
// for NULL.
func creditLimitText(n pgtype.Numeric) (*string, error) {
	if !n.Valid {
		return nil, nil
	}
	if n.NaN || n.InfinityModifier != pgtype.Finite || n.Int == nil {
		return nil, fmt.Errorf("customers: a stored credit limit is not a number")
	}
	r := new(big.Rat).SetInt(n.Int)
	scale := new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(abs32(n.Exp))), nil))
	if n.Exp < 0 {
		r.Quo(r, scale)
	} else {
		r.Mul(r, scale)
	}
	return valuePtr(r.FloatString(2)), nil
}

func abs32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}

// creditLimitColumn is a limit's text as the column's value.
func creditLimitColumn(text *string) (pgtype.Numeric, error) {
	if text == nil {
		return pgtype.Numeric{}, nil
	}
	var n pgtype.Numeric
	if err := n.Scan(*text); err != nil {
		return pgtype.Numeric{}, fmt.Errorf("customers: %q is not a storable credit limit: %w", *text, err)
	}
	return n, nil
}

// writeCreditSettings is the PUT's write of the credit half on tx, which
// already holds the customer's lock when billingWritten (the profile's own
// write ran first); otherwise it takes it. A changed limit publishes
// customers.credit_limit.changed on the same transaction — the module's first
// outbox event, whose payload is the customer's id and the limit, never a
// name. The change goes on the timeline as a billing-profile update.
func writeCreditSettings(ctx context.Context, tx pgx.Tx, txq *store.Queries, id int32, before, after creditSettings,
	expectedRevision *int32, billingWritten bool, now time.Time, act actor,
) (store.UpdateCustomerCreditSettingsRow, error) {
	if !billingWritten {
		if _, err := lockWritableCustomer(ctx, txq, id); err != nil {
			return store.UpdateCustomerCreditSettingsRow{}, err
		}
	}
	limit, err := creditLimitColumn(after.CreditLimit)
	if err != nil {
		return store.UpdateCustomerCreditSettingsRow{}, err
	}
	updated, err := txq.UpdateCustomerCreditSettings(ctx, store.UpdateCustomerCreditSettingsParams{
		ID: id, CreditLimit: limit, BookkeepingObligatedOverride: after.BookkeepingObligatedOverride,
		BookkeepingObligatedOverrideReason: after.OverrideReason, UpdatedAt: now, ExpectedRevision: expectedRevision,
	})
	if err != nil {
		return store.UpdateCustomerCreditSettingsRow{}, err
	}
	if !stringPtrEqual(before.CreditLimit, after.CreditLimit) {
		if err := events.Publish(ctx, tx, customersModule, contracts.CustomerCreditLimitChanged{
			CustomerID: id, CreditLimit: after.CreditLimit, ChangedAt: now,
		}, now); err != nil {
			return store.UpdateCustomerCreditSettingsRow{}, err
		}
	}
	return updated, recordCustomerCreditSettingsUpdated(ctx, txq, now, id, before, after, act.Kind, act.Display, act.UserID)
}

// overviewCredit is the overview's credit section (I38): the customer's
// limit beside its exposure — Invoices' open NOK through totals, plus Orders'
// confirmed and uninvoiced through deps.OrderBacklog when Orders is on —
// judged by the one definition, creditlimit. nil when no limit is set.
func (s *server) overviewCredit(ctx context.Context, q *store.Queries, customerID int32, totals contracts.CustomerReceivableTotals) (*gen.CustomerOverviewCredit, error) {
	facts, err := readCreditFacts(ctx, q, customerID)
	if err != nil {
		return nil, err
	}
	if facts.settings.CreditLimit == nil {
		return nil, nil
	}
	var backlog []contracts.CurrencyAmount
	if s.deps.OrderBacklog != nil {
		if backlog, err = s.deps.OrderBacklog.UninvoicedForCustomer(ctx, customerID); err != nil {
			// The section is a panel's: without the orders the exposure would
			// be under-reported, so it is left out and the rest still answers.
			s.deps.Logger.WarnContext(ctx, "customers: overview: the order backlog could not be read; the credit section is left out",
				"customer_id", customerID, "error", err.Error())
			return nil, nil
		}
	}
	// The orders' other currencies at today's rate; one with no rate stays
	// uncounted. A rate that cannot be read leaves the section out, as the
	// backlog does.
	rates, err := creditlimit.RatesFor(ctx, s.deps.ExchangeRates, s.deps.Clock(), creditlimit.Currencies(backlog)...)
	if err != nil {
		s.deps.Logger.WarnContext(ctx, "customers: overview: the exchange rates could not be read; the credit section is left out",
			"customer_id", customerID, "error", err.Error())
		return nil, nil
	}
	exposure, err := creditlimit.Exposure(&totals, backlog, rates)
	if err != nil {
		return nil, err
	}
	exceeded, err := creditlimit.Exceeds(facts.settings.CreditLimit, exposure)
	if err != nil {
		return nil, err
	}
	uncounted := map[string]*big.Rat{}
	for _, u := range exposure.Uncounted {
		amount, err := overviewAmount(u.Amount)
		if err != nil {
			return nil, err
		}
		uncounted[u.Currency] = amount
	}
	return &gen.CustomerOverviewCredit{
		Limit: *facts.settings.CreditLimit, ExposureNok: exposure.NOK, Exceeded: exceeded,
		Uncounted: overviewAmounts(uncounted), OrdersCounted: s.deps.OrderBacklog != nil,
	}, nil
}
