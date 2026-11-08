package invoices

import (
	"sync/atomic"
	"testing"
	"time"
)

// A run is notified at most once a second (live design K37): the first call
// goes out, one inside the window is held and sent once when the window ends,
// and another run is its own.
func TestRunNotifier_AtMostOncePerSecondPerRun_WithATrailingEdge(t *testing.T) {
	var n runNotifier
	var sent atomic.Int32
	send := func() { sent.Add(1) }
	n.throttle(1, send)
	n.throttle(1, send)
	n.throttle(1, send)
	if got := sent.Load(); got != 1 {
		t.Fatalf("sent %d inside the window, want 1", got)
	}
	n.throttle(2, send)
	if got := sent.Load(); got != 2 {
		t.Fatalf("another run was held back: sent %d, want 2", got)
	}
	deadline := time.Now().Add(3 * time.Second)
	for sent.Load() < 4 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := sent.Load(); got != 3 {
		t.Fatalf("sent %d after the window, want the one held trailing event (3)", got)
	}
}

// A finished run is forgotten with its held event, and entries older than the
// window are pruned by the next call.
func TestRunNotifier_ForgetsFinishedAndPrunesOld(t *testing.T) {
	var n runNotifier
	var sent atomic.Int32
	send := func() { sent.Add(1) }
	n.throttle(1, send)
	n.throttle(1, send) // held
	n.forget(1)
	time.Sleep(1200 * time.Millisecond)
	if got := sent.Load(); got != 1 {
		t.Errorf("sent %d, want the held event dropped by forget", got)
	}
	n.throttle(2, send)
	time.Sleep(1100 * time.Millisecond)
	n.throttle(3, send)
	n.mu.Lock()
	_, old := n.runs[2]
	n.mu.Unlock()
	if old {
		t.Error("a run older than the window was not pruned")
	}
}
