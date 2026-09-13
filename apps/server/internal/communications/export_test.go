package communications

// ReplyFingerprintForTest is replyFingerprint (conversations_reply.go),
// exported only for tests via the standard Go export_test.go convention —
// this file compiles into test binaries only, never production ones, so it
// adds no production surface. package communications_test's own black-box
// tests (conversations_reply_test.go) need the exact same fingerprint
// computation the real handler uses to build a matching idempotency_records
// fixture; before this, that test hand-duplicated replyFingerprint's
// anonymous struct shape, which could in principle drift silently from the
// real one (fix round 1's review confirmed it fails loudly rather than
// silently, but a construction that cannot drift at all is strictly
// better). Being the same func value, this cannot drift: any change to
// replyFingerprint is this var's change too.
var ReplyFingerprintForTest = replyFingerprint

// NewOutboxWorkerWithMeterForTest is newOutboxWorker (outbox.go) with the
// meter provider chosen by the caller, so outbox_test.go can give one worker
// its own sdk/metric reader and read exact counter values back. Production's
// NewOutboxWorker resolves the global provider instead.
//
// The alternative — package-level instruments on the global provider — would
// make every assertion in this package a race against every other parallel
// test that happens to run a worker, since a global counter is shared by all
// of them. This seam keeps the four counters (metrics.go) genuinely testable
// without a process-wide otel.SetMeterProvider that no parallel test could
// rely on.
var NewOutboxWorkerWithMeterForTest = newOutboxWorker

// RetentionLeaseKeyForTest is retentionLeaseKey (retention.go), exported by
// the same convention so retention_test.go can take the real lease from its
// own connection and watch the worker skip its cycle. A test that hardcoded
// the constant would still pass if the worker started using a different key.
const RetentionLeaseKeyForTest = retentionLeaseKey
