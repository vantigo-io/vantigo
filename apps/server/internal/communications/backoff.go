package communications

import (
	"math"
	"time"
)

// retryBackoff is this module's one retry-backoff expression, shared by both
// workers that have one: the outbox delivery worker's MarkFailedAsync
// (`SV/OutboxJobProcessor.cs:262`, communications inventory §13.4) and the
// attachment-cleanup worker's own failure path
// (`SV/AttachmentCleanupService.cs:77`, inventory §12.2), which .NET writes
// twice as the identical expression:
//
//	NextAttemptAt = UtcNow + min(3600, pow(2, min(attempts, 10))) seconds
//
// It is ported literally, dead branch included. Two things about it look like
// bugs and are neither, so neither is "fixed" here:
//
//   - **The 3600 s cap is unreachable.** The exponent is clamped at 10, so
//     pow(2, …) ≤ 1024 and min(3600, …) can never bind. The effective ceiling
//     is 1024 s, not the 3600 s `docs/communications.md` documents (inventory
//     §11 D1, `:1346-1349`). The design doc's divergence 4 records that the
//     *doc sentence* is what gets corrected (in task 14) — not this
//     arithmetic. Making 3600 reachable here would change real retry timing
//     for every deployment, which is a behaviour change dressed as a typo fix.
//
//   - **The sequence starts at 2 s, not 1 s.** attempts is incremented at
//     *claim* (queries/outbox.sql's ClaimOutboxJob), so the first failure
//     already has attempts = 1 and schedules 2 s, then 4, 8, 16, 32, 64, 128,
//     256, 512, 1024. With the default maxOutboxAttempts of 8 the job is
//     terminal at attempts = 8, so the largest backoff a live job ever
//     actually waits is 128 s (scheduled at attempts = 7) — the 256/512/1024
//     tail is reachable only with a raised max_attempts, and 3600 never.
//
// attempts ≤ 0 cannot occur through the claim path (the claim increments
// before any failure can be recorded) but is clamped to 0 → 1 s rather than
// left to produce a fractional duration, since math.Pow of a negative
// exponent is a valid float and would silently become a sub-second retry.
func retryBackoff(attempts int32) time.Duration {
	exponent := attempts
	if exponent > 10 {
		exponent = 10
	}
	if exponent < 0 {
		exponent = 0
	}
	seconds := math.Pow(2, float64(exponent))
	// Ported deliberately even though it cannot bind: see the doc comment.
	if seconds > 3600 {
		seconds = 3600
	}
	return time.Duration(seconds) * time.Second
}
