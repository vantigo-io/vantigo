package identity

import (
	"strings"

	"github.com/vantigo-io/vantigo/server/internal/config"
)

// A brand's support contacts (brands design B37) are validated exactly as the
// environment's APP_SUPPORT_* are, by the same functions: the editor and the
// one-time import of the environment (brand_import.go) both go through
// validateSupport. A violation is 422 brand_support_invalid naming the field.

const (
	codeBrandSupportInvalid = "brand_support_invalid"
	maxSupportPhone         = 30
)

// supportFieldMessages say what each contact may be.
var supportFieldMessages = map[string]string{
	"email": "The support e-mail must be a plain address such as support@example.com.",
	"phone": "The support phone may use only digits, spaces and + - ( ) . and be at most 30 characters.",
	"url":   "The support link must be an absolute http or https URL or a path starting with a single /.",
}

// validPhone is a phone safe to put in a tel: link: digits, spaces and
// + - ( ) . only, at most 30 characters.
func validPhone(v string) bool {
	if len(v) > maxSupportPhone {
		return false
	}
	for _, r := range v {
		switch {
		case r >= '0' && r <= '9', r == ' ', r == '+', r == '-', r == '(', r == ')', r == '.':
		default:
			return false
		}
	}
	return true
}

// validateSupport trims s and checks every contact that is set. It answers the
// trimmed contacts and, for each one that is invalid, the field's name and a
// sentence; an empty contact is "not set" and always valid.
func validateSupport(s brandSupport) (brandSupport, map[string][]string) {
	s.Email, s.Phone, s.URL = strings.TrimSpace(s.Email), strings.TrimSpace(s.Phone), strings.TrimSpace(s.URL)
	bad := map[string][]string{}
	if s.Email != "" && !config.ValidPlainAddress(s.Email) {
		bad["email"] = []string{supportFieldMessages["email"]}
	}
	if s.Phone != "" && !validPhone(s.Phone) {
		bad["phone"] = []string{supportFieldMessages["phone"]}
	}
	if s.URL != "" && !config.ValidLink(s.URL) {
		bad["url"] = []string{supportFieldMessages["url"]}
	}
	if len(bad) == 0 {
		return s, nil
	}
	return s, bad
}
