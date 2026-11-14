package saft

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
)

// Xmllint validates doc against the XSD at xsd with xmllint, for the tests:
// ran is false when xmllint is not on the PATH, which a test says in its log
// (CI's oracle, tools/saft, validates the same files with the JDK). The
// network is never used.
func Xmllint(doc []byte, xsd string) (ran bool, err error) {
	if _, err := exec.LookPath("xmllint"); err != nil {
		return false, nil
	}
	abs, err := filepath.Abs(xsd)
	if err != nil {
		return true, err
	}
	cmd := exec.Command("xmllint", "--noout", "--nonet", "--schema", abs, "-")
	cmd.Stdin = bytes.NewReader(doc)
	if out, err := cmd.CombinedOutput(); err != nil {
		return true, fmt.Errorf("xmllint: %w\n%s", err, out)
	}
	return true, nil
}
