package manufacturing

// contracts.KitStructure (manufacturing phase 2 M145): a kit BoM's components
// as Orders copies them onto a kit line's child lines, exploded through
// nested kits. It is ungated — it carries no cost, no person and nothing a
// caller without manufacturing:access may not see, only which variants make
// up a kit and how many of each — and reads on the pool in one snapshot.

import (
	"context"
	"fmt"
	"math/big"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// newKitStructure builds contracts.KitStructure over d's pool.
func newKitStructure(d module.Deps) contracts.KitStructure { return kitStructure{deps: d} }

type kitStructure struct{ deps module.Deps }

// kitRead is a kit BoM as the explosion needs it: how much one BoM makes and
// its lines in their order.
type kitRead struct {
	output *big.Rat
	lines  []store.ManufacturingBomLine
}

// Kits answers, per kit variant among variantIDs, its default active kit
// BoM's components, a nested kit's exploded into its own, down to
// contracts.MaxKitLevels kits (M144 keeps every kit within it; a deeper one
// would be answered as a component).
func (k kitStructure) Kits(ctx context.Context, variantIDs []int32) (map[int32][]contracts.KitComponent, error) {
	out := map[int32][]contracts.KitComponent{}
	if len(variantIDs) == 0 {
		return out, nil
	}
	tx, err := k.deps.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("manufacturing: begin a read of the kits: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := store.New(tx)

	// The kits a level at a time: the variants asked, then their kits'
	// components, as deep as a kit may nest.
	kits := map[int32]kitRead{}
	frontier := dedupe(variantIDs)
	for level := 1; level <= contracts.MaxKitLevels && len(frontier) > 0; level++ {
		defaults, err := q.DefaultActiveBomsOfVariants(ctx, frontier)
		if err != nil {
			return nil, fmt.Errorf("manufacturing: read the kits: %w", err)
		}
		var ids []int64
		variantOf := map[int64]int32{}
		for _, d := range defaults {
			if d.Kind != kindKit {
				continue
			}
			ids = append(ids, d.ID)
			variantOf[d.ID] = d.VariantID
			kits[d.VariantID] = kitRead{output: ratOrZero(d.OutputQuantity)}
		}
		if len(ids) == 0 {
			break
		}
		lines, err := q.LinesOfBoms(ctx, ids)
		if err != nil {
			return nil, fmt.Errorf("manufacturing: read the kits' lines: %w", err)
		}
		var next []int32
		for _, l := range lines {
			v := variantOf[l.BomID]
			kit := kits[v]
			kit.lines = append(kit.lines, l)
			kits[v] = kit
			if _, known := kits[l.VariantID]; !known {
				next = append(next, l.VariantID)
			}
		}
		frontier = dedupe(next)
	}

	for _, v := range dedupe(variantIDs) {
		kit, ok := kits[v]
		if !ok {
			continue
		}
		var order []int32
		per := map[int32]*big.Rat{}
		explodeKit(kits, kit, big.NewRat(1, 1), 1, func(variant int32, q *big.Rat) {
			if per[variant] == nil {
				order = append(order, variant)
				per[variant] = new(big.Rat)
			}
			per[variant].Add(per[variant], q)
		})
		components := make([]contracts.KitComponent, 0, len(order))
		for _, c := range order {
			if t := text(per[c], quantityPlaces); t != "0" {
				components = append(components, contracts.KitComponent{VariantID: c, PerKit: t})
			}
		}
		out[v] = components
	}
	return out, nil
}

// explodeKit calls add with each component of kit, per factor kits, at level:
// a line's quantity per the BoM's output, a component that is a kit of its
// own exploded one level down while the levels last.
func explodeKit(kits map[int32]kitRead, kit kitRead, factor *big.Rat, level int, add func(variant int32, q *big.Rat)) {
	if kit.output.Sign() <= 0 {
		return
	}
	for _, l := range kit.lines {
		q := new(big.Rat).Quo(ratOrZero(l.Quantity), kit.output)
		q.Mul(q, factor)
		if inner, ok := kits[l.VariantID]; ok && level < contracts.MaxKitLevels {
			explodeKit(kits, inner, q, level+1, add)
			continue
		}
		add(l.VariantID, q)
	}
}
