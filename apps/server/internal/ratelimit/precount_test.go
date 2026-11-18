package ratelimit

import (
	"context"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

var apiPolicy = Policy{Name: "api", Limit: 5, Window: time.Minute}

func mustCount(t *testing.T, l *Limiter, p Policy, client string) Decision {
	t.Helper()
	d, err := l.Count(context.Background(), p, client)
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	return d
}

// sharedHits is the shared row's count for client under p.
func sharedHits(t *testing.T, l *Limiter, p Policy, client string) int {
	t.Helper()
	var hits int
	if err := l.pool.QueryRow(context.Background(), `SELECT hits FROM platform.rate_limit WHERE key = $1`, p.Name+":"+client).Scan(&hits); err != nil {
		t.Fatal(err)
	}
	return hits
}

// waitFlushed waits for a flush Count started in the background.
func waitFlushed(t *testing.T, l *Limiter, p Policy, client string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		l.mu.Lock()
		st := l.counts[p.Name+":"+client]
		busy := st != nil && st.flushing
		l.mu.Unlock()
		if !busy {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the flush did not finish")
}

func TestCount_LimitsWithinAWindowWithTheHeadersValues(t *testing.T) {
	l, _ := newLimiter(t)
	for i := range 5 {
		d := mustCount(t, l, apiPolicy, "integration-a")
		if !d.Allowed || d.Remaining != 4-i {
			t.Fatalf("hit %d: %+v, want allowed with %d remaining", i+1, d, 4-i)
		}
		if want := time.Date(2026, 9, 10, 12, 1, 0, 0, time.UTC); !d.Reset.Equal(want) {
			t.Fatalf("Reset = %v, want %v", d.Reset, want)
		}
	}
	d := mustCount(t, l, apiPolicy, "integration-a")
	if d.Allowed || d.Remaining != 0 || d.RetryAfter != 50*time.Second {
		t.Errorf("sixth hit: %+v, want refused, 0 remaining, retry after 50s", d)
	}
	if !mustCount(t, l, apiPolicy, "integration-b").Allowed {
		t.Error("another client was refused: clients count separately")
	}
}

// Only the window's first hit waits on the shared row; the rest reach it in
// the background once a second, and the row is what replicas agree on.
func TestCount_FlushesIntoTheSharedRowOnceASecond(t *testing.T) {
	l, now := newLimiter(t)
	p := Policy{Name: "api", Limit: 100, Window: time.Minute}
	for range 3 {
		mustCount(t, l, p, "c")
	}
	if got := sharedHits(t, l, p, "c"); got != 1 {
		t.Fatalf("shared row = %d before a second passed, want 1 (only the seed)", got)
	}
	*now = now.Add(time.Second)
	mustCount(t, l, p, "c") // the fourth hit starts the flush
	waitFlushed(t, l, p, "c")
	if got := sharedHits(t, l, p, "c"); got != 4 {
		t.Fatalf("shared row = %d after the flush, want 4", got)
	}

	// Another replica's hits reach this one at its next flush.
	other := NewWithClock(l.pool, l.now)
	for range 10 {
		mustCount(t, other, p, "c")
	}
	*now = now.Add(time.Second)
	mustCount(t, l, p, "c")
	waitFlushed(t, l, p, "c")
	if d := mustCount(t, l, p, "c"); d.Remaining != 100-7 {
		t.Errorf("Remaining = %d, want %d: the other replica's seed hit, its nine still in process, and this one's six", d.Remaining, 100-7)
	}
}

func TestCount_ANewWindowStartsAfresh(t *testing.T) {
	l, now := newLimiter(t)
	for range 6 {
		mustCount(t, l, apiPolicy, "c")
	}
	*now = now.Add(time.Minute)
	if d := mustCount(t, l, apiPolicy, "c"); !d.Allowed || d.Remaining != 4 {
		t.Errorf("first hit of the next window: %+v, want allowed with 4 remaining", d)
	}
}

func TestCount_IsExactUnderConcurrencyInOneProcess(t *testing.T) {
	l, _ := newLimiter(t)
	p := Policy{Name: "api", Limit: 50, Window: time.Minute}
	var wg sync.WaitGroup
	var mu sync.Mutex
	allowed := 0
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 10 {
				d, err := l.Count(context.Background(), p, "c")
				if err != nil {
					t.Error(err)
					return
				}
				if d.Allowed {
					mu.Lock()
					allowed++
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()
	if allowed != 50 {
		t.Errorf("%d of 80 allowed, want exactly 50", allowed)
	}
}

func TestHitAndBlocked_AnswerRemainingAndReset(t *testing.T) {
	l, _ := newLimiter(t)
	reset := time.Date(2026, 9, 10, 12, 1, 0, 0, time.UTC)
	if d, _ := l.Blocked(context.Background(), login, "x"); !d.Allowed || d.Remaining != 2 || !d.Reset.Equal(reset) {
		t.Errorf("Blocked on a fresh key: %+v", d)
	}
	if d := mustHit(t, l, login, "x"); d.Remaining != 1 || !d.Reset.Equal(reset) {
		t.Errorf("first hit: %+v, want 1 remaining, reset %v", d, reset)
	}
	if d, _ := l.Blocked(context.Background(), login, "x"); !d.Allowed || d.Remaining != 1 {
		t.Errorf("Blocked after one hit: %+v, want allowed with 1 remaining", d)
	}
	mustHit(t, l, login, "x")
	if d := mustHit(t, l, login, "x"); d.Allowed || d.Remaining != 0 {
		t.Errorf("third hit: %+v, want refused with 0 remaining", d)
	}
}

func TestWriteHeaders_TellsTheLimitWhatIsLeftAndWhenItResets(t *testing.T) {
	l, _ := newLimiter(t)
	d := mustCount(t, l, apiPolicy, "c")
	rec := httptest.NewRecorder()
	WriteHeaders(rec, apiPolicy, d)
	for name, want := range map[string]string{"RateLimit-Limit": "5", "RateLimit-Remaining": "4", "RateLimit-Reset": "50"} {
		if got := rec.Header().Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}
