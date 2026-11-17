package purchasing

import (
	"math/big"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// A supplier item's price is per its own packaging: a line in another one
// takes it converted, and a factor that is not known leaves the line at 0.
func TestItemPriceFor(t *testing.T) {
	t.Parallel()
	box, pallet, broken, gone := int32(7), int32(8), int32(9), int32(99)
	v := contracts.VariantEntry{ID: 1, SKU: "BOLT", Packagings: []contracts.PackagingEntry{
		{ID: box, Name: "Eske", Quantity: "10.000"},
		{ID: pallet, Name: "Pall", Quantity: "480"},
		{ID: broken, Name: "Tom", Quantity: "0"},
	}}
	cases := []struct {
		name       string
		price      string
		item, line *int32
		want       string // "" is no price: the line stays at 0
	}{
		{"same packaging", "12.5", &box, &box, "12.5"},
		{"both in the unit", "1.25", nil, nil, "1.25"},
		{"item per box, line per unit", "12.5", &box, nil, "1.25"},
		{"item per unit, line per box", "1.2345", nil, &box, "12.345"},
		{"item per box, line per pallet", "12.5", &box, &pallet, "600"},
		{"a third of a box", "1", &box, nil, "0.1"},
		{"thirds round", "10", &pallet, nil, "0.0208"},
		{"the item's packaging is gone", "12.5", &gone, nil, ""},
		{"the line's packaging is gone", "12.5", nil, &gone, ""},
		{"a packaging holding nothing", "12.5", &broken, nil, ""},
		{"no price", "", &box, nil, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			var price *big.Rat
			if c.price != "" {
				price = mustRat(c.price)
			}
			got := itemPriceFor(v, price, c.item, c.line)
			switch {
			case c.want == "" && got != nil:
				t.Fatalf("itemPriceFor = %s, want none", got.FloatString(4))
			case c.want != "" && (got == nil || got.Cmp(mustRat(c.want)) != 0):
				t.Fatalf("itemPriceFor = %v, want %s", got, c.want)
			}
		})
	}
}

// An import's price takes the Norwegian forms where they read one way only.
func TestImportDecimal(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"12.50":        "12.50",
		"12,50":        "12.50",
		"1 234,50":     "1234.50",
		"1 234,50":     "1234.50",
		"1 234":        "1234",
		"12 345 678,5": "12345678.5",
		"1.234,50":     "1234.50",
		"1.234.567,25": "1234567.25",
		"1.234":        "1.234",
		"1 23,5":       "1 23.5",
		"1,234.50":     "1,234.50",
		" 99 ":         "99",
	} {
		if got := importDecimal(in); got != want {
			t.Errorf("importDecimal(%q) = %q, want %q", in, got, want)
		}
	}
}
