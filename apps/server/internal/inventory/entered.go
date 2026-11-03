package inventory

import (
	"encoding/json"
	"fmt"
	"math/big"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
)

// Entering in packagings (design I25). Every quantity input — an adjustment's,
// a move's, a receipt's and a count's line — carries exactly one of quantity
// and entered: {packagings: [{packagingId, count}], loose}. Both, neither, a
// packaging named twice or a count below 1 is entered_invalid. The entry is
// judged for its shape when the request is read, and resolved against the
// variant's packagings as ProductCatalog.LockVariants answers them, inside the
// transaction that writes it: the base quantity is Σ count × the packaging's
// quantity + loose, exact, and a packaging the variant does not have is
// packaging_not_found. What was entered is kept as a snapshot — each
// packaging's name, count and content then, and the loose rest — so history
// reads "3 Euro pallet + 2 Box" after the packagings change. Stock itself is
// always in the variant's own unit (I24).

// maxEnteredPackagings is the most packagings one entry names: a variant has
// at most ten (I24).
const maxEnteredPackagings = 10

// entry is a quantity entered in packagings, its shape judged, not yet
// resolved against the variant.
type entry struct {
	packagings []gen.InventoryEnteredPackaging
	loose      *big.Rat
}

// enteredSnapshot is an entry as it was resolved, stored on the line as JSON
// with its quantities as exact decimal text.
type enteredSnapshot struct {
	Packagings []snapshotPackaging `json:"packagings"`
	Loose      *string             `json:"loose,omitempty"`
}

type snapshotPackaging struct {
	PackagingID int32  `json:"packagingId"`
	Name        string `json:"name"`
	Count       int32  `json:"count"`
	Quantity    string `json:"quantity"`
}

// readEntered judges a line's quantity and entered together: exactly one is
// given. A quantity is parsed under rule and answered; an entry is answered
// unresolved. code and detail say why the line is refused.
func readEntered(quantity *float64, entered *gen.InventoryEntered, rule quantityRule) (*big.Rat, *entry, string, string) {
	switch {
	case quantity != nil && entered != nil:
		return nil, nil, codeEnteredInvalid, "A line gives either a quantity or what was entered in packagings, not both."
	case quantity == nil && entered == nil:
		return nil, nil, codeEnteredInvalid, "A line gives a quantity, or what was entered in packagings."
	case quantity != nil:
		q, msg := parseQuantity(*quantity, rule)
		if msg != "" {
			return nil, nil, codeQuantityInvalid, msg
		}
		return q, nil, "", ""
	}
	if len(entered.Packagings) > maxEnteredPackagings {
		return nil, nil, codeEnteredInvalid, fmt.Sprintf("An entry names at most %d packagings.", maxEnteredPackagings)
	}
	seen := map[int32]bool{}
	for _, p := range entered.Packagings {
		if p.Count < 1 {
			return nil, nil, codeEnteredInvalid, fmt.Sprintf("The count of packaging %d must be 1 or more.", p.PackagingId)
		}
		if seen[p.PackagingId] {
			return nil, nil, codeEnteredInvalid, fmt.Sprintf("The packaging %d is named twice; give it once with its count.", p.PackagingId)
		}
		seen[p.PackagingId] = true
	}
	e := &entry{packagings: entered.Packagings}
	if entered.Loose != nil {
		loose, msg := parseQuantity(*entered.Loose, nonNegativeQuantity)
		if msg != "" {
			return nil, nil, codeQuantityInvalid, "The loose rest: " + msg
		}
		e.loose = loose
	}
	return nil, e, "", ""
}

// resolve is the entry's base quantity in v's unit, against v's packagings
// as they are now, and the snapshot to keep; code and detail say why it does
// not resolve.
func (e *entry) resolve(v contracts.VariantEntry) (*big.Rat, []byte, string, string) {
	total := new(big.Rat)
	snap := enteredSnapshot{Packagings: make([]snapshotPackaging, 0, len(e.packagings))}
	for _, p := range e.packagings {
		var found *contracts.PackagingEntry
		for i := range v.Packagings {
			if v.Packagings[i].ID == p.PackagingId {
				found = &v.Packagings[i]
				break
			}
		}
		if found == nil {
			return nil, nil, codePackagingNotFound, fmt.Sprintf("%s has no packaging %d.", v.SKU, p.PackagingId)
		}
		content := ratFromText(found.Quantity)
		total.Add(total, new(big.Rat).Mul(content, new(big.Rat).SetInt64(int64(p.Count))))
		snap.Packagings = append(snap.Packagings, snapshotPackaging{
			PackagingID: found.ID, Name: found.Name, Count: p.Count, Quantity: decimalText(content),
		})
	}
	if e.loose != nil {
		total.Add(total, e.loose)
		snap.Loose = ptr(decimalText(e.loose))
	}
	if total.Cmp(maxQuantity) > 0 {
		return nil, nil, codeQuantityInvalid, "The entry comes to more than 99,999,999,999.999"
	}
	if !threePlaces(total) {
		return nil, nil, codeQuantityInvalid, "The entry comes to a quantity with more than three decimals"
	}
	raw, err := json.Marshal(snap)
	if err != nil {
		return nil, nil, codeEnteredInvalid, "The entry could not be kept"
	}
	return total, raw, "", ""
}

// threePlaces reports whether v has at most three decimals: its denominator
// divides 1000. A packaging's quantity has three places, a count is whole and
// loose is parsed to three places, so a sum always has; this holds the rule
// whatever the catalog answers.
func threePlaces(v *big.Rat) bool {
	return new(big.Int).Mod(big.NewInt(1000), v.Denom()).Sign() == 0
}

// snapshotOf is a stored entry as the contract answers it, nil for none or
// one that does not read.
func snapshotOf(raw []byte) *gen.InventoryEnteredSnapshot {
	if len(raw) == 0 {
		return nil
	}
	var snap enteredSnapshot
	if json.Unmarshal(raw, &snap) != nil {
		return nil
	}
	out := &gen.InventoryEnteredSnapshot{}
	out.Packagings = make([]struct {
		Count       int32   `json:"count"`
		Name        string  `json:"name"`
		PackagingId int32   `json:"packagingId"`
		Quantity    float64 `json:"quantity"`
	}, 0, len(snap.Packagings))
	for _, p := range snap.Packagings {
		out.Packagings = append(out.Packagings, struct {
			Count       int32   `json:"count"`
			Name        string  `json:"name"`
			PackagingId int32   `json:"packagingId"`
			Quantity    float64 `json:"quantity"`
		}{Count: p.Count, Name: p.Name, PackagingId: p.PackagingID, Quantity: wireQuantity(ratFromText(p.Quantity))})
	}
	if snap.Loose != nil {
		out.Loose = ptr(wireQuantity(ratFromText(*snap.Loose)))
	}
	return out
}

// packagingsOf is a variant's packagings as the contract answers them, by
// the catalog's order.
func packagingsOf(v contracts.VariantEntry) *[]gen.InventoryPackaging {
	out := make([]gen.InventoryPackaging, 0, len(v.Packagings))
	for _, p := range v.Packagings {
		out = append(out, gen.InventoryPackaging{
			Id: p.ID, Name: p.Name, Kind: p.Kind, Quantity: wireQuantity(ratFromText(p.Quantity)), Barcode: p.Barcode,
		})
	}
	return &out
}
