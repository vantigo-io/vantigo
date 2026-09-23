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
	got := resolveBillingProfile(42, 100042, "Acme AS", "business", false, nil, nil, profile, nil, nil)

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
	got := resolveBillingProfile(1, 1, "Empty AS", "business", false, nil, nil, billingProfile{}, nil, nil)

	if got.Currency != "" || got.Language != "" || got.InvoiceDelivery != "" || got.ReminderDelivery != "" ||
		got.GLN != "" || got.BuyerReference != "" {
		t.Errorf("got = %+v, want every pass-through string field empty", got)
	}
}

// TestResolveBillingProfile_PaymentTermsDays_NilStaysNilValueStaysValue pins
// PaymentTermsDays specifically, since it is a *int32 rather than a *string:
// with no group default to fall back on, resolveBillingProfile carries the
// pointer through unchanged (Go's contracts.CustomerBillingProfile.
// PaymentTermsDays), never a deref'd 0 — the group tier has its own test below.
func TestResolveBillingProfile_PaymentTermsDays_NilStaysNilValueStaysValue(t *testing.T) {
	got := resolveBillingProfile(1, 1, "Nil Terms AS", "business", false, nil, nil, billingProfile{}, nil, nil)
	if got.PaymentTermsDays != nil {
		t.Errorf("PaymentTermsDays = %v, want nil", got.PaymentTermsDays)
	}

	terms := int32(45)
	got = resolveBillingProfile(1, 1, "Set Terms AS", "business", false, nil, nil, billingProfile{PaymentTermsDays: &terms}, nil, nil)
	if got.PaymentTermsDays == nil || *got.PaymentTermsDays != 45 {
		t.Errorf("PaymentTermsDays = %v, want 45", got.PaymentTermsDays)
	}
}

// TestResolveBillingProfile_PaymentTermsDays_GroupDefaultIsTheThirdTier pins the
// one field in this module with three resolution levels (customer groups design
// D4): the profile's own value wins, the group's default fills an unset one, and
// with neither the answer is still nil — "not decided here, whoever invoices
// uses its own default", exactly what every consumer already reads nil as.
func TestResolveBillingProfile_PaymentTermsDays_GroupDefaultIsTheThirdTier(t *testing.T) {
	own := int32(14)
	groupDefault := int32(30)

	// Own wins. A customer that negotiated 14 days is not moved to 30 by the
	// group it happens to be in — the group carries a DEFAULT, not a policy.
	got := resolveBillingProfile(1, 1, "Own Terms AS", "business", false, nil, nil,
		billingProfile{PaymentTermsDays: &own}, &groupDefault, nil)
	if got.PaymentTermsDays == nil || *got.PaymentTermsDays != 14 {
		t.Errorf("PaymentTermsDays = %v, want 14: the profile's own value wins", got.PaymentTermsDays)
	}

	// The group fills an unset one.
	got = resolveBillingProfile(1, 1, "Inherits AS", "business", false, nil, nil, billingProfile{}, &groupDefault, nil)
	if got.PaymentTermsDays == nil || *got.PaymentTermsDays != 30 {
		t.Errorf("PaymentTermsDays = %v, want 30: the group's default fills it", got.PaymentTermsDays)
	}

	// Neither: a group with no default of its own and no group at all are the
	// same argument here (both nil) and must be the same answer — nobody
	// decided, which is what every consumer already reads nil as.
	got = resolveBillingProfile(1, 1, "Neither AS", "business", false, nil, nil, billingProfile{}, nil, nil)
	if got.PaymentTermsDays != nil {
		t.Errorf("PaymentTermsDays = %v, want nil", got.PaymentTermsDays)
	}

	// 0 is a decision ("due on receipt"), not an absence — the case a
	// resolution written with a zero check instead of a nil check gets wrong,
	// in both tiers.
	zero := int32(0)
	got = resolveBillingProfile(1, 1, "Receipt AS", "business", false, nil, nil,
		billingProfile{PaymentTermsDays: &zero}, &groupDefault, nil)
	if got.PaymentTermsDays == nil || *got.PaymentTermsDays != 0 {
		t.Errorf("PaymentTermsDays = %v, want 0: the profile decided 0 and the group must not override it", got.PaymentTermsDays)
	}
	got = resolveBillingProfile(1, 1, "Receipt Group AS", "business", false, nil, nil, billingProfile{}, &zero, nil)
	if got.PaymentTermsDays == nil || *got.PaymentTermsDays != 0 {
		t.Errorf("PaymentTermsDays = %v, want 0: the group decided 0", got.PaymentTermsDays)
	}
}
