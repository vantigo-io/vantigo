package meta_test

import (
	"context"
	"testing"
	"time"
)

const inbDay = 24 * time.Hour

func (f *inbFixture) retain(t *testing.T) {
	t.Helper()
	if err := f.ret.RunOnce(context.Background()); err != nil {
		t.Fatalf("retention: %v", err)
	}
}

func TestRetention_AcknowledgedRowsAndTheirFilesGoAfterSevenDays(t *testing.T) {
	f := newInbFixture(t)
	f.post(t, inbEnvelope(inbPage, inbText("m.r1", "keep a week", map[string]any{"attachments": inbImage(inbImageURL)})))
	f.cycle(t)
	events := f.pending(t, inbPage, 0)
	if err := f.m.Ack(context.Background(), inbPage, events[0].Cursor); err != nil {
		t.Fatal(err)
	}

	f.h.Advance(6 * inbDay)
	f.retain(t)
	if n := f.h.Count(t, `SELECT count(*) FROM meta.inbound`); n != 1 || len(f.files.keys()) != 1 {
		t.Fatalf("after 6 days: %d rows, %v", n, f.files.keys())
	}
	f.h.Advance(inbDay + time.Hour)
	f.retain(t)
	if n := f.h.Count(t, `SELECT count(*) FROM meta.inbound`); n != 0 || len(f.files.keys()) != 0 {
		t.Fatalf("after 7 days: %d rows, %v", n, f.files.keys())
	}
}

func TestRetention_UnacknowledgedRowsGoAfterThirtyDays(t *testing.T) {
	f := newInbFixture(t)
	f.post(t, inbEnvelope(inbPage, inbText("m.r2", "never read", map[string]any{"attachments": inbImage(inbImageURL)})))
	f.cycle(t)

	f.h.Advance(29 * inbDay)
	f.retain(t)
	if n := f.h.Count(t, `SELECT count(*) FROM meta.inbound`); n != 1 {
		t.Fatalf("after 29 days: %d rows", n)
	}
	f.h.Advance(2 * inbDay)
	f.retain(t)
	if n := f.h.Count(t, `SELECT count(*) FROM meta.inbound`); n != 0 || len(f.files.keys()) != 0 {
		t.Fatalf("after 31 days: %d rows, %v", n, f.files.keys())
	}
}

func TestRetention_RawEventsAndProfiles(t *testing.T) {
	f := newInbFixture(t)
	f.post(t, inbEnvelope(inbPage, inbText("m.r3", "raw", nil)))
	f.cycle(t)
	f.h.Exec(t, `INSERT INTO meta.profiles (page_id, psid, display_name, fetched_at) VALUES ($1, 'a', 'Kari', $2), ($1, 'b', 'Ola', $3)`,
		inbPage, f.h.Now().Add(-31*inbDay), f.h.Now().Add(-29*inbDay))

	f.retain(t)
	if n := f.h.Count(t, `SELECT count(*) FROM meta.profiles`); n != 1 {
		t.Errorf("%d profiles after the pass, want the 29 day old one only", n)
	}
	if n := f.h.Count(t, `SELECT count(*) FROM meta.webhook_events`); n != 1 {
		t.Errorf("a fresh raw event was deleted")
	}
	f.h.Advance(8 * inbDay)
	f.retain(t)
	if n := f.h.Count(t, `SELECT count(*) FROM meta.webhook_events`); n != 0 {
		t.Errorf("%d raw events after 8 days", n)
	}
	if n := f.h.Count(t, `SELECT count(*) FROM meta.profiles`); n != 0 {
		t.Errorf("%d profiles after 37 days", n)
	}
}
