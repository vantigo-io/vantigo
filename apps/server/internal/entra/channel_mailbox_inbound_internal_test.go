package entra

import (
	"testing"
	"time"
)

// TestMailItemOf: a message whose receivedDateTime does not read is kept
// with a zero ReceivedAt — Communications receives it at the moment it reads
// it — and only an id that is no validID leaves an item out.
func TestMailItemOf(t *testing.T) {
	at := time.Date(2026, 10, 29, 8, 0, 0, 0, time.UTC)
	for name, c := range map[string]struct {
		in   inboxDeltaItem
		ok   bool
		zero bool
	}{
		"readable":            {in: inboxDeltaItem{ID: "AAMk1", ReceivedDateTime: at.Format(time.RFC3339)}, ok: true},
		"an unreadable time":  {in: inboxDeltaItem{ID: "AAMk2", ReceivedDateTime: "yesterday"}, ok: true, zero: true},
		"no time":             {in: inboxDeltaItem{ID: "AAMk3"}, ok: true, zero: true},
		"an id that is no id": {in: inboxDeltaItem{ID: "..", ReceivedDateTime: at.Format(time.RFC3339)}},
		"no id":               {in: inboxDeltaItem{ReceivedDateTime: at.Format(time.RFC3339)}},
	} {
		t.Run(name, func(t *testing.T) {
			item, ok := mailItemOf(c.in)
			if ok != c.ok {
				t.Fatalf("mailItemOf(%+v) ok = %v, want %v", c.in, ok, c.ok)
			}
			if ok && item.ReceivedAt.IsZero() != c.zero {
				t.Errorf("ReceivedAt = %v, want zero %v", item.ReceivedAt, c.zero)
			}
			if ok && !c.zero && !item.ReceivedAt.Equal(at) {
				t.Errorf("ReceivedAt = %v, want %v", item.ReceivedAt, at)
			}
		})
	}
}
