package secrets

import (
	"errors"
	"testing"
)

// TestSealBound_OpensOnlyUnderItsPurposeAndBound proves a bound value opens
// under the same purpose and bound alone: not under another bound, not under
// another purpose, and never through Open, nor a Seal through OpenBound.
func TestSealBound_OpensOnlyUnderItsPurposeAndBound(t *testing.T) {
	box, err := New(testAppSecret)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	sealed, err := box.SealBound("identity/idempotency", []byte("row-a"), []byte(`{"id":1}`))
	if err != nil {
		t.Fatalf("SealBound: %v", err)
	}
	got, err := box.OpenBound("identity/idempotency", []byte("row-a"), sealed)
	if err != nil || string(got) != `{"id":1}` {
		t.Fatalf("OpenBound = %q, %v; want the plaintext", got, err)
	}
	if _, err := box.OpenBound("identity/idempotency", []byte("row-b"), sealed); !errors.Is(err, ErrInvalid) {
		t.Errorf("OpenBound under another bound = %v, want ErrInvalid", err)
	}
	if _, err := box.OpenBound("identity/other", []byte("row-a"), sealed); !errors.Is(err, ErrInvalid) {
		t.Errorf("OpenBound under another purpose = %v, want ErrInvalid", err)
	}
	if _, err := box.Open("identity/idempotency", sealed); !errors.Is(err, ErrInvalid) {
		t.Errorf("Open of a bound value = %v, want ErrInvalid", err)
	}
	plain, err := box.Seal("identity/idempotency", []byte("x"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if _, err := box.OpenBound("identity/idempotency", nil, plain); !errors.Is(err, ErrInvalid) {
		t.Errorf("OpenBound of a Seal with an empty bound = %v, want ErrInvalid", err)
	}
}
