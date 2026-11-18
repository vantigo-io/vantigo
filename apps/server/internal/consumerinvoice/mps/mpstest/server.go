// Package mpstest is a fake Mastercard Payment Services for tests only: the
// Kombo submitter service's four operations the adapter calls, and the
// Mandate API's queue and coverage check, over a TLS listener that requires
// a client certificate, as MPS's does.
//
// The fake keeps a register of persons the lookup finds by MPS's matching
// rule, the shipments it was handed with states that advance by the test's
// clock, the cancellation deadline (14:00 Oslo time on the day before the
// due date), and a mandate queue with paging and replay by X-Request-ID.
// Failures are injected per operation with FailOn and RefuseOn, which hold
// until cleared — never a FailNext a parallel caller could eat (spec §12).
package mpstest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"encoding/xml"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The operations FailOn and RefuseOn name.
const (
	OpLookup   = "getEfakturaIdentifiers"
	OpShipment = "addShipment"
	OpStatus   = "getTransactionStatus"
	OpCancel   = "cancelTransaction"
	OpMandates = "mandates"
	OpCoverage = "coverage"
)

// Person is one consumer in the fake's register.
type Person struct {
	Identifier                 string
	Phone, Email, DateOfBirth  string // DateOfBirth as 2006-01-02
	FirstName, LastName        string
	PostalCode                 string
	AutoAccept                 bool
	Agreements, BlockedIssuers []string
}

// Transaction is one transaction as the fake holds it.
type Transaction struct {
	ShipmentID, ID, PaymentType, KID, Account, Amount, DueDate string
	Identifier, Reference, BrandName                           string
	PDF                                                        []byte
	// Efaktura and ATG are the forced states; "" lets the clock decide.
	Efaktura, ATG                string
	Attention                    bool
	RejectionCode, RejectionText string
	received                     time.Time
}

// Mandate is one mandate the queue delivers.
type Mandate struct {
	KID, Status  string // ACTV, INAC or PEND
	NotifyByMail bool
	Deleted      bool
}

type failure struct {
	status     int
	code, text string
}

// Server is the fake. Every method is safe for concurrent use.
type Server struct {
	srv  *httptest.Server
	cert tls.Certificate

	mu          sync.Mutex
	clock       func() time.Time
	persons     []Person
	shipments   map[string][]*Transaction
	order       []string
	failures    map[string]failure
	queue       []Mandate
	pageSize    int
	pages       map[string]mandatePageOut
	limits      map[string]int64
	requests    []string
	acceptAfter time.Duration
	dropped     map[string]bool
}

// Oslo is the business zone the cancellation deadline is judged in.
var oslo = func() *time.Location {
	loc, err := time.LoadLocation("Europe/Oslo")
	if err != nil {
		return time.FixedZone("CET", 3600)
	}
	return loc
}()

// New starts the fake. Close it with Close.
func New() *Server {
	s := &Server{
		clock: time.Now, shipments: map[string][]*Transaction{}, failures: map[string]failure{},
		pages: map[string]mandatePageOut{}, limits: map[string]int64{}, pageSize: 1000,
		acceptAfter: 5 * time.Minute, dropped: map[string]bool{},
	}
	s.cert = selfSigned("mpstest client")
	mux := http.NewServeMux()
	mux.HandleFunc("POST /efaktura", s.soap)
	mux.HandleFunc("GET /creditor-mandate/creditor-account", s.mandates)
	mux.HandleFunc("GET /creditor-mandate/creditor-account/kid", s.coverage)
	s.srv = httptest.NewUnstartedServer(mux)
	s.srv.TLS = &tls.Config{ClientAuth: tls.RequireAnyClientCert, MinVersion: tls.VersionTLS12}
	s.srv.StartTLS()
	return s
}

// Close stops the fake.
func (s *Server) Close() { s.srv.Close() }

// EfakturaURL and MandateURL are the base URLs to configure the adapter with.
func (s *Server) EfakturaURL() string { return s.srv.URL + "/efaktura" }
func (s *Server) MandateURL() string  { return s.srv.URL + "/creditor-mandate" }

// ClientCertificate is a certificate the fake accepts.
func (s *Server) ClientCertificate() tls.Certificate { return s.cert }

// ClientTransport is Transport presenting ClientCertificate: what a test's
// router hands the adapter, which uses a transport that is not an
// *http.Transport as it is.
func (s *Server) ClientTransport() *http.Transport {
	t := s.Transport()
	t.TLSClientConfig.Certificates = []tls.Certificate{s.cert}
	return t
}

// CertificatePEM and KeyPEM are ClientCertificate as the files an operator
// configures (INVOICES_MPS_CLIENT_CERT_FILE, INVOICES_MPS_CLIENT_KEY_FILE).
func (s *Server) CertificatePEM() []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.cert.Certificate[0]})
}

func (s *Server) KeyPEM() []byte {
	der, err := x509.MarshalPKCS8PrivateKey(s.cert.PrivateKey)
	if err != nil {
		panic(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

// Transport is an *http.Transport that trusts the fake's server certificate,
// with no client certificate of its own: the adapter adds one.
func (s *Server) Transport() *http.Transport {
	return s.srv.Client().Transport.(*http.Transport).Clone()
}

// SetClock makes the fake judge time by clock.
func (s *Server) SetClock(clock func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clock = clock
}

// SetAcceptAfter is how long after a shipment its transactions are accepted
// (default five minutes).
func (s *Server) SetAcceptAfter(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.acceptAfter = d
}

// AddPerson puts a consumer in the register.
func (s *Server) AddPerson(p Person) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.persons = append(s.persons, p)
}

// FailOn makes every call of op answer status — 401 the credentials refused,
// 429 a throttle, 500 a server fault — until cleared with status 0.
func (s *Server) FailOn(op string, status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if status == 0 {
		delete(s.failures, op)
		return
	}
	s.failures[op] = failure{status: status}
}

// RefuseOn makes every call of op answer a client fault with code and text
// until cleared with FailOn(op, 0).
func (s *Server) RefuseOn(op, code, text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures[op] = failure{status: http.StatusInternalServerError, code: code, text: text}
}

// DropAnswersOn makes every call of op do its work and then lose its answer
// (a 500 after the fact) until cleared with false: the mandate queue's page
// is handed out, and the caller never hears it. Only OpMandates heeds it.
func (s *Server) DropAnswersOn(op string, drop bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dropped[op] = drop
}

// SetTransactionState forces a transaction's states and its rejection.
func (s *Server) SetTransactionState(transactionID, efaktura, atg string, attention bool, code, text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, txs := range s.shipments {
		for _, t := range txs {
			if t.ID == transactionID {
				t.Efaktura, t.ATG, t.Attention, t.RejectionCode, t.RejectionText = efaktura, atg, attention, code, text
			}
		}
	}
}

// Shipments is every transaction received, in order.
func (s *Server) Shipments() []Transaction {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Transaction
	for _, id := range s.order {
		for _, t := range s.shipments[id] {
			out = append(out, *t)
		}
	}
	return out
}

// Requests is every request body received, for a test to search.
func (s *Server) Requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.requests)
}

// QueueMandates puts mandates in the delta queue.
func (s *Server) QueueMandates(ms ...Mandate) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queue = append(s.queue, ms...)
}

// SetPageSize is how many mandates one page carries (default 1,000).
func (s *Server) SetPageSize(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pageSize = n
}

// SetLimit is the payer's limit on a mandate in øre: Covers answers whether
// an amount is within it. A KID without one is covered whatever the amount.
func (s *Server) SetLimit(kid string, minor int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.limits[kid] = minor
}

func (s *Server) failureOf(op string) (failure, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.failures[op]
	return f, ok
}

// The SOAP side.

type inEnvelope struct {
	Header struct {
		Security struct {
			Username string `xml:"UsernameToken>Username"`
			Password string `xml:"UsernameToken>Password"`
		} `xml:"Security"`
	} `xml:"Header"`
	Body struct {
		Inner []byte `xml:",innerxml"`
	} `xml:"Body"`
}

func (s *Server) soap(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	s.requests = append(s.requests, string(raw))
	s.mu.Unlock()
	action := strings.Trim(r.Header.Get("SOAPAction"), `"`)
	var env inEnvelope
	if err := xml.Unmarshal(raw, &env); err != nil || env.Header.Security.Username == "" || env.Header.Security.Password == "" {
		fault(w, "AUTHENTICATION_FAILED", "No valid UsernameToken.")
		return
	}
	if f, ok := s.failureOf(action); ok {
		switch {
		case f.code != "":
			fault(w, f.code, f.text)
		case f.status == http.StatusUnauthorized:
			fault(w, "AUTHENTICATION_FAILED", "The user is not known.")
		case f.status == http.StatusTooManyRequests:
			w.Header().Set("Retry-After", "30")
			w.WriteHeader(http.StatusTooManyRequests)
		default:
			serverFault(w)
		}
		return
	}
	switch action {
	case OpLookup:
		s.lookup(w, env.Body.Inner)
	case OpShipment:
		s.addShipment(w, env.Body.Inner)
	case OpStatus:
		s.status(w, env.Body.Inner)
	case OpCancel:
		s.cancel(w, env.Body.Inner)
	default:
		fault(w, "UNKNOWN_OPERATION", "No such operation.")
	}
}

func fault(w http.ResponseWriter, code, text string) {
	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	w.WriteHeader(http.StatusInternalServerError)
	_, _ = fmt.Fprintf(w, `<?xml version="1.0"?><soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body><soap:Fault><faultcode>soap:Client</faultcode><faultstring>%s</faultstring><detail><error><errorCode>%s</errorCode><errorMessage>%s</errorMessage></error></detail></soap:Fault></soap:Body></soap:Envelope>`,
		esc(text), esc(code), esc(text))
}

func serverFault(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	w.WriteHeader(http.StatusInternalServerError)
	_, _ = io.WriteString(w, `<?xml version="1.0"?><soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body><soap:Fault><faultcode>soap:Server</faultcode><faultstring>Internal error</faultstring></soap:Fault></soap:Body></soap:Envelope>`)
}

func respond(w http.ResponseWriter, inner string) {
	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	_, _ = io.WriteString(w, `<?xml version="1.0"?><soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body>`+inner+`</soap:Body></soap:Envelope>`)
}

func esc(v string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(v))
	return b.String()
}

type lookupIn struct {
	Keys []struct {
		Org         string   `xml:"organizationNumber"`
		SSN         string   `xml:"socialSecurityNumber"`
		Phone       string   `xml:"telephoneAlias"`
		Email       string   `xml:"emailAlias"`
		FirstName   string   `xml:"name>firstName"`
		LastName    string   `xml:"name>lastName"`
		PostalCode  string   `xml:"address>postalCode"`
		Lines       []string `xml:"address>addressLine"`
		DateOfBirth string   `xml:"dateOfBirth"`
		Issuer      string   `xml:"efakturaIssuer"`
	} `xml:"lookupKeys>lookupKey"`
}

// lookup answers by MPS's matching rule: two of phone, e-mail and date of
// birth; one of them with the name or the postal code; none with the name
// and the postal code (the address lines are not compared).
func (s *Server) lookup(w http.ResponseWriter, body []byte) {
	var in lookupIn
	if err := xml.Unmarshal(body, &in); err != nil {
		fault(w, "INVALID_REQUEST", "The request does not decode.")
		return
	}
	s.mu.Lock()
	persons := slices.Clone(s.persons)
	s.mu.Unlock()
	var b strings.Builder
	b.WriteString(`<getEfakturaIdentifiersResponse><results>`)
	for _, k := range in.Keys {
		if k.Org != "" {
			b.WriteString(`<result><responseResult>RECEIVER_NOT_FOUND</responseResult></result>`)
			continue
		}
		strong := 0
		for _, v := range []string{k.Phone, k.Email, k.DateOfBirth} {
			if v != "" {
				strong++
			}
		}
		weak := (k.FirstName != "" && k.LastName != "") || k.PostalCode != "" || len(k.Lines) > 0
		allWeak := k.FirstName != "" && k.PostalCode != "" && len(k.Lines) > 0
		if (strong == 0 && !allWeak) || (strong == 1 && !weak) {
			b.WriteString(`<result><responseResult>LOOKUP_KEY_MISSING_DATA</responseResult></result>`)
			continue
		}
		var hits []Person
		for _, p := range persons {
			matched := 0
			if k.Phone != "" && k.Phone == p.Phone {
				matched++
			}
			if k.Email != "" && strings.EqualFold(k.Email, p.Email) {
				matched++
			}
			if k.DateOfBirth != "" && k.DateOfBirth == p.DateOfBirth {
				matched++
			}
			nameOK := strings.EqualFold(k.FirstName, p.FirstName) && strings.EqualFold(k.LastName, p.LastName)
			postalOK := k.PostalCode != "" && k.PostalCode == p.PostalCode
			ok := false
			switch {
			case strong >= 2:
				ok = matched >= 2
			case strong == 1:
				ok = matched == 1 && (nameOK || postalOK)
			default:
				ok = nameOK && postalOK
			}
			if ok {
				hits = append(hits, p)
			}
		}
		switch len(hits) {
		case 0:
			b.WriteString(`<result><responseResult>RECEIVER_NOT_FOUND</responseResult></result>`)
		case 1:
			p := hits[0]
			fmt.Fprintf(&b, `<result><responseResult>UNIQUE_RECEIVER_FOUND</responseResult><efakturaIdentifier>%s</efakturaIdentifier><autoAcceptEfaktura>%t</autoAcceptEfaktura>`,
				esc(p.Identifier), p.AutoAccept)
			if k.Issuer != "" {
				fmt.Fprintf(&b, `<issuerResponse><blockedByReceiver>%t</blockedByReceiver><hasEfakturaAgreement>%t</hasEfakturaAgreement></issuerResponse>`,
					slices.Contains(p.BlockedIssuers, k.Issuer), slices.Contains(p.Agreements, k.Issuer))
			}
			b.WriteString(`</result>`)
		default:
			b.WriteString(`<result><responseResult>MULTIPLE_RECEIVERS</responseResult></result>`)
		}
	}
	b.WriteString(`</results></getEfakturaIdentifiersResponse>`)
	respond(w, b.String())
}

type shipmentIn struct {
	MessageID    string `xml:"shipment>messageIdentifier"`
	ShipmentID   string `xml:"shipment>shipmentId"`
	Issuer       string `xml:"shipment>issuer"`
	Transactions []struct {
		ID           string `xml:"transactionId"`
		PaymentType  string `xml:"paymentType"`
		DocumentType string `xml:"documentType"`
		KID          string `xml:"kid"`
		Account      string `xml:"accountNumber"`
		Amount       string `xml:"amount"`
		DueDate      string `xml:"dueDate"`
		Identifier   string `xml:"efakturaInfo>efakturaIdentifier"`
		Reference    string `xml:"efakturaInfo>eFakturaReference"`
		BrandName    string `xml:"efakturaInfo>brandName"`
		PDF          []byte `xml:"efakturaInfo>invoicePdf"`
	} `xml:"shipment>transactions>transaction"`
}

func (s *Server) addShipment(w http.ResponseWriter, body []byte) {
	var in shipmentIn
	if err := xml.Unmarshal(body, &in); err != nil {
		fault(w, "INVALID_REQUEST", "The request does not decode.")
		return
	}
	if len(in.MessageID) < 5 || len(in.MessageID) > 50 || len(in.Issuer) != 9 || in.ShipmentID == "" {
		fault(w, "INVALID_SHIPMENT", "The shipment's message identifier, issuer or id is invalid.")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.shipments[in.ShipmentID]; ok {
		w.Header().Set("Content-Type", "text/xml; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `<?xml version="1.0"?><soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body><soap:Fault><faultcode>soap:Client</faultcode><faultstring>Duplicate</faultstring><detail><error><errorCode>DUPLICATE_SHIPMENT</errorCode></error></detail></soap:Fault></soap:Body></soap:Envelope>`)
		return
	}
	now := s.clock()
	var txs []*Transaction
	for _, t := range in.Transactions {
		if t.DocumentType != "INVOICE" {
			faultUnlocked(w, "3100", "Only INVOICE is accepted.")
			return
		}
		due, err := time.ParseInLocation(time.DateOnly, t.DueDate, oslo)
		if err != nil {
			faultUnlocked(w, "INVALID_DUE_DATE", "The due date does not parse.")
			return
		}
		minDays := 3
		if t.PaymentType == "EFAKTURA_AND_AVTALEGIRO" {
			minDays = 4
		}
		today := time.Date(now.In(oslo).Year(), now.In(oslo).Month(), now.In(oslo).Day(), 0, 0, 0, 0, oslo)
		if due.Before(today.AddDate(0, 0, minDays)) || due.After(today.AddDate(1, 0, 0)) {
			faultUnlocked(w, "INVALID_DUE_DATE", "The due date is outside the window.")
			return
		}
		txs = append(txs, &Transaction{
			ShipmentID: in.ShipmentID, ID: t.ID, PaymentType: t.PaymentType, KID: t.KID, Account: t.Account,
			Amount: t.Amount, DueDate: t.DueDate, Identifier: t.Identifier, Reference: t.Reference,
			BrandName: t.BrandName, PDF: t.PDF, received: now,
		})
	}
	s.shipments[in.ShipmentID] = txs
	s.order = append(s.order, in.ShipmentID)
	respond(w, `<addShipmentResponse><shipmentId>`+esc(in.ShipmentID)+`</shipmentId></addShipmentResponse>`)
}

func faultUnlocked(w http.ResponseWriter, code, text string) { fault(w, code, text) }

// statesOf is a transaction's states now: forced, or by the clock.
func (s *Server) statesOf(t *Transaction, now time.Time) (ef, atg string) {
	ef, atg = t.Efaktura, t.ATG
	accepted := !now.Before(t.received.Add(s.acceptAfter))
	if ef == "" {
		ef = "IN_PROGRESS"
		if accepted {
			ef = "ACCEPTED"
		}
	}
	if atg == "" {
		switch {
		case t.PaymentType != "EFAKTURA_AND_AVTALEGIRO":
			atg = "NOT_AN_ATG_TRANSACTION"
		case accepted:
			atg = "PROCESSED"
		default:
			atg = "PENDING"
		}
	}
	return ef, atg
}

func (s *Server) status(w http.ResponseWriter, body []byte) {
	var in struct {
		ShipmentID string `xml:"shipmentId"`
	}
	if err := xml.Unmarshal(body, &in); err != nil {
		fault(w, "INVALID_REQUEST", "The request does not decode.")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	txs, ok := s.shipments[strings.TrimSpace(in.ShipmentID)]
	if !ok {
		faultUnlocked(w, "SHIPMENT_NOT_FOUND", "No such shipment.")
		return
	}
	now := s.clock()
	var b strings.Builder
	b.WriteString(`<getTransactionStatusResponse><transactions>`)
	for _, t := range txs {
		ef, atg := s.statesOf(t, now)
		fmt.Fprintf(&b, `<transaction><transactionId>%s</transactionId><efakturaStatus>%s</efakturaStatus><atgStatus>%s</atgStatus><attention>%t</attention>`,
			esc(t.ID), ef, atg, t.Attention)
		if t.RejectionCode != "" {
			fmt.Fprintf(&b, `<rejectionCode>%s</rejectionCode><rejectionText>%s</rejectionText>`, esc(t.RejectionCode), esc(t.RejectionText))
		}
		b.WriteString(`</transaction>`)
	}
	b.WriteString(`</transactions></getTransactionStatusResponse>`)
	respond(w, b.String())
}

// CancelDeadline is the last moment a claim due on due may be cancelled:
// 14:00 Oslo time on the day before.
func CancelDeadline(due time.Time) time.Time {
	d := due.In(oslo)
	return time.Date(d.Year(), d.Month(), d.Day()-1, 14, 0, 0, 0, oslo)
}

func (s *Server) cancel(w http.ResponseWriter, body []byte) {
	var in struct {
		ShipmentID    string `xml:"shipmentId"`
		TransactionID string `xml:"transactionId"`
	}
	if err := xml.Unmarshal(body, &in); err != nil {
		fault(w, "INVALID_REQUEST", "The request does not decode.")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock()
	for _, t := range s.shipments[in.ShipmentID] {
		if t.ID != in.TransactionID {
			continue
		}
		due, _ := time.ParseInLocation(time.DateOnly, t.DueDate, oslo)
		result := "NOT_ALLOWED"
		if t.PaymentType == "EFAKTURA_AND_AVTALEGIRO" && now.Before(CancelDeadline(due)) {
			result = "CANCELLATION_REQUESTED"
			t.ATG = "CANCELLED"
		}
		respond(w, `<cancelTransactionResponse><result>`+result+`</result></cancelTransactionResponse>`)
		return
	}
	faultUnlocked(w, "TRANSACTION_NOT_FOUND", "No such transaction.")
}

// The Mandate API.

type mandatePageOut struct {
	Page struct {
		TotalPages int `json:"total_pages"`
		Number     int `json:"page_number"`
	} `json:"page"`
	Mandates []mandateOut `json:"mandates"`
}

type mandateOut struct {
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
}

func (s *Server) mandateFailure(w http.ResponseWriter, op string) bool {
	f, ok := s.failureOf(op)
	if !ok {
		return false
	}
	status := f.status
	if f.code != "" {
		status = http.StatusBadRequest
	}
	w.WriteHeader(status)
	return true
}

func validMandateHeaders(r *http.Request) bool {
	account := r.Header.Get("Creditor-Account-Number")
	if len(account) != 11 || strings.Trim(account, "0123456789") != "" {
		return false
	}
	return r.Header.Get("X-Request-ID") != ""
}

func (s *Server) mandates(w http.ResponseWriter, r *http.Request) {
	if s.mandateFailure(w, OpMandates) {
		return
	}
	if !validMandateHeaders(r) {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	id := r.Header.Get("X-Request-ID")
	s.mu.Lock()
	page, seen := s.pages[id]
	if !seen {
		if len(s.queue) == 0 {
			s.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
			return
		}
		n := min(s.pageSize, len(s.queue))
		page.Page.TotalPages = (len(s.queue) + s.pageSize - 1) / s.pageSize
		page.Page.Number = 1
		for _, m := range s.queue[:n] {
			var out mandateOut
			out.Reference, out.Status.Code, out.Deleted = m.KID, m.Status, m.Deleted
			out.Debtor.Contact.Preferred = "NONE"
			if m.NotifyByMail {
				out.Debtor.Contact.Preferred = "MAIL"
			}
			page.Mandates = append(page.Mandates, out)
		}
		s.queue = s.queue[n:]
		s.pages[id] = page
	}
	drop := s.dropped[OpMandates]
	s.mu.Unlock()
	if drop {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(page)
}

func (s *Server) coverage(w http.ResponseWriter, r *http.Request) {
	if s.mandateFailure(w, OpCoverage) {
		return
	}
	if !validMandateHeaders(r) {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	kid := r.URL.Query().Get("kid")
	amount := r.URL.Query().Get("valid-amount")
	major, minor, _ := strings.Cut(amount, ".")
	m, err1 := strconv.ParseInt(major, 10, 64)
	n, err2 := strconv.ParseInt(minor, 10, 64)
	if err1 != nil || err2 != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	limit, limited := s.limits[kid]
	s.mu.Unlock()
	covered := !limited || m*100+n <= limit
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"mandate_coverage_indicator": covered})
}

// selfSigned is a fresh client certificate.
func selfSigned(cn string) tls.Certificate {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: cn},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, KeyUsage: x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		panic(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}
