package customers_test

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/customers"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is the billing profile's credit half (invoices phase 6 design
// I37, I38; plan 2026-11-18-invoices-phase6-e-plan.md, steps E11, E14): the
// credit limit, the derived bookkeeping obligation with its override, the
// customers.credit_limit.changed event, anonymisation, the directory's
// fields and the overview's credit section.

type creditProfileJSON struct {
	CreditLimit                        *string `json:"creditLimit"`
	BookkeepingObligated               *bool   `json:"bookkeepingObligated"`
	BookkeepingObligatedSource         string  `json:"bookkeepingObligatedSource"`
	BookkeepingObligatedOverride       *bool   `json:"bookkeepingObligatedOverride"`
	BookkeepingObligatedOverrideReason *string `json:"bookkeepingObligatedOverrideReason"`
	Revision                           int32   `json:"revision"`
}

func fetchCreditProfile(t *testing.T, c *modtest.Client, id int32) creditProfileJSON {
	t.Helper()
	r := getBillingProfile(t, c, id)
	if r.Status != http.StatusOK {
		t.Fatalf("get billing profile: status %d body %s", r.Status, r.Body)
	}
	var p creditProfileJSON
	r.JSON(&p)
	return p
}

func creditLimitEvents(t *testing.T, h *modtest.Harness, customerID int32) int {
	t.Helper()
	return h.Count(t, `SELECT count(*) FROM platform.events
	                   WHERE name = 'customers.credit_limit.changed' AND module = 'customers' AND aggregate_id = ($1::int)::text`, customerID)
}

// The limit and the override are written, read back, and the limit's change
// published once: an unchanged PUT publishes nothing.
func TestPutBillingProfile_CreditLimitAndOverride(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "Kreditt AS")

	body := map[string]any{"creditLimit": "50000", "bookkeepingObligatedOverride": true, "bookkeepingObligatedOverrideReason": " Regnskapspliktig AS "}
	if r := putBillingProfile(t, c, created.Id, body); r.Status != http.StatusOK {
		t.Fatalf("put: status %d body %s", r.Status, r.Body)
	}
	got := fetchCreditProfile(t, c, created.Id)
	if got.CreditLimit == nil || *got.CreditLimit != "50000.00" {
		t.Errorf("creditLimit = %v, want 50000.00", got.CreditLimit)
	}
	if got.BookkeepingObligatedSource != "override" || got.BookkeepingObligated == nil || !*got.BookkeepingObligated {
		t.Errorf("bookkeeping = %v from %q, want true from override", got.BookkeepingObligated, got.BookkeepingObligatedSource)
	}
	if got.BookkeepingObligatedOverrideReason == nil || *got.BookkeepingObligatedOverrideReason != "Regnskapspliktig AS" {
		t.Errorf("reason = %v, want trimmed", got.BookkeepingObligatedOverrideReason)
	}
	if n := creditLimitEvents(t, h, created.Id); n != 1 {
		t.Errorf("%d credit_limit.changed events, want 1", n)
	}
	revision := got.Revision

	// The same again is a no-op: no revision, no event.
	if r := putBillingProfile(t, c, created.Id, body); r.Status != http.StatusOK {
		t.Fatalf("resubmit: status %d body %s", r.Status, r.Body)
	}
	if again := fetchCreditProfile(t, c, created.Id); again.Revision != revision {
		t.Errorf("revision after a no-op = %d, want %d", again.Revision, revision)
	}
	if n := creditLimitEvents(t, h, created.Id); n != 1 {
		t.Errorf("%d events after a no-op, want 1", n)
	}

	// A PUT that names neither field keeps both: a client that never knew of
	// them cannot wipe them.
	if r := putBillingProfile(t, c, created.Id, map[string]any{"currency": "NOK"}); r.Status != http.StatusOK {
		t.Fatalf("other fields: status %d body %s", r.Status, r.Body)
	}
	if kept := fetchCreditProfile(t, c, created.Id); kept.CreditLimit == nil || *kept.CreditLimit != "50000.00" ||
		kept.BookkeepingObligatedOverride == nil || !*kept.BookkeepingObligatedOverride {
		t.Errorf("after a PUT without them = %+v, want the limit and the override kept", kept)
	}
	if n := creditLimitEvents(t, h, created.Id); n != 1 {
		t.Errorf("%d events after a PUT that kept the limit, want 1", n)
	}

	// Only the override is cleared: a write, no event. Then the limit is
	// removed: an event with a null limit.
	if r := putBillingProfile(t, c, created.Id, map[string]any{"clearBookkeepingOverride": true}); r.Status != http.StatusOK {
		t.Fatalf("clear override: status %d body %s", r.Status, r.Body)
	}
	if n := creditLimitEvents(t, h, created.Id); n != 1 {
		t.Errorf("%d events after an override change, want 1", n)
	}
	if r := putBillingProfile(t, c, created.Id, map[string]any{"clearCreditLimit": true}); r.Status != http.StatusOK {
		t.Fatalf("clear: status %d body %s", r.Status, r.Body)
	}
	cleared := fetchCreditProfile(t, c, created.Id)
	if cleared.CreditLimit != nil || cleared.BookkeepingObligatedOverride != nil || cleared.BookkeepingObligatedSource != "unknown" {
		t.Errorf("after clearing = %+v, want no limit, no override, unknown", cleared)
	}
	if n := creditLimitEvents(t, h, created.Id); n != 2 {
		t.Errorf("%d events after removing the limit, want 2", n)
	}
	if null := modtest.One[bool](t, h, `SELECT payload->'creditLimit' = 'null'::jsonb FROM platform.events
	                                     WHERE name = 'customers.credit_limit.changed' AND aggregate_id = ($1::int)::text ORDER BY id DESC LIMIT 1`, created.Id); !null {
		t.Error("the removal's payload creditLimit is not null")
	}
}

// The credit fields' refusals, each on its own field.
func TestPutBillingProfile_CreditRefusals(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "Avvist AS")
	for _, tc := range []struct {
		name  string
		body  map[string]any
		field string
	}{
		{"negative limit", map[string]any{"creditLimit": "-1"}, "creditLimit"},
		{"three decimals", map[string]any{"creditLimit": "10.005"}, "creditLimit"},
		{"not a number", map[string]any{"creditLimit": "ti tusen"}, "creditLimit"},
		{"thousands separators", map[string]any{"creditLimit": "1 000,00"}, "creditLimit"},
		{"set and cleared", map[string]any{"creditLimit": "10", "clearCreditLimit": true}, "creditLimit"},
		{"override set and cleared", map[string]any{"bookkeepingObligatedOverride": true, "bookkeepingObligatedOverrideReason": "x", "clearBookkeepingOverride": true}, "bookkeepingObligatedOverride"},
		{"override without reason", map[string]any{"bookkeepingObligatedOverride": false}, "bookkeepingObligatedOverrideReason"},
		{"override with blank reason", map[string]any{"bookkeepingObligatedOverride": false, "bookkeepingObligatedOverrideReason": "  "}, "bookkeepingObligatedOverrideReason"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := putBillingProfile(t, c, created.Id, tc.body)
			if r.Status != http.StatusBadRequest {
				t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
			}
			var p struct {
				Errors map[string][]string `json:"errors"`
			}
			r.JSON(&p)
			if len(p.Errors[tc.field]) == 0 {
				t.Errorf("errors = %v, want one on %s", p.Errors, tc.field)
			}
		})
	}
}

// The derived fact (I37): the registry record of the customer's own
// organisation number decides — VAT register or Register of Business
// Enterprises — the override beats it, and no record is unknown.
func TestBillingProfile_BookkeepingObligatedFromTheRegistry(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	orgNr := "923609016"
	created := createCustomer(t, c, "Registrert AS")
	setLegalIdentity(t, h, created.Id, "no", orgNr, "REGISTRERT AS")
	if got := fetchCreditProfile(t, c, created.Id); got.BookkeepingObligatedSource != "unknown" || got.BookkeepingObligated != nil {
		t.Errorf("no record: %v from %q, want unknown", got.BookkeepingObligated, got.BookkeepingObligatedSource)
	}
	insert := func(vat bool, register *bool) {
		h.Exec(t, `DELETE FROM customers.customer_registry_records WHERE customer_id = $1`, created.Id)
		h.Exec(t, `
			INSERT INTO customers.customer_registry_records
				(customer_id, organisation_number, name, vat_registered, business_register_registered, bankrupt, under_liquidation, under_forced_liquidation, fetched_at)
			VALUES ($1, $2, 'REGISTRERT AS', $3, $4, false, false, false, $5)`, created.Id, orgNr, vat, register, h.Now())
	}
	yes, no := true, false
	for _, tc := range []struct {
		name     string
		vat      bool
		register *bool
		want     bool
	}{
		{"VAT registered, register not read yet", true, nil, true},
		{"in the Register of Business Enterprises only", false, &yes, true},
		{"in neither", false, &no, false},
	} {
		insert(tc.vat, tc.register)
		got := fetchCreditProfile(t, c, created.Id)
		if got.BookkeepingObligatedSource != "registry" || got.BookkeepingObligated == nil || *got.BookkeepingObligated != tc.want {
			t.Errorf("%s: %v from %q, want %v from registry", tc.name, got.BookkeepingObligated, got.BookkeepingObligatedSource, tc.want)
		}
	}
	// Not VAT-registered and the Register of Business Enterprises not read
	// yet: the register has not said no, so the answer is unknown.
	insert(false, nil)
	if got := fetchCreditProfile(t, c, created.Id); got.BookkeepingObligatedSource != "unknown" || got.BookkeepingObligated != nil {
		t.Errorf("register not read: %v from %q, want unknown", got.BookkeepingObligated, got.BookkeepingObligatedSource)
	}
	if r := putBillingProfile(t, c, created.Id, map[string]any{"bookkeepingObligatedOverride": true, "bookkeepingObligatedOverrideReason": "Bekreftet"}); r.Status != http.StatusOK {
		t.Fatalf("override: status %d body %s", r.Status, r.Body)
	}
	if got := fetchCreditProfile(t, c, created.Id); got.BookkeepingObligatedSource != "override" || !*got.BookkeepingObligated {
		t.Errorf("override: %v from %q, want true from override", got.BookkeepingObligated, got.BookkeepingObligatedSource)
	}

	// The directory answers the same, with the limit.
	profile, err := customers.Module().Directory(h.Deps()).BillingProfile(context.Background(), created.Id, contracts.BillingProfileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if profile.BookkeepingObligated == nil || !*profile.BookkeepingObligated || profile.BookkeepingObligatedSource != "override" || profile.CreditLimit != nil {
		t.Errorf("directory = %v from %q, limit %v; want true from override, no limit", profile.BookkeepingObligated, profile.BookkeepingObligatedSource, profile.CreditLimit)
	}
}

// Anonymisation keeps the limit and the override, business facts, and
// blanks the override's reason, free text.
func TestAnonymisation_KeepsTheCreditLimitAndBlanksTheReason(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	person := archivedPerson(t, c, "Kari Nordmann")
	h.Exec(t, `UPDATE customers.customers SET credit_limit = 1000, bookkeeping_obligated_override = false,
	           bookkeeping_obligated_override_reason = 'Kari sa det' WHERE id = $1`, person.Id)
	scheduleOn(t, h, person.Id, day(h, 1))
	h.Advance(48 * time.Hour)
	runAnonymisation(t, h)
	row := modtest.One[string](t, h, `SELECT credit_limit::text || '|' || bookkeeping_obligated_override::text || '|' || bookkeeping_obligated_override_reason
	                                  FROM customers.customers WHERE id = $1`, person.Id)
	if row != "1000.00|false|" {
		t.Errorf("after anonymisation = %q, want 1000.00|false|", row)
	}
}

// fakeBacklog is contracts.OrderBacklog over amounts a test sets.
type fakeBacklog struct {
	mu      sync.Mutex
	amounts []contracts.CurrencyAmount
}

func (f *fakeBacklog) UninvoicedForCustomer(context.Context, int32) ([]contracts.CurrencyAmount, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.amounts, nil
}

// The overview's credit section: open NOK invoices plus the order backlog in
// NOK against the limit, the other currencies listed and never added.
func TestOverview_CreditAgainstTheLimit(t *testing.T) {
	t.Parallel()
	receivables := &fakeReceivables{}
	backlog := &fakeBacklog{amounts: []contracts.CurrencyAmount{{Currency: "NOK", Amount: "400.00"}, {Currency: "SEK", Amount: "75.00"}}}
	h := newHarness(t, modtest.WithReceivables(receivables), modtest.WithOrderBacklog(backlog))
	id := insertCustomer(t, h, "Eksponert AS", "active")
	receivables.set(id, contracts.CustomerReceivableTotals{
		Currencies: []contracts.CurrencyReceivables{
			{Currency: "EUR", Outstanding: "100.00", Overdue: "0.00"},
			{Currency: "NOK", Outstanding: "700.50", Overdue: "0.00"},
		},
		AsOf: "2026-09-12",
	})
	viewer := h.SignIn(t, "customers:view", "invoices:access")

	// No limit: no section.
	if _, keys := getOverview(t, viewer, id); keys["credit"] != nil {
		t.Errorf("credit section without a limit: %s", keys["credit"])
	}

	h.Exec(t, `UPDATE customers.customers SET credit_limit = 1000 WHERE id = $1`, id)
	var got struct {
		Credit struct {
			Limit         string               `json:"limit"`
			ExposureNok   string               `json:"exposureNok"`
			Exceeded      bool                 `json:"exceeded"`
			OrdersCounted bool                 `json:"ordersCounted"`
			Uncounted     []overviewAmountJSON `json:"uncounted"`
		} `json:"credit"`
	}
	r := viewer.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/overview", id), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("overview: status %d body %s", r.Status, r.Body)
	}
	r.JSON(&got)
	if got.Credit.Limit != "1000.00" || got.Credit.ExposureNok != "1100.50" || !got.Credit.Exceeded || !got.Credit.OrdersCounted {
		t.Errorf("credit = %+v, want limit 1000.00, exposure 1100.50, exceeded, orders counted", got.Credit)
	}
	if want := amounts("EUR", 100.0, "SEK", 75.0); !reflect.DeepEqual(got.Credit.Uncounted, want) {
		t.Errorf("uncounted = %v, want %v", got.Credit.Uncounted, want)
	}

	// Without invoices:access, nothing of it.
	if _, keys := getOverview(t, h.SignIn(t, "customers:view"), id); keys["credit"] != nil {
		t.Errorf("credit section without invoices:access: %s", keys["credit"])
	}
}
