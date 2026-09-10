package buildinfo

import "testing"

// An unstamped build (go test, go run) must say "dev" so a locally built
// binary can never be mistaken for a release in a log or health response.
func TestVersionDefaultsToDev(t *testing.T) {
	if Version != "dev" {
		t.Fatalf("Version = %q, want %q", Version, "dev")
	}
}
