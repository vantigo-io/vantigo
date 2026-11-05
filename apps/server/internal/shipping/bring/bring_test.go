package bring_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/shipping"
	"github.com/vantigo-io/vantigo/server/internal/shipping/bring"
	"github.com/vantigo-io/vantigo/server/internal/shipping/bring/bringtest"
)

func client(t *testing.T, srv *bringtest.Server, mutate ...func(*bring.Options)) *bring.Client {
	t.Helper()
	o := bring.Options{
		BaseURL: srv.URL(), UID: bringtest.UID, Key: bringtest.Key, ClientURL: "https://test.vantigo.example",
		CustomerNumbers: []string{bringtest.CustomerParcels, bringtest.CustomerCargo}, Test: true, Transport: srv.Transport(),
		Now: func() time.Time { return time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC) },
	}
	for _, m := range mutate {
		m(&o)
	}
	c, err := bring.New(o)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

var (
	sender = shipping.Address{Name: "Lager AS", Line1: "Lagerveien 1", PostalCode: "0150", City: "OSLO", Country: "NO"}
	// recipient's contacts are dummies: Bring notifies real ones even in test.
	recipient = shipping.Address{Name: "Kunde AS", Line1: "Kundeveien 2", PostalCode: "5003", City: "BERGEN", Country: "NO",
		Phone: "+4700000000", Email: "test@example.invalid"}
)

func TestNew_Refuses(t *testing.T) {
	t.Parallel()
	for name, o := range map[string]bring.Options{
		"http":        {BaseURL: "http://api.bring.com", UID: "u", Key: "k", CustomerNumbers: []string{"5"}},
		"no key":      {UID: "u", CustomerNumbers: []string{"5"}},
		"no customer": {UID: "u", Key: "k"},
	} {
		if _, err := bring.New(o); err == nil {
			t.Errorf("%s: New accepted it", name)
		}
	}
}

func TestCapabilitiesAndServices(t *testing.T) {
	t.Parallel()
	c := client(t, bringtest.New(t))
	caps := c.Capabilities()
	if !caps.Rates || !caps.PickupPoints || caps.Cancel || caps.Webhooks || !slices.Equal(caps.LabelFormats, []shipping.Format{shipping.FormatPDF}) {
		t.Errorf("Capabilities = %+v", caps)
	}
	services, err := c.Services(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	codes := map[string]shipping.Service{}
	for _, s := range services {
		codes[s.Code] = s
	}
	for _, code := range []string{"5800", "5600", "3584", "5801", "5000", "4850", "5100", "5300", "5400"} {
		if _, ok := codes[code]; !ok {
			t.Errorf("Services lacks %s", code)
		}
	}
	if s := codes["5800"]; s.NameNB != "Pakke til hentested" || !s.PickupPoint || s.Segment != "b2c" {
		t.Errorf("5800 = %+v", s)
	}
	if !codes["5100"].Waybill || codes["5000"].Waybill {
		t.Error("only cargo products have a waybill")
	}
}

func TestRates_GramsAndCentimetresWithNetPrice(t *testing.T) {
	t.Parallel()
	srv := bringtest.New(t)
	c := client(t, srv)
	opts, err := c.Rates(context.Background(), shipping.RateRequest{
		From: sender, To: recipient, Services: []string{"5800", "5100"},
		Packages: []shipping.Package{{WeightGrams: 2500, LengthMM: 301, WidthMM: 200, HeightMM: 95}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(opts) != 2 {
		t.Fatalf("options = %+v", opts)
	}
	// 2.5 kg → 3 started kilos: list 130.00, net 104.00, VAT a quarter.
	o := opts[0]
	if o.Service != "5800" || o.Price != (shipping.Money{Currency: "NOK", Minor: 10400}) || o.VAT.Minor != 2600 || !o.Estimate {
		t.Errorf("option = %+v", o)
	}
	if o.ExpectedDate == nil || o.ExpectedDate.Day() != 10 {
		t.Errorf("expected date = %v", o.ExpectedDate)
	}
	var body struct {
		Consignments []struct {
			Packages []struct {
				GrossWeight int64 `json:"grossWeight"`
				Length      int64 `json:"length"`
				Height      int64 `json:"height"`
			} `json:"packages"`
			Products []struct {
				ID             string `json:"id"`
				CustomerNumber string `json:"customerNumber"`
			} `json:"products"`
		} `json:"consignments"`
	}
	reqs := srv.Requests()
	if err := json.Unmarshal(reqs[len(reqs)-1].Body, &body); err != nil {
		t.Fatal(err)
	}
	p := body.Consignments[0].Packages[0]
	if p.GrossWeight != 2500 || p.Length != 31 || p.Height != 10 {
		t.Errorf("package sent = %+v, want grams and whole centimetres rounded up", p)
	}
	// Cargo goes on the second customer number.
	if prods := body.Consignments[0].Products; prods[0].CustomerNumber != "5" || prods[1].CustomerNumber != "7" {
		t.Errorf("customer numbers = %+v", prods)
	}
	h := reqs[len(reqs)-1].Header
	if h.Get("X-Bring-Test-Indicator") != "true" || h.Get("X-Mybring-API-Uid") != bringtest.UID || h.Get("X-Bring-Client-URL") == "" {
		t.Errorf("headers = %v", h)
	}
}

func TestRates_ListPriceWithoutFinancialRole(t *testing.T) {
	t.Parallel()
	srv := bringtest.New(t)
	srv.SetNoFinancialRole(true)
	c := client(t, srv)
	opts, err := c.Rates(context.Background(), shipping.RateRequest{From: sender, To: recipient, Services: []string{"5000"},
		Packages: []shipping.Package{{WeightGrams: 1000}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(opts) != 1 || opts[0].Price.Minor != 11000 {
		t.Errorf("options = %+v, want the list price", opts)
	}
	v, err := c.Verify(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v.PricesAvailable || len(v.Notes) == 0 || !strings.Contains(v.Notes[0], "financial role") {
		t.Errorf("Verify = %+v, want the financial role named", v)
	}
}

func TestRates_RefusalAndKeys(t *testing.T) {
	t.Parallel()
	srv := bringtest.New(t)
	c := client(t, srv)
	to := recipient
	to.PostalCode = bringtest.RefusedPostal
	_, err := c.Rates(context.Background(), shipping.RateRequest{From: sender, To: to, Packages: []shipping.Package{{WeightGrams: 1}}})
	var refusal *shipping.CarrierRefusal
	if !errors.As(err, &refusal) || refusal.Message != bringtest.RefusedMessage {
		t.Errorf("err = %v, want Bring's refusal", err)
	}
	bad := client(t, srv, func(o *bring.Options) { o.Key = "wrong" })
	if _, err := bad.Verify(context.Background()); !errors.Is(err, shipping.ErrUnauthorized) {
		t.Errorf("Verify with a wrong key = %v, want ErrUnauthorized", err)
	}
	if _, err := c.Rates(context.Background(), shipping.RateRequest{From: sender, To: recipient}); !errors.Is(err, shipping.ErrInvalidRequest) {
		t.Errorf("no packages = %v, want ErrInvalidRequest", err)
	}
}

func book(t *testing.T, c *bring.Client, key string, mutate ...func(*shipping.BookRequest)) (shipping.Booking, error) {
	t.Helper()
	r := shipping.BookRequest{
		Sender: sender, Recipient: recipient, Service: "5800", IdempotencyKey: key,
		Packages: []shipping.Package{{WeightGrams: 1250, LengthMM: 300, WidthMM: 200, HeightMM: 100, Content: "Goods"}, {WeightGrams: 7}},
	}
	for _, m := range mutate {
		m(&r)
	}
	return c.Book(context.Background(), r)
}

func TestBook_KilogramsAndLabelRedirect(t *testing.T) {
	t.Parallel()
	srv := bringtest.New(t)
	c := client(t, srv)
	b, err := book(t, c, "5f8e0e7a-3b1c-4d77-9a1e-1f2b3c4d5e6f")
	if err != nil {
		t.Fatal(err)
	}
	if b.ConsignmentNumber == "" || len(b.PackageNumbers) != 2 || b.Service != "5800" ||
		!strings.HasPrefix(b.TrackingURL, "https://tracking.bring.com/tracking/") {
		t.Errorf("booking = %+v", b)
	}
	got := srv.Consignments()
	if len(got) != 1 || !slices.Equal(got[0].WeightsKg, []string{"1.250", "0.007"}) {
		t.Errorf("weights sent = %+v, want kilograms", got)
	}
	if got[0].CorrelationID != "5f8e0e7a-3b1c-4d77-9a1e-1f2b3c4d5e6f" || got[0].Reference != got[0].CorrelationID || !got[0].Test {
		t.Errorf("consignment = %+v, want the key as correlation id and reference, in test mode", got[0])
	}
	if len(b.Documents) != 1 || b.Documents[0].Kind != shipping.DocumentLabel {
		t.Fatalf("documents = %+v, want the label only", b.Documents)
	}
	doc, err := c.Label(context.Background(), b, shipping.DocumentLabel, shipping.FormatPDF)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(doc.Body), "%PDF") || doc.ContentType != "application/pdf" {
		t.Errorf("label = %q", doc.Body)
	}
	if srv.Count("GET", "/files/"+b.ConsignmentNumber+".pdf") != 1 {
		t.Error("the label's redirect was not followed")
	}
	if _, err := c.Label(context.Background(), b, shipping.DocumentWaybill, shipping.FormatPDF); !errors.Is(err, shipping.ErrNotFound) {
		t.Errorf("waybill of a parcel = %v, want ErrNotFound", err)
	}
	if _, err := c.Label(context.Background(), b, shipping.DocumentLabel, shipping.FormatZPL); !errors.Is(err, shipping.ErrNotSupported) {
		t.Errorf("ZPL = %v, want ErrNotSupported", err)
	}
}

func TestBook_CargoHasAWaybill(t *testing.T) {
	t.Parallel()
	srv := bringtest.New(t)
	c := client(t, srv)
	b, err := book(t, c, "cargo-key-1", func(r *shipping.BookRequest) { r.Service = "5100" })
	if err != nil {
		t.Fatal(err)
	}
	if srv.Consignments()[0].CustomerNumber != bringtest.CustomerCargo {
		t.Error("cargo was not booked on the cargo customer number")
	}
	if _, err := c.Label(context.Background(), b, shipping.DocumentWaybill, shipping.FormatPDF); err != nil {
		t.Errorf("waybill: %v", err)
	}
}

func TestBook_Refused(t *testing.T) {
	t.Parallel()
	srv := bringtest.New(t)
	c := client(t, srv)
	_, err := book(t, c, "refused-key", func(r *shipping.BookRequest) { r.Recipient.PostalCode = bringtest.RefusedPostal })
	var refusal *shipping.CarrierRefusal
	if !errors.As(err, &refusal) || refusal.Code != bringtest.RefusedCode || refusal.Message != bringtest.RefusedMessage {
		t.Fatalf("err = %v, want Bring's refusal in English", err)
	}
	if !shipping.IsDefinitiveRefusal(err) {
		t.Error("a refusal is definitive")
	}
	if _, err := book(t, c, ""); !errors.Is(err, shipping.ErrInvalidRequest) {
		t.Errorf("no key = %v, want ErrInvalidRequest", err)
	}
	if _, err := book(t, c, "k", func(r *shipping.BookRequest) { r.Service = "9650" }); !errors.Is(err, shipping.ErrInvalidRequest) {
		t.Errorf("a withdrawn product = %v, want ErrInvalidRequest", err)
	}
	srv.Fail(bringtest.OpBook, bringtest.Failure{Status: 503, Body: "{}", Times: 1})
	if _, err := book(t, c, "k503"); err == nil || shipping.IsDefinitiveRefusal(err) {
		t.Errorf("a 503 = %v, want an unknown outcome", err)
	}
}

// A timeout after Bring booked: the retry under the same key finds the
// booking by its reference and books nothing more.
func TestBook_RetryAfterTimeoutFindsTheBooking(t *testing.T) {
	t.Parallel()
	srv := bringtest.New(t)
	c := client(t, srv, func(o *bring.Options) { o.Timeout = 300 * time.Millisecond })
	srv.Fail(bringtest.OpBook, bringtest.Failure{Stall: 2 * time.Second, Times: 1})
	key := "b0b0b0b0-0000-4000-8000-000000000001"
	if _, err := book(t, c, key); err == nil || shipping.IsDefinitiveRefusal(err) {
		t.Fatalf("first attempt = %v, want an unknown outcome", err)
	}
	if n := len(srv.Consignments()); n != 1 {
		t.Fatalf("bookings after the timeout = %d, want 1", n)
	}
	b, err := book(t, c, key, func(r *shipping.BookRequest) { r.Retry = true })
	if err != nil {
		t.Fatal(err)
	}
	if !b.Recovered || b.ConsignmentNumber != srv.Consignments()[0].Number || len(b.PackageNumbers) != 2 {
		t.Errorf("retry = %+v, want the first booking recovered", b)
	}
	if n := len(srv.Consignments()); n != 1 {
		t.Errorf("bookings after the retry = %d, want still 1", n)
	}
	// A retry that only looks, and finds nothing, books nothing.
	if _, err := book(t, c, "never-booked-key", func(r *shipping.BookRequest) { r.Retry, r.FindOnly = true, true }); !errors.Is(err, shipping.ErrNotFound) {
		t.Errorf("find only = %v, want ErrNotFound", err)
	}
	if n := len(srv.Consignments()); n != 1 {
		t.Errorf("bookings after a find-only retry = %d, want still 1", n)
	}
	// A retry that finds nothing books.
	if _, err := book(t, c, "never-booked-key", func(r *shipping.BookRequest) { r.Retry = true }); err != nil {
		t.Fatal(err)
	}
	if n := len(srv.Consignments()); n != 2 {
		t.Errorf("bookings = %d, want 2", n)
	}
}

func TestTrack_Folds(t *testing.T) {
	t.Parallel()
	srv := bringtest.New(t)
	c := client(t, srv)
	b, err := book(t, c, "track-key")
	if err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)
	srv.SetEvents(b.ConsignmentNumber,
		bringtest.Event{Status: "PRE_NOTIFIED", Description: "Pre-notified", At: day.Add(8 * time.Hour)},
		bringtest.Event{Status: "IN_TRANSIT", Description: "On its way", City: "OSLO", PostalCode: "0150", At: day.Add(12 * time.Hour)},
		bringtest.Event{Status: "READY_FOR_PICKUP", Description: "Ready", City: "BERGEN", At: day.Add(30 * time.Hour)},
	)
	got, err := c.Track(context.Background(), []string{b.ConsignmentNumber, "unknown-123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("statuses = %+v, want the unknown reference left out", got)
	}
	st := got[0]
	if st.State != shipping.StateReadyForPickup || len(st.Events) != 3 || st.Events[0].Code != "PRE_NOTIFIED" ||
		st.Events[1].State != shipping.StateInTransit || st.Events[1].Place != "0150 OSLO" {
		t.Errorf("status = %+v", st)
	}
	srv.SetEvents(b.ConsignmentNumber, bringtest.Event{Status: "DELIVERED", At: day.Add(40 * time.Hour)})
	if got, _ := c.Track(context.Background(), []string{b.PackageNumbers[0]}); len(got) != 1 || got[0].State != shipping.StateDelivered {
		t.Errorf("by package number = %+v, want delivered", got)
	}
	srv.SetEvents(b.ConsignmentNumber, bringtest.Event{Status: "DEVIATION", At: day.Add(41 * time.Hour)})
	if got, _ := c.Track(context.Background(), []string{"track-key"}); len(got) != 1 || got[0].State != shipping.StateException {
		t.Errorf("by reference = %+v, want an exception", got)
	}
}

// A reference whose tracking fails is left out and the next is asked; a
// caller's ctx that has ended stops the poll with its error.
func TestTrack_OneFailedReferenceDoesNotStopThePoll(t *testing.T) {
	t.Parallel()
	srv := bringtest.New(t)
	c := client(t, srv)
	b, err := book(t, c, "track-key-2")
	if err != nil {
		t.Fatal(err)
	}
	srv.Fail(bringtest.OpTracking, bringtest.Failure{Status: 502, Body: "bad gateway", Times: 1})
	got, err := c.Track(context.Background(), []string{"first-fails", b.ConsignmentNumber})
	if err != nil || len(got) != 1 || got[0].ConsignmentNumber != b.ConsignmentNumber {
		t.Errorf("Track = %+v, %v; want the second reference answered", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := c.Track(ctx, []string{b.ConsignmentNumber}); !errors.Is(err, context.Canceled) || len(got) != 0 {
		t.Errorf("Track on an ended ctx = %+v, %v", got, err)
	}
}

func TestCancelIsNotSupported(t *testing.T) {
	t.Parallel()
	c := client(t, bringtest.New(t))
	out, err := c.Cancel(context.Background(), shipping.Booking{ConsignmentNumber: "1"}, "k")
	if err != nil || out != shipping.CancelNotSupported {
		t.Errorf("Cancel = %v, %v", out, err)
	}
	if _, err := c.ParseWebhook(shipping.WebhookRequest{}, "s"); !errors.Is(err, shipping.ErrNotSupported) {
		t.Errorf("ParseWebhook = %v", err)
	}
}

func TestPickupPoints(t *testing.T) {
	t.Parallel()
	c := client(t, bringtest.New(t))
	pts, err := c.PickupPoints(context.Background(), shipping.Address{PostalCode: "0150", Country: "NO"})
	if err != nil {
		t.Fatal(err)
	}
	if len(pts) != 2 || pts[0].ID != "100150" || pts[0].Locker || !pts[1].Locker || pts[1].MaxWeightGrams != 20000 {
		t.Errorf("pickup points = %+v", pts)
	}
}

func TestVerify(t *testing.T) {
	t.Parallel()
	c := client(t, bringtest.New(t))
	v, err := c.Verify(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !v.PricesAvailable {
		t.Errorf("Verify = %+v", v)
	}
}
