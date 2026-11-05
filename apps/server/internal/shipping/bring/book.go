package bring

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/vantigo-io/vantigo/server/internal/shipping"
)

// This file is the Booking API (POST /booking/api/create) and the label. A
// consignment carries a correlationId, the parties, the product and its
// additional services, and packages in kilograms; Bring answers the
// consignment number, a package number per package and links to the labels
// (a PDF behind a redirect), the waybill and tracking. Bring has no
// idempotency key: the caller's key is the correlationId and the sender's
// reference, and a retry (BookRequest.Retry) looks the reference up through
// tracking first and answers the booking it finds.

type bookContact struct {
	Name        string `json:"name,omitempty"`
	Email       string `json:"email,omitempty"`
	PhoneNumber string `json:"phoneNumber,omitempty"`
}

type bookParty struct {
	Name         string       `json:"name"`
	AddressLine  string       `json:"addressLine"`
	AddressLine2 string       `json:"addressLine2,omitempty"`
	PostalCode   string       `json:"postalCode"`
	City         string       `json:"city"`
	CountryCode  string       `json:"countryCode"`
	Reference    string       `json:"reference,omitempty"`
	Contact      *bookContact `json:"contact,omitempty"`
}

type bookDimensions struct {
	HeightInCm int64 `json:"heightInCm,omitempty"`
	WidthInCm  int64 `json:"widthInCm,omitempty"`
	LengthInCm int64 `json:"lengthInCm,omitempty"`
}

type bookPackage struct {
	CorrelationID    string          `json:"correlationId"`
	WeightInKg       json.Number     `json:"weightInKg"`
	GoodsDescription string          `json:"goodsDescription,omitempty"`
	Dimensions       *bookDimensions `json:"dimensions,omitempty"`
}

type bookService struct {
	ID string `json:"id"`
}

type bookProduct struct {
	ID                 string        `json:"id"`
	CustomerNumber     string        `json:"customerNumber"`
	AdditionalServices []bookService `json:"additionalServices,omitempty"`
}

type bookPickupPoint struct {
	ID          string `json:"id"`
	CountryCode string `json:"countryCode"`
}

type bookConsignment struct {
	CorrelationID    string `json:"correlationId"`
	ShippingDateTime string `json:"shippingDateTime"`
	Parties          struct {
		Sender      bookParty        `json:"sender"`
		Recipient   bookParty        `json:"recipient"`
		PickupPoint *bookPickupPoint `json:"pickupPoint,omitempty"`
	} `json:"parties"`
	Product  bookProduct   `json:"product"`
	Packages []bookPackage `json:"packages"`
}

type bookRequest struct {
	SchemaVersion int               `json:"schemaVersion"`
	TestIndicator bool              `json:"testIndicator"`
	Consignments  []bookConsignment `json:"consignments"`
}

type bookAnswer struct {
	Consignments []struct {
		CorrelationID string `json:"correlationId"`
		Confirmation  *struct {
			ConsignmentNumber string `json:"consignmentNumber"`
			Links             struct {
				Labels   string `json:"labels"`
				Waybill  string `json:"waybill"`
				Tracking string `json:"tracking"`
			} `json:"links"`
			Packages []struct {
				CorrelationID string `json:"correlationId"`
				PackageNumber string `json:"packageNumber"`
			} `json:"packages"`
			Product *struct {
				ID string `json:"id"`
			} `json:"product"`
		} `json:"confirmation"`
		Errors []bringError `json:"errors"`
	} `json:"consignments"`
}

// kg is grams as kilograms with three decimals, exactly.
func kg(grams int64) json.Number {
	return json.Number(new(big.Rat).SetFrac64(grams, 1000).FloatString(3))
}

func partyOf(a shipping.Address, reference string) bookParty {
	p := bookParty{
		Name: a.Name, AddressLine: a.Line1, AddressLine2: a.Line2, PostalCode: a.PostalCode, City: a.City,
		CountryCode: countryOr(a.Country), Reference: reference,
	}
	if a.ContactName != "" || a.Email != "" || a.Phone != "" {
		p.Contact = &bookContact{Name: a.ContactName, Email: a.Email, PhoneNumber: a.Phone}
	}
	return p
}

// checkBook refuses a booking Bring would refuse, before anything is sent.
func checkBook(r shipping.BookRequest) error {
	if r.IdempotencyKey == "" || len(r.IdempotencyKey) > 64 {
		return fmt.Errorf("%w: an idempotency key of 1–64 characters is required", shipping.ErrInvalidRequest)
	}
	if _, ok := productOf(r.Service); !ok {
		return fmt.Errorf("%w: Bring product %q is not one this adapter books", shipping.ErrInvalidRequest, r.Service)
	}
	if err := checkPackages(r.Packages); err != nil {
		return err
	}
	for _, a := range []shipping.Address{r.Sender, r.Recipient} {
		if a.Name == "" || a.Line1 == "" || a.PostalCode == "" || a.City == "" {
			return fmt.Errorf("%w: the sender and the recipient each need a name, an address, a postal code and a city", shipping.ErrInvalidRequest)
		}
	}
	return nil
}

// Book books one consignment. With Retry it first looks the key up as a
// reference through tracking, and answers the booking an earlier call made
// when it finds one (Recovered: Bring's tracking does not give the label's
// link again); it books only when it finds none.
func (c *Client) Book(ctx context.Context, r shipping.BookRequest) (shipping.Booking, error) {
	if err := checkBook(r); err != nil {
		return shipping.Booking{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	if r.Retry {
		found, err := c.track(ctx, r.IdempotencyKey)
		if err != nil {
			return shipping.Booking{}, err
		}
		if found != nil && found.ConsignmentNumber != "" {
			return shipping.Booking{
				ConsignmentNumber: found.ConsignmentNumber, PackageNumbers: found.PackageNumbers, Service: r.Service,
				TrackingURL: cmpOr(found.TrackingURL, trackingPage+found.ConsignmentNumber), Recovered: true,
			}, nil
		}
	}
	day := r.ShippingDate
	if day.IsZero() {
		day = c.now()
	}
	cons := bookConsignment{CorrelationID: r.IdempotencyKey, ShippingDateTime: day.UTC().Format("2006-01-02T15:04:05")}
	// The sender's reference is the key alone, so tracking finds it exactly;
	// SenderReference has no field of its own at Bring.
	cons.Parties.Sender = partyOf(r.Sender, r.IdempotencyKey)
	cons.Parties.Recipient = partyOf(r.Recipient, r.RecipientReference)
	if r.Recipient.PickupPointID != "" {
		cons.Parties.PickupPoint = &bookPickupPoint{ID: r.Recipient.PickupPointID, CountryCode: countryOr(r.Recipient.Country)}
	}
	cons.Product = bookProduct{ID: r.Service, CustomerNumber: c.customerFor(r.Service)}
	for _, v := range r.ValueAddedServices {
		cons.Product.AdditionalServices = append(cons.Product.AdditionalServices, bookService{ID: v})
	}
	for i, p := range r.Packages {
		bp := bookPackage{CorrelationID: cmpOr(p.CorrelationID, strconv.Itoa(i+1)), WeightInKg: kg(p.WeightGrams), GoodsDescription: p.Content}
		if p.LengthMM > 0 || p.WidthMM > 0 || p.HeightMM > 0 {
			bp.Dimensions = &bookDimensions{HeightInCm: cm(p.HeightMM), WidthInCm: cm(p.WidthMM), LengthInCm: cm(p.LengthMM)}
		}
		cons.Packages = append(cons.Packages, bp)
	}
	a, err := c.call(ctx, "booking", http.MethodPost, c.endpoint(pathBooking, nil), bookRequest{
		SchemaVersion: 1, TestIndicator: c.test, Consignments: []bookConsignment{cons},
	})
	if err != nil {
		return shipping.Booking{}, err
	}
	var ans bookAnswer
	decodeErr := json.Unmarshal(a.body, &ans)
	// Booking answers its refusals per consignment, under 200 or 400.
	if decodeErr == nil && len(ans.Consignments) == 1 && len(ans.Consignments[0].Errors) > 0 {
		e := ans.Consignments[0].Errors[0]
		return shipping.Booking{}, &shipping.CarrierRefusal{Code: e.code(), Message: e.message()}
	}
	if err := classify("booking", a); err != nil {
		return shipping.Booking{}, err
	}
	if decodeErr != nil || len(ans.Consignments) != 1 || ans.Consignments[0].Confirmation == nil ||
		ans.Consignments[0].Confirmation.ConsignmentNumber == "" {
		return shipping.Booking{}, errors.New("bring: the booking's answer names no consignment")
	}
	conf := ans.Consignments[0].Confirmation
	b := shipping.Booking{
		ConsignmentNumber: conf.ConsignmentNumber, Service: r.Service,
		TrackingURL: cmpOr(conf.Links.Tracking, trackingPage+conf.ConsignmentNumber),
	}
	if conf.Product != nil && conf.Product.ID != "" {
		b.Service = conf.Product.ID
	}
	byCorrelation := map[string]string{}
	for _, p := range conf.Packages {
		byCorrelation[p.CorrelationID] = p.PackageNumber
	}
	for _, p := range cons.Packages {
		b.PackageNumbers = append(b.PackageNumbers, byCorrelation[p.CorrelationID])
	}
	if conf.Links.Labels != "" {
		b.Documents = append(b.Documents, shipping.DocumentRef{Kind: shipping.DocumentLabel, Ref: conf.Links.Labels})
	}
	if conf.Links.Waybill != "" {
		b.Documents = append(b.Documents, shipping.DocumentRef{Kind: shipping.DocumentWaybill, Ref: conf.Links.Waybill})
	}
	return b, nil
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// Label fetches a booking's label or waybill, following Bring's redirect to
// the PDF. Bring issues PDF only.
func (c *Client) Label(ctx context.Context, b shipping.Booking, kind shipping.DocumentKind, format shipping.Format) (shipping.Document, error) {
	if format != shipping.FormatPDF {
		return shipping.Document{}, fmt.Errorf("%w: Bring issues PDF labels only", shipping.ErrNotSupported)
	}
	var ref string
	for _, d := range b.Documents {
		if d.Kind == kind {
			ref = d.Ref
		}
	}
	if ref == "" {
		return shipping.Document{}, shipping.ErrNotFound
	}
	u, err := url.Parse(ref)
	if err != nil || u.Scheme != "https" || !strings.EqualFold(u.Hostname(), c.base.Hostname()) && !strings.HasSuffix(strings.ToLower(u.Hostname()), ".bring.com") {
		return shipping.Document{}, fmt.Errorf("%w: the document's link is not Bring's", shipping.ErrInvalidRequest)
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	a, err := c.call(ctx, "label", http.MethodGet, u.String(), nil)
	if err != nil {
		return shipping.Document{}, err
	}
	if err := classify("label", a); err != nil {
		return shipping.Document{}, err
	}
	if !strings.HasPrefix(string(a.body), "%PDF") {
		return shipping.Document{}, errors.New("bring: the document Bring answered is not a PDF")
	}
	return shipping.Document{Kind: kind, Format: shipping.FormatPDF, ContentType: "application/pdf", Body: a.body}, nil
}
