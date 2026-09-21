package customers

import "testing"

// This file white-box tests directory.go's resolveBillingProfile directly
// (the values_test.go/brreg_internal_test.go convention: package customers,
// not customers_test, since directory_test.go's own external-package tests
// only reach resolveBillingProfile indirectly through the database-backed
// contracts.CustomerDirectory.BillingProfile) — final review fix wave,
// finding M4: a pure table test for the fields resolveBillingProfile merely
// passes through, unlike InvoiceEmail/ReminderEmail/PeppolID, which it
// actually resolves and directory_test.go's own SQL-backed tests already
// cover.

// TestResolveBillingProfile_PassThroughFields pins every field
// resolveBillingProfile copies across unchanged: CustomerNumber (the id
// argument's twin) and the profile's own Currency, Language,
// InvoiceDelivery, ReminderDelivery, GLN and BuyerReference, each deref'd
// from the billingProfile's *string, "" when unset.
func TestResolveBillingProfile_PassThroughFields(t *testing.T) {
	profile := billingProfile{
		Currency: billingStrPtr("SEK"), Language: billingStrPtr("en"),
		InvoiceDelivery: billingStrPtr("paper"), ReminderDelivery: billingStrPtr("paper"),
		Gln: billingStrPtr("1234567890128"), BuyerReference: billingStrPtr("PO-9"),
	}
	got := resolveBillingProfile(42, 100042, "Acme AS", "business", false, nil, nil, profile, nil)

	if got.ID != 42 {
		t.Errorf("ID = %d, want 42", got.ID)
	}
	if got.CustomerNumber != 100042 {
		t.Errorf("CustomerNumber = %d, want 100042", got.CustomerNumber)
	}
	if got.Name != "Acme AS" {
		t.Errorf("Name = %q, want Acme AS", got.Name)
	}
	if got.Type != "business" {
		t.Errorf("Type = %q, want business", got.Type)
	}
	if got.Archived {
		t.Errorf("Archived = true, want false")
	}
	if got.Currency != "SEK" {
		t.Errorf("Currency = %q, want SEK", got.Currency)
	}
	if got.Language != "en" {
		t.Errorf("Language = %q, want en", got.Language)
	}
	if got.InvoiceDelivery != "paper" {
		t.Errorf("InvoiceDelivery = %q, want paper", got.InvoiceDelivery)
	}
	if got.ReminderDelivery != "paper" {
		t.Errorf("ReminderDelivery = %q, want paper", got.ReminderDelivery)
	}
	if got.GLN != "1234567890128" {
		t.Errorf("GLN = %q, want 1234567890128", got.GLN)
	}
	if got.BuyerReference != "PO-9" {
		t.Errorf("BuyerReference = %q, want PO-9", got.BuyerReference)
	}
}

// TestResolveBillingProfile_PassThroughFields_EmptyProfileIsAllZeroValues
// proves the deref side of the same pass-through: every one of those string
// fields is "" (never a literal "<nil>" or a panic) when the underlying
// billingProfile field is nil.
func TestResolveBillingProfile_PassThroughFields_EmptyProfileIsAllZeroValues(t *testing.T) {
	got := resolveBillingProfile(1, 1, "Empty AS", "business", false, nil, nil, billingProfile{}, nil)

	if got.Currency != "" || got.Language != "" || got.InvoiceDelivery != "" || got.ReminderDelivery != "" ||
		got.GLN != "" || got.BuyerReference != "" {
		t.Errorf("got = %+v, want every pass-through string field empty", got)
	}
}

// TestResolveBillingProfile_PaymentTermsDays_NilStaysNilValueStaysValue pins
// PaymentTermsDays specifically, since it is a *int32 rather than a *string:
// resolveBillingProfile carries the pointer through unchanged (Go's
// contracts.CustomerBillingProfile.PaymentTermsDays), never a deref'd 0.
func TestResolveBillingProfile_PaymentTermsDays_NilStaysNilValueStaysValue(t *testing.T) {
	got := resolveBillingProfile(1, 1, "Nil Terms AS", "business", false, nil, nil, billingProfile{}, nil)
	if got.PaymentTermsDays != nil {
		t.Errorf("PaymentTermsDays = %v, want nil", got.PaymentTermsDays)
	}

	terms := int32(45)
	got = resolveBillingProfile(1, 1, "Set Terms AS", "business", false, nil, nil, billingProfile{PaymentTermsDays: &terms}, nil)
	if got.PaymentTermsDays == nil || *got.PaymentTermsDays != 45 {
		t.Errorf("PaymentTermsDays = %v, want 45", got.PaymentTermsDays)
	}
}
