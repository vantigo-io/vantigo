package customers

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// TestImportRows_StopsBeforeTheRowAfterACancel: a request cancelled while a
// row runs — its caller gone — lets that row finish and runs no other. No
// database: importRows's apply is where each row's transaction opens, so a
// count of its calls is exactly the rows that ran.
func TestImportRows_StopsBeforeTheRowAfterACancel(t *testing.T) {
	t.Parallel()
	l := importLayout{index: map[string]int{"name": 0}, spelling: map[string]string{"name": "name"}, groups: map[csvGroup]bool{csvGroupRow: true}, width: 1}
	var file csvFile
	for i := 1; i <= 5; i++ {
		file.Rows = append(file.Rows, csvRecord{Row: i, Cells: []string{fmt.Sprintf("Kunde %d AS", i)}})
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ran := 0
	_, err := importRows(ctx, file, l, importVocabulary{}, false, func(importPlan) (importOutcome, error) {
		ran++
		if ran == 2 {
			cancel()
		}
		return importCreated, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if ran != 2 {
		t.Errorf("%d rows ran, want 2: the row that was running when the request was cancelled, and none after it", ran)
	}
}
