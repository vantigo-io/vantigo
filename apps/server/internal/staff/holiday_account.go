package staff

// This file is the holiday account's arithmetic (design S9, §4 The holiday
// account), pure. Delivery B's Task 2 brings the conversion the schemes
// answer with; the account itself is Task 3's.

// convertVirkedager is v virkedager — Monday to Saturday, as the Holiday Act
// counts — as working days of a five-day week, the way Norwegian payroll
// converts them: ⌊v / 6⌋ whole weeks of five days, and the rest up to five.
// 25 virkedager are 21 days, 30 are 25, 31 are 26 (plan pre-flight I3), so a
// Monday-to-Friday week costs five days and an entitlement is never
// undercounted (design M6).
func convertVirkedager(v int) int {
	return v/6*5 + min(v%6, 5)
}
