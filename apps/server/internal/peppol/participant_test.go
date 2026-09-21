package peppol

import "testing"

// The two vectors below were verified against the live SML on 2026-09-21
// (design spec "What the network looks like"): a lookup of
// XQK4T3FMTEZDUY5BOVJMTNAQ45N7E4TIBYHWFPGOT75BSP7UYG2A.iso6523-actorid-upis.participant.sml.prod.tech.peppol.org
// really does return ELMA's NAPTR record for 0192:923609016. They are the
// whole point of this file: the hash is the only part of discovery we cannot
// observe going wrong — a wrong host is indistinguishable from "not
// registered", so an off-by-one here would quietly report every customer as
// unregistered.
const (
	prodZone = "participant.sml.prod.tech.peppol.org"

	vantigoHash = "XQK4T3FMTEZDUY5BOVJMTNAQ45N7E4TIBYHWFPGOT75BSP7UYG2A"
	specHash    = "Y7DZFXAF3D4CJZ4KCGRXTEC6TWVCGA4KY7ZWA5BOIF6MSWD4TDRQ"
)

func TestParticipantHost_SpecVectors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		zone  string
		value string
		want  string
	}{
		{
			name:  "the specification's own example",
			zone:  "edelivery.tech.ec.europa.eu",
			value: "0088:123abc",
			want:  specHash + ".iso6523-actorid-upis.edelivery.tech.ec.europa.eu",
		},
		{
			name:  "a Norwegian organisation number in the production zone",
			zone:  prodZone,
			value: "0192:923609016",
			want:  vantigoHash + ".iso6523-actorid-upis." + prodZone,
		},
		{
			name:  "the test zone is just another zone",
			zone:  "participant.sml.test.tech.peppol.org",
			value: "0192:923609016",
			want:  vantigoHash + ".iso6523-actorid-upis.participant.sml.test.tech.peppol.org",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ParticipantHost(tc.zone, tc.value); got != tc.want {
				t.Errorf("ParticipantHost(%q, %q):\n got %q\nwant %q", tc.zone, tc.value, got, tc.want)
			}
		})
	}
}

// TestParticipantHost_LowerCasesTheValue pins the one transformation the
// specification requires and a reader would not guess: the VALUE is
// lower-cased before hashing, so a customer whose identifier was typed in
// capitals resolves to the same participant. (Nothing else is: the scheme is
// already lower-case, and the hash covers the value alone.)
func TestParticipantHost_LowerCasesTheValue(t *testing.T) {
	t.Parallel()
	lower := ParticipantHost("example.test", "0088:123abc")
	for _, variant := range []string{"0088:123ABC", "0088:123AbC"} {
		if got := ParticipantHost("example.test", variant); got != lower {
			t.Errorf("ParticipantHost(…, %q) = %q, want the same host as the lower-case value %q", variant, got, lower)
		}
	}
}

// TestParticipantHost_HashesOnlyTheValue proves the scheme is not part of the
// hashed string — hashing "iso6523-actorid-upis::0088:123abc" (the full
// participant identifier, which is what the SMP request path uses) would be
// the natural mistake and would produce a name that never resolves.
func TestParticipantHost_HashesOnlyTheValue(t *testing.T) {
	t.Parallel()
	if got := ParticipantHost("example.test", Scheme+"::0088:123abc"); got == ParticipantHost("example.test", "0088:123abc") {
		t.Fatal("ParticipantHost hashed the scheme-qualified identifier to the same host as the bare value")
	}
	if got, want := ParticipantHost("example.test", "0088:123abc"), specHash+".iso6523-actorid-upis.example.test"; got != want {
		t.Errorf("ParticipantHost = %q, want %q", got, want)
	}
}

// TestParticipantHost_LabelIsUnpaddedBase32 pins the shape of the first label
// on its own: RFC 4648 base32, upper-case, the four "=" of a 32-byte digest
// stripped (a padded label is not a legal DNS label at all).
func TestParticipantHost_LabelIsUnpaddedBase32(t *testing.T) {
	t.Parallel()
	label := ParticipantHost("example.test", "0192:974760673")[:52]
	if len(label) != 52 {
		t.Fatalf("label %q is %d characters, want 52", label, len(label))
	}
	for i, r := range label {
		if (r < 'A' || r > 'Z') && (r < '2' || r > '7') {
			t.Fatalf("label %q character %d (%q) is not upper-case RFC 4648 base32", label, i, r)
		}
	}
}
