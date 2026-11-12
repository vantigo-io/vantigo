package manufacturing

import (
	"errors"
	"math/big"
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/store"
)

// TestWriteLineLots_UnreadLot is M82's numbers: a lot taken that the post
// read nothing of is errLotsMoved on every attempt, the last too, so a row
// is never written without its number; runProduction answers that as 409.
func TestWriteLineLots_UnreadLot(t *testing.T) {
	t.Parallel()
	lines := []productionLine{{componentID: 1, consumed: big.NewRat(1, 1), scrap: new(big.Rat)}}
	refs := []consumeRef{{componentID: 1, row: -1, auto: true}}
	posted := contracts.StockPosted{Picks: []contracts.StockPick{{Index: 0, LotID: 9, LocationID: 3, Quantity: "1"}}}
	if _, err := writeLineLots(t.Context(), nil, 1, lines, refs, posted, map[int64]lotInfo{}); !errors.Is(err, errLotsMoved) {
		t.Errorf("an unread lot = %v, want errLotsMoved", err)
	}
	if status, p, ok := answerOf(lotsMovedConflict()); !ok || status != http.StatusConflict || p.Code == nil || *p.Code != codeRevisionConflict {
		t.Errorf("the last attempt's answer = %d %+v", status, p)
	}
}

// TestSplitConsumption_RowsShortOfLine is M82's sum judged again at the
// split: a line whose lot rows add up to less than it consumes and scraps —
// a re-proposal's — is insufficient_stock, never posted for less.
func TestSplitConsumption_RowsShortOfLine(t *testing.T) {
	t.Parallel()
	c := store.ManufacturingOrderComponent{ID: 7, VariantID: 3, Sku: "BERRY-1"}
	lines := []productionLine{{componentID: 7, consumed: big.NewRat(2, 1), scrap: new(big.Rat),
		lots: []lotRow{{lotID: 1, locationID: 4, consumed: big.NewRat(1, 1), scrapped: new(big.Rat)}}}}
	_, _, err := splitConsumption(store.ManufacturingOrder{}, map[int64]store.ManufacturingOrderComponent{7: c}, lines, nil, map[int32]bool{3: true})
	status, p, ok := answerOf(err)
	if !ok || status != http.StatusConflict || p.Code == nil || *p.Code != contracts.StockInsufficientStock {
		t.Errorf("rows short of the line = %d %+v (%v)", status, p, err)
	}
}
