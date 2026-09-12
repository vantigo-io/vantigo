package customers

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// This file white-box tests brreg.go's retry primitives directly (the
// values_test.go convention: package customers, not customers_test) —
// brregBackoff's jitter and cap, and waitBackoff's context-cancellation
// behaviour — neither of which an HTTP-level test in brreg_test.go can pin
// without either being flaky (asserting on a jittered wall-clock duration
// through a real request) or never sleeping at all.

// TestBrregBackoff_IsBoundedAndJittered proves every draw across the three
// retry attempts falls inside [0, cap], and that repeated calls are not all
// the same value — the mutation this catches is a fixed (non-jittered) or
// unbounded backoff.
func TestBrregBackoff_IsBoundedAndJittered(t *testing.T) {
	wantCaps := map[int]time.Duration{
		1: brregBackoffBase,     // 200ms
		2: 2 * brregBackoffBase, // 400ms
		3: 4 * brregBackoffBase, // 800ms
	}
	seen := map[int]map[time.Duration]bool{1: {}, 2: {}, 3: {}}
	for range 200 {
		for attempt, capForAttempt := range wantCaps {
			d := brregBackoff(attempt)
			if d < 0 || d > capForAttempt {
				t.Fatalf("brregBackoff(%d) = %v, want [0, %v]", attempt, d, capForAttempt)
			}
			seen[attempt][d] = true
		}
	}
	for attempt, draws := range seen {
		if len(draws) < 2 {
			t.Errorf("brregBackoff(%d) returned the same value on every one of 200 calls, want jitter", attempt)
		}
	}
}

// TestBrregBackoff_NeverExceedsCap proves a later attempt's exponential
// growth is clamped at brregBackoffCap (1s), not left to grow unbounded —
// attempt 4 would be 1.6s uncapped, well past the 1s ceiling the review
// ruled.
func TestBrregBackoff_NeverExceedsCap(t *testing.T) {
	for range 200 {
		if d := brregBackoff(4); d > brregBackoffCap {
			t.Fatalf("brregBackoff(4) = %v, want capped at %v", d, brregBackoffCap)
		}
	}
}

// TestWaitBackoff_ReturnsPromptlyOnCanceledContext proves a canceled
// context stops the wait immediately rather than sleeping out the full
// duration — the mutation this catches is a plain time.Sleep(d) that
// ignores ctx entirely, which would make a canceled lookup sit in a sleep
// instead of returning right away.
func TestWaitBackoff_ReturnsPromptlyOnCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	ok := waitBackoff(ctx, time.Hour)
	elapsed := time.Since(start)

	if ok {
		t.Error("waitBackoff on an already-canceled context = true, want false")
	}
	if elapsed > 100*time.Millisecond {
		t.Errorf("waitBackoff on an already-canceled context took %v, want it to return promptly instead of sleeping out the full duration", elapsed)
	}
}

// TestWaitBackoff_WaitsOutADurationThatCompletesFirst is the ordinary path:
// a short duration against a context with no deadline completes the wait
// and reports true.
func TestWaitBackoff_WaitsOutADurationThatCompletesFirst(t *testing.T) {
	if !waitBackoff(context.Background(), time.Millisecond) {
		t.Error("waitBackoff(context.Background(), 1ms) = false, want true")
	}
}

// TestIsRetryableStatus pins isRetryableStatus's exact predicate: 5xx and
// 408 are retryable, everything else — 2xx/3xx, and 4xx other than 408 — is
// not.
func TestIsRetryableStatus(t *testing.T) {
	cases := map[int]bool{
		http.StatusOK:                  false,
		http.StatusNotFound:            false,
		http.StatusBadRequest:          false,
		http.StatusRequestTimeout:      true,
		http.StatusTooManyRequests:     false,
		http.StatusInternalServerError: true,
		http.StatusBadGateway:          true,
		http.StatusServiceUnavailable:  true,
	}
	for status, want := range cases {
		if got := isRetryableStatus(status); got != want {
			t.Errorf("isRetryableStatus(%d) = %v, want %v", status, got, want)
		}
	}
}

// TestIsSuccessStatus pins isSuccessStatus's exact predicate: only 2xx.
func TestIsSuccessStatus(t *testing.T) {
	cases := map[int]bool{
		199: false,
		200: true,
		299: true,
		300: false,
		404: false,
		500: false,
	}
	for status, want := range cases {
		if got := isSuccessStatus(status); got != want {
			t.Errorf("isSuccessStatus(%d) = %v, want %v", status, got, want)
		}
	}
}
