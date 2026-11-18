package kid_test

import (
	"errors"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/invoices/kid"
)

// The customer KID's space (invoices phase 6 design I32): customer 1234 and
// invoice 1234 never share a KID, under either algorithm and at any length
// from 4 to 25 — every reserved invoice KID begins with 0 and every
// customer KID with 9.
func TestCustomerKID_NeverMeetsAnInvoiceKID(t *testing.T) {
	t.Parallel()
	for _, algorithm := range []string{kid.Mod10, kid.Mod11} {
		for length := 4; length <= 25; length++ {
			for _, n := range []int64{1, 9, 12, 99, 1234, 98765} {
				customer, cerr := kid.CustomerKID(n, length, algorithm)
				invoice, ierr := kid.ComputeReserved(n, length, algorithm)
				if cerr != nil || ierr != nil {
					if !errors.Is(cerr, kid.ErrLengthExceeded) || !errors.Is(ierr, kid.ErrLengthExceeded) {
						t.Errorf("%s length %d number %d: %v / %v, want both to fit or both to exceed", algorithm, length, n, cerr, ierr)
					}
					continue
				}
				if customer == invoice {
					t.Errorf("%s length %d number %d: both are %s", algorithm, length, n, customer)
				}
				if len(customer) != length || customer[0] != '9' || invoice[0] != '0' {
					t.Errorf("%s length %d number %d: customer %s, invoice %s", algorithm, length, n, customer, invoice)
				}
				if !kid.IsCustomerKID(customer) || kid.IsCustomerKID(invoice) {
					t.Errorf("IsCustomerKID(%s) or IsCustomerKID(%s) is wrong", customer, invoice)
				}
				if got, ok := kid.CustomerNumberOf(customer); !ok || got != n {
					t.Errorf("CustomerNumberOf(%s) = %d %v, want %d", customer, got, ok, n)
				}
				if !kid.Verify(invoice, algorithm, n) {
					t.Errorf("the reserved invoice KID %s does not verify as %d", invoice, n)
				}
			}
		}
	}
}

// The reserved cap: an invoice number with as many digits as length − 1
// still fits Compute, but not ComputeReserved or FitsReserved.
func TestComputeReserved_CapsOneDigitLower(t *testing.T) {
	t.Parallel()
	if _, err := kid.Compute(999, 4, kid.Mod10); err != nil {
		t.Fatalf("Compute(999, 4): %v", err)
	}
	if _, err := kid.ComputeReserved(999, 4, kid.Mod10); !errors.Is(err, kid.ErrLengthExceeded) {
		t.Errorf("ComputeReserved(999, 4): %v, want ErrLengthExceeded", err)
	}
	if fits, _ := kid.FitsReserved(99, 4); !fits {
		t.Errorf("FitsReserved(99, 4) = false")
	}
	if fits, _ := kid.FitsReserved(100, 4); fits {
		t.Errorf("FitsReserved(100, 4) = true")
	}
	if _, err := kid.CustomerKID(100, 4, kid.Mod10); !errors.Is(err, kid.ErrLengthExceeded) {
		t.Errorf("CustomerKID(100, 4): %v, want ErrLengthExceeded", err)
	}
}
