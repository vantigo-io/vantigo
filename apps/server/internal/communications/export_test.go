package communications

import "github.com/vantigo-io/vantigo/server/internal/secrets"

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

// OpenChannelPasswordForTest decrypts a stored channel credential's
// secret_ciphertext with this module's own purpose, so a black-box test can
// assert WHICH password survived a concurrent write.
//
// It exists because the credential lost update has no other detector: the
// password is sealed at rest and never echoed in any response, sealing is
// randomised (so two ciphertexts of the same plaintext differ, and comparing
// ciphertexts proves nothing), and no constraint covers the column, so there
// is no violation to observe either. Without decrypting, a test cannot tell a
// reverted password from a rotated one.
func OpenChannelPasswordForTest(box *secrets.Box, stored string) (string, error) {
	sealed, err := decodeCiphertext(stored)
	if err != nil {
		return "", err
	}
	opened, err := box.Open(channelCredentialPurpose, sealed)
	if err != nil {
		return "", err
	}
	return string(opened), nil
}

// RetentionLeaseKeyForTest is retentionLeaseKey (retention.go), exported by
// the same convention so retention_test.go can take the real lease from its
// own connection and watch the worker skip its cycle. A test that hardcoded
// the constant would still pass if the worker started using a different key.
const RetentionLeaseKeyForTest = retentionLeaseKey
