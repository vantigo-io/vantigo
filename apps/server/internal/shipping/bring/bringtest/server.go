// Package bringtest is an httptest Bring for tests only: the shapes of the
// Shipping Guide v2, Booking, Tracking v2 and Pickup Point APIs the Bring
// adapter speaks (orders design O49, research R §2.9), served from one TLS
// listener so every test package — the adapter's, the module's, the
// integration test — drives the real adapter over real HTTP without touching
// the network. vippstest is the same idea for Vipps.
//
// It is an ordinary package, not a _test file, so other packages' tests can
// import it; nothing outside a test ever should.
//
// What it does: every call must carry the Mybring headers of UID and Key and
// a client URL. The Shipping Guide prices every product of a request at
// NOK 100 plus NOK 10 per started kilogram as the list price, and 80 % of
// that as the net price unless NoFinancialRole is set. A booking gets the
// next consignment number and a package number per package, is kept under
// its correlationId and its sender's reference, and answers links to its
// label — a redirect to a PDF — and, for a cargo product, its waybill. A
// booking to postal code 0000 is refused with Bring's message. Tracking finds
// a booking by consignment number, package number or reference, and answers
// the events a test scripts with SetEvents (PRE_NOTIFIED when none). Every
// answer carries a field the adapter does not know, which it must ignore.
// Fail answers an operation with a status and a body, or books and then
// stalls past the client's deadline or answers what the client cannot read
// (an unknown outcome after Bring booked).
//
// A client reaches it through Transport, which trusts the server's
// certificate — a harness passes it as Deps.HTTPTransport.
package bringtest

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The credentials the server accepts, and Bring's test customer numbers.
const (
	UID              = "bringtest@example.com"
	Key              = "bringtest-api-key"
	CustomerParcels  = "5"
	CustomerNordic   = "6"
	CustomerCargo    = "7"
	RefusedPostal    = "0000"
	RefusedMessage   = "Recipient postal code 0000 is not valid."
	RefusedCode      = "INVALID_POSTAL_CODE"
	unknownFieldName = "someFieldBringAddedLater"
)

// Op names the operation a Fail applies to.
type Op string

const (
	OpGuide    Op = "guide"
	OpBook     Op = "book"
	OpLabel    Op = "label"
	OpTracking Op = "tracking"
	OpPickup   Op = "pickup"
)

// Failure is how an operation is answered instead of normally.
type Failure struct {
	// Status and Body are answered as they are.
	Status int
	Body   string
	// Stall, on OpBook, books as usual and then waits this long before
	// answering: a client with a shorter deadline sees a timeout after Bring
	// booked. On any other operation it waits this long, then answers as
	// usual.
	Stall time.Duration
	// AfterBook, on OpBook, books as usual and then answers Status and Body:
	// an answer the client cannot read after Bring booked.
	AfterBook bool
	// Times is how many requests it answers; 0 is every one until Clear.
	Times int
}

// Request is one request the server received, in order.
type Request struct {
	Method, Path, Query string
	Header              http.Header
	Body                []byte
}

// Event is one tracking event a test scripts.
type Event struct {
	Status, Description, City, PostalCode string
	At                                    time.Time
}

// Consignment is a booking the server holds.
type Consignment struct {
	Number, CorrelationID, Reference, Product, CustomerNumber string
	PackageNumbers                                            []string
	WeightsKg                                                 []string
	RecipientPostal                                           string
	Test                                                      bool
	Events                                                    []Event
}

// Server is the fake Bring.
type Server struct {
	srv *httptest.Server

	mu              sync.Mutex
	requests        []Request
	fail            map[Op]*Failure
	consignments    []*Consignment
	next            int
	NoFinancialRole bool
}

// New starts a server, closed when t ends.
func New(t testing.TB) *Server {
	s := &Server{fail: map[Op]*Failure{}, next: 1}
	s.srv = httptest.NewTLSServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.srv.Close)
	return s
}

// URL is the server's base URL.
func (s *Server) URL() string { return s.srv.URL }

// Transport trusts the server's certificate.
func (s *Server) Transport() http.RoundTripper { return s.srv.Client().Transport }

// Fail answers op with f until Clear, or f.Times times.
func (s *Server) Fail(op Op, f Failure) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fail[op] = &f
}

// Clear removes every Fail.
func (s *Server) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fail = map[Op]*Failure{}
}

// SetNoFinancialRole makes the Shipping Guide answer list prices only.
func (s *Server) SetNoFinancialRole(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.NoFinancialRole = v
}

// Requests are the requests received so far.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.requests...)
}

// Count is how many requests of method had path.
func (s *Server) Count(method, path string) int {
	n := 0
	for _, r := range s.Requests() {
		if r.Method == method && r.Path == path {
			n++
		}
	}
	return n
}

// Consignments are the bookings made, a copy.
func (s *Server) Consignments() []Consignment {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Consignment, 0, len(s.consignments))
	for _, c := range s.consignments {
		out = append(out, *c)
	}
	return out
}

// SetEvents scripts a consignment's tracking events.
func (s *Server) SetEvents(number string, events ...Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.consignments {
		if c.Number == number {
			c.Events = events
		}
	}
}

// failure takes op's failure, if any.
func (s *Server) failure(op Op) *Failure {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.fail[op]
	if f == nil {
		return nil
	}
	if f.Times > 0 {
		f.Times--
		if f.Times == 0 {
			delete(s.fail, op)
		}
	}
	return f
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	s.requests = append(s.requests, Request{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Header: r.Header.Clone(), Body: body})
	s.mu.Unlock()

	// The label's file is behind the redirect, and needs no keys.
	if strings.HasPrefix(r.URL.Path, "/files/") {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = fmt.Fprintf(w, "%%PDF-1.4\n%% bringtest %s\n%%%%EOF\n", strings.TrimPrefix(r.URL.Path, "/files/"))
		return
	}
	if r.Header.Get("X-Mybring-API-Uid") != UID || r.Header.Get("X-Mybring-API-Key") != Key {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"message": "Authentication failed"})
		return
	}
	if r.Header.Get("X-Bring-Client-URL") == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"message": "X-Bring-Client-URL is required"})
		return
	}
	var op Op
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/shippingguide/api/v2/products":
		op = OpGuide
	case r.Method == http.MethodPost && r.URL.Path == "/booking/api/create":
		op = OpBook
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/labels/"):
		op = OpLabel
	case r.Method == http.MethodGet && r.URL.Path == "/tracking/api/v2/tracking.json":
		op = OpTracking
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/pickuppoint/api/pickuppoint/"):
		op = OpPickup
	default:
		http.NotFound(w, r)
		return
	}
	f := s.failure(op)
	if f != nil && f.Stall == 0 && !f.AfterBook {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.Status)
		_, _ = io.WriteString(w, f.Body)
		return
	}
	if f != nil && f.Stall > 0 && op != OpBook {
		select {
		case <-time.After(f.Stall):
		case <-r.Context().Done():
			return
		}
	}
	switch op {
	case OpGuide:
		s.guide(w, body)
	case OpBook:
		s.book(w, r, body, f)
	case OpLabel:
		http.Redirect(w, r, "/files/"+strings.TrimPrefix(r.URL.Path, "/labels/"), http.StatusFound)
	case OpTracking:
		s.tracking(w, r.URL.Query().Get("q"))
	case OpPickup:
		s.pickup(w, r.URL.Path)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func money(minor int64) string { return fmt.Sprintf("%d.%02d", minor/100, minor%100) }

func (s *Server) guide(w http.ResponseWriter, body []byte) {
	var req struct {
		Consignments []struct {
			ID             string `json:"id"`
			ToPostalCode   string `json:"toPostalCode"`
			FromPostalCode string `json:"fromPostalCode"`
			Packages       []struct {
				GrossWeight int64 `json:"grossWeight"`
			} `json:"packages"`
			Products []struct {
				ID             string `json:"id"`
				CustomerNumber string `json:"customerNumber"`
			} `json:"products"`
		} `json:"consignments"`
	}
	if err := json.Unmarshal(body, &req); err != nil || len(req.Consignments) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"errors": []any{map[string]any{"code": "BAD_REQUEST", "description": "Malformed request"}}})
		return
	}
	s.mu.Lock()
	noFinance := s.NoFinancialRole
	s.mu.Unlock()
	var consignments []any
	for _, c := range req.Consignments {
		grams := int64(0)
		for _, p := range c.Packages {
			grams += p.GrossWeight
		}
		kilos := int64(math.Ceil(float64(grams) / 1000))
		list := int64(10000 + 1000*kilos)
		var prods []any
		for _, p := range c.Products {
			if c.ToPostalCode == RefusedPostal {
				prods = append(prods, map[string]any{"id": p.ID, "errors": []any{map[string]any{"code": RefusedCode, "description": RefusedMessage}}})
				continue
			}
			if p.CustomerNumber == "" {
				prods = append(prods, map[string]any{"id": p.ID, "errors": []any{map[string]any{"code": "CUSTOMER_NUMBER_MISSING", "description": "No customer number"}}})
				continue
			}
			price := func(net int64) map[string]any {
				return map[string]any{"currencyCode": "NOK", unknownFieldName: true, "priceWithoutAdditionalServices": map[string]any{
					"amountWithoutVAT": money(net), "vat": money(net / 4), "amountWithVAT": money(net + net/4),
				}}
			}
			pr := map[string]any{"listPrice": price(list)}
			if !noFinance {
				pr["netPrice"] = price(list * 8 / 10)
			}
			prods = append(prods, map[string]any{
				"id": p.ID, unknownFieldName: "x", "guiInformation": map[string]any{"displayName": "Bring " + p.ID},
				"price":            pr,
				"expectedDelivery": map[string]any{"workingDays": "1", "expectedDeliveryDate": map[string]any{"year": "2026", "month": "11", "day": "10"}},
			})
		}
		consignments = append(consignments, map[string]any{"id": c.ID, "products": prods})
	}
	writeJSON(w, http.StatusOK, map[string]any{"consignments": consignments, unknownFieldName: 1})
}

func (s *Server) book(w http.ResponseWriter, r *http.Request, body []byte, f *Failure) {
	var req struct {
		TestIndicator bool `json:"testIndicator"`
		Consignments  []struct {
			CorrelationID string `json:"correlationId"`
			Parties       struct {
				Sender struct {
					Reference string `json:"reference"`
				} `json:"sender"`
				Recipient struct {
					PostalCode string `json:"postalCode"`
				} `json:"recipient"`
			} `json:"parties"`
			Product struct {
				ID             string `json:"id"`
				CustomerNumber string `json:"customerNumber"`
			} `json:"product"`
			Packages []struct {
				CorrelationID string      `json:"correlationId"`
				WeightInKg    json.Number `json:"weightInKg"`
			} `json:"packages"`
		} `json:"consignments"`
	}
	if err := json.Unmarshal(body, &req); err != nil || len(req.Consignments) != 1 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"message": "Malformed request"})
		return
	}
	in := req.Consignments[0]
	if in.Parties.Recipient.PostalCode == RefusedPostal {
		writeJSON(w, http.StatusBadRequest, map[string]any{"consignments": []any{map[string]any{
			"correlationId": in.CorrelationID, "confirmation": nil,
			"errors": []any{map[string]any{"uniqueId": "e1", "code": RefusedCode, "messages": []any{
				map[string]any{"lang": "no", "message": "Ugyldig postnummer."}, map[string]any{"lang": "en", "message": RefusedMessage},
			}}},
		}}})
		return
	}
	s.mu.Lock()
	n := s.next
	s.next++
	c := &Consignment{
		Number: fmt.Sprintf("7040%011d", n), CorrelationID: in.CorrelationID, Reference: in.Parties.Sender.Reference,
		Product: in.Product.ID, CustomerNumber: in.Product.CustomerNumber, RecipientPostal: in.Parties.Recipient.PostalCode,
		Test: req.TestIndicator || r.Header.Get("X-Bring-Test-Indicator") == "true",
	}
	var pkgs []any
	for i, p := range in.Packages {
		num := fmt.Sprintf("3704%011d%02d", n, i+1)
		c.PackageNumbers = append(c.PackageNumbers, num)
		c.WeightsKg = append(c.WeightsKg, p.WeightInKg.String())
		pkgs = append(pkgs, map[string]any{"correlationId": p.CorrelationID, "packageNumber": num})
	}
	s.consignments = append(s.consignments, c)
	s.mu.Unlock()

	base := "https://" + r.Host
	links := map[string]any{"labels": base + "/labels/" + c.Number + ".pdf", "tracking": "https://tracking.bring.com/tracking/" + c.Number, "waybill": nil}
	if in.Product.ID == "5100" || in.Product.ID == "5300" || in.Product.ID == "5400" {
		links["waybill"] = base + "/labels/waybill-" + c.Number + ".pdf"
	}
	if f != nil && f.AfterBook {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.Status)
		_, _ = io.WriteString(w, f.Body)
		return
	}
	if f != nil && f.Stall > 0 {
		select {
		case <-time.After(f.Stall):
		case <-r.Context().Done():
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{unknownFieldName: true, "consignments": []any{map[string]any{
		"correlationId": in.CorrelationID, "errors": nil,
		"confirmation": map[string]any{
			"consignmentNumber": c.Number, "links": links, "packages": pkgs, unknownFieldName: "x",
			"dateAndTimes": map[string]any{"earliestPickup": nil},
		},
	}}})
}

func (s *Server) tracking(w http.ResponseWriter, q string) {
	s.mu.Lock()
	var found *Consignment
	for _, c := range s.consignments {
		if c.Number == q || c.Reference == q || c.CorrelationID == q {
			found = c
		}
		for _, p := range c.PackageNumbers {
			if p == q {
				found = c
			}
		}
	}
	var copyOf Consignment
	if found != nil {
		copyOf = *found
	}
	s.mu.Unlock()
	if found == nil {
		writeJSON(w, http.StatusOK, map[string]any{"consignmentSet": []any{map[string]any{
			"error": map[string]any{"code": 404, "message": "No shipments found"},
		}}})
		return
	}
	events := copyOf.Events
	if len(events) == 0 {
		events = []Event{{Status: "PRE_NOTIFIED", Description: "The shipment is pre-notified", At: time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)}}
	}
	var pkgs []any
	for _, p := range copyOf.PackageNumbers {
		var es []any
		// Bring lists the newest first.
		for i := len(events) - 1; i >= 0; i-- {
			e := events[i]
			es = append(es, map[string]any{
				"status": e.Status, "description": e.Description, "dateIso": e.At.Format(time.RFC3339),
				"city": e.City, "postalCode": e.PostalCode, "country": "Norway", unknownFieldName: 2,
			})
		}
		pkgs = append(pkgs, map[string]any{"packageNumber": p, "eventSet": es})
	}
	writeJSON(w, http.StatusOK, map[string]any{"consignmentSet": []any{map[string]any{
		"consignmentId": copyOf.Number, "senderReference": copyOf.Reference, "packageSet": pkgs, unknownFieldName: "y",
	}}})
}

func (s *Server) pickup(w http.ResponseWriter, path string) {
	rest := strings.TrimSuffix(strings.TrimPrefix(path, "/pickuppoint/api/pickuppoint/"), ".json")
	parts := strings.Split(rest, "/")
	if len(parts) != 3 || parts[1] != "postalCode" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"message": "Malformed path"})
		return
	}
	pc := parts[2]
	n, _ := strconv.Atoi(pc)
	writeJSON(w, http.StatusOK, map[string]any{"pickupPoint": []any{
		map[string]any{"id": strconv.Itoa(100000 + n), "name": "Kiwi " + pc, "address": "Storgata 1", "postalCode": pc, "city": "OSLO",
			"countryCode": parts[0], "openingHoursEnglish": "Mon-Sat 07-23", "type": 1, unknownFieldName: true},
		map[string]any{"id": strconv.Itoa(200000 + n), "name": "Pakkeboks " + pc, "address": "Storgata 2", "postalCode": pc, "city": "OSLO",
			"countryCode": parts[0], "openingHoursEnglish": "24/7", "type": "LOCKER", "locker": true, "maxWeight": "20"},
	}})
}
