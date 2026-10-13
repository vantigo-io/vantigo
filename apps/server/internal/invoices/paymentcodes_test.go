package invoices_test

import (
	"os"
	"regexp"
	"slices"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/invoices"
)

// TestPaymentCodes_TheContractListsThem: every payments-port code is a
// distinct snake_case word that InvoicesConflictProblem's description names
// (plan reading 9), so a client reading the contract learns each one before
// any task answers it.
func TestPaymentCodes_TheContractListsThem(t *testing.T) {
	t.Parallel()
	contract, err := os.ReadFile("../../../../openapi/invoices.yaml")
	if err != nil {
		t.Fatalf("read the contract: %v", err)
	}
	description := regexp.MustCompile(`(?m)^\s*InvoicesConflictProblem:\s*\n\s*description: '(.*)'$`).FindSubmatch(contract)
	if description == nil {
		t.Fatal("the contract has no InvoicesConflictProblem description")
	}
	named := regexp.MustCompile(`\b[a-z]+(?:_[a-z]+)+\b`).FindAllString(string(description[1]), -1)
	if len(invoices.PaymentCodes) != 15 {
		t.Errorf("payment codes = %d, want the fifteen of 4C", len(invoices.PaymentCodes))
	}
	seen := map[string]bool{}
	for _, code := range invoices.PaymentCodes {
		if seen[code] {
			t.Errorf("%s is declared twice", code)
		}
		seen[code] = true
		if !regexp.MustCompile(`^[a-z]+(_[a-z]+)+$`).MatchString(code) {
			t.Errorf("%q is not a snake_case code", code)
		}
		if !slices.Contains(named, code) {
			t.Errorf("InvoicesConflictProblem's description does not name %s", code)
		}
	}
}
