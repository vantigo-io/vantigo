package peppol

import (
	"cmp"
	"encoding/binary"
	"fmt"
	"slices"
	"strings"
)

// smpService is the NAPTR service field Peppol publishes an SMP under. It is
// compared case-insensitively: RFC 3403 says the flags and service fields are
// not case sensitive, and the wild is not uniform about the capitals.
const smpService = "Meta:SMP"

// NAPTR is one decoded NAPTR record (RR type 35). Peppol uses exactly one
// shape of it — order/preference for priority, flags "U" (a terminal record
// whose regexp yields a URI), service "Meta:SMP", a regexp substitution
// carrying the SMP's base URL, and the root as replacement — but the fields
// are kept as the record has them rather than pre-interpreted, so a record
// that turns out to be something else can be skipped by the caller instead of
// failing to decode.
//
// Replacement is the name in presentation form, always ending in a dot ("."
// for the root, which is what every U-flag record carries). Label bytes are
// not escaped: a label containing a dot or a backslash would come out
// indistinguishable from a label separator. Nothing in this package reads
// Replacement for anything but reporting, and Peppol's records are all root,
// so that is a limitation rather than a bug — but it is why this type must not
// be used to re-encode a record.
type NAPTR struct {
	Order       uint16
	Preference  uint16
	Flags       string
	Service     string
	Regexp      string
	Replacement string
}

// decodeNAPTR reads one NAPTR record's RDATA. The Go resolver does not expose
// type 35 and golang.org/x/net/dns/dnsmessage has no NAPTR type either, so
// the record arrives as a dnsmessage.UnknownResource holding these raw bytes
// and this function is what makes sense of them.
//
// The format (RFC 3403 §4.1) is a u16 order, a u16 preference, three
// <character-string>s (a length byte and that many bytes: flags, service,
// regexp) and a domain name as length-prefixed labels ending in a zero byte.
// It is context-free — §4.1 forbids name compression in NAPTR RDATA — which
// is what makes decoding possible here at all, with only the record's own
// bytes and no view of the enclosing message.
//
// These bytes come from a third party over UDP, so every length is checked
// against what is left of the buffer and nothing is trusted; decodeNAPTR
// returns an error on anything it cannot read and never panics (FuzzDecodeNAPTR
// holds it to that). Two judgement calls:
//
//   - a label length byte above 63 is rejected rather than interpreted. In a
//     full message 0xc0 would start a compression pointer, but §4.1 forbids
//     compression here, so in this RDATA it is either a corrupt record or an
//     attempt to make a decoder read out of bounds. 0x40–0xbf are reserved
//     label types and equally unreadable.
//   - bytes left over after the replacement name are rejected. Because the
//     format is self-delimiting there is no ambiguity about where the record
//     ends, so trailing bytes are not "extra data to ignore" — a decoder that
//     ignored them would accept a record with something hidden behind it.
func decodeNAPTR(rdata []byte) (NAPTR, error) {
	if len(rdata) < 4 {
		return NAPTR{}, fmt.Errorf("peppol: NAPTR rdata is %d bytes, too short for its order and preference", len(rdata))
	}
	record := NAPTR{
		Order:      binary.BigEndian.Uint16(rdata[0:2]),
		Preference: binary.BigEndian.Uint16(rdata[2:4]),
	}

	offset := 4
	for _, field := range []struct {
		name  string
		value *string
	}{
		{"flags", &record.Flags},
		{"service", &record.Service},
		{"regexp", &record.Regexp},
	} {
		value, next, err := decodeCharacterString(rdata, offset)
		if err != nil {
			return NAPTR{}, fmt.Errorf("peppol: NAPTR %s: %w", field.name, err)
		}
		*field.value, offset = value, next
	}

	replacement, offset, err := decodeName(rdata, offset)
	if err != nil {
		return NAPTR{}, fmt.Errorf("peppol: NAPTR replacement: %w", err)
	}
	if offset != len(rdata) {
		return NAPTR{}, fmt.Errorf("peppol: NAPTR rdata has %d bytes left over after the replacement name", len(rdata)-offset)
	}
	record.Replacement = replacement
	return record, nil
}

// decodeCharacterString reads a <character-string> at offset and returns it
// with the offset just past it.
func decodeCharacterString(rdata []byte, offset int) (string, int, error) {
	if offset >= len(rdata) {
		return "", 0, fmt.Errorf("rdata ends before its length byte (offset %d of %d bytes)", offset, len(rdata))
	}
	length := int(rdata[offset])
	offset++
	if offset+length > len(rdata) {
		return "", 0, fmt.Errorf("length %d runs %d bytes past the end of the rdata", length, offset+length-len(rdata))
	}
	return string(rdata[offset : offset+length]), offset + length, nil
}

// decodeName reads a domain name in wire format at offset and returns it in
// presentation form (the root as "."), with the offset just past the
// terminating zero byte.
func decodeName(rdata []byte, offset int) (string, int, error) {
	var name strings.Builder
	for {
		if offset >= len(rdata) {
			return "", 0, fmt.Errorf("rdata ends before the name's root label (offset %d of %d bytes)", offset, len(rdata))
		}
		length := int(rdata[offset])
		offset++
		if length == 0 {
			break
		}
		if length > 63 {
			return "", 0, fmt.Errorf("label length byte 0x%02x is a compression pointer or a reserved label type, neither of which a NAPTR rdata may use (RFC 3403 §4.1)", length)
		}
		if offset+length > len(rdata) {
			return "", 0, fmt.Errorf("label of %d bytes runs %d bytes past the end of the rdata", length, offset+length-len(rdata))
		}
		name.Write(rdata[offset : offset+length])
		name.WriteByte('.')
		offset += length
	}
	if name.Len() == 0 {
		return ".", offset, nil
	}
	return name.String(), offset, nil
}

// smpBaseURL is the record set's answer to "where is this participant's SMP?".
// It takes the records in the priority the participant published them (lowest
// order first, preference breaking a tie) and returns the base URL of the
// first one that is a terminal SMP entry: flags "U" and service "Meta:SMP".
//
// ok is false when the answer holds no such record. That is the design's third
// DNS outcome and not an error: the name exists, so the participant IS in the
// network, but nothing in it points at an SMP — registered, able to receive
// nothing.
//
// A malformed regexp in a record that DID qualify is an error, and
// deliberately not a reason to try the next record: the participant nominated
// this SMP first, so silently using a lower-priority one would send documents
// somewhere they did not ask for. The caller's ctx is better spent reporting a
// broken record than guessing around it.
func smpBaseURL(records []NAPTR) (string, bool, error) {
	ordered := slices.Clone(records)
	slices.SortStableFunc(ordered, func(a, b NAPTR) int {
		if a.Order != b.Order {
			return cmp.Compare(a.Order, b.Order)
		}
		return cmp.Compare(a.Preference, b.Preference)
	})

	for _, record := range ordered {
		if !strings.EqualFold(record.Flags, "U") || !strings.EqualFold(record.Service, smpService) {
			continue
		}
		base, err := substitution(record.Regexp)
		if err != nil {
			return "", false, fmt.Errorf("peppol: the %s record with order %d, preference %d: %w", smpService, record.Order, record.Preference, err)
		}
		return base, true, nil
	}
	return "", false, nil
}

// substitution reads the replacement half of a NAPTR regexp field — the
// "<delim>ere<delim>replacement<delim>" substitution expression of RFC 2915
// §2, where the delimiter is whatever the first byte is (Peppol's records use
// "!", as in "!.*!https://smp.elma-smp.no/!", but the delimiter is the
// record's choice, not a constant).
//
// The ERE is not applied: Peppol's is always ".*" and the replacement is
// always a literal URL, so there is nothing to match against and no
// backreference to expand. A replacement that did contain one would be taken
// literally and then rejected by the URL policy in smp.go, which is the right
// end for it.
//
// Flags after the closing delimiter (RFC 2915 allows "i") are ignored; an
// expression with a delimiter anywhere else — including inside the URL — is
// rejected rather than guessed at.
func substitution(expression string) (string, error) {
	if expression == "" {
		return "", fmt.Errorf("has an empty regexp field, so it names no SMP URL")
	}
	delimiter := expression[0:1]
	parts := strings.Split(expression, delimiter)
	if len(parts) != 4 || parts[0] != "" {
		return "", fmt.Errorf("has regexp %q, which is not a %s-delimited substitution expression", expression, delimiter)
	}
	if parts[2] == "" {
		return "", fmt.Errorf("has regexp %q, whose replacement half is empty", expression)
	}
	return parts[2], nil
}
