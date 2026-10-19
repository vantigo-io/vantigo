// Package reminderrules is the rules engine of Invoices phase 4 (design D8)
// and its charge formula (D9): pure functions over plain inputs, which the
// overdue list, the run's preview, the run and the letter's dispatch all call,
// so the four never disagree.
//
// It reads no database and no clock: every day is an input, as the module
// derives it (holidays.DayIn in the workspace's business time zone) — UTC
// midnight of that calendar day — so day
// arithmetic never meets a daylight-saving shift; and every amount is an
// exact decimal (*big.Rat), rounded only where the rules say: a fee to the
// krone (.50 up), interest to the øre once, the half away from zero. A leaf:
// it imports the standard library and the platform's public-holiday calendar
// (internal/holidays, as pure) only (purity_test.go), and the server's
// rule-input loader is its only door.
package reminderrules
