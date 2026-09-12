package customers

import (
	"fmt"
	"strings"
	"unicode/utf16"
)

// This file ports the .NET Customers module's value objects
// (DM/Customers/Common/*.cs, DM/Customers/ValueObjects/LegalIdentity.cs,
// customers inventory §2.2): each a `TryCreate(raw) -> (normalized, error)`
// pair in .NET, kept here as a normalize-or-explain function rather than a
// wrapper type, since Go has no implicit conversions to make a wrapper type
// pull its weight. The exact error message text is the contract — this
// module has no machine-readable error codes at all (inventory §1) — so
// every string below is copied verbatim from the .NET source, not
// paraphrased.

// utf16Length is len(s) as .NET's string.Length counts it: UTF-16 code
// units, not runes. Duplicated from internal/identity/passwords.go's
// utf16Length: depguard forbids this module importing identity, and the
// function is four lines.
func utf16Length(s string) int {
	n := 0
	for _, r := range s {
		n += utf16.RuneLen(r)
	}
	return n
}

// validateFriendlyName is FriendlyName's Validate and constructor
// (DM/Customers/Common/FriendlyName.cs): non-blank, at most 255 UTF-16
// code units, trimmed but case-preserved.
func validateFriendlyName(raw string) (string, string) {
	if strings.TrimSpace(raw) == "" {
		return "", "A friendly name cannot be null or empty"
	}
	if n := utf16Length(raw); n > 255 {
		return "", fmt.Sprintf("A friendly name cannot be longer than 255 characters, the given value was %d characters", n)
	}
	return strings.TrimSpace(raw), ""
}

// validateCustomerStatus is CustomerStatus's Validate and constructor
// (DM/Customers/Common/CustomerStatus.cs): one of active/disabled/archived,
// case-insensitive, trimmed and lowercased. The "but was '{v}'" message
// quotes the raw, unnormalized value, as .NET's does.
func validateCustomerStatus(raw string) (string, string) {
	if strings.TrimSpace(raw) == "" {
		return "", "A customer status cannot be null or empty"
	}
	lower := strings.ToLower(strings.TrimSpace(raw))
	switch lower {
	case "active", "disabled", "archived":
		return lower, ""
	default:
		return "", fmt.Sprintf("A customer status must be one of 'active', 'disabled' or 'archived', but was '%s'", raw)
	}
}

// validateCountryCode is CountryCode's Validate and constructor
// (DM/Customers/Common/CountryCode.cs): only non-blank is required — no
// ISO-3166 shape check exists in .NET, so none exists here either.
func validateCountryCode(raw string) (string, string) {
	if strings.TrimSpace(raw) == "" {
		return "", "A country code cannot be null or empty"
	}
	return strings.ToLower(strings.TrimSpace(raw)), ""
}

// validateLegalID is LegalId's Validate and constructor
// (DM/Customers/Common/LegalId.cs): non-blank, at most 50 UTF-16 code
// units, trimmed and lowercased. Not format- or checksum-validated, e.g. no
// Norwegian organisasjonsnummer check, regardless of source.
func validateLegalID(raw string) (string, string) {
	if strings.TrimSpace(raw) == "" {
		return "", "A legal id cannot be null or empty"
	}
	if n := utf16Length(raw); n > 50 {
		return "", fmt.Sprintf("A legal id cannot be longer than 50 characters, the given value was %d characters", n)
	}
	return strings.ToLower(strings.TrimSpace(raw)), ""
}

// validateLegalName is LegalName's Validate and constructor
// (DM/Customers/Common/LegalName.cs): non-blank, at most 255 UTF-16 code
// units, trimmed but case-preserved.
func validateLegalName(raw string) (string, string) {
	if strings.TrimSpace(raw) == "" {
		return "", "A legal name cannot be null or empty"
	}
	if n := utf16Length(raw); n > 255 {
		return "", fmt.Sprintf("A legal name cannot be longer than 255 characters, the given value was %d characters", n)
	}
	return strings.TrimSpace(raw), ""
}

// validateLegalSource is LegalSource's Validate and constructor
// (DM/Customers/Common/LegalSource.cs): one of brreg/manual,
// case-insensitive, trimmed and lowercased.
func validateLegalSource(raw string) (string, string) {
	if strings.TrimSpace(raw) == "" {
		return "", "A legal source cannot be null or empty"
	}
	lower := strings.ToLower(strings.TrimSpace(raw))
	if lower != "brreg" && lower != "manual" {
		return "", fmt.Sprintf("A legal source must be one of 'brreg', 'manual', but was '%s'", raw)
	}
	return lower, ""
}

// validateLegalType is LegalType's Validate and constructor
// (DM/Customers/Common/LegalType.cs): only non-blank is required —
// "person"/"business" are conventional constants, not an enforced set, so
// e.g. "spaceship" passes.
func validateLegalType(raw string) (string, string) {
	if strings.TrimSpace(raw) == "" {
		return "", "A legal type cannot be null or empty"
	}
	return strings.ToLower(strings.TrimSpace(raw)), ""
}

// legalIdentity is a customer's normalized legal identity: the five value
// objects LegalIdentity.cs composes, always all-set-or-all-unset together
// (inventory §2.1). A nil *legalIdentity is "no identity"; every field of a
// non-nil one has already passed its value object's validation.
type legalIdentity struct {
	Country string
	Type    string
	ID      string
	Name    string
	Source  string
}

// validateLegalIdentity is LegalIdentity.TryCreate
// (DM/Customers/ValueObjects/LegalIdentity.cs:18-72): every field is
// validated independently and every error reported together, keyed
// "country"/"type"/"id"/"name"/"source" — never short-circuited on the
// first failure.
func validateLegalIdentity(country, legalType, id, name, source string) (legalIdentity, map[string][]string) {
	errs := map[string][]string{}

	c, err := validateCountryCode(country)
	if err != "" {
		errs["country"] = []string{err}
	}
	t, err := validateLegalType(legalType)
	if err != "" {
		errs["type"] = []string{err}
	}
	i, err := validateLegalID(id)
	if err != "" {
		errs["id"] = []string{err}
	}
	n, err := validateLegalName(name)
	if err != "" {
		errs["name"] = []string{err}
	}
	s, err := validateLegalSource(source)
	if err != "" {
		errs["source"] = []string{err}
	}

	if len(errs) > 0 {
		return legalIdentity{}, errs
	}
	return legalIdentity{Country: c, Type: t, ID: i, Name: n, Source: s}, nil
}
