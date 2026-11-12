package inventory

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// countingExecer counts the statements run on it and keeps their arguments.
type countingExecer struct{ args [][]any }

func (c *countingExecer) Exec(_ context.Context, _ string, args ...any) (pgconn.CommandTag, error) {
	c.args = append(c.args, args)
	return pgconn.CommandTag{}, nil
}

// TestNotifyLots_OneStatement is a 500-serial receipt's notification (M1 of
// the L1b review): one statement, the list told once without an id and each
// lot's page once, the duplicates and the zero id dropped.
func TestNotifyLots_OneStatement(t *testing.T) {
	t.Parallel()
	ids := []int64{0}
	for i := range 500 {
		ids = append(ids, int64(500-i))
	}
	ids = append(ids, 7, 7)
	var q countingExecer
	if err := notifyLots(context.Background(), &q, ids); err != nil {
		t.Fatal(err)
	}
	if len(q.args) != 1 {
		t.Fatalf("statements = %d, want 1", len(q.args))
	}
	bodies, _ := q.args[0][1].([]string)
	if len(bodies) != 501 || bodies[0] != `{"t":"inventory.lots","k":"lot.changed"}` ||
		bodies[1] != `{"t":"inventory.lot:1","k":"lot.changed","id":"1"}` || bodies[500] != `{"t":"inventory.lot:500","k":"lot.changed","id":"500"}` {
		t.Errorf("events = %d: %v … %v", len(bodies), bodies[:min(2, len(bodies))], bodies[max(0, len(bodies)-1):])
	}
	q = countingExecer{}
	if err := notifyLots(context.Background(), &q, []int64{0}); err != nil || len(q.args) != 0 {
		t.Errorf("no lots: %v, %d statements", err, len(q.args))
	}
}
