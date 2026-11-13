package manufacturing

import (
	"math/big"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/manufacturing/store"
)

func r(s string) *big.Rat { return mustRat(s) }

// TestJudgeSuggestion is M107's arithmetic with each term and M108's rule:
// low at or below the reorder point, the target the maximum or one above
// the reorder point, the quantity rounded up to the multiple, make with a
// default BoM and buy without, and the need-by today or after the lead time.
func TestJudgeSuggestion(t *testing.T) {
	// Friday 2026-11-13; three business days on is Wednesday the 18th.
	today := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	bom := &store.ManufacturingBom{ID: 7, LeadTimeDays: 3}
	base := func() projection {
		return projection{onHand: r("30"), reserved: r("5"), incoming: r("2"), inProduction: r("3"),
			neededByProduction: r("10"), neededBySales: r("4"), reorderPoint: r("20")}
	}
	cases := []struct {
		name      string
		edit      func(*projection)
		bom       *store.ManufacturingBom
		low       bool
		projected string
		quantity  string
		kind      string
		needBy    time.Time
	}{
		// 30 − 5 + 2 + 3 − 10 − 4 = 16 ≤ 20: target 21, needed 5.
		{name: "every term", bom: bom, low: true, projected: "16", quantity: "5", kind: kindMake,
			needBy: time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)},
		{name: "a maximum is the target", edit: func(p *projection) { p.maximum = r("40") }, bom: bom, low: true,
			projected: "16", quantity: "24", kind: kindMake, needBy: time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)},
		{name: "rounded up to the multiple", edit: func(p *projection) { p.maximum = r("40"); p.multiple = r("10") }, bom: bom,
			low: true, projected: "16", quantity: "30", kind: kindMake, needBy: time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)},
		{name: "a fractional multiple", edit: func(p *projection) { p.multiple = r("2.5") }, low: true, projected: "16",
			quantity: "5", kind: kindBuy, needBy: today},
		{name: "already short today", edit: func(p *projection) { p.reserved = r("15") }, bom: bom, low: true,
			projected: "6", quantity: "15", kind: kindMake, needBy: today},
		{name: "more supply is not low", edit: func(p *projection) { p.inProduction = r("8") }, bom: bom},
		{name: "at least 1", edit: func(p *projection) { p.maximum = r("16"); p.reorderPoint = r("16") }, low: true,
			projected: "16", quantity: "1", kind: kindBuy, needBy: today},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := base()
			if tc.edit != nil {
				tc.edit(&p)
			}
			plan := judgeSuggestion(101, p, tc.bom, today)
			if (plan != nil) != tc.low {
				t.Fatalf("low = %v, want %v", plan != nil, tc.low)
			}
			if plan == nil {
				return
			}
			if plan.reason.Projected != tc.projected || text(plan.quantity, quantityPlaces) != tc.quantity ||
				plan.kind != tc.kind || !plan.needBy.Equal(tc.needBy) {
				t.Errorf("plan = projected %s, quantity %s, %s, need by %s", plan.reason.Projected,
					text(plan.quantity, quantityPlaces), plan.kind, plan.needBy.Format(time.DateOnly))
			}
		})
	}
}
