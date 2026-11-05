package orders

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
	"github.com/vantigo-io/vantigo/server/internal/orders/store"
)

// This file is the header a quote and an order share (O13) and its customer
// snapshot (O14): built from the billing profile, refreshed on every save of a
// draft, frozen once a quote is sent or an order confirmed.

// baseCurrency is the deployment's currency, a document's when its customer
// names none (O13).
const baseCurrency = "NOK"

// header is a document's header as both tables store it, column for column.
type header struct {
	CustomerID                 int32
	CustomerNumber             *int64
	CustomerName               string
	CustomerType               *string
	CustomerOrganisationNumber *string
	CustomerForeignID          *string
	CustomerCountry            *string
	InvoiceAddressLine1        *string
	InvoiceAddressLine2        *string
	InvoicePostalCode          *string
	InvoiceCity                *string
	InvoiceCountry             *string
	DeliveryAddressID          *int32
	DeliveryName               *string
	DeliveryAddressLine1       *string
	DeliveryAddressLine2       *string
	DeliveryPostalCode         *string
	DeliveryCity               *string
	DeliveryCountry            *string
	ContactID                  *int32
	ContactName                *string
	ContactEmail               *string
	ContactPhone               *string
	YourReference              string
	SalespersonID              *uuid.UUID
	SalespersonDisplay         *string
	ProjectID                  *int32
	Currency                   string
	PricesIncludeVat           bool
	PaymentTermsDays           int32
	DiscountVisible            bool
	Language                   string
	IntroText                  string
	ClosingText                string
	InternalNote               string
	DeliveryDate               pgtype.Date
}

// headerOfQuote and headerOfOrder are a stored document's header.
func headerOfQuote(q store.OrdersQuote) header {
	return header{
		CustomerID: q.CustomerID, CustomerNumber: q.CustomerNumber, CustomerName: q.CustomerName, CustomerType: q.CustomerType,
		CustomerOrganisationNumber: q.CustomerOrganisationNumber, CustomerForeignID: q.CustomerForeignID,
		CustomerCountry: q.CustomerCountry, InvoiceAddressLine1: q.InvoiceAddressLine1, InvoiceAddressLine2: q.InvoiceAddressLine2,
		InvoicePostalCode: q.InvoicePostalCode, InvoiceCity: q.InvoiceCity, InvoiceCountry: q.InvoiceCountry,
		DeliveryAddressID: q.DeliveryAddressID, DeliveryName: q.DeliveryName, DeliveryAddressLine1: q.DeliveryAddressLine1,
		DeliveryAddressLine2: q.DeliveryAddressLine2, DeliveryPostalCode: q.DeliveryPostalCode, DeliveryCity: q.DeliveryCity,
		DeliveryCountry: q.DeliveryCountry, ContactID: q.ContactID, ContactName: q.ContactName, ContactEmail: q.ContactEmail,
		ContactPhone: q.ContactPhone, YourReference: q.YourReference, SalespersonID: q.SalespersonID,
		SalespersonDisplay: q.SalespersonDisplay, ProjectID: q.ProjectID, Currency: q.Currency,
		PricesIncludeVat: q.PricesIncludeVat, PaymentTermsDays: q.PaymentTermsDays, DiscountVisible: q.DiscountVisible,
		Language: q.Language, IntroText: q.IntroText, ClosingText: q.ClosingText, InternalNote: q.InternalNote,
		DeliveryDate: q.DeliveryDate,
	}
}

func headerOfOrder(o store.OrdersOrder) header {
	return header{
		CustomerID: o.CustomerID, CustomerNumber: o.CustomerNumber, CustomerName: o.CustomerName, CustomerType: o.CustomerType,
		CustomerOrganisationNumber: o.CustomerOrganisationNumber, CustomerForeignID: o.CustomerForeignID,
		CustomerCountry: o.CustomerCountry, InvoiceAddressLine1: o.InvoiceAddressLine1, InvoiceAddressLine2: o.InvoiceAddressLine2,
		InvoicePostalCode: o.InvoicePostalCode, InvoiceCity: o.InvoiceCity, InvoiceCountry: o.InvoiceCountry,
		DeliveryAddressID: o.DeliveryAddressID, DeliveryName: o.DeliveryName, DeliveryAddressLine1: o.DeliveryAddressLine1,
		DeliveryAddressLine2: o.DeliveryAddressLine2, DeliveryPostalCode: o.DeliveryPostalCode, DeliveryCity: o.DeliveryCity,
		DeliveryCountry: o.DeliveryCountry, ContactID: o.ContactID, ContactName: o.ContactName, ContactEmail: o.ContactEmail,
		ContactPhone: o.ContactPhone, YourReference: o.YourReference, SalespersonID: o.SalespersonID,
		SalespersonDisplay: o.SalespersonDisplay, ProjectID: o.ProjectID, Currency: o.Currency,
		PricesIncludeVat: o.PricesIncludeVat, PaymentTermsDays: o.PaymentTermsDays, DiscountVisible: o.DiscountVisible,
		Language: o.Language, IntroText: o.IntroText, ClosingText: o.ClosingText, InternalNote: o.InternalNote,
		DeliveryDate: o.DeliveryDate,
	}
}

// headerInput is what a create or a save of a quote or an order says about
// the header: every field optional but the customer. A field left out keeps
// the document's current value on a save, and takes its default on a create.
type headerInput struct {
	CustomerID        int32
	ContactID         *int32
	DeliveryAddressID *int32
	Currency          *string
	DeliveryDate      *openapi_types.Date
	DiscountVisible   *bool
	IntroText         *string
	ClosingText       *string
	InternalNote      *string
	YourReference     *string
	Language          *string
	PaymentTermsDays  *int32
	PricesIncludeVat  *bool
	ProjectID         *int32
	SalespersonID     *uuid.UUID
}

// headerInputOfQuote and headerInputOfQuoteUpdate read the two quote bodies.
func headerInputOfQuote(b gen.OrdersQuoteInput) headerInput {
	in := headerInput{
		CustomerID: b.CustomerId, ContactID: b.ContactId, DeliveryAddressID: b.DeliveryAddressId, Currency: b.Currency,
		DeliveryDate: b.DeliveryDate, DiscountVisible: b.DiscountVisible, IntroText: b.IntroText, ClosingText: b.ClosingText,
		InternalNote: b.InternalNote, YourReference: b.YourReference, PaymentTermsDays: b.PaymentTermsDays,
		PricesIncludeVat: b.PricesIncludeVat, ProjectID: b.ProjectId, SalespersonID: b.SalespersonId,
	}
	if b.Language != nil {
		l := string(*b.Language)
		in.Language = &l
	}
	return in
}

func headerInputOfQuoteUpdate(b gen.OrdersQuoteUpdate) headerInput {
	in := headerInput{
		CustomerID: b.CustomerId, ContactID: b.ContactId, DeliveryAddressID: b.DeliveryAddressId, Currency: b.Currency,
		DeliveryDate: b.DeliveryDate, DiscountVisible: b.DiscountVisible, IntroText: b.IntroText, ClosingText: b.ClosingText,
		InternalNote: b.InternalNote, YourReference: b.YourReference, PaymentTermsDays: b.PaymentTermsDays,
		PricesIncludeVat: b.PricesIncludeVat, ProjectID: b.ProjectId, SalespersonID: b.SalespersonId,
	}
	if b.Language != nil {
		l := string(*b.Language)
		in.Language = &l
	}
	return in
}

// headerReads are the reads a header is resolved from, all made before any
// transaction: the customer's billing profile, the settings, the salesperson
// and the project.
type headerReads struct {
	profile     *contracts.CustomerBillingProfile
	settings    ordersSettings
	salesperson *contracts.UserEntry
	project     *contracts.ProjectEntry
}

// readHeader makes the reads in's header is resolved from, before any
// transaction. salesperson is the one the header names: the input's, else
// base's, else the caller.
func (s *server) readHeader(ctx context.Context, in headerInput, base *header) (headerReads, error) {
	var r headerReads
	var err error
	if r.profile, err = s.billingProfile(ctx, in.CustomerID); err != nil {
		return r, err
	}
	if r.settings, err = readSettings(ctx, store.New(s.deps.Pool)); err != nil {
		return r, err
	}
	salesperson := callerID(ctx)
	switch {
	case in.SalespersonID != nil:
		salesperson = *in.SalespersonID
	case base != nil && base.SalespersonID != nil:
		salesperson = *base.SalespersonID
	}
	if salesperson != uuid.Nil {
		if r.salesperson, err = s.user(ctx, salesperson); err != nil {
			return r, err
		}
	}
	project := in.ProjectID
	if project == nil && base != nil {
		project = base.ProjectID
	}
	if project != nil {
		if r.project, err = s.project(ctx, *project); err != nil {
			return r, err
		}
	}
	return r, nil
}

// customerUsable is the refusal a customer that may get no new document
// answers (customer_disabled): one disabled, archived or merged away. A create
// judges it, and a save that changes the customer; a draft whose customer
// was disabled since can still be saved, and is refused at send or confirm.
func customerUsable(f *fieldErrors, p *contracts.CustomerBillingProfile) {
	switch {
	case p == nil:
	case p.MergedInto != nil:
		f.addCoded("customerId", codeCustomerDisabled, fmt.Sprintf("The customer was merged into customer %d", *p.MergedInto))
	case p.Archived || p.Status != "active":
		f.addCoded("customerId", codeCustomerDisabled, "The customer is disabled or archived")
	}
}

// resolveHeader is in's header over base (the document's current header on a
// save, nil on a create), with the customer snapshot taken afresh from the
// profile (O14), judged into f.
func resolveHeader(f *fieldErrors, in headerInput, base *header, r headerReads) header {
	var h header
	if base != nil {
		h = *base
	}
	if r.profile == nil {
		f.add("customerId", "There is no such customer")
		return h
	}
	p := r.profile
	sameCustomer := base != nil && base.CustomerID == in.CustomerID
	h.CustomerID = in.CustomerID
	snapshotCustomer(&h, p)

	// The delivery address and the contact: the request's, else the
	// document's while still the customer's, else the primary address and no
	// contact.
	h.DeliveryAddressID, h.DeliveryName, h.DeliveryAddressLine1, h.DeliveryAddressLine2 = nil, nil, nil, nil
	h.DeliveryPostalCode, h.DeliveryCity, h.DeliveryCountry = nil, nil, nil
	want := in.DeliveryAddressID
	if want == nil && sameCustomer && base.DeliveryAddressID != nil {
		want = base.DeliveryAddressID
	}
	var chosen *contracts.CustomerAddressEntry
	for i := range p.DeliveryAddresses {
		if want != nil && p.DeliveryAddresses[i].ID == *want {
			chosen = &p.DeliveryAddresses[i]
		}
	}
	switch {
	case chosen == nil && in.DeliveryAddressID != nil:
		f.add("deliveryAddressId", "The customer has no such delivery address")
	case chosen == nil && len(p.DeliveryAddresses) > 0:
		chosen = &p.DeliveryAddresses[0]
	}
	if chosen != nil {
		h.DeliveryAddressID = &chosen.ID
		h.DeliveryName = textPtr(chosen.Label)
		h.DeliveryAddressLine1, h.DeliveryAddressLine2 = textPtr(chosen.Line1), textPtr(chosen.Line2)
		h.DeliveryPostalCode, h.DeliveryCity = textPtr(chosen.PostalCode), textPtr(chosen.City)
		h.DeliveryCountry = countryPtr(chosen.Country)
	}

	h.ContactID, h.ContactName, h.ContactEmail, h.ContactPhone = nil, nil, nil, nil
	wantContact := in.ContactID
	if wantContact == nil && sameCustomer && base.ContactID != nil {
		wantContact = base.ContactID
	}
	if wantContact != nil {
		var contact *contracts.CustomerContactEntry
		for i := range p.Contacts {
			if p.Contacts[i].ID == *wantContact {
				contact = &p.Contacts[i]
			}
		}
		switch {
		case contact != nil:
			h.ContactID = &contact.ID
			h.ContactName, h.ContactEmail, h.ContactPhone = textPtr(contact.Name), textPtr(contact.Email), textPtr(contact.Phone)
		case in.ContactID != nil:
			f.add("contactId", "The customer has no such contact")
		}
	}

	// The references, our side and the money.
	switch {
	case in.YourReference != nil:
		h.YourReference = strings.TrimSpace(*in.YourReference)
	case base == nil || !sameCustomer:
		h.YourReference = p.BuyerReference
	}
	if utf8.RuneCountInString(h.YourReference) > 100 {
		f.add("yourReference", "Your reference is at most 100 characters")
	}
	if r.salesperson != nil {
		h.SalespersonID = &r.salesperson.ID
		h.SalespersonDisplay = textPtr(r.salesperson.DisplayName)
	} else if in.SalespersonID != nil {
		f.add("salespersonId", "There is no such user")
	}
	if in.ProjectID != nil || (base != nil && base.ProjectID != nil) {
		switch {
		case r.project == nil:
			f.add("projectId", "There is no such project")
		case r.project.CustomerID != nil && *r.project.CustomerID != in.CustomerID:
			f.addCoded("projectId", codeProjectCustomerMismatch, "The project belongs to another customer")
		default:
			h.ProjectID = &r.project.ID
		}
	}
	switch {
	case in.Currency != nil:
		h.Currency = strings.ToUpper(strings.TrimSpace(*in.Currency))
	case base == nil || !sameCustomer:
		h.Currency = strings.ToUpper(p.Currency)
	}
	if h.Currency == "" {
		h.Currency = baseCurrency
	}
	if len(h.Currency) != 3 {
		f.add("currency", "A currency is three letters, as NOK")
	}
	switch {
	case in.PricesIncludeVat != nil:
		h.PricesIncludeVat = *in.PricesIncludeVat
	case base == nil || !sameCustomer:
		h.PricesIncludeVat = p.Type == "person"
	}
	switch {
	case in.PaymentTermsDays != nil:
		h.PaymentTermsDays = *in.PaymentTermsDays
	case base == nil || !sameCustomer:
		h.PaymentTermsDays = r.settings.defaultPaymentTermsDays
		if p.PaymentTermsDays != nil {
			h.PaymentTermsDays = *p.PaymentTermsDays
		}
	}
	if h.PaymentTermsDays < 0 || h.PaymentTermsDays > 365 {
		f.add("paymentTermsDays", "Payment terms are 0 to 365 days")
	}
	switch {
	case in.DiscountVisible != nil:
		h.DiscountVisible = *in.DiscountVisible
	case base == nil:
		h.DiscountVisible = true
	}
	switch {
	case in.Language != nil:
		h.Language = *in.Language
	case base == nil || !sameCustomer:
		h.Language = "nb"
		if strings.EqualFold(p.Language, "en") {
			h.Language = "en"
		}
	}
	if h.Language != "nb" && h.Language != "en" {
		f.add("language", "The language is nb or en")
	}

	// The texts and the delivery date.
	for _, t := range []struct {
		field string
		in    *string
		out   *string
	}{{"introText", in.IntroText, &h.IntroText}, {"closingText", in.ClosingText, &h.ClosingText}, {"internalNote", in.InternalNote, &h.InternalNote}} {
		if t.in != nil {
			*t.out = *t.in
		}
		if utf8.RuneCountInString(*t.out) > 4000 {
			f.add(t.field, "A text is at most 4000 characters")
		}
	}
	if in.DeliveryDate != nil {
		h.DeliveryDate = pgtype.Date{Time: in.DeliveryDate.Time, Valid: true}
	}
	return h
}

// snapshotCustomer copies the customer's own fields from the profile (O13):
// the number, name, type, legal id and country, and the invoice address.
func snapshotCustomer(h *header, p *contracts.CustomerBillingProfile) {
	number := p.CustomerNumber
	h.CustomerNumber = &number
	h.CustomerName = p.Name
	h.CustomerType = textPtr(p.Type)
	h.CustomerOrganisationNumber, h.CustomerForeignID = nil, nil
	switch {
	case strings.EqualFold(p.LegalCountry, "no") && len(p.LegalID) == 9:
		h.CustomerOrganisationNumber = textPtr(p.LegalID)
	case p.LegalID != "":
		h.CustomerForeignID = textPtr(p.LegalID)
	}
	h.CustomerCountry = countryPtr(p.LegalCountry)
	h.InvoiceAddressLine1, h.InvoiceAddressLine2, h.InvoicePostalCode, h.InvoiceCity, h.InvoiceCountry = nil, nil, nil, nil, nil
	if a := p.InvoiceAddress; a != nil {
		h.InvoiceAddressLine1, h.InvoiceAddressLine2 = textPtr(a.Line1), textPtr(a.Line2)
		h.InvoicePostalCode, h.InvoiceCity = textPtr(a.PostalCode), textPtr(a.City)
		h.InvoiceCountry = countryPtr(a.Country)
		if h.CustomerCountry == nil {
			h.CustomerCountry = h.InvoiceCountry
		}
	}
}

// textPtr is s trimmed, nil when empty.
func textPtr(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}

// countryPtr is an ISO country code in upper case, nil when empty.
func countryPtr(s string) *string {
	s = strings.ToUpper(strings.TrimSpace(s))
	if len(s) != 2 {
		return nil
	}
	return &s
}

// deref is *p, "" for nil.
func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// dayOf is the civil day of t in loc, at midnight UTC.
func dayOf(t time.Time, loc *time.Location) time.Time {
	y, m, d := t.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// snapshotOut is a header's customer snapshot as the API answers it.
func snapshotOut(h header) gen.OrdersCustomerSnapshot {
	out := gen.OrdersCustomerSnapshot{
		CustomerId: h.CustomerID, CustomerNumber: h.CustomerNumber, Name: h.CustomerName, Country: h.CustomerCountry,
		OrganisationNumber: h.CustomerOrganisationNumber, ForeignId: h.CustomerForeignID,
		InvoiceAddress: gen.OrdersAddress{
			Line1: h.InvoiceAddressLine1, Line2: h.InvoiceAddressLine2, PostalCode: h.InvoicePostalCode,
			City: h.InvoiceCity, Country: h.InvoiceCountry,
		},
		DeliveryAddressId: h.DeliveryAddressID,
		DeliveryAddress: gen.OrdersAddress{
			Name: h.DeliveryName, Line1: h.DeliveryAddressLine1, Line2: h.DeliveryAddressLine2,
			PostalCode: h.DeliveryPostalCode, City: h.DeliveryCity, Country: h.DeliveryCountry,
		},
		ContactId: h.ContactID,
		Contact:   gen.OrdersContact{Name: h.ContactName, Email: h.ContactEmail, Phone: h.ContactPhone},
	}
	if h.CustomerType != nil {
		t := gen.OrdersCustomerSnapshotType(*h.CustomerType)
		out.Type = &t
	}
	return out
}

// personOut is a user snapshot as the API answers it, nil when there is none.
func personOut(id *uuid.UUID, display *string) *gen.OrdersPerson {
	if id == nil && display == nil {
		return nil
	}
	return &gen.OrdersPerson{UserId: id, DisplayName: deref(display)}
}

// dateOut is a date column as the API answers it.
func dateOut(d pgtype.Date) *openapi_types.Date {
	if !d.Valid {
		return nil
	}
	return &openapi_types.Date{Time: d.Time}
}
