package contracts

import "context"

// MaxKitLevels is how many kits deep a kit nests at most (manufacturing
// phase 2 M144): a kit is level 1, a kit among its components level 2.
const MaxKitLevels = 3

// KitStructure answers, per kit variant, its default active kit BoM's
// components exploded through nested kits (at most MaxKitLevels levels): the
// variant and the quantity per kit (manufacturing phase 2 M145). Orders copies
// it onto a kit line's child lines when the line is added. It is a
// single-provider slot, provided by Manufacturing and nil when Manufacturing
// is off — a kit variant is then an ordinary goods line.
//
// It carries no cost or personal data: it is ungated, and the consumer
// decides what to show. The rules a provider keeps:
//
//   - It reads on the pool, in one snapshot per call; the consumer calls it
//     before its transaction, never inside one.
//   - It answers only the kit variants among variantIDs: a variant with no
//     default active kit BoM is absent, never an error.
//   - A component that is itself a kit (its default active BoM is a kit) is
//     exploded into its own components, its quantity multiplied through; one
//     variant reached twice is answered once, the quantities summed, in the
//     order first reached.
//   - PerKit is exact decimal text, more than 0, at most three decimals,
//     half-up; a component whose quantity rounds to 0 is left out.
type KitStructure interface {
	Kits(ctx context.Context, variantIDs []int32) (map[int32][]KitComponent, error)
}

// KitComponent is one component of a kit as KitStructure answers it.
type KitComponent struct {
	VariantID int32
	PerKit    string // exact decimal text, > 0
}
