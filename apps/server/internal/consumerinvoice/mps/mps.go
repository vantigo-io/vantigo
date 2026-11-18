// Package mps is the consumerinvoice port's adapter for Mastercard Payment
// Services (invoices phase 6 design I29): eFaktura and AvtaleGiro through
// MPS's Kombo submitter service, a SOAP service in the variant where MPS
// hosts the invoice from the PDF we send ([KMS]), and its creditor Mandate
// API, a REST service.
//
// Vantigo is the submitter: the client certificate MPS issued, its key and
// the WS-Security user are the operator's (Config), and every call goes over
// mutual TLS from an allow-listed address. The issuer is the tenant, named by
// its organisation number on every shipment and lookup.
//
// What the public documentation does not settle — the service's XML
// namespace, whether a lookup's answers come back in the order of its keys,
// the coverage call's KID parameter — is marked UNSURE where it is decided;
// the opt-in test against MPS's test environment
// (mps_test_env_test.go) is where each is confirmed.
//
// A SOAP fault always comes back as HTTP 500, so the body is read whatever
// the status. Rejection texts are the network's words and are never logged
// here; the caller redacts them.
package mps

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/consumerinvoice"
)

// Config is one adapter: the operator's submitter credentials and base URLs,
// and the tenant's issuer and creditor account.
type Config struct {
	// EfakturaBaseURL is the SOAP service's endpoint; MandateBaseURL the
	// Mandate API's base, without a trailing slash.
	EfakturaBaseURL, MandateBaseURL string
	// Certificate is the submitter's client certificate and key. The Mandate
	// API uses MandateCertificate when the tenant has one of its own.
	Certificate        tls.Certificate
	MandateCertificate *tls.Certificate
	// Username and Password are the WS-Security user MPS created.
	Username, Password string
	// Issuer is the tenant's 9-digit organisation number; CreditorAccount
	// its 11-digit AvtaleGiro account, "" without AvtaleGiro.
	Issuer, CreditorAccount string
	// Transport is the installation's outbound transport; it is cloned with
	// the client certificate. nil is http.DefaultTransport.
	Transport http.RoundTripper
	// Clock is the module clock, for the requests' timestamps.
	Clock func() time.Time
}

// Adapter is consumerinvoice.Provider over MPS.
type Adapter struct {
	cfg     Config
	soap    *http.Client
	mandate *http.Client
}

var _ consumerinvoice.Provider = (*Adapter)(nil)

// callTimeout bounds one call: MPS asks clients to time out after 20 s.
const callTimeout = 20 * time.Second

// New builds the adapter. It makes no call.
func New(cfg Config) (*Adapter, error) {
	if cfg.EfakturaBaseURL == "" || cfg.MandateBaseURL == "" {
		return nil, errors.New("mps: the base URLs are required")
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	soap, err := clientWith(cfg.Transport, cfg.Certificate)
	if err != nil {
		return nil, err
	}
	mandateCert := cfg.Certificate
	if cfg.MandateCertificate != nil {
		mandateCert = *cfg.MandateCertificate
	}
	mandate, err := clientWith(cfg.Transport, mandateCert)
	if err != nil {
		return nil, err
	}
	cfg.MandateBaseURL = strings.TrimRight(cfg.MandateBaseURL, "/")
	return &Adapter{cfg: cfg, soap: soap, mandate: mandate}, nil
}

// clientWith is an HTTP client over base, cloned, presenting cert.
func clientWith(base http.RoundTripper, cert tls.Certificate) (*http.Client, error) {
	if base == nil {
		base = http.DefaultTransport
	}
	t, ok := base.(*http.Transport)
	if !ok {
		// A test's router, which presents the fake's client certificate
		// itself (mpstest's ClientTransport): used as it is.
		return &http.Client{Transport: base, Timeout: callTimeout}, nil
	}
	t = t.Clone()
	if t.TLSClientConfig == nil {
		t.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	} else if t.TLSClientConfig.MinVersion < tls.VersionTLS12 {
		t.TLSClientConfig.MinVersion = tls.VersionTLS12
	}
	t.TLSClientConfig.Certificates = []tls.Certificate{cert}
	return &http.Client{Transport: t, Timeout: callTimeout}, nil
}

// ServiceNamespace is the Kombo submitter service's XML namespace, on each
// operation's element. UNSURE: taken from the published XSD's naming; the
// test environment confirms it. Each request sets it as its element's
// xmlns from here, the one place it is written.
const ServiceNamespace = "http://efaktura.nets.no/kombo/submitter/v1"

const (
	soapNS       = "http://schemas.xmlsoap.org/soap/envelope/"
	wsseNS       = "http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-secext-1.0.xsd"
	passwordText = "http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-username-token-profile-1.0#PasswordText"
)

type envelope struct {
	XMLName xml.Name `xml:"http://schemas.xmlsoap.org/soap/envelope/ Envelope"`
	Header  header   `xml:"Header"`
	Body    struct {
		Content any `xml:",any"`
	} `xml:"Body"`
}

type header struct {
	Security struct {
		XMLName       xml.Name `xml:"http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-secext-1.0.xsd Security"`
		UsernameToken struct {
			Username string `xml:"Username"`
			Password struct {
				Type  string `xml:"Type,attr"`
				Value string `xml:",chardata"`
			} `xml:"Password"`
		} `xml:"UsernameToken"`
	}
}

// responseEnvelope decodes any answer: a fault or the operation's response.
type responseEnvelope struct {
	Body struct {
		Fault *struct {
			Code   string `xml:"faultcode"`
			String string `xml:"faultstring"`
			Detail struct {
				Error struct {
					Code string `xml:"errorCode"`
					Text string `xml:"errorMessage"`
				} `xml:",any"`
			} `xml:"detail"`
		} `xml:"Fault"`
		Inner []byte `xml:",innerxml"`
	} `xml:"Body"`
}

// Fault codes the adapter folds.
const (
	faultUnauthorized = "AUTHENTICATION_FAILED"
	faultThrottled    = "TOO_MANY_REQUESTS"
	faultDuplicate    = "DUPLICATE_SHIPMENT"
)

// call posts one SOAP operation and decodes its response element into out.
func (a *Adapter) call(ctx context.Context, action string, in, out any) error {
	var env envelope
	env.Header.Security.UsernameToken.Username = a.cfg.Username
	env.Header.Security.UsernameToken.Password.Type = passwordText
	env.Header.Security.UsernameToken.Password.Value = a.cfg.Password
	env.Body.Content = in
	body, err := xml.Marshal(env)
	if err != nil {
		return fmt.Errorf("mps: encode %s: %w", action, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.cfg.EfakturaBaseURL, bytes.NewReader(append([]byte(xml.Header), body...)))
	if err != nil {
		return fmt.Errorf("mps: build %s: %w", action, err)
	}
	req.Header.Set("Content-Type", "text/xml; charset=utf-8")
	req.Header.Set("SOAPAction", action)
	resp, err := a.soap.Do(req)
	if err != nil {
		return fmt.Errorf("mps: %s: %w", action, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
	if err != nil {
		return fmt.Errorf("mps: read %s: %w", action, err)
	}
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return consumerinvoice.ErrUnauthorized
	case http.StatusTooManyRequests:
		return &consumerinvoice.ErrThrottled{RetryAfter: retryAfter(resp.Header.Get("Retry-After"))}
	}
	var decoded responseEnvelope
	if err := xml.Unmarshal(raw, &decoded); err != nil {
		return fmt.Errorf("mps: %s answered %d with a body that does not decode: %w", action, resp.StatusCode, err)
	}
	if f := decoded.Body.Fault; f != nil {
		code := strings.TrimSpace(f.Detail.Error.Code)
		if code == "" {
			code = strings.TrimSpace(f.Code)
		}
		switch {
		case code == faultUnauthorized:
			return consumerinvoice.ErrUnauthorized
		case code == faultThrottled:
			return &consumerinvoice.ErrThrottled{}
		case code == faultDuplicate:
			return consumerinvoice.ErrDuplicate
		case strings.TrimSpace(f.Detail.Error.Code) == "" && strings.HasSuffix(strings.TrimSpace(f.Code), "Server"):
			// A server fault without a code of its own: retryable.
			return fmt.Errorf("mps: %s: a server fault", action)
		}
		text := strings.TrimSpace(f.Detail.Error.Text)
		if text == "" {
			text = strings.TrimSpace(f.String)
		}
		return &consumerinvoice.ErrRefused{Code: code, Text: text}
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("mps: %s answered %d", action, resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	if err := xml.Unmarshal(decoded.Body.Inner, out); err != nil {
		return fmt.Errorf("mps: decode %s's answer: %w", action, err)
	}
	return nil
}

// retryAfter is a Retry-After header's seconds, zero for anything else.
func retryAfter(v string) time.Duration {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n <= 0 {
		return 0
	}
	return time.Duration(n) * time.Second
}

// The lookup ([KSA] getEfakturaIdentifiers).

type lookupRequest struct {
	XMLName xml.Name    `xml:"getEfakturaIdentifiers"`
	NS      string      `xml:"xmlns,attr"`
	Keys    []lookupKey `xml:"lookupKeys>lookupKey"`
}

type lookupKey struct {
	OrganizationNumber string      `xml:"organizationNumber,omitempty"`
	TelephoneAlias     string      `xml:"telephoneAlias,omitempty"`
	EmailAlias         string      `xml:"emailAlias,omitempty"`
	Name               *lookupName `xml:"name,omitempty"`
	Address            *lookupAddr `xml:"address,omitempty"`
	DateOfBirth        string      `xml:"dateOfBirth,omitempty"`
	Issuer             string      `xml:"efakturaIssuer,omitempty"`
}

type lookupName struct {
	FirstName string `xml:"firstName"`
	LastName  string `xml:"lastName"`
}

type lookupAddr struct {
	Lines      []string `xml:"addressLine"`
	PostalCode string   `xml:"postalCode,omitempty"`
	City       string   `xml:"city,omitempty"`
	Country    string   `xml:"countryCode,omitempty"`
}

type lookupResponse struct {
	Results []struct {
		Result     string `xml:"responseResult"`
		Identifier string `xml:"efakturaIdentifier"`
		AutoAccept bool   `xml:"autoAcceptEfaktura"`
		Issuer     *struct {
			Blocked      bool `xml:"blockedByReceiver"`
			HasAgreement bool `xml:"hasEfakturaAgreement"`
		} `xml:"issuerResponse"`
	} `xml:"results>result"`
}

// Lookup finds each consumer, in the order given. UNSURE: the answers are
// read in the order of the keys, as the response carries no key of its own.
func (a *Adapter) Lookup(ctx context.Context, consumers []consumerinvoice.Consumer) ([]consumerinvoice.Address, error) {
	if len(consumers) == 0 {
		return nil, nil
	}
	if len(consumers) > consumerinvoice.MaxLookupBatch {
		return nil, fmt.Errorf("mps: %d consumers in one lookup, at most %d", len(consumers), consumerinvoice.MaxLookupBatch)
	}
	in := lookupRequest{NS: ServiceNamespace, Keys: make([]lookupKey, 0, len(consumers))}
	for _, c := range consumers {
		in.Keys = append(in.Keys, keyOf(c, a.cfg.Issuer))
	}
	var out lookupResponse
	if err := a.call(ctx, "getEfakturaIdentifiers", in, &out); err != nil {
		return nil, err
	}
	if len(out.Results) != len(consumers) {
		return nil, fmt.Errorf("mps: the lookup answered %d results for %d keys", len(out.Results), len(consumers))
	}
	addrs := make([]consumerinvoice.Address, 0, len(out.Results))
	for _, r := range out.Results {
		var addr consumerinvoice.Address
		switch strings.TrimSpace(r.Result) {
		case "UNIQUE_RECEIVER_FOUND":
			addr.Status, addr.Identifier, addr.AutoAccept = consumerinvoice.LookupFound, strings.TrimSpace(r.Identifier), r.AutoAccept
		case "RECEIVER_NOT_FOUND":
			addr.Status = consumerinvoice.LookupNotFound
		case "MULTIPLE_RECEIVERS":
			addr.Status = consumerinvoice.LookupAmbiguous
		case "LOOKUP_KEY_MISSING_DATA":
			addr.Status = consumerinvoice.LookupMissingData
		default:
			return nil, fmt.Errorf("mps: the lookup answered an unknown result %q", r.Result)
		}
		if r.Issuer != nil {
			addr.Blocked, addr.HasAgreement = r.Issuer.Blocked, r.Issuer.HasAgreement
		}
		addrs = append(addrs, addr)
	}
	return addrs, nil
}

// keyOf is a consumer's lookup key, the issuer named so a block is seen
// ([BP]). There is no national identity number to send.
func keyOf(c consumerinvoice.Consumer, issuer string) lookupKey {
	k := lookupKey{TelephoneAlias: c.Phone, EmailAlias: c.Email, Issuer: issuer}
	if c.DateOfBirth != nil {
		k.DateOfBirth = c.DateOfBirth.Format(time.DateOnly)
	}
	if c.FirstName != "" || c.LastName != "" {
		k.Name = &lookupName{FirstName: c.FirstName, LastName: c.LastName}
	}
	if len(c.AddressLines) > 0 || c.PostalCode != "" || c.City != "" {
		k.Address = &lookupAddr{Lines: c.AddressLines, PostalCode: c.PostalCode, City: c.City, Country: c.Country}
	}
	return k
}

// The shipment ([KMS] addShipment).

type shipmentRequest struct {
	XMLName      xml.Name      `xml:"addShipment"`
	NS           string        `xml:"xmlns,attr"`
	MessageID    string        `xml:"shipment>messageIdentifier"`
	ShipmentID   string        `xml:"shipment>shipmentId"`
	Issuer       string        `xml:"shipment>issuer"`
	Transactions []transaction `xml:"shipment>transactions>transaction"`
}

type transaction struct {
	ID           string       `xml:"transactionId"`
	PaymentType  string       `xml:"paymentType"`
	DocumentType string       `xml:"documentType"`
	KID          string       `xml:"kid"`
	Account      string       `xml:"accountNumber"`
	Amount       string       `xml:"amount"`
	DueDate      string       `xml:"dueDate"`
	Efaktura     efakturaInfo `xml:"efakturaInfo"`
	ATG          *atgInfo     `xml:"atgInfo,omitempty"`
}

type efakturaInfo struct {
	Identifier string `xml:"efakturaIdentifier"`
	Reference  string `xml:"eFakturaReference,omitempty"`
	BrandName  string `xml:"brandName"`
	PDF        []byte `xml:"invoicePdf"`
}

type atgInfo struct {
	AbbreviatedName   string `xml:"atgAbbreviatedName"`
	ExternalReference string `xml:"atgExternalReference,omitempty"`
}

type shipmentResponse struct {
	ShipmentID string `xml:"shipmentId"`
}

// paymentTypeOf is the network's paymentType.
func paymentTypeOf(t consumerinvoice.PaymentType) (string, error) {
	switch t {
	case consumerinvoice.PaymentEfaktura:
		return "EFAKTURA", nil
	case consumerinvoice.PaymentBoth:
		return "EFAKTURA_AND_AVTALEGIRO", nil
	default:
		return "", fmt.Errorf("mps: unknown payment type %q", t)
	}
}

// Send hands one shipment over; the PDF goes base64-encoded, and MPS hosts
// it. The reference is the shipment id we chose.
func (a *Adapter) Send(ctx context.Context, s consumerinvoice.Shipment) (consumerinvoice.Ref, error) {
	if len(s.Transactions) == 0 || len(s.Transactions) > consumerinvoice.MaxShipment {
		return "", fmt.Errorf("mps: a shipment carries 1 to %d transactions, not %d", consumerinvoice.MaxShipment, len(s.Transactions))
	}
	in := shipmentRequest{NS: ServiceNamespace, MessageID: s.MessageID, ShipmentID: s.ID, Issuer: a.cfg.Issuer}
	for _, t := range s.Transactions {
		if t.Amount.Currency != "NOK" {
			return "", fmt.Errorf("mps: the network carries NOK only, not %s", t.Amount.Currency)
		}
		pt, err := paymentTypeOf(t.PaymentType)
		if err != nil {
			return "", err
		}
		tx := transaction{
			ID: t.ID, PaymentType: pt, DocumentType: "INVOICE", KID: t.KID, Account: t.Account,
			Amount: t.Amount.String(), DueDate: t.DueDate.Format(time.DateOnly),
			Efaktura: efakturaInfo{Identifier: t.Identifier, Reference: t.Reference, BrandName: t.BrandName, PDF: t.PDF},
		}
		if t.PaymentType == consumerinvoice.PaymentBoth {
			tx.ATG = &atgInfo{AbbreviatedName: t.AbbreviatedName, ExternalReference: t.ExternalReference}
		}
		in.Transactions = append(in.Transactions, tx)
	}
	var out shipmentResponse
	if err := a.call(ctx, "addShipment", in, &out); err != nil {
		return "", err
	}
	if out.ShipmentID != "" && out.ShipmentID != s.ID {
		return "", fmt.Errorf("mps: the shipment was answered under another id")
	}
	return consumerinvoice.Ref(s.ID), nil
}

// The status ([KSA] getTransactionStatus).

type statusRequest struct {
	XMLName    xml.Name `xml:"getTransactionStatus"`
	NS         string   `xml:"xmlns,attr"`
	ShipmentID string   `xml:"shipmentId"`
}

type statusResponse struct {
	Transactions []struct {
		Raw           []byte `xml:",innerxml"`
		ID            string `xml:"transactionId"`
		Efaktura      string `xml:"efakturaStatus"`
		ATG           string `xml:"atgStatus"`
		Attention     bool   `xml:"attention"`
		RejectionCode string `xml:"rejectionCode"`
		RejectionText string `xml:"rejectionText"`
	} `xml:"transactions>transaction"`
}

// efakturaStates folds MPS's eFaktura transaction states.
var efakturaStates = map[string]consumerinvoice.EfakturaState{
	"IN_PROGRESS": consumerinvoice.EfakturaSubmitted,
	"ACCEPTED":    consumerinvoice.EfakturaDelivered,
	"PROCESSED":   consumerinvoice.EfakturaDelivered,
	"REJECTED":    consumerinvoice.EfakturaFailed,
	"STOPPED":     consumerinvoice.EfakturaFailed,
	"NOT_FOUND":   consumerinvoice.EfakturaFailed,
	"CANCELLED":   consumerinvoice.EfakturaCancelled,
	// A duplicate is the same transaction sent again: the first stands.
	"DUPLICATE": consumerinvoice.EfakturaSubmitted,
}

// atgStates folds MPS's AvtaleGiro transaction states. The CANCELLATION_*
// states short of CANCELLED are a cancellation still in progress.
func atgState(s string) (consumerinvoice.AvtaleGiroState, bool) {
	switch {
	case s == "" || s == "NOT_AN_ATG_TRANSACTION" || s == "EXTERNAL_ATG":
		return "", true
	case s == "IN_PROGRESS" || s == "PENDING":
		return consumerinvoice.AvtaleGiroSubmitted, true
	case s == "PROCESSED":
		return consumerinvoice.AvtaleGiroAccepted, true
	case s == "REJECTED":
		return consumerinvoice.AvtaleGiroRejected, true
	case s == "CANCELLED":
		return consumerinvoice.AvtaleGiroCancelled, true
	case strings.HasPrefix(s, "CANCELLATION_"):
		return consumerinvoice.AvtaleGiroAccepted, true
	default:
		return "", false
	}
}

// Status is each of the shipment's transactions.
func (a *Adapter) Status(ctx context.Context, ref consumerinvoice.Ref) ([]consumerinvoice.TransactionStatus, error) {
	var out statusResponse
	if err := a.call(ctx, "getTransactionStatus", statusRequest{NS: ServiceNamespace, ShipmentID: string(ref)}, &out); err != nil {
		return nil, err
	}
	statuses := make([]consumerinvoice.TransactionStatus, 0, len(out.Transactions))
	for _, t := range out.Transactions {
		ef := strings.TrimSpace(t.Efaktura)
		efState, ok := efakturaStates[ef]
		if !ok && ef != "NOT_AN_EFAKTURA_TRANSACTION" {
			return nil, fmt.Errorf("mps: unknown eFaktura state %q", ef)
		}
		atg := strings.TrimSpace(t.ATG)
		atgS, ok := atgState(atg)
		if !ok {
			return nil, fmt.Errorf("mps: unknown AvtaleGiro state %q", atg)
		}
		statuses = append(statuses, consumerinvoice.TransactionStatus{
			TransactionID: strings.TrimSpace(t.ID), Efaktura: efState, AvtaleGiro: atgS,
			ProviderEfaktura: ef, ProviderAvtaleGiro: atg, Attention: t.Attention,
			RejectionCode: strings.TrimSpace(t.RejectionCode), RejectionText: strings.TrimSpace(t.RejectionText),
			Raw: append([]byte("<transaction>"), append(bytes.TrimSpace(t.Raw), []byte("</transaction>")...)...),
		})
	}
	return statuses, nil
}

// The cancellation ([KSA] cancelTransaction).

type cancelRequest struct {
	XMLName       xml.Name `xml:"cancelTransaction"`
	NS            string   `xml:"xmlns,attr"`
	ShipmentID    string   `xml:"shipmentId"`
	TransactionID string   `xml:"transactionId"`
}

type cancelResponse struct {
	Result string `xml:"result"`
}

// Cancel asks for one transaction's cancellation.
func (a *Adapter) Cancel(ctx context.Context, ref consumerinvoice.Ref, transactionID string) (consumerinvoice.CancelOutcome, error) {
	var out cancelResponse
	if err := a.call(ctx, "cancelTransaction", cancelRequest{NS: ServiceNamespace, ShipmentID: string(ref), TransactionID: transactionID}, &out); err != nil {
		return "", err
	}
	switch strings.TrimSpace(out.Result) {
	case "CANCELLATION_REQUESTED":
		return consumerinvoice.CancelRequested, nil
	case "NOT_ALLOWED":
		return consumerinvoice.CancelNotAllowed, nil
	case "PROCESSING_IN_PROGRESS-TRY_LATER":
		return consumerinvoice.CancelTryLater, nil
	default:
		return "", fmt.Errorf("mps: the cancellation answered an unknown result %q", out.Result)
	}
}

// The Mandate API ([MANDATE]).

type mandatePage struct {
	Page struct {
		TotalPages int `json:"total_pages"`
		Number     int `json:"page_number"`
	} `json:"page"`
	Mandates []struct {
		Reference string `json:"mandate_reference"`
		Status    struct {
			Code string `json:"code"`
		} `json:"mandate_status"`
		Deleted bool `json:"deleted"`
		Debtor  struct {
			Contact struct {
				Preferred string `json:"prefered_method"`
			} `json:"contact_details"`
		} `json:"debtor"`
	} `json:"mandates"`
}

// mandateGet is one GET on the Mandate API with its two headers; a 204
// answers nil.
func (a *Adapter) mandateGet(ctx context.Context, path string, query url.Values, requestID string) ([]byte, error) {
	if a.cfg.CreditorAccount == "" {
		return nil, errors.New("mps: no AvtaleGiro creditor account is configured")
	}
	u := a.cfg.MandateBaseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("mps: build the mandate request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Creditor-Account-Number", a.cfg.CreditorAccount)
	req.Header.Set("X-Request-ID", requestID)
	resp, err := a.mandate.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mps: the mandate API: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
	if err != nil {
		return nil, fmt.Errorf("mps: read the mandate API's answer: %w", err)
	}
	switch {
	case resp.StatusCode == http.StatusNoContent:
		return nil, nil
	case resp.StatusCode == http.StatusOK:
		return body, nil
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, consumerinvoice.ErrUnauthorized
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, &consumerinvoice.ErrThrottled{RetryAfter: retryAfter(resp.Header.Get("Retry-After"))}
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		return nil, &consumerinvoice.ErrRefused{Code: strconv.Itoa(resp.StatusCode)}
	default:
		return nil, fmt.Errorf("mps: the mandate API answered %d", resp.StatusCode)
	}
}

// Mandates reads the next page of the delta queue.
func (a *Adapter) Mandates(ctx context.Context, requestID string) (consumerinvoice.MandatePage, error) {
	body, err := a.mandateGet(ctx, "/creditor-account", nil, requestID)
	if err != nil || body == nil {
		return consumerinvoice.MandatePage{}, err
	}
	var page mandatePage
	if err := decodeJSON(body, &page); err != nil {
		return consumerinvoice.MandatePage{}, fmt.Errorf("mps: decode the mandates: %w", err)
	}
	out := consumerinvoice.MandatePage{More: page.Page.TotalPages > 1, Mandates: make([]consumerinvoice.Mandate, 0, len(page.Mandates))}
	for _, m := range page.Mandates {
		var status consumerinvoice.MandateStatus
		switch m.Status.Code {
		case "ACTV":
			status = consumerinvoice.MandateActive
		case "INAC":
			status = consumerinvoice.MandateInactive
		case "PEND":
			status = consumerinvoice.MandatePending
		default:
			return consumerinvoice.MandatePage{}, fmt.Errorf("mps: unknown mandate status %q", m.Status.Code)
		}
		out.Mandates = append(out.Mandates, consumerinvoice.Mandate{
			KID: strings.TrimSpace(m.Reference), Status: status, Deleted: m.Deleted,
			NotifyByMail: m.Debtor.Contact.Preferred == "MAIL",
		})
	}
	return out, nil
}

// Covers asks whether the mandate covers amount. UNSURE: the KID travels as
// the kid query parameter beside valid-amount.
func (a *Adapter) Covers(ctx context.Context, kid string, amount consumerinvoice.Money) (bool, error) {
	body, err := a.mandateGet(ctx, "/creditor-account/kid", url.Values{"kid": {kid}, "valid-amount": {amount.String()}}, newRequestID())
	if err != nil {
		var refused *consumerinvoice.ErrRefused
		if errors.As(err, &refused) && refused.Code == "404" {
			return false, nil
		}
		return false, err
	}
	var out struct {
		Covered bool `json:"mandate_coverage_indicator"`
	}
	if body == nil {
		return false, nil
	}
	if err := decodeJSON(body, &out); err != nil {
		return false, fmt.Errorf("mps: decode the coverage: %w", err)
	}
	return out.Covered, nil
}

// Verify makes one lookup of the issuer's own organisation number — the
// certificate, the IP and the user all checked by one cheap call — and, with
// a creditor account, one coverage check of a KID nobody holds, which reads
// no mandate and leaves the delta queue alone.
func (a *Adapter) Verify(ctx context.Context) error {
	in := lookupRequest{NS: ServiceNamespace, Keys: []lookupKey{{OrganizationNumber: a.cfg.Issuer, Issuer: a.cfg.Issuer}}}
	if err := a.call(ctx, "getEfakturaIdentifiers", in, nil); err != nil {
		return err
	}
	if a.cfg.CreditorAccount == "" {
		return nil
	}
	_, err := a.Covers(ctx, "0000", consumerinvoice.NOK(100))
	return err
}

// decodeJSON decodes a Mandate API answer.
func decodeJSON(body []byte, out any) error {
	return json.Unmarshal(body, out)
}

// newRequestID is a fresh X-Request-ID, a random UUID: a coverage check is
// never a replay.
func newRequestID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}
