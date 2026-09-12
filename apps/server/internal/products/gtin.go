package products

// isValidGTIN ports Gtin.IsValid (DM/Gtin.cs, products inventory §2):
// GTIN-8, GTIN-12 (UPC-A), GTIN-13 (EAN-13) or GTIN-14 only — digits-only,
// and the rightmost digit is a check digit computed with alternating 3-1
// weights counted from the right. Any non-digit character anywhere,
// including a space or a dash inside an otherwise valid digit string, is
// rejected (Gtin.cs:16-40, TS/Domain/Products/GtinTests.cs:38-45).
func isValidGTIN(value string) bool {
	switch len(value) {
	case 8, 12, 13, 14:
	default:
		return false
	}

	sum := 0
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c < '0' || c > '9' {
			return false
		}
		// Weights alternate 3, 1, 3, ... counted from the digit immediately
		// left of the check digit, equivalent to weighting positions whose
		// distance from the right end is odd with 3.
		weight := 1
		if (len(value)-1-i)%2 == 1 {
			weight = 3
		}
		sum += int(c-'0') * weight
	}
	return sum%10 == 0
}
