package products

// Ported from Domain/Products/GtinTests.cs.

import "testing"

func TestIsValidGTIN_WithCorrectCheckDigit_ReturnsTrue(t *testing.T) {
	t.Parallel()
	cases := []string{
		"96385074",       // GTIN-8
		"036000291452",   // GTIN-12 (UPC-A)
		"4006381333931",  // GTIN-13 (EAN-13)
		"7350053850019",  // GTIN-13
		"00012345600012", // GTIN-14
	}
	for _, v := range cases {
		if !isValidGTIN(v) {
			t.Errorf("isValidGTIN(%q) = false, want true", v)
		}
	}
}

func TestIsValidGTIN_WithWrongCheckDigit_ReturnsFalse(t *testing.T) {
	t.Parallel()
	cases := []string{"96385075", "4006381333932", "00012345600013"}
	for _, v := range cases {
		if isValidGTIN(v) {
			t.Errorf("isValidGTIN(%q) = true, want false", v)
		}
	}
}

func TestIsValidGTIN_WithUnsupportedLength_ReturnsFalse(t *testing.T) {
	t.Parallel()
	cases := []string{"", "1234567", "123456789", "12345678901", "123456789012345"}
	for _, v := range cases {
		if isValidGTIN(v) {
			t.Errorf("isValidGTIN(%q) = true, want false", v)
		}
	}
}

// TestIsValidGTIN_WithNonDigitCharacters_ReturnsFalse pins that a non-digit
// character anywhere, including a space or a dash inside an otherwise
// valid digit string, is rejected — not just filtered out.
func TestIsValidGTIN_WithNonDigitCharacters_ReturnsFalse(t *testing.T) {
	t.Parallel()
	cases := []string{"9638507a", "4006381 33393", "40063813339-1"}
	for _, v := range cases {
		if isValidGTIN(v) {
			t.Errorf("isValidGTIN(%q) = true, want false", v)
		}
	}
}
