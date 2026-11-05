// Package shipping is the port between a business module and a carrier
// (orders design O49): what a module asks of a carrier — the options and
// prices for a parcel, a booking, its label and waybill, its tracking, a
// cancel, the products an account may book, pickup points and a check of the
// keys — in the carrier's terms folded onto one shape.
//
// It is a platform package, shaped like internal/payments: it holds
// interfaces and values only, no SQL — every consumer keeps its own tables
// (module boundaries rule 4) — and it imports no module, only the standard
// library and internal/buildinfo. An adapter lives beside it (shipping/bring),
// and its fake beside the adapter (shipping/bring/bringtest), for tests only.
//
// Money crosses the port in minor units, weights in grams and dimensions in
// millimetres; an adapter converts to what its carrier takes. The idempotency
// key is the caller's, persisted with the shipment before the call and reused
// on every retry; an adapter never makes one up, and never retries on its own.
// A carrier without an idempotency key of its own (Bring) is made safe by the
// adapter: the key travels as the consignment's reference, and a retry looks
// it up before booking again.
package shipping

import (
	"context"
	"net/http"
	"time"
)

// Carrier is one carrier account: its keys, its customer numbers and whether
// it runs in the carrier's test mode. It is safe for concurrent use.
type Carrier interface {
	// Capabilities declares what the carrier can do. A caller asks it before
	// offering an operation; an absent capability is never found by an error.
	Capabilities() Capabilities
	// Rates answers one option per product (or delivery window) the carrier
	// offers for the parcel. A carrier with no rate call answers no options.
	Rates(ctx context.Context, r RateRequest) ([]Option, error)
	// Book books a consignment under the caller's idempotency key.
	Book(ctx context.Context, r BookRequest) (Booking, error)
	// Label fetches one of a booking's documents — the label or the waybill —
	// in a format Capabilities lists.
	Label(ctx context.Context, b Booking, kind DocumentKind, format Format) (Document, error)
	// Track answers the tracking of each reference — a consignment number, a
	// package number or the booking's reference — the carrier knows; a
	// reference it does not know, or that did not answer in time, is left
	// out, never an error. When ctx ends before every reference was asked it
	// answers what it has and ctx's error: a reference left out may then
	// never have been asked.
	Track(ctx context.Context, refs []string) ([]TrackingStatus, error)
	// ParseWebhook authenticates a tracking callback against the configured
	// secret and answers what it reports. Only for a carrier whose
	// Capabilities.Webhooks is set; others answer ErrNotSupported.
	ParseWebhook(r WebhookRequest, secret string) ([]TrackingStatus, error)
	// Cancel cancels a booking under the caller's key, and answers one of
	// three outcomes: cancelled, not supported (void it locally) or too late.
	Cancel(ctx context.Context, b Booking, idempotencyKey string) (CancelOutcome, error)
	// Services are the products the account may book: read from the carrier,
	// or from the adapter's data file, never Go constants.
	Services(ctx context.Context) ([]Service, error)
	// PickupPoints answers the pickup points near an address. Only for a
	// carrier whose Capabilities.PickupPoints is set; others answer
	// ErrNotSupported.
	PickupPoints(ctx context.Context, near Address) ([]PickupPoint, error)
	// Verify makes the cheapest authenticated call, for the settings page,
	// and says what the keys may do.
	Verify(ctx context.Context) (Verification, error)
}

// Capabilities is what a carrier can do beyond booking and tracking.
type Capabilities struct {
	// Rates is a carrier that answers prices; without it Rates answers none.
	Rates bool
	// PickupPoints is a carrier that lists pickup points.
	PickupPoints bool
	// DeliveryWindows is a carrier that sells delivery windows (an option's
	// Window), and whose option must be booked by its Token before ExpiresAt.
	DeliveryWindows bool
	// Cancel is a carrier with a cancel call; without it Cancel answers
	// CancelNotSupported.
	Cancel bool
	// Webhooks is a carrier that pushes tracking (ParseWebhook).
	Webhooks bool
	// CoverageCheck is a carrier that says per address whether it delivers.
	CoverageCheck bool
	// LabelFormats are the label formats the carrier issues, PDF first.
	LabelFormats []Format
}

// Money is an amount in minor units (øre) of a currency (ISO 4217).
type Money struct {
	Currency string
	Minor    int64
}

// Address is a party's address and its contact, whose phone and e-mail drive
// the carrier's notices.
type Address struct {
	Name, Line1, Line2 string
	PostalCode, City   string
	Country            string // ISO 3166-1 alpha-2
	ContactName        string
	Phone, Email       string
	PickupPointID      string // the recipient's chosen pickup point, "" for none
}

// Package is one parcel: its weight in grams and its dimensions in
// millimetres (zero when not given), and what it holds.
type Package struct {
	WeightGrams                 int64
	LengthMM, WidthMM, HeightMM int64
	Content                     string
	PackageNumber               string // set once booked
	CorrelationID               string // the caller's id of the package, "" for its position
}

// RateRequest asks for the options to send packages from one address to
// another on a day.
type RateRequest struct {
	From, To Address
	Packages []Package
	// Services narrows the answer to these product codes; empty asks for
	// every product the account may book.
	Services []string
	// ShippingDate is the day the parcel is handed in; zero is today.
	ShippingDate time.Time
}

// Window is a delivery window.
type Window struct{ Start, End time.Time }

// Option is one product (or window) the carrier offers for a parcel.
type Option struct {
	// Service is the product's code, Name its name as the carrier gives it.
	Service, Name string
	// Price is excluding VAT; VAT the VAT on it, in the same currency.
	Price, VAT Money
	// Estimate is set when the price is not the agreement price (Bring's
	// netPrice is an estimate too: the invoice is the truth).
	Estimate bool
	// ExpectedDate is the expected delivery day, nil when not given; Window
	// the delivery window of a carrier that sells them.
	ExpectedDate *time.Time
	Window       *Window
	// ExpiresAt and Token are a quote that must be booked as given before it
	// expires (Capabilities.DeliveryWindows); zero and "" otherwise.
	ExpiresAt *time.Time
	Token     string
}

// BookRequest is one consignment to book.
type BookRequest struct {
	Sender, Recipient Address
	// Service is the product code; ValueAddedServices its additional services.
	Service            string
	ValueAddedServices []string
	Packages           []Package
	// OptionToken is the chosen option's Token, "" for none.
	OptionToken string
	// SenderReference is printed on the label and searchable in tracking;
	// RecipientReference is the customer's.
	SenderReference, RecipientReference string
	// IdempotencyKey is the caller's, stored before the call. A carrier
	// without one of its own gets it as the consignment's reference.
	IdempotencyKey string
	// Retry says an earlier call under the same key may have booked: its
	// outcome is unknown. The adapter answers the existing booking when it
	// finds one, and books only when it finds none.
	Retry bool
	// FindOnly, with Retry, only looks: when the carrier holds no booking
	// under the key the adapter answers ErrNotFound and books nothing. A
	// caller sets it while the earlier call is recent enough that the carrier
	// may not show its booking yet.
	FindOnly bool
	// ShippingDate is the day the parcel is handed in; zero is now.
	ShippingDate time.Time
}

// DocumentKind is a booking's document.
type DocumentKind string

const (
	// DocumentLabel is the label the parcel carries.
	DocumentLabel DocumentKind = "label"
	// DocumentWaybill is the waybill (fraktbrev), for a product that needs
	// one apart from its labels.
	DocumentWaybill DocumentKind = "waybill"
)

// Format is a document's format.
type Format string

const (
	FormatPDF Format = "pdf"
	FormatZPL Format = "zpl"
)

// DocumentRef is where a booking's document is fetched from: opaque to the
// caller, which stores it and hands it back to Label.
type DocumentRef struct {
	Kind DocumentKind
	Ref  string
}

// Document is a fetched document.
type Document struct {
	Kind        DocumentKind
	Format      Format
	ContentType string
	Body        []byte
}

// Booking is a consignment as the carrier booked it.
type Booking struct {
	// ConsignmentNumber is the carrier's number of the consignment, and
	// PackageNumbers one per package, in the request's order.
	ConsignmentNumber string
	PackageNumbers    []string
	// Service is the product actually booked, which may differ from the one
	// asked for after a carrier's fallback.
	Service string
	// Cost is the booked cost when the carrier gives one, nil otherwise;
	// CostEstimate says it is not the agreement price.
	Cost         *Money
	CostEstimate bool
	// TrackingURL is the consignment's public tracking page.
	TrackingURL string
	// Documents are the booking's documents to fetch with Label.
	Documents []DocumentRef
	// Recovered says the booking was found by its reference after an
	// earlier call's outcome was unknown: its documents may be missing, and
	// then the label is printed from the carrier's own portal.
	Recovered bool
}

// State is a consignment's state as the port folds a carrier's statuses.
type State string

const (
	StateBooked         State = "booked"
	StateInTransit      State = "in_transit"
	StateReadyForPickup State = "ready_for_pickup"
	StateDelivered      State = "delivered"
	StateReturned       State = "returned"
	StateException      State = "exception"
	StateCancelled      State = "cancelled"
)

// TrackingEvent is one event as the carrier reported it, folded.
type TrackingEvent struct {
	State State
	// Code and Text are the carrier's own; Place where it happened.
	Code, Text, Place string
	At                time.Time
}

// TrackingStatus is one consignment's tracking.
type TrackingStatus struct {
	// Reference is the reference the caller asked for.
	Reference         string
	ConsignmentNumber string
	PackageNumbers    []string
	// State is the latest event's, StateBooked when there is none.
	State  State
	Events []TrackingEvent
	// TrackingURL is the consignment's public tracking page, "" when unknown.
	TrackingURL string
}

// CancelOutcome is what a cancel came to.
type CancelOutcome string

const (
	// CancelCancelled is a booking the carrier cancelled.
	CancelCancelled CancelOutcome = "cancelled"
	// CancelNotSupported is a carrier with no cancel: the caller voids the
	// booking locally, and its label must not be used.
	CancelNotSupported CancelOutcome = "not_supported"
	// CancelTooLate is a booking the carrier already holds the parcel of.
	CancelTooLate CancelOutcome = "too_late"
)

// Service is one product the account may book.
type Service struct {
	Code           string
	NameEN, NameNB string
	// Segment is "b2c", "b2b", "cargo" or "return".
	Segment string
	// PickupPoint is a product delivered to a pickup point.
	PickupPoint bool
	// Waybill is a product with a waybill apart from its labels.
	Waybill bool
}

// PickupPoint is a place a parcel can be collected from.
type PickupPoint struct {
	ID, Name                  string
	Address, PostalCode, City string
	Country                   string
	// Locker is a parcel locker; otherwise the point is manned.
	Locker       bool
	OpeningHours string
	// MaxWeightGrams and the dimensions are what it takes, 0 when unknown.
	MaxWeightGrams                       int64
	MaxLengthMM, MaxWidthMM, MaxHeightMM int64
}

// Verification is what Verify learned of the keys.
type Verification struct {
	// PricesAvailable is false when the user may not read the account's
	// prices (Bring: no financial role on the customer number).
	PricesAvailable bool
	// Notes are what the settings page should show, in English.
	Notes []string
}

// WebhookRequest is a callback as it arrived: Body the raw bytes, unparsed.
type WebhookRequest struct {
	Method, PathAndQuery string
	Header               http.Header
	Body                 []byte
}
