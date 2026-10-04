package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/mail"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/storage"
)

// The customers + invoices tests (payments and delivery design D9): both
// modules real, so every gate, prefill and snapshot Invoices takes from a
// customer is taken from the customers module's own directory — not the
// invoices suite's fake of it — and every merge, export and anonymisation
// customers runs reaches the invoices module's own holder and slot. Each
// module's suite proves its own rules against an imitation of the other; what
// only this package can prove is that the two agree when composed, exactly
// as cmd/vantigo composes them.

const (
	invoicesBase = "/api/v1/invoices"
	// sellerEmail is the seller's own address, what a send's Reply-To names.
	sellerEmail = "faktura@kraft-verket.no"
)

// The days these tests write and expect, from the clock every installation
// starts at: today is modtest.Start's day, the issue's and a payment's, and
// a draft's delivery two days before it.
var (
	invoiceToday     = modtest.Start.Format(time.DateOnly)
	invoiceDelivered = modtest.Start.AddDate(0, 0, -2).Format(time.DateOnly)
)

// smtpRecorder stands in for mail.SendOutbound: every envelope it is handed
// is kept for the test to read.
type smtpRecorder struct {
	mu   sync.Mutex
	sent []mail.Outbound
}

func (s *smtpRecorder) send(_ context.Context, _ config.MailConfig, out mail.Outbound) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, out)
	return nil
}

func (s *smtpRecorder) mails() []mail.Outbound {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.sent)
}

// invoicesInstallation is the real customers and invoices modules composed
// together, with a real file-system object store over a temporary directory
// (the issue stores the PDF the send attaches) and the smtp driver configured
// with the SMTP seam recorded — config.Load refuses the smtp driver without a
// host and a from. The seller is saved complete, its e-mail sellerEmail, and
// the one caller answered may do everything both modules' fixtures need.
func invoicesInstallation(t *testing.T) (*modtest.Harness, *modtest.Client, *smtpRecorder) {
	t.Helper()
	objects, err := storage.NewFS(t.TempDir(), true, true)
	if err != nil {
		t.Fatalf("storage.NewFS: %v", err)
	}
	smtp := &smtpRecorder{}
	h := newInstallationWith(t, []modtest.Option{
		modtest.WithObjectStore(objects),
		modtest.WithSMTPSend(smtp.send),
		modtest.WithEnv("MAIL_DRIVER", "smtp"),
		modtest.WithEnv("SMTP_HOST", "smtp.example.invalid"),
		modtest.WithEnv("SMTP_FROM", "faktura@example.invalid"),
	}, modCustomers, modInvoices)
	admin := h.SignIn(t,
		"customers:view", "customers:create", "customers:update", "customers:delete", "customers:merge",
		"customers:billing-manage", "customers:legal-identity-view", "customers:legal-identity-manage",
		"customers:personal-data", "customers:timeline-view",
		"invoices:access", "invoices:create", "invoices:issue", "invoices:manage", "invoices:payments",
	)
	okJSON(t, admin, http.MethodPut, invoicesBase+"/settings", map[string]any{
		"legalName": "Kraft-Verket AS", "organisationNumber": "974 760 673",
		"vatRegistered": true, "inForetaksregisteret": true,
		"addressLine1": "Storgata 1", "addressLine2": "", "postalCode": "0155", "city": "Oslo", "country": "no",
		"bankAccount": "8601.11.17947", "iban": "NO93 8601 1117 947", "bic": "dnbanokkxxx",
		"email": sellerEmail, "defaultPaymentTermsDays": 14, "defaultCurrency": "NOK",
		"footerText": "Takk for handelen.", "seriesStart": 1,
		"peppolId": nil, "kidLength": nil, "kidAlgorithm": nil, "revision": 1,
	}, nil)
	return h, admin, smtp
}

// invoiceCustomer is POST /customers's answer.
type invoiceCustomer struct {
	Id             int32 `json:"id"`
	CustomerNumber int64 `json:"customerNumber"`
}

// invoiceNewBusiness creates a Norwegian business through the customers API, its
// legal identity orgNumber registered under legalName, with an invoice
// address.
func invoiceNewBusiness(t *testing.T, c *modtest.Client, name, legalName, orgNumber string) invoiceCustomer {
	t.Helper()
	var created invoiceCustomer
	okJSON(t, c, http.MethodPost, "/api/v1/customers", map[string]any{
		"name": name,
		"identity": map[string]any{
			"country": "no", "type": "business", "id": orgNumber, "name": legalName, "source": "manual",
		},
	}, &created)
	invoiceAddress(t, c, created.Id)
	return created
}

// invoiceBillingProfile replaces a customer's billing profile through the customers
// API.
func invoiceBillingProfile(t *testing.T, c *modtest.Client, id int32, profile map[string]any) {
	t.Helper()
	okJSON(t, c, http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/billing-profile", id), profile, nil)
}

// invoiceAddress gives a customer its invoice address through the customers
// API; the first of its type is the primary, which the directory resolves.
func invoiceAddress(t *testing.T, c *modtest.Client, id int32) {
	t.Helper()
	okJSON(t, c, http.MethodPost, fmt.Sprintf("/api/v1/customers/%d/addresses", id), map[string]any{
		"type": "invoice", "line1": "Kaiveien 3", "postalCode": "5003", "city": "Bergen", "country": "no",
	}, nil)
}

// invoiceDoc is one invoices document as these tests read it.
type invoiceDoc struct {
	ID               int64   `json:"id"`
	Kind             string  `json:"kind"`
	Status           string  `json:"status"`
	Number           *int64  `json:"number"`
	CustomerID       int32   `json:"customerId"`
	CustomerName     *string `json:"customerName"`
	IssueDate        *string `json:"issueDate"`
	DueDate          *string `json:"dueDate"`
	PaymentTermsDays *int32  `json:"paymentTermsDays"`
	YourReference    string  `json:"yourReference"`
	GrossTotal       float64 `json:"grossTotal"`
	Buyer            *struct {
		CustomerNumber     int64   `json:"customerNumber"`
		Type               string  `json:"type"`
		Name               string  `json:"name"`
		OrganisationNumber *string `json:"organisationNumber"`
		AddressLine1       *string `json:"addressLine1"`
		PostalCode         *string `json:"postalCode"`
		City               *string `json:"city"`
		Country            *string `json:"country"`
		Language           string  `json:"language"`
	} `json:"buyer"`
	Credits *struct {
		ID int64 `json:"id"`
	} `json:"credits"`
	Payments []struct {
		ID     int64   `json:"id"`
		Amount float64 `json:"amount"`
		Note   string  `json:"note"`
	} `json:"payments"`
	Deliveries []struct {
		Recipient *string `json:"recipient"`
		Subject   string  `json:"subject"`
	} `json:"deliveries"`
}

func invoiceAt(id int64) string { return fmt.Sprintf("%s/%d", invoicesBase, id) }

// draftFor is a create body for customer: ten hours at 1000 on the standard
// 25 % code (the first the invoices migration seeds, id 1), delivered on
// invoiceDelivered.
func draftFor(customer int32) map[string]any {
	return map[string]any{
		"customerId": customer, "deliveryDate": invoiceDelivered,
		"lines": []map[string]any{{"description": "Konsulenttime", "quantity": 10, "unit": "timer", "unitPrice": 1000, "vatCodeId": 1}},
	}
}

// newDraft creates an invoice draft for customer.
func newDraft(t *testing.T, c *modtest.Client, customer int32) invoiceDoc {
	t.Helper()
	var doc invoiceDoc
	okJSON(t, c, http.MethodPost, invoicesBase, draftFor(customer), &doc)
	return doc
}

// issueDoc issues document id today.
func issueDoc(t *testing.T, c *modtest.Client, id int64) invoiceDoc {
	t.Helper()
	var doc invoiceDoc
	okJSON(t, c, http.MethodPost, invoiceAt(id)+"/issue", map[string]any{}, &doc)
	if doc.Status != "issued" || doc.Number == nil {
		t.Fatalf("issue %d = status %s number %v, want issued and numbered", id, doc.Status, doc.Number)
	}
	return doc
}

// readDoc reads document id.
func readDoc(t *testing.T, c *modtest.Client, id int64) invoiceDoc {
	t.Helper()
	var doc invoiceDoc
	okJSON(t, c, http.MethodGet, invoiceAt(id), nil, &doc)
	return doc
}

// invoiceRefusedAs asserts res is a 409 with code.
func invoiceRefusedAs(t *testing.T, what string, res *modtest.Response, code string) {
	t.Helper()
	if res.Status != http.StatusConflict {
		t.Errorf("%s = %d %s, want 409 %s", what, res.Status, res.Body, code)
		return
	}
	var p struct {
		Code string `json:"code"`
	}
	res.JSON(&p)
	if p.Code != code {
		t.Errorf("%s = 409 %s, want %s", what, p.Code, code)
	}
}

// invoiceKindCount is one {kind, count} entry of customer.merged's moved list,
// the merge response's moved list and customer.anonymised's erased list.
type invoiceKindCount struct {
	Kind  string `json:"kind"`
	Count int64  `json:"count"`
}

// invoiceNewestEvent is the newest entry of a customer's timeline, which must be
// of eventType, its payload decoded into payload.
func invoiceNewestEvent(t *testing.T, c *modtest.Client, customer int32, eventType string, payload any) {
	t.Helper()
	var timeline struct {
		Data []struct {
			EventType string          `json:"eventType"`
			Payload   json.RawMessage `json:"payload"`
		} `json:"data"`
	}
	okJSON(t, c, http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/timeline", customer), nil, &timeline)
	if len(timeline.Data) == 0 || timeline.Data[0].EventType != eventType {
		t.Fatalf("customer %d's newest timeline entry = %+v, want %s", customer, timeline.Data, eventType)
	}
	if err := json.Unmarshal(timeline.Data[0].Payload, payload); err != nil {
		t.Fatalf("%s's payload %s: %v", eventType, timeline.Data[0].Payload, err)
	}
}

func invoiceStr(s *string) string {
	if s == nil {
		return "<absent>"
	}
	return *s
}

// TestInvoices_ADraftTakesTheRealProfileAndTheSnapshotIsTheCustomer: a
// customer made through the customers API — its legal identity, its invoice
// address and a billing profile with 30 days and a buyer reference — is
// invoiced: the draft takes the profile's terms and reference from the real
// directory, and the issue's buyer snapshot is the customer's own data as
// customers resolves it (the legal name over the display name, the
// organisation number, the invoice address, the customer number).
func TestInvoices_ADraftTakesTheRealProfileAndTheSnapshotIsTheCustomer(t *testing.T) {
	t.Parallel()
	_, admin, _ := invoicesInstallation(t)
	customer := invoiceNewBusiness(t, admin, "Fjord Nord", "Fjord Nord AS", "923609016")
	invoiceBillingProfile(t, admin, customer.Id, map[string]any{
		"invoiceEmail": "faktura@fjordnord.example", "paymentTermsDays": 30, "buyerReference": "PO-4711",
	})

	draft := newDraft(t, admin, customer.Id)
	if draft.YourReference != "PO-4711" || draft.PaymentTermsDays == nil || *draft.PaymentTermsDays != 30 {
		t.Errorf("the draft's prefills = %q, %v; want the profile's PO-4711 and 30 days", draft.YourReference, draft.PaymentTermsDays)
	}
	if draft.CustomerName == nil || *draft.CustomerName != "Fjord Nord" {
		t.Errorf("the draft's customerName = %s, want the customer's own name", invoiceStr(draft.CustomerName))
	}

	doc := issueDoc(t, admin, draft.ID)
	due := modtest.Start.AddDate(0, 0, 30).Format(time.DateOnly)
	if invoiceStr(doc.IssueDate) != invoiceToday || invoiceStr(doc.DueDate) != due {
		t.Errorf("issued %s due %s, want %s due 30 days on, %s", invoiceStr(doc.IssueDate), invoiceStr(doc.DueDate), invoiceToday, due)
	}
	b := doc.Buyer
	if b == nil {
		t.Fatal("the issued invoice has no buyer snapshot")
	}
	if b.CustomerNumber != customer.CustomerNumber || b.Type != "business" || b.Name != "Fjord Nord AS" ||
		invoiceStr(b.OrganisationNumber) != "923609016" || b.Language != "nb" {
		t.Errorf("the buyer = #%d %s %q %s %s, want #%d business \"Fjord Nord AS\" 923609016 nb",
			b.CustomerNumber, b.Type, b.Name, invoiceStr(b.OrganisationNumber), b.Language, customer.CustomerNumber)
	}
	if invoiceStr(b.AddressLine1) != "Kaiveien 3" || invoiceStr(b.PostalCode) != "5003" || invoiceStr(b.City) != "Bergen" || invoiceStr(b.Country) != "NO" {
		t.Errorf("the buyer's address = %s, %s %s, %s; want the customer's invoice address",
			invoiceStr(b.AddressLine1), invoiceStr(b.PostalCode), invoiceStr(b.City), invoiceStr(b.Country))
	}
}

// TestInvoices_ADisabledCustomerIsRefusedANewDraftButCredited: a customer
// disabled through the customers API is refused a new draft — the gate reads
// the real directory's status — while its issued invoice is still corrected:
// the credit note is created and issued, naming the original's buyer.
func TestInvoices_ADisabledCustomerIsRefusedANewDraftButCredited(t *testing.T) {
	t.Parallel()
	_, admin, _ := invoicesInstallation(t)
	customer := invoiceNewBusiness(t, admin, "Sperret AS", "Sperret AS", "923609016")
	original := issueDoc(t, admin, newDraft(t, admin, customer.Id).ID)

	var updated struct {
		Status string `json:"status"`
	}
	okJSON(t, admin, http.MethodPut, fmt.Sprintf("/api/v1/customers/%d", customer.Id),
		map[string]any{"name": "Sperret AS", "status": "disabled"}, &updated)
	if updated.Status != "disabled" {
		t.Fatalf("the customer's status = %q, want disabled", updated.Status)
	}

	invoiceRefusedAs(t, "a new draft for the disabled customer", admin.Do(http.MethodPost, invoicesBase, draftFor(customer.Id)), "customer_blocked")

	var creditDraft invoiceDoc
	okJSON(t, admin, http.MethodPost, invoiceAt(original.ID)+"/credit", nil, &creditDraft)
	credit := issueDoc(t, admin, creditDraft.ID)
	if credit.Kind != "credit_note" || credit.Credits == nil || credit.Credits.ID != original.ID {
		t.Errorf("the credit note = %s crediting %+v, want a credit note of %d", credit.Kind, credit.Credits, original.ID)
	}
	if credit.Buyer == nil || original.Buyer == nil || credit.Buyer.Name != original.Buyer.Name || credit.GrossTotal != original.GrossTotal {
		t.Errorf("the credit note = buyer %+v gross %v, want the original's buyer and %v", credit.Buyer, credit.GrossTotal, original.GrossTotal)
	}
}

// TestInvoices_AMergeRepointsTheRealDocuments: a merge through the customers
// API reaches the invoices module's own reference holder in the merge's
// transaction: the duplicate's issued invoice and its draft both name the
// survivor afterwards, the merge's moved list and customer.merged's own name
// invoices.invoices with both, the issued document keeps the buyer it
// printed, and a new draft for the duplicate is refused as merged.
func TestInvoices_AMergeRepointsTheRealDocuments(t *testing.T) {
	t.Parallel()
	_, admin, _ := invoicesInstallation(t)
	survivor := invoiceNewBusiness(t, admin, "Acme AS", "Acme AS", "923609016")
	absorbed := invoiceNewBusiness(t, admin, "Acme Norge AS", "Acme Norge AS", "987654325")
	issued := issueDoc(t, admin, newDraft(t, admin, absorbed.Id).ID)
	draft := newDraft(t, admin, absorbed.Id)

	var merged struct {
		Moved []invoiceKindCount `json:"moved"`
	}
	okJSON(t, admin, http.MethodPost, fmt.Sprintf("/api/v1/customers/%d/merge", survivor.Id), map[string]any{"sourceId": absorbed.Id}, &merged)
	if !slices.Contains(merged.Moved, invoiceKindCount{Kind: "invoices.invoices", Count: 2}) {
		t.Errorf("the merge's moved = %+v, want invoices.invoices 2 from the real holder", merged.Moved)
	}
	var event struct {
		Moved []invoiceKindCount `json:"moved"`
	}
	invoiceNewestEvent(t, admin, survivor.Id, "customer.merged", &event)
	if !slices.Contains(event.Moved, invoiceKindCount{Kind: "invoices.invoices", Count: 2}) {
		t.Errorf("customer.merged's moved = %+v, want invoices.invoices 2", event.Moved)
	}

	for _, id := range []int64{issued.ID, draft.ID} {
		if got := readDoc(t, admin, id); got.CustomerID != survivor.Id {
			t.Errorf("document %d names customer %d, want the survivor %d", id, got.CustomerID, survivor.Id)
		}
	}
	if got := readDoc(t, admin, issued.ID); got.Buyer == nil || got.Buyer.Name != "Acme Norge AS" || got.Buyer.CustomerNumber != absorbed.CustomerNumber {
		t.Errorf("the issued document's buyer = %+v, want the snapshot it printed: Acme Norge AS #%d", got.Buyer, absorbed.CustomerNumber)
	}
	invoiceRefusedAs(t, "a new draft for the duplicate", admin.Do(http.MethodPost, invoicesBase, draftFor(absorbed.Id)), "customer_merged")
}

// TestInvoices_ASendReachesTheProfilesAddressWithTheSellersReplyTo: the send
// mails the invoice e-mail the customer's billing profile names — read from
// the real directory — with the PDF the issue stored in the real object
// store, under the seller's name with Reply-To the seller's e-mail saved
// through PUT /invoices/settings; and the delivery is on the document, its
// recipient answered to a sender.
func TestInvoices_ASendReachesTheProfilesAddressWithTheSellersReplyTo(t *testing.T) {
	t.Parallel()
	_, admin, smtp := invoicesInstallation(t)
	customer := invoiceNewBusiness(t, admin, "Kunde AS", "Kunde AS", "923609016")
	invoiceBillingProfile(t, admin, customer.Id, map[string]any{"invoiceEmail": "faktura@kunde.example"})
	doc := issueDoc(t, admin, newDraft(t, admin, customer.Id).ID)

	var sent invoiceDoc
	okJSON(t, admin, http.MethodPost, invoiceAt(doc.ID)+"/send", map[string]any{}, &sent)

	mails := smtp.mails()
	if len(mails) != 1 {
		t.Fatalf("%d mails sent, want 1", len(mails))
	}
	out := mails[0]
	if !slices.Equal(out.To, []string{"faktura@kunde.example"}) || out.ReplyTo != sellerEmail || out.DisplayName != "Kraft-Verket AS" {
		t.Errorf("the envelope = to %v reply-to %q from %q, want the profile's faktura@kunde.example, reply-to %s, from Kraft-Verket AS",
			out.To, out.ReplyTo, out.DisplayName, sellerEmail)
	}
	if len(out.Attachments) != 1 || !bytes.HasPrefix(out.Attachments[0].Content, []byte("%PDF")) {
		t.Errorf("%d attachments, want the stored PDF", len(out.Attachments))
	}
	got := readDoc(t, admin, doc.ID)
	if len(got.Deliveries) != 1 || invoiceStr(got.Deliveries[0].Recipient) != "faktura@kunde.example" || got.Deliveries[0].Subject == "" {
		t.Errorf("the document's deliveries = %+v, want the one send to faktura@kunde.example", got.Deliveries)
	}
}

// TestInvoices_TheExportAndTheAnonymisation: a person's export through the
// customers API carries the invoices module's section — the issued document
// with its payment and its delivery, and the draft; then the anonymisation
// worker, built by module.Workers with invoices among the modules exactly as
// worker mode builds it, run once: the drafts are deleted, the document is
// kept, its delivery's recipient and its payment's note are blanked,
// customer.anonymised lists the invoices module's four kinds, and a send
// afterwards is refused.
func TestInvoices_TheExportAndTheAnonymisation(t *testing.T) {
	t.Parallel()
	h, admin, _ := invoicesInstallation(t)
	var person invoiceCustomer
	okJSON(t, admin, http.MethodPost, "/api/v1/customers", map[string]any{"name": "Kari Nordmann", "type": "person"}, &person)
	invoiceAddress(t, admin, person.Id)
	invoiceBillingProfile(t, admin, person.Id, map[string]any{"invoiceEmail": "kari@example.org"})
	doc := issueDoc(t, admin, newDraft(t, admin, person.Id).ID)
	okJSON(t, admin, http.MethodPost, invoiceAt(doc.ID)+"/payments", map[string]any{"amount": 500, "paidOn": invoiceToday, "note": "Kari ringte"}, nil)
	okJSON(t, admin, http.MethodPost, invoiceAt(doc.ID)+"/send", map[string]any{}, nil)
	draft := newDraft(t, admin, person.Id)

	var file struct {
		Modules map[string]json.RawMessage `json:"modules"`
	}
	okJSON(t, admin, http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/personal-data", person.Id), nil, &file)
	var section struct {
		Documents []struct {
			Number   *int64 `json:"number"`
			Payments []struct {
				Amount string `json:"amount"`
				Note   string `json:"note"`
			} `json:"payments"`
			Deliveries []struct {
				Recipient string `json:"recipient"`
			} `json:"deliveries"`
		} `json:"documents"`
		Drafts []struct {
			Kind string `json:"kind"`
		} `json:"drafts"`
	}
	if err := json.Unmarshal(file.Modules["invoices"], &section); err != nil {
		t.Fatalf("modules.invoices = %s: %v", file.Modules["invoices"], err)
	}
	if len(section.Documents) != 1 || section.Documents[0].Number == nil || *section.Documents[0].Number != *doc.Number ||
		len(section.Documents[0].Payments) != 1 || section.Documents[0].Payments[0].Amount != "500.00" ||
		section.Documents[0].Payments[0].Note != "Kari ringte" ||
		len(section.Documents[0].Deliveries) != 1 || section.Documents[0].Deliveries[0].Recipient != "kari@example.org" ||
		len(section.Drafts) != 1 {
		t.Errorf("modules.invoices = %s, want document %d with its 500.00 payment and its note, its delivery to kari@example.org, and the draft",
			file.Modules["invoices"], *doc.Number)
	}

	if r := admin.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d", person.Id), nil); r.Status != http.StatusNoContent {
		t.Fatalf("archive: status %d body %s", r.Status, r.Body)
	}
	okJSON(t, admin, http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/anonymisation", person.Id),
		map[string]any{"anonymiseOn": h.Now().UTC().Format("2006-01-02")}, nil)
	if ran, err := anonymisationWorker(t, h, modCustomers, modInvoices).RunCycle(context.Background()); err != nil || !ran {
		t.Fatalf("RunCycle = %v, %v", ran, err)
	}

	if r := admin.Do(http.MethodGet, invoiceAt(draft.ID), nil); r.Status != http.StatusNotFound {
		t.Errorf("the draft after the anonymisation = %d %s, want 404: erased", r.Status, r.Body)
	}
	kept := readDoc(t, admin, doc.ID)
	if kept.Status != "issued" || len(kept.Payments) != 1 || kept.Payments[0].Note != "" || kept.Payments[0].Amount != 500 ||
		len(kept.Deliveries) != 1 || invoiceStr(kept.Deliveries[0].Recipient) != "" || kept.Deliveries[0].Subject == "" {
		t.Errorf("the document after the anonymisation = %s, payments %+v, deliveries %+v; want it kept, its payment's note and its delivery's address blanked",
			kept.Status, kept.Payments, kept.Deliveries)
	}

	var event struct {
		Erased []invoiceKindCount `json:"erased"`
	}
	invoiceNewestEvent(t, admin, person.Id, "customer.anonymised", &event)
	var ofInvoices []invoiceKindCount
	for _, e := range event.Erased {
		if e.Kind == "invoices.drafts" || e.Kind == "invoices.documents" || e.Kind == "invoices.payments" || e.Kind == "invoices.deliveries" {
			ofInvoices = append(ofInvoices, e)
		}
	}
	if want := []invoiceKindCount{
		{Kind: "invoices.drafts", Count: 1}, {Kind: "invoices.documents", Count: 0},
		{Kind: "invoices.payments", Count: 1}, {Kind: "invoices.deliveries", Count: 1},
	}; !slices.Equal(ofInvoices, want) {
		t.Errorf("customer.anonymised's erased = %+v, want the invoices module's %+v", event.Erased, want)
	}

	invoiceRefusedAs(t, "a send after the anonymisation", admin.Do(http.MethodPost, invoiceAt(doc.ID)+"/send", map[string]any{}), "customer_anonymised")
}
