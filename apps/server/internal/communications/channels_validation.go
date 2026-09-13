package communications

import (
	"net/mail"
	"strings"
	"unicode"
	"unicode/utf16"
)

// This file is the Channels area's value validators (ValidateChannel /
// ValidateChannelUpdate, EP/Dtos/CommunicationValidation.cs, communications
// inventory §3.1's field table and §19.2 items 4 and 11). It imports the
// standard library's net/mail, never internal/mail (this module's SMTP
// sender and destination guard) — channels.go keeps that import instead, to
// avoid the two "mail" packages colliding in one file.

// utf16Length is len(s) as .NET's string.Length counts it: UTF-16 code
// units, not runes. Duplicated per-module (customers/values.go,
// energy/values.go, products/values.go): depguard forbids this module
// importing another module's package for four lines of code.
func utf16Length(s string) int {
	n := 0
	for _, r := range s {
		n += utf16.RuneLen(r)
	}
	return n
}

// validChannelAddress is CommunicationValidation.IsEmail
// (EP/Dtos/CommunicationValidation.cs:74-78, inventory §19.2 item 4):
// MimeKit-backed and round-trip-strict, not the simpler shape check other
// modules' email validators use. At most 320 UTF-16 code units, no
// leading/trailing whitespace, no whitespace anywhere, no control
// characters, none of '<' '>' '"', and net/mail.ParseAddress must round-trip
// to exactly the input — a display-name form like "Bob <b@x.test>" parses
// to "b@x.test", which differs from the input and is correctly rejected.
// This round-trip equality is the load-bearing half and the easiest part to
// lose when hand-rolling a Go validator (inventory's own warning).
func validChannelAddress(v string) bool {
	if v == "" || utf16Length(v) > 320 {
		return false
	}
	if v != strings.TrimSpace(v) {
		return false
	}
	for _, r := range v {
		switch r {
		case '<', '>', '"':
			return false
		}
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return false
		}
	}
	parsed, err := mail.ParseAddress(v)
	if err != nil {
		return false
	}
	return parsed.Address == v
}

// validDisplayName is the channel displayName rule behind "DisplayName is
// invalid." (inventory §3.1): at most 200 UTF-16 code units, already
// trimmed (untrimmed is invalid — never silently trimmed), no control
// characters. Callers only invoke this for a non-empty value: blank means
// "no display name" on create and "clear to null" on update (inventory
// §19.2 item 11's tri-state), neither of which is a validation failure.
func validDisplayName(v string) bool {
	if utf16Length(v) > 200 {
		return false
	}
	if v != strings.TrimSpace(v) {
		return false
	}
	for _, r := range v {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// validSMTPPort is the port half of "SMTP credentials require a host and
// valid port." (inventory §3.1: "port must be 1-65535").
func validSMTPPort(port int32) bool {
	return port >= 1 && port <= 65535
}
