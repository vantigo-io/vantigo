package communications

import (
	"testing"

	"github.com/google/uuid"
)

// TestDeterministicGUID_ReproducesDotNetsByteOrder is the byte-order pin for
// ObjectOwnershipLifecycle.DeterministicGuid (`SV/ObjectOwnershipLifecycle.cs:32-36`,
// inventory §12.3). .NET hashes "{seed:N}:{discriminator}" with SHA-256, takes
// the first 16 bytes, and hands them to the Guid(ReadOnlySpan<byte>)
// constructor — which reads the first four bytes as a little-endian int32 and
// the next two pairs as little-endian int16s, leaving the last eight in order.
// A naive Go uuid.FromBytes over the same digest produces a DIFFERENT id with
// no error at all, which is why both the expected value and the naive value
// are asserted here: the test has to fail on the trap, not merely pass on the
// right answer.
//
// The expected strings were computed independently of this package (SHA-256 of
// the input, then .NET's field layout applied by hand), not by running the
// function under test.
func TestDeterministicGUID_ReproducesDotNetsByteOrder(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		seed          uuid.UUID
		discriminator string
		want          string
		naive         string // what uuid.FromBytes(digest[:16]) would have produced
	}{
		{
			name:          "cleanup record id",
			seed:          uuid.Nil,
			discriminator: "cleanup:test-key",
			want:          "8d5fa632-b971-11bb-4847-9f68fef38e00",
			naive:         "32a65f8d-71b9-bb11-4847-9f68fef38e00",
		},
		{
			name:          "staged attachment key",
			seed:          uuid.Nil,
			discriminator: "cleanup:staged-attachments/0123456789abcdef0123456789abcdef/fedcba9876543210fedcba9876543210/00112233445566778899aabbccddeeff",
			want:          "a4a90dc6-507d-c732-6860-1313eca8ebab",
			naive:         "c60da9a4-7d50-32c7-6860-1313eca8ebab",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := deterministicGUID(tt.seed, tt.discriminator).String()
			if got != tt.want {
				t.Errorf("deterministicGUID = %s, want %s (.NET's Guid(byte[]) byte order)", got, tt.want)
			}
			if got == tt.naive {
				t.Errorf("deterministicGUID = %s, which is the naive uuid.FromBytes result: the first eight bytes must be byte-swapped", got)
			}
		})
	}
}

// TestDeterministicGUID_IsStableAndDiscriminating pins the two properties the
// id is actually used for: the same inputs always produce the same id (the
// crash-retry identity), and a different discriminator produces a different
// one.
func TestDeterministicGUID_IsStableAndDiscriminating(t *testing.T) {
	t.Parallel()
	a := deterministicGUID(uuid.Nil, "cleanup:one")
	if again := deterministicGUID(uuid.Nil, "cleanup:one"); again != a {
		t.Errorf("deterministicGUID is not stable: %s then %s", a, again)
	}
	if other := deterministicGUID(uuid.Nil, "cleanup:two"); other == a {
		t.Error("two discriminators produced the same id")
	}
	seeded := uuid.MustParse("11111111-2222-3333-4444-555555555555")
	if withSeed := deterministicGUID(seeded, "cleanup:one"); withSeed == a {
		t.Error("two seeds produced the same id")
	}
}

// TestIsSafeRelativeKey is ObjectOwnershipLifecycle.IsSafeRelativeKey
// (`:24-30`), which every reserve, mark, release and queue call runs first.
// It is deliberately a different (looser) rule set from internal/storage's own
// key validation — inventory §16.1 notes both run, on different call paths —
// so it is ported as written rather than folded into the storage one.
func TestIsSafeRelativeKey(t *testing.T) {
	t.Parallel()
	safe := []string{
		"staged-attachments/abc/def",
		"a",
		"inbound/0123/4567.eml",
		"with space/and.dots",
	}
	for _, key := range safe {
		if !isSafeRelativeKey(key) {
			t.Errorf("isSafeRelativeKey(%q) = false, want true", key)
		}
	}
	unsafe := []string{
		"",
		"   ",
		"/leading-slash",
		"back\\slash",
		"control\x00char",
		"a//b",
		"a/./b",
		"a/../b",
		"..",
		".",
		"trailing/",
	}
	for _, key := range unsafe {
		if isSafeRelativeKey(key) {
			t.Errorf("isSafeRelativeKey(%q) = true, want false", key)
		}
	}
}
