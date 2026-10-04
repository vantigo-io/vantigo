package kid_test

import (
	"errors"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/invoices/kid"
)

// The check digits against the specification's own worked example (EHF and
// KID design D3, research §6.1): 12345678 is 123456782 under MOD10 and
// 123456785 under MOD11.
func TestKID_TheSpecificationsWorkedExamples(t *testing.T) {
	t.Parallel()
	if got := kid.CheckMod10("12345678"); got != '2' {
		t.Errorf("CheckMod10(12345678) = %q, want '2'", got)
	}
	if got := kid.CheckMod11("12345678"); got != '5' {
		t.Errorf("CheckMod11(12345678) = %q, want '5'", got)
	}
	for _, c := range []struct {
		algorithm, want string
	}{{kid.Mod10, "123456782"}, {kid.Mod11, "123456785"}} {
		got, err := kid.Compute(12345678, 9, c.algorithm)
		if err != nil || got != c.want {
			t.Errorf("Compute(12345678, 9, %s) = %q, %v; want %q", c.algorithm, got, err, c.want)
		}
	}
	// Luhn's own: 7992739871 checks to 3; and a sum that is a multiple of
	// ten checks to 0.
	if got := kid.CheckMod10("7992739871"); got != '3' {
		t.Errorf("CheckMod10(7992739871) = %q, want '3'", got)
	}
	if got := kid.CheckMod10("0"); got != '0' {
		t.Errorf("CheckMod10(0) = %q, want '0'", got)
	}
}

// MOD11's two special remainders: 0 checks to 0, and 1 — where 11 − 1 would
// be ten — is a hyphen, as the specification prescribes.
func TestKID_Mod11RemainderOneIsAHyphen(t *testing.T) {
	t.Parallel()
	// 2: weight 2 → sum 4, remainder 4 → 7. 5: 10, remainder 10 → 1.
	// 6: weight 2 → 12, remainder 1 → '-'. 11: 1·3+1·2 = 5 → 6.
	// 0000: remainder 0 → 0.
	for digits, want := range map[string]byte{"2": '7', "5": '1', "6": '-', "11": '6', "0000": '0'} {
		if got := kid.CheckMod11(digits); got != want {
			t.Errorf("CheckMod11(%s) = %q, want %q", digits, got, want)
		}
	}
	got, err := kid.Compute(6, 5, kid.Mod11)
	if err != nil || got != "0006-" {
		t.Errorf("Compute(6, 5, mod11) = %q, %v; want 0006-", got, err)
	}
	if !kid.Verify("0006-", kid.Mod11, 6) {
		t.Error("Verify(0006-, mod11, 6) = false, want true")
	}
}

// Compute pads the number to the agreed length less one, refuses a number
// that does not fit or an unknown algorithm; Verify recomputes under the
// stored algorithm; Fits is the settings' rule and its headroom warning.
func TestKID_ComputeVerifyAndFits(t *testing.T) {
	t.Parallel()
	got, err := kid.Compute(1001, 7, kid.Mod10)
	if err != nil || got != "0010017" {
		t.Errorf("Compute(1001, 7, mod10) = %q, %v; want 0010017", got, err)
	}
	if got, err := kid.Compute(1001, 7, kid.Mod11); err != nil || got != "0010014" {
		t.Errorf("Compute(1001, 7, mod11) = %q, %v; want 0010014", got, err)
	}
	if _, err := kid.Compute(1000000, 7, kid.Mod10); !errors.Is(err, kid.ErrLengthExceeded) {
		t.Errorf("Compute(1000000, 7) = %v, want ErrLengthExceeded", err)
	}
	if got, err := kid.Compute(999999, 7, kid.Mod10); err != nil || len(got) != 7 {
		t.Errorf("Compute(999999, 7) = %q, %v; want seven digits", got, err)
	}
	if _, err := kid.Compute(1, 7, "mod12"); err == nil || errors.Is(err, kid.ErrLengthExceeded) {
		t.Errorf("Compute with mod12 = %v, want an unknown-algorithm error", err)
	}
	if _, err := kid.Compute(0, 7, kid.Mod10); err == nil {
		t.Error("Compute(0, …) = nil error, want a refusal: invoice numbers start at 1")
	}

	for _, c := range []struct {
		kid, algorithm string
		number         int64
		want           bool
	}{
		{"0010017", kid.Mod10, 1001, true},
		{"0010017", kid.Mod11, 1001, false}, // MOD11 checks 1001 to 4
		{"0010014", kid.Mod11, 1001, true},
		{"0010018", kid.Mod10, 1001, false},
		{"0010017", kid.Mod10, 1002, false},
		{"010017", kid.Mod10, 1001, true}, // another agreed length, same number
		{"0010O17", kid.Mod10, 1001, false},
		{"123456785", kid.Mod11, 12345678, true},
		{"", kid.Mod10, 1, false},
		{"0010017", "", 1001, false},
	} {
		if got := kid.Verify(c.kid, c.algorithm, c.number); got != c.want {
			t.Errorf("Verify(%q, %q, %d) = %v, want %v", c.kid, c.algorithm, c.number, got, c.want)
		}
	}

	for _, c := range []struct {
		next             int64
		length           int
		fits, headroomLo bool
	}{
		{1, 4, true, false},     // 1 digit of 3: two to spare
		{10, 4, true, true},     // 2 of 3: one to spare
		{100, 4, true, true},    // 3 of 3: none to spare
		{1000, 4, false, false}, // 4 of 3
		{1001, 7, true, false},  // 4 of 6: two to spare
		{10001, 7, true, true},  // 5 of 6
		{1000000, 7, false, false},
	} {
		fits, low := kid.Fits(c.next, c.length)
		if fits != c.fits || low != c.headroomLo {
			t.Errorf("Fits(%d, %d) = %v, %v; want %v, %v", c.next, c.length, fits, low, c.fits, c.headroomLo)
		}
	}
}
