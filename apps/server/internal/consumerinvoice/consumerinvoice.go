// Package consumerinvoice is the port between a business module and a
// consumer invoice network (invoices phase 6 design I29): Norway's eFaktura,
// which puts an invoice in a person's online bank, and AvtaleGiro, which
// collects it by direct debit. What a module asks of the network — find a
// person, send a shipment of invoices, follow it, cancel a claim, read the
// AvtaleGiro mandates and whether one covers an amount — in the provider's
// terms folded onto one shape.
//
// It is a platform package, like internal/payments: it holds interfaces and
// values only, no SQL — every consumer keeps its own tables (module
// boundaries rule 4) — and it imports no module, only the standard library.
// An adapter lives beside it (consumerinvoice/mps, Mastercard Payment
// Services, the first), and its fake beside the adapter (mps/mpstest) for
// tests only. A second adapter, an aggregator, is a second package beside
// mps, not a change to the port.
//
// No national identity number crosses the port (§14 Q2): a person is found
// by phone, e-mail, date of birth, name and address only. Amounts cross in
// minor units only (Money). The idempotency keys — a shipment's and each
// transaction's id — are the caller's, persisted before the call and reused
// on every retry; an adapter never makes one up, and never retries on its
// own.
package consumerinvoice

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Provider is one submitter's account at the network, opened for one issuer.
type Provider interface {
	// Lookup finds each consumer's eFaktura address, in the order given; at
	// most MaxLookupBatch a call.
	Lookup(ctx context.Context, consumers []Consumer) ([]Address, error)
	// Send hands one shipment to the network. The same shipment id twice is
	// one shipment: a second answers ErrDuplicate.
	Send(ctx context.Context, s Shipment) (Ref, error)
	// Status is what became of each of a shipment's transactions.
	Status(ctx context.Context, ref Ref) ([]TransactionStatus, error)
	// Cancel asks the network to stop one transaction's claim.
	Cancel(ctx context.Context, ref Ref, transactionID string) (CancelOutcome, error)
	// Mandates reads the next page of the AvtaleGiro mandates created,
	// changed or deleted since the last page delivered. The same requestID
	// answers the same page again; a new one moves on.
	Mandates(ctx context.Context, requestID string) (MandatePage, error)
	// Covers is whether the mandate with this KID covers amount: the
	// payer's limit is never answered, only this.
	Covers(ctx context.Context, kid string, amount Money) (bool, error)
	// Verify makes the cheapest authenticated calls, for the settings page.
	Verify(ctx context.Context) error
}

// MaxLookupBatch is the most consumers one Lookup takes, and MaxShipment the
// most transactions one shipment carries.
const (
	MaxLookupBatch = 1000
	MaxShipment    = 1000
)

// Money is an amount in a currency's minor units — øre for NOK, the only
// currency the network carries.
type Money struct {
	Currency string
	Minor    int64
}

// NOK is minor øre.
func NOK(minor int64) Money { return Money{Currency: "NOK", Minor: minor} }

// String writes the amount with two decimals and no currency, "1234.56", the
// network's own form, by integer arithmetic only.
func (m Money) String() string {
	sign := ""
	magnitude := uint64(m.Minor)
	if m.Minor < 0 {
		sign = "-"
		magnitude = -magnitude
	}
	return fmt.Sprintf("%s%d.%02d", sign, magnitude/100, magnitude%100)
}

// Consumer is one person's lookup key. It has no national identity number
// field, by design (§14 Q2).
type Consumer struct {
	// Phone is with its country code, "+4712345678"; Email as given.
	Phone, Email string
	// DateOfBirth is nil when not known.
	DateOfBirth *time.Time
	// FirstName and LastName are the person's name, split as the caller
	// knows it.
	FirstName, LastName string
	// AddressLines, PostalCode, City and Country (ISO 3166-1 alpha-2) are
	// the postal address.
	AddressLines     []string
	PostalCode, City string
	Country          string
}

// MatchableKeys is whether a consumer satisfies the network's matching rule
// without a unique key: two of phone, e-mail and date of birth; or one of
// them with the name, the address or the postal code; or none of them with
// the name, the address and the postal code all. A consumer that does not is
// not looked up: the answer would be LookupMissingData.
func (c Consumer) MatchableKeys() bool {
	strong := 0
	for _, set := range []bool{c.Phone != "", c.Email != "", c.DateOfBirth != nil} {
		if set {
			strong++
		}
	}
	name := c.FirstName != "" && c.LastName != ""
	address := len(c.AddressLines) > 0
	postal := c.PostalCode != ""
	switch {
	case strong >= 2:
		return true
	case strong == 1:
		return name || address || postal
	default:
		return name && address && postal
	}
}

// LookupStatus is what the network found for one consumer.
type LookupStatus string

const (
	// LookupFound is exactly one receiver.
	LookupFound LookupStatus = "found"
	// LookupNotFound is no receiver.
	LookupNotFound LookupStatus = "not_found"
	// LookupAmbiguous is more than one receiver matching the key.
	LookupAmbiguous LookupStatus = "ambiguous"
	// LookupMissingData is a key the matching rule cannot use.
	LookupMissingData LookupStatus = "missing_data"
)

// Address is one consumer's answer.
type Address struct {
	Status LookupStatus
	// Identifier is the 9-digit eFaktura identifier, set when found.
	Identifier string
	// AutoAccept is the consumer's "I want everything" choice; HasAgreement
	// an agreement with this issuer; Blocked a block of this issuer.
	AutoAccept, HasAgreement, Blocked bool
}

// Sendable is whether an invoice may go to the address: found, not
// blocked, and wanted.
func (a Address) Sendable() bool {
	return a.Status == LookupFound && !a.Blocked && (a.AutoAccept || a.HasAgreement)
}

// PaymentType is what a transaction carries.
type PaymentType string

const (
	// PaymentEfaktura is the eFaktura alone.
	PaymentEfaktura PaymentType = "efaktura"
	// PaymentBoth is the eFaktura and its AvtaleGiro claim, notified by the
	// eFaktura.
	PaymentBoth PaymentType = "both"
)

// Shipment is one call's worth of transactions from one issuer.
type Shipment struct {
	// ID is the caller's UUID for the shipment, its idempotency key, and
	// MessageID its 5-50 character message identifier.
	ID, MessageID string
	Transactions  []Transaction
}

// Transaction is one invoice.
type Transaction struct {
	// ID is the caller's UUID for the transaction.
	ID          string
	PaymentType PaymentType
	// KID is the invoice's KID — for PaymentBoth the mandate's, the customer
	// KID — Account the 11-digit account it is paid to.
	KID, Account string
	Amount       Money
	DueDate      time.Time
	// Identifier is the consumer's eFaktura identifier, and Reference the
	// issuer's eFaktura reference for the consumer (the customer number).
	Identifier, Reference string
	// BrandName is the issuer's name in the bank (at most 40 characters),
	// AbbreviatedName the AvtaleGiro short name (at most 10).
	BrandName, AbbreviatedName string
	// ExternalReference is the issuer's own reference, the invoice number.
	ExternalReference string
	// PDF is the invoice as issued.
	PDF []byte
}

// Ref is the network's name for one shipment.
type Ref string

// EfakturaState is an eFaktura's state, folded.
type EfakturaState string

const (
	// EfakturaSubmitted is any state short of an outcome.
	EfakturaSubmitted EfakturaState = "submitted"
	// EfakturaDelivered is accepted or processed: in the consumer's bank.
	EfakturaDelivered EfakturaState = "delivered"
	// EfakturaFailed is rejected, stopped or not found: final.
	EfakturaFailed EfakturaState = "failed"
	// EfakturaCancelled is a cancelled transaction.
	EfakturaCancelled EfakturaState = "cancelled"
)

// AvtaleGiroState is a claim's state, folded; "" for a transaction without
// one.
type AvtaleGiroState string

const (
	// AvtaleGiroSubmitted is any state short of an outcome.
	AvtaleGiroSubmitted AvtaleGiroState = "submitted"
	// AvtaleGiroAccepted is a claim the bank will debit on the due date.
	AvtaleGiroAccepted AvtaleGiroState = "accepted"
	// AvtaleGiroRejected is a claim refused, by the bank or the payer.
	AvtaleGiroRejected AvtaleGiroState = "rejected"
	// AvtaleGiroCancelled is a claim cancelled.
	AvtaleGiroCancelled AvtaleGiroState = "cancelled"
)

// TransactionStatus is one transaction as the network holds it.
type TransactionStatus struct {
	TransactionID string
	Efaktura      EfakturaState
	AvtaleGiro    AvtaleGiroState
	// ProviderEfaktura and ProviderAvtaleGiro are the network's own state
	// names, kept for the record.
	ProviderEfaktura, ProviderAvtaleGiro string
	// Attention is the amount above the mandate's limit, flagged by the bank.
	Attention bool
	// RejectionCode and RejectionText are the network's words; redacted
	// before they are stored or logged.
	RejectionCode, RejectionText string
	// Raw is the network's answer as it came, stored as the evidence.
	Raw []byte
}

// CancelOutcome is what a cancellation asked for.
type CancelOutcome string

const (
	// CancelRequested is taken: the next Status says cancelled.
	CancelRequested CancelOutcome = "requested"
	// CancelNotAllowed is too late, or not a transaction that can be.
	CancelNotAllowed CancelOutcome = "not_allowed"
	// CancelTryLater is the network busy with the transaction.
	CancelTryLater CancelOutcome = "try_later"
)

// MandateStatus is a mandate's state.
type MandateStatus string

const (
	MandateActive   MandateStatus = "active"
	MandateInactive MandateStatus = "inactive"
	MandatePending  MandateStatus = "pending"
)

// Mandate is one AvtaleGiro mandate as the queue delivered it.
type Mandate struct {
	// KID is the mandate's reference: the customer KID the payer entered.
	KID    string
	Status MandateStatus
	// NotifyByMail is the payer asking for a paper notice.
	NotifyByMail bool
	// Deleted is a mandate the payer removed.
	Deleted bool
}

// MandatePage is one page of the delta queue. More is whether another page
// waits; an empty page with More false is nothing new.
type MandatePage struct {
	Mandates []Mandate
	More     bool
}

// ErrRefused is the network's refusal of a request it understood: a fault
// with its code and text, for the caller to redact and record. It is not
// retryable.
type ErrRefused struct {
	Code, Text string
}

func (e *ErrRefused) Error() string {
	if e.Text == "" {
		return "consumerinvoice: the network refused the request: " + e.Code
	}
	return "consumerinvoice: the network refused the request: " + e.Code + ": " + e.Text
}

// ErrThrottled is the network asking the caller to slow down. RetryAfter is
// zero when it named no wait.
type ErrThrottled struct {
	RetryAfter time.Duration
}

func (e *ErrThrottled) Error() string {
	if e.RetryAfter <= 0 {
		return "consumerinvoice: the network is throttling requests"
	}
	return fmt.Sprintf("consumerinvoice: the network is throttling requests; retry after %s", e.RetryAfter)
}

var (
	// ErrUnauthorized is the network refusing the certificate or the user.
	ErrUnauthorized = errors.New("consumerinvoice: the network refused the credentials")
	// ErrDuplicate is a shipment id the network has seen: the earlier Send
	// went through.
	ErrDuplicate = errors.New("consumerinvoice: the shipment was received already")
)

// Any other error from a Provider — a transport failure, a timeout, a 5xx
// without a fault, an answer that does not decode — is a plain error:
// retryable, and the outcome of a Send that met one is unknown.
