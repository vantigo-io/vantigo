//go:build bring

package bring_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/shipping"
	"github.com/vantigo-io/vantigo/server/internal/shipping/bring"
)

// The test-mode tests run the adapter against Bring itself, in its test mode
// (orders design O50, research R §2.9):
//
//	go test -tags bring -run TestBringTestEnv ./internal/shipping/bring/
//
// with a Mybring user's BRING_TEST_UID (the login) and BRING_TEST_KEY (its API
// key), and BRING_TEST_CUSTOMER — a test customer number (5 parcels Norway,
// 6 Nordic, 7 cargo) or a real one, every call then marked
// X-Bring-Test-Indicator: true. They skip without them. BRING_TEST_BASE_URL
// overrides https://api.bring.com.
//
// Bring sends notices to real phone numbers and e-mails even in test mode,
// so every contact here is a dummy.

func testEnv(t *testing.T) *bring.Client {
	t.Helper()
	uid, key, customer := os.Getenv("BRING_TEST_UID"), os.Getenv("BRING_TEST_KEY"), os.Getenv("BRING_TEST_CUSTOMER")
	if uid == "" || key == "" || customer == "" {
		t.Skip("BRING_TEST_UID, BRING_TEST_KEY and BRING_TEST_CUSTOMER are not all set")
	}
	c, err := bring.New(bring.Options{
		BaseURL: os.Getenv("BRING_TEST_BASE_URL"), UID: uid, Key: key, ClientURL: "https://vantigo.io",
		CustomerNumbers: strings.Split(customer, ","), Test: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

var (
	envSender = shipping.Address{Name: "Vantigo test sender", Line1: "Testveien 1", PostalCode: "0150", City: "OSLO", Country: "NO",
		ContactName: "Test Sender", Email: "sender@example.invalid", Phone: "+4799999999"}
	envRecipient = shipping.Address{Name: "Vantigo test recipient", Line1: "Testgata 2", PostalCode: "5003", City: "BERGEN", Country: "NO",
		ContactName: "Test Recipient", Email: "recipient@example.invalid", Phone: "+4799999998"}
)

func TestBringTestEnv_VerifyAndRates(t *testing.T) {
	c := testEnv(t)
	ctx := context.Background()
	v, err := c.Verify(ctx)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	t.Logf("prices available: %v %v", v.PricesAvailable, v.Notes)
	opts, err := c.Rates(ctx, shipping.RateRequest{From: envSender, To: envRecipient, Services: []string{"5800", "5000"},
		Packages: []shipping.Package{{WeightGrams: 1500, LengthMM: 300, WidthMM: 200, HeightMM: 100}}})
	if err != nil {
		t.Fatalf("Rates: %v", err)
	}
	if len(opts) == 0 {
		t.Fatal("Rates answered no options")
	}
	for _, o := range opts {
		t.Logf("%s %s: %d %s (+%d VAT), expected %v", o.Service, o.Name, o.Price.Minor, o.Price.Currency, o.VAT.Minor, o.ExpectedDate)
	}
}

func TestBringTestEnv_BookLabelTrack(t *testing.T) {
	c := testEnv(t)
	ctx := context.Background()
	key := uuid.NewString()
	b, err := c.Book(ctx, shipping.BookRequest{
		Sender: envSender, Recipient: envRecipient, Service: "5800", IdempotencyKey: key,
		Packages: []shipping.Package{{WeightGrams: 1500, LengthMM: 300, WidthMM: 200, HeightMM: 100, Content: "Test"}},
	})
	if err != nil {
		t.Fatalf("Book: %v", err)
	}
	t.Logf("booked %s, packages %v, tracking %s", b.ConsignmentNumber, b.PackageNumbers, b.TrackingURL)
	doc, err := c.Label(ctx, b, shipping.DocumentLabel, shipping.FormatPDF)
	if err != nil {
		t.Fatalf("Label: %v", err)
	}
	if !strings.HasPrefix(string(doc.Body), "%PDF") {
		t.Fatal("the label is not a PDF")
	}
	// Tracking may need a while to index a fresh booking: a retry's lookup
	// is logged, not judged.
	time.Sleep(5 * time.Second)
	st, err := c.Track(ctx, []string{key})
	t.Logf("tracking by reference: %+v, %v", st, err)
	pts, err := c.PickupPoints(ctx, envRecipient)
	if err != nil {
		t.Fatalf("PickupPoints: %v", err)
	}
	t.Logf("%d pickup points near %s", len(pts), envRecipient.PostalCode)
}
