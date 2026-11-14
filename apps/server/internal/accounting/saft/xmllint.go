package saft

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Xmllint validates doc against the XSD at xsd with xmllint, for the tests:
// ran is false when xmllint is not on the PATH, which a test says in its log
// — but with CI set, where the test job installs it, a missing xmllint is an
// error, so no file goes unvalidated there. The network is never used.
func Xmllint(doc []byte, xsd string) (ran bool, err error) {
	if _, err := exec.LookPath("xmllint"); err != nil {
		if os.Getenv("CI") != "" {
			return false, errors.New("xmllint is not on the PATH in CI: the file is not validated")
		}
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
