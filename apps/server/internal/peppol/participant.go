package peppol

import (
	"crypto/sha256"
	"encoding/base32"
	"strings"
)

// Scheme is the participant identifier scheme every Peppol participant is
// addressed by ("iso6523-actorid-upis"): the identifier as a whole is
// "iso6523-actorid-upis::0192:923609016", where "0192" is the ICD of the
// Norwegian organisation number register and the rest is the number. Only the
// part after "::" is the VALUE — the thing ParticipantHost hashes and the
// thing callers hand to Client.Lookup.
const Scheme = "iso6523-actorid-upis"

// ParticipantHost is the DNS name a participant's SMP is published under:
//
//	strip-trailing(base32(sha256(lowercase(value))), "=") + "." + Scheme + "." + zone
//
// where base32 is RFC 4648's upper-case alphabet and zone is the SML zone
// (production "participant.sml.prod.tech.peppol.org", test
// "participant.sml.test.tech.peppol.org" — OpenPeppol insourced the SML in
// 2026 and the Commission's old zones are past their switch-over deadline).
// A 32-byte digest is 52 base32 characters plus four "=" of padding, and the
// padding is stripped because "=" is not legal in a DNS label.
//
// Two details of that formula are easy to get wrong and impossible to notice
// afterwards, because a name built wrongly simply does not exist and a
// participant that does not exist looks exactly like one that is not
// registered:
//
//   - only the VALUE is hashed, never the scheme-qualified identifier (the
//     scheme is a plain label in the name instead);
//   - the value is lower-cased first, so an identifier typed in capitals
//     resolves to the same participant.
//
// The returned name has no trailing dot; Resolver.LookupNAPTR adds one when it
// packs the question.
func ParticipantHost(zone, value string) string {
	digest := sha256.Sum256([]byte(strings.ToLower(value)))
	label := strings.TrimRight(base32.StdEncoding.EncodeToString(digest[:]), "=")
	return label + "." + Scheme + "." + zone
}
