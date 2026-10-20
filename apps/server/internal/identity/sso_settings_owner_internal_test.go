package identity

import (
	"strings"
	"testing"
)

// A device inside the token directory is refused as no regular file, and
// the path is answered cleaned. The PUT's table cannot hold a device, since
// a test may make none, so the rule is held here, on the function itself.
func TestTokenFileProblem_ADeviceIsNoRegularFile(t *testing.T) {
	t.Parallel()
	clean, problem := tokenFileProblem("entra", "/dev//./zero", "/dev")
	if clean != "/dev/zero" || !strings.Contains(problem, "must be a regular file") {
		t.Errorf("tokenFileProblem(/dev//./zero in /dev) = %q, %q; want /dev/zero and a regular-file refusal", clean, problem)
	}
	// Outside the directory, nothing is asked of the filesystem: the
	// refusal names the directory, whatever the path leads to.
	if _, problem := tokenFileProblem("entra", "/dev/zero", "/var/run/secrets/azure/tokens"); !strings.Contains(problem, "must be a file in /var/run/secrets/azure/tokens") {
		t.Errorf("a device outside the token directory: %q", problem)
	}
}
