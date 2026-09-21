package peppol

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

// naptrRDATA encodes one NAPTR record the way a name server would, and is the
// fixture builder every test in this package that needs a NAPTR answer uses.
// It is deliberately a second, independent implementation of the format
// decodeNAPTR reads (RFC 3403 §4.1: two u16s, three <character-string>s,
// then a domain name with no compression) — a decoder tested only against its
// own encoder proves nothing, which is why the test below this one decodes a
// byte-for-byte transcription of the live ELMA record instead.
//
// replacement is written in presentation form: "." for the root (every
// U-flag record, which is all Peppol uses) or a dotted name.
func naptrRDATA(order, preference uint16, flags, service, regexp, replacement string) []byte {
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.BigEndian, order)
	_ = binary.Write(&buf, binary.BigEndian, preference)
	for _, s := range []string{flags, service, regexp} {
		buf.WriteByte(byte(len(s)))
		buf.WriteString(s)
	}
	for _, label := range strings.Split(strings.TrimSuffix(replacement, "."), ".") {
		if label == "" {
			continue
		}
		buf.WriteByte(byte(len(label)))
		buf.WriteString(label)
	}
	buf.WriteByte(0) // the root label ends every name
	return buf.Bytes()
}

// elmaRDATA is the RDATA of the record the production SML really returns for
// 0192:923609016, transcribed by hand from the live answer
// (`100 10 "U" "Meta:SMP" "!.*!https://smp.elma-smp.no/!" .`, TTL 60) with
// every length byte written out. If decodeNAPTR ever stops reading this, the
// feature is broken against the real network no matter what the rest of the
// suite says.
func elmaRDATA() []byte {
	rdata := []byte{
		0x00, 0x64, // order 100
		0x00, 0x0a, // preference 10
		0x01, 'U', // flags, one character
		0x08, // service, eight characters
	}
	rdata = append(rdata, "Meta:SMP"...)
	rdata = append(rdata, 0x1d) // regexp, 29 characters
	rdata = append(rdata, "!.*!https://smp.elma-smp.no/!"...)
	return append(rdata, 0x00) // replacement: the root
}

// paddedLabel is the live record with its replacement replaced by a label whose
// length byte is lengthByte and which really is followed by count bytes, so the
// rdata is long enough for a bounds check to pass.
func paddedLabel(lengthByte byte, count int) []byte {
	rdata := append(elmaRDATA()[:len(elmaRDATA())-1], lengthByte)
	rdata = append(rdata, bytes.Repeat([]byte{'a'}, count)...)
	return append(rdata, 0x00)
}

func TestDecodeNAPTR_LiveELMARecord(t *testing.T) {
	t.Parallel()
	got, err := decodeNAPTR(elmaRDATA())
	if err != nil {
		t.Fatalf("decodeNAPTR(the live ELMA record): %v", err)
	}
	want := NAPTR{
		Order:       100,
		Preference:  10,
		Flags:       "U",
		Service:     "Meta:SMP",
		Regexp:      "!.*!https://smp.elma-smp.no/!",
		Replacement: ".",
	}
	if got != want {
		t.Errorf("decodeNAPTR:\n got %+v\nwant %+v", got, want)
	}
}

// TestDecodeNAPTR_RoundTripsTheFixtureBuilder is the cheap half of the
// decoder's contract: whatever naptrRDATA encodes comes back out, including a
// non-root replacement name (S and A flag records carry one, and although
// Peppol never publishes them a decoder that mangled the name would be wrong).
func TestDecodeNAPTR_RoundTripsTheFixtureBuilder(t *testing.T) {
	t.Parallel()
	cases := []NAPTR{
		{100, 10, "U", "Meta:SMP", "!.*!https://smp.example.test/!", "."},
		{0, 0, "", "", "", "."},
		{65535, 65535, "u", "meta:smp", "#.*#https://smp.example.test#", "smp.example.test."},
		{20, 5, "S", "SIP+D2U", "", "_sip._udp.example.test."},
	}
	for _, want := range cases {
		got, err := decodeNAPTR(naptrRDATA(want.Order, want.Preference, want.Flags, want.Service, want.Regexp, want.Replacement))
		if err != nil {
			t.Errorf("decodeNAPTR(%+v): %v", want, err)
			continue
		}
		if got != want {
			t.Errorf("decodeNAPTR:\n got %+v\nwant %+v", got, want)
		}
	}
}

// TestDecodeNAPTR_RejectsMalformedRDATA is the whole reason this decoder is
// hand-written: the bytes come from a third party over UDP, so every length
// byte has to be checked against what is left rather than trusted. Each case
// is one way a length can lie.
func TestDecodeNAPTR_RejectsMalformedRDATA(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		rdata []byte
	}{
		{"empty", nil},
		{"order alone", []byte{0x00, 0x64}},
		{"no room for the flags length byte", []byte{0x00, 0x64, 0x00, 0x0a}},
		{"flags length runs past the end", []byte{0x00, 0x64, 0x00, 0x0a, 0x09, 'U'}},
		{"service length runs past the end", []byte{0x00, 0x64, 0x00, 0x0a, 0x01, 'U', 0x40, 'M', 'e', 't', 'a'}},
		{"regexp length runs past the end", append([]byte{0x00, 0x64, 0x00, 0x0a, 0x01, 'U', 0x00, 0xff}, "!.*!x!"...)},
		{"replacement is missing entirely", elmaRDATA()[:len(elmaRDATA())-1]},
		{"replacement label runs past the end", append(elmaRDATA()[:len(elmaRDATA())-1], 0x05, 'a', 'b')},
		{"replacement uses a compression pointer", append(elmaRDATA()[:len(elmaRDATA())-1], 0xc0, 0x0c)},
		{"replacement uses a reserved label type", append(elmaRDATA()[:len(elmaRDATA())-1], 0x41, 'a')},
		// The two above are short enough that a decoder which merely checked
		// the bounds would reject them anyway; these two carry enough bytes to
		// satisfy a bounds check, so only a decoder that actually refuses the
		// label type rejects them. (RFC 3403 §4.1 forbids compression in NAPTR
		// rdata, so 0xc0 here is corruption or an attack, never a pointer to
		// follow.)
		{"a compression pointer long enough to look in bounds", paddedLabel(0xc0, 0xc0)},
		{"a reserved label type long enough to look in bounds", paddedLabel(0x41, 0x41)},
		{"trailing bytes after the replacement", append(elmaRDATA(), 0x00, 0x01)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := decodeNAPTR(tc.rdata)
			if err == nil {
				t.Fatalf("decodeNAPTR(% x) = %+v, want an error", tc.rdata, got)
			}
			if !strings.HasPrefix(err.Error(), "peppol: ") {
				t.Errorf("error %q does not name the package", err)
			}
		})
	}
}

// FuzzDecodeNAPTR holds the decoder to its one absolute promise: it never
// panics, whatever arrives on the wire. The seeds are the live record, the
// fixture builder's output and a handful of the truncations above; the
// invariants checked on a successful decode are the ones a caller relies on
// (a <character-string> cannot exceed 255 bytes, and a name always ends in a
// dot), so a decode that "succeeded" by reading past its bounds is caught
// rather than merely surviving.
func FuzzDecodeNAPTR(f *testing.F) {
	f.Add(elmaRDATA())
	f.Add(naptrRDATA(100, 10, "U", "Meta:SMP", "!.*!https://smp.example.test!", "."))
	f.Add(naptrRDATA(20, 5, "S", "SIP+D2U", "", "_sip._udp.example.test."))
	f.Add([]byte{})
	f.Add([]byte{0x00, 0x64, 0x00, 0x0a})
	f.Add([]byte{0x00, 0x64, 0x00, 0x0a, 0xff, 'U'})
	f.Add(append(elmaRDATA()[:len(elmaRDATA())-1], 0xc0, 0x0c))

	f.Fuzz(func(t *testing.T, rdata []byte) {
		got, err := decodeNAPTR(rdata)
		if err != nil {
			return
		}
		for name, field := range map[string]string{"flags": got.Flags, "service": got.Service, "regexp": got.Regexp} {
			if len(field) > 255 {
				t.Fatalf("%s decoded to %d bytes, which no <character-string> can hold", name, len(field))
			}
		}
		if !strings.HasSuffix(got.Replacement, ".") {
			t.Fatalf("replacement %q does not end in a dot", got.Replacement)
		}
	})
}

// TestSMPBaseURL_PicksTheURLTheNetworkPointsAt walks the choices smpBaseURL
// makes over a record set: lowest order wins, preference breaks a tie, and a
// record that is not a U-flag Meta:SMP entry is not a candidate at all.
func TestSMPBaseURL_PicksTheURLTheNetworkPointsAt(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		records []NAPTR
		want    string
	}{
		{
			name:    "the live single-record answer",
			records: []NAPTR{{100, 10, "U", "Meta:SMP", "!.*!https://smp.elma-smp.no/!", "."}},
			want:    "https://smp.elma-smp.no/",
		},
		{
			name: "lowest order wins, whatever order the answer arrived in",
			records: []NAPTR{
				{200, 10, "U", "Meta:SMP", "!.*!https://high.example.test/!", "."},
				{100, 99, "U", "Meta:SMP", "!.*!https://low.example.test/!", "."},
			},
			want: "https://low.example.test/",
		},
		{
			name: "preference breaks a tie on order",
			records: []NAPTR{
				{100, 50, "U", "Meta:SMP", "!.*!https://second.example.test/!", "."},
				{100, 10, "U", "Meta:SMP", "!.*!https://first.example.test/!", "."},
			},
			want: "https://first.example.test/",
		},
		{
			name:    "flags and service are matched case-insensitively",
			records: []NAPTR{{100, 10, "u", "meta:smp", "!.*!https://smp.example.test/!", "."}},
			want:    "https://smp.example.test/",
		},
		{
			name:    "the delimiter is whatever the first byte says",
			records: []NAPTR{{100, 10, "U", "Meta:SMP", "#.*#https://smp.example.test/#", "."}},
			want:    "https://smp.example.test/",
		},
		{
			name: "records that are not a U-flag Meta:SMP entry are skipped",
			records: []NAPTR{
				{10, 10, "S", "Meta:SMP", "!.*!https://wrongflag.example.test/!", "_smp._tcp.example.test."},
				{20, 10, "U", "E2U+sip", "!.*!sip:someone@example.test!", "."},
				{30, 10, "", "Meta:SMP", "!.*!https://noflag.example.test/!", "."},
				{40, 10, "U", "Meta:SMP", "!.*!https://smp.example.test/!", "."},
			},
			want: "https://smp.example.test/",
		},
		{
			name:    "substitution flags after the closing delimiter are ignored",
			records: []NAPTR{{100, 10, "U", "Meta:SMP", "!.*!https://smp.example.test/!i", "."}},
			want:    "https://smp.example.test/",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok, err := smpBaseURL(tc.records)
			if err != nil {
				t.Fatalf("smpBaseURL: %v", err)
			}
			if !ok {
				t.Fatal("smpBaseURL found no usable record, want one")
			}
			if got != tc.want {
				t.Errorf("smpBaseURL = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestSMPBaseURL_NoUsableRecord is the design's third DNS outcome: the name
// exists (the participant IS registered) but nothing in it points at an SMP,
// so there is nothing to ask and no error to report either.
func TestSMPBaseURL_NoUsableRecord(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		records []NAPTR
	}{
		{"no records at all", nil},
		{"only a non-SMP service", []NAPTR{{100, 10, "U", "E2U+email", "!.*!mailto:someone@example.test!", "."}}},
		{"a Meta:SMP record without the U flag", []NAPTR{{100, 10, "S", "Meta:SMP", "", "_smp._tcp.example.test."}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok, err := smpBaseURL(tc.records)
			if err != nil {
				t.Fatalf("smpBaseURL: %v", err)
			}
			if ok {
				t.Errorf("smpBaseURL = %q, ok = true, want no usable record", got)
			}
		})
	}
}

// TestSMPBaseURL_MalformedRegexpIsAnError covers the case where the record
// the network tells us to use is the broken one: that is a failure to report,
// not a participant to call unregistered, and not a reason to fall through to
// a lower-priority SMP the participant did not nominate first.
func TestSMPBaseURL_MalformedRegexpIsAnError(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		regexp string
	}{
		{"empty", ""},
		{"only a delimiter", "!"},
		{"no replacement part", "!.*!"},
		{"an empty replacement", "!.*!!"},
		{"unterminated", "!.*!https://smp.example.test/"},
		{"an extra delimiter inside the URL", "!.*!https://smp.example.test/!!"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			records := []NAPTR{
				{100, 10, "U", "Meta:SMP", tc.regexp, "."},
				{200, 10, "U", "Meta:SMP", "!.*!https://fallback.example.test/!", "."},
			}
			got, ok, err := smpBaseURL(records)
			if err == nil {
				t.Fatalf("smpBaseURL(regexp %q) = %q, %v, want an error", tc.regexp, got, ok)
			}
			if ok {
				t.Error("smpBaseURL reported ok alongside an error")
			}
		})
	}
}

// TestSMPBaseURL_LeavesTheCallersSliceAlone: sorting in place would be a
// surprise for a caller that keeps the records for anything else (a log line,
// a later record).
func TestSMPBaseURL_LeavesTheCallersSliceAlone(t *testing.T) {
	t.Parallel()
	records := []NAPTR{
		{200, 10, "U", "Meta:SMP", "!.*!https://high.example.test/!", "."},
		{100, 10, "U", "Meta:SMP", "!.*!https://low.example.test/!", "."},
	}
	before := append([]NAPTR(nil), records...)
	if _, _, err := smpBaseURL(records); err != nil {
		t.Fatalf("smpBaseURL: %v", err)
	}
	for i := range before {
		if records[i] != before[i] {
			t.Fatalf("smpBaseURL reordered the caller's slice: records[%d] = %+v, want %+v", i, records[i], before[i])
		}
	}
}
