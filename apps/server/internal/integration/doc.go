// Package integration holds the tests that need more than one business module
// composed for real.
//
// Every other test in this repository is a module's own: it composes that
// module beside identity and hands it hand-written fakes for whatever its
// contracts reach — a fake project directory to the expenses tests, a fake
// expenses provider to the projects tests. That is the right shape for a
// module's own rules, and it is what keeps `internal/<module>` an island that
// depguard can enforce. But it means the one promise a cross-module delivery
// makes — that two modules reading the same data report the same figures — is
// pinned on each side only against the other side's *imitation*. A provider
// that changed its absent-key semantics, its decimal text or its date format
// would keep every suite green and break the product.
//
// So this package is deliberately not a module and is deliberately outside
// every depguard rule: `.golangci.yml`'s rules are scoped with `files:` to
// `**/internal/<module>/**` and the platform list, and this directory is in
// neither, which is what lets one test import `customers`, `projects`, `time`
// and `expenses` together. Nothing here is built into the binary — the package has
// no production code and nothing imports it — so the exemption buys a test and
// costs no coupling. If anything non-test is ever added here, that reasoning
// stops holding and this package needs a depguard rule of its own.
package integration
