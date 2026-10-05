package integration_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/invoices/accesspoint/storecovetest"
	"github.com/vantigo-io/vantigo/server/internal/invoices/kid"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/peppol"
	"github.com/vantigo-io/vantigo/server/internal/storage"
)

// The customers + invoices EHF test (EHF and KID design D8, D9, D12): a
// customer made through the customers API, its Peppol id and its preference
// for EHF on the real billing profile, invoiced under the seller's KID
// agreement and sent as EHF — the receiver re-checked against a fake Peppol
// network, the UBL submitted by the invoices-ehf worker through the module's
// own Storecove adapter to the test Storecove, its delivery learnt from the
// provider's event queue by the events worker and its evidence stored as the
// record. Then the person's export through customers carries the
// transmission, and the anonymisation keeps it while cancelling one that was
// never attempted. Every step runs on the harness clock; nothing sleeps.

// ehfLookup stands in for the Peppol network both modules share
// (Deps.PeppolLookup): every participant is registered for both document
// types, and every participant asked about is kept.
type ehfLookup struct {
	mu    sync.Mutex
	asked []string
}

func (l *ehfLookup) lookup(_ context.Context, participant string) (peppol.Result, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.asked = append(l.asked, participant)
	return peppol.Result{Registered: true, CanReceiveInvoice: true, CanReceiveCreditNote: true}, nil
}

func (l *ehfLookup) participants() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.asked)
}

// The seller's and the buyer's Peppol ids.
const (
	ehfSellerPeppolID = "0192:974760673"
	ehfBuyerPeppolID  = "0192:923609016"
)

// ehfTransmission is one transmission as the document answers it.
type ehfTransmission struct {
	ID                  int64   `json:"id"`
	Status              string  `json:"status"`
	ReceiverParticipant string  `json:"receiverParticipant"`
	UblSha256           string  `json:"ublSha256"`
	UblURL              string  `json:"ublUrl"`
	ProviderRef         *string `json:"providerRef"`
	DeliveredAt         *string `json:"deliveredAt"`
	CancelledAt         *string `json:"cancelledAt"`
}

// ehfDoc is a document as these steps read it: its KID and its EHF state.
type ehfDoc struct {
	ID     int64   `json:"id"`
	Number *int64  `json:"number"`
	Kid    *string `json:"kid"`
	Ehf    *struct {
		Status        string            `json:"status"`
		ProviderRef   *string           `json:"providerRef"`
		Preference    *string           `json:"preference"`
		BuyerPeppolID *string           `json:"buyerPeppolId"`
		Transmissions []ehfTransmission `json:"transmissions"`
	} `json:"ehf"`
}

// ehfListItem is one document of the list as these steps read it.
type ehfListItem struct {
	ID        int64   `json:"id"`
	EhfStatus *string `json:"ehfStatus"`
}

// readEhfDoc reads document id.
func readEhfDoc(t *testing.T, c *modtest.Client, id int64) ehfDoc {
	t.Helper()
	var doc ehfDoc
	okJSON(t, c, http.MethodGet, invoiceAt(id), nil, &doc)
	return doc
}

// ehfStatusOf asserts document id answers the EHF status want, its newest
// transmission first, and answers that transmission.
func ehfStatusOf(t *testing.T, c *modtest.Client, id int64, want string) ehfTransmission {
	t.Helper()
	doc := readEhfDoc(t, c, id)
	if doc.Ehf == nil || doc.Ehf.Status != want || len(doc.Ehf.Transmissions) == 0 || doc.Ehf.Transmissions[0].Status != want {
		t.Fatalf("document %d's ehf = %+v, want %s", id, doc.Ehf, want)
	}
	return doc.Ehf.Transmissions[0]
}

// sendAsEhf sends document id as EHF and asserts the transmission queued.
func sendAsEhf(t *testing.T, c *modtest.Client, id int64) ehfTransmission {
	t.Helper()
	var doc ehfDoc
	okJSON(t, c, http.MethodPost, invoiceAt(id)+"/send-ehf", nil, &doc)
	if doc.Ehf == nil || doc.Ehf.Status != "queued" || len(doc.Ehf.Transmissions) != 1 || doc.Ehf.Transmissions[0].Status != "queued" {
		t.Fatalf("send-ehf of %d answered ehf %+v, want one queued transmission", id, doc.Ehf)
	}
	return doc.Ehf.Transmissions[0]
}

// ehfInstallation is invoicesInstallation able to send as EHF, set up the way
// an operator does it: the test Storecove as the access point, a fake Peppol
// network both modules share and a file-system object store; the access
// point stored through PUT /invoices/settings/access-point, then the seller's
// Peppol id and a ten-digit MOD10 KID agreement through PUT /invoices/settings.
func ehfInstallation(t *testing.T) (*modtest.Harness, *modtest.Client, *storecovetest.Server, *ehfLookup, storage.ObjectStore) {
	t.Helper()
	storecove, opts := withStorecove(t)
	lookup := &ehfLookup{}
	objects, err := storage.NewFS(t.TempDir(), true, true)
	if err != nil {
		t.Fatalf("storage.NewFS: %v", err)
	}
	h, admin, _ := invoicesInstallation(t, append(opts, modtest.WithObjectStore(objects), modtest.WithPeppolLookup(lookup.lookup))...)
	okJSON(t, admin, http.MethodPut, invoicesBase+"/settings/access-point", map[string]any{
		"provider": "storecove", "legalEntityId": storecovetest.LegalEntityID, "apiKey": storecovetest.APIKey,
	}, nil)
	var settings struct {
		Revision int32 `json:"revision"`
	}
	okJSON(t, admin, http.MethodGet, invoicesBase+"/settings", nil, &settings)
	okJSON(t, admin, http.MethodPut, invoicesBase+"/settings", map[string]any{
		"legalName": "Kraft-Verket AS", "organisationNumber": "974 760 673",
		"vatRegistered": true, "inForetaksregisteret": true,
		"addressLine1": "Storgata 1", "addressLine2": "", "postalCode": "0155", "city": "Oslo", "country": "no",
		"bankAccount": "8601.11.17947", "iban": "NO93 8601 1117 947", "bic": "dnbanokkxxx",
		"email": sellerEmail, "defaultPaymentTermsDays": 14, "defaultCurrency": "NOK",
		"footerText": "Takk for handelen.", "seriesStart": 1,
		"peppolId": ehfSellerPeppolID, "kidLength": 10, "kidAlgorithm": "mod10", "revision": settings.Revision,
		"workVatCodes": map[string]any{"hours": 1, "expenses": 1, "milestones": 1},
	}, nil)
	return h, admin, storecove, lookup, objects
}

// ublBuyerEndpoint is the part of a submitted UBL invoice a test reads back,
// matched by namespace: the buyer's electronic address.
type ublBuyerEndpoint struct {
	Customer struct {
		Party struct {
			Endpoint struct {
				SchemeID string `xml:"schemeID,attr"`
				Value    string `xml:",chardata"`
			} `xml:"urn:oasis:names:specification:ubl:schema:xsd:CommonBasicComponents-2 EndpointID"`
		} `xml:"urn:oasis:names:specification:ubl:schema:xsd:CommonAggregateComponents-2 Party"`
	} `xml:"urn:oasis:names:specification:ubl:schema:xsd:CommonAggregateComponents-2 AccountingCustomerParty"`
}

// TestEhf_ARealCustomerIsInvoicedAsEhfEndToEnd: an operator stores the
// access point and the seller's Peppol id and KID agreement; a customer made
// through the customers API with a Peppol id and invoiceDelivery ehf is
// invoiced, the issue computing a KID, and sent as EHF — the receiver
// re-checked with the snapshot's Peppol id. The invoices-ehf worker, run
// once, submits the stored UBL; the events worker, run once, reads the
// provider's "succeeded" and marks it delivered; the worker, once more,
// stores the receipt and the delivered copy. The document answers its ehf
// block, the UBL downloads as submitted, and the list says delivered. The
// person's export through customers carries the transmission, and the
// anonymisation worker keeps it delivered while cancelling a second
// invoice's transmission that was queued and never attempted.
func TestEhf_ARealCustomerIsInvoicedAsEhfEndToEnd(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h, admin, storecove, lookup, objects := ehfInstallation(t)

	// The customer: a private person — the one kind customers exports and
	// anonymises — trading as a sole proprietor, whose billing profile names
	// the Peppol id and prefers EHF.
	var person invoiceCustomer
	okJSON(t, admin, http.MethodPost, "/api/v1/customers", map[string]any{"name": "Ola Nordmann", "type": "person"}, &person)
	invoiceAddress(t, admin, person.Id)
	invoiceBillingProfile(t, admin, person.Id, map[string]any{
		"invoiceDelivery": "ehf", "peppolId": ehfBuyerPeppolID, "buyerReference": "PO-4711",
	})

	issued := issueDoc(t, admin, newDraft(t, admin, person.Id).ID)
	doc := readEhfDoc(t, admin, issued.ID)
	if doc.Kid == nil || len(*doc.Kid) != 10 || !strings.HasPrefix(*doc.Kid, fmt.Sprintf("%09d", *issued.Number)) ||
		!kid.Verify(*doc.Kid, "mod10", *issued.Number) {
		t.Errorf("the issued invoice's KID = %s, want number %d zero-padded to nine digits and its MOD10 check digit",
			invoiceStr(doc.Kid), *issued.Number)
	}
	if doc.Ehf == nil || doc.Ehf.Status != "not_sent" || doc.Ehf.Preference == nil || *doc.Ehf.Preference != "ehf" ||
		doc.Ehf.BuyerPeppolID == nil || *doc.Ehf.BuyerPeppolID != ehfBuyerPeppolID {
		t.Errorf("the issued invoice's ehf = %+v, want not_sent, the profile's ehf preference and the buyer's Peppol id", doc.Ehf)
	}

	// The send: the receiver re-checked with the snapshot's Peppol id, a
	// transmission queued.
	queued := sendAsEhf(t, admin, issued.ID)
	if queued.ReceiverParticipant != ehfBuyerPeppolID || !slices.Contains(lookup.participants(), ehfBuyerPeppolID) {
		t.Errorf("the transmission's receiver = %s, the network asked about %v; want %s both", queued.ReceiverParticipant, lookup.participants(), ehfBuyerPeppolID)
	}

	// The submit: one worker cycle, one submission of the stored UBL.
	if err := invoices.NewEhfWorker(h.Deps()).RunCycle(ctx); err != nil {
		t.Fatalf("the invoices-ehf worker: %v", err)
	}
	submitted := ehfStatusOf(t, admin, issued.ID, "submitted")
	subs := storecove.Submissions()
	if len(subs) != 1 {
		t.Fatalf("%d submissions, want 1", len(subs))
	}
	sum := sha256.Sum256(subs[0].UBL)
	if hex.EncodeToString(sum[:]) != queued.UblSha256 || submitted.ProviderRef == nil || *submitted.ProviderRef != subs[0].GUID {
		t.Errorf("the submission = UBL sha256 %x guid %s, the transmission's ublSha256 %s providerRef %v; want the stored UBL under the provider's reference",
			sum, subs[0].GUID, queued.UblSha256, submitted.ProviderRef)
	}

	// Delivered: the provider's event, drained once.
	storecove.Enqueue(storecovetest.Event{Event: "succeeded", SubmissionGUID: subs[0].GUID})
	if held, err := invoices.NewEhfEventsWorker(h.Deps()).RunCycle(ctx); err != nil || !held {
		t.Fatalf("the invoices-ehf-events worker = %v, %v; want a drain", held, err)
	}
	if storecove.Queued() != 0 || len(storecove.Acked()) != 1 {
		t.Errorf("the queue after the drain = %d queued, %d acked; want it drained and acked", storecove.Queued(), len(storecove.Acked()))
	}
	delivered := ehfStatusOf(t, admin, issued.ID, "delivered")
	if delivered.DeliveredAt == nil {
		t.Errorf("the delivered transmission = %+v, want its delivery time", delivered)
	}

	// The evidence: the worker once more stores the receipt and the
	// delivered copy as the record.
	receipt := storecove.Evidence(subs[0].GUID, storecovetest.Document{Body: []byte("<Invoice>as delivered</Invoice>")})
	if err := invoices.NewEhfWorker(h.Deps()).RunCycle(ctx); err != nil {
		t.Fatalf("the invoices-ehf worker's evidence claim: %v", err)
	}
	// Deps.ObjectStore is the module's own store as given (newServer scopes
	// only one it builds from the configuration), so the keys are the
	// module's.
	base := fmt.Sprintf("documents/%d/%d-%d", issued.ID, *issued.Number, queued.ID)
	for _, key := range []string{base + "-receipt.json", base + "-delivered.xml"} {
		if ok, err := objects.Exists(ctx, key); err != nil || !ok {
			t.Errorf("the object store has %s = %v, %v; want the evidence stored", key, ok, err)
		}
	}
	evidence := modtest.One[string](t, h, `SELECT evidence_sha256 FROM invoices.transmissions WHERE id = $1`, queued.ID)
	if sum := sha256.Sum256(receipt); evidence != hex.EncodeToString(sum[:]) {
		t.Errorf("evidence_sha256 = %s, want the receipt's", evidence)
	}

	// What a reader sees: the ehf block, the UBL as submitted, the list.
	final := ehfStatusOf(t, admin, issued.ID, "delivered")
	if want := fmt.Sprintf("%s/%d/transmissions/%d/ubl", invoicesBase, issued.ID, queued.ID); final.UblURL != want || final.ID != queued.ID {
		t.Errorf("the transmission = %+v, want %d with ublUrl %s", final, queued.ID, want)
	}
	ubl := admin.Do(http.MethodGet, final.UblURL, nil)
	if ubl.Status != http.StatusOK || string(ubl.Body) != string(subs[0].UBL) {
		t.Errorf("GET %s = %d, %d bytes; want 200 and the UBL submitted", final.UblURL, ubl.Status, len(ubl.Body))
	}
	var list struct {
		Data []ehfListItem `json:"data"`
	}
	okJSON(t, admin, http.MethodGet, invoicesBase, nil, &list)
	if i := slices.IndexFunc(list.Data, func(d ehfListItem) bool { return d.ID == issued.ID }); i < 0 || invoiceStr(list.Data[i].EhfStatus) != "delivered" {
		t.Errorf("the list = %+v, want document %d with ehfStatus delivered", list.Data, issued.ID)
	}

	// A second invoice queued and never attempted.
	second := issueDoc(t, admin, newDraft(t, admin, person.Id).ID)
	unattempted := sendAsEhf(t, admin, second.ID)

	// The export through customers carries both transmissions.
	var file struct {
		Modules map[string]json.RawMessage `json:"modules"`
	}
	okJSON(t, admin, http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/personal-data", person.Id), nil, &file)
	var section struct {
		Documents []struct {
			Number        int64 `json:"number"`
			Transmissions []struct {
				ID                  int64  `json:"id"`
				Status              string `json:"status"`
				ReceiverParticipant string `json:"receiverParticipant"`
				UblSha256           string `json:"ublSha256"`
			} `json:"transmissions"`
		} `json:"documents"`
	}
	if err := json.Unmarshal(file.Modules["invoices"], &section); err != nil {
		t.Fatalf("modules.invoices = %s: %v", file.Modules["invoices"], err)
	}
	exported := map[int64]string{}
	for _, d := range section.Documents {
		for _, tr := range d.Transmissions {
			if tr.ReceiverParticipant != ehfBuyerPeppolID || tr.UblSha256 == "" {
				t.Errorf("exported transmission %+v, want the receiver and the UBL's hash", tr)
			}
			exported[tr.ID] = fmt.Sprintf("%d:%s", d.Number, tr.Status)
		}
	}
	if want := map[int64]string{
		queued.ID: fmt.Sprintf("%d:delivered", *issued.Number), unattempted.ID: fmt.Sprintf("%d:queued", *second.Number),
	}; fmt.Sprint(exported) != fmt.Sprint(want) {
		t.Errorf("modules.invoices' transmissions = %v, want %v", exported, want)
	}
	if strings.Contains(string(file.Modules["invoices"]), "as delivered") {
		t.Errorf("modules.invoices = %s, carries the evidence's bytes", file.Modules["invoices"])
	}

	// The anonymisation keeps the delivered transmission and cancels the
	// one never attempted.
	if r := admin.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d", person.Id), nil); r.Status != http.StatusNoContent {
		t.Fatalf("archive: status %d body %s", r.Status, r.Body)
	}
	okJSON(t, admin, http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/anonymisation", person.Id),
		map[string]any{"anonymiseOn": h.Now().UTC().Format("2006-01-02")}, nil)
	if ran, err := anonymisationWorker(t, h, modCustomers, modInvoices).RunCycle(ctx); err != nil || !ran {
		t.Fatalf("the anonymisation worker = %v, %v", ran, err)
	}
	var event struct {
		Erased []invoiceKindCount `json:"erased"`
	}
	invoiceNewestEvent(t, admin, person.Id, "customer.anonymised", &event)
	if !slices.Contains(event.Erased, invoiceKindCount{Kind: "invoices.transmissions", Count: 1}) {
		t.Errorf("customer.anonymised's erased = %+v, want invoices.transmissions 1", event.Erased)
	}
	if kept := ehfStatusOf(t, admin, issued.ID, "delivered"); kept.ID != queued.ID {
		t.Errorf("the delivered transmission after the anonymisation = %+v, want %d kept", kept, queued.ID)
	}
	if cancelled := ehfStatusOf(t, admin, second.ID, "cancelled"); cancelled.ID != unattempted.ID || cancelled.CancelledAt == nil {
		t.Errorf("the second invoice's transmission = %+v, want %d cancelled", cancelled, unattempted.ID)
	}
	if n := len(storecove.Submissions()); n != 1 {
		t.Errorf("%d submissions, want the first invoice's only", n)
	}
}

// TestEhf_ABusinessIsReachedAtItsOrganisationNumber: a business made through
// the customers API with its organisation number and invoiceDelivery ehf and
// no Peppol id of its own is invoiced and sent as EHF — the directory derives
// 0192:<organisation number>, the network is asked about it, and the
// invoices-ehf worker, run once, submits a UBL whose buyer's EndpointID is
// that organisation number under scheme 0192. (The export and the
// anonymisation are a private person's alone: the end-to-end test above.)
func TestEhf_ABusinessIsReachedAtItsOrganisationNumber(t *testing.T) {
	t.Parallel()
	h, admin, storecove, lookup, _ := ehfInstallation(t)
	business := invoiceNewBusiness(t, admin, "Fjord Nord", "Fjord Nord AS", "923609016")
	invoiceBillingProfile(t, admin, business.Id, map[string]any{"invoiceDelivery": "ehf", "buyerReference": "PO-4711"})

	issued := issueDoc(t, admin, newDraft(t, admin, business.Id).ID)
	queued := sendAsEhf(t, admin, issued.ID)
	if queued.ReceiverParticipant != "0192:923609016" || !slices.Contains(lookup.participants(), "0192:923609016") {
		t.Errorf("the receiver = %s, the network asked about %v; want 0192:923609016, derived from the organisation number",
			queued.ReceiverParticipant, lookup.participants())
	}
	if err := invoices.NewEhfWorker(h.Deps()).RunCycle(context.Background()); err != nil {
		t.Fatalf("the invoices-ehf worker: %v", err)
	}
	ehfStatusOf(t, admin, issued.ID, "submitted")
	subs := storecove.Submissions()
	if len(subs) != 1 {
		t.Fatalf("%d submissions, want 1", len(subs))
	}
	var ubl ublBuyerEndpoint
	if err := xml.Unmarshal(subs[0].UBL, &ubl); err != nil {
		t.Fatalf("the submitted UBL: %v", err)
	}
	if ubl.Customer.Party.Endpoint.SchemeID != "0192" || ubl.Customer.Party.Endpoint.Value != "923609016" {
		t.Errorf("the buyer's EndpointID = %q under scheme %q, want 923609016 under 0192", ubl.Customer.Party.Endpoint.Value, ubl.Customer.Party.Endpoint.SchemeID)
	}
}
