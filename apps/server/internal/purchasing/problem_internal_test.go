package purchasing

import (
	"context"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/openapi"
)

// PurchasingProblem's description names every refusal code the module
// answers, so a client reading the contract knows each one.
func TestProblemDescription_NamesEveryCode(t *testing.T) {
	doc, err := openapi.Load(context.Background(), moduleName)
	if err != nil {
		t.Fatal(err)
	}
	desc := doc.Components.Schemas["PurchasingProblem"].Value.Description
	for _, code := range refusalCodes {
		if !strings.Contains(desc, code) {
			t.Errorf("PurchasingProblem does not name %s", code)
		}
	}
}

// The decimals are written as the contract says: quantities trimmed, prices
// with two to four places, a line's net rounded to two.
func TestTrimmedAndPrices(t *testing.T) {
	for in, want := range map[string]string{"12.000": "12", "2.500": "2.5", "0": "0"} {
		if got := trimmed(mustRat(in), 3); got != want {
			t.Errorf("trimmed(%s) = %s, want %s", in, got, want)
		}
	}
	for in, want := range map[string]string{"12": "12.00", "12.5": "12.50", "0.0125": "0.0125"} {
		if got := priceText(mustRat(in)); got != want {
			t.Errorf("priceText(%s) = %s, want %s", in, got, want)
		}
	}
	if got := lineNet(mustRat("3"), mustRat("10.005"), mustRat("10")).FloatString(2); got != "27.01" {
		t.Errorf("lineNet = %s, want 27.01", got)
	}
}
