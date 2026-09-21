package customers_test

import (
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is GET/PUT /customers/{id}/billing-profile (invoice-ready
// customer design D1, D4): a customer's payment terms, currency, document
// language, delivery methods and the identifiers used to send it invoices,
// plus its computed warnings. No .NET ancestor, since a billing profile is
// new to this port; shaped after contact_info_test.go's coverage of this
// module's other revision-guarded sub-resource — the happy path, blank/
// absent both clearing to null, one HTTP case per validation rule
// (billing_values_test.go carries the table-driven unit coverage of the
// validators themselves), the revision guard, the no-op rule, the generated
// timeline event with its actor and before/after payload, the permission
// gate (customers:billing-manage, distinct from customers:update) — and,
// new to this sub-resource, the computed warnings.

type billingProfileJSON struct {
	InvoiceEmail     *string  `json:"invoiceEmail"`
	ReminderEmail    *string  `json:"reminderEmail"`
	PaymentTermsDays *int32   `json:"paymentTermsDays"`
	Currency         *string  `json:"currency"`
	Language         *string  `json:"language"`
	InvoiceDelivery  *string  `json:"invoiceDelivery"`
	ReminderDelivery *string  `json:"reminderDelivery"`
	PeppolId         *string  `json:"peppolId"`
	Gln              *string  `json:"gln"`
	BuyerReference   *string  `json:"buyerReference"`
	Revision         int32    `json:"revision"`
	Warnings         []string `json:"warnings"`
}

func getBillingProfile(t *testing.T, c *modtest.Client, id int32) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/billing-profile", id), nil)
}

func putBillingProfile(t *testing.T, c *modtest.Client, id int32, body map[string]any) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/billing-profile", id), body)
}

// fetchBillingProfile GETs the profile (expecting 200) and returns it decoded.
func fetchBillingProfile(t *testing.T, c *modtest.Client, id int32) billingProfileJSON {
	t.Helper()
	r := getBillingProfile(t, c, id)
	if r.Status != http.StatusOK {
		t.Fatalf("get billing profile %d: status %d body %s, want 200", id, r.Status, r.Body)
	}
	var p billingProfileJSON
	r.JSON(&p)
	return p
}

func TestGetCustomersByIdBillingProfile_FreshCustomer_AllNullWithNoInvoiceAddressWarningOnly(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "Fresh Billing Co")

	got := fetchBillingProfile(t, c, created.Id)
	if got.InvoiceEmail != nil || got.ReminderEmail != nil || got.PaymentTermsDays != nil ||
		got.Currency != nil || got.Language != nil || got.InvoiceDelivery != nil || got.ReminderDelivery != nil ||
		got.PeppolId != nil || got.Gln != nil || got.BuyerReference != nil {
		t.Errorf("profile = %+v, want every field null", got)
	}
	if got.Revision != 1 {
		t.Errorf("revision = %d, want 1 (the row's own)", got.Revision)
	}
	if want := []string{"no_invoice_address"}; len(got.Warnings) != 1 || got.Warnings[0] != want[0] {
		t.Errorf("warnings = %v, want %v", got.Warnings, want)
	}
}

func TestGetCustomersByIdBillingProfile_WhenCustomerDoesNotExist_ReturnsNotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	r := getBillingProfile(t, c, 999999)
	if r.Status != http.StatusNotFound {
		t.Errorf("status %d body %s, want 404", r.Status, r.Body)
	}
}

// TestGetCustomersByIdBillingProfile_OnlyNeedsCustomersView proves the read
// gate is customers:view alone — no billing-manage, no legal-identity-view —
// the controller ruling for this sub-resource's GET.
func TestGetCustomersByIdBillingProfile_OnlyNeedsCustomersView(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner := authenticatedClient(t, h)
	created := createCustomer(t, owner, "View Only Co")

	viewer := h.SignIn(t, "customers:view")
	r := getBillingProfile(t, viewer, created.Id)
	if r.Status != http.StatusOK {
		t.Errorf("status %d body %s, want 200", r.Status, r.Body)
	}
}

func TestPutCustomersByIdBillingProfile_SetsEveryField_ShowsInGet(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "Full Billing Co")

	body := map[string]any{
		"invoiceEmail": "invoice@fullbilling.co", "reminderEmail": "reminders@fullbilling.co",
		"paymentTermsDays": 30, "currency": "nok", "language": "NB",
		"invoiceDelivery": "EHF", "reminderDelivery": "Email",
		"peppolId": "0192:923609016", "gln": "4006381333931", "buyerReference": "PO-42",
	}
	r := putBillingProfile(t, c, created.Id, body)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var updated billingProfileJSON
	r.JSON(&updated)
	want := func(p billingProfileJSON) {
		if p.InvoiceEmail == nil || *p.InvoiceEmail != "invoice@fullbilling.co" {
			t.Errorf("InvoiceEmail = %v, want invoice@fullbilling.co", p.InvoiceEmail)
		}
		if p.ReminderEmail == nil || *p.ReminderEmail != "reminders@fullbilling.co" {
			t.Errorf("ReminderEmail = %v, want reminders@fullbilling.co", p.ReminderEmail)
		}
		if p.PaymentTermsDays == nil || *p.PaymentTermsDays != 30 {
			t.Errorf("PaymentTermsDays = %v, want 30", p.PaymentTermsDays)
		}
		if p.Currency == nil || *p.Currency != "NOK" {
			t.Errorf("Currency = %v, want NOK", p.Currency)
		}
		if p.Language == nil || *p.Language != "nb" {
			t.Errorf("Language = %v, want nb", p.Language)
		}
		if p.InvoiceDelivery == nil || *p.InvoiceDelivery != "ehf" {
			t.Errorf("InvoiceDelivery = %v, want ehf", p.InvoiceDelivery)
		}
		if p.ReminderDelivery == nil || *p.ReminderDelivery != "email" {
			t.Errorf("ReminderDelivery = %v, want email", p.ReminderDelivery)
		}
		if p.PeppolId == nil || *p.PeppolId != "0192:923609016" {
			t.Errorf("PeppolId = %v, want 0192:923609016", p.PeppolId)
		}
		if p.Gln == nil || *p.Gln != "4006381333931" {
			t.Errorf("Gln = %v, want 4006381333931", p.Gln)
		}
		if p.BuyerReference == nil || *p.BuyerReference != "PO-42" {
			t.Errorf("BuyerReference = %v, want PO-42", p.BuyerReference)
		}
	}
	want(updated)
	want(fetchBillingProfile(t, c, created.Id))

	// A Peppol id is set and delivery is ehf, so ehf_without_recipient does
	// not fire; invoiceEmail is set, so email_without_address (moot: delivery
	// is ehf, not email) is irrelevant; the customer is still missing an
	// invoice address.
	if want := []string{"no_invoice_address"}; len(updated.Warnings) != 1 || updated.Warnings[0] != want[0] {
		t.Errorf("warnings = %v, want %v", updated.Warnings, want)
	}
}

// TestPutCustomersByIdBillingProfile_BlankAndAbsentFieldsClearToNull proves
// the controller ruling: blank/whitespace-only, explicit null and an absent
// key all mean the same thing — clear the field — and none of the string
// fields is a validation error, the same rule contact_info.go's PUT applies.
func TestPutCustomersByIdBillingProfile_BlankAndAbsentFieldsClearToNull(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "Clearable Billing Co")

	r := putBillingProfile(t, c, created.Id, map[string]any{
		"invoiceEmail": "invoice@clearable.co", "currency": "NOK", "buyerReference": "PO-1",
	})
	if r.Status != http.StatusOK {
		t.Fatalf("set: status %d body %s, want 200", r.Status, r.Body)
	}

	r = putBillingProfile(t, c, created.Id, map[string]any{"invoiceEmail": "   ", "currency": "", "buyerReference": nil})
	if r.Status != http.StatusOK {
		t.Fatalf("clear with blank/null: status %d body %s, want 200", r.Status, r.Body)
	}
	var cleared billingProfileJSON
	r.JSON(&cleared)
	if cleared.InvoiceEmail != nil || cleared.Currency != nil || cleared.BuyerReference != nil {
		t.Errorf("profile = %+v, want invoiceEmail/currency/buyerReference cleared to null", cleared)
	}

	// Re-set, then clear by omitting every field entirely: absent must mean
	// the same as null (a full replace, customers foundation design D1).
	r = putBillingProfile(t, c, created.Id, map[string]any{"currency": "NOK"})
	if r.Status != http.StatusOK {
		t.Fatalf("re-set: status %d body %s, want 200", r.Status, r.Body)
	}
	r = putBillingProfile(t, c, created.Id, map[string]any{})
	if r.Status != http.StatusOK {
		t.Fatalf("clear with an empty body: status %d body %s, want 200", r.Status, r.Body)
	}
	var afterAbsent billingProfileJSON
	r.JSON(&afterAbsent)
	if afterAbsent.Currency != nil {
		t.Errorf("Currency = %v, want nil after an absent-field PUT (absent means clear)", afterAbsent.Currency)
	}
}

func TestPutCustomersByIdBillingProfile_InvalidPaymentTermsDays_ReturnsBadRequest(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "Bad Terms Co")

	r := putBillingProfile(t, c, created.Id, map[string]any{"paymentTermsDays": 400})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	want := "Payment terms must be between 0 and 365 days, but was 400"
	if msgs := problem.Errors["paymentTermsDays"]; len(msgs) != 1 || msgs[0] != want {
		t.Errorf("errors[paymentTermsDays] = %v, want [%q]", msgs, want)
	}
}

func TestPutCustomersByIdBillingProfile_InvalidCurrency_ReturnsBadRequest(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "Bad Currency Co")

	r := putBillingProfile(t, c, created.Id, map[string]any{"currency": "US"})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	want := "A currency must be a three-letter ISO 4217 code, but was 'US'"
	if msgs := problem.Errors["currency"]; len(msgs) != 1 || msgs[0] != want {
		t.Errorf("errors[currency] = %v, want [%q]", msgs, want)
	}
}

func TestPutCustomersByIdBillingProfile_InvalidLanguage_ReturnsBadRequest(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "Bad Language Co")

	r := putBillingProfile(t, c, created.Id, map[string]any{"language": "fr"})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	want := "A document language must be one of 'nb' or 'en', but was 'fr'"
	if msgs := problem.Errors["language"]; len(msgs) != 1 || msgs[0] != want {
		t.Errorf("errors[language] = %v, want [%q]", msgs, want)
	}
}

func TestPutCustomersByIdBillingProfile_InvalidInvoiceDelivery_ReturnsBadRequest(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "Bad Invoice Delivery Co")

	r := putBillingProfile(t, c, created.Id, map[string]any{"invoiceDelivery": "fax"})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	want := "An invoice delivery method must be one of 'email', 'ehf', 'efaktura' or 'paper', but was 'fax'"
	if msgs := problem.Errors["invoiceDelivery"]; len(msgs) != 1 || msgs[0] != want {
		t.Errorf("errors[invoiceDelivery] = %v, want [%q]", msgs, want)
	}
}

func TestPutCustomersByIdBillingProfile_InvalidReminderDelivery_ReturnsBadRequest(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "Bad Reminder Delivery Co")

	r := putBillingProfile(t, c, created.Id, map[string]any{"reminderDelivery": "ehf"})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	want := "A reminder delivery method must be one of 'email' or 'paper', but was 'ehf'"
	if msgs := problem.Errors["reminderDelivery"]; len(msgs) != 1 || msgs[0] != want {
		t.Errorf("errors[reminderDelivery] = %v, want [%q]", msgs, want)
	}
}

func TestPutCustomersByIdBillingProfile_InvalidPeppolId_ReturnsBadRequest(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "Bad Peppol Co")

	r := putBillingProfile(t, c, created.Id, map[string]any{"peppolId": "not-a-peppol-id"})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	want := "A Peppol participant id must look like 0192:923609016 (a four-digit scheme, a colon, an identifier), but was 'not-a-peppol-id'"
	if msgs := problem.Errors["peppolId"]; len(msgs) != 1 || msgs[0] != want {
		t.Errorf("errors[peppolId] = %v, want [%q]", msgs, want)
	}
}

// TestPutCustomersByIdBillingProfile_Scheme0192InvalidOrgNumber_ReusesTheOrgNumberMessage
// pins the controller ruling: scheme 0192 with a bad number reuses the
// organisation-number message end to end, through the HTTP layer.
func TestPutCustomersByIdBillingProfile_Scheme0192InvalidOrgNumber_ReusesTheOrgNumberMessage(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "Bad Org Number Peppol Co")

	r := putBillingProfile(t, c, created.Id, map[string]any{"peppolId": "0192:923609017"})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	want := "A Norwegian organisation number must be nine digits with a valid check digit, but was '923609017'"
	if msgs := problem.Errors["peppolId"]; len(msgs) != 1 || msgs[0] != want {
		t.Errorf("errors[peppolId] = %v, want [%q]", msgs, want)
	}
}

func TestPutCustomersByIdBillingProfile_InvalidGln_ReturnsBadRequest(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "Bad GLN Co")

	r := putBillingProfile(t, c, created.Id, map[string]any{"gln": "not-a-gln"})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	want := "A GLN must be 13 digits with a valid check digit, but was 'not-a-gln'"
	if msgs := problem.Errors["gln"]; len(msgs) != 1 || msgs[0] != want {
		t.Errorf("errors[gln] = %v, want [%q]", msgs, want)
	}
}

func TestPutCustomersByIdBillingProfile_InvalidInvoiceEmail_ReturnsBadRequest(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "Bad Invoice Email Co")

	r := putBillingProfile(t, c, created.Id, map[string]any{"invoiceEmail": "not-an-email"})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	want := "An email address must look like name@example.com, but was 'not-an-email'"
	if msgs := problem.Errors["invoiceEmail"]; len(msgs) != 1 || msgs[0] != want {
		t.Errorf("errors[invoiceEmail] = %v, want [%q]", msgs, want)
	}
}

func TestPutCustomersByIdBillingProfile_InvalidReminderEmail_ReturnsBadRequest(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "Bad Reminder Email Co")

	r := putBillingProfile(t, c, created.Id, map[string]any{"reminderEmail": "not-an-email"})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	want := "An email address must look like name@example.com, but was 'not-an-email'"
	if msgs := problem.Errors["reminderEmail"]; len(msgs) != 1 || msgs[0] != want {
		t.Errorf("errors[reminderEmail] = %v, want [%q]", msgs, want)
	}
}

func TestPutCustomersByIdBillingProfile_TooLongBuyerReference_ReturnsBadRequest(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "Bad Buyer Reference Co")

	tooLong := ""
	for i := 0; i < 101; i++ {
		tooLong += "a"
	}
	r := putBillingProfile(t, c, created.Id, map[string]any{"buyerReference": tooLong})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	want := "A buyer reference cannot be longer than 100 characters, the given value was 101 characters"
	if msgs := problem.Errors["buyerReference"]; len(msgs) != 1 || msgs[0] != want {
		t.Errorf("errors[buyerReference] = %v, want [%q]", msgs, want)
	}
}

func TestPutCustomersByIdBillingProfile_WithStaleRevision_ReturnsConflictAndLeavesRowUntouched(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "Stale Rev Billing Co")
	before := fetchBillingProfile(t, c, created.Id)
	h.Advance(time.Second)

	r := putBillingProfile(t, c, created.Id, map[string]any{"currency": "NOK", "revision": 999})
	if r.Status != http.StatusConflict {
		t.Fatalf("status %d body %s, want 409", r.Status, r.Body)
	}
	var problem conflictProblemJSON
	r.JSON(&problem)
	if problemTitle(problem.Title) != "Customer revision conflict" {
		t.Errorf("Title = %q, want %q", problemTitle(problem.Title), "Customer revision conflict")
	}
	wantDetail := "The customer has been changed since revision 999 was read; it is now at revision 1."
	if problemTitle(problem.Detail) != wantDetail {
		t.Errorf("Detail = %q, want %q", problemTitle(problem.Detail), wantDetail)
	}
	if problem.Code != nil {
		t.Errorf("Code = %v, want nil: a revision conflict carries no code", problem.Code)
	}

	after := fetchBillingProfile(t, c, created.Id)
	if after.Currency != nil {
		t.Errorf("Currency = %v, want unchanged nil", after.Currency)
	}
	if after.Revision != before.Revision {
		t.Errorf("revision = %d, want unchanged %d", after.Revision, before.Revision)
	}
	if n := countTimelineEvents(t, h, created.Id, "customer.billing_profile_updated"); n != 0 {
		t.Errorf("customer.billing_profile_updated events = %d, want 0", n)
	}
}

// TestPutCustomersByIdBillingProfile_NoOp_DoesNotBumpRevisionOrRecordEvent
// resubmits exactly what is already stored: the no-op rule (customers
// foundation design D5) says this writes nothing at all, so only the first,
// real write's event exists afterward.
func TestPutCustomersByIdBillingProfile_NoOp_DoesNotBumpRevisionOrRecordEvent(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "NoOp Billing Co")

	r := putBillingProfile(t, c, created.Id, map[string]any{"currency": "NOK", "revision": 1})
	if r.Status != http.StatusOK {
		t.Fatalf("set: status %d body %s, want 200", r.Status, r.Body)
	}
	before := fetchBillingProfile(t, c, created.Id)
	h.Advance(time.Second)

	r = putBillingProfile(t, c, created.Id, map[string]any{"currency": "NOK", "revision": before.Revision})
	if r.Status != http.StatusOK {
		t.Fatalf("resubmit: status %d body %s, want 200", r.Status, r.Body)
	}
	after := fetchBillingProfile(t, c, created.Id)
	if after.Revision != before.Revision {
		t.Errorf("revision = %d, want unchanged %d", after.Revision, before.Revision)
	}
	if n := countTimelineEvents(t, h, created.Id, "customer.billing_profile_updated"); n != 1 {
		t.Errorf("customer.billing_profile_updated events = %d, want 1 (only from the first, real write)", n)
	}
}

// TestPutCustomersByIdBillingProfile_ChangesFields_BumpsRevisionAndRecordsEventWithActorAndPayload
// proves the real-write path end to end: exactly one revision bump, one
// customer.billing_profile_updated event attributed to the signed-in caller
// (customers foundation design D1), with the changed field's before/after in
// the payload.
func TestPutCustomersByIdBillingProfile_ChangesFields_BumpsRevisionAndRecordsEventWithActorAndPayload(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, userID := authenticatedClientWithID(t, h)
	created := createCustomer(t, c, "Changed Billing Co")

	r := putBillingProfile(t, c, created.Id, map[string]any{"currency": "NOK", "paymentTermsDays": 14})
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var updated billingProfileJSON
	r.JSON(&updated)
	if updated.Revision != 2 {
		t.Errorf("revision = %d, want 2", updated.Revision)
	}
	if got := fetchBillingProfile(t, c, created.Id); got.Revision != 2 {
		t.Errorf("persisted revision = %d, want 2", got.Revision)
	}

	if n := countTimelineEvents(t, h, created.Id, "customer.billing_profile_updated"); n != 1 {
		t.Fatalf("customer.billing_profile_updated events = %d, want 1", n)
	}
	event := fetchTimelineEvent(t, h, created.Id, "customer.billing_profile_updated")
	before, _ := event.Payload["before"].(map[string]any)
	after, _ := event.Payload["after"].(map[string]any)
	if before == nil || before["currency"] != nil {
		t.Errorf("before.currency = %v, want nil", before["currency"])
	}
	if after == nil || after["currency"] != "NOK" {
		t.Errorf("after.currency = %v, want NOK", after["currency"])
	}
	if after["paymentTermsDays"] != float64(14) {
		t.Errorf("after.paymentTermsDays = %v, want 14", after["paymentTermsDays"])
	}

	gotActor := modtest.One[string](t, h, `
		SELECT actor_user_id::text FROM customers.customers_timeline_entries
		WHERE customer_id = $1 AND event_type = 'customer.billing_profile_updated'`, created.Id)
	if gotActor != userID.String() {
		t.Errorf("actor_user_id = %s, want %s (the signed-in caller)", gotActor, userID)
	}
}

func TestPutCustomersByIdBillingProfile_WhenCustomerDoesNotExist_ReturnsNotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	r := putBillingProfile(t, c, 999999, map[string]any{"currency": "NOK"})
	if r.Status != http.StatusNotFound {
		t.Errorf("status %d body %s, want 404", r.Status, r.Body)
	}
}

// TestPutCustomersByIdBillingProfile_WithoutBillingManagePermission_ReturnsForbidden
// pins D1's own point: customers:billing-manage is a permission distinct
// from customers:update — a caller with only customers:update (and view)
// cannot write the billing profile, even though it can PUT the customer
// itself and its contact info.
func TestPutCustomersByIdBillingProfile_WithoutBillingManagePermission_ReturnsForbidden(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "customers:view", "customers:create", "customers:update")
	created := createCustomer(t, c, "No Billing Manage Co")

	r := putBillingProfile(t, c, created.Id, map[string]any{"currency": "NOK"})
	if r.Status != http.StatusForbidden {
		t.Errorf("status %d body %s, want 403", r.Status, r.Body)
	}

	// The same caller can still GET it (customers:view alone).
	getR := getBillingProfile(t, c, created.Id)
	if getR.Status != http.StatusOK {
		t.Errorf("GET status %d body %s, want 200", getR.Status, getR.Body)
	}
}

// TestGetCustomer_ResponseNeverCarriesBillingFields proves the controller
// ruling: SafeCustomerResponse (GET /customers/{id} and the list) never
// exposes any billing-profile field, however it was set.
func TestGetCustomer_ResponseNeverCarriesBillingFields(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "Billing Not Leaked Co")

	r := putBillingProfile(t, c, created.Id, map[string]any{
		"currency": "NOK", "invoiceEmail": "invoice@notleaked.co", "gln": "4006381333931",
	})
	if r.Status != http.StatusOK {
		t.Fatalf("set billing profile: status %d body %s, want 200", r.Status, r.Body)
	}

	getR := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d", created.Id), nil)
	if getR.Status != http.StatusOK {
		t.Fatalf("get customer: status %d body %s, want 200", getR.Status, getR.Body)
	}
	if body := getR.Body; containsAny(body, "invoiceEmail", "\"gln\"", "peppolId", "buyerReference", "paymentTermsDays") {
		t.Errorf("GET /customers/{id} body leaks a billing field: %s", body)
	}

	rawList := c.Do(http.MethodGet, "/api/v1/customers", nil)
	if rawList.Status != http.StatusOK {
		t.Fatalf("list customers: status %d body %s, want 200", rawList.Status, rawList.Body)
	}
	if body := rawList.Body; containsAny(body, "invoiceEmail", "\"gln\"", "peppolId", "buyerReference", "paymentTermsDays") {
		t.Errorf("GET /customers body leaks a billing field: %s", body)
	}
}

// TestBillingWarnings_EndToEnd_EHFWithNorwegianBusinessIdentity proves
// ehf_without_recipient does not fire when the customer has a Norwegian
// business legal identity to derive a recipient from, even with no peppolId
// set on the profile itself, exercised through the full HTTP path (the
// table-driven unit coverage lives in billing_values_test.go's
// TestBillingWarnings_EHFWithoutRecipient).
func TestBillingWarnings_EndToEnd_EHFWithNorwegianBusinessIdentity(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomerWithIdentity(t, c, "EHF Recipient Co", "no", "923609016")

	r := putBillingProfile(t, c, created.Id, map[string]any{"invoiceDelivery": "ehf"})
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var updated billingProfileJSON
	r.JSON(&updated)
	if hasWarning(updated.Warnings, "ehf_without_recipient") {
		t.Errorf("warnings = %v, want no ehf_without_recipient (NO business identity derives a recipient)", updated.Warnings)
	}
}

// TestBillingWarnings_EndToEnd_EmailDeliverySatisfiedByContactInfoEmail
// proves email_without_address does not fire when the customer's own
// contact-info email is set, even with no billing invoiceEmail of its own.
func TestBillingWarnings_EndToEnd_EmailDeliverySatisfiedByContactInfoEmail(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "Email Via Contact Co")

	r := putBillingProfile(t, c, created.Id, map[string]any{"invoiceDelivery": "email"})
	if r.Status != http.StatusOK {
		t.Fatalf("set delivery: status %d body %s, want 200", r.Status, r.Body)
	}
	var withoutContact billingProfileJSON
	r.JSON(&withoutContact)
	if !hasWarning(withoutContact.Warnings, "email_without_address") {
		t.Errorf("warnings = %v, want email_without_address before any email is set", withoutContact.Warnings)
	}

	cr := putContactInfo(t, c, created.Id, map[string]any{"email": "hello@emailviacontact.co"})
	if cr.Status != http.StatusOK {
		t.Fatalf("set contact info: status %d body %s, want 200", cr.Status, cr.Body)
	}

	got := fetchBillingProfile(t, c, created.Id)
	if hasWarning(got.Warnings, "email_without_address") {
		t.Errorf("warnings = %v, want no email_without_address (contact-info email set)", got.Warnings)
	}
}

// TestBillingWarnings_EndToEnd_EfakturaForBusiness proves
// efaktura_for_business fires for a business customer with delivery set to
// efaktura, and does not for a person customer.
func TestBillingWarnings_EndToEnd_EfakturaForBusiness(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	business := createCustomerOfType(t, c, "Efaktura Business Co", "business")
	r := putBillingProfile(t, c, business.Id, map[string]any{"invoiceDelivery": "efaktura"})
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var businessProfile billingProfileJSON
	r.JSON(&businessProfile)
	if !hasWarning(businessProfile.Warnings, "efaktura_for_business") {
		t.Errorf("warnings = %v, want efaktura_for_business", businessProfile.Warnings)
	}

	person := createCustomerOfType(t, c, "Efaktura Person Co", "person")
	r = putBillingProfile(t, c, person.Id, map[string]any{"invoiceDelivery": "efaktura"})
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var personProfile billingProfileJSON
	r.JSON(&personProfile)
	if hasWarning(personProfile.Warnings, "efaktura_for_business") {
		t.Errorf("warnings = %v, want no efaktura_for_business for a person customer", personProfile.Warnings)
	}
}

// TestBillingWarnings_EndToEnd_AddingAPrimaryPostalAddressClearsNoInvoiceAddress
// proves no_invoice_address clears once the customer gains a primary postal
// address, even without a dedicated invoice-type address (D3's resolution
// order: primary invoice, else primary postal).
func TestBillingWarnings_EndToEnd_AddingAPrimaryPostalAddressClearsNoInvoiceAddress(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "Gains An Address Co")

	before := fetchBillingProfile(t, c, created.Id)
	if !hasWarning(before.Warnings, "no_invoice_address") {
		t.Errorf("warnings = %v, want no_invoice_address before any address exists", before.Warnings)
	}

	ar := postAddress(t, c, created.Id, map[string]any{
		"type": "postal", "line1": "Storgata 1", "postalCode": "0155", "city": "Oslo", "country": "no",
	})
	if ar.Status != http.StatusCreated {
		t.Fatalf("create address: status %d body %s, want 201", ar.Status, ar.Body)
	}

	after := fetchBillingProfile(t, c, created.Id)
	if hasWarning(after.Warnings, "no_invoice_address") {
		t.Errorf("warnings = %v, want no no_invoice_address (a primary postal address now exists)", after.Warnings)
	}
}

// hasWarning reports whether warnings contains code.
func hasWarning(warnings []string, code string) bool {
	return slices.Contains(warnings, code)
}

func containsAny(body []byte, substrs ...string) bool {
	for _, s := range substrs {
		if contains(string(body), s) {
			return true
		}
	}
	return false
}
