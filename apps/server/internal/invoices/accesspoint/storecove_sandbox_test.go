//go:build storecove

package accesspoint_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/invoices/accesspoint"
)

// The sandbox tests run the adapter against Storecove itself (EHF and KID
// design D7): `go test -tags storecove ./internal/invoices/accesspoint/` with
// STORECOVE_SANDBOX_API_KEY and STORECOVE_SANDBOX_LEGAL_ENTITY_ID set — a
// sandbox key from Storecove's sales-contact onboarding and the legal entity
// it sends as. They skip without them. INVOICES_STORECOVE_BASE_URL overrides
// the production base URL (the sandbox is the same host with a sandbox key),
// and STORECOVE_SANDBOX_SELLER_ORG the seller's organisation number in the
// UBL (default 974760673); keep it the legal entity's own.
//
// Each submits one document to the Norwegian test receiver NO:ORG 010101018,
// drains the account's event queue — acknowledging every event, so run them
// on an account nothing else reads — until its submission's outcome arrives,
// and asserts that the PDF embedded in the submitted UBL is in the copy
// Storecove delivered: Storecove regenerates the UBL it transmits, and its
// documentation does not say whether the attachment survives.

const sandboxReceiver = "0192:010101018"

// sandboxPDF is the smallest PDF a reader opens: one empty page.
var sandboxPDF = []byte("%PDF-1.4\n1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj\n2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj\n3 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 595 842]>>endobj\ntrailer<</Root 1 0 R>>\n%%EOF\n")

func sandbox(t *testing.T) accesspoint.AccessPoint {
	t.Helper()
	key := os.Getenv("STORECOVE_SANDBOX_API_KEY")
	entity := os.Getenv("STORECOVE_SANDBOX_LEGAL_ENTITY_ID")
	if key == "" || entity == "" {
		t.Skip("STORECOVE_SANDBOX_API_KEY and STORECOVE_SANDBOX_LEGAL_ENTITY_ID are not set")
	}
	id, err := strconv.Atoi(entity)
	if err != nil {
		t.Fatalf("STORECOVE_SANDBOX_LEGAL_ENTITY_ID = %q: %v", entity, err)
	}
	base := os.Getenv("INVOICES_STORECOVE_BASE_URL")
	if base == "" {
		base = "https://api.storecove.com/api/v2"
	}
	return accesspoint.NewStorecove(base, key, id, nil, nil)
}

func sellerOrg() string {
	if org := os.Getenv("STORECOVE_SANDBOX_SELLER_ORG"); org != "" {
		return org
	}
	return "974760673"
}

func TestStorecoveSandbox_AnInvoiceIsDeliveredWithItsPDF(t *testing.T) {
	ap := sandbox(t)
	number := fmt.Sprintf("VT%d", time.Now().Unix())
	sendAndAssertDelivered(t, ap, "urn:oasis:names:specification:ubl:schema:xsd:Invoice-2::Invoice##urn:cen.eu:en16931:2017#compliant#urn:fdc:peppol.eu:2017:poacc:billing:3.0::2.1",
		sandboxInvoice(number))
}

func TestStorecoveSandbox_ACreditNoteIsDeliveredWithItsPDF(t *testing.T) {
	ap := sandbox(t)
	number := fmt.Sprintf("VC%d", time.Now().Unix())
	sendAndAssertDelivered(t, ap, "urn:oasis:names:specification:ubl:schema:xsd:CreditNote-2::CreditNote##urn:cen.eu:en16931:2017#compliant#urn:fdc:peppol.eu:2017:poacc:billing:3.0::2.1",
		sandboxCreditNote(number))
}

func sendAndAssertDelivered(t *testing.T, ap accesspoint.AccessPoint, documentType string, ubl []byte) {
	t.Helper()
	ctx := context.Background()
	if err := ap.Verify(ctx); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	ref, err := ap.Submit(ctx, accesspoint.Submission{
		IdempotencyKey: uuid.New(), Sender: "0192:" + sellerOrg(), Receiver: sandboxReceiver,
		DocumentType: documentType, ProcessID: "urn:fdc:peppol.eu:2017:poacc:billing:01:1.0", UBL: ubl,
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	t.Logf("submitted as %s", ref)

	state := drainUntil(t, ap, ref, 10*time.Minute)
	if state != accesspoint.StateDelivered {
		t.Fatalf("the outcome of %s = %s, want delivered", ref, state)
	}
	var ev accesspoint.Evidence
	for deadline := time.Now().Add(2 * time.Minute); ; time.Sleep(10 * time.Second) {
		ev, err = ap.Evidence(ctx, ref)
		if err == nil || time.Now().After(deadline) {
			break
		}
	}
	if err != nil {
		t.Fatalf("Evidence: %v", err)
	}
	t.Logf("delivered over %s, message %s, %d bytes (%s)", ev.ReceivingAP, ev.MessageID, len(ev.Delivered), ev.DeliveredMIME)
	embedded, err := embeddedDocuments(ev.Delivered)
	if err != nil {
		t.Fatalf("read the delivered copy: %v", err)
	}
	for _, doc := range embedded {
		if bytes.Equal(doc, sandboxPDF) {
			return
		}
	}
	t.Errorf("the delivered copy carries %d embedded documents, none of them the submitted PDF", len(embedded))
}

// drainUntil reads the account's queue, acknowledging every event, until
// one says what became of ref.
func drainUntil(t *testing.T, ap accesspoint.AccessPoint, ref accesspoint.SubmissionRef, wait time.Duration) accesspoint.SubmissionState {
	t.Helper()
	ctx := context.Background()
	for deadline := time.Now().Add(wait); time.Now().Before(deadline); {
		e, ok, err := ap.NextEvent(ctx)
		if err != nil {
			t.Fatalf("NextEvent: %v", err)
		}
		if !ok {
			time.Sleep(15 * time.Second)
			continue
		}
		if err := ap.AckEvent(ctx, e.ID); err != nil {
			t.Fatalf("AckEvent: %v", err)
		}
		t.Logf("event %s: %s for %s (%s)", e.ID, e.State, e.SubmissionRef, e.Reason)
		if e.SubmissionRef == ref && e.State != accesspoint.StateSubmitted {
			return e.State
		}
	}
	t.Fatalf("no outcome for %s within %s", ref, wait)
	return ""
}

// embeddedDocuments is every cbc:EmbeddedDocumentBinaryObject in a UBL
// document, decoded.
func embeddedDocuments(doc []byte) ([][]byte, error) {
	const cbc = "urn:oasis:names:specification:ubl:schema:xsd:CommonBasicComponents-2"
	dec := xml.NewDecoder(bytes.NewReader(doc))
	var out [][]byte
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		start, ok := tok.(xml.StartElement)
		if !ok || start.Name.Space != cbc || start.Name.Local != "EmbeddedDocumentBinaryObject" {
			continue
		}
		var text string
		if err := dec.DecodeElement(&text, &start); err != nil {
			return nil, err
		}
		body, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(text), ""))
		if err != nil {
			return nil, err
		}
		out = append(out, body)
	}
}

const sandboxNamespaces = `xmlns:cac="urn:oasis:names:specification:ubl:schema:xsd:CommonAggregateComponents-2" xmlns:cbc="urn:oasis:names:specification:ubl:schema:xsd:CommonBasicComponents-2"`

// sandboxParties is the seller, the buyer — the test receiver — and the
// payment, the same on both documents.
func sandboxParties() string {
	org := sellerOrg()
	return `<cac:AccountingSupplierParty><cac:Party>` +
		`<cbc:EndpointID schemeID="0192">` + org + `</cbc:EndpointID>` +
		`<cac:PostalAddress><cbc:StreetName>Storgata 1</cbc:StreetName><cbc:CityName>Oslo</cbc:CityName><cbc:PostalZone>0155</cbc:PostalZone><cac:Country><cbc:IdentificationCode>NO</cbc:IdentificationCode></cac:Country></cac:PostalAddress>` +
		`<cac:PartyTaxScheme><cbc:CompanyID>NO` + org + `MVA</cbc:CompanyID><cac:TaxScheme><cbc:ID>VAT</cbc:ID></cac:TaxScheme></cac:PartyTaxScheme>` +
		`<cac:PartyTaxScheme><cbc:CompanyID>Foretaksregisteret</cbc:CompanyID><cac:TaxScheme><cbc:ID>TAX</cbc:ID></cac:TaxScheme></cac:PartyTaxScheme>` +
		`<cac:PartyLegalEntity><cbc:RegistrationName>Vantigo Sandbox AS</cbc:RegistrationName><cbc:CompanyID schemeID="0192">` + org + `</cbc:CompanyID></cac:PartyLegalEntity>` +
		`</cac:Party></cac:AccountingSupplierParty>` +
		`<cac:AccountingCustomerParty><cac:Party>` +
		`<cbc:EndpointID schemeID="0192">010101018</cbc:EndpointID>` +
		`<cac:PostalAddress><cbc:StreetName>Kundeveien 2</cbc:StreetName><cbc:CityName>Oslo</cbc:CityName><cbc:PostalZone>0150</cbc:PostalZone><cac:Country><cbc:IdentificationCode>NO</cbc:IdentificationCode></cac:Country></cac:PostalAddress>` +
		`<cac:PartyLegalEntity><cbc:RegistrationName>Peppol Test Receiver</cbc:RegistrationName><cbc:CompanyID schemeID="0192">010101018</cbc:CompanyID></cac:PartyLegalEntity>` +
		`</cac:Party></cac:AccountingCustomerParty>`
}

func sandboxAttachment(number string) string {
	return `<cac:AdditionalDocumentReference><cbc:ID>` + number + `</cbc:ID><cac:Attachment>` +
		`<cbc:EmbeddedDocumentBinaryObject mimeCode="application/pdf" filename="` + number + `.pdf">` +
		base64.StdEncoding.EncodeToString(sandboxPDF) + `</cbc:EmbeddedDocumentBinaryObject></cac:Attachment></cac:AdditionalDocumentReference>`
}

const sandboxTotals = `<cac:TaxTotal><cbc:TaxAmount currencyID="NOK">250.00</cbc:TaxAmount><cac:TaxSubtotal>` +
	`<cbc:TaxableAmount currencyID="NOK">1000.00</cbc:TaxableAmount><cbc:TaxAmount currencyID="NOK">250.00</cbc:TaxAmount>` +
	`<cac:TaxCategory><cbc:ID>S</cbc:ID><cbc:Percent>25</cbc:Percent><cac:TaxScheme><cbc:ID>VAT</cbc:ID></cac:TaxScheme></cac:TaxCategory>` +
	`</cac:TaxSubtotal></cac:TaxTotal>` +
	`<cac:LegalMonetaryTotal><cbc:LineExtensionAmount currencyID="NOK">1000.00</cbc:LineExtensionAmount>` +
	`<cbc:TaxExclusiveAmount currencyID="NOK">1000.00</cbc:TaxExclusiveAmount><cbc:TaxInclusiveAmount currencyID="NOK">1250.00</cbc:TaxInclusiveAmount>` +
	`<cbc:PayableAmount currencyID="NOK">1250.00</cbc:PayableAmount></cac:LegalMonetaryTotal>`

const sandboxItem = `<cbc:LineExtensionAmount currencyID="NOK">1000.00</cbc:LineExtensionAmount>` +
	`<cac:Item><cbc:Name>Konsulenttime</cbc:Name><cac:ClassifiedTaxCategory><cbc:ID>S</cbc:ID><cbc:Percent>25</cbc:Percent>` +
	`<cac:TaxScheme><cbc:ID>VAT</cbc:ID></cac:TaxScheme></cac:ClassifiedTaxCategory></cac:Item>` +
	`<cac:Price><cbc:PriceAmount currencyID="NOK">1000.00</cbc:PriceAmount></cac:Price>`

const sandboxHeader = `<cbc:CustomizationID>urn:cen.eu:en16931:2017#compliant#urn:fdc:peppol.eu:2017:poacc:billing:3.0</cbc:CustomizationID>` +
	`<cbc:ProfileID>urn:fdc:peppol.eu:2017:poacc:billing:01:1.0</cbc:ProfileID>`

func sandboxInvoice(number string) []byte {
	today := time.Now().Format(time.DateOnly)
	due := time.Now().AddDate(0, 0, 14).Format(time.DateOnly)
	return []byte(`<?xml version="1.0" encoding="UTF-8"?>` +
		`<Invoice xmlns="urn:oasis:names:specification:ubl:schema:xsd:Invoice-2" ` + sandboxNamespaces + `>` +
		sandboxHeader +
		`<cbc:ID>` + number + `</cbc:ID><cbc:IssueDate>` + today + `</cbc:IssueDate><cbc:DueDate>` + due + `</cbc:DueDate>` +
		`<cbc:InvoiceTypeCode>380</cbc:InvoiceTypeCode><cbc:DocumentCurrencyCode>NOK</cbc:DocumentCurrencyCode>` +
		`<cbc:BuyerReference>PO-77</cbc:BuyerReference>` +
		sandboxAttachment(number) + sandboxParties() +
		`<cac:Delivery><cbc:ActualDeliveryDate>` + today + `</cbc:ActualDeliveryDate></cac:Delivery>` +
		`<cac:PaymentMeans><cbc:PaymentMeansCode>30</cbc:PaymentMeansCode><cac:PayeeFinancialAccount><cbc:ID>86011117947</cbc:ID></cac:PayeeFinancialAccount></cac:PaymentMeans>` +
		`<cac:PaymentTerms><cbc:Note>14 dager</cbc:Note></cac:PaymentTerms>` +
		sandboxTotals +
		`<cac:InvoiceLine><cbc:ID>1</cbc:ID><cbc:InvoicedQuantity unitCode="HUR">1</cbc:InvoicedQuantity>` + sandboxItem + `</cac:InvoiceLine>` +
		`</Invoice>`)
}

func sandboxCreditNote(number string) []byte {
	today := time.Now().Format(time.DateOnly)
	return []byte(`<?xml version="1.0" encoding="UTF-8"?>` +
		`<CreditNote xmlns="urn:oasis:names:specification:ubl:schema:xsd:CreditNote-2" ` + sandboxNamespaces + `>` +
		sandboxHeader +
		`<cbc:ID>` + number + `</cbc:ID><cbc:IssueDate>` + today + `</cbc:IssueDate>` +
		`<cbc:CreditNoteTypeCode>381</cbc:CreditNoteTypeCode><cbc:DocumentCurrencyCode>NOK</cbc:DocumentCurrencyCode>` +
		`<cbc:BuyerReference>PO-77</cbc:BuyerReference>` +
		`<cac:BillingReference><cac:InvoiceDocumentReference><cbc:ID>1</cbc:ID><cbc:IssueDate>` + today + `</cbc:IssueDate></cac:InvoiceDocumentReference></cac:BillingReference>` +
		sandboxAttachment(number) + sandboxParties() +
		sandboxTotals +
		`<cac:CreditNoteLine><cbc:ID>1</cbc:ID><cbc:CreditedQuantity unitCode="HUR">1</cbc:CreditedQuantity>` + sandboxItem + `</cac:CreditNoteLine>` +
		`</CreditNote>`)
}
